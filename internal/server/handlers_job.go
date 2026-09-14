package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"

	"github.com/han/qrush/internal/config"
	"github.com/han/qrush/internal/protocol"
)

// Request handlers for the job queue: submit, query, reorder, kill and the
// per-job settings (label, timeout) plus the global slot count.

func (s *Server) handleNewJob(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadNewJob](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	req := payload.Request
	req.Environment = s.envStore.Apply(req.Environment)
	job := s.jobs.AddWithOutputPath(req, s.executor.OutputPathFor)

	ok := s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgNewJobOK,
		Payload: protocol.PayloadJobID{JobID: job.ID},
	})

	s.scheduler.Poke()
	return ok
}

func (s *Server) handleRerun(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	req, ok := s.jobs.RerunRequest(payload.JobID)
	if !ok {
		return s.sendError(conn, fmt.Sprintf("job %d not found", payload.JobID))
	}
	job := s.jobs.AddWithOutputPath(req, s.executor.OutputPathFor)
	sent := s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgNewJobOK,
		Payload: protocol.PayloadJobID{JobID: job.ID},
	})
	s.scheduler.Poke()
	return sent
}

func (s *Server) handleList(conn net.Conn) bool {
	jobs := s.jobs.AllInfo()
	for _, info := range jobs {
		if !s.sendMsg(conn, &protocol.Msg{
			Type:    protocol.MsgListLine,
			Payload: protocol.PayloadListLine{Job: info},
		}) {
			return false
		}
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgListEnd,
		Payload: protocol.PayloadSlots{Slots: s.scheduler.MaxSlots()},
	})
}

func (s *Server) handleGetVersion(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgVersion,
		Payload: protocol.PayloadVersion{Version: protocol.ProtocolVersion},
	})
}

func (s *Server) handleRemoveJob(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if s.jobs.Remove(payload.JobID) {
		return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgRemoveJobOK})
	}
	return s.sendError(conn, "cannot remove job")
}

func (s *Server) handleGetState(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerState,
		Payload: protocol.PayloadState{State: info.State},
	})
}

func (s *Server) handleAskOutput(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type: protocol.MsgAnswerOutput,
		Payload: protocol.PayloadOutput{
			Filename: info.OutputFilename,
			PID:      info.PID,
		},
	})
}

func (s *Server) handleInfo(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgInfoData,
		Payload: protocol.PayloadInfo{Job: info},
	})
}

func (s *Server) handleSetMaxSlots(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSlots](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Slots < 1 {
		return s.sendError(conn, "slots must be greater than zero")
	}
	s.scheduler.SetMaxSlots(payload.Slots)
	// Persist so the setting survives a daemon restart.
	if err := config.SaveRuntime("slots", strconv.Itoa(payload.Slots)); err != nil {
		log.Printf("persist slots: %v", err)
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleGetMaxSlots(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgGetMaxSlotsOK,
		Payload: protocol.PayloadSlots{Slots: s.scheduler.MaxSlots()},
	})
}

func (s *Server) handleUrgent(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.MakeUrgent(payload.JobID) {
		return s.sendError(conn, "cannot make job urgent")
	}
	s.scheduler.Poke()
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgUrgentOK})
}

func (s *Server) handleSwapJobs(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSwap](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.Swap(payload.ID1, payload.ID2) {
		return s.sendError(conn, "cannot swap jobs")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSwapJobsOK})
}

func (s *Server) handleWaitJob(ctx context.Context, conn net.Conn, msg *protocol.Msg) bool {
	// A wait can block for the job's whole runtime — don't let it hold one of
	// the maxConns slots, or a handful of `ru -w` would lock everyone out
	// (terminal and TUI attaches are exempted the same way).
	s.activeConns.Add(-1)
	defer s.activeConns.Add(1)

	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	ch := s.jobs.WaitFor(payload.JobID)

	select {
	case result := <-ch:
		return s.sendMsg(conn, &protocol.Msg{
			Type:    protocol.MsgWaitJobOK,
			Payload: protocol.PayloadResult{Result: result},
		})
	case <-ctx.Done():
		return false
	}
}

func (s *Server) handleKillJob(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if err := s.executor.Kill(payload.JobID); err != nil {
		return s.sendError(conn, err.Error())
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleCountRunning(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgCountRunningOK,
		Payload: protocol.PayloadCount{Count: s.jobs.RunningCount()},
	})
}

func (s *Server) handleGetLabel(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerLabel,
		Payload: protocol.PayloadLabel{Label: info.Label},
	})
}

func (s *Server) handleSetJobLabel(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSetLabel](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.SetLabel(payload.JobID, payload.Label) {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleSetJobTimeout(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSetTimeout](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.SetTimeout(payload.JobID, payload.TimeoutMS) {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleLastID(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgLastIDOK,
		Payload: protocol.PayloadJobID{JobID: s.jobs.LastID()},
	})
}

func (s *Server) handleGetCmd(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	info, ok := s.jobs.GetInfo(payload.JobID)
	if !ok {
		return s.sendError(conn, "job not found")
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerCmd,
		Payload: protocol.PayloadCmd{Cmd: info.Command},
	})
}
