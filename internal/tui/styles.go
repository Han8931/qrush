package tui

import "github.com/charmbracelet/lipgloss"

var (
	groupStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	sessionStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	treeIconStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("73"))
	treeSummaryStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	treeEmptyStyle      = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("241"))
	treePaneStyle       = lipgloss.NewStyle().Background(lipgloss.Color("235"))
	cursorStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("73"))
	cursorInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("248")).Background(lipgloss.Color("236"))
	selectedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("110"))
	cursorSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("110"))
	runningStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114"))
	queuedStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
	finishedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	finishedErrStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	skippedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	jobIDStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	statusBarStyle      = lipgloss.NewStyle().Background(lipgloss.Color("234")).Foreground(lipgloss.Color("248"))
	inputStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	helpStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	borderStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	focusBorderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("73"))
	airlineMode         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("73"))
	modeNormalStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("73"))  // cyan
	modeInsertStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("114")) // green
	modeCommandStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("179")) // amber
	branchStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("140")).Background(lipgloss.Color("236"))
	airlineFocus        = lipgloss.NewStyle().Foreground(lipgloss.Color("254")).Background(lipgloss.Color("238"))
	airlineInfo         = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236"))
	airlineMuted        = lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Background(lipgloss.Color("234"))
	airlineError        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("203"))
	headerActive        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("73"))
	headerInactive      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("248")).Background(lipgloss.Color("234"))
	jobsHeaderStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("254")).Background(lipgloss.Color("236"))
	jobsSummaryStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("234"))
	jobsDetailKeyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	jobsDetailRuleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
)
