package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/han/qrush/internal/protocol"
	"github.com/han/qrush/internal/sysmon"
)

func quitsOn(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// In `ru --jobs` (jobsOnly) mode, quitting the jobs view exits the program;
// otherwise it drops back to the split view.
func TestJobsQuitJobsOnly(t *testing.T) {
	for _, key := range []string{"q", "esc"} {
		m := model{jobsOnly: true, viewMode: viewJobs}
		got, cmd := m.handleJobsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if key == "esc" {
			got, cmd = m.handleJobsKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
		if !quitsOn(t, cmd) {
			t.Errorf("%q in jobsOnly mode: expected tea.Quit", key)
		}
		if fm := got.(model); fm.viewMode != viewJobs {
			t.Errorf("%q in jobsOnly mode: viewMode changed to %d", key, fm.viewMode)
		}
	}
}

func TestJobsQuitWithOpenSession(t *testing.T) {
	// Even with a session open (e.g. after a detach), q quits the program —
	// daemon-side shells persist. esc is the key that drops back in.
	m := model{viewMode: viewJobs, activeSession: "default", layouts: map[string]*paneNode{"default": {}}}
	_, cmd := m.handleJobsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !quitsOn(t, cmd) {
		t.Fatal("q with an open session should quit the program")
	}
}

func TestJobsEscDropsToSplit(t *testing.T) {
	// With a session open, esc drops back to that session's split view.
	m := model{viewMode: viewJobs, activeSession: "default", layouts: map[string]*paneNode{"default": {}}}
	got, cmd := m.handleJobsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if quitsOn(t, cmd) {
		t.Fatal("esc with an open session should not quit the program")
	}
	if fm := got.(model); fm.viewMode != viewSplit {
		t.Errorf("esc should drop to viewSplit, got viewMode=%d", fm.viewMode)
	}
}

func TestJobsQuitNoSessionQuits(t *testing.T) {
	// On the home screen with no session open, q exits the program.
	m := model{viewMode: viewJobs}
	_, cmd := m.handleJobsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !quitsOn(t, cmd) {
		t.Fatal("q with no open session should quit the program")
	}
}

func runeKey(r string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)}
}

// `e` on a job row opens the combined edit box: job name + session + group,
// with ↑/↓ cycling all three fields.
func TestJobEditFormFields(t *testing.T) {
	j := protocol.JobInfo{ID: 7, Label: "lbl", Session: "work"}
	m := model{viewMode: viewJobs}
	m.jobs.rows = []mgmtRow{{kind: rowJob, job: j, session: "work", group: "grp"}}

	got, _ := m.handleJobsKey(runeKey("e"))
	fm := got.(model)
	f := fm.jobs.form
	if !f.active || f.jobID != 7 {
		t.Fatalf("expected active job-edit form for job 7, got %+v", f)
	}
	if n := len((&f).inputs()); n != 4 {
		t.Fatalf("expected 4 fields on a job row (name/session/group/timeout), got %d", n)
	}
	if f.labelInput.Value() != "lbl" || f.nameInput.Value() != "work" || f.groupInput.Value() != "grp" {
		t.Errorf("form not prefilled: %q %q %q", f.labelInput.Value(), f.nameInput.Value(), f.groupInput.Value())
	}
	if f.timeoutInput.Value() != "" {
		t.Errorf("timeout should default to none (empty), got %q", f.timeoutInput.Value())
	}

	got, _ = fm.handleJobsKey(tea.KeyMsg{Type: tea.KeyDown})
	fm = got.(model)
	if fm.jobs.form.focusField != 1 {
		t.Errorf("down should focus the session field, got %d", fm.jobs.form.focusField)
	}
	got, _ = fm.handleJobsKey(tea.KeyMsg{Type: tea.KeyDown})
	fm = got.(model)
	if !fm.jobs.form.groupFocused() {
		t.Errorf("second down should focus the group field, got %d", fm.jobs.form.focusField)
	}
}

// With the group field focused, the edit box lists the existing groups so
// tab-cycling is a visible choice.
func TestSessionFormListsGroupsWhenGroupFocused(t *testing.T) {
	m := model{groups: []string{"default", "work"}}
	fm := m.openSessionForm(false, "sess", "default")
	fm.jobs.form.setFocus(1) // group field

	out := strings.Join(fm.sessionFormLines(24, 60), "\n")
	if !strings.Contains(stripAnsi(out), "work") {
		t.Errorf("expected group strip to list %q, got %q", "work", stripAnsi(out))
	}

	// Not shown while the name field has focus.
	fm.jobs.form.setFocus(0)
	out = stripAnsi(strings.Join(fm.sessionFormLines(24, 60), "\n"))
	if strings.Contains(out, "work") {
		t.Errorf("group strip should be hidden when the name field is focused, got %q", out)
	}
}

