package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Tab no longer changes focus in the tree, so it falls through to keyNone
// (and in a terminal pane it is forwarded to the shell for completion).
func TestTabIsNotAFocusAction(t *testing.T) {
	got := mapTreeKey(tea.KeyMsg{Type: tea.KeyTab})
	if got != keyNone {
		t.Errorf("tab should map to keyNone, got %v", got)
	}
}
