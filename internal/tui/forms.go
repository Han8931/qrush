package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/protocol"
)

// The edit modals: the session/job edit box (`e`, `n`), the group rename
// variant, and the `:config` settings box.

// configForm is the settings modal opened by `:config`: the daemon options
// that are editable at runtime (parallel slots, log directory).
type configForm struct {
	active      bool
	slotsInput  textinput.Model
	logdirInput textinput.Model
	origSlots   int
	origLogdir  string
	focusField  int // 0 = slots, 1 = logdir
}

// sessionForm is the edit modal opened with `e`. On a session row (or when
// creating) it edits the session's name + group; on a job row it grows a job
// name field on top, so one box edits everything about the row.
type sessionForm struct {
	active       bool
	creating     bool // blank form → SessionCreate; else rename/move
	groupRename  bool // single-field variant: rename the group in origName
	jobID        int  // >= 0: job row — show the job-name field; -1 otherwise
	origLabel    string
	origName     string
	origGroup    string
	origTimeout  string
	labelInput   textinput.Model // job name; only rendered when jobID >= 0
	nameInput    textinput.Model // session name
	groupInput   textinput.Model
	timeoutInput textinput.Model // job timeout duration; empty = none
	focusField   int             // index into inputs()
}

// fields returns the form's field labels in focus order.
func (f *sessionForm) fields() []string {
	switch {
	case f.groupRename:
		return []string{"name"}
	case f.jobID >= 0:
		return []string{"name", "session", "group", "timeout"}
	default:
		return []string{"name", "group"}
	}
}

// inputs returns pointers to the form's visible fields in focus order.
func (f *sessionForm) inputs() []*textinput.Model {
	switch {
	case f.groupRename:
		return []*textinput.Model{&f.nameInput}
	case f.jobID >= 0:
		return []*textinput.Model{&f.labelInput, &f.nameInput, &f.groupInput, &f.timeoutInput}
	default:
		return []*textinput.Model{&f.nameInput, &f.groupInput}
	}
}

// setFocus focuses field i (wrapping) and blurs the rest.
func (f *sessionForm) setFocus(i int) {
	inputs := f.inputs()
	n := len(inputs)
	f.focusField = ((i % n) + n) % n
	for idx, ti := range inputs {
		if idx == f.focusField {
			ti.Focus()
		} else {
			ti.Blur()
		}
	}
}

// groupFocused reports whether the group field has focus.
func (f *sessionForm) groupFocused() bool {
	fs := f.fields()
	return f.focusField < len(fs) && fs[f.focusField] == "group"
}

func formInput(value string) textinput.Model {
	ti := newTextInput()
	ti.CharLimit = 64
	ti.Prompt = ""
	ti.SetValue(value)
	ti.CursorEnd()
	return ti
}

// openSessionForm shows the create/edit session modal, pre-filled when editing.
func (m model) openSessionForm(creating bool, name, group string) model {
	ni := formInput(name)
	ni.Focus()
	m.jobs.form = sessionForm{
		active:     true,
		creating:   creating,
		jobID:      -1,
		origName:   name,
		origGroup:  group,
		nameInput:  ni,
		groupInput: formInput(group),
	}
	return m
}

// openGroupRenameForm shows the one-field modal that renames a group.
func (m model) openGroupRenameForm(name string) model {
	ni := formInput(name)
	ni.Focus()
	m.jobs.form = sessionForm{
		active:      true,
		groupRename: true,
		jobID:       -1,
		origName:    name,
		nameInput:   ni,
	}
	return m
}

// openJobEditForm shows the combined edit modal for a job row: the job's name
// plus its session's name and group.
func (m model) openJobEditForm(j protocol.JobInfo, group string) model {
	li := formInput(j.Label)
	li.Focus()
	timeout := ""
	if j.TimeoutMS > 0 {
		timeout = durationCompact(time.Duration(j.TimeoutMS) * time.Millisecond)
	}
	m.jobs.form = sessionForm{
		active:       true,
		jobID:        j.ID,
		origLabel:    j.Label,
		origName:     j.Session,
		origGroup:    group,
		origTimeout:  timeout,
		labelInput:   li,
		nameInput:    formInput(j.Session),
		groupInput:   formInput(group),
		timeoutInput: formInput(timeout),
	}
	return m
}

