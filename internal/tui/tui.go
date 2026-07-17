package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/protocol"
	"github.com/han/qrush/internal/sysmon"
)

type pane int

const (
	paneTree pane = iota
	paneTerm
)

type model struct {
	width  int
	height int
	focus  pane

	ctrlWPressed bool
	ctrlWTimer   int
	ctrlBPressed bool
	ctrlBTimer   int
	nextPaneID   int

	nodes []treeNode
	// sessionExpanded tracks, by session name, whether the management view
	// shows that session's jobs nested beneath it. Group-level expansion lives
	// on treeNode.expanded; sessions need their own flag because the protocol
	// SessionInfo type can't carry UI state.
	sessionExpanded map[string]bool
	maxSlots        int
	err             error
	status          string
	inputMode       inputKind
	textInput       textinput.Model
	cmdInput        textinput.Model

	// layouts holds each session's split-layout tree (tmux-style tiling).
	// panes is a flat id→shell registry used for PTY event routing. shell and
	// focusPane track the currently focused pane within the active session.
	layouts       map[string]*paneNode
	panes         map[int]*shellState
	focusPane     *paneNode
	shell         *shellState
	activeSession string
	gitBranch     string

	// viewMode selects the top-level screen: the normal split view or the
	// full-screen job-management modal. jobs holds that modal's state.
	viewMode viewMode
	jobs     jobsView

	// jobsOnly is set when the TUI was launched directly into the
	// job-management view (`ru --jobs`). In that mode, quitting the jobs
	// view exits the program instead of dropping into the split view.
	jobsOnly bool

	// hwMon samples system-wide hardware metrics shown in the job view's
	// status bar; hwStats holds the latest snapshot.
	hwMon   *sysmon.Monitor
	hwStats sysmon.Stats

	// mouseOn tracks whether terminal mouse reporting is currently enabled. It
	// is turned on only while the management view is showing so terminal panes
	// keep their native text selection.
	mouseOn bool
}

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

type inputKind int

const (
	inputNone inputKind = iota
	inputCreate
	inputRename
	inputMove
	inputCommand
)

type treeDataMsg struct {
	groups       []string
	sessions     []protocol.SessionInfo
	jobs         []protocol.JobInfo
	maxSlots     int
	openJobsView bool
	err          error
}

type tickMsg time.Time
type commaTimeoutMsg struct{ id int }
type ctrlWTimeoutMsg struct{ id int }
type ctrlBTimeoutMsg struct{ id int }
type gTimeoutMsg struct{ id int }

type actionDoneMsg struct {
	status string
	err    error
}

