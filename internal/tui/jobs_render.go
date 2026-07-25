package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/han/qrush/internal/format"
	"github.com/han/qrush/internal/protocol"
)

// Rendering for the management screen: the job table (columns, rows,
// header), the surrounding chrome (borders, detail pane, footer, HW bar),
// and the shared modal-box helpers.

type columnWidths struct {
	id, group, session, state, tm, timeout, name, command int
}

func computeJobColumns(inner int) columnWidths {
	c := columnWidths{id: 5, group: 10, session: 12, state: 11, tm: 8, timeout: 8, name: 14}
	const sep = 9 // three spaces after ID, single space between the other columns
	fixedNoCmd := func() int {
		return c.id + c.group + c.session + c.state + c.tm + c.timeout + c.name + sep
	}
	c.command = inner - fixedNoCmd()
	if c.command < 10 {
		// Reclaim room from the widest text columns first.
		for _, p := range []*int{&c.name, &c.session, &c.group} {
			if c.command >= 10 {
				break
			}
			need := 10 - c.command
			room := *p - 6
			if room > need {
				room = need
			}
			if room > 0 {
				*p -= room
				c.command = inner - fixedNoCmd()
			}
		}
	}
	if c.command < 1 {
		c.command = 1
	}
	return c
}

func formatJobRow(j protocol.JobInfo, group string, c columnWidths) string {
	return renderJobsRow(j, group, c, nil)
}

// renderJobsRow renders one table row. When bg is non-nil the row is drawn as a
// highlight: every cell — and the padding within it — carries that background,
// while each cell keeps its own semantic foreground. This lets the focused row
// stay tinted without flattening the state colors (running=green, failed=red).
// displaySession hides the implicit "default" session name in the flat table —
// a job with no explicit session reads as an empty SESSION cell, not "default".
func displaySession(name string) string {
	if name == "default" {
		return ""
	}
	return name
}

func renderJobsRow(j protocol.JobInfo, group string, c columnWidths, bg lipgloss.TerminalColor) string {
	timeStr := ""
	if j.State == protocol.StateRunning || j.State == protocol.StateFinished {
		timeStr = format.Duration(format.ElapsedMS(j))
	}
	// The label lives in its own NAME column; unnamed jobs echo a dim command
	// snippet there so the column still identifies the row at a glance.
	nameTxt, nameStyle := j.Label, jobNameStyle
	if nameTxt == "" {
		nameTxt, nameStyle = j.Command, treeEmptyStyle
	}
	stateTxt, stateStyle := jobStateText(j)
	cell := func(text string, w int, style lipgloss.Style) string {
		if bg != nil {
			style = style.Background(bg)
		}
		return style.Render(fitToWidth(stripAnsi(text), w))
	}
	sep := " "
	if bg != nil {
		sep = lipgloss.NewStyle().Background(bg).Render(" ")
	}
	id := cell(fmt.Sprintf("%*d", c.id, j.ID), c.id, jobIDStyle)
	grp := cell(group, c.group, groupStyle)
	sess := cell(displaySession(j.Session), c.session, sessionStyle)
	state := cell(stateTxt, c.state, stateStyle)
	tm := cell(timeStr, c.tm, lipgloss.NewStyle())
	timeoutTxt, timeoutStyle := "-", treeEmptyStyle
	if j.TimeoutMS > 0 {
		timeoutTxt, timeoutStyle = durationCompact(time.Duration(j.TimeoutMS)*time.Millisecond), queuedStyle
	}
	timeout := cell(timeoutTxt, c.timeout, timeoutStyle)
	name := cell(nameTxt, c.name, nameStyle)
	command := cell(j.Command, c.command, lipgloss.NewStyle())
	return id + sep + sep + sep + grp + sep + sess + sep + state + sep + tm + sep + timeout + sep + name + sep + command
}

// durationCompact renders a duration without trailing zero units (30m0s → 30m).
func durationCompact(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// padRowBg extends a highlighted row's background to the full inner width. For
// unhighlighted rows (bg == nil) it is a no-op — jobsBordered pads those.
func padRowBg(s string, width int, bg lipgloss.TerminalColor) string {
	if bg == nil {
		return s
	}
	if w := lipgloss.Width(s); w < width {
		return s + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", width-w))
	}
	return s
}

// clockOrEmpty renders a timestamp as HH:MM:SS, or blank if unset.
func clockOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04:05")
}

