package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Display-width helpers. Widths are measured with lipgloss so ANSI escapes and
// wide glyphs (CJK, emoji) count correctly.

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

// truncateToWidth cuts s to the given display width, preserving ANSI styling.
// (The previous strip-and-rebuild approach deleted every escape code, so any
// line that overflowed its box lost all its colors — including the edit-box
// cursor block.)
func truncateToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return padRight(s, width)
	}
	return padRight(ansi.Truncate(s, width, ""), width)
}

func padRight(s string, width int) string {
	n := lipgloss.Width(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func stripAnsi(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
		} else {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}