func newModel(sh *shellState) model {
	ti := textinput.New()
	ti.CharLimit = 64

	ci := textinput.New()
	ci.CharLimit = 128
	ci.Prompt = ":"

	layouts := make(map[string]*paneNode)
	panes := make(map[int]*shellState)
	activeSession := "default"
	var focusPane *paneNode
	nextPaneID := 0
	if sh != nil {
		sh.id = nextPaneID
		nextPaneID++
		panes[sh.id] = sh
		activeSession = sh.session
		focusPane = newLeaf(sh)
		layouts[activeSession] = focusPane
	}
	return model{
		textInput:       ti,
		cmdInput:        ci,
		focus:           paneTerm,
		sessionExpanded: make(map[string]bool),
		layouts:         layouts,
		panes:           panes,
		focusPane:       focusPane,
		nextPaneID:      nextPaneID,
		shell:           sh,
		activeSession:   activeSession,
		gitBranch:       currentGitBranch(),
		hwMon:           sysmon.New(),
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{fetchTreeData, tickCmd()}
	for _, sh := range m.panes {
		go sh.readLoop()
		cmds = append(cmds, waitForPTYOutput(sh.id, sh.ptyCh))
	}
	// `ru --jobs` starts directly in the job view; seed its status bar at once.
	if m.viewMode == viewJobs {
		cmds = append(cmds, sampleHWCmd(m.hwMon))
	}
	return tea.Batch(cmds...)
}

func fetchTreeData() tea.Msg {
	data, err := client.TreeData()
	if err != nil {
		return treeDataMsg{err: err}
	}
	return treeDataMsg{
		groups:       data.Groups,
		sessions:     data.Sessions,
		jobs:         data.Jobs,
		maxSlots:     data.MaxSlots,
		openJobsView: data.OpenJobsView,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type hwStatsMsg sysmon.Stats

// sampleHWCmd reads hardware metrics off the Update goroutine (the macOS path
// shells out to vm_stat) and delivers them as a message.
func sampleHWCmd(mon *sysmon.Monitor) tea.Cmd {
	return func() tea.Msg {
		return hwStatsMsg(mon.Sample())
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeShell()
		cmds := []tea.Cmd{clearScreenCmd()}
		// Enable mouse capture the first time we know the size while on the
		// management screen (enabling from Init is unreliable).
		if m.viewMode == viewJobs && !m.mouseOn {
			m.mouseOn = true
			cmds = append(cmds, tea.EnableMouseCellMotion)
		}
		return m, tea.Batch(cmds...)

	case treeDataMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		expanded := make(map[string]bool)
		for _, n := range m.nodes {
			expanded[n.group] = n.expanded
		}
		m.nodes = buildTree(msg.groups, msg.sessions, msg.jobs)
		for i, n := range m.nodes {
			if exp, ok := expanded[n.group]; ok {
				m.nodes[i].expanded = exp
			}
		}
		m.maxSlots = msg.maxSlots
		m.pruneSessionExpanded()
		// Another client (`ru --jobs` from inside a pane) asked us to open the
		// jobs view. Honour it only from the split view so we don't disturb a
		// pager or an already-open table.
		if msg.openJobsView && m.viewMode == viewSplit {
			m, mouseCmd := m.openJobsView()
			return m, tea.Batch(clearScreenCmd(), sampleHWCmd(m.hwMon), mouseCmd)
		}
		if m.viewMode == viewJobs {
			m.jobs.allJobs = msg.jobs
			m.refreshJobsRows()
			if m.jobs.mode == jobsPager && m.jobs.pager.follow && m.jobs.pager.running {
				return m, m.reloadPagerCmd()
			}
		}
		return m, nil

	case pagerLoadedMsg:
		return m.applyPagerLoaded(msg), nil

	case hwStatsMsg:
		m.hwStats = sysmon.Stats(msg)
		return m, nil

	case tickMsg:
		m.gitBranch = m.focusedBranch()
		cmds := []tea.Cmd{fetchTreeData, tickCmd()}
		// The hardware status bar only appears in the job view, so only sample
		// while it is open.
		if m.viewMode == viewJobs {
			cmds = append(cmds, sampleHWCmd(m.hwMon))
		}
		return m, tea.Batch(cmds...)

	case ptyOutputMsg:
		if sh := m.panes[msg.id]; sh != nil {
			cmd := waitForPTYOutput(msg.id, sh.ptyCh)
			if msg.clear {
				return m, tea.Batch(clearScreenCmd(), cmd)
			}
			return m, cmd
		}
		return m, nil

	case shellExitMsg:
		return m.handleShellExit(msg.id)

	case ctrlWTimeoutMsg:
		if msg.id == m.ctrlWTimer && m.ctrlWPressed {
			m.ctrlWPressed = false
			if m.focus == paneTerm && m.shell != nil {
				m.shell.write([]byte{0x17})
			}
		}
		return m, nil

	case ctrlBTimeoutMsg:
		return m, nil

	case jobsGTimeoutMsg:
		if msg.id == m.jobs.pendingTimer {
			m.jobs.pending = 0
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("error: %v", msg.err)
		} else {
			m.status = msg.status
		}
		return m, fetchTreeData

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		if m.viewMode == viewJobs {
			return m.handleJobsKey(msg)
		}
		if m.inputMode == inputCommand {
			return m.handleCommandKey(msg)
		}
		var handled bool
		var cmd tea.Cmd
		m, handled, cmd = m.handleCtrlB(msg)
		if handled {
			return m, cmd
		}
		m, handled, cmd = m.handleCtrlW(msg)
		if handled {
			return m, cmd
		}
		return m.handleTermKey(msg)
	}
	return m, nil
}

func (m model) handleCtrlW(msg tea.KeyMsg) (model, bool, tea.Cmd) {
	// When focus is on the terminal and a full-screen TUI program is active
	// (vim, htop, less, etc.), forward Ctrl+W to the program so its own
	// window-management commands work (e.g. vim's <C-w>h to switch windows).
	if m.focus == paneTerm && m.shell != nil && m.shell.inAltScreen() {
		return m, false, nil
	}

	key := msg.String()

	if m.ctrlWPressed {
		m.ctrlWPressed = false
		switch key {
		case "h", "left":
			return m.ctrlWLeft(), true, nil
		case "l", "right":
			return m.ctrlWRight(), true, nil
		case "k", "up":
			return m.ctrlWVert(false), true, nil
		case "j", "down":
			return m.ctrlWVert(true), true, nil
		case "w":
			return m.ctrlWCycle(), true, nil
		case "q":
			nm, cmd := m.closeFocused()
			return nm, true, cmd
		}
		if m.focus == paneTerm && m.shell != nil {
			m.shell.write([]byte{0x17})
		}
		return m, false, nil
	}

	if msg.Type == tea.KeyCtrlW {
		m.ctrlWPressed = true
		m.ctrlWTimer++
		timerID := m.ctrlWTimer
		return m, true, tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
			return ctrlWTimeoutMsg{id: timerID}
		})
	}

	return m, false, nil
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

// ctrlWLeft moves to the pane on the left.
func (m model) ctrlWLeft() model {
	m.movePaneFocus(splitVert, false)
	return m
}

// ctrlWRight moves to the pane on the right.
func (m model) ctrlWRight() model {
	m.movePaneFocus(splitVert, true)
	return m
}

// ctrlWVert moves between vertically stacked panes.
func (m model) ctrlWVert(down bool) model {
	m.movePaneFocus(splitHoriz, down)
	return m
}

// ctrlWCycle cycles focus through every pane in turn, wrapping at the end.
func (m model) ctrlWCycle() model {
	var leaves []*paneNode
	if root := m.activeRoot(); root != nil {
		leaves = root.leaves()
	}
	idx := -1
	for i, lf := range leaves {
		if lf == m.focusPane {
			idx = i
			break
		}
	}
	if idx >= 0 && idx < len(leaves)-1 {
		m.focusPane = leaves[idx+1]
		m.syncFocusShell()
		return m
	}
	if len(leaves) > 0 {
		m.focusPane = leaves[0]
		m.syncFocusShell()
	}
	return m
}

// handleCtrlB implements a tmux-style prefix: Ctrl+B then <key>.
// Always active so users can reach TUI commands from any context (shell,
// vim, etc.). The prefix stays active until the next key, like tmux.
func (m model) handleCtrlB(msg tea.KeyMsg) (model, bool, tea.Cmd) {
	key := msg.String()

	if m.ctrlBPressed {
		m.ctrlBPressed = false
		switch key {
		case "esc":
			return m, true, nil
		case "c":
			m.inputMode = inputCommand
			m.cmdInput.SetValue("")
			m.cmdInput.Focus()
			m.resizeShell()
			return m, true, textinput.Blink
		case "q":
			return m, true, tea.Quit
		case "|", "%":
			nm, cmd := m.splitFocused(splitVert)
			return nm, true, cmd
		case "-", "\"":
			nm, cmd := m.splitFocused(splitHoriz)
			return nm, true, cmd
		case "o":
			nm, cmd := m.focusNextPane()
			return nm, true, cmd
		case "x":
			nm, cmd := m.closeFocused()
			return nm, true, cmd
		case "left", "h":
			nm, cmd := m.focusDirPane(splitVert, false)
			return nm, true, cmd
		case "right", "l":
			nm, cmd := m.focusDirPane(splitVert, true)
			return nm, true, cmd
		case "up", "k":
			nm, cmd := m.focusDirPane(splitHoriz, false)
			return nm, true, cmd
		case "down":
			nm, cmd := m.focusDirPane(splitHoriz, true)
			return nm, true, cmd
		case "j":
			// Ctrl+B j opens the job-management view. Pane-down is on Ctrl+B ↓.
			nm, mouseCmd := m.openJobsView()
			return nm, true, tea.Batch(sampleHWCmd(nm.hwMon), mouseCmd)
		case "d":
			// Ctrl+B d detaches (tmux-style) back to the management view. The
			// session's panes stay alive on the daemon.
			nm, mouseCmd := m.openJobsView()
			return nm, true, tea.Batch(clearScreenCmd(), sampleHWCmd(nm.hwMon), mouseCmd)
		}
		// Not a recognized chord — forward the buffered Ctrl+B to the focused
		// pane and let the current key flow through normal processing.
		if m.focus == paneTerm && m.shell != nil {
			m.shell.write([]byte{0x02})
		}
		return m, false, nil
	}

	if msg.Type == tea.KeyCtrlB {
		m.ctrlBPressed = true
		m.ctrlBTimer++
		return m, true, nil
	}

	return m, false, nil
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

// pruneSessionExpanded drops expansion state for sessions that no longer exist.
func (m *model) pruneSessionExpanded() {
	if len(m.sessionExpanded) == 0 {
		return
	}
	live := make(map[string]bool)
	for _, n := range m.nodes {
		for _, s := range n.sessions {
			live[s.Name] = true
		}
	}
	for name := range m.sessionExpanded {
		if !live[name] {
			delete(m.sessionExpanded, name)
		}
	}
}

func (m model) resetSessionShell(session string) (model, tea.Cmd) {
	if session == "" {
		session = m.activeSession
	}
	if root := m.layouts[session]; root != nil {
		for _, leaf := range root.leaves() {
			if leaf.shell != nil {
				delete(m.panes, leaf.shell.id)
				leaf.shell.destroy()
			}
		}
		delete(m.layouts, session)
	}
	if session != m.activeSession {
		m.status = fmt.Sprintf("reset session %s", session)
		// Clear the stale daemon layout and reap any leftover panes.
		return m, tea.Batch(clearScreenCmd(), persistLayoutCmd(session, nil))
	}
	cols, rows := m.paneSpawnSize()
	name, err := client.OpenTerminal(session, cols, rows)
	if err != nil {
		m.status = fmt.Sprintf("error: %v", err)
		return m, nil
	}
	sh, err := spawnShellPane(cols, rows, session, name)
	if err != nil {
		m.status = fmt.Sprintf("error: %v", err)
		return m, nil
	}
	sh.id = m.nextPaneID
	m.nextPaneID++
	m.panes[sh.id] = sh
	leaf := newLeaf(sh)
	m.layouts[session] = leaf
	m.focusPane = leaf
	m.shell = sh
	go sh.readLoop()
	m.status = fmt.Sprintf("reset session %s", session)
	return m, tea.Batch(waitForPTYOutput(sh.id, sh.ptyCh), clearScreenCmd(), persistLayoutCmd(session, leaf))
}

func (m model) handleTermKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" && m.shellDone() {
		return m, tea.Quit
	}

	if m.shell != nil && !m.shellDone() {
		b := keyToBytes(msg)
		if b != nil {
			m.shell.write(b)
		}
	}
	return m, nil
}

func (m model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputMode = inputNone
		m.cmdInput.Blur()
		m.resizeShell()
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.cmdInput.Value())
		m.cmdInput.Blur()
		m.inputMode = inputNone
		m.resizeShell()
		if value != "" {
			return m.executeCommand(value)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	return m, cmd
}

func (m model) executeCommand(input string) (tea.Model, tea.Cmd) {
	switch input {
	case "q", "quit", ":q", ":quit":
		return m, tea.Quit
	case "jobs", "j", ":jobs", ":j":
		nm, mouseCmd := m.openJobsView()
		return nm, tea.Batch(sampleHWCmd(nm.hwMon), mouseCmd)
	case "vs", "vsplit", "vsp":
		nm, cmd := m.splitFocused(splitVert)
		return nm, cmd
	case "hs", "hsplit", "sp", "split":
		nm, cmd := m.splitFocused(splitHoriz)
		return nm, cmd
	case "o", "next", "next-pane":
		nm, cmd := m.focusNextPane()
		return nm, cmd
	case "x", "close", "kill-pane":
		nm, cmd := m.closeFocused()
		return nm, cmd
	case "detach", "d", ":detach":
		nm, mouseCmd := m.openJobsView()
		return nm, tea.Batch(clearScreenCmd(), sampleHWCmd(nm.hwMon), mouseCmd)
	default:
		m.status = fmt.Sprintf("unknown command: %s", input)
	}
	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		return "loading..."
	}
	if m.viewMode == viewJobs {
		return m.renderJobsView(m.width, m.height)
	}

	var b strings.Builder
	headerHeight := 1
	contentHeight := m.height - 1 - headerHeight
	if m.inputMode != inputNone {
		contentHeight--
	}
	if contentHeight < 3 {
		contentHeight = 3
	}

	regionW, _ := m.termRegionSize()
	// The region spans the header line plus the content rows; line 0 is the
	// box top border, lines 1..contentHeight are the content/bottom rows.
	regionLines := m.renderTermRegion(regionW, contentHeight+1)

	for row := 0; row < contentHeight+1; row++ {
		if row < len(regionLines) {
			b.WriteString(regionLines[row])
		} else {
			b.WriteString(fitToWidth("", regionW))
		}
		b.WriteByte('\n')
	}

	if m.inputMode == inputCommand {
		b.WriteString(m.modeStyle().Render(" COMMAND "))
		b.WriteString(" ")
		b.WriteString(m.cmdInput.View())
		b.WriteByte('\n')
	}

	if m.inputMode == inputNone {
		b.WriteString(m.renderFooter())
	}

	return b.String()
}

