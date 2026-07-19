package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
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

// configForm is the settings modal opened by `:config`: the daemon options
// that are editable at runtime (parallel slots, log directory).
type configForm struct {
	active      bool
	slotsInput  textinput.Model
	logdirInput textinput.Model
	origSlots   int
	origLogdir  string
	focusField  int // 0 = slots, 1 = logdir
}

// sessionPicker is the overlay (opened with `s`) that lists every session —
// including empty ones with no jobs — so any session can be opened, edited, or
// created. It is the only place empty sessions are reachable in the flat table.
type sessionPicker struct {
	active bool
	rows   []pickerRow
	cursor int
}

// pickerRow is one line in the session picker: a group header (session == "")
// or a session under it.
type pickerRow struct {
	group   string
	session string
	isGroup bool
	open    bool // the currently-active session
	jobs    int
}

// sessionForm is the edit modal opened with `e`. On a session row (or when
// creating) it edits the session's name + group; on a job row it grows a job
// name field on top, so one box edits everything about the row.
type sessionForm struct {
	active       bool
	creating     bool // blank form → SessionCreate; else rename/move
	groupRename  bool // single-field variant: rename the group in origName
	jobID        int  // >= 0: job row — show the job-name field; -1 otherwise
	origLabel    string
	origName     string
	origGroup    string
	origTimeout  string
	labelInput   textinput.Model // job name; only rendered when jobID >= 0
	nameInput    textinput.Model // session name
	groupInput   textinput.Model
	timeoutInput textinput.Model // job timeout duration; empty = none
	focusField   int             // index into inputs()
}

// fields returns the form's field labels in focus order.
func (f *sessionForm) fields() []string {
	switch {
	case f.groupRename:
		return []string{"name"}
	case f.jobID >= 0:
		return []string{"name", "session", "group", "timeout"}
	default:
		return []string{"name", "group"}
	}
}

// inputs returns pointers to the form's visible fields in focus order.
func (f *sessionForm) inputs() []*textinput.Model {
	switch {
	case f.groupRename:
		return []*textinput.Model{&f.nameInput}
	case f.jobID >= 0:
		return []*textinput.Model{&f.labelInput, &f.nameInput, &f.groupInput, &f.timeoutInput}
	default:
		return []*textinput.Model{&f.nameInput, &f.groupInput}
	}
}

// setFocus focuses field i (wrapping) and blurs the rest.
func (f *sessionForm) setFocus(i int) {
	inputs := f.inputs()
	n := len(inputs)
	f.focusField = ((i % n) + n) % n
	for idx, ti := range inputs {
		if idx == f.focusField {
			ti.Focus()
		} else {
			ti.Blur()
		}
	}
}

