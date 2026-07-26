package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func leafWithID(id int) *paneNode {
	return newLeaf(&shellState{id: id})
}

// Drive the real key dispatch (Update): Ctrl+W then l/h must move pane focus.
func TestCtrlWChordThroughUpdate(t *testing.T) {
	root := leafWithID(1)
	right := root.split(root, &shellState{id: 2}, splitVert)
	leftLeaf := root.leaves()[0]

	newModel := func() model {
		return model{
			width:         80,
			height:        24,
			focus:         paneTerm,
			activeSession: "default",
			layouts:       map[string]*paneNode{"default": root},
			panes:         map[int]*shellState{1: leftLeaf.shell, 2: right.shell},
			focusPane:     leftLeaf,
			shell:         leftLeaf.shell,
		}
	}

	// Release-Ctrl form: Ctrl+W, then plain "l".
	m := newModel()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = nm.(model)
	if !m.ctrlWPressed {
		t.Fatal("Ctrl+W should arm the pane-nav prefix")
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if nm.(model).focusPane != right {
		t.Fatal("Ctrl+W then l should focus the right pane")
	}

	// Hold-Ctrl form: Ctrl+W, then Ctrl+L.
	m = newModel()
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = nm.(model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if nm.(model).focusPane != right {
		t.Fatal("Ctrl+W then Ctrl+L should focus the right pane")
	}
}

func TestSingleLeafLayout(t *testing.T) {
	root := leafWithID(1)
	leaves, seps := root.layout(rect{0, 0, 80, 24})
	if len(leaves) != 1 {
		t.Fatalf("expected 1 leaf, got %d", len(leaves))
	}
	if len(seps) != 0 {
		t.Fatalf("expected no separators, got %d", len(seps))
	}
	if leaves[0].rect != (rect{0, 0, 80, 24}) {
		t.Fatalf("leaf rect = %+v", leaves[0].rect)
	}
}

func TestVerticalSplitTiling(t *testing.T) {
	root := leafWithID(1)
	created := root.split(root, &shellState{id: 2}, splitVert)
	if created == nil {
		t.Fatal("split returned nil")
	}
	leaves, seps := root.layout(rect{0, 0, 81, 24})
	if len(leaves) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(leaves))
	}
	if len(seps) != 1 || !seps[0].vertical {
		t.Fatalf("expected 1 vertical separator, got %+v", seps)
	}
	// Widths plus the 1-cell separator must fill the area exactly.
	if w := leaves[0].rect.w + leaves[1].rect.w + 1; w != 81 {
		t.Fatalf("widths + separator = %d, want 81", w)
	}
	// Both panes span the full height.
	for _, lr := range leaves {
		if lr.rect.h != 24 {
			t.Fatalf("pane height = %d, want 24", lr.rect.h)
		}
	}
	// Separator sits between the two panes.
	if seps[0].x != leaves[0].rect.w {
		t.Fatalf("separator x = %d, want %d", seps[0].x, leaves[0].rect.w)
	}
}

func TestHorizontalSplitTiling(t *testing.T) {
	root := leafWithID(1)
	root.split(root, &shellState{id: 2}, splitHoriz)
	leaves, seps := root.layout(rect{0, 0, 80, 25})
	if len(leaves) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(leaves))
	}
	if len(seps) != 1 || seps[0].vertical {
		t.Fatalf("expected 1 horizontal separator, got %+v", seps)
	}
	if h := leaves[0].rect.h + leaves[1].rect.h + 1; h != 25 {
		t.Fatalf("heights + separator = %d, want 25", h)
	}
}

func TestRecursiveSplitAndCount(t *testing.T) {
	root := leafWithID(1)
	right := root.split(root, &shellState{id: 2}, splitVert)
	// Split the right pane horizontally.
	root.split(right, &shellState{id: 3}, splitHoriz)
	leaves := root.leaves()
	if len(leaves) != 3 {
		t.Fatalf("expected 3 leaves, got %d", len(leaves))
	}
	rects, _ := root.layout(rect{0, 0, 81, 25})
	if len(rects) != 3 {
		t.Fatalf("expected 3 rects, got %d", len(rects))
	}
}

