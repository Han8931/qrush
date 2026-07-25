package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/han/qrush/internal/protocol"
)

// treePane is the NerdTree-style sidebar in the MANAGE view: a foldable
// group → session → job tree for managing sessions and groups. The flat job
// list to its right is unchanged; this pane is purely for browsing/managing the
// hierarchy.
type treePane struct {
	show      bool
	focus     bool // keys drive the tree instead of the list
	cursor    int
	rows      []treeRow       // derived visible rows (honors fold state)
	collapsed map[string]bool // node key → folded; survives the live refresh
}

type treeRowKind int

const (
	treeGroup treeRowKind = iota
	treeSession
	treeJob
)

type treeRow struct {
	kind    treeRowKind
	group   string
	session string
	job     protocol.JobInfo
	key     string // fold key (group/session rows); "" for jobs
	hasKids bool
}

// treePaneWidth is the sidebar's fixed inner width (columns), plus a divider.
const treePaneWidth = 26

func groupKey(g string) string      { return "g\x00" + g }
func sessionKey(g, s string) string { return "s\x00" + g + "\x00" + s }

// buildTreeRows flattens the group→session→job hierarchy into visible rows,
// skipping the children of any collapsed node.
func (m model) buildTreeRows() []treeRow {
	col := m.jobs.tree.collapsed
	var rows []treeRow
	for _, node := range m.nodes {
		gk := groupKey(node.group)
		rows = append(rows, treeRow{kind: treeGroup, group: node.group, key: gk, hasKids: len(node.sessions) > 0})
		if col[gk] {
			continue
		}
		for _, s := range node.sessions {
			sk := sessionKey(node.group, s.Name)
			js := node.jobs[s.Name]
			rows = append(rows, treeRow{kind: treeSession, group: node.group, session: s.Name, key: sk, hasKids: len(js) > 0})
			if col[sk] {
				continue
			}
			for _, j := range js {
				rows = append(rows, treeRow{kind: treeJob, group: node.group, session: s.Name, job: j})
			}
		}
	}
	return rows
}

// refreshTreeRows rebuilds the visible rows and keeps the cursor in range. Safe
// to call every tick; fold state lives in collapsed and is preserved.
func (m *model) refreshTreeRows() {
	if m.jobs.tree.collapsed == nil {
		m.jobs.tree.collapsed = map[string]bool{}
	}
	m.jobs.tree.rows = m.buildTreeRows()
	if m.jobs.tree.cursor >= len(m.jobs.tree.rows) {
		m.jobs.tree.cursor = len(m.jobs.tree.rows) - 1
	}
	if m.jobs.tree.cursor < 0 {
		m.jobs.tree.cursor = 0
	}
}

func (t *treePane) current() (treeRow, bool) {
	if t.cursor >= 0 && t.cursor < len(t.rows) {
		return t.rows[t.cursor], true
	}
	return treeRow{}, false
}

// --- key handling ---------------------------------------------------------

// handleTreeKey processes sidebar keys. `T` toggles the pane from anywhere and
// `tab` moves focus between the tree and the list; while the tree has focus it
// owns navigation and fold keys. The bool reports whether the key was consumed
// (false lets the list handler run).
func (m model) handleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "T":
		m.jobs.tree.show = !m.jobs.tree.show
		if m.jobs.tree.show {
			(&m).refreshTreeRows()
		} else {
			m.jobs.tree.focus = false
		}
		return m, nil, true
	case "tab":
		if m.jobs.tree.show {
			m.jobs.tree.focus = !m.jobs.tree.focus
			return m, nil, true
		}
		return m, nil, false
	}

	if !m.jobs.tree.show || !m.jobs.tree.focus {
		return m, nil, false
	}

	t := &m.jobs.tree
	switch msg.String() {
	case "esc":
		t.focus = false
	case "j", "down":
		if t.cursor < len(t.rows)-1 {
			t.cursor++
		}
	case "k", "up":
		if t.cursor > 0 {
			t.cursor--
		}
	case "g":
		t.cursor = 0
	case "G":
		if len(t.rows) > 0 {
			t.cursor = len(t.rows) - 1
		}
	case "l", "o", " ":
		(&m).treeToggleFold()
	case "h":
		(&m).treeCollapseOrParent()
	case "enter":
		return m.treeActivate()
	case "e":
		return m.treeEdit()
	case "n", "a":
		grp := ""
		if r, ok := t.current(); ok {
			grp = r.group
		}
		return m.openSessionForm(true, "", grp), textinput.Blink, true
	case "d":
		(&m).treeDelete()
	}
	return m, nil, true
}

// treeToggleFold folds/unfolds the group or session under the cursor.
func (m *model) treeToggleFold() {
	r, ok := m.jobs.tree.current()
	if !ok || r.key == "" || !r.hasKids {
		return
	}
	if m.jobs.tree.collapsed == nil {
		m.jobs.tree.collapsed = map[string]bool{}
	}
	m.jobs.tree.collapsed[r.key] = !m.jobs.tree.collapsed[r.key]
	m.refreshTreeRows()
}