// groupFocused reports whether the group field has focus.
func (f *sessionForm) groupFocused() bool {
	fs := f.fields()
	return f.focusField < len(fs) && fs[f.focusField] == "group"
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
	id, group, session, state, tm, timeout, name, command int
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

func computeJobColumns(inner int) columnWidths {
	c := columnWidths{id: 5, group: 10, session: 12, state: 11, tm: 8, timeout: 8, name: 14}
	const sep = 9 // three spaces after ID, single space between the other columns
	fixedNoCmd := func() int {
		return c.id + c.group + c.session + c.state + c.tm + c.timeout + c.name + sep
	}
	c.command = inner - fixedNoCmd()
	if c.command < 10 {
		// Reclaim room from the widest text columns first.
		for _, p := range []*int{&c.name, &c.session, &c.group} {
			if c.command >= 10 {
				break
			}
			need := 10 - c.command
			room := *p - 6
			if room > need {
				room = need
			}
			if room > 0 {
				*p -= room
				c.command = inner - fixedNoCmd()
			}
		}
	}
	if c.command < 1 {
		c.command = 1
	}
	return c
}

func formatJobRow(j protocol.JobInfo, group string, c columnWidths) string {
	return renderJobsRow(j, group, c, nil)
}

// renderJobsRow renders one table row. When bg is non-nil the row is drawn as a
// highlight: every cell — and the padding within it — carries that background,
// while each cell keeps its own semantic foreground. This lets the focused row
// stay tinted without flattening the state colors (running=green, failed=red).
func renderJobsRow(j protocol.JobInfo, group string, c columnWidths, bg lipgloss.TerminalColor) string {
	timeStr := ""
	if j.State == protocol.StateRunning || j.State == protocol.StateFinished {
		timeStr = format.Duration(format.ElapsedMS(j))
	}
	// The label lives in its own NAME column; unnamed jobs echo a dim command
	// snippet there so the column still identifies the row at a glance.
	nameTxt, nameStyle := j.Label, jobNameStyle
	if nameTxt == "" {
		nameTxt, nameStyle = j.Command, treeEmptyStyle
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
	grp := cell(group, c.group, groupStyle)
	sess := cell(j.Session, c.session, sessionStyle)
	state := cell(stateTxt, c.state, stateStyle)
	tm := cell(timeStr, c.tm, lipgloss.NewStyle())
	timeoutTxt, timeoutStyle := "-", treeEmptyStyle
	if j.TimeoutMS > 0 {
		timeoutTxt, timeoutStyle = durationCompact(time.Duration(j.TimeoutMS)*time.Millisecond), queuedStyle
	}
	timeout := cell(timeoutTxt, c.timeout, timeoutStyle)
	name := cell(nameTxt, c.name, nameStyle)
	command := cell(j.Command, c.command, lipgloss.NewStyle())
	return id + sep + sep + sep + grp + sep + sess + sep + state + sep + tm + sep + timeout + sep + name + sep + command
}

// durationCompact renders a duration without trailing zero units (30m0s → 30m).
func durationCompact(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
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

func (m model) jobsHeaderRow(c columnWidths) string {
	arrow := "▲"
	if m.jobs.sortRev {
		arrow = "▼"
	}
	// mark tags the header of the currently-sorted column with the arrow.
	mark := func(title string, active bool) string {
		if active {
			return title + " " + arrow
		}
		return title
	}
	row := fmt.Sprintf("%*s", c.id, mark("ID", m.jobs.sortMode == sortByID)) + "   " +
		fitToWidth(mark("GROUP", m.jobs.sortMode == sortGrouped), c.group) + " " +
		fitToWidth("SESSION", c.session) + " " +
		fitToWidth(mark("STATE", m.jobs.sortMode == sortByState), c.state) + " " +
		fitToWidth(mark("TIME", m.jobs.sortMode == sortByTime), c.tm) + " " +
		fitToWidth("TIMEOUT", c.timeout) + " " +
		fitToWidth("NAME", c.name) + " " +
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

// --- session picker (`s`) -------------------------------------------------

// openSessionPicker builds and shows the session picker overlay, placing the
// cursor on the first session row.
func (m model) openSessionPicker() model {
	m.jobs.picker = sessionPicker{active: true, rows: m.buildPickerRows()}
	m.jobs.picker.cursor = m.jobs.picker.firstSession()
	return m
}

// buildPickerRows lists every group and its sessions — including sessions with
// no jobs, which never appear in the flat job table.
func (m model) buildPickerRows() []pickerRow {
	var rows []pickerRow
	for _, node := range m.nodes {
		rows = append(rows, pickerRow{group: node.group, isGroup: true})
		for _, s := range node.sessions {
			rows = append(rows, pickerRow{
				group:   node.group,
				session: s.Name,
				open:    s.Name == m.activeSession && m.activeSession != "",
				jobs:    len(node.jobs[s.Name]),
			})
		}
	}
	return rows
}

func (p sessionPicker) current() *pickerRow {
	if p.cursor >= 0 && p.cursor < len(p.rows) {
		return &p.rows[p.cursor]
	}
	return nil
}

// firstSession returns the index of the first non-group row (or 0).
func (p sessionPicker) firstSession() int {
	for i, r := range p.rows {
		if !r.isGroup {
			return i
		}
	}
	return 0
}

// step moves the cursor to the next/previous session row, skipping group
// headers; it stays put when there is none in that direction.
func (p sessionPicker) step(from, dir int) int {
	for i := from + dir; i >= 0 && i < len(p.rows); i += dir {
		if !p.rows[i].isGroup {
			return i
		}
	}
	return from
}

func (m model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.jobs.picker
	switch msg.String() {
	case "esc", "S", "q":
		m.jobs.picker = sessionPicker{}
		return m, nil
	case "j", "down":
		p.cursor = p.step(p.cursor, 1)
	case "k", "up":
		p.cursor = p.step(p.cursor, -1)
	case "enter":
		if r := p.current(); r != nil && !r.isGroup {
			m.jobs.picker = sessionPicker{}
			return m.openSession(r.session)
		}
	case "e":
		if r := p.current(); r != nil {
			m.jobs.picker = sessionPicker{}
			if r.isGroup {
				return m.openGroupRenameForm(r.group), textinput.Blink
			}
			return m.openSessionForm(false, r.session, r.group), textinput.Blink
		}
	case "n", "a":
		m.jobs.picker = sessionPicker{}
		return m.openSessionForm(true, "", ""), textinput.Blink
	case "d":
		if r := p.current(); r != nil {
			m.jobs.picker = sessionPicker{}
			if r.isGroup {
				m.jobs.confirm = confirmState{kind: confirmDeleteGroup, group: r.group}
			} else {
				m.jobs.confirm = confirmState{kind: confirmDeleteSession, session: r.session}
			}
			return m, nil
		}
	}
	return m, nil
}

func formInput(value string) textinput.Model {
	ti := newTextInput()
	ti.CharLimit = 64
	ti.Prompt = ""
	ti.SetValue(value)
	ti.CursorEnd()
	return ti
}

// openSessionForm shows the create/edit session modal, pre-filled when editing.
func (m model) openSessionForm(creating bool, name, group string) model {
	ni := formInput(name)
	ni.Focus()
	m.jobs.form = sessionForm{
		active:     true,
		creating:   creating,
		jobID:      -1,
		origName:   name,
		origGroup:  group,
		nameInput:  ni,
		groupInput: formInput(group),
	}
	return m
}

// openGroupRenameForm shows the one-field modal that renames a group.
func (m model) openGroupRenameForm(name string) model {
	ni := formInput(name)
	ni.Focus()
	m.jobs.form = sessionForm{
		active:      true,
		groupRename: true,
		jobID:       -1,
		origName:    name,
		nameInput:   ni,
	}
	return m
}

// openJobEditForm shows the combined edit modal for a job row: the job's name
// plus its session's name and group.
func (m model) openJobEditForm(j protocol.JobInfo, group string) model {
	li := formInput(j.Label)
	li.Focus()
	timeout := ""
	if j.TimeoutMS > 0 {
		timeout = durationCompact(time.Duration(j.TimeoutMS) * time.Millisecond)
	}
	m.jobs.form = sessionForm{
		active:       true,
		jobID:        j.ID,
		origLabel:    j.Label,
		origName:     j.Session,
		origGroup:    group,
		origTimeout:  timeout,
		labelInput:   li,
		nameInput:    formInput(j.Session),
		groupInput:   formInput(group),
		timeoutInput: formInput(timeout),
	}
	return m
}

func (m model) handleSessionFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.jobs.form
	switch msg.String() {
	case "esc":
		m.jobs.form = sessionForm{}
		return m, nil
	case "tab":
		// On the group field, tab cycles through the existing groups so they
		// are discoverable; ↑/↓ and shift+tab still switch fields.
		if f.groupFocused() {
			if g, ok := nextGroup(m.groups, strings.TrimSpace(f.groupInput.Value()), 1); ok {
				f.groupInput.SetValue(g)
				f.groupInput.CursorEnd()
			}
			return m, nil
		}
		f.setFocus(f.focusField + 1)
		return m, textinput.Blink
	case "down":
		f.setFocus(f.focusField + 1)
		return m, textinput.Blink
	case "shift+tab", "up":
		f.setFocus(f.focusField - 1)
		return m, textinput.Blink
	case "enter":
		return m.submitSessionForm()
	}
	var cmd tea.Cmd
	in := f.inputs()[f.focusField]
	*in, cmd = in.Update(msg)
	return m, cmd
}

// nextGroup returns the entry dir (±1) steps after cur in groups, wrapping.
// A value not in the list (or blank) starts from the first/last group.
func nextGroup(groups []string, cur string, dir int) (string, bool) {
	if len(groups) == 0 {
		return "", false
	}
	idx := -1
	for i, g := range groups {
		if g == cur {
			idx = i
			break
		}
	}
	if idx < 0 {
		if dir < 0 {
			return groups[len(groups)-1], true
		}
		return groups[0], true
	}
	return groups[(idx+dir+len(groups))%len(groups)], true
}

func (m model) submitSessionForm() (tea.Model, tea.Cmd) {
	f := m.jobs.form
	name := strings.TrimSpace(f.nameInput.Value())
	group := strings.TrimSpace(f.groupInput.Value())
	m.jobs.form = sessionForm{}
	if name == "" {
		m.status = "session name required"
		return m, nil
	}
	if f.groupRename {
		if name == f.origName {
			return m, nil
		}
		return m, renameGroupCmd(f.origName, name)
	}
	if f.creating {
		// Open the new session as soon as the next tree refresh includes it.
		m.pendingOpen = name
		return m, createSessionInGroup(name, group)
	}

	var cmds []tea.Cmd
	if f.jobID >= 0 {
		if label := strings.TrimSpace(f.labelInput.Value()); label != f.origLabel {
			cmds = append(cmds, setJobLabelCmd(f.jobID, label))
		}
		if timeout := strings.TrimSpace(f.timeoutInput.Value()); timeout != f.origTimeout {
			var ms int64
			if timeout != "" && !strings.EqualFold(timeout, "none") {
				d, err := time.ParseDuration(timeout)
				if err != nil || d <= 0 {
					m.status = fmt.Sprintf("invalid timeout %q (e.g. 30m, 90s)", timeout)
					return m, nil
				}
				ms = d.Milliseconds()
			}
			cmds = append(cmds, setJobTimeoutCmd(f.jobID, ms))
		}
	}
	if name != f.origName || group != f.origGroup {
		cmds = append(cmds, editSession(f.origName, name, f.origGroup, group))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func setJobTimeoutCmd(id int, ms int64) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetJobTimeout(id, ms); err != nil {
			return actionDoneMsg{err: err}
		}
		if ms <= 0 {
			return actionDoneMsg{status: fmt.Sprintf("job %d timeout cleared", id)}
		}
		return actionDoneMsg{status: fmt.Sprintf("job %d timeout set to %s", id, durationCompact(time.Duration(ms)*time.Millisecond))}
	}
}

func setJobLabelCmd(id int, label string) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetJobLabel(id, label); err != nil {
			return actionDoneMsg{err: err}
		}
		if label == "" {
			return actionDoneMsg{status: fmt.Sprintf("job %d name cleared", id)}
		}
		return actionDoneMsg{status: fmt.Sprintf("job %d named %q", id, label)}
	}
}

// --- settings form (`:config`) --------------------------------------------

// settingsMsg carries the fetched daemon settings that seed the `:config`
// modal (slots come from the model's own tree poll).
type settingsMsg struct {
	logdir string
}

// loadSettingsCmd fetches the daemon-side settings the modal edits.
func loadSettingsCmd() tea.Cmd {
	return func() tea.Msg {
		dir, err := client.GetLogdir()
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return settingsMsg{logdir: dir}
	}
}

// openSettingsForm shows the `:config` modal pre-filled with current values.
func (m model) openSettingsForm(logdir string) model {
	si := newTextInput()
	si.CharLimit = 4
	si.Prompt = ""
	si.SetValue(strconv.Itoa(m.maxSlots))
	si.CursorEnd()
	si.Focus()
	li := newTextInput()
	li.CharLimit = 256
	li.Prompt = ""
	li.SetValue(logdir)
	li.CursorEnd()
	m.jobs.settings = configForm{
		active:      true,
		slotsInput:  si,
		logdirInput: li,
		origSlots:   m.maxSlots,
		origLogdir:  logdir,
	}
	return m
}

func (f *configForm) toggleField() {
	f.focusField = 1 - f.focusField
	if f.focusField == 0 {
		f.slotsInput.Focus()
		f.logdirInput.Blur()
	} else {
		f.logdirInput.Focus()
		f.slotsInput.Blur()
	}
}

func (m model) handleSettingsFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.jobs.settings
	switch msg.String() {
	case "esc":
		m.jobs.settings = configForm{}
		return m, nil
	case "tab", "shift+tab", "up", "down":
		f.toggleField()
		return m, textinput.Blink
	case "enter":
		return m.submitSettingsForm()
	}
	var cmd tea.Cmd
	if f.focusField == 0 {
		f.slotsInput, cmd = f.slotsInput.Update(msg)
	} else {
		f.logdirInput, cmd = f.logdirInput.Update(msg)
	}
	return m, cmd
}