// The focused field of an edit box must render a visible cursor block. Tests
// run without a TTY, where lipgloss strips all styling, so force a color
// profile for the assertion.
func TestSessionFormShowsCursor(t *testing.T) {
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	m := model{}
	fm := m.openSessionForm(true, "", "")
	out := strings.Join(fm.sessionFormLines(20, 60), "\n")
	if !strings.Contains(out, "\x1b[7m") && !strings.Contains(out, "\x1b[7;") &&
		!strings.Contains(out, ";7m") && !strings.Contains(out, "\x1b[48;") {
		t.Errorf("expected a styled cursor block in the form render, got %q", out)
	}
}

// Space tags the cursor job (ranger/lf-style), actions consume the tagged
// set, and Esc clears it.
func TestJobsSpaceTagsAndActs(t *testing.T) {
	jobs := []protocol.JobInfo{{ID: 3, Session: "default"}, {ID: 5, Session: "default"}}
	m := model{viewMode: viewJobs}
	m.jobs.rows = jobRows(jobs)
	m.jobs.allJobs = jobs

	got, _ := m.handleJobsKey(tea.KeyMsg{Type: tea.KeySpace})
	fm := got.(model)
	if !fm.jobs.tagged[3] {
		t.Fatal("expected job 3 tagged after space")
	}
	if fm.jobs.cursor != 1 {
		t.Errorf("expected cursor to advance to 1, got %d", fm.jobs.cursor)
	}

	got, _ = fm.handleJobsKey(tea.KeyMsg{Type: tea.KeySpace})
	fm = got.(model)
	if len(fm.jobs.tagged) != 2 {
		t.Fatalf("expected 2 tagged jobs, got %d", len(fm.jobs.tagged))
	}
	if ids := fm.jobsActionIDs(); len(ids) != 2 {
		t.Errorf("expected actions to target both tagged jobs, got %v", ids)
	}

	got, _ = fm.handleJobsKey(tea.KeyMsg{Type: tea.KeyEsc})
	fm = got.(model)
	if len(fm.jobs.tagged) != 0 {
		t.Errorf("esc should clear the tagged set, got %v", fm.jobs.tagged)
	}
}

// Toggling an already-tagged job with Space removes the tag.
func TestJobsSpaceToggleUntags(t *testing.T) {
	jobs := []protocol.JobInfo{{ID: 3, Session: "default"}}
	m := model{viewMode: viewJobs}
	m.jobs.rows = jobRows(jobs)
	m.jobs.allJobs = jobs

	got, _ := m.handleJobsKey(tea.KeyMsg{Type: tea.KeySpace})
	fm := got.(model)
	got, _ = fm.handleJobsKey(tea.KeyMsg{Type: tea.KeySpace})
	fm = got.(model)
	if len(fm.jobs.tagged) != 0 {
		t.Errorf("second space should untag, got %v", fm.jobs.tagged)
	}
}

// jobRows wraps plain jobs as management job-rows for tests.
func jobRows(jobs []protocol.JobInfo) []mgmtRow {
	rows := make([]mgmtRow, len(jobs))
	for i, j := range jobs {
		rows[i] = mgmtRow{kind: rowJob, job: j, session: j.Session}
	}
	return rows
}

// modelWithRows builds a management-view model positioned on the given job row.
func modelWithRows(jobs []protocol.JobInfo, cursor int) model {
	m := model{viewMode: viewJobs}
	m.jobs.rows = jobRows(jobs)
	m.jobs.cursor = cursor
	if cursor >= 0 && cursor < len(m.jobs.rows) {
		m.jobs.cursorKey = rowKey(m.jobs.rows[cursor])
	}
	return m
}

// `d` on a finished job removes it immediately, with no confirmation prompt.
func TestJobsDeleteFinishedNoConfirm(t *testing.T) {
	rows := []protocol.JobInfo{{ID: 7, State: protocol.StateFinished}}
	m := modelWithRows(rows, 0)
	got, cmd := m.handleJobsKey(runeKey("d"))
	if cmd == nil {
		t.Fatal("expected a remove command for a finished job")
	}
	if fm := got.(model); fm.jobs.confirm.kind != confirmNone {
		t.Errorf("finished job should delete without confirm, got kind=%d", fm.jobs.confirm.kind)
	}
}

