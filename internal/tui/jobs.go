package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/format"
	"github.com/han/qrush/internal/protocol"
)

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

// mgmtRowKind tags a row in the unified group→session→job management list.
type mgmtRowKind int

const (
	rowGroup mgmtRowKind = iota
	rowSession
	rowJob
)

// mgmtRow is one visible line in the management list. Group and session rows
// index back into m.nodes; job rows carry their JobInfo payload directly.
type mgmtRow struct {
	kind       mgmtRowKind
	nodeIdx    int              // index into m.nodes (all kinds)
	sessionIdx int              // index into node.sessions (session/job rows)
	session    string           // session name (session/job rows)
	group      string           // group name (all kinds)
	job        protocol.JobInfo // populated for rowJob
	depth      int              // indentation level
}

// jobsView holds all state for the full-screen job-management modal.
type jobsView struct {
	mode    jobsSubMode
	allJobs []protocol.JobInfo // raw, refreshed each tick
	rows    []mgmtRow          // derived: flattened visible group/session/job rows

	cursorKey string // selection anchored by composite key, not index
	cursor    int
	offset    int

	scopeAll  bool
	filtering bool
	filter    string

	visual bool // visual (multi-select) mode active
	anchor int  // row index where the visual selection started

	pending      byte // first key of a two-key motion: 'g' (gg) or 'd' (dd)
	pendingTimer int

	confirm confirmState
	form    sessionForm
	pager   pagerState
}

// sessionForm is the small modal for creating or editing a session (name +
// group). It is shown over the management list.
type sessionForm struct {
	active     bool
	creating   bool // blank form → SessionCreate; else rename/move
	origName   string
	origGroup  string
	nameInput  textinput.Model
	groupInput textinput.Model
	focusField int // 0 = name, 1 = group
}

type pagerState struct {
	jobID     int
	cmd       string
	info      protocol.JobInfo
	showInfo  bool
	path      string
	lines     []string
	offset    int
	follow    bool
	running   bool
	truncated bool
	noOutput  bool
	loadErr   error
	searching bool
	search    string
}

type columnWidths struct {
	id, state, tm, start, end, session, command int
}

type jobsGTimeoutMsg struct{ id int }

type pagerLoadedMsg struct {
	id        int
	cmd       string
	info      protocol.JobInfo
	path      string
	lines     []string
	running   bool
	truncated bool
	noOutput  bool
	err       error
}

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

// buildMgmtRows flattens the group→session→job tree into the visible rows,
// honouring group and per-session expansion plus the active job filter/scope.
func (m model) buildMgmtRows() []mgmtRow {
	var out []mgmtRow
	for ni, node := range m.nodes {
		out = append(out, mgmtRow{kind: rowGroup, nodeIdx: ni, group: node.group})
		if !node.expanded {
			continue
		}
		for si, session := range node.sessions {
			out = append(out, mgmtRow{
				kind: rowSession, nodeIdx: ni, sessionIdx: si,
				session: session.Name, group: node.group, depth: 1,
			})
			if !m.sessionExpanded[session.Name] {
				continue
			}
			jobs := sortJobs(filterJobs(node.jobs[session.Name], m.jobs.filter, session.Name, true))
			for _, j := range jobs {
				out = append(out, mgmtRow{
					kind: rowJob, nodeIdx: ni, sessionIdx: si,
					session: session.Name, group: node.group, job: j, depth: 2,
				})
			}
		}
	}
	return out
}

// rowKey is a stable identity for a row so the cursor survives refresh and
// expand/collapse.
func rowKey(r mgmtRow) string {
	switch r.kind {
	case rowGroup:
		return "g:" + r.group
	case rowSession:
		return "s:" + r.session
	default:
		return fmt.Sprintf("j:%d", r.job.ID)
	}
}