func (m model) submitSettingsForm() (tea.Model, tea.Cmd) {
	f := m.jobs.settings
	m.jobs.settings = configForm{}

	n, err := strconv.Atoi(strings.TrimSpace(f.slotsInput.Value()))
	if err != nil || n < 1 {
		m.status = fmt.Sprintf("invalid slot count: %q", f.slotsInput.Value())
		return m, nil
	}
	logdir := strings.TrimSpace(f.logdirInput.Value())

	var cmds []tea.Cmd
	if n != f.origSlots {
		cmds = append(cmds, setMaxSlotsCmd(n))
	}
	if logdir != "" && logdir != f.origLogdir {
		cmds = append(cmds, setLogdirCmd(logdir))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// --- command mode (`:`) ---------------------------------------------------

func (m model) handleJobsCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.jobs.commanding = false
		m.cmdInput.Blur()
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.cmdInput.Value())
		m.jobs.commanding = false
		m.cmdInput.Blur()
		if value == "" {
			return m, nil
		}
		return m.executeJobsCommand(value)
	}
	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	return m, cmd
}

// executeJobsCommand runs a `:` command typed on the management screen. It
// covers quick navigation plus a small config surface (parallel slots, log
// directory, sort order).
func (m model) executeJobsCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(strings.TrimPrefix(input, ":"))
	if len(fields) == 0 {
		return m, nil
	}
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	// `set`/`config` are optional prefixes: `:set slots 4` == `:slots 4`.
	if (cmd == "set" || cmd == "config") && len(args) > 0 {
		cmd = strings.ToLower(args[0])
		args = args[1:]
	} else if cmd == "config" {
		// Open the settings edit box; the logdir is fetched first to pre-fill it.
		return m, loadSettingsCmd()
	}

	switch cmd {
	case "help", "h", "?":
		m.jobs.helping = true
		return m, nil
	case "q", "quit":
		return m, tea.Quit
	case "slots", "parallel", "p":
		if len(args) == 0 {
			m.status = fmt.Sprintf("slots %d — usage: :set slots <n>", m.maxSlots)
			return m, nil
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			m.status = fmt.Sprintf("invalid slot count: %q", args[0])
			return m, nil
		}
		return m, setMaxSlotsCmd(n)
	case "logdir":
		if len(args) == 0 {
			return m, showLogdirCmd()
		}
		return m, setLogdirCmd(args[0])
	case "sort":
		if len(args) == 0 {
			m.status = "usage: :sort <group|id|state|time>"
			return m, nil
		}
		if sm, ok := parseSortMode(args[0]); ok {
			m.jobs.sortMode = sm
			m.refreshJobsRows()
		} else {
			m.status = fmt.Sprintf("unknown sort: %q", args[0])
		}
		return m, nil
	case "clear":
		return m, clearFinishedCmd()
	case "kill":
		// :kill <id...> | -a/--all | (no arg: current selection)
		if len(args) > 0 && (args[0] == "-a" || args[0] == "--all") {
			return m, killAllCmd()
		}
		ids, err := m.commandTargetIDs(args)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.jobs.visual = false
		m.jobs.tagged = nil
		return m, killJobs(ids)
	case "restart":
		// :restart <id...> | -a/--all | (no arg: current selection). Running
		// jobs are killed first; every non-queued target is re-enqueued.
		if len(args) > 0 && (args[0] == "-a" || args[0] == "--all") {
			return m, restartAllCmd()
		}
		ids, err := m.commandTargetIDs(args)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.jobs.visual = false
		m.jobs.tagged = nil
		return m, restartJobs(ids)
	case "reset":
		m.jobs.confirm = confirmState{kind: confirmReset}
		return m, nil
	default:
		m.status = fmt.Sprintf("unknown command: %s", input)
	}
	return m, nil
}