// renderTermRegion renders the whole right-hand terminal region — the rounded
// outer box (with the active session as a title), every tiled pane's contents,
// and the separators between panes — into exactly `height` styled lines, each
// `width` display columns wide.
func (m model) renderTermRegion(width, height int) []string {
	focused := m.focus == paneTerm

	grid := make([][]string, height)
	for y := range grid {
		grid[y] = make([]string, width)
		for x := range grid[y] {
			grid[y][x] = " "
		}
	}

	if width >= 4 && height >= 3 {
		iw, ih := width-2, height-2
		root := m.activeRoot()
		var leaves []leafRect
		var seps []separator
		focusRect := rect{}
		haveFocus := false
		if root != nil {
			leaves, seps = root.layout(rect{x: 0, y: 0, w: iw, h: ih})
			for _, lr := range leaves {
				if lr.node == m.focusPane {
					focusRect = lr.rect
					haveFocus = true
				}
			}
		}

		for _, lr := range leaves {
			m.placePane(grid, lr, focused && lr.node == m.focusPane)
		}

		for _, sp := range seps {
			st := termBorderStyle(focused && haveFocus && sepTouchesRect(sp, focusRect))
			if sp.vertical {
				cx := 1 + sp.x
				for i := 0; i < sp.length; i++ {
					cy := 1 + sp.y + i
					if cy > 0 && cy < height-1 && cx > 0 && cx < width-1 {
						grid[cy][cx] = st.Render("│")
					}
				}
			} else {
				cy := 1 + sp.y
				for i := 0; i < sp.length; i++ {
					cx := 1 + sp.x + i
					if cy > 0 && cy < height-1 && cx > 0 && cx < width-1 {
						grid[cy][cx] = st.Render("─")
					}
				}
			}
		}
	}

	m.drawOuterBorder(grid, width, height, termBorderStyle(focused))

	lines := make([]string, height)
	for y := 0; y < height; y++ {
		var b strings.Builder
		for x := 0; x < width; x++ {
			b.WriteString(grid[y][x])
		}
		lines[y] = fitToWidth(b.String(), width)
	}
	return lines
}

