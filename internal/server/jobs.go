package server

import (
	"sort"
	"sync"
	"time"

	"github.com/han/qrush/internal/protocol"
)

type Job struct {
	ID               int
	Info             protocol.JobInfo
	CommandArgs      []string
	ShouldKeepFinish bool
	GzipOutput       bool
	SeparateStderr   bool
	RequireElevel    bool
	WorkDir          string
	Environment      []string
	Logfile          string
	waitChs          []chan protocol.Result
}

type JobQueue struct {
	mu       sync.Mutex
	jobs     []*Job
	byID     map[int]*Job
	nextID   int
	lastID   int
	sessions map[string]string
	groups   map[string]bool
}

func NewJobQueue() *JobQueue {
	return &JobQueue{
		byID:     make(map[int]*Job),
		sessions: map[string]string{"default": "default"},
		groups:   map[string]bool{"default": true},
	}
}

// RerunRequest reconstructs a NewJobRequest from an existing job so it can be
// re-enqueued as a fresh job. DependOn is intentionally dropped — a rerun should
// not re-wait on the original job's (now-finished) dependencies.
func (q *JobQueue) RerunRequest(id int) (protocol.NewJobRequest, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.byID[id]
	if !ok {
		return protocol.NewJobRequest{}, false
	}
	return protocol.NewJobRequest{
		Command:        j.Info.Command,
		CommandArgs:    j.CommandArgs,
		WorkDir:        j.WorkDir,
		Environment:    j.Environment,
		StoreOutput:    j.Info.StoreOutput,
		SeparateStderr: j.SeparateStderr,
		GzipOutput:     j.GzipOutput,
		RequireElevel:  j.RequireElevel,
		Label:          j.Info.Label,
		Session:        j.Info.Session,
		Message:        j.Info.Message,
		NumSlots:       j.Info.NumSlots,
		Logfile:        j.Logfile,
	}, true
}

func (q *JobQueue) Add(req protocol.NewJobRequest) *Job {
	return q.AddWithOutputPath(req, nil)
}

// AddWithOutputPath enqueues a job and, when output is stored, assigns its
// output filename via pathFor inside the same critical section. Setting the
// path after Add returns would race with the scheduler: a concurrent poke can
// start the job first, making the executor pick a different random path than
// the one later recorded in Info.OutputFilename.
func (q *JobQueue) AddWithOutputPath(req protocol.NewJobRequest, pathFor func(jobID int, logfile string) string) *Job {
	q.mu.Lock()
	defer q.mu.Unlock()

	numSlots := req.NumSlots
	if numSlots < 1 {
		numSlots = 1
	}

	session := req.Session
	if session == "" {
		session = "default"
	}
	if _, ok := q.sessions[session]; !ok {
		q.sessions[session] = "default"
	}

	j := &Job{
		ID: q.nextID,
		Info: protocol.JobInfo{
			ID:          q.nextID,
			Command:     req.Command,
			State:       protocol.StateQueued,
			StoreOutput: req.StoreOutput,
			DependOn:    req.DependOn,
			Label:       req.Label,
			Session:     session,
			Message:     req.Message,
			NumSlots:    numSlots,
			EnqueueTime: time.Now(),
		},
		CommandArgs:      req.CommandArgs,
		ShouldKeepFinish: true,
		GzipOutput:       req.GzipOutput,
		SeparateStderr:   req.SeparateStderr,
		RequireElevel:    req.RequireElevel,
		WorkDir:          req.WorkDir,
		Environment:      req.Environment,
		Logfile:          req.Logfile,
	}
	if pathFor != nil && req.StoreOutput {
		j.Info.OutputFilename = pathFor(j.ID, req.Logfile)
	}

	q.jobs = append(q.jobs, j)
	q.byID[j.ID] = j
	q.lastID = q.nextID
	q.nextID++
	return j
}