func (m model) handleSessionFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.jobs.form
	switch msg.String() {
	case "esc":
		m.jobs.form = sessionForm{}
		return m, nil
	case "tab":
		// On the group field, tab cycles through the existing groups so they
		// are discoverable; ↑/↓ and shift+tab still switch fields.
		if f.groupFocused() {
			if g, ok := nextGroup(m.groups, strings.TrimSpace(f.groupInput.Value()), 1); ok {
				f.groupInput.SetValue(g)
				f.groupInput.CursorEnd()
			}
			return m, nil
		}
		f.setFocus(f.focusField + 1)
		return m, textinput.Blink
	case "down":
		f.setFocus(f.focusField + 1)
		return m, textinput.Blink
	case "shift+tab", "up":
		f.setFocus(f.focusField - 1)
		return m, textinput.Blink
	case "enter":
		return m.submitSessionForm()
	}
	var cmd tea.Cmd
	in := f.inputs()[f.focusField]
	*in, cmd = in.Update(msg)
	return m, cmd
}

// nextGroup returns the entry dir (±1) steps after cur in groups, wrapping.
// A value not in the list (or blank) starts from the first/last group.
func nextGroup(groups []string, cur string, dir int) (string, bool) {
	if len(groups) == 0 {
		return "", false
	}
	idx := -1
	for i, g := range groups {
		if g == cur {
			idx = i
			break
		}
	}
	if idx < 0 {
		if dir < 0 {
			return groups[len(groups)-1], true
		}
		return groups[0], true
	}
	return groups[(idx+dir+len(groups))%len(groups)], true
}