func (m *model) refreshJobsRows() {
	rows := m.buildMgmtRows()
	m.jobs.rows = rows
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

func computeJobColumns(inner int) columnWidths {
	c := columnWidths{id: 5, state: 11, tm: 8, start: 8, end: 8, session: 14}
	const sep = 6 // single space between each of the seven columns
	fixedNoCmd := func() int { return c.id + c.state + c.tm + c.start + c.end + c.session + sep }
	c.command = inner - fixedNoCmd()
	if c.command < 10 {
		need := 10 - c.command
		c.session -= need
		if c.session < 6 {
			c.session = 6
		}
		c.command = inner - fixedNoCmd()
	}
	if c.command < 1 {
		c.command = 1
	}
	return c
}

func formatJobRow(j protocol.JobInfo, c columnWidths) string {
	return renderJobsRow(j, c, nil)
}

// renderJobsRow renders one table row. When bg is non-nil the row is drawn as a
// highlight: every cell — and the padding within it — carries that background,
// while each cell keeps its own semantic foreground. This lets the focused row
// stay tinted without flattening the state colors (running=green, failed=red).
func renderJobsRow(j protocol.JobInfo, c columnWidths, bg lipgloss.TerminalColor) string {
	timeStr := ""
	if j.State == protocol.StateRunning || j.State == protocol.StateFinished {
		timeStr = format.Duration(j.Result.RealTimeMS)
	}
	cmd := j.Command
	if j.Label != "" {
		cmd = "[" + j.Label + "] " + j.Command
	}
	stateTxt, stateStyle := jobStateText(j)
	cell := func(text string, w int, style lipgloss.Style) string {
		if bg != nil {
			style = style.Background(bg)
		}
		return style.Render(fitToWidth(stripAnsi(text), w))
	}
	sep := " "
	if bg != nil {
		sep = lipgloss.NewStyle().Background(bg).Render(" ")
	}
	id := cell(fmt.Sprintf("%*d", c.id, j.ID), c.id, jobIDStyle)
	state := cell(stateTxt, c.state, stateStyle)
	tm := cell(timeStr, c.tm, lipgloss.NewStyle())
	start := cell(clockOrEmpty(j.StartTime), c.start, lipgloss.NewStyle())
	end := cell(clockOrEmpty(j.EndTime), c.end, lipgloss.NewStyle())
	sess := cell(j.Session, c.session, lipgloss.NewStyle())
	command := cell(cmd, c.command, lipgloss.NewStyle())
	return id + sep + state + sep + tm + sep + start + sep + end + sep + sess + sep + command
}

// padRowBg extends a highlighted row's background to the full inner width. For
// unhighlighted rows (bg == nil) it is a no-op — jobsBordered pads those.
func padRowBg(s string, width int, bg lipgloss.TerminalColor) string {
	if bg == nil {
		return s
	}
	if w := lipgloss.Width(s); w < width {
		return s + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", width-w))
	}
	return s
}

// clockOrEmpty renders a timestamp as HH:MM:SS, or blank if unset.
func clockOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04:05")
}

func jobsHeaderRow(c columnWidths) string {
	row := fmt.Sprintf("%*s", c.id, "ID") + " " +
		fitToWidth("STATE", c.state) + " " +
		fitToWidth("TIME", c.tm) + " " +
		fitToWidth("START", c.start) + " " +
		fitToWidth("END", c.end) + " " +
		fitToWidth("SESSION", c.session) + " " +
		fitToWidth("COMMAND", c.command)
	return treeSummaryStyle.Render(row)
}

func dropPartialFirstLine(data []byte) []byte {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[i+1:]
	}
	return data
}

func pagerSplitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	out := make([]string, len(raw))
	for i, l := range raw {
		out[i] = strings.ReplaceAll(stripAnsi(l), "\t", "    ")
	}
	return out
}

func findMatches(lines []string, q string) []int {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var out []int
	for i, l := range lines {
		if strings.Contains(strings.ToLower(l), q) {
			out = append(out, i)
		}
	}
	return out
}

// --- layout math ----------------------------------------------------------

func (m model) jobsBodyHeight() int {
	footer := 1
	filter := 0
	if m.jobs.filtering {
		filter = 1
	}
	boxH := m.height - footer - filter - 1 // -1 for the hardware status bar
	// The job-info pane is always shown beneath the table.
	body := boxH - 4 - jobsDetailHeight // top border + summary + header + bottom border
	if body < 1 {
		body = 1
	}
	return body
}

func (m model) pagerBodyHeight() int {
	footer := 1
	search := 0
	if m.jobs.pager.searching {
		search = 1
	}
	info := len(m.pagerInfoLines(m.width - 2))
	body := m.height - 2 - footer - search - info // top + bottom border
	if body < 1 {
		body = 1
	}
	return body
}

// pagerInfoLines renders the job-info panel shown at the top of the pager when
// toggled on with `i`. Returns nil when the panel is hidden.
func (m model) pagerInfoLines(inner int) []string {
	if !m.jobs.pager.showInfo || inner < 1 {
		return nil
	}
	j := m.jobs.pager.info
	info := strings.Split(strings.TrimRight(format.FormatJobInfo(&j), "\n"), "\n")
	out := make([]string, 0, len(info)+1)
	out = append(out, treeSummaryStyle.Render(fitToWidth("── job info ", inner)))
	for _, il := range info {
		out = append(out, truncateToWidth(il, inner))
	}
	return out
}

// --- key handling ---------------------------------------------------------