// placePane draws one pane's terminal contents into the grid at its rectangle
// (offset by 1,1 to clear the outer border).
func (m model) placePane(grid [][]string, lr leafRect, showCursor bool) {
	r := lr.rect
	if r.w < 1 || r.h < 1 {
		return
	}
	sh := lr.node.shell
	done := sh == nil
	if sh != nil {
		sh.mu.Lock()
		done = sh.done
		sh.mu.Unlock()
	}

	var cells [][]string
	if done {
		cells = make([][]string, r.h)
		for y := range cells {
			cells[y] = make([]string, r.w)
			for x := range cells[y] {
				cells[y][x] = " "
			}
		}
		for i, ch := range "[shell exited]" {
			if i >= r.w {
				break
			}
			cells[0][i] = helpStyle.Render(string(ch))
		}
	} else {
		cells = renderTermCells(sh.vt, r.w, r.h, showCursor)
	}

	for yy := 0; yy < r.h && yy < len(cells); yy++ {
		for xx := 0; xx < r.w && xx < len(cells[yy]); xx++ {
			gy := 1 + r.y + yy
			gx := 1 + r.x + xx
			if gy >= 0 && gy < len(grid) && gx >= 0 && gx < len(grid[gy]) {
				grid[gy][gx] = cells[yy][xx]
			}
		}
	}
}

