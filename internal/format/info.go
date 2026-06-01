package format

import (
	"fmt"
	"strings"

	"github.com/han/qrush/internal/protocol"
)

func FormatJobInfo(j *protocol.JobInfo) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Command:    %s\n", j.Command)
	fmt.Fprintf(&b, "State:      %s\n", j.State)
	fmt.Fprintf(&b, "Slots:      %d\n", j.NumSlots)

	if j.Label != "" {
		fmt.Fprintf(&b, "Label:      %s\n", j.Label)
	}

	if j.Message != "" {
		fmt.Fprintf(&b, "Message:    %s\n", j.Message)
	}

	if !j.EnqueueTime.IsZero() {
		fmt.Fprintf(&b, "Enqueued:   %s\n", j.EnqueueTime.Format("2006-01-02 15:04:05"))
	}
	if !j.StartTime.IsZero() {
		fmt.Fprintf(&b, "Started:    %s\n", j.StartTime.Format("2006-01-02 15:04:05"))
	}
	if !j.EndTime.IsZero() {
		fmt.Fprintf(&b, "Ended:      %s\n", j.EndTime.Format("2006-01-02 15:04:05"))
	}

	if j.State == protocol.StateRunning {
		fmt.Fprintf(&b, "PID:        %d\n", j.PID)
	}

	if j.State == protocol.StateFinished {
		fmt.Fprintf(&b, "Exit code:  %d\n", j.Result.ExitCode)
		if j.Result.RealTimeMS > 0 {
			fmt.Fprintf(&b, "Real time:  %s\n", Duration(j.Result.RealTimeMS))
			fmt.Fprintf(&b, "User time:  %s\n", Duration(j.Result.UserTimeMS))
			fmt.Fprintf(&b, "Sys time:   %s\n", Duration(j.Result.SystemTimeMS))
		}
	}

	if j.OutputFilename != "" {
		fmt.Fprintf(&b, "Output:     %s\n", j.OutputFilename)
	}

	if len(j.DependOn) > 0 {
		deps := make([]string, len(j.DependOn))
		for i, d := range j.DependOn {
			deps[i] = fmt.Sprintf("%d", d)
		}
		fmt.Fprintf(&b, "Depends on: %s\n", strings.Join(deps, ", "))
	}

	return b.String()
}