func (m model) handleJobsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.jobs.mode == jobsPager {
		return m.handlePagerKey(msg)
	}

	if m.jobs.form.active {
		return m.handleSessionFormKey(msg)
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

	// Complete a pending two-key motion (gg jumps to top).
	if m.jobs.pending != 0 {
		op := m.jobs.pending
		m.jobs.pending = 0
		if op == 'g' && key == "g" {
			(&m).jobsGoto(0)
			return m, nil
		}
		// not a completion — fall through and process key normally
	}

	switch key {
	case "esc":
		if m.jobs.visual {
			m.jobs.visual = false
			return m, nil
		}
		return m.leaveManagement()
	case "q":
		return m.leaveManagement()
	case "j", "down", " ":
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
	case "l", "right":
		return m.expandRow(true)
	case "h", "left":
		return m.expandRow(false)
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
	case "e":
		if r, ok := m.jobsCursorRow(); ok && r.kind == rowSession {
			return m.openSessionForm(false, r.session, r.group), textinput.Blink
		}
	case "n":
		return m.openSessionForm(true, "", ""), textinput.Blink
	case "x":
		if ids := m.jobsActionIDs(); len(ids) > 0 {
			m.jobs.visual = false
			return m, killJobs(ids)
		}
	case "u":
		if ids := m.jobsActionIDs(); len(ids) > 0 {
			m.jobs.visual = false
			return m, makeUrgentJobs(ids)
		}
	case "r":
		// On a session row, reset that session's shell; on job rows, rerun.
		if r, ok := m.jobsCursorRow(); ok && r.kind == rowSession && !m.jobs.visual {
			nm, cmd := m.resetSessionShell(r.session)
			return nm, cmd
		}
		if ids := m.jobsActionIDs(); len(ids) > 0 {
			m.jobs.visual = false
			return m, rerunJobs(ids)
		}
	case "d":
		// Session/group rows delete the container (with confirmation); job rows
		// remove jobs (finished ones without a prompt).
		if r, ok := m.jobsCursorRow(); ok && !m.jobs.visual {
			switch r.kind {
			case rowSession:
				m.jobs.confirm = confirmState{kind: confirmDeleteSession, session: r.session}
				return m, nil
			case rowGroup:
				m.jobs.confirm = confirmState{kind: confirmDeleteGroup, group: r.group}
				return m, nil
			}
		}
		ids := m.jobsActionIDs()
		if len(ids) == 0 {
			return m, nil
		}
		allFinished := m.actionAllFinished()
		m.jobs.visual = false
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
	case "enter", "o":
		return m.activateRow()
	}
	return m, nil
}

// mgmtBodyTopLine is the terminal row (0-based) where the first list row is
// drawn: after the top border, summary line, and column header.
func (m model) mgmtBodyTopLine() int { return 3 }

// handleMouse maps clicks and wheel scrolls on the management screen to row
// selection/activation. Mouse events are only captured while this screen shows.
func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.viewMode != viewJobs || m.jobs.mode != jobsTable || m.jobs.form.active || m.jobs.filtering {
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
		return m.activateRow()
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

// activateRow acts on the cursor row: groups/sessions toggle or open, jobs open
// the output pager.
func (m model) activateRow() (tea.Model, tea.Cmd) {
	r, ok := m.jobsCursorRow()
	if !ok {
		return m, nil
	}
	switch r.kind {
	case rowGroup:
		m.nodes[r.nodeIdx].expanded = !m.nodes[r.nodeIdx].expanded
		m.refreshJobsRows()
		return m, nil
	case rowSession:
		return m.openSession(r.session)
	default: // rowJob
		return m, openPagerCmd(r.job)
	}
}

// openSession leaves the management view and activates the given session,
// building or restoring its terminal panes.
func (m model) openSession(name string) (tea.Model, tea.Cmd) {
	m.viewMode = viewSplit
	m.mouseOn = false
	nm, cmd := m.activateSession(name)
	return nm, tea.Batch(cmd, tea.DisableMouse)
}

// expandRow opens or closes the group/session under the cursor. Collapsing a
// job row folds its parent session.
func (m model) expandRow(open bool) (tea.Model, tea.Cmd) {
	r, ok := m.jobsCursorRow()
	if !ok {
		return m, nil
	}
	switch r.kind {
	case rowGroup:
		m.nodes[r.nodeIdx].expanded = open
	case rowSession:
		m.sessionExpanded[r.session] = open
	case rowJob:
		if !open {
			m.sessionExpanded[r.session] = false
		}
	}
	m.refreshJobsRows()
	return m, nil
}

func (m model) startPending(op byte) (tea.Model, tea.Cmd) {
	m.jobs.pending = op
	m.jobs.pendingTimer++
	id := m.jobs.pendingTimer
	return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return jobsGTimeoutMsg{id: id} })
}

