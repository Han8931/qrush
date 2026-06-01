package format

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/han/qrush/internal/protocol"
)

func TestFormatJobListEmpty(t *testing.T) {
	out := FormatJobList(nil, 10, protocol.FormatDefault)
	if !strings.HasPrefix(out, "Running: 0/10") {
		t.Errorf("expected running count, got %q", out)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "STATE") || !strings.Contains(out, "COMMAND") {
		t.Errorf("expected header, got %q", out)
	}
	if !strings.Contains(out, "No jobs") {
		t.Errorf("expected no jobs message, got %q", out)
	}
}

func TestFormatJobListDefault(t *testing.T) {
	jobs := []protocol.JobInfo{
		{ID: 0, Command: "echo hello", State: protocol.StateFinished, Result: protocol.Result{RealTimeMS: 1000}},
		{ID: 1, Command: "sleep 10", State: protocol.StateRunning, Result: protocol.Result{RealTimeMS: 3000}},
		{ID: 2, Command: "make build", State: protocol.StateQueued},
	}

	out := FormatJobList(jobs, 10, protocol.FormatDefault)
	if !strings.HasPrefix(out, "Running: 1/10") {
		t.Errorf("expected running count in output, got %q", out)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "STATE") || !strings.Contains(out, "COMMAND") {
		t.Errorf("expected header in output, got %q", out)
	}
	if !strings.Contains(out, "echo hello") {
		t.Error("expected 'echo hello' in output")
	}
	if !strings.Contains(out, "finished") {
		t.Error("expected 'finished' in output")
	}
	if !strings.Contains(out, "running") {
		t.Error("expected 'running' in output")
	}
	if !strings.Contains(out, "queued") {
		t.Error("expected 'queued' in output")
	}
}

func TestFormatJobListWithLabel(t *testing.T) {
	jobs := []protocol.JobInfo{
		{ID: 0, Command: "make", State: protocol.StateQueued, Label: "build"},
	}
	out := FormatJobList(jobs, 1, protocol.FormatDefault)
	if !strings.Contains(out, "[build]make") {
		t.Errorf("expected labeled output, got %q", out)
	}
}

func TestFormatJobListJSON(t *testing.T) {
	jobs := []protocol.JobInfo{
		{ID: 0, Command: "echo", State: protocol.StateFinished},
	}
	out := FormatJobList(jobs, 1, protocol.FormatJSON)
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 job, got %d", len(parsed))
	}
	if parsed[0]["Command"] != "echo" {
		t.Errorf("expected Command='echo', got %v", parsed[0]["Command"])
	}
	if parsed[0]["State"] != "finished" {
		t.Errorf("expected State='finished', got %v", parsed[0]["State"])
	}
}

func TestFormatJobListTab(t *testing.T) {
	jobs := []protocol.JobInfo{
		{ID: 0, Command: "echo", State: protocol.StateFinished, Result: protocol.Result{RealTimeMS: 500}},
	}
	out := FormatJobList(jobs, 1, protocol.FormatTab)
	parts := strings.Split(strings.TrimSpace(out), "\t")
	if len(parts) != 4 {
		t.Errorf("expected 4 tab-separated fields, got %d: %q", len(parts), out)
	}
}

func TestFormatStateSignal(t *testing.T) {
	j := protocol.JobInfo{State: protocol.StateFinished, Result: protocol.Result{DiedBySignal: true, Signal: 9}}
	got := formatState(j)
	if got != "signal 9" {
		t.Errorf("expected 'signal 9', got %q", got)
	}
}

func TestFormatStateExitCode(t *testing.T) {
	j := protocol.JobInfo{State: protocol.StateFinished, Result: protocol.Result{ExitCode: 1}}
	got := formatState(j)
	if got != "exit 1" {
		t.Errorf("expected 'exit 1', got %q", got)
	}
}

func TestFormatStateSkipped(t *testing.T) {
	j := protocol.JobInfo{State: protocol.StateSkipped}
	got := formatState(j)
	if got != "skipped" {
		t.Errorf("expected 'skipped', got %q", got)
	}
}
