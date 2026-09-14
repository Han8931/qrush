package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/han/qrush/internal/format"
	"github.com/han/qrush/internal/protocol"
)

// Core state for the job-management screen: view modes and the flat row model
// (build/sort/filter/anchor). Key and mouse dispatch lives in jobs_keys.go.

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

// mgmtRow is one line in the flat management table. The list is jobs-only;
// sessions/groups are browsed and managed from the tree sidebar. session and
// group are carried alongside the job so the columns and sort can use them.
type mgmtRow struct {
	session string           // session name
	group   string           // group name
	job     protocol.JobInfo // the job payload
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

	pending      byte // first key of a two-key motion: 'g' (gg), 's' (sort) or ',' (leader)
	pendingTimer int

	confirm  confirmState
	form     sessionForm
	settings configForm
	picker   sessionPicker
	pager    pagerState
	tree     treePane // NerdTree-style foldable group/session sidebar
}

type jobsGTimeoutMsg struct{ id int }

// --- open / refresh -------------------------------------------------------

// openJobsView switches to the full-screen management view and enables mouse
// capture (so sessions/jobs can be clicked). The returned command turns mouse
// reporting on; callers batch it with their own commands.
func (m model) openJobsView() (model, tea.Cmd) {
	m.viewMode = viewJobs
	// Preserve the collapse state across re-entry, but reset transient sub-state.
	// The tree sidebar's visibility and fold state survive too.
	m.jobs = jobsView{
		scopeAll: m.jobs.scopeAll,
		filter:   m.jobs.filter,
		tree:     treePane{show: m.jobs.tree.show, collapsed: m.jobs.tree.collapsed},
	}
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
// active filter and the current sort. The list is jobs-only: empty sessions are
// browsed and opened from the tree sidebar, not shown here as placeholder rows.
func (m model) buildMgmtRows() []mgmtRow {
	var out []mgmtRow
	for _, node := range m.nodes {
		for _, session := range node.sessions {
			jobs := filterJobs(node.jobs[session.Name], m.jobs.filter, "", true)
			for _, j := range jobs {
				out = append(out, mgmtRow{
					session: session.Name, group: node.group, job: j,
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
	m.refreshTreeRows()
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
		out = append(out, r.job)
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

// jobsSelected returns the job under the cursor, if any.
func (m model) jobsSelected() (protocol.JobInfo, bool) {
	if r, ok := m.jobsCursorRow(); ok {
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
