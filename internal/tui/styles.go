package tui

import "github.com/charmbracelet/lipgloss"

// --- theme -----------------------------------------------------------------
//
// All colors flow through the semantic tokens below. Each is a
// lipgloss.AdaptiveColor with charcoal surfaces and soft cyan accents in dark
// mode, and pale neutral surfaces with deeper accents in light mode.
// To retheme the whole app, edit these tokens — not the styles beneath them.
var (
	// Surfaces: progressively lighter panels on dark, darker on light. The dark
	// surfaces are lifted a step off pure black so panels read as distinct
	// layers instead of a single dark void.
	cBarBg    = lipgloss.AdaptiveColor{Dark: "#181C20", Light: "#F8FAFB"} // deepest bar
	cPanelBg  = lipgloss.AdaptiveColor{Dark: "#202529", Light: "#F2F4F5"} // pane fill
	cHeaderBg = lipgloss.AdaptiveColor{Dark: "#292F34", Light: "#E5EAED"} // table header / info
	cSubtleBg = lipgloss.AdaptiveColor{Dark: "#343D44", Light: "#DCE3E7"} // focused airline segment

	// Text: from brightest heading to faintest hint. The lower half of the ramp
	// is brightened so secondary text (hints, muted rows, help) stays legible.
	cFgBright = lipgloss.AdaptiveColor{Dark: "#EDF2F5", Light: "#202A32"}
	cFg       = lipgloss.AdaptiveColor{Dark: "#D5DEE4", Light: "#303D47"}
	cFgMuted  = lipgloss.AdaptiveColor{Dark: "#BCC7CF", Light: "#44535F"}
	cFgFaint  = lipgloss.AdaptiveColor{Dark: "#ADB9C2", Light: "#4B5B67"}
	cFgDim    = lipgloss.AdaptiveColor{Dark: "#A6B0B8", Light: "#52606A"}

	// Hairlines / borders: raised well above the panel fill so frames and
	// dividers are actually visible on dark backgrounds.
	cRule   = lipgloss.AdaptiveColor{Dark: "#3B464F", Light: "#C2CDD4"}
	cBorder = lipgloss.AdaptiveColor{Dark: "#53616C", Light: "#9BAAB5"}

	// Accents: hues carry semantic meaning across the whole TUI. Brightened on
	// dark for punchier, more legible status colors.
	cAccent = lipgloss.AdaptiveColor{Dark: "#89CDD3", Light: "#176570"} // cyan — focus/normal
	cGreen  = lipgloss.AdaptiveColor{Dark: "#A4CCA5", Light: "#32643D"} // running / insert
	cAmber  = lipgloss.AdaptiveColor{Dark: "#DEBF89", Light: "#805512"} // queued / command
	cRed    = lipgloss.AdaptiveColor{Dark: "#E8A09A", Light: "#A13435"} // error / failed
	cBlue   = lipgloss.AdaptiveColor{Dark: "#A7BFDA", Light: "#365F86"} // info

	// Badges use contrasting ink; pale selections have their own dark text.
	cInk      = lipgloss.AdaptiveColor{Dark: "#181C20", Light: "#FFFFFF"}
	cCursorBg = cAccent
	cSelectBg = lipgloss.AdaptiveColor{Dark: "#9CBFCB", Light: "#B5D3DE"}
	cSelectFg = lipgloss.AdaptiveColor{Dark: "#182830", Light: "#243A44"}
	cInactBg  = lipgloss.AdaptiveColor{Dark: "#2C3339", Light: "#E3E8EB"}
	cInactFg  = cFgMuted

	// cRowFocusBg tints the focused row in the jobs table. Unlike the solid
	// accent bar, it is dark/desaturated enough that each cell keeps its own
	// semantic foreground (running=green, failed=red, …) and stays legible.
	cRowFocusBg = lipgloss.AdaptiveColor{Dark: "#303E47", Light: "#D4E2E8"}

	// Neutral headers leave the accent for focus and active controls.
	cHeaderTint = cHeaderBg
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
	// treeCursorDimStyle marks the sidebar's cursor row while the list (not the
	// tree) has focus, so its position stays visible without competing.
	treeCursorDimStyle  = lipgloss.NewStyle().Foreground(cInactFg).Background(cInactBg)
	selectedStyle       = lipgloss.NewStyle().Foreground(cSelectFg).Background(cSelectBg)
	cursorSelectedStyle = lipgloss.NewStyle().Foreground(cSelectFg).Background(cSelectBg)
	runningStyle        = lipgloss.NewStyle().Bold(true).Foreground(cGreen)
	queuedStyle         = lipgloss.NewStyle().Foreground(cAmber)
	finishedStyle       = lipgloss.NewStyle().Foreground(cFgFaint)
	finishedErrStyle    = lipgloss.NewStyle().Bold(true).Foreground(cRed)
	skippedStyle        = lipgloss.NewStyle().Foreground(cFgMuted)
	jobIDStyle          = lipgloss.NewStyle().Foreground(cFgMuted)
	jobNameStyle        = lipgloss.NewStyle().Foreground(cFgBright)
	statusBarStyle      = lipgloss.NewStyle().Background(cBarBg).Foreground(cFgDim)
	inputStyle          = lipgloss.NewStyle().Foreground(cBlue)
	helpStyle           = lipgloss.NewStyle().Foreground(cFgFaint)
	borderStyle         = lipgloss.NewStyle().Foreground(cBorder)
	focusBorderStyle    = lipgloss.NewStyle().Foreground(cAccent)
	airlineMode         = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent)
	modeNormalStyle     = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent) // cyan
	modeInsertStyle     = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cGreen)  // green
	modeCommandStyle    = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAmber)  // amber
	branchStyle         = lipgloss.NewStyle().Foreground(cFgMuted).Background(cHeaderBg)
	airlineFocus        = lipgloss.NewStyle().Foreground(cFgBright).Background(cSubtleBg)
	airlineInfo         = lipgloss.NewStyle().Foreground(cFg).Background(cHeaderBg)
	airlineMuted        = lipgloss.NewStyle().Foreground(cFgMuted).Background(cBarBg)
	airlineError        = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cRed)
	headerActive        = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent)
	headerInactive      = lipgloss.NewStyle().Bold(true).Foreground(cFgDim).Background(cBarBg)
	// jobsHeaderStyle block-highlights the table header row: bright bold text on
	// the gentle header tint.
	jobsHeaderStyle     = lipgloss.NewStyle().Bold(true).Foreground(cFgBright).Background(cHeaderTint)
	jobsDetailKeyStyle  = lipgloss.NewStyle().Foreground(cBlue)
	jobsDetailRuleStyle = lipgloss.NewStyle().Foreground(cRule)
)
