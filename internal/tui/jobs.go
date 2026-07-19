package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/han/qrush/internal/format"
	"github.com/han/qrush/internal/protocol"
)

// Core state and key handling for the job-management screen: view modes,
// the flat row model (build/sort/filter/anchor), selection, and the main
// key/mouse dispatch.

// viewMode selects the top-level screen.
type viewMode int

const (
	viewSplit viewMode = iota
	viewJobs
)

// jobsSubMode is the sub-view within the job-management modal.
type jobsSubMode int

const (
	jobsTable jobsSubMode = iota
	jobsPager
)

type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmRemove
	confirmClear
	confirmDeleteSession
	confirmDeleteGroup
	confirmReset
)

type confirmState struct {
	kind    confirmKind
	ids     []int  // jobs targeted by a confirmRemove
	session string // session targeted by confirmDeleteSession
	group   string // group targeted by confirmDeleteGroup
}

const (
	jobsDetailHeight = 9
	pagerByteCap     = 1 << 20 // 1 MiB tail cap for the output pager
)

// mgmtRowKind tags a row in the flat management table: a job, or a placeholder
// for a session that currently has no jobs (so every session stays visible).
// Job-action code guards on rowJob, so session rows are naturally excluded.
type mgmtRowKind int

const (
	rowJob mgmtRowKind = iota
	rowSession
)

// mgmtRow is one line in the flat management table. Job rows carry a job;
// session rows are placeholders for job-less sessions. Both carry group +
// session so Enter can open the session and the columns can be filled.
type mgmtRow struct {
	kind    mgmtRowKind
	session string           // session name
	group   string           // group name
	job     protocol.JobInfo // the job payload (rowJob only)
}

// sortMode selects how the flat job table is ordered.
type sortMode int

const (
	sortGrouped sortMode = iota // group → session → id (default)
	sortByID                    // job id
	sortByState                 // running → queued → finished, then id
	sortByTime                  // longest real time first
)

func (s sortMode) label() string {
	switch s {
	case sortByID:
		return "id"
	case sortByState:
		return "state"
	case sortByTime:
		return "time"
	default:
		return "group"
	}
}

// jobsView holds all state for the full-screen job-management modal.
type jobsView struct {
	mode    jobsSubMode
	allJobs []protocol.JobInfo // raw, refreshed each tick
	rows    []mgmtRow          // derived: flattened visible group/session/job rows

	cursorKey string // selection anchored by composite key, not index
	cursor    int
	offset    int

	scopeAll   bool
	filtering  bool
	filter     string
	commanding bool // the ':' command line is open
	helping    bool // the help overlay is open

	visual bool // visual (multi-select) mode active
	anchor int  // row index where the visual selection started

	// tagged is the ranger/lf-style persistent selection: job IDs toggled
	// with Space. Actions (x/u/r/d) apply to the tagged set when non-empty.
	tagged map[int]bool

	sortMode sortMode
	sortRev  bool // reverse the current sort

	pending      byte // first key of a two-key motion: 'g' (gg) or 'd' (dd)
	pendingTimer int

	confirm  confirmState
	form     sessionForm
	settings configForm
	picker   sessionPicker
	pager    pagerState
}

type jobsGTimeoutMsg struct{ id int }

// --- open / refresh -------------------------------------------------------

// openJobsView switches to the full-screen management view and enables mouse
// capture (so sessions/jobs can be clicked). The returned command turns mouse
// reporting on; callers batch it with their own commands.
func (m model) openJobsView() (model, tea.Cmd) {
	m.viewMode = viewJobs
	// Preserve the collapse state across re-entry, but reset transient sub-state.
	m.jobs = jobsView{scopeAll: m.jobs.scopeAll, filter: m.jobs.filter}
	m.jobs.allJobs = m.collectJobs()
	m.refreshJobsRows()
	m.mouseOn = true
	return m, tea.EnableMouseCellMotion
}

// collectJobs flattens the per-session job lists already cached in the tree.
func (m model) collectJobs() []protocol.JobInfo {
	var out []protocol.JobInfo
	for _, n := range m.nodes {
		for _, js := range n.jobs {
			out = append(out, js...)
		}
	}
	return out
}

