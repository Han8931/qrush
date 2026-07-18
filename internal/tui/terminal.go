package tui

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/protocol"
)

type shellState struct {
	id       int
	session  string
	pane     string
	remote   *client.Client
	writeMu  sync.Mutex // serializes all sends on remote (input/resize + vt replies)
	ptmx     *os.File
	cmd      *exec.Cmd
	vt       vt10x.Terminal
	mu       sync.Mutex
	ptyCh    chan ptyEvent
	done     bool
	atPrompt bool
	prevAlt  bool
	rcDir    string
	lastCols int    // last size sent (coalesces redundant resizes; mu-guarded)
	lastRows int    //
	cwd      string // working dir reported by the pane via OSC 7 (mu-guarded)
}

type ptyEvent struct {
	clear bool
}

// remoteInputWriter lets the client-side vt10x send terminal replies (e.g.
// cursor-position reports) back to the daemon pane as input. Routed through the
// shell's writeMu so it can't interleave with keystroke/resize sends.
type remoteInputWriter struct{ s *shellState }

func (w remoteInputWriter) Write(p []byte) (int, error) {
	w.s.sendRemoteMsg(&protocol.Msg{
		Type:    protocol.MsgTerminalInput,
		Payload: protocol.PayloadTerminalData{Data: append([]byte(nil), p...)},
	})
	return len(p), nil
}

func (s *shellState) sendRemoteMsg(msg *protocol.Msg) {
	if s.remote == nil {
		return
	}
	s.writeMu.Lock()
	_ = s.remote.Send(msg)
	s.writeMu.Unlock()
}

func spawnShellPane(cols, rows int, session, pane string) (*shellState, error) {
	if session == "" {
		session = "default"
	}
	if pane == "" {
		pane = "main"
	}

	if remote, err := client.AttachTerminal(session, pane, cols, rows); err == nil {
		s := &shellState{
			session: session,
			pane:    pane,
			remote:  remote,
			ptyCh:   make(chan ptyEvent, 1),
		}
		s.vt = vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(remoteInputWriter{s: s}))
		return s, nil
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	if session == "" {
		session = "default"
	}

	cmd, rcDir := shellCommand(shell)
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "QRUSH_SESSION="+session)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		if rcDir != "" {
			os.RemoveAll(rcDir)
		}
		return nil, fmt.Errorf("start shell: %w", err)
	}

	vterm := vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(ptmx))

	return &shellState{
		session: session,
		pane:    pane,
		ptmx:    ptmx,
		cmd:     cmd,
		vt:      vterm,
		ptyCh:   make(chan ptyEvent, 1),
		rcDir:   rcDir,
	}, nil
}

func shellCommand(shell string) (*exec.Cmd, string) {
	name := filepath.Base(shell)
	switch name {
	case "zsh":
		if rcDir, err := setupZshPromptMarker(); err == nil {
			cmd := exec.Command(shell)
			cmd.Env = append(os.Environ(), "ZDOTDIR="+rcDir, "QRUSH_ORIG_ZDOTDIR="+origZDOTDIR())
			return cmd, rcDir
		}
	case "bash":
		if rcDir, rcFile, err := setupBashPromptMarker(); err == nil {
			return exec.Command(shell, "--rcfile", rcFile, "-i"), rcDir
		}
	}
	return exec.Command(shell), ""
}

func setupZshPromptMarker() (string, error) {
	rcDir, err := os.MkdirTemp("", "qrush-zsh-*")
	if err != nil {
		return "", err
	}
	rc := `if [ -n "$QRUSH_ORIG_ZDOTDIR" ] && [ -r "$QRUSH_ORIG_ZDOTDIR/.zshrc" ]; then
  source "$QRUSH_ORIG_ZDOTDIR/.zshrc"
elif [ -r "$HOME/.zshrc" ]; then
  source "$HOME/.zshrc"
fi
function __qrush_prompt_marker() { printf '\033]133;A\a'; }
autoload -Uz add-zsh-hook
add-zsh-hook precmd __qrush_prompt_marker
`
	if err := os.WriteFile(filepath.Join(rcDir, ".zshrc"), []byte(rc), 0600); err != nil {
		os.RemoveAll(rcDir)
		return "", err
	}
	return rcDir, nil
}

