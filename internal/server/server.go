package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
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
}

func New(cfg *config.Config) (*Server, error) {
	if cfg.SaveList != "" {
		return nil, fmt.Errorf("QRUSH_SAVELIST is not implemented")
	}
	path := ipc.SocketPath()
	listener, err := ipc.NewListener(path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}

	logDir := cfg.TmpDir
	executor := NewExecutor(logDir)
	executor.SetOnFinishCommand(cfg.OnFinish)
	jobs := NewJobQueue()
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

	scheduler.SetOnFinishHook(srv.pruneFinished)

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
			conn.Close()
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
	default:
		s.sendError(conn, "unknown message type")
		return true
	}
}

func (s *Server) handleNewJob(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadNewJob](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	req := payload.Request
	req.Environment = s.envStore.Apply(req.Environment)
	job := s.jobs.Add(req)
	if req.StoreOutput {
		s.jobs.SetOutputFilename(job.ID, s.executor.OutputPathFor(job.ID, req.Logfile))
	}

	ok := s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgNewJobOK,
		Payload: protocol.PayloadJobID{JobID: job.ID},
	})

	s.scheduler.Poke()
	return ok
}

func (s *Server) handleRerun(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	req, ok := s.jobs.RerunRequest(payload.JobID)
	if !ok {
		return s.sendError(conn, fmt.Sprintf("job %d not found", payload.JobID))
	}
	job := s.jobs.Add(req)
	if req.StoreOutput {
		s.jobs.SetOutputFilename(job.ID, s.executor.OutputPathFor(job.ID, req.Logfile))
	}
	sent := s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgNewJobOK,
		Payload: protocol.PayloadJobID{JobID: job.ID},
	})
	s.scheduler.Poke()
	return sent
}

func (s *Server) handleList(conn net.Conn) bool {
	jobs := s.jobs.AllInfo()
	for _, info := range jobs {
		if !s.sendMsg(conn, &protocol.Msg{
			Type:    protocol.MsgListLine,
			Payload: protocol.PayloadListLine{Job: info},
		}) {
			return false
		}
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgListEnd,
		Payload: protocol.PayloadSlots{Slots: s.scheduler.MaxSlots()},
	})
}

func (s *Server) handleGetVersion(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgVersion,
		Payload: protocol.PayloadVersion{Version: protocol.ProtocolVersion},
	})
}

func (s *Server) handleRemoveJob(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if s.jobs.Remove(payload.JobID) {
		return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgRemoveJobOK})
	}
	return s.sendError(conn, "cannot remove job")
}

func (s *Server) handleGetState(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerState,
		Payload: protocol.PayloadState{State: info.State},
	})
}

func (s *Server) handleAskOutput(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type: protocol.MsgAnswerOutput,
		Payload: protocol.PayloadOutput{
			Filename: info.OutputFilename,
			PID:      info.PID,
		},
	})
}

func (s *Server) handleInfo(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgInfoData,
		Payload: protocol.PayloadInfo{Job: info},
	})
}

func (s *Server) handleSetMaxSlots(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSlots](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Slots < 1 {
		return s.sendError(conn, "slots must be greater than zero")
	}
	s.scheduler.SetMaxSlots(payload.Slots)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleGetMaxSlots(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgGetMaxSlotsOK,
		Payload: protocol.PayloadSlots{Slots: s.scheduler.MaxSlots()},
	})
}

func (s *Server) handleUrgent(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.MakeUrgent(payload.JobID) {
		return s.sendError(conn, "cannot make job urgent")
	}
	s.scheduler.Poke()
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgUrgentOK})
}

func (s *Server) handleSwapJobs(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSwap](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.Swap(payload.ID1, payload.ID2) {
		return s.sendError(conn, "cannot swap jobs")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSwapJobsOK})
}

func (s *Server) handleWaitJob(ctx context.Context, conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	ch := s.jobs.WaitFor(payload.JobID)

	select {
	case result := <-ch:
		return s.sendMsg(conn, &protocol.Msg{
			Type:    protocol.MsgWaitJobOK,
			Payload: protocol.PayloadResult{Result: result},
		})
	case <-ctx.Done():
		return false
	}
}

func (s *Server) handleKillJob(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if err := s.executor.Kill(payload.JobID); err != nil {
		return s.sendError(conn, err.Error())
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleCountRunning(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgCountRunningOK,
		Payload: protocol.PayloadCount{Count: s.jobs.RunningCount()},
	})
}