func (m model) submitSessionForm() (tea.Model, tea.Cmd) {
	f := m.jobs.form
	name := strings.TrimSpace(f.nameInput.Value())
	group := strings.TrimSpace(f.groupInput.Value())
	m.jobs.form = sessionForm{}
	if name == "" {
		m.status = "session name required"
		return m, nil
	}
	if f.groupRename {
		if name == f.origName {
			return m, nil
		}
		return m, renameGroupCmd(f.origName, name)
	}
	if f.creating {
		// Open the new session as soon as the next tree refresh includes it.
		m.pendingOpen = name
		return m, createSessionInGroup(name, group)
	}

	var cmds []tea.Cmd
	if f.jobID >= 0 {
		if label := strings.TrimSpace(f.labelInput.Value()); label != f.origLabel {
			cmds = append(cmds, setJobLabelCmd(f.jobID, label))
		}
		if timeout := strings.TrimSpace(f.timeoutInput.Value()); timeout != f.origTimeout {
			var ms int64
			if timeout != "" && !strings.EqualFold(timeout, "none") {
				d, err := time.ParseDuration(timeout)
				if err != nil || d <= 0 {
					m.status = fmt.Sprintf("invalid timeout %q (e.g. 30m, 90s)", timeout)
					return m, nil
				}
				ms = d.Milliseconds()
			}
			cmds = append(cmds, setJobTimeoutCmd(f.jobID, ms))
		}
	}
	if name != f.origName || group != f.origGroup {
		cmds = append(cmds, editSession(f.origName, name, f.origGroup, group))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// --- settings form (`:config`) --------------------------------------------

// settingsMsg carries the fetched daemon settings that seed the `:config`
// modal (slots come from the model's own tree poll).
type settingsMsg struct {
	logdir string
}

// loadSettingsCmd fetches the daemon-side settings the modal edits.
func loadSettingsCmd() tea.Cmd {
	return func() tea.Msg {
		dir, err := client.GetLogdir()
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return settingsMsg{logdir: dir}
	}
}

// openSettingsForm shows the `:config` modal pre-filled with current values.
func (m model) openSettingsForm(logdir string) model {
	si := newTextInput()
	si.CharLimit = 4
	si.Prompt = ""
	si.SetValue(strconv.Itoa(m.maxSlots))
	si.CursorEnd()
	si.Focus()
	li := newTextInput()
	li.CharLimit = 256
	li.Prompt = ""
	li.SetValue(logdir)
	li.CursorEnd()
	m.jobs.settings = configForm{
		active:      true,
		slotsInput:  si,
		logdirInput: li,
		origSlots:   m.maxSlots,
		origLogdir:  logdir,
	}
	return m
}

func (f *configForm) toggleField() {
	f.focusField = 1 - f.focusField
	if f.focusField == 0 {
		f.slotsInput.Focus()
		f.logdirInput.Blur()
	} else {
		f.logdirInput.Focus()
		f.slotsInput.Blur()
	}
}

func (m model) handleSettingsFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.jobs.settings
	switch msg.String() {
	case "esc":
		m.jobs.settings = configForm{}
		return m, nil
	case "tab", "shift+tab", "up", "down":
		f.toggleField()
		return m, textinput.Blink
	case "enter":
		return m.submitSettingsForm()
	}
	var cmd tea.Cmd
	if f.focusField == 0 {
		f.slotsInput, cmd = f.slotsInput.Update(msg)
	} else {
		f.logdirInput, cmd = f.logdirInput.Update(msg)
	}
	return m, cmd
}

func (m model) submitSettingsForm() (tea.Model, tea.Cmd) {
	f := m.jobs.settings
	m.jobs.settings = configForm{}

	n, err := strconv.Atoi(strings.TrimSpace(f.slotsInput.Value()))
	if err != nil || n < 1 {
		m.status = fmt.Sprintf("invalid slot count: %q", f.slotsInput.Value())
		return m, nil
	}
	logdir := strings.TrimSpace(f.logdirInput.Value())

	var cmds []tea.Cmd
	if n != f.origSlots {
		cmds = append(cmds, setMaxSlotsCmd(n))
	}
	if logdir != "" && logdir != f.origLogdir {
		cmds = append(cmds, setLogdirCmd(logdir))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// sessionFormLines renders the edit modal centered in the body: session name +
// group, preceded by the job-name field when a job row is being edited.
func (m model) sessionFormLines(bodyH, inner int) []string {
	f := m.jobs.form
	title := "Edit session"
	switch {
	case f.creating:
		title = "New session"
	case f.groupRename:
		title = "Rename group"
	case f.jobID >= 0:
		title = fmt.Sprintf("Edit job %d", f.jobID)
	}
	boxInner := modalInnerWidth(inner, 46)
	const labelW = 7
	valueW := boxInner - 2 - labelW - 2 // marker(2) + label + gap(2)
	if valueW < 8 {
		valueW = 8
	}
	field := func(label string, ti textinput.Model, focused bool) string {
		ti.Width = valueW - 1 // textinput renders Width+1 cells (the cursor block)
		marker := "  "
		lbl := fmt.Sprintf("%-*s", labelW, label)
		if focused {
			marker = modalTitleStyle.Render("▌ ")
			lbl = modalActiveStyle.Render(lbl)
		} else {
			lbl = jobsDetailKeyStyle.Render(lbl)
		}
		return marker + lbl + "  " + ti.View()
	}

	labels := (&f).fields()
	inputs := (&f).inputs()
	content := []string{""}
	for i, lbl := range labels {
		content = append(content, field(lbl, *inputs[i], f.focusField == i))
	}
	// While the group field is focused, show the existing groups as a strip so
	// tab-cycling is a visible choice, not a blind rotation.
	if f.groupFocused() {
		choices := groupChoiceLines(m.groups, strings.TrimSpace(f.groupInput.Value()), boxInner-2)
		if len(choices) > 0 {
			content = append(content, "")
			content = append(content, choices...)
		}
	}
	content = append(content,
		"",
		helpStyle.Render("  tab: groups · ↑/↓: switch · ⏎: save · esc: cancel"),
	)
	return centerBox(modalBox(title, content, boxInner), bodyH, inner)
}

// groupChoiceLines renders the existing groups as wrapped strip lines; the
// entry matching the group field's current value is highlighted.
func groupChoiceLines(groups []string, current string, width int) []string {
	if len(groups) == 0 {
		return nil
	}
	var lines []string
	line, lineW := "  ", 2
	for _, g := range groups {
		tok, tokW := treeSummaryStyle.Render(g), lipgloss.Width(g)
		if g == current {
			tok, tokW = selectedStyle.Render(" "+g+" "), tokW+2
		}
		if lineW+tokW > width && lineW > 2 {
			lines = append(lines, line)
			line, lineW = "  ", 2
		}
		line += tok + "  "
		lineW += tokW + 2
	}
	return append(lines, line)
}

// settingsFormLines renders the `:config` settings modal centered in the body.
func (m model) settingsFormLines(bodyH, inner int) []string {
	f := m.jobs.settings
	boxInner := modalInnerWidth(inner, 46)
	const labelW = 6
	valueW := boxInner - 2 - labelW - 2
	if valueW < 8 {
		valueW = 8
	}
	field := func(label string, ti textinput.Model, focused bool) string {
		ti.Width = valueW - 1 // textinput renders Width+1 cells (the cursor block)
		marker := "  "
		lbl := fmt.Sprintf("%-*s", labelW, label)
		if focused {
			marker = modalTitleStyle.Render("▌ ")
			lbl = modalActiveStyle.Render(lbl)
		} else {
			lbl = jobsDetailKeyStyle.Render(lbl)
		}
		return marker + lbl + "  " + ti.View()
	}
	content := []string{
		"",
		field("slots", f.slotsInput, f.focusField == 0),
		field("logdir", f.logdirInput, f.focusField == 1),
		"",
		helpStyle.Render("  tab: switch · ⏎: save · esc: cancel"),
	}
	return centerBox(modalBox("Settings", content, boxInner), bodyH, inner)
}
