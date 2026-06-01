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
	width       int
	height      int
	treeWidth   int
	treeVisible bool
	treeWide    bool
	focus       pane

	commaPressed bool
	commaTimer   int
	ctrlWPressed bool
	ctrlWTimer   int
	ctrlBPressed bool
	ctrlBTimer   int
	gPressed     bool
	gTimer       int
	nextPaneID   int

	nodes         []treeNode
	cursor        int
	selected      map[string]bool
	maxSlots      int
	err           error
	status        string
	inputMode     inputKind
	textInput     textinput.Model
	cmdInput      textinput.Model
	renameOld     string
	renameGroup   bool
	createGroup   bool
	createInGroup string
	moveSession   string

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
		textInput:     ti,
		cmdInput:      ci,
		treeVisible:   true,
		focus:         paneTerm,
		selected:      make(map[string]bool),
		layouts:       layouts,
		panes:         panes,
		focusPane:     focusPane,
		nextPaneID:    nextPaneID,
		shell:         sh,
		activeSession: activeSession,
		gitBranch:     currentGitBranch(),
		hwMon:         sysmon.New(),
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
		m.treeWidth = computeTreeWidth(m.width, m.treeWide)
		m.resizeShell()
		return m, clearScreenCmd()

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
		total := totalRows(m.nodes)
		if m.cursor >= total && total > 0 {
			m.cursor = total - 1
		}
		m.pruneSelection()
		// Another client (`ru --jobs` from inside a pane) asked us to open the
		// jobs view. Honour it only from the split view so we don't disturb a
		// pager or an already-open table.
		if msg.openJobsView && m.viewMode == viewSplit {
			m = m.openJobsView()
			return m, tea.Batch(clearScreenCmd(), sampleHWCmd(m.hwMon))
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

	case commaTimeoutMsg:
		if msg.id == m.commaTimer && m.commaPressed {
			m.commaPressed = false
			if m.focus == paneTerm && m.shell != nil {
				m.shell.write([]byte(","))
			}
		}
		return m, nil

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

	case gTimeoutMsg:
		if msg.id == m.gTimer && m.gPressed {
			m.gPressed = false
		}
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

	case tea.KeyMsg:
		if m.viewMode == viewJobs {
			return m.handleJobsKey(msg)
		}
		if m.inputMode == inputCommand {
			return m.handleCommandKey(msg)
		}
		if m.inputMode != inputNone && m.focus == paneTree {
			return m.handleInputKey(msg)
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
		m, handled, cmd = m.handleCommaN(msg)
		if handled {
			return m, cmd
		}
		switch m.focus {
		case paneTree:
			return m.handleTreeKey(msg)
		case paneTerm:
			return m.handleTermKey(msg)
		}
	}
	return m, nil
}

func (m model) handleCommaN(msg tea.KeyMsg) (model, bool, tea.Cmd) {
	// Don't intercept commas when the user is typing in a full-screen TUI
	// program (vim/nano/etc.) so commit messages, search text, etc. pass
	// through cleanly.
	if m.focus == paneTerm && m.shell != nil && m.shell.inAltScreen() {
		return m, false, nil
	}

	key := msg.String()

	if m.commaPressed {
		m.commaPressed = false
		if key == "n" {
			m.treeVisible = !m.treeVisible
			if !m.treeVisible && m.focus == paneTree {
				m.focus = paneTerm
			}
			m.treeWidth = computeTreeWidth(m.width, m.treeWide)
			m.resizeShell()
			return m, true, nil
		}
		if m.focus == paneTerm && m.shell != nil {
			m.shell.write([]byte(","))
		}
		return m, false, nil
	}

	if key == "," {
		m.commaPressed = true
		m.commaTimer++
		timerID := m.commaTimer
		return m, true, tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
			return commaTimeoutMsg{id: timerID}
		})
	}

	return m, false, nil
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

// ctrlWLeft moves to the pane on the left, falling back to the tree when there
// is no pane there (the tree sits to the left of the terminal region).
func (m model) ctrlWLeft() model {
	if m.focus == paneTree {
		return m
	}
	if m.movePaneFocus(splitVert, false) {
		return m
	}
	if m.treeVisible {
		m.focus = paneTree
	}
	return m
}

// ctrlWRight moves into the terminal from the tree, or to the pane on the right.
func (m model) ctrlWRight() model {
	if m.focus == paneTree {
		m.focus = paneTerm
		return m
	}
	m.movePaneFocus(splitVert, true)
	return m
}

