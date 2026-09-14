package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"

	"github.com/han/qrush/internal/config"
	"github.com/han/qrush/internal/ipc"
	"github.com/han/qrush/internal/protocol"
)

type Server struct {
	listener     ipc.Listener
	jobs         *JobQueue
	scheduler    *Scheduler
	executor     *Executor
	terminals    *TerminalManager
	envStore     *EnvStore
	cfg          *config.Config
	done         chan struct{}
	shutdownOnce sync.Once
	activeConns  atomic.Int64
	maxConns     int
	maxFinished  int
	mu           sync.Mutex
	// pendingJobsView is set by MsgRequestJobsView and read-and-cleared by the
	// next tree poll, signalling an attached TUI to open its jobs view.
	pendingJobsView bool
	// tuiConn is the single registered interactive TUI (MsgTUIAttach). A new
	// registration displaces the old one so two TUIs never mirror the same
	// panes (they would fight over PTY sizes and stomp each other's layouts).
	tuiConn net.Conn
}

func New(cfg *config.Config) (*Server, error) {
	jobs, err := loadJobQueue(cfg.SaveList)
	if err != nil {
		return nil, err
	}
	path := ipc.SocketPath()
	listener, err := ipc.NewListener(path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}

	logDir := cfg.Logdir
	if logDir == "" {
		logDir = cfg.TmpDir
	}
	executor := NewExecutor(logDir)
	executor.SetOnFinishCommand(cfg.OnFinish)
	scheduler := NewScheduler(jobs, executor, cfg.Slots)
	terminals := NewTerminalManager()

	srv := &Server{
		listener:    listener,
		jobs:        jobs,
		scheduler:   scheduler,
		executor:    executor,
		terminals:   terminals,
		envStore:    NewEnvStore(),
		cfg:         cfg,
		done:        make(chan struct{}),
		maxConns:    cfg.MaxConn,
		maxFinished: cfg.MaxFinished,
	}

	scheduler.SetOnStateChangeHook(srv.queueChanged)

	return srv, nil
}

func (s *Server) Run(ctx context.Context) error {
	go s.scheduler.Run(ctx)

	go func() {
		select {
		case <-ctx.Done():
			s.listener.Close()
		case <-s.done:
		}
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			case <-ctx.Done():
				return nil
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}

		if s.maxConns > 0 && int(s.activeConns.Load()) >= s.maxConns {
			// Tell the client why instead of silently closing (which surfaced
			// as a baffling EOF). Sent from a goroutine so a stalled client
			// can't block the accept loop.
			go func(c net.Conn) {
				_ = protocol.Send(c, &protocol.Msg{
					Type:    protocol.MsgError,
					Payload: protocol.PayloadError{Message: fmt.Sprintf("too many connections (max %d)", s.maxConns)},
				})
				c.Close()
			}(conn)
			continue
		}

		s.activeConns.Add(1)
		go func() {
			defer s.activeConns.Add(-1)
			s.handleConnection(ctx, conn)
		}()
	}
}

func (s *Server) Shutdown() {
	s.shutdownOnce.Do(func() {
		close(s.done)
		s.executor.KillAll()
		s.terminals.KillAll()
		s.persistQueue()
		s.listener.Close()
	})
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	for {
		msg, err := protocol.Recv(conn)
		if err != nil {
			if err != io.EOF {
				// client disconnected or malformed message
			}
			return
		}
		if !s.dispatch(ctx, conn, msg) {
			return
		}
	}
}

func (s *Server) sendMsg(conn net.Conn, msg *protocol.Msg) bool {
	if err := protocol.Send(conn, msg); err != nil {
		return false
	}
	return true
}

func (s *Server) sendError(conn net.Conn, message string) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgError,
		Payload: protocol.PayloadError{Message: message},
	})
}

