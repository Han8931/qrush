package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/han/qrush/internal/protocol"
)

func TestJobQueueSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q := NewJobQueue()
	q.CreateGroup("work")
	q.CreateSession("build")
	q.MoveSession("build", "work")
	first := q.Add(protocol.NewJobRequest{
		Command:     "make build",
		CommandArgs: []string{"make", "build"},
		Session:     "build",
		Label:       "compile",
		NumSlots:    2,
	})
	q.MarkFinished(first.ID, protocol.Result{ExitCode: 0})
	q.Add(protocol.NewJobRequest{Command: "make test", DependOn: []int{first.ID}})

	if err := q.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	restored, err := loadJobQueue(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	jobs := restored.AllInfo()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Label != "compile" || jobs[0].Session != "build" || jobs[0].NumSlots != 2 {
		t.Fatalf("job metadata was not restored: %+v", jobs[0])
	}
	if jobs[1].State != protocol.StateQueued || len(jobs[1].DependOn) != 1 {
		t.Fatalf("queued job was not restored: %+v", jobs[1])
	}
	if got := restored.AllSessionInfo(); len(got) != 2 {
		t.Fatalf("expected sessions to be restored, got %+v", got)
	}
	if next := restored.Add(protocol.NewJobRequest{Command: "next"}); next.ID != 2 {
		t.Fatalf("expected next ID 2, got %d", next.ID)
	}
}

func TestLoadJobQueueMarksRunningJobsInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q := NewJobQueue()
	job := q.Add(protocol.NewJobRequest{Command: "long-running"})
	q.SetRunning(job.ID, 1234, "output.log")
	if err := q.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	restored, err := loadJobQueue(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	info, ok := restored.GetInfo(job.ID)
	if !ok {
		t.Fatal("restored job not found")
	}
	if info.State != protocol.StateFinished || info.Result.ExitCode != -1 || info.PID != 0 {
		t.Fatalf("running job was not marked interrupted: %+v", info)
	}
}

func TestLoadJobQueueRejectsCorruptSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadJobQueue(path); err == nil {
		t.Fatal("expected corrupt snapshot to be rejected")
	}
}

func TestLoadJobQueueMissingSnapshotUsesEmptyQueue(t *testing.T) {
	q, err := loadJobQueue(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if jobs := q.AllInfo(); len(jobs) != 0 {
		t.Fatalf("expected empty queue, got %+v", jobs)
	}
}