func (q *JobQueue) Remove(id int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, ok := q.byID[id]
	if !ok {
		return false
	}
	if j.Info.State == protocol.StateRunning {
		return false
	}

	delete(q.byID, id)
	for i, job := range q.jobs {
		if job.ID == id {
			q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
			break
		}
	}
	return true
}

// GetInfo returns a snapshot copy of the job's info, safe to read without locks.
func (q *JobQueue) GetInfo(id int) (protocol.JobInfo, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.byID[id]
	if !ok {
		return protocol.JobInfo{}, false
	}
	return j.Info, true
}

// GetJob returns the job pointer. Caller must not read Info fields without holding the queue lock.
// Used internally by scheduler and executor.
func (q *JobQueue) GetJob(id int) (*Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.byID[id]
	return j, ok
}

// AllInfo returns snapshot copies of all job infos, safe to read without locks.
func (q *JobQueue) AllInfo() []protocol.JobInfo {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]protocol.JobInfo, len(q.jobs))
	for i, j := range q.jobs {
		result[i] = j.Info
	}
	return result
}

func (q *JobQueue) LastID() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.lastID
}

func (q *JobQueue) SetRunning(id int, pid int, outputFile string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if j, ok := q.byID[id]; ok {
		j.Info.State = protocol.StateRunning
		j.Info.PID = pid
		if outputFile != "" {
			j.Info.OutputFilename = outputFile
		}
		j.Info.StartTime = time.Now()
	}
}

func (q *JobQueue) SetOutputFilename(id int, outputFile string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if j, ok := q.byID[id]; ok {
		j.Info.OutputFilename = outputFile
	}
}

