package server

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/han/qrush/internal/protocol"
)

const (
	terminalBacklogLimit = 1024 * 1024
	terminalClientBuffer = 256
)

// byteRing is a fixed-capacity ring buffer of raw terminal output. Writes never
// allocate after construction; Bytes() returns the retained tail in order. It
// replaces the previous "re-slice the whole backlog on every read" approach,
// which allocated ~1MB per 32KB read on busy terminals.
type byteRing struct {
	buf  []byte
	w    int
	full bool
}

func newByteRing(size int) *byteRing { return &byteRing{buf: make([]byte, size)} }

func (r *byteRing) write(p []byte) {
	size := len(r.buf)
	if size == 0 {
		return
	}
	if len(p) >= size {
		copy(r.buf, p[len(p)-size:])
		r.w = 0
		r.full = true
		return
	}
	end := r.w + len(p)
	if end <= size {
		copy(r.buf[r.w:], p)
	} else {
		k := size - r.w
		copy(r.buf[r.w:], p[:k])
		copy(r.buf, p[k:])
		r.full = true
	}
	if end >= size {
		r.full = true
	}
	r.w = end % size
}

func (r *byteRing) bytes() []byte {
	if !r.full {
		return append([]byte(nil), r.buf[:r.w]...)
	}
	out := make([]byte, len(r.buf))
	n := copy(out, r.buf[r.w:])
	copy(out[n:], r.buf[:r.w])
	return out
}

type TerminalManager struct {
	mu       sync.Mutex
	sessions map[string]*TerminalPTY
	layouts  map[string][]byte // session -> opaque client layout blob
	nextName uint64            // monotonic source of unique pane names
}

type TerminalPTY struct {
	session string
	pane    string
	ptmx    *os.File
	cmd     *exec.Cmd
	rcDir   string

	mu      sync.Mutex
	clients map[chan []byte]bool
	backlog *byteRing
	done    bool
}

func NewTerminalManager() *TerminalManager {
	return &TerminalManager{
		sessions: make(map[string]*TerminalPTY),
		layouts:  make(map[string][]byte),
	}
}

func terminalKey(session, pane string) string {
	if session == "" {
		session = "default"
	}
	if pane == "" {
		pane = "main"
	}
	return session + "\x00" + pane
}

func (m *TerminalManager) GetOrCreate(session, pane string, cols, rows int) (*TerminalPTY, error) {
	key := terminalKey(session, pane)
	m.mu.Lock()
	if t := m.sessions[key]; t != nil && !t.isDone() {
		m.mu.Unlock()
		t.Resize(cols, rows)
		return t, nil
	}
	delete(m.sessions, key)
	m.mu.Unlock()

	t, err := startTerminalPTY(session, pane, cols, rows)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing := m.sessions[key]; existing != nil && !existing.isDone() {
		m.mu.Unlock()
		t.discard() // lost the create race: kill AND reap (no readLoop will)
		existing.Resize(cols, rows)
		return existing, nil
	}
	m.sessions[key] = t
	m.mu.Unlock()

	go func() {
		t.readLoop()
		m.mu.Lock()
		if m.sessions[key] == t {
			delete(m.sessions, key)
		}
		m.mu.Unlock()
	}()
	return t, nil
}

// ListAll returns every live pane across all sessions.
func (m *TerminalManager) ListAll() []protocol.TerminalInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []protocol.TerminalInfo
	for _, t := range m.sessions {
		if !t.isDone() {
			out = append(out, protocol.TerminalInfo{Session: t.session, Pane: t.pane})
		}
	}
	return out
}

func (m *TerminalManager) Kill(session, pane string) {
	key := terminalKey(session, pane)
	m.mu.Lock()
	t := m.sessions[key]
	delete(m.sessions, key)
	m.mu.Unlock()
	if t != nil {
		t.Kill()
	}
}

func (m *TerminalManager) KillAll() {
	m.mu.Lock()
	terms := make([]*TerminalPTY, 0, len(m.sessions))
	for _, t := range m.sessions {
		terms = append(terms, t)
	}
	m.sessions = make(map[string]*TerminalPTY)
	m.mu.Unlock()
	for _, t := range terms {
		t.Kill()
	}
}