func setupBashPromptMarker() (string, string, error) {
	rcDir, err := os.MkdirTemp("", "qrush-bash-*")
	if err != nil {
		return "", "", err
	}
	rcFile := filepath.Join(rcDir, "bashrc")
	rc := `if [ -r "$HOME/.bashrc" ]; then
  source "$HOME/.bashrc"
fi
__qrush_prompt_marker() { printf '\033]133;A\a'; }
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

func (s *shellState) resize(cols, rows int) {
	// Coalesce: skip when the size hasn't changed so a window drag (or a tree
	// toggle that doesn't move this pane) doesn't flood the daemon/PTY.
	s.mu.Lock()
	if cols == s.lastCols && rows == s.lastRows {
		s.mu.Unlock()
		return
	}
	s.lastCols, s.lastRows = cols, rows
	s.mu.Unlock()

	s.vt.Resize(cols, rows)
	if s.remote != nil {
		s.sendRemoteMsg(&protocol.Msg{
			Type:    protocol.MsgTerminalResize,
			Payload: protocol.PayloadTerminalResize{Cols: cols, Rows: rows},
		})
		return
	}
	_ = pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

func (s *shellState) getCwd() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwd
}

// tryReattach re-dials the daemon pane after an unexpected stream drop (e.g. the
// server disconnected a wedged subscriber), swapping in the new connection. The
// caller clears the vt and lets the replayed backlog resync the screen.
func (s *shellState) tryReattach() (*client.Client, bool) {
	s.mu.Lock()
	cols, rows, done := s.lastCols, s.lastRows, s.done
	s.mu.Unlock()
	if done || s.remote == nil {
		return nil, false
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	for attempt := 0; attempt < 3; attempt++ {
		c, err := client.AttachTerminal(s.session, s.pane, cols, rows)
		if err == nil {
			old := s.remote
			s.writeMu.Lock()
			s.remote = c
			s.writeMu.Unlock()
			if old != nil {
				old.Close()
			}
			return c, true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, false
}

// extractCwd strips OSC 7 (cwd report) sequences from the stream and records the
// reported directory, so the status bar's branch can follow the pane's cwd.
func (s *shellState) extractCwd(data []byte) []byte {
	const prefix = "\x1b]7;"
	if !bytesContains(data, prefix) {
		return data
	}
	str := string(data)
	var b strings.Builder
	for {
		i := strings.Index(str, prefix)
		if i < 0 {
			b.WriteString(str)
			break
		}
		b.WriteString(str[:i])
		rest := str[i+len(prefix):]
		end, termLen := len(rest), 0
		if j := strings.IndexByte(rest, '\a'); j >= 0 {
			end, termLen = j, 1
		}
		if k := strings.Index(rest, "\x1b\\"); k >= 0 && k < end {
			end, termLen = k, 2
		}
		if cwd := parseFileURI(rest[:end]); cwd != "" {
			s.mu.Lock()
			s.cwd = cwd
			s.mu.Unlock()
		}
		if end+termLen > len(rest) {
			break
		}
		str = rest[end+termLen:]
	}
	return []byte(b.String())
}

func parseFileURI(uri string) string {
	const fp = "file://"
	if !strings.HasPrefix(uri, fp) {
		return ""
	}
	rest := uri[len(fp):]
	slash := strings.IndexByte(rest, '/') // skip host component
	if slash < 0 {
		return ""
	}
	p := rest[slash:]
	if dec, err := url.PathUnescape(p); err == nil {
		return dec
	}
	return p
}

func (s *shellState) write(b []byte) {
	s.mu.Lock()
	s.atPrompt = false
	s.mu.Unlock()
	if s.ptmx == nil {
		if s.remote != nil {
			s.sendRemoteMsg(&protocol.Msg{
				Type:    protocol.MsgTerminalInput,
				Payload: protocol.PayloadTerminalData{Data: append([]byte(nil), b...)},
			})
		}
		return
	}
	s.ptmx.Write(b)
}

func (s *shellState) close() {
	if s.remote != nil {
		s.remote.Close()
		return
	}
	if s.cmd != nil && s.cmd.Process != nil {
		terminateShellProcess(s.cmd.Process.Pid)
	}
	if s.ptmx != nil {
		s.ptmx.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		done := make(chan struct{})
		go func() {
			_ = s.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			forceKillShellProcess(s.cmd.Process.Pid)
			<-done
		}
	}
	if s.rcDir != "" {
		os.RemoveAll(s.rcDir)
	}
}

func (s *shellState) destroy() {
	if s.remote != nil {
		_ = client.KillTerminal(s.session, s.pane)
		s.remote.Close()
		return
	}
	s.close()
}

type ptyOutputMsg struct {
	id    int
	clear bool
}
type shellExitMsg struct{ id int }

func (s *shellState) readLoop() {
	if s.remote != nil {
		s.remoteReadLoop()
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			data, clearPane := clearAfterAltScreenExit(buf[:n])
			data, _ = s.normalizePromptMarker(data)
			data = s.extractCwd(data)
			s.vt.Write(data)

			// Detect alt-screen exit (vim/htop/less leaving full-screen mode).
			// If a shell does not emit prompt markers, keep a CRLF fallback so the
			// prompt is not drawn over the restored cursor position.
			s.vt.Lock()
			currAlt := s.vt.Mode()&vt10x.ModeAltScreen != 0
			s.vt.Unlock()
			if s.prevAlt && !currAlt && !clearPane {
				s.vt.Write([]byte("\r\n"))
			}
			s.prevAlt = currAlt

			s.notifyPTY(clearPane)
		}
		if err != nil {
			s.mu.Lock()
			s.done = true
			s.mu.Unlock()
			if s.rcDir != "" {
				os.RemoveAll(s.rcDir)
				s.rcDir = ""
			}
			close(s.ptyCh)
			return
		}
	}
}

func (s *shellState) remoteReadLoop() {
	conn := s.remote
	for {
		msg, err := conn.Recv()
		if err != nil {
			// The daemon may have dropped a wedged subscriber (not a real exit).
			// Try to reattach; on success the replayed backlog resyncs us.
			if newConn, ok := s.tryReattach(); ok {
				conn = newConn
				s.vt.Write([]byte("\x1b[3J\x1b[2J\x1b[H")) // clear; backlog redraws
				s.notifyPTY(true)
				continue
			}
			s.mu.Lock()
			s.done = true
			s.mu.Unlock()
			close(s.ptyCh)
			return
		}
		switch msg.Type {
		case protocol.MsgTerminalOutput:
			payload, pErr := protocol.PayloadAs[protocol.PayloadTerminalData](msg)
			if pErr != nil {
				continue
			}
			data, clearPane := clearAfterAltScreenExit(payload.Data)
			data, _ = s.normalizePromptMarker(data)
			data = s.extractCwd(data)
			s.vt.Write(data)

			s.vt.Lock()
			currAlt := s.vt.Mode()&vt10x.ModeAltScreen != 0
			s.vt.Unlock()
			if s.prevAlt && !currAlt && !clearPane {
				s.vt.Write([]byte("\r\n"))
			}
			s.prevAlt = currAlt
			s.notifyPTY(clearPane)
		case protocol.MsgTerminalExit:
			s.mu.Lock()
			s.done = true
			s.mu.Unlock()
			close(s.ptyCh)
			return
		}
	}
}

func (s *shellState) normalizePromptMarker(data []byte) ([]byte, bool) {
	const promptMarker = "\x1b]133;A\a"
	hasPromptMarker := bytesContains(data, promptMarker)
	if !hasPromptMarker {
		return data, false
	}
	s.mu.Lock()
	s.atPrompt = true
	s.mu.Unlock()

	s.vt.Lock()
	cursor := s.vt.Cursor()
	_, rows := s.vt.Size()
	lastContentRow := lastNonEmptyRow(s.vt)
	s.vt.Unlock()

	out := strings.ReplaceAll(string(data), promptMarker, "")
	if lastContentRow < 0 && cursor.X == 0 {
		return []byte(out), false
	}
	if lastContentRow >= rows-1 {
		return []byte("\r\n" + out), true
	}

	targetRow := lastContentRow + 2
	if targetRow < 1 {
		targetRow = cursor.Y + 1
	}
	if targetRow < 1 {
		targetRow = 1
	}
	return []byte(fmt.Sprintf("\x1b[%d;1H\x1b[K%s", targetRow, out)), true
}

func (s *shellState) promptReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.atPrompt
}

func (s *shellState) inAltScreen() bool {
	if s.vt == nil {
		return false
	}
	s.vt.Lock()
	defer s.vt.Unlock()
	return s.vt.Mode()&vt10x.ModeAltScreen != 0
}

func bytesContains(data []byte, needle string) bool {
	return bytes.Contains(data, []byte(needle))
}

func (s *shellState) notifyPTY(clear bool) {
	if clear {
		select {
		case <-s.ptyCh:
		default:
		}
		select {
		case s.ptyCh <- ptyEvent{clear: true}:
		default:
		}
		return
	}
	select {
	case s.ptyCh <- ptyEvent{}:
	default:
	}
}

func clearAfterAltScreenExit(data []byte) ([]byte, bool) {
	out := string(data)
	for _, seq := range []string{
		"\x1b[?1049l",
		"\x1b[?1047l",
		"\x1b[?47l",
	} {
		if strings.Contains(out, seq) {
			return data, true
		}
	}
	return data, false
}

func lastNonEmptyRow(vt vt10x.Terminal) int {
	cols, rows := vt.Size()
	for y := rows - 1; y >= 0; y-- {
		for x := 0; x < cols; x++ {
			ch := vt.Cell(x, y).Char
			if ch != 0 && ch != ' ' {
				return y
			}
		}
	}
	return -1
}

func waitForPTYOutput(id int, ch <-chan ptyEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return shellExitMsg{id: id}
		}
		return ptyOutputMsg{id: id, clear: event.clear}
	}
}

// termStyle keys one pane cell's visual attributes: fg/bg color plus the
// cursor highlight (drawn as reverse video, overriding the cell colors).
type termStyle struct {
	fg, bg vt10x.Color
	cursor bool
}

// sgrPrefix returns the escape sequence that starts a cell with this style,
// or "" when the cell needs no styling (default colors, no cursor).
func (ts termStyle) sgrPrefix() string {
	if ts.cursor {
		return "\x1b[7m"
	}
	fg := colorSGR(ts.fg, "38")
	bg := colorSGR(ts.bg, "48")
	switch {
	case fg == "" && bg == "":
		return ""
	case fg == "":
		return "\x1b[" + bg + "m"
	case bg == "":
		return "\x1b[" + fg + "m"
	}
	return "\x1b[" + fg + ";" + bg + "m"
}

// colorSGR renders one vt10x color as an SGR parameter (base "38" = fg,
// "48" = bg). vt10x colors are the 0-based ANSI/xterm palette for values
// < 256, packed r<<16|g<<8|b truecolor above that, and the Default* sentinels
// from 1<<24 — those (and anything unknown) return "" for the terminal's own
// default color.
func colorSGR(c vt10x.Color, base string) string {
	switch {
	case c >= 1<<24: // vt10x.DefaultFG / DefaultBG / DefaultCursor
		return ""
	case c < 256:
		return fmt.Sprintf("%s;5;%d", base, c)
	default:
		return fmt.Sprintf("%s;2;%d;%d;%d", base, (c>>16)&0xff, (c>>8)&0xff, c&0xff)
	}
}

// renderTermCells renders the terminal into a rows×cols grid of styled
// single-column strings. When showCursor is false the cursor cell is drawn
// like any other (used for unfocused split panes). Escape prefixes are built
// once per distinct style per frame — not per cell — since a screen typically
// holds a handful of styles across thousands of cells.
func renderTermCells(vt vt10x.Terminal, cols, rows int, showCursor bool) [][]string {
	vt.Lock()
	defer vt.Unlock()

	cells := make([][]string, rows)
	cursor := vt.Cursor()
	prefixes := make(map[termStyle]string)

	for y := 0; y < rows; y++ {
		row := make([]string, cols)
		for x := 0; x < cols; x++ {
			g := vt.Cell(x, y)
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}

			st := termStyle{fg: g.FG, bg: g.BG, cursor: showCursor && x == cursor.X && y == cursor.Y}
			prefix, ok := prefixes[st]
			if !ok {
				prefix = st.sgrPrefix()
				prefixes[st] = prefix
			}
			if prefix == "" {
				row[x] = string(ch)
			} else {
				row[x] = prefix + string(ch) + "\x1b[0m"
			}
		}
		cells[y] = row
	}
	return cells
}

func keyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{'\x7f'}
	case tea.KeyEscape:
		return []byte{'\x1b'}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyCtrlA:
		return []byte{0x01}
	case tea.KeyCtrlB:
		return []byte{0x02}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyCtrlD:
		return []byte{0x04}
	case tea.KeyCtrlE:
		return []byte{0x05}
	case tea.KeyCtrlF:
		return []byte{0x06}
	case tea.KeyCtrlG:
		return []byte{0x07}
	case tea.KeyCtrlK:
		return []byte{0x0b}
	case tea.KeyCtrlL:
		return []byte{0x0c}
	case tea.KeyCtrlN:
		return []byte{0x0e}
	case tea.KeyCtrlO:
		return []byte{0x0f}
	case tea.KeyCtrlP:
		return []byte{0x10}
	case tea.KeyCtrlR:
		return []byte{0x12}
	case tea.KeyCtrlS:
		return []byte{0x13}
	case tea.KeyCtrlT:
		return []byte{0x14}
	case tea.KeyCtrlU:
		return []byte{0x15}
	case tea.KeyCtrlV:
		return []byte{0x16}
	case tea.KeyCtrlW:
		return []byte{0x17}
	case tea.KeyCtrlX:
		return []byte{0x18}
	case tea.KeyCtrlY:
		return []byte{0x19}
	case tea.KeyCtrlZ:
		return []byte{0x1a}
	}
	return nil
}
