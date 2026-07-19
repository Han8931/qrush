package format

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/han/qrush/internal/protocol"
)

// ElapsedMS returns a job's wall-clock milliseconds: the measured result for
// finished jobs, the live elapsed time for running ones (Result is only
// filled in at completion, so it reads 0 while the job runs).
func ElapsedMS(j protocol.JobInfo) int64 {
	if j.State == protocol.StateRunning && !j.StartTime.IsZero() {
		return time.Since(j.StartTime).Milliseconds()
	}
	return j.Result.RealTimeMS
}

func FormatJobList(jobs []protocol.JobInfo, maxSlots int, format protocol.ListFormat) string {
	switch format {
	case protocol.FormatJSON:
		return formatJSON(jobs)
	case protocol.FormatTab:
		return formatTab(jobs)
	default:
		return formatDefault(jobs, maxSlots)
	}
}

func formatDefault(jobs []protocol.JobInfo, maxSlots int) string {
	var b strings.Builder
	running := 0
	for _, j := range jobs {
		if j.State == protocol.StateRunning {
			running++
		}
	}

	fmt.Fprintf(&b, "Running: %d/%d\n", running, maxSlots)
	fmt.Fprintf(&b, "%-4s %-12s %-7s %s\n", "ID", "STATE", "TIME", "COMMAND")
	if len(jobs) == 0 {
		fmt.Fprintln(&b, "No jobs")
		return b.String()
	}

	for _, j := range jobs {
		stateStr := formatState(j)
		timeStr := ""
		if j.State == protocol.StateRunning || j.State == protocol.StateFinished {
			timeStr = Duration(ElapsedMS(j))
		}

		label := j.Command
		if j.Label != "" {
			label = fmt.Sprintf("[%s]%s", j.Label, j.Command)
		}
		if timeStr == "" {
			timeStr = "-"
		}

		fmt.Fprintf(&b, "%-4d %-12s %-7s %s\n", j.ID, stateStr, timeStr, label)
	}

	return b.String()
}

func formatState(j protocol.JobInfo) string {
	switch j.State {
	case protocol.StateQueued:
		if j.Attempt > 0 {
			return fmt.Sprintf("retry %d/%d", j.Attempt, j.Retries)
		}
		return "queued"
	case protocol.StateAllocating:
		return "allocating"
	case protocol.StateRunning:
		return "running"
	case protocol.StateFinished:
		if j.Result.TimedOut {
			return "timeout"
		}
		if j.Result.DiedBySignal {
			return fmt.Sprintf("signal %d", j.Result.Signal)
		}
		if j.Result.ExitCode == 0 {
			return "finished"
		}
		return fmt.Sprintf("exit %d", j.Result.ExitCode)
	case protocol.StateSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

func formatJSON(jobs []protocol.JobInfo) string {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(data)
}

func formatTab(jobs []protocol.JobInfo) string {
	var b strings.Builder
	for _, j := range jobs {
		stateStr := formatState(j)
		timeStr := Duration(ElapsedMS(j))
		label := j.Command
		if j.Label != "" {
			label = fmt.Sprintf("[%s]%s", j.Label, j.Command)
		}
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\n", j.ID, stateStr, timeStr, label)
	}
	return b.String()
}