func (m model) drawOuterBorder(grid [][]string, width, height int, st lipgloss.Style) {
	if width < 2 || height < 2 {
		return
	}
	top := boxedTop(width, m.termTitle(), st)
	bottom := boxedBottom(width, st)
	for x, ch := range []rune(stripAnsi(top)) {
		if x < width {
			grid[0][x] = st.Render(string(ch))
		}
	}
	for x, ch := range []rune(stripAnsi(bottom)) {
		if x < width {
			grid[height-1][x] = st.Render(string(ch))
		}
	}
	for y := 1; y < height-1; y++ {
		grid[y][0] = st.Render("│")
		grid[y][width-1] = st.Render("│")
	}
}

func (m model) termTitle() string {
	title := " " + m.activeSession + " "
	if n := m.paneCount(); n > 1 {
		title = fmt.Sprintf(" %s · %d panes ", m.activeSession, n)
	}
	return title
}

func boxedTop(width int, title string, st lipgloss.Style) string {
	maxTitle := width - 4
	if maxTitle < 0 {
		maxTitle = 0
	}
	if lipgloss.Width(title) > maxTitle {
		title = strings.TrimRight(truncateToWidth(title, maxTitle), " ")
	}
	remaining := width - 2 - lipgloss.Width(title)
	if remaining < 0 {
		remaining = 0
	}
	return st.Render("╭" + title + strings.Repeat("─", remaining) + "╮")
}

