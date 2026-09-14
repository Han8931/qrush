package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/han/qrush/internal/protocol"
)

// Key and mouse dispatch for the job-management screen, plus the cursor,
// selection and action helpers the bindings drive.

func (m model) handleJobsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.jobs.mode == jobsPager {
		return m.handlePagerKey(msg)
	}

	if m.jobs.form.active {
		return m.handleSessionFormKey(msg)
	}

	if m.jobs.settings.active {
		return m.handleSettingsFormKey(msg)
	}

	if m.jobs.picker.active {
		return m.handlePickerKey(msg)
	}

	if m.jobs.commanding {
		return m.handleJobsCommandKey(msg)
	}

	if m.jobs.helping {
		// Any key dismisses the help overlay.
		m.jobs.helping = false
		return m, nil
	}

	if m.jobs.confirm.kind != confirmNone {
		c := m.jobs.confirm
		m.jobs.confirm = confirmState{}
		if s := msg.String(); s == "y" || s == "Y" {
			switch c.kind {
			case confirmRemove:
				return m, removeJobs(c.ids)
			case confirmClear:
				return m, clearFinishedCmd()
			case confirmDeleteSession:
				return m, deleteSession(c.session)
			case confirmDeleteGroup:
				return m, deleteGroup(c.group)
			case confirmReset:
				return m, resetServerCmd()
			}
		}
		return m, nil
	}

	if m.jobs.filtering {
		switch msg.String() {
		case "enter":
			m.jobs.filtering = false
			m.jobs.filter = m.textInput.Value()
			m.textInput.Blur()
			m.refreshJobsRows()
			return m, nil
		case "esc":
			m.jobs.filtering = false
			m.textInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		m.jobs.filter = m.textInput.Value()
		m.refreshJobsRows()
		return m, cmd
	}

	key := msg.String()

	// Ctrl+W chord: vim-style focus movement between the tree sidebar (left) and
	// the job list (right), mirroring the split-view pane nav. `tab` also toggles.
	if m.ctrlWPressed {
		m.ctrlWPressed = false
		switch key {
		case "h", "left", "ctrl+h", "backspace":
			if m.jobs.tree.show {
				m.jobs.tree.focus = true
			}
			return m, nil
		case "l", "right", "ctrl+l":
			m.jobs.tree.focus = false
			return m, nil
		case "w", "ctrl+w":
			if m.jobs.tree.show {
				m.jobs.tree.focus = !m.jobs.tree.focus
			}
			return m, nil
		}
		// unrecognized chord key: fall through and process it normally
	}
	if msg.Type == tea.KeyCtrlW {
		m.ctrlWPressed = true
		m.ctrlWTimer++
		id := m.ctrlWTimer
		return m, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return ctrlWTimeoutMsg{id: id} })
	}

	// `,` leader chord. Handled before the tree so it works from anywhere,
	// including while the tree has focus. `,n` toggles the sidebar.
	if m.jobs.pending == ',' {
		m.jobs.pending = 0
		if key == "n" {
			return m.toggleTree()
		}
		// unknown leader key: fall through and process it normally
	} else if key == "," {
		return m.startPending(',')
	}

	// Tree sidebar: `tab` moves focus; while it has focus it owns navigation
	// and fold keys (else the list handler below runs).
	if nm, cmd, done := m.handleTreeKey(msg); done {
		return nm, cmd
	}

	// Complete a pending two-key motion (gg jumps to top; sg/si/ss/st sort).
	if m.jobs.pending != 0 {
		op := m.jobs.pending
		m.jobs.pending = 0
		if op == 'g' && key == "g" {
			(&m).jobsGoto(0)
			return m, nil
		}
		if op == 's' {
			if mode, ok := chordSortMode(key); ok {
				(&m).applySort(mode)
				return m, nil
			}
		}
		// not a completion — fall through and process key normally
	}

	// Open the `:` command line. A colon typed or pasted with following
	// characters (e.g. ":config") arrives as one runes message, so seed the
	// input with whatever trails the colon.
	if strings.HasPrefix(key, ":") {
		m.jobs.commanding = true
		m.cmdInput.SetValue(key[1:])
		m.cmdInput.Focus()
		m.cmdInput.CursorEnd()
		return m, textinput.Blink
	}

	switch key {
	case "esc":
		if m.jobs.visual {
			m.jobs.visual = false
			return m, nil
		}
		if len(m.jobs.tagged) > 0 {
			m.jobs.tagged = nil
			return m, nil
		}
		return m.leaveManagement()
	case "q":
		// Always quit, even with a session open in the background — daemon-side
		// shells persist, and `esc` already covers "back to the session". Without
		// this, `q` after a detach just bounced back into the session.
		return m, tea.Quit
	case " ":
		// ranger/lf-style tagging: toggle the cursor job's selection, then
		// advance one row. Esc clears; actions consume the tagged set.
		if r, ok := m.jobsCursorRow(); ok {
			if m.jobs.tagged == nil {
				m.jobs.tagged = make(map[int]bool)
			}
			if m.jobs.tagged[r.job.ID] {
				delete(m.jobs.tagged, r.job.ID)
			} else {
				m.jobs.tagged[r.job.ID] = true
			}
		}
		(&m).jobsMove(1)
	case "j", "down":
		(&m).jobsMove(1)
	case "k", "up":
		(&m).jobsMove(-1)
	case "ctrl+d":
		(&m).jobsMove(m.jobsBodyHeight() / 2)
	case "ctrl+u":
		(&m).jobsMove(-m.jobsBodyHeight() / 2)
	case "G":
		(&m).jobsGoto(len(m.jobs.rows) - 1)
	case "g":
		return m.startPending('g')
	case "V":
		if m.jobs.visual {
			m.jobs.visual = false
		} else if _, ok := m.jobsSelected(); ok {
			m.jobs.visual = true
			m.jobs.anchor = m.jobs.cursor
		}
	case "/":
		m.jobs.filtering = true
		m.textInput.SetValue(m.jobs.filter)
		m.textInput.Focus()
		return m, textinput.Blink
	case "?":
		m.jobs.helping = true
		return m, nil
	case "S":
		// Open the session picker (reaches every session, incl. empty ones).
		return m.openSessionPicker(), nil
	case "s":
		// Start a sort chord: sg / si / ss / st pick the field, repeating the
		// active field's chord reverses it. Header shows column + direction.
		return m.startPending('s')
	case "R":
		// Reverse the current sort (bada-style flip).
		m.jobs.sortRev = !m.jobs.sortRev
		m.refreshJobsRows()
		return m, nil
	case "e":
		// Edit the cursor job in one box: job name + session + group. (Sessions
		// and groups are edited from the tree sidebar.)
		if r, ok := m.jobsCursorRow(); ok {
			return m.openJobEditForm(r.job, r.group), textinput.Blink
		}
	case "n", "a":
		// Create a new session via the edit box (blank form).
		return m.openSessionForm(true, "", ""), textinput.Blink
	case "x":
		if ids := m.jobsActionIDs(); len(ids) > 0 {
			m.jobs.visual = false
			m.jobs.tagged = nil
			return m, killJobs(ids)
		}
	case "u":
		if ids := m.jobsActionIDs(); len(ids) > 0 {
			m.jobs.visual = false
			m.jobs.tagged = nil
			return m, makeUrgentJobs(ids)
		}
	case "r":
		if ids := m.jobsActionIDs(); len(ids) > 0 {
			m.jobs.visual = false
			m.jobs.tagged = nil
			return m, rerunJobs(ids)
		}
	case "d":
		// Remove jobs (finished ones without a prompt).
		ids := m.jobsActionIDs()
		if len(ids) == 0 {
			return m, nil
		}
		allFinished := m.actionAllFinished()
		m.jobs.visual = false
		m.jobs.tagged = nil
		if allFinished {
			return m, removeJobs(ids)
		}
		m.jobs.confirm = confirmState{kind: confirmRemove, ids: ids}
		return m, nil
	case "D":
		// Delete every finished job at once. They are inert, so skip the
		// confirmation prompt (`C` keeps the confirming variant).
		return m, clearFinishedCmd()
	case "C":
		m.jobs.confirm = confirmState{kind: confirmClear}
	case "enter":
		return m.activateRow()
	case "o":
		if j, ok := m.jobsSelected(); ok {
			return m, openPagerCmd(j)
		}
	}
	return m, nil
}