// treeCollapseOrParent folds an expanded node, else jumps to its parent — the
// familiar `h` behavior in a tree.
func (m *model) treeCollapseOrParent() {
	r, ok := m.jobs.tree.current()
	if !ok {
		return
	}
	if r.key != "" && r.hasKids && !m.jobs.tree.collapsed[r.key] {
		m.jobs.tree.collapsed[r.key] = true
		m.refreshTreeRows()
		return
	}
	// Move up to the nearest shallower row (session→group, job→session).
	want := treeGroup
	if r.kind == treeJob {
		want = treeSession
	} else if r.kind == treeSession {
		want = treeGroup
	} else {
		return
	}
	for i := m.jobs.tree.cursor - 1; i >= 0; i-- {
		if m.jobs.tree.rows[i].kind == want {
			m.jobs.tree.cursor = i
			return
		}
	}
}

// treeActivate handles Enter: fold a group, open a session, page a job's output.
func (m model) treeActivate() (tea.Model, tea.Cmd, bool) {
	r, ok := m.jobs.tree.current()
	if !ok {
		return m, nil, true
	}
	switch r.kind {
	case treeGroup:
		(&m).treeToggleFold()
		return m, nil, true
	case treeSession:
		nm, cmd := m.openSession(r.session)
		return nm, cmd, true
	case treeJob:
		return m, openPagerCmd(r.job), true
	}
	return m, nil, true
}

// treeEdit opens the rename/edit form for the group or session under the cursor.
func (m model) treeEdit() (tea.Model, tea.Cmd, bool) {
	r, ok := m.jobs.tree.current()
	if !ok {
		return m, nil, true
	}
	switch r.kind {
	case treeGroup:
		return m.openGroupRenameForm(r.group), textinput.Blink, true
	case treeSession, treeJob:
		return m.openSessionForm(false, r.session, r.group), textinput.Blink, true
	}
	return m, nil, true
}

// treeDelete asks to delete the group or session under the cursor (the default
// session can't be deleted; the daemon rejects it).
func (m *model) treeDelete() {
	r, ok := m.jobs.tree.current()
	if !ok {
		return
	}
	switch r.kind {
	case treeGroup:
		m.jobs.confirm = confirmState{kind: confirmDeleteGroup, group: r.group}
	case treeSession:
		m.jobs.confirm = confirmState{kind: confirmDeleteSession, session: r.session}
	}
}

// --- rendering ------------------------------------------------------------

// treePaneLines renders the sidebar column as exactly h lines, treePaneWidth
// wide, scrolled to keep the cursor visible.
func (m model) treePaneLines(h int) []string {
	t := m.jobs.tree
	off := 0
	if t.cursor >= h {
		off = t.cursor - h + 1
	}
	out := make([]string, 0, h)
	for i := 0; i < h; i++ {
		idx := off + i
		if idx >= len(t.rows) {
			out = append(out, strings.Repeat(" ", treePaneWidth))
			continue
		}
		out = append(out, m.renderTreeRow(t.rows[idx], idx == t.cursor))
	}
	return out
}

func (m model) renderTreeRow(r treeRow, selected bool) string {
	var text string
	style := treeSummaryStyle
	switch r.kind {
	case treeGroup:
		text = foldMark(m.jobs.tree.collapsed[r.key], r.hasKids) + " " + r.group
		style = groupStyle
	case treeSession:
		text = "  " + foldMark(m.jobs.tree.collapsed[r.key], r.hasKids) + " " + r.session
		style = sessionStyle
	case treeJob:
		label := r.job.Label
		if label == "" {
			label = r.job.Command
		}
		text = fmt.Sprintf("    #%d %s", r.job.ID, label)
	}
	txt := fitToWidth(stripAnsi(" "+text), treePaneWidth)
	if selected && m.jobs.tree.focus {
		return lipgloss.NewStyle().Background(cRowFocusBg).Render(txt)
	}
	if selected {
		// Unfocused cursor: a subtle marker so position is still visible.
		return treeCursorDimStyle.Render(txt)
	}
	return style.Render(txt)
}

// foldMark returns the disclosure triangle for a node (blank when it has no
// children, so leaves don't pretend to be foldable).
func foldMark(collapsed, hasKids bool) string {
	if !hasKids {
		return " "
	}
	if collapsed {
		return "▸"
	}
	return "▾"
}

// treeHeaderCell renders the sidebar's column header, brightened when focused.
func (m model) treeHeaderCell() string {
	s := treeSummaryStyle
	if m.jobs.tree.focus {
		s = modalActiveStyle
	}
	return s.Render(fitToWidth(" SESSIONS", treePaneWidth))
}

func treeDividerCell() string { return borderStyle.Render("│") }