func boxedBottom(width int, st lipgloss.Style) string {
	if width < 2 {
		return st.Render(strings.Repeat("─", width))
	}
	return st.Render("╰" + strings.Repeat("─", width-2) + "╯")
}

// sepTouchesRect reports whether a separator borders the given rectangle, used
// to highlight the separators around the focused pane.
func sepTouchesRect(sp separator, r rect) bool {
	if sp.vertical {
		if sp.x != r.x-1 && sp.x != r.x+r.w {
			return false
		}
		return sp.y < r.y+r.h && sp.y+sp.length > r.y
	}
	if sp.y != r.y-1 && sp.y != r.y+r.h {
		return false
	}
	return sp.x < r.x+r.w && sp.x+sp.length > r.x
}

func termBorderStyle(focused bool) lipgloss.Style {
	if focused {
		return focusBorderStyle
	}
	return borderStyle
}

func clearScreenCmd() tea.Cmd {
	return func() tea.Msg {
		return tea.ClearScreen()
	}
}

// currentGitBranch returns the checked-out branch (or short commit when
// detached) for the directory ru was launched in, or "" when not in a git
// repository. It reads .git/HEAD directly to avoid spawning a subprocess.
func currentGitBranch() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return gitBranchIn(dir)
}

// focusedBranch resolves the git branch for the focused pane's working
// directory (reported via OSC 7), falling back to the launch directory.
func (m model) focusedBranch() string {
	if m.shell != nil {
		if cwd := m.shell.getCwd(); cwd != "" {
			return gitBranchIn(cwd)
		}
	}
	return currentGitBranch()
}

