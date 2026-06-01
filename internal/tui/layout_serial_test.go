package tui

import "testing"

func leafNamed(name string) *paneNode {
	return newLeaf(&shellState{pane: name})
}

func TestLayoutMarshalRoundTrip(t *testing.T) {
	// vsplit( p1 , hsplit( p2 , p3 ) )
	root := &paneNode{
		dir: splitVert,
		children: []*paneNode{
			leafNamed("p1"),
			{dir: splitHoriz, children: []*paneNode{leafNamed("p2"), leafNamed("p3")}},
		},
	}

	got := unmarshalLayout(marshalLayout(root))
	if got == nil {
		t.Fatal("unmarshal returned nil")
	}
	if got.dir != splitVert || len(got.children) != 2 {
		t.Fatalf("root shape wrong: %+v", got)
	}
	if got.children[0].dir != splitLeaf || got.children[0].shell.pane != "p1" {
		t.Fatalf("left leaf wrong: %+v", got.children[0])
	}
	right := got.children[1]
	if right.dir != splitHoriz || right.children[0].shell.pane != "p2" || right.children[1].shell.pane != "p3" {
		t.Fatalf("right subtree wrong: %+v", right)
	}

	names := paneNames(got)
	if len(names) != 3 {
		t.Fatalf("paneNames = %v, want 3 entries", names)
	}
}

func TestPruneDeadLeaves(t *testing.T) {
	root := &paneNode{
		dir:      splitVert,
		children: []*paneNode{leafNamed("p1"), leafNamed("p2")},
	}
	// p2 is dead: tree collapses to the p1 leaf.
	got := pruneDeadLeaves(root, map[string]bool{"p1": true})
	if got == nil || got.dir != splitLeaf || got.shell.pane != "p1" {
		t.Fatalf("expected collapse to p1 leaf, got %+v", got)
	}

	// All dead: nil.
	if pruneDeadLeaves(leafNamed("x"), map[string]bool{}) != nil {
		t.Fatal("expected nil when no leaves survive")
	}

	// Both alive: shape preserved.
	root2 := &paneNode{dir: splitHoriz, children: []*paneNode{leafNamed("a"), leafNamed("b")}}
	got2 := pruneDeadLeaves(root2, map[string]bool{"a": true, "b": true})
	if got2 == nil || got2.dir != splitHoriz || len(got2.leaves()) != 2 {
		t.Fatalf("expected both panes kept, got %+v", got2)
	}
}