// openSessionForm shows the create/edit session modal, pre-filled when editing.
func (m model) openSessionForm(creating bool, name, group string) model {
	ni := textinput.New()
	ni.CharLimit = 64
	ni.Prompt = ""
	ni.SetValue(name)
	ni.Focus()
	gi := textinput.New()
	gi.CharLimit = 64
	gi.Prompt = ""
	gi.SetValue(group)
	m.jobs.form = sessionForm{
		active:     true,
		creating:   creating,
		origName:   name,
		origGroup:  group,
		nameInput:  ni,
		groupInput: gi,
		focusField: 0,
	}
	return m
}

func (m model) handleSessionFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.jobs.form
	switch msg.String() {
	case "esc":
		m.jobs.form = sessionForm{}
		return m, nil
	case "tab", "shift+tab", "up", "down":
		f.focusField = 1 - f.focusField
		if f.focusField == 0 {
			f.nameInput.Focus()
			f.groupInput.Blur()
		} else {
			f.groupInput.Focus()
			f.nameInput.Blur()
		}
		return m, textinput.Blink
	case "enter":
		return m.submitSessionForm()
	}
	var cmd tea.Cmd
	if f.focusField == 0 {
		f.nameInput, cmd = f.nameInput.Update(msg)
	} else {
		f.groupInput, cmd = f.groupInput.Update(msg)
	}
	return m, cmd
}

func (m model) submitSessionForm() (tea.Model, tea.Cmd) {
	f := m.jobs.form
	name := strings.TrimSpace(f.nameInput.Value())
	group := strings.TrimSpace(f.groupInput.Value())
	creating := f.creating
	m.jobs.form = sessionForm{}
	if name == "" {
		m.status = "session name required"
		return m, nil
	}
	if creating {
		return m, createSessionInGroup(name, group)
	}
	if name == f.origName && group == f.origGroup {
		return m, nil
	}
	return m, editSession(f.origName, name, f.origGroup, group)
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

func killJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, killJob(id))
	}
	return tea.Batch(cmds...)
}

func makeUrgentJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, makeUrgent(id))
	}
	return tea.Batch(cmds...)
}

func removeJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, removeJob(id))
	}
	return tea.Batch(cmds...)
}

func rerunJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, rerunJob(id))
	}
	return tea.Batch(cmds...)
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

func (m model) handlePagerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.jobs.pager
	if p.searching {
		switch msg.String() {
		case "enter":
			p.searching = false
			p.search = m.textInput.Value()
			m.textInput.Blur()
			(&m).pagerSearch(true)
			return m, nil
		case "esc":
			p.searching = false
			m.textInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	key := msg.String()
	bodyH := m.pagerBodyHeight()
	if m.jobs.pending == 'g' {
		m.jobs.pending = 0
		if key == "g" {
			p.follow = false
			p.offset = 0
			return m, nil
		}
	}

	switch key {
	case "q", "esc":
		m.jobs.mode = jobsTable
		m.jobs.pager = pagerState{}
		return m, nil
	case "j", "down":
		p.follow = false
		p.offset = clampScroll(p.offset+1, bodyH, len(p.lines))
	case "k", "up":
		p.follow = false
		p.offset = clampScroll(p.offset-1, bodyH, len(p.lines))
	case "ctrl+d":
		p.follow = false
		p.offset = clampScroll(p.offset+bodyH/2, bodyH, len(p.lines))
	case "ctrl+u":
		p.follow = false
		p.offset = clampScroll(p.offset-bodyH/2, bodyH, len(p.lines))
	case "g":
		return m.startPending('g')
	case "G":
		p.offset = clampScroll(len(p.lines), bodyH, len(p.lines))
		if p.running {
			p.follow = true
		}
	case "/":
		p.searching = true
		m.textInput.SetValue("")
		m.textInput.Focus()
		return m, textinput.Blink
	case "n":
		(&m).pagerSearch(true)
	case "N":
		(&m).pagerSearch(false)
	case "i":
		p.showInfo = !p.showInfo
		p.offset = clampScroll(p.offset, m.pagerBodyHeight(), len(p.lines))
	}
	return m, nil
}

func (m *model) pagerSearch(forward bool) {
	p := &m.jobs.pager
	matches := findMatches(p.lines, p.search)
	if len(matches) == 0 {
		return
	}
	target := matches[0]
	if forward {
		target = matches[0]
		for _, idx := range matches {
			if idx > p.offset {
				target = idx
				break
			}
		}
	} else {
		target = matches[len(matches)-1]
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i] < p.offset {
				target = matches[i]
				break
			}
		}
	}
	p.follow = false
	p.offset = clampScroll(target, m.pagerBodyHeight(), len(p.lines))
}

// --- output pager commands ------------------------------------------------