func gitBranchIn(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	return parseGitHead(strings.TrimSpace(string(data)))
}

func parseGitHead(head string) string {
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	}
	if len(head) >= 7 {
		return head[:7] // detached HEAD: short SHA
	}
	return head
}

// findGitDir walks up from start looking for a .git directory or file,
// resolving the "gitdir:" pointer used by worktrees and submodules.
func findGitDir(start string) string {
	dir := start
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return gitPath
			}
			data, readErr := os.ReadFile(gitPath)
			if readErr == nil {
				if gd, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: "); ok {
					if !filepath.IsAbs(gd) {
						gd = filepath.Join(dir, gd)
					}
					return gd
				}
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (m model) renderFooter() string {
	if m.status != "" {
		statusStyle := airlineInfo
		if strings.HasPrefix(m.status, "error:") {
			statusStyle = airlineError
		}
		return renderAirline(m.width, []statusSegment{
			{text: " " + m.modeName() + " ", style: m.modeStyle()},
			{text: " " + m.status + " ", style: statusStyle},
		}, m.branchSegments())
	}

	left := []statusSegment{
		{text: " " + m.modeName() + " ", style: m.modeStyle()},
		{text: " " + m.activeSession + " ", style: airlineFocus},
	}

	right := m.branchSegments()
	right = append(right,
		statusSegment{text: fmt.Sprintf(" jobs %d ", m.jobCount()), style: airlineMuted},
		statusSegment{text: fmt.Sprintf(" slots %d ", m.maxSlots), style: airlineMuted},
		statusSegment{text: " ^B d:detach ^B |/- split ", style: airlineFocus},
	)
	return renderAirline(m.width, left, right)
}

// modeName reports the current editing mode in vim terms: COMMAND while a TUI
// command prompt is open, otherwise INSERT (typing into the focused shell pane).
func (m model) modeName() string {
	if m.inputMode != inputNone {
		return "COMMAND"
	}
	return "INSERT"
}

func (m model) modeStyle() lipgloss.Style {
	if m.inputMode != inputNone {
		return modeCommandStyle
	}
	return modeInsertStyle
}

// branchSegments returns the git-branch status segment for the launch
// directory, or nothing when not in a git repository.
func (m model) branchSegments() []statusSegment {
	if m.gitBranch == "" {
		return nil
	}
	return []statusSegment{{text: " ⎇ " + m.gitBranch + " ", style: branchStyle}}
}

type statusSegment struct {
	text  string
	style lipgloss.Style
}

func renderAirline(width int, left, right []statusSegment) string {
	if width <= 0 {
		return ""
	}

	leftStr := renderStatusSegments(left)
	rightStr := renderStatusSegments(right)
	space := width - lipgloss.Width(leftStr) - lipgloss.Width(rightStr)
	if space < 1 {
		line := leftStr + rightStr
		return fitToWidth(line, width)
	}
	return leftStr + airlineMuted.Render(strings.Repeat(" ", space)) + rightStr
}

func renderStatusSegments(segments []statusSegment) string {
	var b strings.Builder
	for i, segment := range segments {
		if i > 0 {
			b.WriteString(airlineMuted.Render(" "))
		}
		b.WriteString(segment.style.Render(segment.text))
	}
	return b.String()
}

func (m model) jobCount() int {
	total := 0
	for _, node := range m.nodes {
		for _, session := range node.sessions {
			total += len(node.jobs[session.Name])
		}
	}
	return total
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

func fitToWidth(s string, width int) string {
	n := lipgloss.Width(s)
	if n == width {
		return s
	}
	if n > width {
		return truncateToWidth(s, width)
	}
	return s + strings.Repeat(" ", width-n)
}

func truncateToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	plain := stripAnsi(s)
	var b strings.Builder
	for _, r := range plain {
		next := b.String() + string(r)
		if lipgloss.Width(next) > width {
			break
		}
		b.WriteRune(r)
	}
	return padRight(b.String(), width)
}