// `d` on a non-finished job still asks for confirmation.
func TestJobsDeleteQueuedConfirms(t *testing.T) {
	rows := []protocol.JobInfo{{ID: 9, State: protocol.StateQueued}}
	m := modelWithRows(rows, 0)
	got, cmd := m.handleJobsKey(runeKey("d"))
	if cmd != nil {
		t.Fatal("queued job should not delete before confirmation")
	}
	fm := got.(model)
	if fm.jobs.confirm.kind != confirmRemove || len(fm.jobs.confirm.ids) != 1 || fm.jobs.confirm.ids[0] != 9 {
		t.Errorf("expected confirmRemove for job 9, got %+v", fm.jobs.confirm)
	}
}

// Space advances the cursor like `j`.
func TestJobsSpaceMovesDown(t *testing.T) {
	rows := []protocol.JobInfo{{ID: 1}, {ID: 2}, {ID: 3}}
	m := modelWithRows(rows, 0)
	got, _ := m.handleJobsKey(runeKey(" "))
	if fm := got.(model); fm.jobs.cursor != 1 {
		t.Errorf("space should move cursor to 1, got %d", fm.jobs.cursor)
	}
	if fm := got.(model); fm.jobs.visual {
		t.Error("space should not enter visual mode")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		512:          "512B",
		2048:         "2.0K",
		30601183232:  "28.5G",
		274877906944: "256.0G",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestGauge(t *testing.T) {
	if got := gauge(0, 8); got != "░░░░░░░░" {
		t.Errorf("gauge(0,8) = %q", got)
	}
	if got := gauge(100, 8); got != "████████" {
		t.Errorf("gauge(100,8) = %q", got)
	}
	if got := gauge(50, 8); got != "████░░░░" {
		t.Errorf("gauge(50,8) = %q", got)
	}
	if got := gauge(150, 4); got != "████" {
		t.Errorf("gauge clamps over 100: %q", got)
	}
}

func TestRenderHWBar(t *testing.T) {
	m := model{}
	m.hwStats = sysmon.Stats{
		CPUPercent: 42, CPUOK: true,
		MemUsed: 8 << 30, MemTotal: 16 << 30, MemOK: true,
		Load: [3]float64{1.5, 1.2, 0.9}, LoadOK: true, NumCPU: 8,
	}
	bar := stripAnsi(m.renderHWBar(120))
	for _, want := range []string{"CPU", "42%", "MEM", "load", "8 cpu"} {
		if !strings.Contains(bar, want) {
			t.Errorf("hw bar missing %q: %q", want, bar)
		}
	}

	// With nothing available, the bar says so rather than rendering blanks.
	empty := stripAnsi((model{}).renderHWBar(120))
	if !strings.Contains(empty, "unavailable") && !strings.Contains(empty, "gathering") {
		t.Errorf("empty hw bar should indicate no data: %q", empty)
	}
}

// The job-info pane below the table is always present and shows the selected
// job's details (no toggle).
func TestJobsDetailPaneAlwaysShown(t *testing.T) {
	rows := []protocol.JobInfo{{ID: 8, Command: "go vet ./...", State: protocol.StateRunning}}
	m := modelWithRows(rows, 0)
	got := strings.Join(m.jobsDetailLines(jobsDetailHeight, 60), "\n")
	if !strings.Contains(got, "go vet ./...") {
		t.Errorf("detail pane missing selected job's command: %q", got)
	}
	if n := len(m.jobsDetailLines(jobsDetailHeight, 60)); n != jobsDetailHeight {
		t.Errorf("detail pane should occupy a fixed %d lines, got %d", jobsDetailHeight, n)
	}
}

// `D` clears all finished jobs immediately, with no confirmation prompt.
func TestJobsDeleteAllFinishedNoConfirm(t *testing.T) {
	rows := []protocol.JobInfo{{ID: 1, State: protocol.StateQueued}, {ID: 2, State: protocol.StateFinished}}
	m := modelWithRows(rows, 0)
	got, cmd := m.handleJobsKey(runeKey("D"))
	if cmd == nil {
		t.Fatal("D should issue a clear-finished command")
	}
	if fm := got.(model); fm.jobs.confirm.kind != confirmNone {
		t.Errorf("D should not prompt for confirmation, got kind=%d", fm.jobs.confirm.kind)
	}
}

// `r` issues a rerun command for the current job.
func TestJobsRerunReturnsCommand(t *testing.T) {
	rows := []protocol.JobInfo{{ID: 5, State: protocol.StateFinished}}
	m := modelWithRows(rows, 0)
	_, cmd := m.handleJobsKey(runeKey("r"))
	if cmd == nil {
		t.Fatal("r should return a rerun command")
	}
}

func jobsFixture() []protocol.JobInfo {
	return []protocol.JobInfo{
		{ID: 3, Command: "make all", Label: "build", Session: "work", State: protocol.StateRunning},
		{ID: 1, Command: "go test ./...", Session: "default", State: protocol.StateQueued},
		{ID: 2, Command: "lint", Session: "work", State: protocol.StateFinished},
	}
}

func TestFilterJobsScope(t *testing.T) {
	jobs := jobsFixture()
	if got := filterJobs(jobs, "", "work", false); len(got) != 2 {
		t.Fatalf("session scope = %d jobs, want 2", len(got))
	}
	if got := filterJobs(jobs, "", "work", true); len(got) != 3 {
		t.Fatalf("all scope = %d jobs, want 3", len(got))
	}
}

func TestFilterJobsText(t *testing.T) {
	jobs := jobsFixture()
	// case-insensitive across command/label/session
	if got := filterJobs(jobs, "TEST", "", true); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("command match failed: %+v", got)
	}
	if got := filterJobs(jobs, "build", "", true); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("label match failed: %+v", got)
	}
	if got := filterJobs(jobs, "default", "", true); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("session match failed: %+v", got)
	}
}