func (s *Server) dispatch(ctx context.Context, conn net.Conn, msg *protocol.Msg) bool {
	defer s.persistQueue()
	switch msg.Type {
	case protocol.MsgNewJob:
		return s.handleNewJob(conn, msg)
	case protocol.MsgRerun:
		return s.handleRerun(conn, msg)
	case protocol.MsgRequestJobsView:
		return s.handleRequestJobsView(conn)
	case protocol.MsgList:
		return s.handleList(conn)
	case protocol.MsgKillServer:
		s.Shutdown()
		return false
	case protocol.MsgGetVersion:
		return s.handleGetVersion(conn)
	case protocol.MsgClearFinished:
		s.jobs.ClearFinished()
		return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
	case protocol.MsgRemoveJob:
		return s.handleRemoveJob(conn, msg)
	case protocol.MsgGetState:
		return s.handleGetState(conn, msg)
	case protocol.MsgAskOutput:
		return s.handleAskOutput(conn, msg)
	case protocol.MsgInfo:
		return s.handleInfo(conn, msg)
	case protocol.MsgSetMaxSlots:
		return s.handleSetMaxSlots(conn, msg)
	case protocol.MsgGetMaxSlots:
		return s.handleGetMaxSlots(conn)
	case protocol.MsgUrgent:
		return s.handleUrgent(conn, msg)
	case protocol.MsgSwapJobs:
		return s.handleSwapJobs(conn, msg)
	case protocol.MsgWaitJob:
		return s.handleWaitJob(ctx, conn, msg)
	case protocol.MsgKillJob:
		return s.handleKillJob(conn, msg)
	case protocol.MsgKillAll:
		s.executor.KillAll()
		return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
	case protocol.MsgCountRunning:
		return s.handleCountRunning(conn)
	case protocol.MsgGetLabel:
		return s.handleGetLabel(conn, msg)
	case protocol.MsgSetJobLabel:
		return s.handleSetJobLabel(conn, msg)
	case protocol.MsgSetJobTimeout:
		return s.handleSetJobTimeout(conn, msg)
	case protocol.MsgLastID:
		return s.handleLastID(conn)
	case protocol.MsgGetCmd:
		return s.handleGetCmd(conn, msg)
	case protocol.MsgGetEnv:
		return s.handleGetEnv(conn, msg)
	case protocol.MsgSetEnv:
		return s.handleSetEnv(conn, msg)
	case protocol.MsgUnsetEnv:
		return s.handleUnsetEnv(conn, msg)
	case protocol.MsgGetLogdir:
		return s.handleGetLogdir(conn)
	case protocol.MsgSetLogdir:
		return s.handleSetLogdir(conn, msg)
	case protocol.MsgSessionList:
		return s.handleSessionList(conn)
	case protocol.MsgSessionCreate:
		return s.handleSessionCreate(conn, msg)
	case protocol.MsgSessionRename:
		return s.handleSessionRename(conn, msg)
	case protocol.MsgSessionDelete:
		return s.handleSessionDelete(conn, msg)
	case protocol.MsgListSession:
		return s.handleListSession(conn, msg)
	case protocol.MsgClearFinishedSession:
		return s.handleClearFinishedSession(conn, msg)
	case protocol.MsgTreeList:
		return s.handleTreeList(conn)
	case protocol.MsgGroupList:
		return s.handleGroupList(conn)
	case protocol.MsgGroupCreate:
		return s.handleGroupCreate(conn, msg)
	case protocol.MsgGroupRename:
		return s.handleGroupRename(conn, msg)
	case protocol.MsgGroupDelete:
		return s.handleGroupDelete(conn, msg)
	case protocol.MsgSessionMove:
		return s.handleSessionMove(conn, msg)
	case protocol.MsgTerminalAttach:
		return s.handleTerminalAttach(conn, msg)
	case protocol.MsgTerminalKill:
		return s.handleTerminalKill(conn, msg)
	case protocol.MsgTerminalOpen:
		return s.handleTerminalOpen(conn, msg)
	case protocol.MsgTerminalGetLayout:
		return s.handleTerminalGetLayout(conn, msg)
	case protocol.MsgTerminalSetLayout:
		return s.handleTerminalSetLayout(conn, msg)
	case protocol.MsgTerminalListAll:
		return s.handleTerminalListAll(conn)
	case protocol.MsgTUIAttach:
		return s.handleTUIAttach(conn)
	case protocol.MsgReset:
		return s.handleReset(conn)
	default:
		s.sendError(conn, "unknown message type")
		return true
	}
}

// handleTUIAttach registers conn as the one active interactive TUI and blocks
// for its lifetime. Any previously registered TUI is told to quit and cut off,
// so at most one TUI drives the daemon's panes at a time.
func (s *Server) handleTUIAttach(conn net.Conn) bool {
	// Long-lived connection — exempt from maxConns, like terminal attaches.
	s.activeConns.Add(-1)
	defer s.activeConns.Add(1)

	s.mu.Lock()
	old := s.tuiConn
	s.tuiConn = conn
	s.mu.Unlock()
	if old != nil {
		_ = protocol.Send(old, &protocol.Msg{Type: protocol.MsgTUITakenOver})
		old.Close()
	}
	// Ack the registration so the client knows it is the active TUI before it
	// proceeds (without this, two near-simultaneous attaches can race).
	if !s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK}) {
		return false
	}

	// Park until the TUI exits (its side closes) or a newer TUI displaces us.
	for {
		if _, err := protocol.Recv(conn); err != nil {
			break
		}
	}

	s.mu.Lock()
	if s.tuiConn == conn {
		s.tuiConn = nil
	}
	s.mu.Unlock()
	return false
}

// handleReset restores the daemon to a fresh state: every running job and
// pane is killed, all jobs/sessions/groups dropped (defaults kept), and the
// runtime settings return to their defaults (persisted overrides cleared).
func (s *Server) handleReset(conn net.Conn) bool {
	s.executor.KillAll()
	s.terminals.Reset()
	s.jobs.Reset()
	s.scheduler.SetMaxSlots(1)
	s.executor.SetLogDir(s.cfg.TmpDir)
	if err := config.DeleteRuntime("slots"); err != nil {
		log.Printf("reset: clear slots override: %v", err)
	}
	if err := config.DeleteRuntime("logdir"); err != nil {
		log.Printf("reset: clear logdir override: %v", err)
	}
	log.Printf("daemon reset to defaults")
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) pruneFinished() {
	if s.maxFinished < 0 {
		return
	}
	s.jobs.PruneFinished(s.maxFinished)
}

func (s *Server) queueChanged() {
	s.pruneFinished()
	s.persistQueue()
}

func (s *Server) persistQueue() {
	if err := s.jobs.save(s.cfg.SaveList); err != nil {
		log.Printf("persist queue: %v", err)
	}
}