func startTerminalPTY(session, pane string, cols, rows int) (*TerminalPTY, error) {
	if session == "" {
		session = "default"
	}
	if pane == "" {
		pane = "main"
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd, rcDir := terminalShellCommand(shell)
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "QRUSH_SESSION="+session)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		if rcDir != "" {
			os.RemoveAll(rcDir)
		}
		return nil, err
	}
	return &TerminalPTY{
		session: session,
		pane:    pane,
		ptmx:    ptmx,
		cmd:     cmd,
		rcDir:   rcDir,
		clients: make(map[chan []byte]bool),
		backlog: newByteRing(terminalBacklogLimit),
	}, nil
}

// Open creates a fresh pane in a session with a unique, restart-stable name
// (server-assigned, never recycled within a daemon lifetime) and returns it.
func (m *TerminalManager) Open(session string, cols, rows int) (string, error) {
	m.mu.Lock()
	m.nextName++
	pane := fmt.Sprintf("p%d", m.nextName)
	m.mu.Unlock()
	if _, err := m.GetOrCreate(session, pane, cols, rows); err != nil {
		return "", err
	}
	return pane, nil
}

// ListLayout returns a session's persisted layout blob plus the names of its
// panes whose PTYs are still alive.
func (m *TerminalManager) ListLayout(session string) ([]byte, []string) {
	if session == "" {
		session = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	blob := append([]byte(nil), m.layouts[session]...)
	var alive []string
	for _, t := range m.sessions {
		if t.session == session && !t.isDone() {
			alive = append(alive, t.pane)
		}
	}
	return blob, alive
}

// SetLayout persists a session's layout blob and reaps any pane in that session
// not present in keep (panes the client closed or dropped from the layout).
func (m *TerminalManager) SetLayout(session string, blob []byte, keep []string) {
	if session == "" {
		session = "default"
	}
	keepSet := make(map[string]bool, len(keep))
	for _, p := range keep {
		keepSet[p] = true
	}
	m.mu.Lock()
	m.layouts[session] = append([]byte(nil), blob...)
	var doomed []*TerminalPTY
	for key, t := range m.sessions {
		if t.session == session && !keepSet[t.pane] {
			doomed = append(doomed, t)
			delete(m.sessions, key)
		}
	}
	m.mu.Unlock()
	for _, t := range doomed {
		t.Kill()
	}
}

// RenameLayout migrates a session's persisted layout blob to a new name so the
// old key is not stranded in memory after a session rename. Live panes keep
// their existing session name (unchanged behavior); only the blob is moved.
func (m *TerminalManager) RenameLayout(oldName, newName string) {
	if oldName == "" {
		oldName = "default"
	}
	if newName == "" {
		newName = "default"
	}
	if oldName == newName {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if blob, ok := m.layouts[oldName]; ok {
		m.layouts[newName] = blob
		delete(m.layouts, oldName)
	}
}

// DropSession removes all persisted state for a session: it deletes the layout
// blob and kills any live panes. Used when a session is deleted outright.
func (m *TerminalManager) DropSession(session string) {
	if session == "" {
		session = "default"
	}
	m.mu.Lock()
	delete(m.layouts, session)
	var doomed []*TerminalPTY
	for key, t := range m.sessions {
		if t.session == session {
			doomed = append(doomed, t)
			delete(m.sessions, key)
		}
	}
	m.mu.Unlock()
	for _, t := range doomed {
		t.Kill()
	}
}

func terminalShellCommand(shell string) (*exec.Cmd, string) {
	name := filepath.Base(shell)
	switch name {
	case "zsh":
		if rcDir, err := setupTerminalZshRC(); err == nil {
			cmd := exec.Command(shell)
			cmd.Env = append(os.Environ(), "ZDOTDIR="+rcDir, "QRUSH_ORIG_ZDOTDIR="+origZDOTDIR())
			return cmd, rcDir
		}
	case "bash":
		if rcDir, rcFile, err := setupTerminalBashRC(); err == nil {
			return exec.Command(shell, "--rcfile", rcFile, "-i"), rcDir
		}
	}
	return exec.Command(shell), ""
}

func setupTerminalZshRC() (string, error) {
	rcDir, err := os.MkdirTemp("", "qrush-zsh-*")
	if err != nil {
		return "", err
	}
	rc := `if [ -n "$QRUSH_ORIG_ZDOTDIR" ] && [ -r "$QRUSH_ORIG_ZDOTDIR/.zshrc" ]; then
  source "$QRUSH_ORIG_ZDOTDIR/.zshrc"
elif [ -r "$HOME/.zshrc" ]; then
  source "$HOME/.zshrc"
fi
function __qrush_prompt_marker() {
  printf '\033]133;A\a'
  printf '\033]7;file://%s%s\a' "${HOST}" "${PWD}"
}
autoload -Uz add-zsh-hook
add-zsh-hook precmd __qrush_prompt_marker
`
	if err := os.WriteFile(filepath.Join(rcDir, ".zshrc"), []byte(rc), 0600); err != nil {
		os.RemoveAll(rcDir)
		return "", err
	}
	return rcDir, nil
}

func setupTerminalBashRC() (string, string, error) {
	rcDir, err := os.MkdirTemp("", "qrush-bash-*")
	if err != nil {
		return "", "", err
	}
	rcFile := filepath.Join(rcDir, "bashrc")
	rc := `if [ -r "$HOME/.bashrc" ]; then
  source "$HOME/.bashrc"
fi
__qrush_prompt_marker() {
  printf '\033]133;A\a'
  printf '\033]7;file://%s%s\a' "${HOSTNAME}" "${PWD}"
}
if [ -n "$PROMPT_COMMAND" ]; then
  PROMPT_COMMAND="__qrush_prompt_marker; $PROMPT_COMMAND"
else
  PROMPT_COMMAND="__qrush_prompt_marker"
fi
`
	if err := os.WriteFile(rcFile, []byte(rc), 0600); err != nil {
		os.RemoveAll(rcDir)
		return "", "", err
	}
	return rcDir, rcFile, nil
}

func origZDOTDIR() string {
	if zdotdir := os.Getenv("ZDOTDIR"); zdotdir != "" {
		return zdotdir
	}
	return os.Getenv("HOME")
}

func (t *TerminalPTY) isDone() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

func (t *TerminalPTY) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := t.ptmx.Read(buf)
		if n > 0 {
			t.broadcast(buf[:n])
		}
		if err != nil {
			t.finish()
			return
		}
	}
}

