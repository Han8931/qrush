package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/han/qrush/internal/format"
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

func totalRows(nodes []treeNode) int {
	n := len(nodes)
	for _, node := range nodes {
		if node.expanded {
			n += len(node.sessions)
		}
	}
	return n
}

type rowInfo struct {
	isGroup    bool
	isSession  bool
	nodeIdx    int
	sessionIdx int
}

func (r rowInfo) key(nodes []treeNode) string {
	if r.nodeIdx < 0 || r.nodeIdx >= len(nodes) {
		return ""
	}
	if r.isGroup {
		return groupSelectionKey(nodes[r.nodeIdx].group)
	}
	if r.isSession && r.sessionIdx >= 0 && r.sessionIdx < len(nodes[r.nodeIdx].sessions) {
		return sessionSelectionKey(nodes[r.nodeIdx].sessions[r.sessionIdx].Name)
	}
	return ""
}

func groupSelectionKey(group string) string {
	return "g:" + group
}

func sessionSelectionKey(session string) string {
	return "s:" + session
}

func rowAt(nodes []treeNode, idx int) rowInfo {
	row := 0
	for ni, node := range nodes {
		if row == idx {
			return rowInfo{isGroup: true, nodeIdx: ni}
		}
		row++
		if node.expanded {
			for si := range node.sessions {
				if row == idx {
					return rowInfo{isSession: true, nodeIdx: ni, sessionIdx: si}
				}
				row++
			}
		}
	}
	return rowInfo{}
}

func parentGroupRow(nodes []treeNode, cursor int) int {
	row := 0
	lastGroupRow := 0
	for _, node := range nodes {
		if row >= cursor {
			return lastGroupRow
		}
		lastGroupRow = row
		row++
		if node.expanded {
			row += len(node.sessions)
		}
	}
	return lastGroupRow
}

func renderRow(nodes []treeNode, idx int, width int, isCursor bool, isSelected bool, treeFocused bool) string {
	info := rowAt(nodes, idx)
	var line string

	if info.isGroup {
		node := nodes[info.nodeIdx]
		icon := "▸"
		if node.expanded {
			icon = "▾"
		}

		summary := groupSummary(node)
		line = fmt.Sprintf(" %s  %s  %s", treeIconStyle.Render(icon), groupStyle.Render(node.group), treeSummaryStyle.Render(summary))
	} else {
		node := nodes[info.nodeIdx]
		session := node.sessions[info.sessionIdx]
		line = renderSessionRow(session, node.jobs[session.Name], isSelected)
	}

	if isCursor {
		padded := padRight(stripAnsi(line), width)
		if !treeFocused {
			return cursorInactiveStyle.Render(padded)
		}
		if isSelected {
			return cursorSelectedStyle.Render(padded)
		}
		return cursorStyle.Render(padded)
	}
	if isSelected {
		padded := padRight(stripAnsi(line), width)
		return selectedStyle.Render(padded)
	}
	return line
}

func renderSessionRow(session protocol.SessionInfo, jobs []protocol.JobInfo, selected bool) string {
	running, queued, finished := jobCounts(jobs)
	state := "idle"
	stateStyle := treeEmptyStyle
	switch {
	case running > 0:
		state = fmt.Sprintf("%d running", running)
		stateStyle = runningStyle
	case queued > 0:
		state = fmt.Sprintf("%d queued", queued)
		stateStyle = queuedStyle
	case finished > 0:
		state = fmt.Sprintf("%d done", finished)
		stateStyle = finishedStyle
	}

	parts := []string{stateStyle.Render(state)}
	if running > 0 && queued > 0 {
		parts = append(parts, queuedStyle.Render(fmt.Sprintf("%d queued", queued)))
	}
	if finished > 0 && (running > 0 || queued > 0) {
		parts = append(parts, treeSummaryStyle.Render(fmt.Sprintf("%d done", finished)))
	}

	return fmt.Sprintf("     %s  %s", sessionStyle.Render(session.Name), strings.Join(parts, treeSummaryStyle.Render("  ")))
}

func renderJobRow(j protocol.JobInfo) string {
	id := jobIDStyle.Render(fmt.Sprintf("%4d", j.ID))
	state := styledState(j)
	timeStr := ""
	if j.State == protocol.StateRunning || j.State == protocol.StateFinished {
		timeStr = format.Duration(j.Result.RealTimeMS)
	}

	label := j.Command
	if j.Label != "" {
		label = fmt.Sprintf("[%s] %s", j.Label, j.Command)
	}

	if timeStr != "" {
		return fmt.Sprintf("    %s  %-12s %-7s %s", id, state, timeStr, label)
	}
	return fmt.Sprintf("    %s  %-12s %s", id, state, label)
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

func styledState(j protocol.JobInfo) string {
	switch j.State {
	case protocol.StateRunning:
		return runningStyle.Render("running")
	case protocol.StateQueued:
		return queuedStyle.Render("queued")
	case protocol.StateFinished:
		if j.Result.DiedBySignal {
			return finishedErrStyle.Render(fmt.Sprintf("signal %d", j.Result.Signal))
		}
		if j.Result.ExitCode != 0 {
			return finishedErrStyle.Render(fmt.Sprintf("exit %d", j.Result.ExitCode))
		}
		return finishedStyle.Render("finished")
	case protocol.StateSkipped:
		return skippedStyle.Render("skipped")
	default:
		return "unknown"
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

func renderTreeLines(nodes []treeNode, cursor int, height int, width int, selected map[string]bool, treeFocused bool) []string {
	total := totalRows(nodes)
	lines := make([]string, height)

	scrollOff := 0
	if cursor >= height {
		scrollOff = cursor - height + 1
	}

	for i := 0; i < height; i++ {
		rowIdx := scrollOff + i
		if rowIdx < total {
			info := rowAt(nodes, rowIdx)
			lines[i] = renderRow(nodes, rowIdx, width, rowIdx == cursor, selected[info.key(nodes)], treeFocused)
		}
	}
	return lines
}

func padRight(s string, width int) string {
	n := lipgloss.Width(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func padRendered(s string, width int) string {
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