func TestRemoveLeafCollapses(t *testing.T) {
	root := leafWithID(1)
	created := root.split(root, &shellState{id: 2}, splitVert)
	_ = created

	newRoot, removed := root.removeLeaf(2)
	if !removed {
		t.Fatal("expected leaf 2 to be removed")
	}
	if !newRoot.isLeaf() || newRoot.shell.id != 1 {
		t.Fatalf("expected collapse to leaf id 1, got %+v", newRoot)
	}
}

func TestRemoveNestedLeaf(t *testing.T) {
	root := leafWithID(1)
	right := root.split(root, &shellState{id: 2}, splitVert)
	root.split(right, &shellState{id: 3}, splitHoriz)

	newRoot, removed := root.removeLeaf(3)
	if !removed {
		t.Fatal("expected leaf 3 removed")
	}
	leaves := newRoot.leaves()
	if len(leaves) != 2 {
		t.Fatalf("expected 2 leaves after removal, got %d", len(leaves))
	}
	if newRoot.findLeaf(3) != nil {
		t.Fatal("leaf 3 should be gone")
	}
}

func TestNextLeafCycles(t *testing.T) {
	root := leafWithID(1)
	b := root.split(root, &shellState{id: 2}, splitVert)
	first := root.leaves()[0]
	if got := root.nextLeaf(first); got != b {
		t.Fatalf("nextLeaf should advance to second pane")
	}
	if got := root.nextLeaf(b); got != first {
		t.Fatalf("nextLeaf should wrap around to first pane")
	}
}

func TestCtrlWPaneNavigation(t *testing.T) {
	root := leafWithID(1)
	right := root.split(root, &shellState{id: 2}, splitVert)
	leftLeaf := root.leaves()[0]

	m := model{
		width:         80,
		height:        24,
		focus:         paneTerm,
		activeSession: "default",
		layouts:       map[string]*paneNode{"default": root},
		panes:         map[int]*shellState{1: leftLeaf.shell, 2: right.shell},
		focusPane:     leftLeaf,
		shell:         leftLeaf.shell,
	}

	if m2 := m.ctrlWRight(); m2.focusPane != right || m2.focus != paneTerm {
		t.Fatal("ctrl+w l should move to the right pane")
	}

	m.focusPane = right
	m.shell = right.shell
	if m3 := m.ctrlWLeft(); m3.focusPane != leftLeaf {
		t.Fatal("ctrl+w h should move back to the left pane")
	}

	// The tree is gone, so ctrl+w h from the leftmost pane is a no-op: focus
	// stays on the leftmost pane and the terminal keeps focus.
	m.focusPane = leftLeaf
	m.shell = leftLeaf.shell
	m4 := m.ctrlWLeft()
	if m4.focusPane != leftLeaf || m4.focus != paneTerm {
		t.Fatal("ctrl+w h from the leftmost pane should stay on the leftmost pane")
	}
}

func TestCtrlWCycle(t *testing.T) {
	root := leafWithID(1)
	right := root.split(root, &shellState{id: 2}, splitVert)
	leftLeaf := root.leaves()[0]

	m := model{
		width:         80,
		height:        24,
		focus:         paneTerm,
		activeSession: "default",
		layouts:       map[string]*paneNode{"default": root},
		panes:         map[int]*shellState{1: leftLeaf.shell, 2: right.shell},
		focusPane:     leftLeaf,
		shell:         leftLeaf.shell,
	}

	// first pane -> second pane -> wrap back to first pane
	m = m.ctrlWCycle()
	if m.focusPane != right {
		t.Fatal("cycle should advance to the second pane")
	}
	m = m.ctrlWCycle()
	if m.focusPane != leftLeaf {
		t.Fatal("cycle past the last pane should wrap to the first pane")
	}
}

func TestNeighborDirectional(t *testing.T) {
	root := leafWithID(1)
	right := root.split(root, &shellState{id: 2}, splitVert)
	left := root.leaves()[0]
	area := rect{0, 0, 81, 24}

	if got := root.neighbor(left, area, splitVert, true); got != right {
		t.Fatal("right neighbor of left pane should be the right pane")
	}
	if got := root.neighbor(right, area, splitVert, false); got != left {
		t.Fatal("left neighbor of right pane should be the left pane")
	}
	if got := root.neighbor(left, area, splitVert, false); got != nil {
		t.Fatal("left pane has no left neighbor")
	}
}
