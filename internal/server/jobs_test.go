package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/han/qrush/internal/protocol"
)

func TestJobQueueAddAndGet(t *testing.T) {
	q := NewJobQueue()
	j := q.Add(protocol.NewJobRequest{Command: "echo hello", CommandArgs: []string{"echo", "hello"}, NumSlots: 1})

	if j.ID != 0 {
		t.Errorf("expected ID=0, got %d", j.ID)
	}
	if j.Info.State != protocol.StateQueued {
		t.Errorf("expected StateQueued, got %v", j.Info.State)
	}

	info, ok := q.GetInfo(0)
	if !ok {
		t.Fatal("job not found")
	}
	if info.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", info.Command)
	}
}

func TestJobQueueAutoIncrement(t *testing.T) {
	q := NewJobQueue()
	j1 := q.Add(protocol.NewJobRequest{Command: "a"})
	j2 := q.Add(protocol.NewJobRequest{Command: "b"})
	j3 := q.Add(protocol.NewJobRequest{Command: "c"})

	if j1.ID != 0 || j2.ID != 1 || j3.ID != 2 {
		t.Errorf("unexpected IDs: %d, %d, %d", j1.ID, j2.ID, j3.ID)
	}
}

func TestJobQueueLastID(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.Add(protocol.NewJobRequest{Command: "b"})

	if q.LastID() != 1 {
		t.Errorf("expected LastID=1, got %d", q.LastID())
	}
}

func TestJobQueueRemove(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.Add(protocol.NewJobRequest{Command: "b"})

	if !q.Remove(0) {
		t.Error("expected Remove(0) to succeed")
	}
	if _, ok := q.GetInfo(0); ok {
		t.Error("job 0 should be removed")
	}

	all := q.AllInfo()
	if len(all) != 1 || all[0].ID != 1 {
		t.Errorf("unexpected remaining jobs: %+v", all)
	}
}

func TestJobQueueRemoveRunningFails(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "sleep 100"})
	q.SetRunning(0, 1234, "/tmp/out")

	if q.Remove(0) {
		t.Error("should not remove a running job")
	}
}

func TestJobQueueRemoveNonexistent(t *testing.T) {
	q := NewJobQueue()
	if q.Remove(99) {
		t.Error("should not remove nonexistent job")
	}
}

func TestJobQueueSetRunning(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "ls"})
	q.SetRunning(0, 5678, "/tmp/out.log")

	info, _ := q.GetInfo(0)
	if info.State != protocol.StateRunning {
		t.Errorf("expected StateRunning, got %v", info.State)
	}
	if info.PID != 5678 {
		t.Errorf("expected PID=5678, got %d", info.PID)
	}
	if info.OutputFilename != "/tmp/out.log" {
		t.Errorf("expected output file, got %q", info.OutputFilename)
	}
}

func TestJobQueueSetRunningPreservesExistingOutputFilename(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "ls"})
	q.SetOutputFilename(0, "/tmp/queued.out")
	q.SetRunning(0, 5678, "")

	info, _ := q.GetInfo(0)
	if info.OutputFilename != "/tmp/queued.out" {
		t.Errorf("expected existing output filename to be preserved, got %q", info.OutputFilename)
	}
}

func TestJobQueueMarkFinished(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "ls"})
	q.SetRunning(0, 100, "")
	q.MarkFinished(0, protocol.Result{ExitCode: 0, RealTimeMS: 500})

	info, _ := q.GetInfo(0)
	if info.State != protocol.StateFinished {
		t.Errorf("expected StateFinished, got %v", info.State)
	}
	if info.Result.ExitCode != 0 {
		t.Errorf("expected ExitCode=0, got %d", info.Result.ExitCode)
	}
}

func TestJobQueueMarkSkipped(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "test"})
	q.MarkSkipped(0)

	info, _ := q.GetInfo(0)
	if info.State != protocol.StateSkipped {
		t.Errorf("expected StateSkipped, got %v", info.State)
	}
}

