package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

type JobState int

const (
	StateQueued JobState = iota
	StateAllocating
	StateRunning
	StateFinished
	StateSkipped
	StateHoldingClient
)

func (s JobState) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateAllocating:
		return "allocating"
	case StateRunning:
		return "running"
	case StateFinished:
		return "finished"
	case StateSkipped:
		return "skipped"
	case StateHoldingClient:
		return "holding_client"
	default:
		return "unknown"
	}
}

func (s JobState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *JobState) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("job state must be a string: %w", err)
	}
	states := map[string]JobState{
		"queued":         StateQueued,
		"allocating":     StateAllocating,
		"running":        StateRunning,
		"finished":       StateFinished,
		"skipped":        StateSkipped,
		"holding_client": StateHoldingClient,
	}
	state, ok := states[name]
	if !ok {
		return fmt.Errorf("unknown job state %q", name)
	}
	*s = state
	return nil
}

type ListFormat int

const (
	FormatDefault ListFormat = iota
	FormatJSON
	FormatTab
)

type Result struct {
	ExitCode     int
	DiedBySignal bool
	Signal       int
	UserTimeMS   int64
	SystemTimeMS int64
	RealTimeMS   int64
	Skipped      bool
	TimedOut     bool // killed because it exceeded the job's --timeout
}

type JobInfo struct {
	ID             int
	Command        string
	State          JobState
	Result         Result
	OutputFilename string
	StoreOutput    bool
	PID            int
	DependOn       []int
	Label          string
	Session        string
	Message        string
	NumSlots       int
	NumGPUs        int
	GPUIDs         []int
	EnqueueTime    time.Time
	StartTime      time.Time
	EndTime        time.Time
	TimeoutMS      int64 // kill the job after this wall-clock time (0: none)
	Retries        int   // extra attempts allowed after a failure
	Attempt        int   // retries used so far (0 on the first run)
}

type SessionInfo struct {
	Name  string
	Group string
}

type NewJobRequest struct {
	Command        string
	CommandArgs    []string
	WorkDir        string
	Environment    []string
	StoreOutput    bool
	SeparateStderr bool
	GzipOutput     bool
	DependOn       []int
	RequireElevel  bool
	Label          string
	Session        string
	Message        string
	NumSlots       int
	Logfile        string
	TimeoutMS      int64
	Retries        int
}