func (m model) jobsHeaderRow(c columnWidths) string {
	arrow := "▲"
	if m.jobs.sortRev {
		arrow = "▼"
	}
	// mark tags the header of the currently-sorted column with the arrow.
	mark := func(title string, active bool) string {
		if active {
			return title + " " + arrow
		}
		return title
	}
	row := fmt.Sprintf("%*s", c.id, mark("ID", m.jobs.sortMode == sortByID)) + "   " +
		fitToWidth(mark("GROUP", m.jobs.sortMode == sortGrouped), c.group) + " " +
		fitToWidth("SESSION", c.session) + " " +
		fitToWidth(mark("STATE", m.jobs.sortMode == sortByState), c.state) + " " +
		fitToWidth(mark("TIME", m.jobs.sortMode == sortByTime), c.tm) + " " +
		fitToWidth("TIMEOUT", c.timeout) + " " +
		fitToWidth("NAME", c.name) + " " +
		fitToWidth("COMMAND", c.command)
	return treeSummaryStyle.Render(row)
}

// --- rendering ------------------------------------------------------------

func (m model) renderJobsView(w, h int) string {
	if m.jobs.mode == jobsPager {
		return m.renderPager(w, h)
	}
	if w < 8 || h < 4 {
		return strings.Repeat("\n", max(0, h-1))
	}
	inner := w - 2
	cols := computeJobColumns(inner)
	bodyH := m.jobsBodyHeight()
	lines := make([]string, 0, h)

	lines = append(lines, boxedTop(w, "", focusBorderStyle))
	lines = append(lines, m.jobsBordered(jobsHeaderStyle.Render(fitToWidth(m.jobsHeaderRow(cols), inner)), inner))

	// Overlays (help / edit form / session picker) take the whole box, so the
	// detail pane is replaced by extra room for a taller centered box.
	overlay := m.jobs.helping || m.jobs.form.active || m.jobs.settings.active || m.jobs.picker.active
	regionH := bodyH
	if overlay {
		regionH = bodyH + jobsDetailHeight
	}
	body := m.mgmtBodyLines(regionH, inner, cols)
	for _, bl := range body {
		lines = append(lines, m.jobsBordered(bl, inner))
	}

	if !overlay {
		for _, dl := range m.jobsDetailLines(jobsDetailHeight, inner) {
			lines = append(lines, m.jobsBordered(dl, inner))
		}
	}

	lines = append(lines, boxedBottom(w, focusBorderStyle))
	if m.jobs.filtering {
		lines = append(lines, fitToWidth(inputStyle.Render("/")+m.textInput.View(), w))
	}
	if m.jobs.commanding {
		lines = append(lines, fitToWidth(inputStyle.Render(m.cmdInput.View()), w))
	}
	lines = append(lines, m.jobsFooter(w))
	lines = append(lines, m.renderHWBar(w))

	return joinExact(lines, w, h)
}

// mgmtBodyLines renders the visible window of the group→session→job list (or the
// edit-form overlay when active), returning exactly bodyH lines of width inner.
func (m model) mgmtBodyLines(bodyH, inner int, cols columnWidths) []string {
	if m.jobs.form.active {
		return m.sessionFormLines(bodyH, inner)
	}
	if m.jobs.settings.active {
		return m.settingsFormLines(bodyH, inner)
	}
	if m.jobs.picker.active {
		return m.sessionPickerLines(bodyH, inner)
	}
	if m.jobs.helping {
		return m.helpLines(bodyH, inner)
	}
	out := make([]string, 0, bodyH)
	if len(m.jobs.rows) == 0 {
		for i := 0; i < bodyH; i++ {
			if i == bodyH/2 {
				out = append(out, treeEmptyStyle.Render(centerText("(no jobs)", inner)))
			} else {
				out = append(out, "")
			}
		}
		return out
	}
	selLo, selHi := -1, -1
	if m.jobs.visual {
		selLo, selHi = m.visualRange()
	}
	for i := 0; i < bodyH; i++ {
		idx := m.jobs.offset + i
		if idx >= len(m.jobs.rows) {
			out = append(out, "")
			continue
		}
		r := m.jobs.rows[idx]
		isCursor := idx == m.jobs.cursor
		inSel := r.kind == rowJob &&
			((m.jobs.visual && idx >= selLo && idx <= selHi) || m.jobs.tagged[r.job.ID])
		out = append(out, m.renderMgmtRow(r, cols, inner, isCursor, inSel))
	}
	return out
}

