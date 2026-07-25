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

// T toggles the sidebar; tab moves focus only when the sidebar is shown.
func TestTreeToggleAndFocus(t *testing.T) {
	m := model{viewMode: viewJobs}
	m.nodes = buildTree([]string{"default"}, []protocol.SessionInfo{{Name: "default", Group: "default"}}, nil)

	nm, _, done := m.handleTreeKey(keyRune('T'))
	m = nm.(model)
	if !done || !m.jobs.tree.show {
		t.Fatalf("T should show the tree")
	}
	nm, _, done = m.handleTreeKey(keyNamed("tab"))
	m = nm.(model)
	if !done || !m.jobs.tree.focus {
		t.Fatalf("tab should focus the shown tree")
	}
	// Hiding clears focus.
	nm, _, _ = m.handleTreeKey(keyRune('T'))
	m = nm.(model)
	if m.jobs.tree.show || m.jobs.tree.focus {
		t.Fatalf("second T should hide the tree and drop focus")
	}
	// tab with the tree hidden is not consumed (list may use it).
	if _, _, done := m.handleTreeKey(keyNamed("tab")); done {
		t.Fatalf("tab should not be consumed while the tree is hidden")
	}
}