// ctrlWVert moves between vertically stacked panes (or enters the terminal from
// the tree).
func (m model) ctrlWVert(down bool) model {
	if m.focus == paneTree {
		m.focus = paneTerm
		return m
	}
	m.movePaneFocus(splitHoriz, down)
	return m
}

// ctrlWCycle cycles focus through the tree (if visible) and every pane in turn.
func (m model) ctrlWCycle() model {
	var leaves []*paneNode
	if root := m.activeRoot(); root != nil {
		leaves = root.leaves()
	}
	if m.focus == paneTree {
		if len(leaves) > 0 {
			m.focus = paneTerm
			m.focusPane = leaves[0]
			m.syncFocusShell()
		}
		return m
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
	// Past the last pane: return to the tree, or wrap to the first pane.
	if m.treeVisible {
		m.focus = paneTree
	} else if len(leaves) > 0 {
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
			nm := m.openJobsView()
			return nm, true, sampleHWCmd(nm.hwMon)
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

func (m model) handleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "g" {
		if m.gPressed {
			m.gPressed = false
			m.cursor = 0
			m.status = ""
			return m, nil
		}
		m.gPressed = true
		m.gTimer++
		timerID := m.gTimer
		return m, tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
			return gTimeoutMsg{id: timerID}
		})
	}
	if m.gPressed {
		m.gPressed = false
	}

	action := mapTreeKey(msg)
	total := totalRows(m.nodes)

	switch action {
	case keyQuit:
		if m.shellDone() {
			return m, tea.Quit
		}
		m.focus = paneTerm
		m.status = ""

	case keyUp:
		if m.cursor > 0 {
			m.cursor--
		}
		m.status = ""

	case keyDown:
		if m.cursor < total-1 {
			m.cursor++
		}
		m.status = ""

	case keyBottom:
		if total > 0 {
			m.cursor = total - 1
		}
		m.status = ""

	case keyToggle:
		if total == 0 {
			break
		}
		info := rowAt(m.nodes, m.cursor)
		if info.isGroup {
			m.nodes[info.nodeIdx].expanded = !m.nodes[info.nodeIdx].expanded
		} else if info.isSession {
			session := m.nodes[info.nodeIdx].sessions[info.sessionIdx].Name
			var cmd tea.Cmd
			m, cmd = m.activateSession(session)
			return m, cmd
		}

	case keySelect:
		if total == 0 {
			break
		}
		m.toggleSelectedRow(m.cursor)
		if m.cursor < total-1 {
			m.cursor++
		}

	case keySelectAll:
		m.toggleSelectAllVisible()

	case keyCollapse:
		if total == 0 {
			break
		}
		info := rowAt(m.nodes, m.cursor)
		if info.isSession {
			m.cursor = parentGroupRow(m.nodes, m.cursor)
		} else {
			m.nodes[info.nodeIdx].expanded = false
		}

	case keyExpandTree:
		m.treeWide = !m.treeWide
		m.treeWidth = computeTreeWidth(m.width, m.treeWide)
		m.resizeShell()
		return m, clearScreenCmd()

	case keyCreateSession:
		m.inputMode = inputCreate
		m.createGroup = false
		m.createInGroup = m.selectedGroup()
		m.textInput.SetValue("")
		m.textInput.Placeholder = "session name"
		m.textInput.Focus()
		m.resizeShell()
		return m, textinput.Blink

	case keyCreateGroup:
		m.inputMode = inputCreate
		m.createGroup = true
		m.createInGroup = ""
		m.textInput.SetValue("")
		m.textInput.Placeholder = "group name"
		m.textInput.Focus()
		m.resizeShell()
		return m, textinput.Blink

	case keyDeleteSession:
		if len(m.selected) > 0 {
			targets := m.selectedTargets()
			m.selected = make(map[string]bool)
			return m, deleteSelected(targets)
		}
		if total == 0 {
			break
		}
		info := rowAt(m.nodes, m.cursor)
		if info.isSession {
			name := m.nodes[info.nodeIdx].sessions[info.sessionIdx].Name
			return m, deleteSession(name)
		}
		if info.isGroup {
			name := m.nodes[info.nodeIdx].group
			return m, deleteGroup(name)
		}

	case keyRenameSession:
		if total == 0 {
			break
		}
		info := rowAt(m.nodes, m.cursor)
		if info.isSession {
			name := m.nodes[info.nodeIdx].sessions[info.sessionIdx].Name
			m.inputMode = inputRename
			m.renameGroup = false
			m.renameOld = name
			m.textInput.SetValue(name)
			m.textInput.Placeholder = "new name"
			m.textInput.Focus()
			m.resizeShell()
			return m, textinput.Blink
		}
		if info.isGroup {
			name := m.nodes[info.nodeIdx].group
			m.inputMode = inputRename
			m.renameGroup = true
			m.renameOld = name
			m.textInput.SetValue(name)
			m.textInput.Placeholder = "new name"
			m.textInput.Focus()
			m.resizeShell()
			return m, textinput.Blink
		}

	case keyMoveSession:
		if total == 0 {
			break
		}
		info := rowAt(m.nodes, m.cursor)
		if info.isSession {
			m.inputMode = inputMove
			m.moveSession = m.nodes[info.nodeIdx].sessions[info.sessionIdx].Name
			m.textInput.SetValue(m.nodes[info.nodeIdx].group)
			m.textInput.Placeholder = "group name"
			m.textInput.Focus()
			m.resizeShell()
			return m, textinput.Blink
		}
	case keyRemoveJob:
		if len(m.selected) > 0 {
			var cmd tea.Cmd
			m, cmd = m.resetSelectedShells(m.selectedTargets())
			return m, cmd
		}
		if total == 0 {
			break
		}
		info := rowAt(m.nodes, m.cursor)
		if info.isSession {
			session := m.nodes[info.nodeIdx].sessions[info.sessionIdx].Name
			var cmd tea.Cmd
			m, cmd = m.resetSessionShell(session)
			return m, cmd
		}
		if info.isGroup {
			m.status = "select a session to reset"
		}
	}
	return m, nil
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

type selectedTargets struct {
	groups   []string
	sessions []string
}

func (m model) selectedTargets() selectedTargets {
	var targets selectedTargets
	for key := range m.selected {
		switch {
		case strings.HasPrefix(key, "g:"):
			targets.groups = append(targets.groups, strings.TrimPrefix(key, "g:"))
		case strings.HasPrefix(key, "s:"):
			targets.sessions = append(targets.sessions, strings.TrimPrefix(key, "s:"))
		}
	}
	return targets
}

func (m *model) toggleSelectedRow(row int) {
	info := rowAt(m.nodes, row)
	key := info.key(m.nodes)
	if key == "" {
		return
	}
	if m.selected[key] {
		delete(m.selected, key)
	} else {
		m.selected[key] = true
	}
}

func (m *model) toggleSelectAllVisible() {
	total := totalRows(m.nodes)
	if total == 0 {
		return
	}
	allSelected := true
	keys := make([]string, 0, total)
	for row := 0; row < total; row++ {
		key := rowAt(m.nodes, row).key(m.nodes)
		if key == "" {
			continue
		}
		keys = append(keys, key)
		if !m.selected[key] {
			allSelected = false
		}
	}
	for _, key := range keys {
		if allSelected {
			delete(m.selected, key)
		} else {
			m.selected[key] = true
		}
	}
}

func (m *model) pruneSelection() {
	valid := make(map[string]bool)
	total := totalRows(m.nodes)
	for row := 0; row < total; row++ {
		key := rowAt(m.nodes, row).key(m.nodes)
		if key != "" {
			valid[key] = true
		}
	}
	for key := range m.selected {
		if !valid[key] {
			delete(m.selected, key)
		}
	}
}

func (m model) resetSelectedShells(targets selectedTargets) (model, tea.Cmd) {
	sessions := make(map[string]bool)
	for _, session := range targets.sessions {
		sessions[session] = true
	}
	for _, group := range targets.groups {
		for _, session := range m.sessionsInGroup(group) {
			sessions[session] = true
		}
	}
	if len(sessions) == 0 {
		m.status = "select a session to reset"
		return m, nil
	}

	var cmds []tea.Cmd
	for session := range sessions {
		var cmd tea.Cmd
		m, cmd = m.resetSessionShell(session)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	m.selected = make(map[string]bool)
	m.status = fmt.Sprintf("reset %d session shell(s)", len(sessions))
	cmds = append(cmds, clearScreenCmd())
	return m, tea.Batch(cmds...)
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

func (m model) sessionsInGroup(group string) []string {
	for _, node := range m.nodes {
		if node.group == group {
			sessions := make([]string, 0, len(node.sessions))
			for _, session := range node.sessions {
				sessions = append(sessions, session.Name)
			}
			return sessions
		}
	}
	return nil
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
		nm := m.openJobsView()
		return nm, sampleHWCmd(nm.hwMon)
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
	case "tree":
		m.treeVisible = true
		m.focus = paneTree
		m.resizeShell()
	case "term", "terminal", "shell":
		m.focus = paneTerm
	case "toggle", "toggle-tree":
		m.treeVisible = !m.treeVisible
		if !m.treeVisible && m.focus == paneTree {
			m.focus = paneTerm
		}
		m.resizeShell()
	default:
		m.status = fmt.Sprintf("unknown command: %s", input)
	}
	return m, nil
}

func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputMode = inputNone
		m.textInput.Blur()
		m.createGroup = false
		m.createInGroup = ""
		m.renameGroup = false
		m.moveSession = ""
		m.resizeShell()
		return m, nil
	case "enter":
		value := m.textInput.Value()
		m.textInput.Blur()
		mode := m.inputMode
		m.inputMode = inputNone
		m.resizeShell()
		if value == "" {
			return m, nil
		}
		switch mode {
		case inputCreate:
			if m.createGroup {
				m.createGroup = false
				return m, createGroup(value)
			}
			group := m.createInGroup
			m.createInGroup = ""
			return m, createSessionInGroup(value, group)
		case inputRename:
			if m.renameGroup {
				m.renameGroup = false
				return m, renameGroup(m.renameOld, value)
			}
			return m, renameSession(m.renameOld, value)
		case inputMove:
			session := m.moveSession
			m.moveSession = ""
			return m, moveSession(session, value)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
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
	treeRegionLines := m.renderTreeRegion(contentHeight + 1)

	for row := 0; row < contentHeight+1; row++ {
		if m.treeVisible {
			if row < len(treeRegionLines) {
				b.WriteString(treeRegionLines[row])
			} else {
				b.WriteString(fitToWidth("", m.treeWidth))
			}
		}
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
	} else if m.inputMode == inputCreate || m.inputMode == inputRename || m.inputMode == inputMove {
		prompt := "new session: "
		if m.inputMode == inputCreate && m.createGroup {
			prompt = "new group: "
		}
		if m.inputMode == inputRename {
			prompt = "rename to: "
		}
		if m.inputMode == inputMove {
			prompt = "move to group: "
		}
		b.WriteString(m.modeStyle().Render(" COMMAND "))
		b.WriteString(" ")
		b.WriteString(inputStyle.Render(prompt))
		b.WriteString(m.textInput.View())
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

func (m model) renderTreeRegion(height int) []string {
	if !m.treeVisible {
		return nil
	}
	width := m.treeWidth
	if width < 4 {
		width = 4
	}
	if height < 3 {
		height = 3
	}
	focused := m.focus == paneTree
	st := termBorderStyle(focused)
	lines := make([]string, height)
	lines[0] = boxedTop(width, treeTitle(m.treeWide), st)
	innerW := width - 2
	innerH := height - 2
	treeLines := renderTreeLines(m.nodes, m.cursor, innerH, innerW, m.selected, focused)
	for row := 0; row < innerH; row++ {
		content := ""
		if row < len(treeLines) {
			content = treeLines[row]
		}
		lines[row+1] = st.Render("│") + treePaneStyle.Render(fitToWidth(content, innerW)) + st.Render("│")
	}
	lines[height-1] = boxedBottom(width, st)
	return lines
}

func treeTitle(wide bool) string {
	if wide {
		return " sessions wide "
	}
	return " sessions "
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
		{text: " " + m.statusLocation() + " ", style: airlineFocus},
	}
	if selected := m.selectedStatus(); selected != "" {
		left = append(left, statusSegment{text: " " + selected + " ", style: airlineInfo})
	}

	right := m.branchSegments()
	right = append(right,
		statusSegment{text: fmt.Sprintf(" sel %d ", len(m.selected)), style: airlineMuted},
		statusSegment{text: fmt.Sprintf(" jobs %d ", m.jobCount()), style: airlineMuted},
		statusSegment{text: fmt.Sprintf(" slots %d ", m.maxSlots), style: airlineMuted},
		statusSegment{text: " gg G space v r d M ", style: airlineFocus},
	)
	return renderAirline(m.width, left, right)
}

// modeName reports the current editing mode in vim terms: COMMAND while a TUI
// command/text prompt is open, INSERT when typing into a shell pane, NORMAL
// while navigating the tree.
func (m model) modeName() string {
	switch {
	case m.inputMode != inputNone:
		return "COMMAND"
	case m.focus == paneTerm:
		return "INSERT"
	default:
		return "NORMAL"
	}
}

func (m model) modeStyle() lipgloss.Style {
	switch {
	case m.inputMode != inputNone:
		return modeCommandStyle
	case m.focus == paneTerm:
		return modeInsertStyle
	default:
		return modeNormalStyle
	}
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

func (m model) statusLocation() string {
	if m.focus == paneTerm || !m.treeVisible {
		return m.activeSession
	}
	info := rowAt(m.nodes, m.cursor)
	if info.nodeIdx >= 0 && info.nodeIdx < len(m.nodes) {
		if info.isSession {
			return m.nodes[info.nodeIdx].sessions[info.sessionIdx].Name
		}
		return m.nodes[info.nodeIdx].group
	}
	return "tree"
}

func (m model) selectedStatus() string {
	if m.focus != paneTree || !m.treeVisible || totalRows(m.nodes) == 0 {
		return ""
	}
	info := rowAt(m.nodes, m.cursor)
	if info.nodeIdx < 0 || info.nodeIdx >= len(m.nodes) {
		return ""
	}
	node := m.nodes[info.nodeIdx]
	if info.isSession {
		session := node.sessions[info.sessionIdx]
		return fmt.Sprintf("%s (%d jobs)", session.Name, len(node.jobs[session.Name]))
	}
	return fmt.Sprintf("%s (%d sessions)", node.group, len(node.sessions))
}

func (m model) selectedGroup() string {
	if totalRows(m.nodes) == 0 {
		return "default"
	}
	info := rowAt(m.nodes, m.cursor)
	if info.nodeIdx >= 0 && info.nodeIdx < len(m.nodes) {
		return m.nodes[info.nodeIdx].group
	}
	return "default"
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
	if m.treeVisible {
		w = m.width - m.treeWidth
	}
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

func computeTreeWidth(totalWidth int, wide bool) int {
	if wide {
		w := totalWidth * 55 / 100
		if w > 80 {
			w = 80
		}
		if w < 30 {
			w = 30
		}
		if totalWidth-w < 20 {
			w = totalWidth - 20
		}
		if w < 20 {
			w = 20
		}
		return w
	}
	w := totalWidth * 30 / 100
	if w > 40 {
		w = 40
	}
	if w < 20 {
		w = 20
	}
	return w
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

func createGroup(name string) tea.Cmd {
	return func() tea.Msg {
		err := client.GroupCreate(name)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("created group %q", name)}
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

func renameGroup(oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		err := client.GroupRename(oldName, newName)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("renamed group %q -> %q", oldName, newName)}
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

func deleteSelected(targets selectedTargets) tea.Cmd {
	return func() tea.Msg {
		deleted := 0
		for _, session := range targets.sessions {
			if err := client.SessionDelete(session); err != nil {
				return actionDoneMsg{err: fmt.Errorf("delete session %q: %w", session, err)}
			}
			deleted++
		}
		for _, group := range targets.groups {
			if err := client.GroupDelete(group); err != nil {
				return actionDoneMsg{err: fmt.Errorf("delete group %q: %w", group, err)}
			}
			deleted++
		}
		return actionDoneMsg{status: fmt.Sprintf("deleted %d item(s)", deleted)}
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

// Run opens the interactive TUI in the normal split view.
func Run() error { return run(false) }

// RunJobs opens the interactive TUI directly in the job-management view.
func RunJobs() error { return run(true) }

func run(startInJobs bool) error {
	m := newModel(nil)

	// The job-management view only reads job data — it never attaches to the
	// terminal panes. Skip building the shell session entirely so that running
	// `ru --jobs` from inside an existing interactive session does not
	// re-attach to (and recursively nest on) the very pane it was launched
	// from, nor clobber that session's saved layout.
	if startInJobs {
		m.activeSession = "default"
		m.jobsOnly = true
		m = m.openJobsView()
		p := tea.NewProgram(m, tea.WithAltScreen())
		_, err := p.Run()
		return err
	}

	created := (&m).buildSession("default", 80, 24)
	if len(created) == 0 {
		return fmt.Errorf("could not start terminal session")
	}
	m.activeSession = "default"
	m.focusPane = firstLeaf(m.layouts["default"])
	m.syncFocusShell()
	m.focus = paneTerm
	// Record the (possibly pruned or freshly opened) layout up front so a
	// crash before any structural change still leaves a consistent layout.
	_ = client.SetTerminalLayout("default", marshalLayout(m.layouts["default"]), paneNames(m.layouts["default"]))

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