// buildMgmtRows flattens every session's jobs into one flat table, applying the
// active filter and the current sort. A session with no matching jobs still gets
// one placeholder row so every session stays visible.
func (m model) buildMgmtRows() []mgmtRow {
	var out []mgmtRow
	for _, node := range m.nodes {
		for _, session := range node.sessions {
			jobs := filterJobs(node.jobs[session.Name], m.jobs.filter, "", true)
			if len(jobs) == 0 {
				// Hide empty sessions only when a text filter is active and the
				// session name itself doesn't match.
				if m.jobs.filter != "" && !strings.Contains(strings.ToLower(session.Name), strings.ToLower(m.jobs.filter)) {
					continue
				}
				out = append(out, mgmtRow{kind: rowSession, session: session.Name, group: node.group})
				continue
			}
			for _, j := range jobs {
				out = append(out, mgmtRow{
					kind: rowJob, session: session.Name, group: node.group, job: j,
				})
			}
		}
	}
	sortMgmtRows(out, m.jobs.sortMode, m.jobs.sortRev)
	return out
}

// sortMgmtRows orders the flat table in place per the selected mode, then
// reverses when sortRev is set (mirrors bada's "press the same key to flip").
func sortMgmtRows(rows []mgmtRow, mode sortMode, rev bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch mode {
		case sortByID:
			return a.job.ID < b.job.ID
		case sortByState:
			if ra, rb := stateRank(a.job), stateRank(b.job); ra != rb {
				return ra < rb
			}
			return a.job.ID < b.job.ID
		case sortByTime:
			if ta, tb := format.ElapsedMS(a.job), format.ElapsedMS(b.job); ta != tb {
				return ta > tb
			}
			return a.job.ID < b.job.ID
		default: // sortGrouped
			if a.group != b.group {
				return a.group < b.group
			}
			if a.session != b.session {
				return a.session < b.session
			}
			return a.job.ID < b.job.ID
		}
	})
	if rev {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
}

// stateRank orders jobs for the state sort: running first, then queued, then
// finished/other.
func stateRank(j protocol.JobInfo) int {
	switch j.State {
	case protocol.StateRunning:
		return 0
	case protocol.StateQueued:
		return 1
	default:
		return 2
	}
}

// rowKey is a stable identity for a row so the cursor survives refresh/resort.
func rowKey(r mgmtRow) string {
	if r.kind == rowSession {
		return "s:" + r.session
	}
	return fmt.Sprintf("j:%d", r.job.ID)
}

func (m *model) refreshJobsRows() {
	rows := m.buildMgmtRows()
	m.jobs.rows = rows
	// Drop tags whose job no longer exists (removed, cleared, pruned).
	if len(m.jobs.tagged) > 0 {
		live := make(map[int]bool, len(m.jobs.allJobs))
		for _, j := range m.jobs.allJobs {
			live[j.ID] = true
		}
		for id := range m.jobs.tagged {
			if !live[id] {
				delete(m.jobs.tagged, id)
			}
		}
	}
	m.jobs.cursor, m.jobs.cursorKey = anchorMgmtCursor(rows, m.jobs.cursorKey, m.jobs.cursor)
	m.jobs.offset = clampOffset(m.jobs.cursor, m.jobs.offset, m.jobsBodyHeight(), len(rows))
	if m.jobs.anchor >= len(rows) {
		m.jobs.anchor = len(rows) - 1
	}
	if m.jobs.anchor < 0 {
		m.jobs.anchor = 0
	}
}

// anchorMgmtCursor keeps the cursor on the row with the same key across
// rebuilds, falling back to the previous index when the row is gone.
func anchorMgmtCursor(rows []mgmtRow, key string, prev int) (int, string) {
	if len(rows) == 0 {
		return 0, ""
	}
	for i, r := range rows {
		if rowKey(r) == key {
			return i, key
		}
	}
	nc := prev
	if nc >= len(rows) {
		nc = len(rows) - 1
	}
	if nc < 0 {
		nc = 0
	}
	return nc, rowKey(rows[nc])
}