// mgmtBodyTopLine is the terminal row (0-based) where the first list row is
// drawn: after the top border and the column header.
func (m model) mgmtBodyTopLine() int { return 2 }

// handleMouse maps clicks and wheel scrolls on the management screen to row
// selection/activation. Mouse events are only captured while this screen shows.
func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.viewMode != viewJobs || m.jobs.mode != jobsTable || m.jobs.form.active ||
		m.jobs.settings.active || m.jobs.filtering {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		(&m).jobsMove(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		(&m).jobsMove(1)
		return m, nil
	case tea.MouseButtonLeft:
		top := m.mgmtBodyTopLine()
		if msg.Y < top || msg.Y >= top+m.jobsBodyHeight() {
			return m, nil
		}
		idx := m.jobs.offset + (msg.Y - top)
		if idx < 0 || idx >= len(m.jobs.rows) {
			return m, nil
		}
		m.jobs.cursor = idx
		m.jobs.cursorKey = rowKey(m.jobs.rows[idx])
		m.jobs.offset = clampOffset(idx, m.jobs.offset, m.jobsBodyHeight(), len(m.jobs.rows))
		return m, nil
	}
	return m, nil
}

// leaveManagement exits the management view: back to the active session's split
// view, or quits when launched with --jobs or when no session is open.
func (m model) leaveManagement() (tea.Model, tea.Cmd) {
	if m.jobsOnly || m.activeSession == "" || m.activeRoot() == nil {
		return m, tea.Quit
	}
	m.viewMode = viewSplit
	m.mouseOn = false
	return m, tea.Batch(clearScreenCmd(), tea.DisableMouse)
}