// renderMgmtRow renders one management-list row with the right highlight. Job
// rows keep their per-column semantic colors under the focus tint; group and
// session headers are free-form lines.
func (m model) renderMgmtRow(r mgmtRow, cols columnWidths, inner int, isCursor, inSel bool) string {
	if r.kind == rowSession {
		if isCursor {
			return padRowBg(renderSessionRow(r.group, r.session, cols, cRowFocusBg), inner, cRowFocusBg)
		}
		return renderSessionRow(r.group, r.session, cols, nil)
	}
	switch {
	case isCursor:
		return padRowBg(renderJobsRow(r.job, r.group, cols, cRowFocusBg), inner, cRowFocusBg)
	case inSel:
		return selectedStyle.Render(fitToWidth(stripAnsi(formatJobRow(r.job, r.group, cols)), inner))
	default:
		return formatJobRow(r.job, r.group, cols)
	}
}

// renderSessionRow renders a placeholder line for a session with no jobs, so it
// stays visible in the table. The columns align with job rows.
func renderSessionRow(group, session string, c columnWidths, bg lipgloss.TerminalColor) string {
	cell := func(text string, w int, style lipgloss.Style) string {
		if bg != nil {
			style = style.Background(bg)
		}
		return style.Render(fitToWidth(stripAnsi(text), w))
	}
	sep := " "
	if bg != nil {
		sep = lipgloss.NewStyle().Background(bg).Render(" ")
	}
	id := cell("·", c.id, treeEmptyStyle)
	grp := cell(group, c.group, groupStyle)
	sess := cell(displaySession(session), c.session, sessionStyle)
	state := cell("no jobs", c.state, treeEmptyStyle)
	tm := cell("", c.tm, lipgloss.NewStyle())
	timeout := cell("", c.timeout, lipgloss.NewStyle())
	name := cell("", c.name, lipgloss.NewStyle())
	command := cell("— empty session · ⏎ to open", c.command, treeEmptyStyle)
	return id + sep + sep + sep + grp + sep + sess + sep + state + sep + tm + sep + timeout + sep + name + sep + command
}

// modalInnerWidth picks a modal's inner width: a comfortable fixed size that
// shrinks to fit narrow terminals.
func modalInnerWidth(bodyInner, prefer int) int {
	w := prefer
	if max := bodyInner - 4; w > max {
		w = max
	}
	if w < 24 {
		w = 24
	}
	return w
}

// modalBox wraps content lines in a rounded box with an accent title embedded in
// the top edge (╭─ Title ─╮), padding each line to innerW. Styled after bada's
// modalFrame/panelTop.
func modalBox(title string, content []string, innerW int) []string {
	titleR := modalTitleStyle.Render(title)
	dashes := innerW - 3 - lipgloss.Width(title)
	if dashes < 0 {
		dashes = 0
	}
	top := borderStyle.Render("╭─ ") + titleR + borderStyle.Render(" "+strings.Repeat("─", dashes)+"╮")
	side := borderStyle.Render("│")
	lines := []string{top}
	for _, c := range content {
		if w := lipgloss.Width(c); w < innerW {
			c += strings.Repeat(" ", innerW-w)
		} else if w > innerW {
			c = truncateToWidth(c, innerW)
		}
		lines = append(lines, side+c+side)
	}
	lines = append(lines, borderStyle.Render("╰"+strings.Repeat("─", innerW)+"╯"))
	return lines
}

// centerBox places a pre-rendered box centered within a bodyH×inner region.
func centerBox(box []string, bodyH, inner int) []string {
	boxW := 0
	for _, l := range box {
		if w := lipgloss.Width(l); w > boxW {
			boxW = w
		}
	}
	pad := strings.Repeat(" ", max(0, (inner-boxW)/2))
	top := max(0, (bodyH-len(box))/2)
	out := make([]string, 0, bodyH)
	for i := 0; i < bodyH; i++ {
		if ci := i - top; ci >= 0 && ci < len(box) {
			out = append(out, pad+box[ci])
		} else {
			out = append(out, "")
		}
	}
	return out
}

// helpLines renders the key-binding / command help overlay.
func (m model) helpLines(bodyH, inner int) []string {
	boxInner := modalInnerWidth(inner, 52)
	row := func(keys, desc string) string {
		return "  " + modalActiveStyle.Render(fmt.Sprintf("%-15s", keys)) + treeSummaryStyle.Render(desc)
	}
	content := []string{
		"",
		jobsDetailKeyStyle.Render("  Navigation"),
		row("j / k", "move down / up"),
		row("gg / G", "jump to top / bottom"),
		row("^d / ^u", "half-page down / up"),
		"",
		jobsDetailKeyStyle.Render("  Sessions & jobs"),
		row("⏎", "job output (session if empty)"),
		row("o", "open the output pager"),
		row("S", "session picker: open/new/edit, d deletes"),
		row("e", "edit row: name/session/group"),
		row("n / a", "new session"),
		row("x / u / r", "kill / urgent / rerun job"),
		row("d", "remove job(s)"),
		"",
		jobsDetailKeyStyle.Render("  View & config"),
		row("sg/si/ss/st", "sort by field · repeat reverses"),
		row("R", "reverse the current sort"),
		row("/", "filter"),
		row("space", "select job + move down"),
		row("V", "visual multi-select"),
		row(":", "command line"),
		row(":set slots N", "set parallel jobs"),
		row(":set logdir P", "set log directory"),
		row(":config", "settings box (slots, logdir)"),
		row(":kill id|-a", "kill job(s) / all running"),
		row(":restart id|-a", "kill + re-enqueue job(s)"),
		row(":reset", "factory-reset qrush (confirms)"),
		row("q", "quit"),
		row("esc", "back to the open session"),
		"",
		helpStyle.Render("  press any key to close"),
	}
	return centerBox(modalBox("Help", content, boxInner), bodyH, inner)
}