func (q *JobQueue) MarkFinished(id int, result protocol.Result) {
	q.mu.Lock()

	j, ok := q.byID[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	j.Info.State = protocol.StateFinished
	j.Info.Result = result
	j.Info.EndTime = time.Now()
	waitChs := j.waitChs
	j.waitChs = nil
	q.mu.Unlock()

	for _, ch := range waitChs {
		ch <- result
		close(ch)
	}
}

func (q *JobQueue) MarkSkipped(id int) {
	q.mu.Lock()

	j, ok := q.byID[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	j.Info.State = protocol.StateSkipped
	j.Info.Result = protocol.Result{Skipped: true}
	j.Info.EndTime = time.Now()
	waitChs := j.waitChs
	j.waitChs = nil
	q.mu.Unlock()

	for _, ch := range waitChs {
		ch <- j.Info.Result
		close(ch)
	}
}

func (q *JobQueue) ClearFinished() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	var remaining []*Job
	count := 0
	for _, j := range q.jobs {
		if j.Info.State == protocol.StateFinished || j.Info.State == protocol.StateSkipped {
			delete(q.byID, j.ID)
			count++
		} else {
			remaining = append(remaining, j)
		}
	}
	q.jobs = remaining
	return count
}

func (q *JobQueue) PruneFinished(maxKeep int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var finished []*Job
	for _, j := range q.jobs {
		if j.Info.State == protocol.StateFinished || j.Info.State == protocol.StateSkipped {
			finished = append(finished, j)
		}
	}

	toRemove := len(finished) - maxKeep
	if toRemove <= 0 {
		return
	}

	removeIDs := make(map[int]bool)
	for i := 0; i < toRemove; i++ {
		removeIDs[finished[i].ID] = true
		delete(q.byID, finished[i].ID)
	}

	var remaining []*Job
	for _, j := range q.jobs {
		if !removeIDs[j.ID] {
			remaining = append(remaining, j)
		}
	}
	q.jobs = remaining
}

func (q *JobQueue) MakeUrgent(id int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	idx := -1
	for i, j := range q.jobs {
		if j.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}

	j := q.jobs[idx]
	if j.Info.State != protocol.StateQueued {
		return false
	}

	firstQueued := -1
	for i, job := range q.jobs {
		if job.Info.State == protocol.StateQueued {
			firstQueued = i
			break
		}
	}
	if firstQueued < 0 || firstQueued == idx {
		return true
	}

	q.jobs = append(q.jobs[:idx], q.jobs[idx+1:]...)
	rear := make([]*Job, len(q.jobs[firstQueued:]))
	copy(rear, q.jobs[firstQueued:])
	q.jobs = append(q.jobs[:firstQueued], j)
	q.jobs = append(q.jobs, rear...)
	return true
}

func (q *JobQueue) Swap(id1, id2 int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	idx1, idx2 := -1, -1
	for i, j := range q.jobs {
		if j.ID == id1 {
			idx1 = i
		}
		if j.ID == id2 {
			idx2 = i
		}
	}
	if idx1 < 0 || idx2 < 0 {
		return false
	}
	q.jobs[idx1], q.jobs[idx2] = q.jobs[idx2], q.jobs[idx1]
	return true
}

func (q *JobQueue) WaitFor(id int) <-chan protocol.Result {
	q.mu.Lock()
	defer q.mu.Unlock()

	ch := make(chan protocol.Result, 1)
	j, ok := q.byID[id]
	if !ok {
		ch <- protocol.Result{ExitCode: -1}
		close(ch)
		return ch
	}
	if j.Info.State == protocol.StateFinished || j.Info.State == protocol.StateSkipped {
		ch <- j.Info.Result
		close(ch)
		return ch
	}
	j.waitChs = append(j.waitChs, ch)
	return ch
}

// NextRunnable returns a copy of the next runnable job's info plus its internal ID.
// Must be called under scheduler lock but NOT under queue lock.
func (q *JobQueue) NextRunnable(maxSlots int, busySlots int) (*Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	freeSlots := maxSlots - busySlots
	for _, j := range q.jobs {
		if j.Info.State != protocol.StateQueued {
			continue
		}
		if j.Info.NumSlots > freeSlots {
			continue
		}
		if !q.dependenciesMet(j) {
			continue
		}
		return j, true
	}
	return nil, false
}

func (q *JobQueue) CheckSkippable() []int {
	q.mu.Lock()
	defer q.mu.Unlock()

	var toSkip []int
	for _, j := range q.jobs {
		if j.Info.State != protocol.StateQueued {
			continue
		}
		if q.shouldSkip(j) {
			toSkip = append(toSkip, j.ID)
		}
	}
	return toSkip
}

func (q *JobQueue) shouldSkip(j *Job) bool {
	if !j.RequireElevel {
		return false
	}
	for _, depID := range j.Info.DependOn {
		dep, ok := q.byID[depID]
		if !ok {
			continue
		}
		if dep.Info.State == protocol.StateFinished && dep.Info.Result.ExitCode != 0 {
			return true
		}
		if dep.Info.State == protocol.StateSkipped {
			return true
		}
	}
	return false
}

func (q *JobQueue) dependenciesMet(j *Job) bool {
	for _, depID := range j.Info.DependOn {
		dep, ok := q.byID[depID]
		if !ok {
			continue
		}
		if dep.Info.State != protocol.StateFinished && dep.Info.State != protocol.StateSkipped {
			return false
		}
	}
	return true
}

func (q *JobQueue) RunningCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	count := 0
	for _, j := range q.jobs {
		if j.Info.State == protocol.StateRunning {
			count++
		}
	}
	return count
}

func (q *JobQueue) BusySlots() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	slots := 0
	for _, j := range q.jobs {
		if j.Info.State == protocol.StateRunning {
			slots += j.Info.NumSlots
		}
	}
	return slots
}

func (q *JobQueue) AllSessions() []string {
	q.mu.Lock()
	defer q.mu.Unlock()

	sessions := make([]string, 0, len(q.sessions))
	for s := range q.sessions {
		sessions = append(sessions, s)
	}
	sort.Strings(sessions)
	return sessions
}