func TestJobQueueClearFinished(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.Add(protocol.NewJobRequest{Command: "b"})
	q.Add(protocol.NewJobRequest{Command: "c"})

	q.MarkFinished(0, protocol.Result{})
	q.MarkSkipped(1)

	count := q.ClearFinished()
	if count != 2 {
		t.Errorf("expected 2 cleared, got %d", count)
	}

	all := q.AllInfo()
	if len(all) != 1 || all[0].ID != 2 {
		t.Errorf("unexpected remaining: %+v", all)
	}
}

func TestJobQueuePruneFinished(t *testing.T) {
	q := NewJobQueue()
	for i := 0; i < 5; i++ {
		q.Add(protocol.NewJobRequest{Command: "cmd"})
		q.MarkFinished(i, protocol.Result{})
	}

	q.PruneFinished(2)

	all := q.AllInfo()
	if len(all) != 2 {
		t.Errorf("expected 2 remaining, got %d", len(all))
	}
}

func TestJobQueueRemoveDeletesGeneratedOutput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "ru_0_abcd.out")
	if err := os.WriteFile(out, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	q := NewJobQueue()
	j := q.AddWithOutputPath(protocol.NewJobRequest{Command: "a", StoreOutput: true},
		func(int, string) string { return out })
	q.SetRunning(j.ID, 1, "")
	q.MarkFinished(j.ID, protocol.Result{})

	if !q.Remove(j.ID) {
		t.Fatal("expected remove to succeed")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("expected generated output file to be deleted, stat err=%v", err)
	}
}

func TestJobQueueClearFinishedKeepsUserLogfile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "custom.log")
	if err := os.WriteFile(out, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	q := NewJobQueue()
	j := q.AddWithOutputPath(protocol.NewJobRequest{Command: "a", StoreOutput: true, Logfile: out},
		func(_ int, logfile string) string { return logfile })
	q.SetRunning(j.ID, 1, "")
	q.MarkFinished(j.ID, protocol.Result{})

	if n := q.ClearFinished(); n != 1 {
		t.Fatalf("expected 1 cleared, got %d", n)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("user-specified logfile must survive clear: %v", err)
	}
}

func TestJobQueueMakeUrgent(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.Add(protocol.NewJobRequest{Command: "b"})
	q.Add(protocol.NewJobRequest{Command: "c"})

	if !q.MakeUrgent(2) {
		t.Error("expected MakeUrgent to succeed")
	}

	all := q.AllInfo()
	if all[0].Command != "c" {
		t.Errorf("expected job 'c' first, got %q", all[0].Command)
	}
}

func TestJobQueueMakeUrgentNonQueued(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.SetRunning(0, 100, "")

	if q.MakeUrgent(0) {
		t.Error("should not make running job urgent")
	}
}

func TestJobQueueSwap(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.Add(protocol.NewJobRequest{Command: "b"})

	if !q.Swap(0, 1) {
		t.Error("expected Swap to succeed")
	}

	all := q.AllInfo()
	if all[0].Command != "b" || all[1].Command != "a" {
		t.Errorf("unexpected order after swap: %q, %q", all[0].Command, all[1].Command)
	}
}

func TestJobQueueSwapNonexistent(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})

	if q.Swap(0, 99) {
		t.Error("should not swap with nonexistent job")
	}
}

func TestJobQueueWaitForFinished(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.MarkFinished(0, protocol.Result{ExitCode: 42})

	ch := q.WaitFor(0)
	result := <-ch
	if result.ExitCode != 42 {
		t.Errorf("expected ExitCode=42, got %d", result.ExitCode)
	}
}

func TestJobQueueWaitForNonexistent(t *testing.T) {
	q := NewJobQueue()
	ch := q.WaitFor(99)
	result := <-ch
	if result.ExitCode != -1 {
		t.Errorf("expected ExitCode=-1, got %d", result.ExitCode)
	}
}

func TestJobQueueWaitForPending(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})

	ch := q.WaitFor(0)
	q.MarkFinished(0, protocol.Result{ExitCode: 7})

	result := <-ch
	if result.ExitCode != 7 {
		t.Errorf("expected ExitCode=7, got %d", result.ExitCode)
	}
}