// commandTargetIDs resolves a `:` command's job targets: explicit ids from
// its arguments, else the current selection (tagged / visual / cursor row).
func (m model) commandTargetIDs(args []string) ([]int, error) {
	if len(args) == 0 {
		ids := m.jobsActionIDs()
		if len(ids) == 0 {
			return nil, fmt.Errorf("no job selected")
		}
		return ids, nil
	}
	ids := make([]int, 0, len(args))
	for _, a := range args {
		id, err := strconv.Atoi(a)
		if err != nil {
			return nil, fmt.Errorf("invalid job id %q", a)
		}
		ids = append(ids, id)
	}
	return ids, nil
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

func killAllCmd() tea.Cmd {
	return func() tea.Msg {
		if err := client.KillAllJobs(); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "killed all running jobs"}
	}
}

// restartJobsMsg kills the running targets and re-enqueues every non-queued
// one; queued targets are already pending and left alone.
func restartJobsMsg(jobs []protocol.JobInfo) tea.Msg {
	count := 0
	for _, j := range jobs {
		if j.State == protocol.StateQueued {
			continue
		}
		if j.State == protocol.StateRunning {
			_ = client.KillJob(j.ID)
		}
		if _, err := client.Rerun(j.ID); err != nil {
			return actionDoneMsg{err: err}
		}
		count++
	}
	return actionDoneMsg{status: fmt.Sprintf("restarted %d job(s)", count)}
}

