package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/protocol"
)

// The `:` command line and the tea.Cmd wrappers around client actions
// (kill, restart, rerun, reset, settings).

func setJobTimeoutCmd(id int, ms int64) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetJobTimeout(id, ms); err != nil {
			return actionDoneMsg{err: err}
		}
		if ms <= 0 {
			return actionDoneMsg{status: fmt.Sprintf("job %d timeout cleared", id)}
		}
		return actionDoneMsg{status: fmt.Sprintf("job %d timeout set to %s", id, durationCompact(time.Duration(ms)*time.Millisecond))}
	}
}

func setJobLabelCmd(id int, label string) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetJobLabel(id, label); err != nil {
			return actionDoneMsg{err: err}
		}
		if label == "" {
			return actionDoneMsg{status: fmt.Sprintf("job %d name cleared", id)}
		}
		return actionDoneMsg{status: fmt.Sprintf("job %d named %q", id, label)}
	}
}

// --- command mode (`:`) ---------------------------------------------------

func (m model) handleJobsCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.jobs.commanding = false
		m.cmdInput.Blur()
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.cmdInput.Value())
		m.jobs.commanding = false
		m.cmdInput.Blur()
		if value == "" {
			return m, nil
		}
		return m.executeJobsCommand(value)
	}
	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	return m, cmd
}

// executeJobsCommand runs a `:` command typed on the management screen. It
// covers quick navigation plus a small config surface (parallel slots, log
// directory, sort order).
func (m model) executeJobsCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(strings.TrimPrefix(input, ":"))
	if len(fields) == 0 {
		return m, nil
	}
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	// `set`/`config` are optional prefixes: `:set slots 4` == `:slots 4`.
	if (cmd == "set" || cmd == "config") && len(args) > 0 {
		cmd = strings.ToLower(args[0])
		args = args[1:]
	} else if cmd == "config" {
		// Open the settings edit box; the logdir is fetched first to pre-fill it.
		return m, loadSettingsCmd()
	}

	switch cmd {
	case "help", "h", "?":
		m.jobs.helping = true
		return m, nil
	case "q", "quit":
		return m, tea.Quit
	case "slots", "parallel", "p":
		if len(args) == 0 {
			m.status = fmt.Sprintf("slots %d — usage: :set slots <n>", m.maxSlots)
			return m, nil
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			m.status = fmt.Sprintf("invalid slot count: %q", args[0])
			return m, nil
		}
		return m, setMaxSlotsCmd(n)
	case "logdir":
		if len(args) == 0 {
			return m, showLogdirCmd()
		}
		return m, setLogdirCmd(args[0])
	case "sort":
		if len(args) == 0 {
			m.status = "usage: :sort <group|id|state|time>"
			return m, nil
		}
		if sm, ok := parseSortMode(args[0]); ok {
			m.jobs.sortMode = sm
			m.refreshJobsRows()
		} else {
			m.status = fmt.Sprintf("unknown sort: %q", args[0])
		}
		return m, nil
	case "clear":
		return m, clearFinishedCmd()
	case "kill":
		// :kill <id...> | -a/--all | (no arg: current selection)
		if len(args) > 0 && (args[0] == "-a" || args[0] == "--all") {
			return m, killAllCmd()
		}
		ids, err := m.commandTargetIDs(args)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.jobs.visual = false
		m.jobs.tagged = nil
		return m, killJobs(ids)
	case "restart":
		// :restart <id...> | -a/--all | (no arg: current selection). Running
		// jobs are killed first; every non-queued target is re-enqueued.
		if len(args) > 0 && (args[0] == "-a" || args[0] == "--all") {
			return m, restartAllCmd()
		}
		ids, err := m.commandTargetIDs(args)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.jobs.visual = false
		m.jobs.tagged = nil
		return m, restartJobs(ids)
	case "reset":
		m.jobs.confirm = confirmState{kind: confirmReset}
		return m, nil
	default:
		m.status = fmt.Sprintf("unknown command: %s", input)
	}
	return m, nil
}

// commandTargetIDs resolves a `:` command's job targets: explicit ids from
// its arguments, else the current selection (tagged / visual / cursor row).
func (m model) commandTargetIDs(args []string) ([]int, error) {
	if len(args) == 0 {
		ids := m.jobsActionIDs()
		if len(ids) == 0 {
			return nil, fmt.Errorf("no job selected")
		}
		return ids, nil
	}
	ids := make([]int, 0, len(args))
	for _, a := range args {
		id, err := strconv.Atoi(a)
		if err != nil {
			return nil, fmt.Errorf("invalid job id %q", a)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func killAllCmd() tea.Cmd {
	return func() tea.Msg {
		if err := client.KillAllJobs(); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "killed all running jobs"}
	}
}

// restartJobsMsg kills the running targets and re-enqueues every non-queued
// one; queued targets are already pending and left alone.
func restartJobsMsg(jobs []protocol.JobInfo) tea.Msg {
	count := 0
	for _, j := range jobs {
		if j.State == protocol.StateQueued {
			continue
		}
		if j.State == protocol.StateRunning {
			_ = client.KillJob(j.ID)
		}
		if _, err := client.Rerun(j.ID); err != nil {
			return actionDoneMsg{err: err}
		}
		count++
	}
	return actionDoneMsg{status: fmt.Sprintf("restarted %d job(s)", count)}
}

func restartJobs(ids []int) tea.Cmd {
	return func() tea.Msg {
		jobs := make([]protocol.JobInfo, 0, len(ids))
		for _, id := range ids {
			info, err := client.GetInfo(id)
			if err != nil {
				return actionDoneMsg{err: err}
			}
			jobs = append(jobs, *info)
		}
		return restartJobsMsg(jobs)
	}
}

func restartAllCmd() tea.Cmd {
	return func() tea.Msg {
		res, err := client.ListJobs()
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return restartJobsMsg(res.Jobs)
	}
}

func renameGroupCmd(oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		if err := client.GroupRename(oldName, newName); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("renamed group %q -> %q", oldName, newName)}
	}
}

func resetServerCmd() tea.Cmd {
	return func() tea.Msg {
		if err := client.ResetServer(); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "daemon reset to defaults"}
	}
}

func setMaxSlotsCmd(n int) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetMaxSlots(n); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: fmt.Sprintf("parallel slots set to %d", n)}
	}
}

func setLogdirCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := client.SetLogdir(path); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "log directory set to " + path}
	}
}

func showLogdirCmd() tea.Cmd {
	return func() tea.Msg {
		dir, err := client.GetLogdir()
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{status: "log directory: " + dir}
	}
}

func killJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, killJob(id))
	}
	return tea.Batch(cmds...)
}

func makeUrgentJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, makeUrgent(id))
	}
	return tea.Batch(cmds...)
}

func removeJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, removeJob(id))
	}
	return tea.Batch(cmds...)
}

func rerunJobs(ids []int) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, rerunJob(id))
	}
	return tea.Batch(cmds...)
}
