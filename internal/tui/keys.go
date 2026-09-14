package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Key handling for the split view: the Ctrl+W and Ctrl+B (tmux prefix) chords,
// the `:` command line, and plain keys forwarded to the focused shell.

func (m model) handleCtrlW(msg tea.KeyMsg) (model, bool, tea.Cmd) {
	// When focus is on the terminal and a full-screen TUI program is active
	// (vim, htop, less, etc.), forward Ctrl+W to the program so its own
	// window-management commands work (e.g. vim's <C-w>h to switch windows).
	if m.focus == paneTerm && m.shell != nil && m.shell.inAltScreen() {
		return m, false, nil
	}

	key := msg.String()

	if m.ctrlWPressed {
		m.ctrlWPressed = false
		// Accept both the release-Ctrl form (<C-w>h) and the hold-Ctrl form
		// (<C-w><C-h>) that many vim users type without letting go of Ctrl.
		switch key {
		case "h", "left", "ctrl+h", "backspace":
			return m.ctrlWLeft(), true, nil
		case "l", "right", "ctrl+l":
			return m.ctrlWRight(), true, nil
		case "k", "up", "ctrl+k":
			return m.ctrlWVert(false), true, nil
		case "j", "down", "ctrl+j":
			return m.ctrlWVert(true), true, nil
		case "w", "ctrl+w":
			return m.ctrlWCycle(), true, nil
		case "q", "ctrl+q":
			nm, cmd := m.closeFocused()
			return nm, true, cmd
		}
		if m.focus == paneTerm && m.shell != nil {
			m.shell.write([]byte{0x17})
		}
		return m, false, nil
	}

	if msg.Type == tea.KeyCtrlW {
		m.ctrlWPressed = true
		m.ctrlWTimer++
		timerID := m.ctrlWTimer
		return m, true, tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
			return ctrlWTimeoutMsg{id: timerID}
		})
	}

	return m, false, nil
}

// ctrlWLeft moves to the pane on the left.
func (m model) ctrlWLeft() model {
	m.movePaneFocus(splitVert, false)
	return m
}

// ctrlWRight moves to the pane on the right.
func (m model) ctrlWRight() model {
	m.movePaneFocus(splitVert, true)
	return m
}

// ctrlWVert moves between vertically stacked panes.
func (m model) ctrlWVert(down bool) model {
	m.movePaneFocus(splitHoriz, down)
	return m
}

// ctrlWCycle cycles focus through every pane in turn, wrapping at the end.
func (m model) ctrlWCycle() model {
	var leaves []*paneNode
	if root := m.activeRoot(); root != nil {
		leaves = root.leaves()
	}
	idx := -1
	for i, lf := range leaves {
		if lf == m.focusPane {
			idx = i
			break
		}
	}
	if idx >= 0 && idx < len(leaves)-1 {
		m.focusPane = leaves[idx+1]
		m.syncFocusShell()
		return m
	}
	if len(leaves) > 0 {
		m.focusPane = leaves[0]
		m.syncFocusShell()
	}
	return m
}

// handleCtrlB implements a tmux-style prefix: Ctrl+B then <key>.
// Always active so users can reach TUI commands from any context (shell,
// vim, etc.). The prefix stays active until the next key, like tmux.
func (m model) handleCtrlB(msg tea.KeyMsg) (model, bool, tea.Cmd) {
	key := msg.String()

	if m.ctrlBPressed {
		m.ctrlBPressed = false
		switch key {
		case "esc":
			return m, true, nil
		case "c":
			m.inputMode = inputCommand
			m.cmdInput.SetValue("")
			m.cmdInput.Focus()
			m.resizeShell()
			return m, true, textinput.Blink
		case "q":
			return m, true, tea.Quit
		case "|", "%":
			nm, cmd := m.splitFocused(splitVert)
			return nm, true, cmd
		case "-", "\"":
			nm, cmd := m.splitFocused(splitHoriz)
			return nm, true, cmd
		case "o":
			nm, cmd := m.focusNextPane()
			return nm, true, cmd
		case "x":
			nm, cmd := m.closeFocused()
			return nm, true, cmd
		case "left", "h":
			nm, cmd := m.focusDirPane(splitVert, false)
			return nm, true, cmd
		case "right", "l":
			nm, cmd := m.focusDirPane(splitVert, true)
			return nm, true, cmd
		case "up", "k":
			nm, cmd := m.focusDirPane(splitHoriz, false)
			return nm, true, cmd
		case "down":
			nm, cmd := m.focusDirPane(splitHoriz, true)
			return nm, true, cmd
		case "j":
			// Ctrl+B j opens the job-management view. Pane-down is on Ctrl+B ↓.
			nm, mouseCmd := m.openJobsView()
			return nm, true, tea.Batch(sampleHWCmd(nm.hwMon), mouseCmd)
		case "d":
			// Ctrl+B d detaches (tmux-style) back to the management view. The
			// session's panes stay alive on the daemon.
			nm, mouseCmd := m.openJobsView()
			return nm, true, tea.Batch(clearScreenCmd(), sampleHWCmd(nm.hwMon), mouseCmd)
		}
		// Not a recognized chord — forward the buffered Ctrl+B to the focused
		// pane and let the current key flow through normal processing.
		if m.focus == paneTerm && m.shell != nil {
			m.shell.write([]byte{0x02})
		}
		return m, false, nil
	}

	if msg.Type == tea.KeyCtrlB {
		m.ctrlBPressed = true
		m.ctrlBTimer++
		return m, true, nil
	}

	return m, false, nil
}

func (m model) handleTermKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" && m.shellDone() {
		return m, tea.Quit
	}

	if m.shell != nil && !m.shellDone() {
		b := keyToBytes(msg)
		if b != nil {
			m.shell.write(b)
		}
	}
	return m, nil
}

func (m model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputMode = inputNone
		m.cmdInput.Blur()
		m.resizeShell()
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.cmdInput.Value())
		m.cmdInput.Blur()
		m.inputMode = inputNone
		m.resizeShell()
		if value != "" {
			return m.executeCommand(value)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	return m, cmd
}

func (m model) executeCommand(input string) (tea.Model, tea.Cmd) {
	switch input {
	case "q", "quit", ":q", ":quit":
		return m, tea.Quit
	case "jobs", "j", ":jobs", ":j":
		nm, mouseCmd := m.openJobsView()
		return nm, tea.Batch(sampleHWCmd(nm.hwMon), mouseCmd)
	case "vs", "vsplit", "vsp":
		nm, cmd := m.splitFocused(splitVert)
		return nm, cmd
	case "hs", "hsplit", "sp", "split":
		nm, cmd := m.splitFocused(splitHoriz)
		return nm, cmd
	case "o", "next", "next-pane":
		nm, cmd := m.focusNextPane()
		return nm, cmd
	case "x", "close", "kill-pane":
		nm, cmd := m.closeFocused()
		return nm, cmd
	case "detach", "d", ":detach":
		nm, mouseCmd := m.openJobsView()
		return nm, tea.Batch(clearScreenCmd(), sampleHWCmd(nm.hwMon), mouseCmd)
	default:
		m.status = fmt.Sprintf("unknown command: %s", input)
	}
	return m, nil
}
