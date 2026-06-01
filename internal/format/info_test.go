package format

import (
	"strings"
	"testing"
	"time"

	"github.com/han/qrush/internal/protocol"
)

func TestFormatJobInfo(t *testing.T) {
	enqueue := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	start := enqueue.Add(time.Second)
	end := start.Add(5 * time.Second)

	j := &protocol.JobInfo{
		ID:             1,
		Command:        "make test",
		State:          protocol.StateFinished,
		Label:          "test",
		NumSlots:       2,
		EnqueueTime:    enqueue,
		StartTime:      start,
		EndTime:        end,
		DependOn:       []int{0},
		OutputFilename: "/tmp/out.log",
		Result: protocol.Result{
			ExitCode:   0,
			RealTimeMS: 5000,
			UserTimeMS: 3000,
		},
	}

	out := FormatJobInfo(j)

	checks := []string{
		"Command:    make test",
		"State:      finished",
		"Slots:      2",
		"Label:      test",
		"Exit code:  0",
		"Output:     /tmp/out.log",
		"Depends on: 0",
		"Enqueued:",
		"Started:",
		"Ended:",
	}

	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("expected %q in output, got:\n%s", c, out)
		}
	}
}

func TestFormatJobInfoMinimal(t *testing.T) {
	j := &protocol.JobInfo{
		ID:       0,
		Command:  "ls",
		State:    protocol.StateQueued,
		NumSlots: 1,
	}

	out := FormatJobInfo(j)
	if !strings.Contains(out, "Command:    ls") {
		t.Errorf("expected command in output, got:\n%s", out)
	}
	if strings.Contains(out, "Label:") {
		t.Error("should not contain Label when empty")
	}
	if strings.Contains(out, "PID:") {
		t.Error("should not contain PID when not running")
	}
}

func TestFormatJobInfoRunningShowsPID(t *testing.T) {
	j := &protocol.JobInfo{
		ID:       0,
		Command:  "sleep 100",
		State:    protocol.StateRunning,
		PID:      12345,
		NumSlots: 1,
	}

	out := FormatJobInfo(j)
	if !strings.Contains(out, "PID:        12345") {
		t.Errorf("expected PID in output, got:\n%s", out)
	}
}
