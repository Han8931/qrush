package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/han/qrush/internal/protocol"
)

func keyRune(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
func keyNamed(s string) tea.KeyMsg {
	if s == "tab" {
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func treeModel() model {
	sessions := []protocol.SessionInfo{
		{Name: "default", Group: "default"},
		{Name: "build", Group: "default"},
	}
	jobs := []protocol.JobInfo{
		{ID: 1, Session: "build"},
		{ID: 2, Session: "build"},
	}
	m := model{viewMode: viewJobs}
	m.nodes = buildTree([]string{"default"}, sessions, jobs)
	m.jobs.tree = treePane{show: true, collapsed: map[string]bool{}}
	m.refreshTreeRows()
	return m
}

// The tree lists group → session → job, and folding a node hides its subtree.
func TestTreeRowsAndFold(t *testing.T) {
	m := treeModel()

	// Expanded: group + 2 sessions + 2 jobs under "build".
	kinds := map[treeRowKind]int{}
	for _, r := range m.jobs.tree.rows {
		kinds[r.kind]++
	}
	if kinds[treeGroup] != 1 || kinds[treeSession] != 2 || kinds[treeJob] != 2 {
		t.Fatalf("expanded tree: got %+v, want 1 group / 2 sessions / 2 jobs", kinds)
	}

	// Fold the "build" session → its two jobs disappear.
	m.jobs.tree.collapsed[sessionKey("default", "build")] = true
	m.refreshTreeRows()
	for _, r := range m.jobs.tree.rows {
		if r.kind == treeJob {
			t.Fatalf("folded session should hide its jobs, still saw job #%d", r.job.ID)
		}
	}

	// Fold the group → everything under it disappears (just the group row left).
	m.jobs.tree.collapsed[groupKey("default")] = true
	m.refreshTreeRows()
	if len(m.jobs.tree.rows) != 1 || m.jobs.tree.rows[0].kind != treeGroup {
		t.Fatalf("folded group should leave only its own row, got %d rows", len(m.jobs.tree.rows))
	}
}

// `,n` toggles the sidebar; tab moves focus only when the sidebar is shown.
func TestTreeToggleAndFocus(t *testing.T) {
	m := model{viewMode: viewJobs}
	m.nodes = buildTree([]string{"default"}, []protocol.SessionInfo{{Name: "default", Group: "default"}}, nil)

	// `,` then `n` toggles the tree via the leader chord.
	nm, _ := m.handleJobsKey(keyRune(','))
	m = nm.(model)
	nm, _ = m.handleJobsKey(keyRune('n'))
	m = nm.(model)
	if !m.jobs.tree.show {
		t.Fatalf(",n should show the tree")
	}
	nm, _, done := m.handleTreeKey(keyNamed("tab"))
	m = nm.(model)
	if !done || !m.jobs.tree.focus {
		t.Fatalf("tab should focus the shown tree")
	}
	// Hiding clears focus — the leader chord works from anywhere, incl. tree focus.
	nm, _ = m.handleJobsKey(keyRune(','))
	m = nm.(model)
	nm, _ = m.handleJobsKey(keyRune('n'))
	m = nm.(model)
	if m.jobs.tree.show || m.jobs.tree.focus {
		t.Fatalf("second ,n should hide the tree and drop focus")
	}
	// tab with the tree hidden is not consumed (list may use it).
	if _, _, done := m.handleTreeKey(keyNamed("tab")); done {
		t.Fatalf("tab should not be consumed while the tree is hidden")
	}
}

// In the MANAGE view, Ctrl+W then h/l moves focus between the tree and the list.
func TestTreeCtrlWFocus(t *testing.T) {
	m := treeModel() // tree shown, list focused (tree.focus == false)

	// Ctrl+W then l keeps focus on the list.
	nm, _ := m.handleJobsKey(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = nm.(model)
	if !m.ctrlWPressed {
		t.Fatal("Ctrl+W should arm the focus chord")
	}
	nm, _ = m.handleJobsKey(keyRune('l'))
	m = nm.(model)
	if m.jobs.tree.focus {
		t.Fatal("Ctrl+W l should focus the list (tree.focus == false)")
	}

	// Ctrl+W then h focuses the tree.
	nm, _ = m.handleJobsKey(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = nm.(model)
	nm, _ = m.handleJobsKey(keyRune('h'))
	m = nm.(model)
	if !m.jobs.tree.focus {
		t.Fatal("Ctrl+W h should focus the tree")
	}

	// Hold-Ctrl form works too: Ctrl+W then Ctrl+L back to the list.
	nm, _ = m.handleJobsKey(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = nm.(model)
	nm, _ = m.handleJobsKey(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = nm.(model)
	if m.jobs.tree.focus {
		t.Fatal("Ctrl+W Ctrl+L should focus the list")
	}
}

// `A` zooms the focused tree; esc restores it before dropping focus.
func TestTreeZoom(t *testing.T) {
	m := treeModel()
	m.jobs.tree.focus = true

	nm, _, done := m.handleTreeKey(keyRune('A'))
	m = nm.(model)
	if !done || !m.jobs.tree.zoom {
		t.Fatalf("A should zoom the tree")
	}
	// esc unzooms first (keeps focus), then a second esc drops focus.
	nm, _, _ = m.handleTreeKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(model)
	if m.jobs.tree.zoom || !m.jobs.tree.focus {
		t.Fatalf("esc should unzoom but keep focus, got zoom=%v focus=%v", m.jobs.tree.zoom, m.jobs.tree.focus)
	}
}

// `m` opens the action menu; a shortcut runs its action and closes the menu.
func TestTreeMenu(t *testing.T) {
	m := treeModel()
	m.jobs.tree.focus = true
	m.jobs.tree.cursor = 1 // the "default" session row

	nm, _, done := m.handleTreeKey(keyRune('m'))
	m = nm.(model)
	if !done || !m.jobs.tree.menu.active {
		t.Fatalf("m should open the action menu")
	}
	if len(m.jobs.tree.menu.items) == 0 || m.jobs.tree.menu.row.kind != treeSession {
		t.Fatalf("menu should target the session under the cursor, got %+v", m.jobs.tree.menu.row)
	}

	// `d` is the delete shortcut: it runs and closes the menu, arming the confirm.
	nm, _, _ = m.handleTreeKey(keyRune('d'))
	m = nm.(model)
	if m.jobs.tree.menu.active {
		t.Fatalf("running an action should close the menu")
	}
	if m.jobs.confirm.kind != confirmDeleteSession || m.jobs.confirm.session != "default" {
		t.Fatalf("delete action should arm confirmDeleteSession for the session, got %+v", m.jobs.confirm)
	}

	// esc closes the menu without acting.
	m2 := treeModel()
	m2.jobs.tree.focus = true
	nm, _, _ = m2.handleTreeKey(keyRune('m'))
	m2 = nm.(model)
	nm, _, _ = m2.handleTreeKey(tea.KeyMsg{Type: tea.KeyEsc})
	m2 = nm.(model)
	if m2.jobs.tree.menu.active {
		t.Fatalf("esc should close the menu")
	}
}
