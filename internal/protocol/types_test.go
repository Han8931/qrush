package protocol

import (
	"encoding/json"
	"testing"
)

func TestJobStateString(t *testing.T) {
	tests := []struct {
		state JobState
		want  string
	}{
		{StateQueued, "queued"},
		{StateAllocating, "allocating"},
		{StateRunning, "running"},
		{StateFinished, "finished"},
		{StateSkipped, "skipped"},
		{StateHoldingClient, "holding_client"},
		{JobState(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("JobState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestJobStateMarshalJSON(t *testing.T) {
	tests := []struct {
		state JobState
		want  string
	}{
		{StateQueued, `"queued"`},
		{StateRunning, `"running"`},
		{StateFinished, `"finished"`},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.state)
		if err != nil {
			t.Errorf("Marshal(%d): %v", tt.state, err)
			continue
		}
		if string(data) != tt.want {
			t.Errorf("Marshal(%d) = %s, want %s", tt.state, data, tt.want)
		}
	}
}

func TestJobInfoMarshalJSON(t *testing.T) {
	j := JobInfo{
		ID:      1,
		Command: "echo",
		State:   StateRunning,
	}

	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}

	if m["State"] != "running" {
		t.Errorf("expected State='running', got %v", m["State"])
	}
}