func createSession(name string) tea.Cmd {
	return func() tea.Msg {
		err := client.SessionCreate(name)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("created session %q", name)}
	}
}

func createSessionInGroup(name, group string) tea.Cmd {
	return func() tea.Msg {
		if err := client.SessionCreate(name); err != nil {
			return actionDoneMsg{err: err}
		}
		if group != "" && group != "default" {
			if err := client.SessionMove(name, group); err != nil {
				return actionDoneMsg{err: err}
			}
		}
		return actionDoneMsg{status: fmt.Sprintf("created session %q", name)}
	}
}

func deleteSession(name string) tea.Cmd {
	return func() tea.Msg {
		err := client.SessionDelete(name)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("deleted session %q", name)}
	}
}

func renameSession(oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		err := client.SessionRename(oldName, newName)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("renamed %q -> %q", oldName, newName)}
	}
}

func deleteGroup(name string) tea.Cmd {
	return func() tea.Msg {
		err := client.GroupDelete(name)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("deleted group %q", name)}
	}
}

func moveSession(session, group string) tea.Cmd {
	return func() tea.Msg {
		err := client.SessionMove(session, group)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("moved %q to %q", session, group)}
	}
}

// editSession renames and/or moves a session in one sequential command so the
// rename lands before the move references the new name.
func editSession(oldName, newName, oldGroup, newGroup string) tea.Cmd {
	return func() tea.Msg {
		cur := oldName
		if newName != "" && newName != oldName {
			if err := client.SessionRename(oldName, newName); err != nil {
				return actionDoneMsg{err: err}
			}
			cur = newName
		}
		if newGroup != oldGroup {
			g := newGroup
			if g == "" {
				g = "default"
			}
			if err := client.SessionMove(cur, g); err != nil {
				return actionDoneMsg{err: err}
			}
		}
		return actionDoneMsg{status: fmt.Sprintf("updated session %q", cur)}
	}
}

func killJob(id int) tea.Cmd {
	return func() tea.Msg {
		err := client.KillJob(id)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("killed job %d", id)}
	}
}

func clearFinishedCmd() tea.Cmd {
	return func() tea.Msg {
		if err := client.ClearFinished(); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "cleared finished jobs"}
	}
}

func removeJob(id int) tea.Cmd {
	return func() tea.Msg {
		err := client.RemoveJob(id)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("removed job %d", id)}
	}
}

func rerunJob(id int) tea.Cmd {
	return func() tea.Msg {
		newID, err := client.Rerun(id)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("reran job %d as %d", id, newID)}
	}
}

func makeUrgent(id int) tea.Cmd {
	return func() tea.Msg {
		err := client.MakeUrgent(id)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("job %d moved to front", id)}
	}
}

// Run opens the interactive TUI on the jobs & sessions management screen.
func Run() error { return run(false) }

// RunJobs opens the management screen in jobs-only mode, where quitting exits
// the program instead of dropping to a session (used by `ru -j`).
func RunJobs() error { return run(true) }

func run(jobsOnly bool) error {
	// Home is the full-screen management view. No terminal session is attached
	// until the user opens one from the list, so no shell is built here — this
	// also means running `ru` from inside an existing qrush pane never
	// re-attaches to (or nests on) the pane it was launched from.
	m := newModel(nil)
	m.activeSession = ""
	m.jobsOnly = jobsOnly
	m, _ = m.openJobsView()

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if fm, ok := finalModel.(model); ok {
		fm.closeShells()
	}
	return err
}

func (m model) closeShells() {
	closed := make(map[*shellState]bool)
	for _, sh := range m.panes {
		if sh != nil && !closed[sh] {
			sh.close()
			closed[sh] = true
		}
	}
}
