package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/han/qrush/internal/client"
)

// Pane lifecycle for the split view: building a session's tiled layout from
// the daemon, splitting/closing panes, moving focus between them, and keeping
// every PTY sized to its rectangle.

func firstLeaf(root *paneNode) *paneNode {
	leaves := root.leaves()
	if len(leaves) == 0 {
		return nil
	}
	return leaves[0]
}

func (m model) activeRoot() *paneNode {
	return m.layouts[m.activeSession]
}

func (m model) paneCount() int {
	root := m.activeRoot()
	if root == nil {
		return 0
	}
	return len(root.leaves())
}

// movePaneFocus moves focus to the geometrically adjacent pane, returning
// whether a neighbor existed.
func (m *model) movePaneFocus(dir splitDir, forward bool) bool {
	root := m.activeRoot()
	if root == nil || m.focusPane == nil {
		return false
	}
	rw, rh := m.termRegionSize()
	area := rect{x: 0, y: 0, w: rw - 2, h: rh - 2}
	next := root.neighbor(m.focusPane, area, dir, forward)
	if next == nil {
		return false
	}
	m.focusPane = next
	m.syncFocusShell()
	m.focus = paneTerm
	return true
}

// buildSession restores a session's panes from the daemon — reattaching every
// still-alive pane named in the persisted layout, pruning dead ones — or opens
// a single fresh pane when there is nothing to restore. It registers the new
// shells in m.panes and sets m.layouts[session], returning the freshly created
// shells so the caller can start their read loops.
func (m *model) buildSession(session string, cols, rows int) []*shellState {
	blob, alive, _ := client.GetTerminalLayout(session)
	aliveSet := make(map[string]bool, len(alive))
	for _, p := range alive {
		aliveSet[p] = true
	}

	var root *paneNode
	if r := unmarshalLayout(blob); r != nil {
		root = pruneDeadLeaves(r, aliveSet)
	}

	var created []*shellState
	if root == nil {
		name, err := client.OpenTerminal(session, cols, rows)
		if err != nil {
			return nil
		}
		sh, err := spawnShellPane(cols, rows, session, name)
		if err != nil {
			return nil
		}
		sh.id = m.nextPaneID
		m.nextPaneID++
		m.panes[sh.id] = sh
		root = newLeaf(sh)
		created = append(created, sh)
	} else {
		for _, leaf := range root.leaves() {
			name := ""
			if leaf.shell != nil {
				name = leaf.shell.pane
			}
			sh, err := spawnShellPane(cols, rows, session, name)
			if err != nil {
				continue
			}
			sh.id = m.nextPaneID
			m.nextPaneID++
			m.panes[sh.id] = sh
			leaf.shell = sh
			created = append(created, sh)
		}
	}
	m.layouts[session] = root
	return created
}

// persistLayoutCmd saves a session's layout to the daemon off the Update
// goroutine. The keep set lets the daemon reap panes no longer in the layout.
func persistLayoutCmd(session string, root *paneNode) tea.Cmd {
	blob := marshalLayout(root)
	keep := paneNames(root)
	return func() tea.Msg {
		_ = client.SetTerminalLayout(session, blob, keep)
		return nil
	}
}

func (m model) activateSession(session string) (model, tea.Cmd) {
	if session == "" {
		session = "default"
	}
	if root := m.layouts[session]; root != nil {
		m.activeSession = session
		m.focusPane = firstLeaf(root)
		m.syncFocusShell()
		m.focus = paneTerm
		m.status = fmt.Sprintf("active session: %s", session)
		m.resizeShell()
		return m, clearScreenCmd()
	}
	cols, rows := m.paneSpawnSize()
	created := (&m).buildSession(session, cols, rows)
	if len(created) == 0 {
		m.status = "error: could not open session terminal"
		return m, nil
	}
	m.activeSession = session
	m.focusPane = firstLeaf(m.layouts[session])
	m.syncFocusShell()
	m.focus = paneTerm
	m.status = fmt.Sprintf("active session: %s", session)
	cmds := make([]tea.Cmd, 0, len(created)+2)
	for _, sh := range created {
		go sh.readLoop()
		cmds = append(cmds, waitForPTYOutput(sh.id, sh.ptyCh))
	}
	cmds = append(cmds, clearScreenCmd(), persistLayoutCmd(session, m.layouts[session]))
	m.resizeShell()
	return m, tea.Batch(cmds...)
}

// syncFocusShell keeps m.shell pointing at the focused leaf's shell.
func (m *model) syncFocusShell() {
	if m.focusPane != nil && m.focusPane.dir == splitLeaf {
		m.shell = m.focusPane.shell
	}
}

