package tui

import tea "github.com/charmbracelet/bubbletea"

type keyAction int

const (
	keyNone keyAction = iota
	keyUp
	keyDown
	keyTop
	keyBottom
	keyToggle
	keyCollapse
	keyExpandTree
	keySelect
	keySelectAll
	keyCreateSession
	keyCreateGroup
	keyDeleteSession
	keyRenameSession
	keyMoveSession
	keyCatOutput
	keyShowInfo
	keyKillJob
	keyRemoveJob
	keyMakeUrgent
	keyQuit
	keyConfirm
	keyCancel
)

func mapTreeKey(msg tea.KeyMsg) keyAction {
	switch msg.String() {
	case "q":
		return keyQuit
	case "k", "up":
		return keyUp
	case "j", "down":
		return keyDown
	case "G":
		return keyBottom
	case "enter", "l", "right":
		return keyToggle
	case "h", "left":
		return keyCollapse
	case " ":
		return keySelect
	case "v":
		return keySelectAll
	case "A":
		return keyExpandTree
	case "a":
		return keyCreateSession
	case "M":
		return keyCreateGroup
	case "d":
		return keyDeleteSession
	case "R":
		return keyRenameSession
	case "m":
		return keyMoveSession
	case "c":
		return keyCatOutput
	case "i":
		return keyShowInfo
	case "x":
		return keyKillJob
	case "r":
		return keyRemoveJob
	case "u":
		return keyMakeUrgent
	case "esc":
		return keyCancel
	}
	return keyNone
}