func openPagerCmd(j protocol.JobInfo) tea.Cmd {
	id := j.ID
	cmd := j.Command
	running := j.State == protocol.StateRunning
	return func() tea.Msg {
		path, err := client.GetOutput(id)
		if err != nil {
			return pagerLoadedMsg{id: id, cmd: cmd, info: j, running: running, err: err}
		}
		if path == "" {
			return pagerLoadedMsg{id: id, cmd: cmd, info: j, running: running, noOutput: true}
		}
		lines, truncated, rerr := readPagerFile(path)
		return pagerLoadedMsg{id: id, cmd: cmd, info: j, path: path, running: running, lines: lines, truncated: truncated, err: rerr}
	}
}

func (m model) reloadPagerCmd() tea.Cmd {
	id := m.jobs.pager.jobID
	job := m.jobs.pager.info
	for _, j := range m.jobs.allJobs {
		if j.ID == id {
			job = j
			break
		}
	}
	return openPagerCmd(job)
}

func readPagerFile(path string) ([]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	size := st.Size()
	truncated := size > pagerByteCap
	if truncated {
		_, _ = f.Seek(size-pagerByteCap, io.SeekStart)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	if truncated {
		data = dropPartialFirstLine(data)
	}
	return pagerSplitLines(data), truncated, nil
}

func (m model) applyPagerLoaded(msg pagerLoadedMsg) model {
	if m.viewMode != viewJobs {
		return m
	}
	fresh := m.jobs.mode != jobsPager
	p := &m.jobs.pager
	p.jobID = msg.id
	p.cmd = msg.cmd
	p.info = msg.info
	p.path = msg.path
	p.running = msg.running
	p.truncated = msg.truncated
	p.noOutput = msg.noOutput
	p.loadErr = msg.err
	p.lines = msg.lines
	m.jobs.mode = jobsPager
	if fresh {
		p.follow = msg.running
		p.offset = 0
	}
	if p.follow {
		p.offset = clampScroll(len(p.lines), m.pagerBodyHeight(), len(p.lines))
	} else {
		p.offset = clampScroll(p.offset, m.pagerBodyHeight(), len(p.lines))
	}
	return m
}

// --- rendering ------------------------------------------------------------

func (m model) renderJobsView(w, h int) string {
	if m.jobs.mode == jobsPager {
		return m.renderPager(w, h)
	}
	if w < 8 || h < 4 {
		return strings.Repeat("\n", max(0, h-1))
	}
	inner := w - 2
	cols := computeJobColumns(inner)
	bodyH := m.jobsBodyHeight()
	lines := make([]string, 0, h)

	lines = append(lines, boxedTop(w, " jobs & sessions ", focusBorderStyle))
	lines = append(lines, m.jobsBordered(m.jobsSummaryLine(inner), inner))
	lines = append(lines, m.jobsBordered(jobsHeaderStyle.Render(fitToWidth(jobsHeaderRow(cols), inner)), inner))

	body := m.mgmtBodyLines(bodyH, inner, cols)
	for _, bl := range body {
		lines = append(lines, m.jobsBordered(bl, inner))
	}

	for _, dl := range m.jobsDetailLines(jobsDetailHeight, inner) {
		lines = append(lines, m.jobsBordered(dl, inner))
	}

	lines = append(lines, boxedBottom(w, focusBorderStyle))
	if m.jobs.filtering {
		lines = append(lines, fitToWidth(inputStyle.Render("/")+m.textInput.View(), w))
	}
	lines = append(lines, m.jobsFooter(w))
	lines = append(lines, m.renderHWBar(w))

	return joinExact(lines, w, h)
}

// mgmtBodyLines renders the visible window of the group→session→job list (or the
// edit-form overlay when active), returning exactly bodyH lines of width inner.
func (m model) mgmtBodyLines(bodyH, inner int, cols columnWidths) []string {
	if m.jobs.form.active {
		return m.sessionFormLines(bodyH, inner)
	}
	out := make([]string, 0, bodyH)
	if len(m.jobs.rows) == 0 {
		for i := 0; i < bodyH; i++ {
			if i == bodyH/2 {
				out = append(out, treeEmptyStyle.Render(centerText("(no sessions)", inner)))
			} else {
				out = append(out, "")
			}
		}
		return out
	}
	selLo, selHi := -1, -1
	if m.jobs.visual {
		selLo, selHi = m.visualRange()
	}
	for i := 0; i < bodyH; i++ {
		idx := m.jobs.offset + i
		if idx >= len(m.jobs.rows) {
			out = append(out, "")
			continue
		}
		r := m.jobs.rows[idx]
		isCursor := idx == m.jobs.cursor
		inSel := m.jobs.visual && r.kind == rowJob && idx >= selLo && idx <= selHi
		out = append(out, m.renderMgmtRow(r, cols, inner, isCursor, inSel))
	}
	return out
}

// renderMgmtRow renders one management-list row with the right highlight. Job
// rows keep their per-column semantic colors under the focus tint; group and
// session headers are free-form lines.
func (m model) renderMgmtRow(r mgmtRow, cols columnWidths, inner int, isCursor, inSel bool) string {
	if r.kind == rowJob {
		switch {
		case isCursor:
			return padRowBg(renderJobsRow(r.job, cols, cRowFocusBg), inner, cRowFocusBg)
		case inSel:
			return selectedStyle.Render(fitToWidth(stripAnsi(formatJobRow(r.job, cols)), inner))
		default:
			return formatJobRow(r.job, cols)
		}
	}
	content := m.mgmtHeaderLine(r)
	if isCursor {
		return lipgloss.NewStyle().Background(cRowFocusBg).Render(fitToWidth(stripAnsi(content), inner))
	}
	return content
}

// mgmtHeaderLine formats a group or session header row.
func (m model) mgmtHeaderLine(r mgmtRow) string {
	node := m.nodes[r.nodeIdx]
	if r.kind == rowGroup {
		icon := "▸"
		if node.expanded {
			icon = "▾"
		}
		return fmt.Sprintf(" %s  %s  %s",
			treeIconStyle.Render(icon),
			groupStyle.Render(node.group),
			treeSummaryStyle.Render(groupSummary(node)))
	}
	session := node.sessions[r.sessionIdx]
	caret := "▸"
	if m.sessionExpanded[session.Name] {
		caret = "▾"
	}
	marker := ""
	if session.Name == m.activeSession && m.activeSession != "" {
		marker = runningStyle.Render(" ● open")
	}
	return fmt.Sprintf("   %s %s  %s%s",
		treeIconStyle.Render(caret),
		sessionStyle.Render(session.Name),
		treeSummaryStyle.Render(sessionSummary(node.jobs[session.Name])),
		marker)
}

// sessionFormLines renders the create/edit-session modal centered in the body.
func (m model) sessionFormLines(bodyH, inner int) []string {
	f := m.jobs.form
	title := " edit session "
	if f.creating {
		title = " new session "
	}
	field := func(label string, ti textinput.Model, focused bool) string {
		box := ti.View()
		lbl := jobsDetailKeyStyle.Render(label)
		if focused {
			lbl = cursorStyle.Render(label)
		}
		return truncateToWidth("  "+lbl+" "+box, inner)
	}
	content := []string{
		jobsDetailRuleStyle.Render(fitToWidth("── "+strings.TrimSpace(title)+" ", inner)),
		"",
		field("name: ", f.nameInput, f.focusField == 0),
		field("group:", f.groupInput, f.focusField == 1),
		"",
		treeEmptyStyle.Render(centerText("tab: switch field · enter: save · esc: cancel", inner)),
	}
	out := make([]string, 0, bodyH)
	top := (bodyH - len(content)) / 2
	if top < 0 {
		top = 0
	}
	for i := 0; i < bodyH; i++ {
		ci := i - top
		if ci >= 0 && ci < len(content) {
			out = append(out, content[ci])
		} else {
			out = append(out, "")
		}
	}
	return out
}

// renderHWBar is the system-wide hardware status bar shown at the bottom of the
// job view: CPU, memory, load average, and core count.
func (m model) renderHWBar(w int) string {
	s := m.hwStats
	left := []statusSegment{{text: " HW ", style: modeCommandStyle}}
	if !s.CPUOK && !s.MemOK && !s.LoadOK {
		left = append(left, statusSegment{text: " gathering… (or unavailable on this platform) ", style: airlineMuted})
		return renderAirline(w, left, nil)
	}
	if s.CPUOK {
		style := airlineInfo
		if s.CPUPercent >= 90 {
			style = airlineError
		}
		left = append(left, statusSegment{
			text:  fmt.Sprintf(" CPU %3.0f%% %s ", s.CPUPercent, gauge(s.CPUPercent, 8)),
			style: style,
		})
	}
	if s.MemOK {
		style := airlineInfo
		if s.MemTotal > 0 && float64(s.MemUsed)/float64(s.MemTotal) >= 0.9 {
			style = airlineError
		}
		memText := fmt.Sprintf(" MEM %s ", humanBytes(s.MemTotal))
		if s.MemUsed > 0 {
			memText = fmt.Sprintf(" MEM %s/%s ", humanBytes(s.MemUsed), humanBytes(s.MemTotal))
		}
		left = append(left, statusSegment{text: memText, style: style})
	}

	var right []statusSegment
	if s.LoadOK {
		right = append(right, statusSegment{
			text:  fmt.Sprintf(" load %.2f %.2f %.2f ", s.Load[0], s.Load[1], s.Load[2]),
			style: airlineInfo,
		})
	}
	if s.NumCPU > 0 {
		right = append(right, statusSegment{text: fmt.Sprintf(" %d cpu ", s.NumCPU), style: airlineMuted})
	}
	return renderAirline(w, left, right)
}

// gauge renders a fixed-width █/░ bar for a 0..100 percentage.
func gauge(pct float64, width int) string {
	if width < 1 {
		return ""
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// humanBytes formats a byte count with a compact binary suffix (e.g. 28.5G).
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"K", "M", "G", "T", "P"}
	if exp >= len(suffix) {
		exp = len(suffix) - 1
	}
	return fmt.Sprintf("%.1f%s", float64(b)/float64(div), suffix[exp])
}

func (m model) jobsBordered(content string, inner int) string {
	b := focusBorderStyle.Render("│")
	return b + fitToWidth(content, inner) + b
}

func (m model) jobsSummaryLine(inner int) string {
	jobs := m.visibleJobs()
	running, queued, finished, failed := jobStateCounts(jobs)
	sessions := 0
	for _, n := range m.nodes {
		sessions += len(n.sessions)
	}
	parts := []string{
		fmt.Sprintf("sessions %d", sessions),
		fmt.Sprintf("running %d", running),
		fmt.Sprintf("queued %d", queued),
		fmt.Sprintf("done %d", finished),
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("failed %d", failed))
	}
	if m.jobs.filter != "" {
		parts = append(parts, "/"+m.jobs.filter)
	}
	return jobsSummaryStyle.Render(fitToWidth(" "+strings.Join(parts, "  ·  ")+" ", inner))
}