func TestSortJobsByID(t *testing.T) {
	got := sortJobs(jobsFixture())
	for i, want := range []int{1, 2, 3} {
		if got[i].ID != want {
			t.Fatalf("sorted[%d] = %d, want %d", i, got[i].ID, want)
		}
	}
}

func TestAnchorCursor(t *testing.T) {
	rows := sortJobs(jobsFixture()) // IDs 1,2,3

	// existing job keeps selection regardless of position
	if c, id := anchorCursor(rows, 3, 0); c != 2 || id != 3 {
		t.Fatalf("anchor existing = (%d,%d), want (2,3)", c, id)
	}
	// removed job clamps to prevCursor and re-derives ID
	if c, id := anchorCursor(rows, 99, 2); c != 2 || id != 3 {
		t.Fatalf("anchor missing = (%d,%d), want (2,3)", c, id)
	}
	// prevCursor past end clamps
	if c, _ := anchorCursor(rows, 99, 10); c != 2 {
		t.Fatalf("anchor clamp = %d, want 2", c)
	}
	// empty
	if c, id := anchorCursor(nil, 5, 1); c != 0 || id != 0 {
		t.Fatalf("anchor empty = (%d,%d), want (0,0)", c, id)
	}
}

func TestClampOffset(t *testing.T) {
	// total fits: no scroll
	if off := clampOffset(0, 0, 10, 5); off != 0 {
		t.Fatalf("fits = %d, want 0", off)
	}
	// cursor below window scrolls down
	if off := clampOffset(9, 0, 5, 20); off != 5 {
		t.Fatalf("scroll down = %d, want 5", off)
	}
	// cursor above window scrolls up
	if off := clampOffset(2, 8, 5, 20); off != 2 {
		t.Fatalf("scroll up = %d, want 2", off)
	}
	// offset never exceeds max
	if off := clampOffset(19, 100, 5, 20); off != 15 {
		t.Fatalf("max clamp = %d, want 15", off)
	}
}

func TestClampScroll(t *testing.T) {
	if off := clampScroll(100, 5, 20); off != 15 {
		t.Fatalf("clamp = %d, want 15", off)
	}
	if off := clampScroll(-3, 5, 20); off != 0 {
		t.Fatalf("negative = %d, want 0", off)
	}
	if off := clampScroll(3, 10, 5); off != 0 {
		t.Fatalf("fits = %d, want 0", off)
	}
}