func TestJobQueueRunningCount(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.Add(protocol.NewJobRequest{Command: "b"})
	q.Add(protocol.NewJobRequest{Command: "c"})

	q.SetRunning(0, 100, "")
	q.SetRunning(1, 200, "")

	if q.RunningCount() != 2 {
		t.Errorf("expected 2 running, got %d", q.RunningCount())
	}
}

func TestJobQueueBusySlots(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", NumSlots: 2})
	q.Add(protocol.NewJobRequest{Command: "b", NumSlots: 3})

	q.SetRunning(0, 100, "")
	q.SetRunning(1, 200, "")

	if q.BusySlots() != 5 {
		t.Errorf("expected 5 busy slots, got %d", q.BusySlots())
	}
}

func TestJobQueueNextRunnable(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", NumSlots: 1})

	j, ok := q.NextRunnable(1, 0)
	if !ok {
		t.Fatal("expected a runnable job")
	}
	if j.Info.Command != "a" {
		t.Errorf("expected command 'a', got %q", j.Info.Command)
	}
}

func TestJobQueueNextRunnableNoSlots(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", NumSlots: 2})

	_, ok := q.NextRunnable(2, 1)
	if ok {
		t.Error("should not find runnable job when not enough free slots")
	}
}

func TestJobQueueNextRunnableClampsToMaxSlots(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", NumSlots: 4})

	// Needing more slots than exist must not queue the job forever: it runs
	// once the machine is fully idle.
	if _, ok := q.NextRunnable(1, 1); ok {
		t.Error("should not run an oversized job while any slot is busy")
	}
	j, ok := q.NextRunnable(1, 0)
	if !ok || j.Info.Command != "a" {
		t.Error("expected oversized job to be runnable on an idle queue")
	}
}

func TestJobQueueNextRunnableDependency(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", NumSlots: 1})
	q.Add(protocol.NewJobRequest{Command: "b", NumSlots: 1, DependOn: []int{0}})

	j, ok := q.NextRunnable(2, 0)
	if !ok || j.Info.Command != "a" {
		t.Error("expected 'a' to be runnable first")
	}

	q.SetRunning(0, 100, "")
	q.MarkFinished(0, protocol.Result{})

	j, ok = q.NextRunnable(2, 0)
	if !ok || j.Info.Command != "b" {
		t.Error("expected 'b' to be runnable after 'a' finished")
	}
}

func TestJobQueueMinSlots(t *testing.T) {
	q := NewJobQueue()
	j := q.Add(protocol.NewJobRequest{Command: "a", NumSlots: 0})
	if j.Info.NumSlots != 1 {
		t.Errorf("expected NumSlots=1 for 0 input, got %d", j.Info.NumSlots)
	}
}

func TestJobQueueSessionDefault(t *testing.T) {
	q := NewJobQueue()
	j := q.Add(protocol.NewJobRequest{Command: "echo"})
	if j.Info.Session != "default" {
		t.Errorf("expected session 'default', got %q", j.Info.Session)
	}
}

func TestJobQueueSessionExplicit(t *testing.T) {
	q := NewJobQueue()
	j := q.Add(protocol.NewJobRequest{Command: "make", Session: "build"})
	if j.Info.Session != "build" {
		t.Errorf("expected session 'build', got %q", j.Info.Session)
	}
}

func TestJobQueueAllSessions(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", Session: "build"})
	q.Add(protocol.NewJobRequest{Command: "b", Session: "deploy"})

	sessions := q.AllSessions()
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d: %v", len(sessions), sessions)
	}
	if sessions[0] != "build" || sessions[1] != "default" || sessions[2] != "deploy" {
		t.Errorf("unexpected sessions: %v", sessions)
	}
}

func TestJobQueueAllInfoBySession(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a"})
	q.Add(protocol.NewJobRequest{Command: "b", Session: "build"})
	q.Add(protocol.NewJobRequest{Command: "c", Session: "build"})

	jobs := q.AllInfoBySession("build")
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs in build, got %d", len(jobs))
	}
	if jobs[0].Command != "b" || jobs[1].Command != "c" {
		t.Errorf("unexpected jobs: %v", jobs)
	}
}

