package tui

// paneNode is a node in a session's split layout. A leaf holds one shell; an
// internal node splits its area between exactly two children. Splits are
// binary and recursive, giving tmux-style tiling.
type splitDir int

const (
	splitLeaf  splitDir = iota
	splitVert           // children side by side (left | right) — "vs"
	splitHoriz          // children stacked (top / bottom)      — "hs"
)

type paneNode struct {
	dir      splitDir
	shell    *shellState // leaf only
	children []*paneNode // internal only, len == 2
}

func newLeaf(sh *shellState) *paneNode {
	return &paneNode{dir: splitLeaf, shell: sh}
}

func (n *paneNode) isLeaf() bool { return n == nil || n.dir == splitLeaf }

// rect is a region in the tiling area, in cells (origin top-left).
type rect struct{ x, y, w, h int }

// leafRect pairs a leaf with the rectangle it occupies.
type leafRect struct {
	node *paneNode
	rect rect
}

// separator is a 1-cell border line drawn between two sibling panes.
type separator struct {
	x, y     int
	length   int
	vertical bool // true: vertical line (between left|right children)
}

// layout walks the tree and produces the rectangle for every leaf plus the
// separators between siblings. A split reserves one cell for its separator.
func (n *paneNode) layout(r rect) ([]leafRect, []separator) {
	var leaves []leafRect
	var seps []separator
	n.layoutInto(r, &leaves, &seps)
	return leaves, seps
}

func (n *paneNode) layoutInto(r rect, leaves *[]leafRect, seps *[]separator) {
	if n == nil {
		return
	}
	if n.dir == splitLeaf {
		*leaves = append(*leaves, leafRect{node: n, rect: r})
		return
	}

	a, b := n.children[0], n.children[1]
	if n.dir == splitVert {
		// Not enough width for two panes plus a separator: collapse to first.
		if r.w < 3 {
			a.layoutInto(r, leaves, seps)
			return
		}
		leftW := (r.w - 1) / 2
		rightW := r.w - 1 - leftW
		a.layoutInto(rect{r.x, r.y, leftW, r.h}, leaves, seps)
		*seps = append(*seps, separator{x: r.x + leftW, y: r.y, length: r.h, vertical: true})
		b.layoutInto(rect{r.x + leftW + 1, r.y, rightW, r.h}, leaves, seps)
		return
	}

	// splitHoriz
	if r.h < 3 {
		a.layoutInto(r, leaves, seps)
		return
	}
	topH := (r.h - 1) / 2
	botH := r.h - 1 - topH
	a.layoutInto(rect{r.x, r.y, r.w, topH}, leaves, seps)
	*seps = append(*seps, separator{x: r.x, y: r.y + topH, length: r.w, vertical: false})
	b.layoutInto(rect{r.x, r.y + topH + 1, r.w, botH}, leaves, seps)
}

// leaves returns every leaf in left-to-right, top-to-bottom traversal order.
func (n *paneNode) leaves() []*paneNode {
	var out []*paneNode
	var walk func(*paneNode)
	walk = func(p *paneNode) {
		if p == nil {
			return
		}
		if p.dir == splitLeaf {
			out = append(out, p)
			return
		}
		walk(p.children[0])
		walk(p.children[1])
	}
	walk(n)
	return out
}

// split turns the target leaf into an internal node with the original shell in
// one child and newShell in the other, returning the new leaf for newShell.
// dir is splitVert or splitHoriz. Returns nil if target is not a leaf.
func (n *paneNode) split(target *paneNode, newShell *shellState, dir splitDir) *paneNode {
	if target == nil || target.dir != splitLeaf {
		return nil
	}
	orig := newLeaf(target.shell)
	created := newLeaf(newShell)
	target.dir = dir
	target.shell = nil
	target.children = []*paneNode{orig, created}
	return created
}

// removeLeaf removes the leaf holding the given shell id, collapsing its parent
// onto the surviving sibling. It returns the new root (which may differ when the
// removed leaf was the root) and whether anything was removed.
func (n *paneNode) removeLeaf(id int) (*paneNode, bool) {
	if n == nil {
		return nil, false
	}
	if n.dir == splitLeaf {
		if n.shell != nil && n.shell.id == id {
			return nil, true
		}
		return n, false
	}
	for i, child := range n.children {
		if child.dir == splitLeaf {
			if child.shell != nil && child.shell.id == id {
				sibling := n.children[1-i]
				return sibling, true
			}
			continue
		}
		if newChild, removed := child.removeLeaf(id); removed {
			if newChild == nil {
				return n.children[1-i], true
			}
			n.children[i] = newChild
			return n, true
		}
	}
	return n, false
}

// findLeaf returns the leaf holding the given shell id, or nil.
func (n *paneNode) findLeaf(id int) *paneNode {
	for _, leaf := range n.leaves() {
		if leaf.shell != nil && leaf.shell.id == id {
			return leaf
		}
	}
	return nil
}

// nextLeaf returns the leaf after focus in traversal order, wrapping around.
func (n *paneNode) nextLeaf(focus *paneNode) *paneNode {
	leaves := n.leaves()
	if len(leaves) == 0 {
		return nil
	}
	for i, leaf := range leaves {
		if leaf == focus {
			return leaves[(i+1)%len(leaves)]
		}
	}
	return leaves[0]
}

// neighbor returns the leaf adjacent to focus in the given direction within the
// supplied area, or nil if there is none. Adjacency is geometric: the closest
// pane whose center lies on the requested side.
func (n *paneNode) neighbor(focus *paneNode, area rect, dir splitDir, forward bool) *paneNode {
	leaves, _ := n.layout(area)
	var cur rect
	found := false
	for _, lr := range leaves {
		if lr.node == focus {
			cur = lr.rect
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	curCX := cur.x + cur.w/2
	curCY := cur.y + cur.h/2

	var best *paneNode
	bestDist := 1 << 30
	for _, lr := range leaves {
		if lr.node == focus {
			continue
		}
		r := lr.rect
		cx := r.x + r.w/2
		cy := r.y + r.h/2
		if dir == splitVert { // horizontal movement (left/right)
			if forward && cx <= curCX {
				continue
			}
			if !forward && cx >= curCX {
				continue
			}
			dist := abs(cx-curCX) + abs(cy-curCY)
			if dist < bestDist {
				bestDist = dist
				best = lr.node
			}
		} else { // vertical movement (up/down)
			if forward && cy <= curCY {
				continue
			}
			if !forward && cy >= curCY {
				continue
			}
			dist := abs(cy-curCY) + abs(cx-curCX)
			if dist < bestDist {
				bestDist = dist
				best = lr.node
			}
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