func (t *TerminalPTY) broadcast(data []byte) {
	cp := append([]byte(nil), data...)
	t.mu.Lock()
	t.backlog.write(cp)
	for ch := range t.clients {
		select {
		case ch <- cp:
		default:
			// Subscriber is too far behind to keep up (deeply buffered, so this
			// only happens to a genuinely wedged client). Disconnect it rather
			// than block the PTY reader or silently drop a chunk and corrupt
			// its view: closing the channel ends its attach handler, the client
			// sees the drop and reattaches, replaying the backlog to resync.
			delete(t.clients, ch)
			close(ch)
		}
	}
	t.mu.Unlock()
}

func (t *TerminalPTY) finish() {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.done = true
	for ch := range t.clients {
		close(ch)
	}
	t.clients = nil
	t.mu.Unlock()
	if t.rcDir != "" {
		os.RemoveAll(t.rcDir)
	}
	t.waitProcess()
}

// waitProcess reaps the child, escalating to SIGKILL if it ignores the earlier
// SIGTERM. Without the bound, a shell that traps SIGTERM would block this
// goroutine (and the registry's pane-removal goroutine) forever.
func (t *TerminalPTY) waitProcess() {
	if t.cmd == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = t.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		if t.cmd.Process != nil {
			_ = forceKillProcessGroup(t.cmd.Process.Pid)
		}
		<-done
	}
}

func (t *TerminalPTY) Attach() (chan []byte, []byte) {
	ch := make(chan []byte, terminalClientBuffer)
	t.mu.Lock()
	backlog := t.backlog.bytes()
	if t.done {
		close(ch)
	} else {
		t.clients[ch] = true
	}
	t.mu.Unlock()
	return ch, backlog
}

func (t *TerminalPTY) Detach(ch chan []byte) {
	t.mu.Lock()
	if t.clients != nil {
		delete(t.clients, ch)
	}
	t.mu.Unlock()
}