// renderHWBar is the system-wide hardware status bar shown at the bottom of the
// job view: CPU, memory, load average, and core count.
func (m model) renderHWBar(w int) string {
	s := m.hwStats
	left := []statusSegment{{text: " HW ", style: modeCommandStyle}}
	if !s.CPUOK && !s.MemOK && !s.LoadOK {
		left = append(left, statusSegment{text: " gathering… (or unavailable on this platform) ", style: airlineMuted})
		return renderAirline(w, left, nil)
	}
	if s.CPUOK {
		style := airlineInfo
		if s.CPUPercent >= 90 {
			style = airlineError
		}
		left = append(left, statusSegment{
			text:  fmt.Sprintf(" CPU %3.0f%% %s ", s.CPUPercent, gauge(s.CPUPercent, 8)),
			style: style,
		})
	}
	if s.MemOK {
		style := airlineInfo
		if s.MemTotal > 0 && float64(s.MemUsed)/float64(s.MemTotal) >= 0.9 {
			style = airlineError
		}
		memText := fmt.Sprintf(" MEM %s ", humanBytes(s.MemTotal))
		if s.MemUsed > 0 && s.MemTotal > 0 {
			// Same shape as the CPU segment: value + gauge, so the two chips
			// read as one system.
			pct := float64(s.MemUsed) / float64(s.MemTotal) * 100
			memText = fmt.Sprintf(" MEM %s/%s %s ", humanBytes(s.MemUsed), humanBytes(s.MemTotal), gauge(pct, 8))
		}
		left = append(left, statusSegment{text: memText, style: style})
	}

	// One quiet segment on the right instead of two competing chips.
	var parts []string
	if s.LoadOK {
		parts = append(parts, fmt.Sprintf("load %.2f %.2f %.2f", s.Load[0], s.Load[1], s.Load[2]))
	}
	if s.NumCPU > 0 {
		parts = append(parts, fmt.Sprintf("%d cpu", s.NumCPU))
	}
	var right []statusSegment
	if len(parts) > 0 {
		right = append(right, statusSegment{text: " " + strings.Join(parts, " · ") + " ", style: airlineMuted})
	}
	return renderAirline(w, left, right)
}

// gauge renders a fixed-width █/░ bar for a 0..100 percentage.
func gauge(pct float64, width int) string {
	if width < 1 {
		return ""
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// humanBytes formats a byte count with a compact binary suffix (e.g. 28.5G).
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"K", "M", "G", "T", "P"}
	if exp >= len(suffix) {
		exp = len(suffix) - 1
	}
	return fmt.Sprintf("%.1f%s", float64(b)/float64(div), suffix[exp])
}

func (m model) jobsBordered(content string, inner int) string {
	b := focusBorderStyle.Render("│")
	return b + fitToWidth(content, inner) + b
}

func jobStateCounts(jobs []protocol.JobInfo) (running, queued, finished, failed int) {
	for _, j := range jobs {
		switch j.State {
		case protocol.StateRunning:
			running++
		case protocol.StateQueued, protocol.StateAllocating:
			queued++
		case protocol.StateFinished:
			if j.Result.DiedBySignal || j.Result.ExitCode != 0 {
				failed++
			} else {
				finished++
			}
		default:
			finished++
		}
	}
	return
}

