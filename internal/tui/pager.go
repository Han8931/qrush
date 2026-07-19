package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/format"
	"github.com/han/qrush/internal/protocol"
)

// The output pager: full-screen scroll/search over a job's output file,
// following running jobs.

type pagerState struct {
	jobID     int
	cmd       string
	info      protocol.JobInfo
	showInfo  bool
	path      string
	lines     []string
	offset    int
	follow    bool
	running   bool
	truncated bool
	noOutput  bool
	loadErr   error
	searching bool
	search    string
}

type pagerLoadedMsg struct {
	id        int
	cmd       string
	info      protocol.JobInfo
	path      string
	lines     []string
	running   bool
	truncated bool
	noOutput  bool
	err       error
}

func dropPartialFirstLine(data []byte) []byte {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[i+1:]
	}
	return data
}

func pagerSplitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	out := make([]string, len(raw))
	for i, l := range raw {
		out[i] = strings.ReplaceAll(stripAnsi(l), "\t", "    ")
	}
	return out
}

func findMatches(lines []string, q string) []int {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var out []int
	for i, l := range lines {
		if strings.Contains(strings.ToLower(l), q) {
			out = append(out, i)
		}
	}
	return out
}

func (m model) pagerBodyHeight() int {
	footer := 1
	search := 0
	if m.jobs.pager.searching {
		search = 1
	}
	info := len(m.pagerInfoLines(m.width - 2))
	body := m.height - 2 - footer - search - info // top + bottom border
	if body < 1 {
		body = 1
	}
	return body
}

// pagerInfoLines renders the job-info panel shown at the top of the pager when
// toggled on with `i`. Returns nil when the panel is hidden.
func (m model) pagerInfoLines(inner int) []string {
	if !m.jobs.pager.showInfo || inner < 1 {
		return nil
	}
	j := m.jobs.pager.info
	info := strings.Split(strings.TrimRight(format.FormatJobInfo(&j), "\n"), "\n")
	out := make([]string, 0, len(info)+1)
	out = append(out, treeSummaryStyle.Render(fitToWidth("── job info ", inner)))
	for _, il := range info {
		out = append(out, truncateToWidth(il, inner))
	}
	return out
}

func (m model) handlePagerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.jobs.pager
	if p.searching {
		switch msg.String() {
		case "enter":
			p.searching = false
			p.search = m.textInput.Value()
			m.textInput.Blur()
			(&m).pagerSearch(true)
			return m, nil
		case "esc":
			p.searching = false
			m.textInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	key := msg.String()
	bodyH := m.pagerBodyHeight()
	if m.jobs.pending == 'g' {
		m.jobs.pending = 0
		if key == "g" {
			p.follow = false
			p.offset = 0
			return m, nil
		}
	}

	switch key {
	case "q", "esc":
		m.jobs.mode = jobsTable
		m.jobs.pager = pagerState{}
		return m, nil
	case "j", "down":
		p.follow = false
		p.offset = clampScroll(p.offset+1, bodyH, len(p.lines))
	case "k", "up":
		p.follow = false
		p.offset = clampScroll(p.offset-1, bodyH, len(p.lines))
	case "ctrl+d":
		p.follow = false
		p.offset = clampScroll(p.offset+bodyH/2, bodyH, len(p.lines))
	case "ctrl+u":
		p.follow = false
		p.offset = clampScroll(p.offset-bodyH/2, bodyH, len(p.lines))
	case "g":
		return m.startPending('g')
	case "G":
		p.offset = clampScroll(len(p.lines), bodyH, len(p.lines))
		if p.running {
			p.follow = true
		}
	case "/":
		p.searching = true
		m.textInput.SetValue("")
		m.textInput.Focus()
		return m, textinput.Blink
	case "n":
		(&m).pagerSearch(true)
	case "N":
		(&m).pagerSearch(false)
	case "i":
		p.showInfo = !p.showInfo
		p.offset = clampScroll(p.offset, m.pagerBodyHeight(), len(p.lines))
	}
	return m, nil
}

func (m *model) pagerSearch(forward bool) {
	p := &m.jobs.pager
	matches := findMatches(p.lines, p.search)
	if len(matches) == 0 {
		return
	}
	target := matches[0]
	if forward {
		target = matches[0]
		for _, idx := range matches {
			if idx > p.offset {
				target = idx
				break
			}
		}
	} else {
		target = matches[len(matches)-1]
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i] < p.offset {
				target = matches[i]
				break
			}
		}
	}
	p.follow = false
	p.offset = clampScroll(target, m.pagerBodyHeight(), len(p.lines))
}

