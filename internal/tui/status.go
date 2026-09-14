package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The split view's airline-style footer: mode indicator, session, git branch,
// and job/slot counts.

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