// activateRow handles Enter on a job row: open its output pager (like `o`).
// Sessions are opened from the tree sidebar or the picker (`S`).
func (m model) activateRow() (tea.Model, tea.Cmd) {
	r, ok := m.jobsCursorRow()
	if !ok {
		return m, nil
	}
	return m, openPagerCmd(r.job)
}

// openSession leaves the management view and activates the given session,
// building or restoring its terminal panes.
func (m model) openSession(name string) (tea.Model, tea.Cmd) {
	if name == "" {
		return m, nil
	}
	m.viewMode = viewSplit
	m.mouseOn = false
	nm, cmd := m.activateSession(name)
	return nm, tea.Batch(cmd, tea.DisableMouse)
}

// chordSortMode maps the second key of an `s` sort chord to its field.
func chordSortMode(key string) (sortMode, bool) {
	switch key {
	case "g":
		return sortGrouped, true
	case "i":
		return sortByID, true
	case "s":
		return sortByState, true
	case "t":
		return sortByTime, true
	}
	return 0, false
}

// applySort selects a sort field; re-selecting the active field reverses it.
func (m *model) applySort(mode sortMode) {
	if m.jobs.sortMode == mode {
		m.jobs.sortRev = !m.jobs.sortRev
	} else {
		m.jobs.sortMode = mode
		m.jobs.sortRev = false
	}
	m.refreshJobsRows()
}

func (m model) startPending(op byte) (tea.Model, tea.Cmd) {
	m.jobs.pending = op
	m.jobs.pendingTimer++
	id := m.jobs.pendingTimer
	return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return jobsGTimeoutMsg{id: id} })
}

func parseSortMode(s string) (sortMode, bool) {
	switch strings.ToLower(s) {
	case "group", "grouped":
		return sortGrouped, true
	case "id":
		return sortByID, true
	case "state":
		return sortByState, true
	case "time":
		return sortByTime, true
	}
	return sortGrouped, false
}

func (m *model) jobsGoto(idx int) {
	n := len(m.jobs.rows)
	if n == 0 {
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	m.jobs.cursor = idx
	m.jobs.cursorKey = rowKey(m.jobs.rows[idx])
	m.jobs.offset = clampOffset(idx, m.jobs.offset, m.jobsBodyHeight(), n)
}

// jobsActionRows returns the job rows an action applies to: the visual
// selection when active (job rows only), otherwise just the cursor job row.
func (m model) jobsActionRows() []protocol.JobInfo {
	// Tagged (Space) selection wins; it survives cursor movement like ranger's.
	if len(m.jobs.tagged) > 0 {
		var rows []protocol.JobInfo
		for _, r := range m.jobs.rows {
			if m.jobs.tagged[r.job.ID] {
				rows = append(rows, r.job)
			}
		}
		if len(rows) > 0 {
			return rows
		}
	}
	if m.jobs.visual {
		lo, hi := m.visualRange()
		var rows []protocol.JobInfo
		for i := lo; i <= hi && i < len(m.jobs.rows); i++ {
			if i >= 0 {
				rows = append(rows, m.jobs.rows[i].job)
			}
		}
		return rows
	}
	if j, ok := m.jobsSelected(); ok {
		return []protocol.JobInfo{j}
	}
	return nil
}

// jobsActionIDs returns the job IDs an action applies to.
func (m model) jobsActionIDs() []int {
	rows := m.jobsActionRows()
	ids := make([]int, 0, len(rows))
	for _, j := range rows {
		ids = append(ids, j.ID)
	}
	return ids
}

// actionAllFinished reports whether every job in the current action set is
// finished — i.e. safe to remove without a confirmation prompt.
func (m model) actionAllFinished() bool {
	rows := m.jobsActionRows()
	if len(rows) == 0 {
		return false
	}
	for _, j := range rows {
		if j.State != protocol.StateFinished {
			return false
		}
	}
	return true
}

func (m model) visualRange() (int, int) {
	lo, hi := m.jobs.anchor, m.jobs.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

func (m *model) jobsMove(delta int) {
	n := len(m.jobs.rows)
	if n == 0 {
		return
	}
	m.jobs.cursor += delta
	if m.jobs.cursor < 0 {
		m.jobs.cursor = 0
	}
	if m.jobs.cursor >= n {
		m.jobs.cursor = n - 1
	}
	m.jobs.cursorKey = rowKey(m.jobs.rows[m.jobs.cursor])
	m.jobs.offset = clampOffset(m.jobs.cursor, m.jobs.offset, m.jobsBodyHeight(), n)
}