func restartJobs(ids []int) tea.Cmd {
	return func() tea.Msg {
		jobs := make([]protocol.JobInfo, 0, len(ids))
		for _, id := range ids {
			info, err := client.GetInfo(id)
			if err != nil {
				return actionDoneMsg{err: err}
			}
			jobs = append(jobs, *info)
		}
		return restartJobsMsg(jobs)
	}
}

func restartAllCmd() tea.Cmd {
	return func() tea.Msg {
		res, err := client.ListJobs()
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return restartJobsMsg(res.Jobs)
	}
}

func renameGroupCmd(oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		if err := client.GroupRename(oldName, newName); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("renamed group %q -> %q", oldName, newName)}
	}
}

func resetServerCmd() tea.Cmd {
	return func() tea.Msg {
		if err := client.ResetServer(); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "daemon reset to defaults"}
	}
}

func setMaxSlotsCmd(n int) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetMaxSlots(n); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("parallel slots set to %d", n)}
	}
}

func setLogdirCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetLogdir(path); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "log directory set to " + path}
	}
}

func showLogdirCmd() tea.Cmd {
	return func() tea.Msg {
		dir, err := client.GetLogdir()
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "log directory: " + dir}
	}
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

	lines = append(lines, boxedTop(w, "", focusBorderStyle))
	lines = append(lines, m.jobsBordered(jobsHeaderStyle.Render(fitToWidth(m.jobsHeaderRow(cols), inner)), inner))

	// Overlays (help / edit form / session picker) take the whole box, so the
	// detail pane is replaced by extra room for a taller centered box.
	overlay := m.jobs.helping || m.jobs.form.active || m.jobs.settings.active || m.jobs.picker.active
	regionH := bodyH
	if overlay {
		regionH = bodyH + jobsDetailHeight
	}
	body := m.mgmtBodyLines(regionH, inner, cols)
	for _, bl := range body {
		lines = append(lines, m.jobsBordered(bl, inner))
	}

	if !overlay {
		for _, dl := range m.jobsDetailLines(jobsDetailHeight, inner) {
			lines = append(lines, m.jobsBordered(dl, inner))
		}
	}

	lines = append(lines, boxedBottom(w, focusBorderStyle))
	if m.jobs.filtering {
		lines = append(lines, fitToWidth(inputStyle.Render("/")+m.textInput.View(), w))
	}
	if m.jobs.commanding {
		lines = append(lines, fitToWidth(inputStyle.Render(m.cmdInput.View()), w))
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
	if m.jobs.settings.active {
		return m.settingsFormLines(bodyH, inner)
	}
	if m.jobs.picker.active {
		return m.sessionPickerLines(bodyH, inner)
	}
	if m.jobs.helping {
		return m.helpLines(bodyH, inner)
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
		inSel := r.kind == rowJob &&
			((m.jobs.visual && idx >= selLo && idx <= selHi) || m.jobs.tagged[r.job.ID])
		out = append(out, m.renderMgmtRow(r, cols, inner, isCursor, inSel))
	}
	return out
}