func jobStateCounts(jobs []protocol.JobInfo) (running, queued, finished, failed int) {
	for _, j := range jobs {
		switch j.State {
		case protocol.StateRunning:
			running++
		case protocol.StateQueued, protocol.StateAllocating:
			queued++
		case protocol.StateFinished:
			if j.Result.DiedBySignal || j.Result.ExitCode != 0 {
				failed++
			} else {
				finished++
			}
		default:
			finished++
		}
	}
	return
}

func (m model) jobsDetailLines(maxLines, inner int) []string {
	out := make([]string, 0, maxLines)
	rule := func(title string) {
		r := title + strings.Repeat("─", max(0, inner-lipgloss.Width(title)))
		out = append(out, jobsDetailRuleStyle.Render(fitToWidth(r, inner)))
	}
	if j, ok := m.jobsSelected(); ok {
		rule(" job details ")
		info := strings.Split(strings.TrimRight(format.FormatJobInfo(&j), "\n"), "\n")
		for _, il := range info {
			if len(out) >= maxLines {
				break
			}
			out = append(out, styleJobDetailLine(il, inner))
		}
	} else if r, ok := m.jobsCursorRow(); ok && r.kind == rowSession {
		rule(" session ")
		node := m.nodes[r.nodeIdx]
		jobs := node.jobs[r.session]
		running, queued, finished := jobCounts(jobs)
		out = append(out, styleJobDetailLine(fmt.Sprintf("name: %s", r.session), inner))
		out = append(out, styleJobDetailLine(fmt.Sprintf("group: %s", node.group), inner))
		out = append(out, styleJobDetailLine(fmt.Sprintf("jobs: %d  running %d  queued %d  done %d", len(jobs), running, queued, finished), inner))
		out = append(out, styleJobDetailLine("open: enter · edit: e · delete: d", inner))
	} else if r, ok := m.jobsCursorRow(); ok && r.kind == rowGroup {
		rule(" group ")
		node := m.nodes[r.nodeIdx]
		out = append(out, styleJobDetailLine(fmt.Sprintf("name: %s", node.group), inner))
		out = append(out, styleJobDetailLine(groupSummary(node), inner))
	}
	for len(out) < maxLines {
		out = append(out, "")
	}
	return out[:maxLines]
}

