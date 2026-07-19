package tui

import "github.com/charmbracelet/lipgloss"

// --- theme -----------------------------------------------------------------
//
// All colors flow through the semantic tokens below. Each is a
// lipgloss.AdaptiveColor: the Dark values are the palette qrush has always
// shipped (so dark terminals render exactly as before), and the Light values
// are counterparts chosen so the UI stays legible on light backgrounds.
// To retheme the whole app, edit these tokens — not the styles beneath them.
var (
	// Surfaces: progressively lighter panels on dark, darker on light.
	cBarBg    = lipgloss.AdaptiveColor{Dark: "234", Light: "254"} // deepest bar
	cPanelBg  = lipgloss.AdaptiveColor{Dark: "235", Light: "253"} // pane fill
	cHeaderBg = lipgloss.AdaptiveColor{Dark: "236", Light: "252"} // table header / info
	cSubtleBg = lipgloss.AdaptiveColor{Dark: "238", Light: "251"} // focused airline segment

	// Text: from brightest heading to faintest hint.
	cFgBright = lipgloss.AdaptiveColor{Dark: "255", Light: "235"}
	cFg       = lipgloss.AdaptiveColor{Dark: "252", Light: "238"}
	cFgMuted  = lipgloss.AdaptiveColor{Dark: "246", Light: "242"}
	cFgFaint  = lipgloss.AdaptiveColor{Dark: "244", Light: "245"}
	cFgDim    = lipgloss.AdaptiveColor{Dark: "241", Light: "248"}

	// Hairlines / borders.
	cRule   = lipgloss.AdaptiveColor{Dark: "237", Light: "250"}
	cBorder = lipgloss.AdaptiveColor{Dark: "237", Light: "250"}

	// Accents: hues carry semantic meaning across the whole TUI.
	cAccent = lipgloss.AdaptiveColor{Dark: "73", Light: "30"}   // cyan — focus/normal
	cGreen  = lipgloss.AdaptiveColor{Dark: "114", Light: "28"}  // running / insert
	cAmber  = lipgloss.AdaptiveColor{Dark: "179", Light: "130"} // queued / command
	cRed    = lipgloss.AdaptiveColor{Dark: "203", Light: "160"} // error / failed
	cBlue   = lipgloss.AdaptiveColor{Dark: "110", Light: "25"}  // ids / info
	cPurple = lipgloss.AdaptiveColor{Dark: "140", Light: "90"}  // git branch

	// Selection: readable dark ink on the accent fills. On-accent text stays
	// near-black in both modes because the fills themselves are saturated.
	cInk      = lipgloss.AdaptiveColor{Dark: "16", Light: "231"}
	cCursorBg = cAccent
	cSelectBg = lipgloss.AdaptiveColor{Dark: "110", Light: "111"}
	cInactBg  = lipgloss.AdaptiveColor{Dark: "236", Light: "251"}
	cInactFg  = lipgloss.AdaptiveColor{Dark: "248", Light: "241"}

	// cRowFocusBg tints the focused row in the jobs table. Unlike the solid
	// accent bar, it is dark/desaturated enough that each cell keeps its own
	// semantic foreground (running=green, failed=red, …) and stays legible.
	cRowFocusBg = lipgloss.AdaptiveColor{Dark: "23", Light: "195"}
)

var (
	groupStyle       = lipgloss.NewStyle().Bold(true).Foreground(cFg)
	sessionStyle     = lipgloss.NewStyle().Foreground(cFgBright)
	treeIconStyle    = lipgloss.NewStyle().Foreground(cAccent)
	folderStyle      = lipgloss.NewStyle().Foreground(cAmber)
	modalTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	modalActiveStyle = lipgloss.NewStyle().Bold(true).Foreground(cFgBright)
	treeSummaryStyle = lipgloss.NewStyle().Foreground(cFgFaint)
	treeEmptyStyle   = lipgloss.NewStyle().Italic(true).Foreground(cFgDim)
	treePaneStyle    = lipgloss.NewStyle().Background(cPanelBg)
	cursorStyle      = lipgloss.NewStyle().Foreground(cInk).Background(cCursorBg)
	// inputCursorStyle is the text-input caret: a maximum-contrast solid block
	// (white in dark mode, near-black in light). NOTE: bubbles' cursor.View
	// applies Reverse(true) on top of this style, swapping fg/bg at render
	// time — so the *foreground* here is the block color the user sees.
	inputCursorStyle    = lipgloss.NewStyle().Foreground(cFgBright).Background(cInk)
	cursorInactiveStyle = lipgloss.NewStyle().Foreground(cInactFg).Background(cInactBg)
	selectedStyle       = lipgloss.NewStyle().Foreground(cInk).Background(cSelectBg)
	cursorSelectedStyle = lipgloss.NewStyle().Foreground(cInk).Background(cSelectBg)
	runningStyle        = lipgloss.NewStyle().Bold(true).Foreground(cGreen)
	queuedStyle         = lipgloss.NewStyle().Foreground(cAmber)
	finishedStyle       = lipgloss.NewStyle().Foreground(cFgFaint)
	finishedErrStyle    = lipgloss.NewStyle().Bold(true).Foreground(cRed)
	skippedStyle        = lipgloss.NewStyle().Foreground(cFgMuted)
	jobIDStyle          = lipgloss.NewStyle().Foreground(cBlue)
	jobNameStyle        = lipgloss.NewStyle().Foreground(cAccent)
	statusBarStyle      = lipgloss.NewStyle().Background(cBarBg).Foreground(cFgDim)
	inputStyle          = lipgloss.NewStyle().Foreground(cBlue)
	helpStyle           = lipgloss.NewStyle().Foreground(cFgFaint)
	borderStyle         = lipgloss.NewStyle().Foreground(cBorder)
	focusBorderStyle    = lipgloss.NewStyle().Foreground(cAccent)
	airlineMode         = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent)
	modeNormalStyle     = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent) // cyan
	modeInsertStyle     = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cGreen)  // green
	modeCommandStyle    = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAmber)  // amber
	branchStyle         = lipgloss.NewStyle().Foreground(cPurple).Background(cHeaderBg)
	airlineFocus        = lipgloss.NewStyle().Foreground(cFgBright).Background(cSubtleBg)
	airlineInfo         = lipgloss.NewStyle().Foreground(cFg).Background(cHeaderBg)
	airlineMuted        = lipgloss.NewStyle().Foreground(cFgMuted).Background(cBarBg)
	airlineError        = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cRed)
	headerActive        = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent)
	headerInactive      = lipgloss.NewStyle().Bold(true).Foreground(cFgDim).Background(cBarBg)
	jobsHeaderStyle     = lipgloss.NewStyle().Bold(true).Foreground(cFgBright).Background(cHeaderBg)
	jobsDetailKeyStyle  = lipgloss.NewStyle().Foreground(cBlue)
	jobsDetailRuleStyle = lipgloss.NewStyle().Foreground(cRule)
)