// renderMgmtRow renders one management-list row with the right highlight. Job
// rows keep their per-column semantic colors under the focus tint; group and
// session headers are free-form lines.
func (m model) renderMgmtRow(r mgmtRow, cols columnWidths, inner int, isCursor, inSel bool) string {
	if r.kind == rowSession {
		if isCursor {
			return padRowBg(renderSessionRow(r.group, r.session, cols, cRowFocusBg), inner, cRowFocusBg)
		}
		return renderSessionRow(r.group, r.session, cols, nil)
	}
	switch {
	case isCursor:
		return padRowBg(renderJobsRow(r.job, r.group, cols, cRowFocusBg), inner, cRowFocusBg)
	case inSel:
		return selectedStyle.Render(fitToWidth(stripAnsi(formatJobRow(r.job, r.group, cols)), inner))
	default:
		return formatJobRow(r.job, r.group, cols)
	}
}

// renderSessionRow renders a placeholder line for a session with no jobs, so it
// stays visible in the table. The columns align with job rows.
func renderSessionRow(group, session string, c columnWidths, bg lipgloss.TerminalColor) string {
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
	id := cell("·", c.id, treeEmptyStyle)
	grp := cell(group, c.group, groupStyle)
	sess := cell(session, c.session, sessionStyle)
	state := cell("no jobs", c.state, treeEmptyStyle)
	tm := cell("", c.tm, lipgloss.NewStyle())
	timeout := cell("", c.timeout, lipgloss.NewStyle())
	name := cell("", c.name, lipgloss.NewStyle())
	command := cell("— empty session · ⏎ to open", c.command, treeEmptyStyle)
	return id + sep + sep + sep + grp + sep + sess + sep + state + sep + tm + sep + timeout + sep + name + sep + command
}

// modalInnerWidth picks a modal's inner width: a comfortable fixed size that
// shrinks to fit narrow terminals.
func modalInnerWidth(bodyInner, prefer int) int {
	w := prefer
	if max := bodyInner - 4; w > max {
		w = max
	}
	if w < 24 {
		w = 24
	}
	return w
}

// modalBox wraps content lines in a rounded box with an accent title embedded in
// the top edge (╭─ Title ─╮), padding each line to innerW. Styled after bada's
// modalFrame/panelTop.
func modalBox(title string, content []string, innerW int) []string {
	titleR := modalTitleStyle.Render(title)
	dashes := innerW - 3 - lipgloss.Width(title)
	if dashes < 0 {
		dashes = 0
	}
	top := borderStyle.Render("╭─ ") + titleR + borderStyle.Render(" "+strings.Repeat("─", dashes)+"╮")
	side := borderStyle.Render("│")
	lines := []string{top}
	for _, c := range content {
		if w := lipgloss.Width(c); w < innerW {
			c += strings.Repeat(" ", innerW-w)
		} else if w > innerW {
			c = truncateToWidth(c, innerW)
		}
		lines = append(lines, side+c+side)
	}
	lines = append(lines, borderStyle.Render("╰"+strings.Repeat("─", innerW)+"╯"))
	return lines
}

// centerBox places a pre-rendered box centered within a bodyH×inner region.
func centerBox(box []string, bodyH, inner int) []string {
	boxW := 0
	for _, l := range box {
		if w := lipgloss.Width(l); w > boxW {
			boxW = w
		}
	}
	pad := strings.Repeat(" ", max(0, (inner-boxW)/2))
	top := max(0, (bodyH-len(box))/2)
	out := make([]string, 0, bodyH)
	for i := 0; i < bodyH; i++ {
		if ci := i - top; ci >= 0 && ci < len(box) {
			out = append(out, pad+box[ci])
		} else {
			out = append(out, "")
		}
	}
	return out
}