// splitFocused splits the focused pane in half, spawning a fresh shell for the
// active session in the new pane and moving focus to it (tmux behavior).
func (m model) splitFocused(dir splitDir) (model, tea.Cmd) {
	root := m.activeRoot()
	if root == nil || m.focusPane == nil {
		return m, nil
	}
	if m.shellDone() {
		m.status = "cannot split: shell exited"
		return m, nil
	}
	cols, rows := m.paneSpawnSize()
	paneName, err := client.OpenTerminal(m.activeSession, cols, rows)
	if err != nil {
		m.status = fmt.Sprintf("error: %v", err)
		return m, nil
	}
	sh, err := spawnShellPane(cols, rows, m.activeSession, paneName)
	if err != nil {
		m.status = fmt.Sprintf("error: %v", err)
		return m, nil
	}
	sh.id = m.nextPaneID
	m.nextPaneID++
	created := root.split(m.focusPane, sh, dir)
	if created == nil {
		sh.destroy()
		return m, nil
	}
	m.panes[sh.id] = sh
	m.focusPane = created
	m.shell = sh
	m.focus = paneTerm
	go sh.readLoop()
	m.resizeShell()
	return m, tea.Batch(waitForPTYOutput(sh.id, sh.ptyCh), clearScreenCmd(), persistLayoutCmd(m.activeSession, root))
}

// closeFocused kills the focused pane's shell and collapses the layout. The
// last remaining pane is never removed (use q to quit instead).
func (m model) closeFocused() (model, tea.Cmd) {
	root := m.activeRoot()
	if root == nil || m.focusPane == nil {
		return m, nil
	}
	if len(root.leaves()) <= 1 {
		m.status = "cannot close last pane (press q to quit)"
		return m, nil
	}
	sh := m.focusPane.shell
	newRoot, removed := root.removeLeaf(sh.id)
	if !removed {
		return m, nil
	}
	m.layouts[m.activeSession] = newRoot
	delete(m.panes, sh.id)
	sh.destroy()
	m.focusPane = firstLeaf(newRoot)
	m.syncFocusShell()
	m.resizeShell()
	return m, tea.Batch(clearScreenCmd(), persistLayoutCmd(m.activeSession, newRoot))
}

// handleShellExit removes a pane whose shell exited on its own. If it was the
// session's only pane the leaf is kept so the "[shell exited]" notice shows.
func (m model) handleShellExit(id int) (tea.Model, tea.Cmd) {
	sh := m.panes[id]
	if sh == nil {
		return m, nil
	}
	root := m.layouts[sh.session]
	if root == nil {
		return m, nil
	}
	if len(root.leaves()) <= 1 {
		if sh.session == m.activeSession {
			m.status = "[shell exited - press q to quit]"
		}
		return m, nil
	}
	newRoot, removed := root.removeLeaf(id)
	if !removed {
		return m, nil
	}
	m.layouts[sh.session] = newRoot
	delete(m.panes, id)
	persist := persistLayoutCmd(sh.session, newRoot)
	if sh.session != m.activeSession {
		return m, persist
	}
	if m.focusPane == nil || m.focusPane.shell == nil || m.focusPane.shell.id == id {
		m.focusPane = firstLeaf(newRoot)
		m.syncFocusShell()
	}
	m.resizeShell()
	return m, tea.Batch(clearScreenCmd(), persist)
}

// focusNextPane cycles focus to the next pane in traversal order.
func (m model) focusNextPane() (model, tea.Cmd) {
	root := m.activeRoot()
	if root == nil || m.focusPane == nil {
		return m, nil
	}
	m.focusPane = root.nextLeaf(m.focusPane)
	m.syncFocusShell()
	m.focus = paneTerm
	return m, nil
}

// focusDirPane moves focus to the geometrically adjacent pane.
func (m model) focusDirPane(dir splitDir, forward bool) (model, tea.Cmd) {
	root := m.activeRoot()
	if root == nil || m.focusPane == nil {
		return m, nil
	}
	rw, rh := m.termRegionSize()
	area := rect{x: 0, y: 0, w: rw - 2, h: rh - 2}
	if next := root.neighbor(m.focusPane, area, dir, forward); next != nil {
		m.focusPane = next
		m.syncFocusShell()
		m.focus = paneTerm
	}
	return m, nil
}

// termRegionSize returns the size of the whole terminal region on the right,
// including its outer border. The per-pane tiling area is this minus the
// 1-cell border on each side.
func (m model) termRegionSize() (w, h int) {
	w = m.width
	h = m.height - 1 // footer line
	if m.inputMode != inputNone {
		h--
	}
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	return
}

// paneSpawnSize is a reasonable initial PTY size for a freshly spawned pane;
// resizeShell corrects it to the pane's real rectangle immediately after.
func (m model) paneSpawnSize() (cols, rows int) {
	w, h := m.termRegionSize()
	cols, rows = w-2, h-2
	if cols < 10 {
		cols = 10
	}
	if rows < 2 {
		rows = 2
	}
	return
}

// resizeShell resizes every pane in the active session's layout to match its
// current tiled rectangle.
func (m model) resizeShell() {
	root := m.activeRoot()
	if root == nil {
		return
	}
	rw, rh := m.termRegionSize()
	iw, ih := rw-2, rh-2
	if iw < 1 {
		iw = 1
	}
	if ih < 1 {
		ih = 1
	}
	leaves, _ := root.layout(rect{x: 0, y: 0, w: iw, h: ih})
	for _, lr := range leaves {
		sh := lr.node.shell
		if sh == nil {
			continue
		}
		sh.mu.Lock()
		done := sh.done
		sh.mu.Unlock()
		if done {
			continue
		}
		w, h := lr.rect.w, lr.rect.h
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		sh.resize(w, h)
	}
}

func (m model) shellDone() bool {
	if m.shell == nil {
		return true
	}
	m.shell.mu.Lock()
	defer m.shell.mu.Unlock()
	return m.shell.done
}
