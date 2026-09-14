package server

import (
	"sort"

	"github.com/han/qrush/internal/protocol"
)

// Session and group bookkeeping on the queue. Sessions name a bucket of jobs;
// groups bucket sessions. Both default to "default", which always exists and
// cannot be renamed or deleted.

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

	var remaining, dropped []*Job
	for _, j := range q.jobs {
		if j.Info.Session == session && (j.Info.State == protocol.StateFinished || j.Info.State == protocol.StateSkipped) {
			delete(q.byID, j.ID)
			dropped = append(dropped, j)
		} else {
			remaining = append(remaining, j)
		}
	}
	q.jobs = remaining
	if len(dropped) > 0 {
		q.dirty = true
	}
	q.mu.Unlock()

	removeOutputFiles(dropped)
	return len(dropped)
}

func (q *JobQueue) CreateSession(name string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.sessions[name]; ok {
		return false
	}
	q.sessions[name] = "default"
	q.dirty = true
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
	q.dirty = true
	return true
}

func (q *JobQueue) DeleteSession(name string) (bool, string) {
	q.mu.Lock()

	if name == "default" {
		q.mu.Unlock()
		return false, "cannot delete default session"
	}
	if _, ok := q.sessions[name]; !ok {
		q.mu.Unlock()
		return false, "session not found"
	}

	for _, j := range q.jobs {
		if j.Info.Session == name {
			if j.Info.State == protocol.StateRunning || j.Info.State == protocol.StateQueued {
				q.mu.Unlock()
				return false, "session has active jobs"
			}
		}
	}

	var remaining, dropped []*Job
	for _, j := range q.jobs {
		if j.Info.Session == name {
			delete(q.byID, j.ID)
			dropped = append(dropped, j)
		} else {
			remaining = append(remaining, j)
		}
	}
	q.jobs = remaining
	delete(q.sessions, name)
	q.dirty = true
	q.mu.Unlock()

	removeOutputFiles(dropped)
	return true, ""
}

func (q *JobQueue) CreateGroup(name string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if name == "" || q.groups[name] {
		return false
	}
	q.groups[name] = true
	q.dirty = true
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
	q.dirty = true
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
	q.dirty = true
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
	q.dirty = true
	return true
}
