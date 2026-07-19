package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/han/qrush/internal/protocol"
)

type treeNode struct {
	group    string
	expanded bool
	sessions []protocol.SessionInfo
	jobs     map[string][]protocol.JobInfo
}

func buildTree(groups []string, sessions []protocol.SessionInfo, jobs []protocol.JobInfo) []treeNode {
	bySession := make(map[string][]protocol.JobInfo)
	for _, j := range jobs {
		bySession[j.Session] = append(bySession[j.Session], j)
	}

	byGroup := make(map[string][]protocol.SessionInfo)
	for _, session := range sessions {
		group := session.Group
		if group == "" {
			group = "default"
		}
		byGroup[group] = append(byGroup[group], session)
	}

	nodes := make([]treeNode, 0, len(groups))
	for _, group := range groups {
		nodes = append(nodes, treeNode{
			group:    group,
			expanded: true,
			sessions: byGroup[group],
			jobs:     bySession,
		})
	}
	return nodes
}

func groupSummary(node treeNode) string {
	totalJobs := 0
	running := 0
	queued := 0
	for _, session := range node.sessions {
		jobs := node.jobs[session.Name]
		totalJobs += len(jobs)
		r, q, _ := jobCounts(jobs)
		running += r
		queued += q
	}
	if len(node.sessions) == 0 {
		return "empty"
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("%d session%s", len(node.sessions), plural(len(node.sessions))))
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", queued))
	}
	if totalJobs > 0 && running == 0 && queued == 0 {
		parts = append(parts, fmt.Sprintf("%d jobs", totalJobs))
	}
	return strings.Join(parts, " · ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// jobStateText returns a job's state label together with the style that colors
// it. Splitting the two lets callers recompose the label onto a highlighted
// background while keeping the semantic foreground (e.g. red for a failure).
func jobStateText(j protocol.JobInfo) (string, lipgloss.Style) {
	switch j.State {
	case protocol.StateRunning:
		return "running", runningStyle
	case protocol.StateQueued:
		if j.Attempt > 0 {
			return fmt.Sprintf("retry %d/%d", j.Attempt, j.Retries), queuedStyle
		}
		return "queued", queuedStyle
	case protocol.StateFinished:
		if j.Result.TimedOut {
			return "timeout", finishedErrStyle
		}
		if j.Result.DiedBySignal {
			return fmt.Sprintf("signal %d", j.Result.Signal), finishedErrStyle
		}
		if j.Result.ExitCode != 0 {
			return fmt.Sprintf("exit %d", j.Result.ExitCode), finishedErrStyle
		}
		return "finished", finishedStyle
	case protocol.StateSkipped:
		return "skipped", skippedStyle
	default:
		return "unknown", lipgloss.NewStyle()
	}
}

func sessionSummary(jobs []protocol.JobInfo) string {
	if len(jobs) == 0 {
		return "(empty)"
	}
	running, queued, finished := 0, 0, 0
	for _, j := range jobs {
		switch j.State {
		case protocol.StateRunning:
			running++
		case protocol.StateQueued:
			queued++
		default:
			finished++
		}
	}

	var parts []string
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", queued))
	}
	if finished > 0 {
		parts = append(parts, fmt.Sprintf("%d done", finished))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func jobCounts(jobs []protocol.JobInfo) (running, queued, finished int) {
	for _, j := range jobs {
		switch j.State {
		case protocol.StateRunning:
			running++
		case protocol.StateQueued:
			queued++
		default:
			finished++
		}
	}
	return
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