func (t *TerminalPTY) Write(data []byte) {
	if len(data) == 0 {
		return
	}
	_, _ = t.ptmx.Write(data)
}

func (t *TerminalPTY) Resize(cols, rows int) {
	if cols < 10 {
		cols = 10
	}
	if rows < 2 {
		rows = 2
	}
	_ = pty.Setsize(t.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (t *TerminalPTY) Kill() {
	if t.cmd != nil && t.cmd.Process != nil {
		_ = killProcessGroup(t.cmd.Process.Pid)
	}
	if t.ptmx != nil {
		_ = t.ptmx.Close()
	}
}

// discard tears down a terminal that was created but never registered (lost the
// create race in GetOrCreate). Because no readLoop runs for it, finish() will
// never reap the child, so we kill, reap, and clean up synchronously here.
func (t *TerminalPTY) discard() {
	t.Kill()
	t.waitProcess()
	if t.rcDir != "" {
		os.RemoveAll(t.rcDir)
	}
}

func (s *Server) handleTerminalAttach(conn net.Conn, msg *protocol.Msg) bool {
	// An attach connection stays open for the pane's whole lifetime. Don't let
	// it count against maxConns, or a few panes plus the TUI's periodic tree
	// polls would exhaust the cap and get silently dropped. Balance the
	// accept-loop's deferred Add(-1) by releasing our slot now and reclaiming
	// it on return.
	s.activeConns.Add(-1)
	defer s.activeConns.Add(1)

	payload, err := protocol.PayloadAs[protocol.PayloadTerminalAttach](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	term, err := s.terminals.GetOrCreate(payload.Session, payload.Pane, payload.Cols, payload.Rows)
	if err != nil {
		return s.sendError(conn, fmt.Sprintf("terminal: %v", err))
	}
	ch, backlog := term.Attach()
	defer term.Detach(ch)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			in, err := protocol.Recv(conn)
			if err != nil {
				return
			}
			switch in.Type {
			case protocol.MsgTerminalInput:
				p, pErr := protocol.PayloadAs[protocol.PayloadTerminalData](in)
				if pErr == nil {
					term.Write(p.Data)
				}
			case protocol.MsgTerminalResize:
				p, pErr := protocol.PayloadAs[protocol.PayloadTerminalResize](in)
				if pErr == nil {
					term.Resize(p.Cols, p.Rows)
				}
			}
		}
	}()

	if len(backlog) > 0 {
		if !s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgTerminalOutput, Payload: protocol.PayloadTerminalData{Data: backlog}}) {
			return false
		}
	}
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				// Channel closed: distinguish a real shell exit from a
				// forced disconnect of a wedged subscriber. Only the former
				// gets MsgTerminalExit; the latter just drops the connection
				// so the client reattaches instead of marking the pane dead.
				if term.isDone() {
					s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgTerminalExit})
				}
				return false
			}
			if !s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgTerminalOutput, Payload: protocol.PayloadTerminalData{Data: data}}) {
				return false
			}
		case <-done:
			return false
		}
	}
}

func (s *Server) handleTerminalKill(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadTerminalKill](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	s.terminals.Kill(payload.Session, payload.Pane)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleTerminalOpen(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadTerminalOpen](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	pane, err := s.terminals.Open(payload.Session, payload.Cols, payload.Rows)
	if err != nil {
		return s.sendError(conn, fmt.Sprintf("terminal: %v", err))
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgTerminalOpenOK,
		Payload: protocol.PayloadTerminalName{Pane: pane},
	})
}

func (s *Server) handleTerminalGetLayout(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadTerminalGetLayout](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	blob, alive := s.terminals.ListLayout(payload.Session)
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgTerminalLayout,
		Payload: protocol.PayloadTerminalLayout{Blob: blob, Alive: alive},
	})
}

func (s *Server) handleTerminalSetLayout(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadTerminalSetLayout](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	s.terminals.SetLayout(payload.Session, payload.Blob, payload.Keep)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleTerminalListAll(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgTerminalListAllOK,
		Payload: protocol.PayloadTerminalListAll{Terminals: s.terminals.ListAll()},
	})
}