// sessionFormLines renders the edit modal centered in the body: session name +
// group, preceded by the job-name field when a job row is being edited.
func (m model) sessionFormLines(bodyH, inner int) []string {
	f := m.jobs.form
	title := "Edit session"
	switch {
	case f.creating:
		title = "New session"
	case f.groupRename:
		title = "Rename group"
	case f.jobID >= 0:
		title = fmt.Sprintf("Edit job %d", f.jobID)
	}
	boxInner := modalInnerWidth(inner, 46)
	const labelW = 7
	valueW := boxInner - 2 - labelW - 2 // marker(2) + label + gap(2)
	if valueW < 8 {
		valueW = 8
	}
	field := func(label string, ti textinput.Model, focused bool) string {
		ti.Width = valueW - 1 // textinput renders Width+1 cells (the cursor block)
		marker := "  "
		lbl := fmt.Sprintf("%-*s", labelW, label)
		if focused {
			marker = modalTitleStyle.Render("▌ ")
			lbl = modalActiveStyle.Render(lbl)
		} else {
			lbl = jobsDetailKeyStyle.Render(lbl)
		}
		return marker + lbl + "  " + ti.View()
	}

	labels := (&f).fields()
	inputs := (&f).inputs()
	content := []string{""}
	for i, lbl := range labels {
		content = append(content, field(lbl, *inputs[i], f.focusField == i))
	}
	// While the group field is focused, show the existing groups as a strip so
	// tab-cycling is a visible choice, not a blind rotation.
	if f.groupFocused() {
		choices := groupChoiceLines(m.groups, strings.TrimSpace(f.groupInput.Value()), boxInner-2)
		if len(choices) > 0 {
			content = append(content, "")
			content = append(content, choices...)
		}
	}
	content = append(content,
		"",
		helpStyle.Render("  tab: groups · ↑/↓: switch · ⏎: save · esc: cancel"),
	)
	return centerBox(modalBox(title, content, boxInner), bodyH, inner)
}

// groupChoiceLines renders the existing groups as wrapped strip lines; the
// entry matching the group field's current value is highlighted.
func groupChoiceLines(groups []string, current string, width int) []string {
	if len(groups) == 0 {
		return nil
	}
	var lines []string
	line, lineW := "  ", 2
	for _, g := range groups {
		tok, tokW := treeSummaryStyle.Render(g), lipgloss.Width(g)
		if g == current {
			tok, tokW = selectedStyle.Render(" "+g+" "), tokW+2
		}
		if lineW+tokW > width && lineW > 2 {
			lines = append(lines, line)
			line, lineW = "  ", 2
		}
		line += tok + "  "
		lineW += tokW + 2
	}
	return append(lines, line)
}

// settingsFormLines renders the `:config` settings modal centered in the body.
func (m model) settingsFormLines(bodyH, inner int) []string {
	f := m.jobs.settings
	boxInner := modalInnerWidth(inner, 46)
	const labelW = 6
	valueW := boxInner - 2 - labelW - 2
	if valueW < 8 {
		valueW = 8
	}
	field := func(label string, ti textinput.Model, focused bool) string {
		ti.Width = valueW - 1 // textinput renders Width+1 cells (the cursor block)
		marker := "  "
		lbl := fmt.Sprintf("%-*s", labelW, label)
		if focused {
			marker = modalTitleStyle.Render("▌ ")
			lbl = modalActiveStyle.Render(lbl)
		} else {
			lbl = jobsDetailKeyStyle.Render(lbl)
		}
		return marker + lbl + "  " + ti.View()
	}
	content := []string{
		"",
		field("slots", f.slotsInput, f.focusField == 0),
		field("logdir", f.logdirInput, f.focusField == 1),
		"",
		helpStyle.Render("  tab: switch · ⏎: save · esc: cancel"),
	}
	return centerBox(modalBox("Settings", content, boxInner), bodyH, inner)
}

// helpLines renders the key-binding / command help overlay.
func (m model) helpLines(bodyH, inner int) []string {
	boxInner := modalInnerWidth(inner, 52)
	row := func(keys, desc string) string {
		return "  " + modalActiveStyle.Render(fmt.Sprintf("%-15s", keys)) + treeSummaryStyle.Render(desc)
	}
	content := []string{
		"",
		jobsDetailKeyStyle.Render("  Navigation"),
		row("j / k", "move down / up"),
		row("gg / G", "jump to top / bottom"),
		row("^d / ^u", "half-page down / up"),
		"",
		jobsDetailKeyStyle.Render("  Sessions & jobs"),
		row("⏎", "job output (session if empty)"),
		row("o", "open the output pager"),
		row("S", "session picker (incl. empty)"),
		row("e", "edit row: name/session/group"),
		row("n / a", "new session"),
		row("x / u / r", "kill / urgent / rerun job"),
		row("d", "remove job(s)"),
		"",
		jobsDetailKeyStyle.Render("  View & config"),
		row("sg/si/ss/st", "sort by field · repeat reverses"),
		row("R", "reverse the current sort"),
		row("/", "filter"),
		row("space", "select job + move down"),
		row("V", "visual multi-select"),
		row(":", "command line"),
		row(":set slots N", "set parallel jobs"),
		row(":set logdir P", "set log directory"),
		row(":config", "settings box (slots, logdir)"),
		row(":kill id|-a", "kill job(s) / all running"),
		row(":restart id|-a", "kill + re-enqueue job(s)"),
		row(":reset", "factory-reset qrush (confirms)"),
		row("q", "quit"),
		row("esc", "back to the open session"),
		"",
		helpStyle.Render("  press any key to close"),
	}
	return centerBox(modalBox("Help", content, boxInner), bodyH, inner)
}

