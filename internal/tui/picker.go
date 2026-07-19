package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// The session picker (`S`): a grouped list of every session.

// sessionPicker is the overlay (opened with `s`) that lists every session —
// including empty ones with no jobs — so any session can be opened, edited, or
// created. It is the only place empty sessions are reachable in the flat table.
type sessionPicker struct {
	active bool
	rows   []pickerRow
	cursor int
}

// pickerRow is one line in the session picker: a group header (session == "")
// or a session under it.
type pickerRow struct {
	group   string
	session string
	isGroup bool
	open    bool // the currently-active session
	jobs    int
}

// --- session picker (`s`) -------------------------------------------------

// openSessionPicker builds and shows the session picker overlay, placing the
// cursor on the first session row.
func (m model) openSessionPicker() model {
	m.jobs.picker = sessionPicker{active: true, rows: m.buildPickerRows()}
	m.jobs.picker.cursor = m.jobs.picker.firstSession()
	return m
}

// buildPickerRows lists every group and its sessions — including sessions with
// no jobs, which never appear in the flat job table.
func (m model) buildPickerRows() []pickerRow {
	var rows []pickerRow
	for _, node := range m.nodes {
		rows = append(rows, pickerRow{group: node.group, isGroup: true})
		for _, s := range node.sessions {
			rows = append(rows, pickerRow{
				group:   node.group,
				session: s.Name,
				open:    s.Name == m.activeSession && m.activeSession != "",
				jobs:    len(node.jobs[s.Name]),
			})
		}
	}
	return rows
}

func (p sessionPicker) current() *pickerRow {
	if p.cursor >= 0 && p.cursor < len(p.rows) {
		return &p.rows[p.cursor]
	}
	return nil
}

// firstSession returns the index of the first non-group row (or 0).
func (p sessionPicker) firstSession() int {
	for i, r := range p.rows {
		if !r.isGroup {
			return i
		}
	}
	return 0
}

// step moves the cursor to the next/previous session row, skipping group
// headers; it stays put when there is none in that direction.
func (p sessionPicker) step(from, dir int) int {
	for i := from + dir; i >= 0 && i < len(p.rows); i += dir {
		if !p.rows[i].isGroup {
			return i
		}
	}
	return from
}

func (m model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.jobs.picker
	switch msg.String() {
	case "esc", "S", "q":
		m.jobs.picker = sessionPicker{}
		return m, nil
	case "j", "down":
		p.cursor = p.step(p.cursor, 1)
	case "k", "up":
		p.cursor = p.step(p.cursor, -1)
	case "enter":
		if r := p.current(); r != nil && !r.isGroup {
			m.jobs.picker = sessionPicker{}
			return m.openSession(r.session)
		}
	case "e":
		if r := p.current(); r != nil {
			m.jobs.picker = sessionPicker{}
			if r.isGroup {
				return m.openGroupRenameForm(r.group), textinput.Blink
			}
			return m.openSessionForm(false, r.session, r.group), textinput.Blink
		}
	case "n", "a":
		m.jobs.picker = sessionPicker{}
		return m.openSessionForm(true, "", ""), textinput.Blink
	case "d":
		if r := p.current(); r != nil {
			m.jobs.picker = sessionPicker{}
			if r.isGroup {
				m.jobs.confirm = confirmState{kind: confirmDeleteGroup, group: r.group}
			} else {
				m.jobs.confirm = confirmState{kind: confirmDeleteSession, session: r.session}
			}
			return m, nil
		}
	}
	return m, nil
}

// sessionPickerLines renders the session picker overlay centered in the body.
func (m model) sessionPickerLines(bodyH, inner int) []string {
	p := m.jobs.picker
	boxInner := modalInnerWidth(inner, 42)
	content := []string{""}
	for i, r := range p.rows {
		if r.isGroup {
			content = append(content, "  "+folderStyle.Render("")+" "+groupStyle.Render(r.group))
			continue
		}
		marker := "    "
		name := sessionStyle.Render(fmt.Sprintf("%-16s", r.session))
		if i == p.cursor {
			marker = "  " + modalTitleStyle.Render("▌ ")
			name = modalActiveStyle.Render(fmt.Sprintf("%-16s", r.session))
		}
		badge := treeSummaryStyle.Render(fmt.Sprintf("  %d jobs", r.jobs))
		if r.open {
			badge += runningStyle.Render("  ● open")
		}
		content = append(content, marker+name+badge)
	}
	content = append(content, "")
	content = append(content, helpStyle.Render("  ⏎:open · e:edit · n:new · d:delete · esc:close"))
	return centerBox(modalBox("Sessions", content, boxInner), bodyH, inner)
}
