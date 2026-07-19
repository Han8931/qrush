package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/han/qrush/internal/protocol"
)

const queueSnapshotVersion = 1

type queueSnapshot struct {
	Version  int               `json:"version"`
	NextID   int               `json:"next_id"`
	LastID   int               `json:"last_id"`
	Jobs     []*Job            `json:"jobs"`
	Sessions map[string]string `json:"sessions"`
	Groups   map[string]bool   `json:"groups"`
}

func (q *JobQueue) save(path string) error {
	if path == "" {
		return nil
	}

	q.mu.Lock()
	if !q.dirty {
		q.mu.Unlock()
		return nil
	}
	q.dirty = false
	snapshot := queueSnapshot{
		Version:  queueSnapshotVersion,
		NextID:   q.nextID,
		LastID:   q.lastID,
		Jobs:     q.jobs,
		Sessions: q.sessions,
		Groups:   q.groups,
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	q.mu.Unlock()
	if err != nil {
		q.markDirty()
		return fmt.Errorf("encode queue snapshot: %w", err)
	}

	if err := writeSnapshot(path, data); err != nil {
		// The change is still unpersisted; re-mark so the next save retries.
		q.markDirty()
		return err
	}
	return nil
}

func (q *JobQueue) markDirty() {
	q.mu.Lock()
	q.dirty = true
	q.mu.Unlock()
}

func writeSnapshot(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".qrush-snapshot-*")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write queue snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync queue snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close queue snapshot: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace queue snapshot: %w", err)
	}
	return nil
}

func loadJobQueue(path string) (*JobQueue, error) {
	q := NewJobQueue()
	if path == "" {
		return q, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read queue snapshot: %w", err)
	}

	var snapshot queueSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode queue snapshot: %w", err)
	}
	if snapshot.Version != queueSnapshotVersion {
		return nil, fmt.Errorf("unsupported queue snapshot version %d", snapshot.Version)
	}

	q.jobs = snapshot.Jobs
	q.nextID = snapshot.NextID
	q.lastID = snapshot.LastID
	if snapshot.Sessions != nil {
		q.sessions = snapshot.Sessions
	}
	if snapshot.Groups != nil {
		q.groups = snapshot.Groups
	}
	q.sessions["default"] = "default"
	q.groups["default"] = true

	now := time.Now()
	for _, job := range q.jobs {
		if job == nil {
			continue
		}
		q.byID[job.ID] = job
		if job.Info.State == protocol.StateRunning || job.Info.State == protocol.StateAllocating || job.Info.State == protocol.StateHoldingClient {
			job.Info.State = protocol.StateFinished
			job.Info.PID = 0
			job.Info.EndTime = now
			job.Info.Result = protocol.Result{ExitCode: -1}
			// The fix-up must reach the next snapshot.
			q.dirty = true
		}
	}
	return q, nil
}