// visibleJobs extracts the job payloads from the current rows, for summary and
// state-count lines.
func (m model) visibleJobs() []protocol.JobInfo {
	var out []protocol.JobInfo
	for _, r := range m.jobs.rows {
		if r.kind == rowJob {
			out = append(out, r.job)
		}
	}
	return out
}

// jobsCursorRow returns the row under the cursor.
func (m model) jobsCursorRow() (mgmtRow, bool) {
	if m.jobs.cursor >= 0 && m.jobs.cursor < len(m.jobs.rows) {
		return m.jobs.rows[m.jobs.cursor], true
	}
	return mgmtRow{}, false
}

// jobsSelected returns the job under the cursor, only when it is a job row.
func (m model) jobsSelected() (protocol.JobInfo, bool) {
	if r, ok := m.jobsCursorRow(); ok && r.kind == rowJob {
		return r.job, true
	}
	return protocol.JobInfo{}, false
}

// --- pure helpers (unit-tested) -------------------------------------------

func filterJobs(jobs []protocol.JobInfo, filter, activeSession string, scopeAll bool) []protocol.JobInfo {
	q := strings.ToLower(strings.TrimSpace(filter))
	var out []protocol.JobInfo
	for _, j := range jobs {
		if !scopeAll && j.Session != activeSession {
			continue
		}
		if q != "" {
			hay := strings.ToLower(j.Command + " " + j.Label + " " + j.Session)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, j)
	}
	return out
}

func sortJobs(jobs []protocol.JobInfo) []protocol.JobInfo {
	out := append([]protocol.JobInfo(nil), jobs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func anchorCursor(rows []protocol.JobInfo, cursorID, prevCursor int) (int, int) {
	if len(rows) == 0 {
		return 0, 0
	}
	for i, j := range rows {
		if j.ID == cursorID {
			return i, cursorID
		}
	}
	nc := prevCursor
	if nc >= len(rows) {
		nc = len(rows) - 1
	}
	if nc < 0 {
		nc = 0
	}
	return nc, rows[nc].ID
}

func clampOffset(cursor, offset, bodyH, total int) int {
	if bodyH < 1 {
		bodyH = 1
	}
	if total <= bodyH {
		return 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+bodyH {
		offset = cursor - bodyH + 1
	}
	if offset < 0 {
		offset = 0
	}
	if maxOff := total - bodyH; offset > maxOff {
		offset = maxOff
	}
	return offset
}

func clampScroll(offset, bodyH, total int) int {
	if bodyH < 1 {
		bodyH = 1
	}
	maxOff := total - bodyH
	if maxOff < 0 {
		maxOff = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOff {
		offset = maxOff
	}
	return offset
}

// --- layout math ----------------------------------------------------------

func (m model) jobsBodyHeight() int {
	footer := 1
	input := 0
	if m.jobs.filtering || m.jobs.commanding {
		input = 1
	}
	boxH := m.height - footer - input - 1 // -1 for the hardware status bar
	// The job-info pane is always shown beneath the table.
	body := boxH - 3 - jobsDetailHeight // top border + header + bottom border
	if body < 1 {
		body = 1
	}
	return body
}

// --- key handling ---------------------------------------------------------

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
		if r, ok := m.jobsCursorRow(); ok && r.kind == rowJob {
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
		// Edit the cursor row in one box: a job row gets job name + session +
		// group; a session row gets session name + group.
		if r, ok := m.jobsCursorRow(); ok {
			if r.kind == rowJob {
				return m.openJobEditForm(r.job, r.group), textinput.Blink
			}
			return m.openSessionForm(false, r.session, r.group), textinput.Blink
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

// activateRow handles Enter: a job row opens its output pager (like `o`); an
// empty-session placeholder row opens that session's shell panes. Sessions with
// jobs are reachable via the picker (`S`).
func (m model) activateRow() (tea.Model, tea.Cmd) {
	r, ok := m.jobsCursorRow()
	if !ok {
		return m, nil
	}
	if r.kind == rowJob {
		return m, openPagerCmd(r.job)
	}
	return m.openSession(r.session)
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
			if r.kind == rowJob && m.jobs.tagged[r.job.ID] {
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
			if i >= 0 && m.jobs.rows[i].kind == rowJob {
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
