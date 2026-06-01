package tui

import (
	"bytes"
	"encoding/gob"
)

// serialNode is the on-the-wire form of a paneNode tree. Only the split shape
// and each leaf's (server-assigned) pane name are persisted; the live shells
// are reattached by name on restore.
type serialNode struct {
	Dir      int
	Pane     string
	Children []serialNode
}

func toSerial(n *paneNode) serialNode {
	if n == nil || n.dir == splitLeaf {
		name := ""
		if n != nil && n.shell != nil {
			name = n.shell.pane
		}
		return serialNode{Dir: int(splitLeaf), Pane: name}
	}
	return serialNode{
		Dir:      int(n.dir),
		Children: []serialNode{toSerial(n.children[0]), toSerial(n.children[1])},
	}
}

func fromSerial(s serialNode) *paneNode {
	if len(s.Children) != 2 || splitDir(s.Dir) == splitLeaf {
		// Placeholder leaf carrying the pane name; the real shell is attached
		// by the caller and swapped into shell before use.
		return newLeaf(&shellState{pane: s.Pane})
	}
	return &paneNode{
		dir:      splitDir(s.Dir),
		children: []*paneNode{fromSerial(s.Children[0]), fromSerial(s.Children[1])},
	}
}

func marshalLayout(root *paneNode) []byte {
	if root == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(toSerial(root)); err != nil {
		return nil
	}
	return buf.Bytes()
}

func unmarshalLayout(blob []byte) *paneNode {
	if len(blob) == 0 {
		return nil
	}
	var s serialNode
	if err := gob.NewDecoder(bytes.NewReader(blob)).Decode(&s); err != nil {
		return nil
	}
	return fromSerial(s)
}

// paneNames returns the pane names of every leaf in the tree (the "keep" set
// used when persisting so the daemon reaps panes no longer in the layout).
func paneNames(root *paneNode) []string {
	if root == nil {
		return nil
	}
	var names []string
	for _, leaf := range root.leaves() {
		if leaf.shell != nil && leaf.shell.pane != "" {
			names = append(names, leaf.shell.pane)
		}
	}
	return names
}

// pruneDeadLeaves drops leaves whose pane is not in alive, collapsing parents
// onto the surviving sibling. Returns nil if nothing survives.
func pruneDeadLeaves(n *paneNode, alive map[string]bool) *paneNode {
	if n == nil {
		return nil
	}
	if n.dir == splitLeaf {
		if n.shell != nil && alive[n.shell.pane] {
			return n
		}
		return nil
	}
	if len(n.children) != 2 {
		return nil
	}
	a := pruneDeadLeaves(n.children[0], alive)
	b := pruneDeadLeaves(n.children[1], alive)
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		n.children[0], n.children[1] = a, b
		return n
	}
}