// sessionPickerLines renders the session picker overlay centered in the body.
func (m model) sessionPickerLines(bodyH, inner int) []string {
	p := m.jobs.picker
	boxInner := modalInnerWidth(inner, 42)
	content := []string{""}
	for i, r := range p.rows {
		if r.isGroup {
			content = append(content, "  "+folderStyle.Render("")+" "+groupStyle.Render(r.group))
			continue
		}
		marker := "    "
		name := sessionStyle.Render(fmt.Sprintf("%-16s", r.session))
		if i == p.cursor {
			marker = "  " + modalTitleStyle.Render("▌ ")
			name = modalActiveStyle.Render(fmt.Sprintf("%-16s", r.session))
		}
		badge := treeSummaryStyle.Render(fmt.Sprintf("  %d jobs", r.jobs))
		if r.open {
			badge += runningStyle.Render("  ● open")
		}
		content = append(content, marker+name+badge)
	}
	content = append(content, "")
	content = append(content, helpStyle.Render("  ⏎:open · e:edit · n:new · d:delete · esc:close"))
	return centerBox(modalBox("Sessions", content, boxInner), bodyH, inner)
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
		if s.MemUsed > 0 && s.MemTotal > 0 {
			// Same shape as the CPU segment: value + gauge, so the two chips
			// read as one system.
			pct := float64(s.MemUsed) / float64(s.MemTotal) * 100
			memText = fmt.Sprintf(" MEM %s/%s %s ", humanBytes(s.MemUsed), humanBytes(s.MemTotal), gauge(pct, 8))
		}
		left = append(left, statusSegment{text: memText, style: style})
	}

	// One quiet segment on the right instead of two competing chips.
	var parts []string
	if s.LoadOK {
		parts = append(parts, fmt.Sprintf("load %.2f %.2f %.2f", s.Load[0], s.Load[1], s.Load[2]))
	}
	if s.NumCPU > 0 {
		parts = append(parts, fmt.Sprintf("%d cpu", s.NumCPU))
	}
	var right []statusSegment
	if len(parts) > 0 {
		right = append(right, statusSegment{text: " " + strings.Join(parts, " · ") + " ", style: airlineMuted})
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
		out = append(out, styleJobDetailLine("name: "+r.session, inner))
		out = append(out, styleJobDetailLine("group: "+r.group, inner))
		out = append(out, styleJobDetailLine("jobs: 0 (empty)", inner))
		out = append(out, styleJobDetailLine("open: enter · edit: e · delete via s", inner))
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
		case confirmReset:
			q = " reset qrush? kills all jobs & panes, deletes sessions, restores default settings (y/n) "
		default:
			q = " clear all finished jobs? (y/n) "
		}
		return renderAirline(w, []statusSegment{
			{text: " CONFIRM ", style: airlineError},
			{text: q, style: airlineInfo},
		}, nil)
	}
	running, queued, finished, failed := jobStateCounts(m.visibleJobs())
	sessions := 0
	for _, n := range m.nodes {
		sessions += len(n.sessions)
	}
	modeText, modeStyle := " MANAGE ", modeCommandStyle
	if m.jobs.visual {
		lo, hi := m.visualRange()
		modeText, modeStyle = fmt.Sprintf(" VISUAL %d ", hi-lo+1), modeInsertStyle
	}
	left := []statusSegment{
		{text: modeText, style: modeStyle},
		{text: fmt.Sprintf(" sort:%s%s ", m.jobs.sortMode.label(), sortArrow(m.jobs.sortRev)), style: airlineFocus},
	}
	if n := len(m.jobs.tagged); n > 0 {
		left = append(left, statusSegment{text: fmt.Sprintf(" sel %d ", n), style: modeInsertStyle})
	}
	if m.jobs.filter != "" {
		left = append(left, statusSegment{text: fmt.Sprintf(" /%s ", m.jobs.filter), style: airlineInfo})
	}
	if m.status != "" {
		left = append(left, statusSegment{text: " " + shorten(m.status, 60) + " ", style: airlineInfo})
	}
	// Right side stays calm: one compact stats segment (fail only when it
	// exists, as its own red chip) and a single pointer to the help overlay
	// instead of a cheat-sheet crammed into the bar.
	var right []statusSegment
	if failed > 0 {
		right = append(right, statusSegment{text: fmt.Sprintf(" fail %d ", failed), style: airlineError})
	}
	right = append(right,
		statusSegment{text: fmt.Sprintf(" run %d · queue %d · done %d · sess %d ", running, queued, finished, sessions), style: airlineMuted},
		statusSegment{text: " ? help ", style: airlineFocus},
	)
	return renderAirline(w, left, right)
}

func sortArrow(rev bool) string {
	if rev {
		return "▼"
	}
	return "▲"
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