func (m model) jobsDetailLines(maxLines, inner int) []string {
	out := make([]string, 0, maxLines)
	rule := func(title string) {
		r := title + strings.Repeat("─", max(0, inner-lipgloss.Width(title)))
		out = append(out, jobsDetailRuleStyle.Render(fitToWidth(r, inner)))
	}
	if j, ok := m.jobsSelected(); ok {
		rule(" job details ")
		info := strings.Split(strings.TrimRight(format.FormatJobInfo(&j), "\n"), "\n")
		for _, il := range info {
			if len(out) >= maxLines {
				break
			}
			out = append(out, styleJobDetailLine(il, inner))
		}
	} else if r, ok := m.jobsCursorRow(); ok && r.kind == rowSession {
		rule(" session ")
		out = append(out, styleJobDetailLine("name: "+r.session, inner))
		out = append(out, styleJobDetailLine("group: "+r.group, inner))
		out = append(out, styleJobDetailLine("jobs: 0 (empty)", inner))
		out = append(out, styleJobDetailLine("open: enter · edit: e · delete: S then d", inner))
	}
	for len(out) < maxLines {
		out = append(out, "")
	}
	return out[:maxLines]
}

func styleJobDetailLine(line string, inner int) string {
	if k, v, ok := strings.Cut(line, ":"); ok {
		key := jobsDetailKeyStyle.Render(k + ":")
		value := strings.TrimLeft(v, " ")
		return fitToWidth(key+" "+truncateToWidth(value, max(0, inner-lipgloss.Width(stripAnsi(key))-1)), inner)
	}
	return truncateToWidth(line, inner)
}

func (m model) jobsFooter(w int) string {
	if m.jobs.confirm.kind != confirmNone {
		var q string
		switch m.jobs.confirm.kind {
		case confirmRemove:
			if n := len(m.jobs.confirm.ids); n == 1 {
				q = fmt.Sprintf(" remove job %d? (y/n) ", m.jobs.confirm.ids[0])
			} else {
				q = fmt.Sprintf(" remove %d jobs? (y/n) ", n)
			}
		case confirmDeleteSession:
			q = fmt.Sprintf(" delete session %q? (y/n) ", m.jobs.confirm.session)
		case confirmDeleteGroup:
			q = fmt.Sprintf(" delete group %q? (y/n) ", m.jobs.confirm.group)
		case confirmReset:
			q = " reset qrush? kills all jobs & panes, deletes sessions, restores default settings (y/n) "
		default:
			q = " clear all finished jobs? (y/n) "
		}
		return renderAirline(w, []statusSegment{
			{text: " CONFIRM ", style: airlineError},
			{text: q, style: airlineInfo},
		}, nil)
	}
	running, queued, finished, failed := jobStateCounts(m.visibleJobs())
	sessions := 0
	for _, n := range m.nodes {
		sessions += len(n.sessions)
	}
	modeText, modeStyle := " MANAGE ", modeCommandStyle
	if m.jobs.visual {
		lo, hi := m.visualRange()
		modeText, modeStyle = fmt.Sprintf(" VISUAL %d ", hi-lo+1), modeInsertStyle
	}
	left := []statusSegment{
		{text: modeText, style: modeStyle},
		{text: fmt.Sprintf(" sort:%s%s ", m.jobs.sortMode.label(), sortArrow(m.jobs.sortRev)), style: airlineFocus},
	}
	if n := len(m.jobs.tagged); n > 0 {
		left = append(left, statusSegment{text: fmt.Sprintf(" sel %d ", n), style: modeInsertStyle})
	}
	if m.jobs.filter != "" {
		left = append(left, statusSegment{text: fmt.Sprintf(" /%s ", m.jobs.filter), style: airlineInfo})
	}
	if m.status != "" {
		left = append(left, statusSegment{text: " " + shorten(m.status, 60) + " ", style: airlineInfo})
	}
	// Right side stays calm: one compact stats segment (fail only when it
	// exists, as its own red chip) and a single pointer to the help overlay
	// instead of a cheat-sheet crammed into the bar.
	var right []statusSegment
	if failed > 0 {
		right = append(right, statusSegment{text: fmt.Sprintf(" fail %d ", failed), style: airlineError})
	}
	right = append(right,
		statusSegment{text: fmt.Sprintf(" run %d · queue %d · done %d · sess %d ", running, queued, finished, sessions), style: airlineMuted},
		statusSegment{text: " ? help ", style: airlineFocus},
	)
	return renderAirline(w, left, right)
}

func sortArrow(rev bool) string {
	if rev {
		return "▼"
	}
	return "▲"
}

// joinExact pads/truncates to exactly h lines of width w and joins them.
func joinExact(lines []string, w, h int) string {
	for len(lines) < h {
		lines = append(lines, fitToWidth("", w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func centerText(s string, w int) string {
	n := lipgloss.Width(s)
	if n >= w {
		return s
	}
	return strings.Repeat(" ", (w-n)/2) + s
}

func shorten(s string, n int) string {
	if lipgloss.Width(s) <= n || n <= 1 {
		return s
	}
	return truncateToWidth(s, n-1) + "…"
}