func styleJobDetailLine(line string, inner int) string {
	if k, v, ok := strings.Cut(line, ":"); ok {
		key := jobsDetailKeyStyle.Render(k + ":")
		value := strings.TrimLeft(v, " ")
		return fitToWidth(key+" "+truncateToWidth(value, max(0, inner-lipgloss.Width(stripAnsi(key))-1)), inner)
	}
	return truncateToWidth(line, inner)
}

func (m model) jobsFooter(w int) string {
	if m.jobs.confirm.kind != confirmNone {
		var q string
		switch m.jobs.confirm.kind {
		case confirmRemove:
			if n := len(m.jobs.confirm.ids); n == 1 {
				q = fmt.Sprintf(" remove job %d? (y/n) ", m.jobs.confirm.ids[0])
			} else {
				q = fmt.Sprintf(" remove %d jobs? (y/n) ", n)
			}
		case confirmDeleteSession:
			q = fmt.Sprintf(" delete session %q? (y/n) ", m.jobs.confirm.session)
		case confirmDeleteGroup:
			q = fmt.Sprintf(" delete group %q? (y/n) ", m.jobs.confirm.group)
		default:
			q = " clear all finished jobs? (y/n) "
		}
		return renderAirline(w, []statusSegment{
			{text: " CONFIRM ", style: airlineError},
			{text: q, style: airlineInfo},
		}, nil)
	}
	running, queued, finished, failed := jobStateCounts(m.visibleJobs())
	modeText, modeStyle := " MANAGE ", modeCommandStyle
	if m.jobs.visual {
		lo, hi := m.visualRange()
		modeText, modeStyle = fmt.Sprintf(" VISUAL %d ", hi-lo+1), modeInsertStyle
	}
	left := []statusSegment{
		{text: modeText, style: modeStyle},
	}
	if r, ok := m.jobsCursorRow(); ok {
		switch r.kind {
		case rowGroup:
			left = append(left, statusSegment{text: " group ", style: airlineFocus})
		case rowSession:
			left = append(left, statusSegment{text: " session ", style: airlineFocus})
		default:
			left = append(left, statusSegment{text: " job ", style: airlineFocus})
		}
	}
	if m.jobs.filter != "" {
		left = append(left, statusSegment{text: fmt.Sprintf(" /%s ", m.jobs.filter), style: airlineInfo})
	}
	right := []statusSegment{
		{text: fmt.Sprintf(" run %d  queue %d  done %d  fail %d ", running, queued, finished, failed), style: airlineMuted},
		{text: " j/k h/l ⏎:open e:edit n:new / x d q ", style: airlineFocus},
	}
	return renderAirline(w, left, right)
}