func TestComputeJobColumns(t *testing.T) {
	c := computeJobColumns(80)
	if c.command < 1 {
		t.Fatalf("command width must be positive, got %d", c.command)
	}
	// narrow terminal must not panic or produce negative widths
	for _, w := range []int{40, 30, 20, 10, 5} {
		c := computeJobColumns(w)
		if c.command < 1 || c.session < 0 {
			t.Fatalf("bad columns at inner=%d: %+v", w, c)
		}
	}
}

func TestFormatJobRowWidth(t *testing.T) {
	c := computeJobColumns(80)
	row := formatJobRow(jobsFixture()[0], "work", c)
	if w := len(stripAnsi(row)); w == 0 {
		t.Fatal("empty row")
	}
}

func TestComputeJobColumnsHasGroupSession(t *testing.T) {
	c := computeJobColumns(120)
	if c.group <= 0 || c.session <= 0 {
		t.Fatalf("group/session columns must be positive, got group=%d session=%d", c.group, c.session)
	}
}

func TestFormatJobRowShowsGroupAndSession(t *testing.T) {
	j := protocol.JobInfo{
		ID:      4,
		Command: "make",
		Session: "build",
		State:   protocol.StateFinished,
	}
	row := stripAnsi(formatJobRow(j, "ci", computeJobColumns(120)))
	if !strings.Contains(row, "ci") {
		t.Errorf("row missing group: %q", row)
	}
	if !strings.Contains(row, "build") {
		t.Errorf("row missing session: %q", row)
	}
}

func TestClockOrEmpty(t *testing.T) {
	if got := clockOrEmpty(time.Time{}); got != "" {
		t.Errorf("zero time should render empty, got %q", got)
	}
}

// `i` in the pager toggles the job-info panel, which surfaces job details.
func TestPagerInfoToggle(t *testing.T) {
	m := model{viewMode: viewJobs, width: 80, height: 24}
	m.jobs.mode = jobsPager
	m.jobs.pager.info = protocol.JobInfo{ID: 2, Command: "go build", State: protocol.StateFinished}

	if len(m.pagerInfoLines(40)) != 0 {
		t.Fatal("info panel should be hidden by default")
	}
	got, _ := m.handlePagerKey(runeKey("i"))
	m = got.(model)
	if !m.jobs.pager.showInfo {
		t.Fatal("i should enable the info panel")
	}
	lines := strings.Join(m.pagerInfoLines(40), "\n")
	if !strings.Contains(lines, "go build") || !strings.Contains(lines, "finished") {
		t.Errorf("info panel missing job details: %q", lines)
	}
	got, _ = m.handlePagerKey(runeKey("i"))
	if got.(model).jobs.pager.showInfo {
		t.Error("second i should hide the info panel")
	}
}

func TestDropPartialFirstLine(t *testing.T) {
	if got := dropPartialFirstLine([]byte("partial\nfull line\n")); string(got) != "full line\n" {
		t.Fatalf("drop = %q", got)
	}
	if got := dropPartialFirstLine([]byte("no newline")); string(got) != "no newline" {
		t.Fatalf("no-newline = %q", got)
	}
}

func TestPagerSplitLines(t *testing.T) {
	got := pagerSplitLines([]byte("a\r\nb\tc\n"))
	if len(got) != 2 || got[0] != "a" || got[1] != "b    c" {
		t.Fatalf("split = %#v", got)
	}
	if got := pagerSplitLines(nil); got != nil {
		t.Fatalf("empty = %#v", got)
	}
}

func TestFindMatches(t *testing.T) {
	lines := []string{"hello world", "goodbye", "Hello again"}
	got := findMatches(lines, "hello")
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("matches = %v, want [0 2]", got)
	}
	if findMatches(lines, "") != nil {
		t.Fatal("empty query should match nothing")
	}
}

func TestJobsActionIDs(t *testing.T) {
	var m model
	m.jobs.rows = jobRows(sortJobs(jobsFixture())) // IDs 1,2,3
	m.jobs.cursor = 1

	// no visual selection -> just the cursor row
	if got := m.jobsActionIDs(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("single = %v, want [2]", got)
	}

	// visual selection spans anchor..cursor inclusive, regardless of direction
	m.jobs.visual = true
	m.jobs.anchor = 2
	m.jobs.cursor = 0
	got := m.jobsActionIDs()
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("visual = %v, want [1 2 3]", got)
	}
}

func TestShorten(t *testing.T) {
	if got := shorten("short", 20); got != "short" {
		t.Fatalf("no-op = %q", got)
	}
	if got := shorten("a very long command line", 10); !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis, got %q", got)
	}
}
