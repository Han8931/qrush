package server

import (
	"context"
	"sync"

	"github.com/han/qrush/internal/protocol"
)

type Scheduler struct {
	jobs         *JobQueue
	executor     *Executor
	maxSlots     int
	trigger      chan struct{}
	mu           sync.Mutex
	onFinishHook func()
}

func NewScheduler(jobs *JobQueue, executor *Executor, maxSlots int) *Scheduler {
	return &Scheduler{
		jobs:     jobs,
		executor: executor,
		maxSlots: maxSlots,
		trigger:  make(chan struct{}, 1),
	}
}

func (s *Scheduler) SetMaxSlots(n int) {
	s.mu.Lock()
	s.maxSlots = n
	s.mu.Unlock()
	s.Poke()
}

func (s *Scheduler) MaxSlots() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxSlots
}

func (s *Scheduler) Run(ctx context.Context) {
	s.trySchedule()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			s.trySchedule()
		}
	}
}

func (s *Scheduler) trySchedule() {
	for _, id := range s.jobs.CheckSkippable() {
		s.jobs.MarkSkipped(id)
	}

	for {
		s.mu.Lock()
		maxSlots := s.maxSlots
		s.mu.Unlock()

		busySlots := s.jobs.BusySlots()

		job, ok := s.jobs.NextRunnable(maxSlots, busySlots)
		if !ok {
			break
		}

		s.jobs.SetRunning(job.ID, 0, "")
		go s.executor.Run(ExecRequest{
			Job:      job,
			JobQueue: s.jobs,
			OnFinish: s.onJobFinish,
		})
	}
}

func (s *Scheduler) onJobFinish(jobID int, result protocol.Result) {
	s.jobs.MarkFinished(jobID, result)
	if s.onFinishHook != nil {
		s.onFinishHook()
	}
	s.Poke()
}

func (s *Scheduler) SetOnFinishHook(fn func()) {
	s.onFinishHook = fn
}

func (s *Scheduler) Poke() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}