func (m model) renderPager(w, h int) string {
	if w < 8 || h < 4 {
		return strings.Repeat("\n", max(0, h-1))
	}
	inner := w - 2
	p := m.jobs.pager
	bodyH := m.pagerBodyHeight()
	lines := make([]string, 0, h)

	badge := ""
	switch {
	case p.noOutput:
		badge = "[no output] "
	case p.loadErr != nil:
		badge = "[error] "
	case p.truncated:
		badge = "[truncated] "
	}
	if p.running && p.follow {
		badge += "[following]"
	} else if !p.running {
		badge += "[finished]"
	}
	title := fmt.Sprintf(" output: job %d %s %s", p.jobID, shorten(p.cmd, 24), badge)
	lines = append(lines, boxedTop(w, title, focusBorderStyle))

	for _, il := range m.pagerInfoLines(inner) {
		lines = append(lines, m.jobsBordered(il, inner))
	}

	if p.noOutput || p.loadErr != nil {
		msg := "(no output)"
		if p.loadErr != nil {
			msg = "error: " + p.loadErr.Error()
		}
		for i := 0; i < bodyH; i++ {
			if i == bodyH/2 {
				lines = append(lines, m.jobsBordered(treeEmptyStyle.Render(centerText(msg, inner)), inner))
			} else {
				lines = append(lines, m.jobsBordered("", inner))
			}
		}
	} else {
		for i := 0; i < bodyH; i++ {
			idx := p.offset + i
			if idx >= len(p.lines) {
				lines = append(lines, m.jobsBordered("", inner))
				continue
			}
			lines = append(lines, m.jobsBordered(truncateToWidth(p.lines[idx], inner), inner))
		}
	}

	lines = append(lines, boxedBottom(w, focusBorderStyle))
	if p.searching {
		lines = append(lines, fitToWidth(inputStyle.Render("/")+m.textInput.View(), w))
	}
	pos := fmt.Sprintf(" %d-%d/%d ", min(p.offset+1, len(p.lines)), min(p.offset+bodyH, len(p.lines)), len(p.lines))
	lines = append(lines, renderAirline(w, []statusSegment{
		{text: " PAGER ", style: modeCommandStyle},
		{text: pos, style: airlineFocus},
	}, []statusSegment{
		{text: " j/k gg G /n i:info  q:back ", style: airlineFocus},
	}))

	return joinExact(lines, w, h)
}

// joinExact pads/truncates to exactly h lines of width w and joins them.
func joinExact(lines []string, w, h int) string {
	for len(lines) < h {
		lines = append(lines, fitToWidth("", w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func centerText(s string, w int) string {
	n := lipgloss.Width(s)
	if n >= w {
		return s
	}
	return strings.Repeat(" ", (w-n)/2) + s
}

func shorten(s string, n int) string {
	if lipgloss.Width(s) <= n || n <= 1 {
		return s
	}
	return truncateToWidth(s, n-1) + "…"
}