func (s *Server) handleGetLabel(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerLabel,
		Payload: protocol.PayloadLabel{Label: info.Label},
	})
}

func (s *Server) handleLastID(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgLastIDOK,
		Payload: protocol.PayloadJobID{JobID: s.jobs.LastID()},
	})
}

func (s *Server) handleGetCmd(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerCmd,
		Payload: protocol.PayloadCmd{Cmd: info.Command},
	})
}

func (s *Server) handleGetEnv(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadEnv](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	val, ok := s.envStore.Get(payload.Key)
	if !ok {
		val = os.Getenv(payload.Key)
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerEnv,
		Payload: protocol.PayloadEnv{Key: payload.Key, Value: val},
	})
}

func (s *Server) handleSetEnv(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadEnv](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Key == "" {
		return s.sendError(conn, "environment key is required")
	}
	s.envStore.Set(payload.Key, payload.Value)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleUnsetEnv(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadEnv](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Key == "" {
		return s.sendError(conn, "environment key is required")
	}
	s.envStore.Unset(payload.Key)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleGetLogdir(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerLogdir,
		Payload: protocol.PayloadLogdir{Path: s.executor.LogDir()},
	})
}

func (s *Server) handleSetLogdir(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadLogdir](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Path == "" {
		return s.sendError(conn, "log directory is required")
	}
	s.executor.SetLogDir(payload.Path)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleSessionList(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgSessionListOK,
		Payload: protocol.PayloadSessionList{Sessions: s.jobs.AllSessions()},
	})
}

func (s *Server) handleSessionCreate(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.CreateSession(payload.Name) {
		return s.sendError(conn, "session already exists")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionCreateOK})
}

func (s *Server) handleSessionRename(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSessionRename](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.RenameSession(payload.OldName, payload.NewName) {
		return s.sendError(conn, "cannot rename session")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionRenameOK})
}

func (s *Server) handleSessionDelete(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	ok, reason := s.jobs.DeleteSession(payload.Name)
	if !ok {
		return s.sendError(conn, reason)
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionDeleteOK})
}

func (s *Server) handleListSession(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	jobs := s.jobs.AllInfoBySession(payload.Name)
	for _, info := range jobs {
		if !s.sendMsg(conn, &protocol.Msg{
			Type:    protocol.MsgListLine,
			Payload: protocol.PayloadListLine{Job: info},
		}) {
			return false
		}
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgListEnd,
		Payload: protocol.PayloadSlots{Slots: s.scheduler.MaxSlots()},
	})
}

func (s *Server) handleClearFinishedSession(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	s.jobs.ClearFinishedInSession(payload.Name)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleTreeList(conn net.Conn) bool {
	s.mu.Lock()
	openJobsView := s.pendingJobsView
	s.pendingJobsView = false
	s.mu.Unlock()
	return s.sendMsg(conn, &protocol.Msg{
		Type: protocol.MsgTreeListOK,
		Payload: protocol.PayloadTreeData{
			Groups:       s.jobs.AllGroups(),
			Sessions:     s.jobs.AllSessionInfo(),
			Jobs:         s.jobs.AllInfo(),
			MaxSlots:     s.scheduler.MaxSlots(),
			OpenJobsView: openJobsView,
		},
	})
}

func (s *Server) handleRequestJobsView(conn net.Conn) bool {
	s.mu.Lock()
	s.pendingJobsView = true
	s.mu.Unlock()
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgRequestJobsViewOK})
}

func (s *Server) handleGroupList(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgGroupListOK,
		Payload: protocol.PayloadGroupList{Groups: s.jobs.AllGroups()},
	})
}

func (s *Server) handleGroupCreate(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.CreateGroup(payload.Name) {
		return s.sendError(conn, "cannot create group")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgGroupCreateOK})
}

func (s *Server) handleGroupRename(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSessionRename](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.RenameGroup(payload.OldName, payload.NewName) {
		return s.sendError(conn, "cannot rename group")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgGroupRenameOK})
}

func (s *Server) handleGroupDelete(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	ok, reason := s.jobs.DeleteGroup(payload.Name)
	if !ok {
		return s.sendError(conn, reason)
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgGroupDeleteOK})
}

func (s *Server) handleSessionMove(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSessionMove](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.MoveSession(payload.Session, payload.Group) {
		return s.sendError(conn, "cannot move session")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionMoveOK})
}

func (s *Server) pruneFinished() {
	if s.maxFinished < 0 {
		return
	}
	s.jobs.PruneFinished(s.maxFinished)
}
