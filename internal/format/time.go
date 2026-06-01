package format

import "fmt"

func Duration(ms int64) string {
	if ms < 0 {
		return ""
	}
	secs := ms / 1000
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	secs = secs % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := mins / 60
	mins = mins % 60
	if hours < 24 {
		return fmt.Sprintf("%dh%dm%ds", hours, mins, secs)
	}
	days := hours / 24
	hours = hours % 24
	return fmt.Sprintf("%dd%dh%dm%ds", days, hours, mins, secs)
}