func TestJobQueueCreateSession(t *testing.T) {
	q := NewJobQueue()
	if !q.CreateSession("new") {
		t.Error("expected CreateSession to succeed")
	}
	if q.CreateSession("new") {
		t.Error("expected duplicate CreateSession to fail")
	}
	if !q.SessionExists("new") {
		t.Error("expected session to exist")
	}
}

func TestJobQueueRenameSession(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", Session: "old"})

	if !q.RenameSession("old", "new") {
		t.Error("expected rename to succeed")
	}
	if q.SessionExists("old") {
		t.Error("old session should not exist")
	}

	jobs := q.AllInfoBySession("new")
	if len(jobs) != 1 || jobs[0].Command != "a" {
		t.Errorf("unexpected jobs after rename: %v", jobs)
	}
}

func TestJobQueueRenameDefaultFails(t *testing.T) {
	q := NewJobQueue()
	if q.RenameSession("default", "other") {
		t.Error("should not rename default session")
	}
}

func TestJobQueueDeleteSession(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", Session: "temp"})
	q.MarkFinished(0, protocol.Result{})

	ok, reason := q.DeleteSession("temp")
	if !ok {
		t.Errorf("expected delete to succeed, got: %s", reason)
	}
	if q.SessionExists("temp") {
		t.Error("session should be deleted")
	}
}

func TestJobQueueDeleteSessionWithActiveJobs(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", Session: "active"})

	ok, _ := q.DeleteSession("active")
	if ok {
		t.Error("should not delete session with queued jobs")
	}
}

func TestJobQueueDeleteDefaultFails(t *testing.T) {
	q := NewJobQueue()
	ok, _ := q.DeleteSession("default")
	if ok {
		t.Error("should not delete default session")
	}
}

func TestJobQueueClearFinishedInSession(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", Session: "build"})
	q.Add(protocol.NewJobRequest{Command: "b"})
	q.MarkFinished(0, protocol.Result{})
	q.MarkFinished(1, protocol.Result{})

	count := q.ClearFinishedInSession("build")
	if count != 1 {
		t.Errorf("expected 1 cleared, got %d", count)
	}
	all := q.AllInfo()
	if len(all) != 1 || all[0].Command != "b" {
		t.Errorf("unexpected remaining: %+v", all)
	}
}

func TestJobQueueGroupLifecycleAndMoveSession(t *testing.T) {
	q := NewJobQueue()
	if !q.CreateGroup("work") {
		t.Fatal("expected group create to succeed")
	}
	if !q.CreateSession("build") {
		t.Fatal("expected session create to succeed")
	}
	if !q.MoveSession("build", "work") {
		t.Fatal("expected move session to succeed")
	}

	sessions := q.AllSessionInfo()
	found := false
	for _, session := range sessions {
		if session.Name == "build" {
			found = true
			if session.Group != "work" {
				t.Fatalf("expected build in work, got %q", session.Group)
			}
		}
	}
	if !found {
		t.Fatal("expected build session")
	}

	if q.CreateGroup("work") {
		t.Fatal("expected duplicate group create to fail")
	}
	ok, _ := q.DeleteGroup("work")
	if ok {
		t.Fatal("expected delete group with sessions to fail")
	}
	if !q.RenameGroup("work", "ci") {
		t.Fatal("expected rename group to succeed")
	}
}

func TestJobQueueCheckSkippable(t *testing.T) {
	q := NewJobQueue()
	q.Add(protocol.NewJobRequest{Command: "a", NumSlots: 1})
	q.Add(protocol.NewJobRequest{Command: "b", NumSlots: 1, DependOn: []int{0}})

	q.jobs[1].RequireElevel = true
	q.MarkFinished(0, protocol.Result{ExitCode: 1})

	toSkip := q.CheckSkippable()
	if len(toSkip) != 1 || toSkip[0] != 1 {
		t.Errorf("expected [1] to be skippable, got %v", toSkip)
	}
}