// --- output pager commands ------------------------------------------------

func openPagerCmd(j protocol.JobInfo) tea.Cmd {
	id := j.ID
	cmd := j.Command
	running := j.State == protocol.StateRunning
	return func() tea.Msg {
		path, err := client.GetOutput(id)
		if err != nil {
			return pagerLoadedMsg{id: id, cmd: cmd, info: j, running: running, err: err}
		}
		if path == "" {
			return pagerLoadedMsg{id: id, cmd: cmd, info: j, running: running, noOutput: true}
		}
		lines, truncated, rerr := readPagerFile(path)
		return pagerLoadedMsg{id: id, cmd: cmd, info: j, path: path, running: running, lines: lines, truncated: truncated, err: rerr}
	}
}

func (m model) reloadPagerCmd() tea.Cmd {
	id := m.jobs.pager.jobID
	job := m.jobs.pager.info
	for _, j := range m.jobs.allJobs {
		if j.ID == id {
			job = j
			break
		}
	}
	return openPagerCmd(job)
}

func readPagerFile(path string) ([]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	size := st.Size()
	truncated := size > pagerByteCap
	if truncated {
		_, _ = f.Seek(size-pagerByteCap, io.SeekStart)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	if truncated {
		data = dropPartialFirstLine(data)
	}
	return pagerSplitLines(data), truncated, nil
}

func (m model) applyPagerLoaded(msg pagerLoadedMsg) model {
	if m.viewMode != viewJobs {
		return m
	}
	fresh := m.jobs.mode != jobsPager
	p := &m.jobs.pager
	p.jobID = msg.id
	p.cmd = msg.cmd
	p.info = msg.info
	p.path = msg.path
	p.running = msg.running
	p.truncated = msg.truncated
	p.noOutput = msg.noOutput
	p.loadErr = msg.err
	p.lines = msg.lines
	m.jobs.mode = jobsPager
	if fresh {
		p.follow = msg.running
		p.offset = 0
	}
	if p.follow {
		p.offset = clampScroll(len(p.lines), m.pagerBodyHeight(), len(p.lines))
	} else {
		p.offset = clampScroll(p.offset, m.pagerBodyHeight(), len(p.lines))
	}
	return m
}

func (m model) renderPager(w, h int) string {
	if w < 8 || h < 4 {
		return strings.Repeat("\n", max(0, h-1))
	}
	inner := w - 2
	p := m.jobs.pager
	bodyH := m.pagerBodyHeight()
	lines := make([]string, 0, h)

	badge := ""
	switch {
	case p.noOutput:
		badge = "[no output] "
	case p.loadErr != nil:
		badge = "[error] "
	case p.truncated:
		badge = "[truncated] "
	}
	if p.running && p.follow {
		badge += "[following]"
	} else if !p.running {
		badge += "[finished]"
	}
	title := fmt.Sprintf(" output: job %d %s %s", p.jobID, shorten(p.cmd, 24), badge)
	lines = append(lines, boxedTop(w, title, focusBorderStyle))

	for _, il := range m.pagerInfoLines(inner) {
		lines = append(lines, m.jobsBordered(il, inner))
	}

	if p.noOutput || p.loadErr != nil {
		msg := "(no output)"
		if p.loadErr != nil {
			msg = "error: " + p.loadErr.Error()
		}
		for i := 0; i < bodyH; i++ {
			if i == bodyH/2 {
				lines = append(lines, m.jobsBordered(treeEmptyStyle.Render(centerText(msg, inner)), inner))
			} else {
				lines = append(lines, m.jobsBordered("", inner))
			}
		}
	} else {
		for i := 0; i < bodyH; i++ {
			idx := p.offset + i
			if idx >= len(p.lines) {
				lines = append(lines, m.jobsBordered("", inner))
				continue
			}
			lines = append(lines, m.jobsBordered(truncateToWidth(p.lines[idx], inner), inner))
		}
	}

	lines = append(lines, boxedBottom(w, focusBorderStyle))
	if p.searching {
		lines = append(lines, fitToWidth(inputStyle.Render("/")+m.textInput.View(), w))
	}
	pos := fmt.Sprintf(" %d-%d/%d ", min(p.offset+1, len(p.lines)), min(p.offset+bodyH, len(p.lines)), len(p.lines))
	lines = append(lines, renderAirline(w, []statusSegment{
		{text: " PAGER ", style: modeCommandStyle},
		{text: pos, style: airlineFocus},
	}, []statusSegment{
		{text: " j/k gg G /n i:info  q:back ", style: airlineFocus},
	}))

	return joinExact(lines, w, h)
}