func (q *JobQueue) AllSessionInfo() []protocol.SessionInfo {
	q.mu.Lock()
	defer q.mu.Unlock()

	sessions := make([]protocol.SessionInfo, 0, len(q.sessions))
	for name, group := range q.sessions {
		sessions = append(sessions, protocol.SessionInfo{Name: name, Group: group})
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Group == sessions[j].Group {
			return sessions[i].Name < sessions[j].Name
		}
		return sessions[i].Group < sessions[j].Group
	})
	return sessions
}

func (q *JobQueue) AllGroups() []string {
	q.mu.Lock()
	defer q.mu.Unlock()

	groups := make([]string, 0, len(q.groups))
	for group := range q.groups {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func (q *JobQueue) AllInfoBySession(session string) []protocol.JobInfo {
	q.mu.Lock()
	defer q.mu.Unlock()

	var result []protocol.JobInfo
	for _, j := range q.jobs {
		if j.Info.Session == session {
			result = append(result, j.Info)
		}
	}
	return result
}

func (q *JobQueue) ClearFinishedInSession(session string) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	var remaining []*Job
	count := 0
	for _, j := range q.jobs {
		if j.Info.Session == session && (j.Info.State == protocol.StateFinished || j.Info.State == protocol.StateSkipped) {
			delete(q.byID, j.ID)
			count++
		} else {
			remaining = append(remaining, j)
		}
	}
	q.jobs = remaining
	return count
}

func (q *JobQueue) CreateSession(name string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.sessions[name]; ok {
		return false
	}
	q.sessions[name] = "default"
	return true
}

func (q *JobQueue) SessionExists(name string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.sessions[name]
	return ok
}

func (q *JobQueue) RenameSession(oldName, newName string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	group, ok := q.sessions[oldName]
	if !ok || oldName == "default" {
		return false
	}
	if _, ok := q.sessions[newName]; ok {
		return false
	}

	delete(q.sessions, oldName)
	q.sessions[newName] = group

	for _, j := range q.jobs {
		if j.Info.Session == oldName {
			j.Info.Session = newName
		}
	}
	return true
}

func (q *JobQueue) DeleteSession(name string) (bool, string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if name == "default" {
		return false, "cannot delete default session"
	}
	if _, ok := q.sessions[name]; !ok {
		return false, "session not found"
	}

	for _, j := range q.jobs {
		if j.Info.Session == name {
			if j.Info.State == protocol.StateRunning || j.Info.State == protocol.StateQueued {
				return false, "session has active jobs"
			}
		}
	}

	var remaining []*Job
	for _, j := range q.jobs {
		if j.Info.Session == name {
			delete(q.byID, j.ID)
		} else {
			remaining = append(remaining, j)
		}
	}
	q.jobs = remaining
	delete(q.sessions, name)
	return true, ""
}

func (q *JobQueue) CreateGroup(name string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if name == "" || q.groups[name] {
		return false
	}
	q.groups[name] = true
	return true
}

func (q *JobQueue) RenameGroup(oldName, newName string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if oldName == "default" || newName == "" || !q.groups[oldName] || q.groups[newName] {
		return false
	}
	delete(q.groups, oldName)
	q.groups[newName] = true
	for session, group := range q.sessions {
		if group == oldName {
			q.sessions[session] = newName
		}
	}
	return true
}

func (q *JobQueue) DeleteGroup(name string) (bool, string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if name == "default" {
		return false, "cannot delete default group"
	}
	if !q.groups[name] {
		return false, "group not found"
	}
	for _, group := range q.sessions {
		if group == name {
			return false, "group has sessions"
		}
	}
	delete(q.groups, name)
	return true, ""
}

func (q *JobQueue) MoveSession(session, group string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.sessions[session]; !ok {
		return false
	}
	if !q.groups[group] {
		return false
	}
	q.sessions[session] = group
	return true
}
