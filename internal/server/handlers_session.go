package server

import (
	"net"

	"github.com/han/qrush/internal/protocol"
)

// Request handlers for sessions and groups, including the combined tree view
// the TUI polls.

func (s *Server) handleSessionList(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgSessionListOK,
		Payload: protocol.PayloadSessionList{Sessions: s.jobs.AllSessions()},
	})
}

func (s *Server) handleSessionCreate(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.CreateSession(payload.Name) {
		return s.sendError(conn, "session already exists")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionCreateOK})
}

func (s *Server) handleSessionRename(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSessionRename](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.RenameSession(payload.OldName, payload.NewName) {
		return s.sendError(conn, "cannot rename session")
	}
	s.terminals.RenameLayout(payload.OldName, payload.NewName)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionRenameOK})
}

func (s *Server) handleSessionDelete(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	ok, reason := s.jobs.DeleteSession(payload.Name)
	if !ok {
		return s.sendError(conn, reason)
	}
	s.terminals.DropSession(payload.Name)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionDeleteOK})
}

func (s *Server) handleListSession(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	jobs := s.jobs.AllInfoBySession(payload.Name)
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

func (s *Server) handleClearFinishedSession(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	s.jobs.ClearFinishedInSession(payload.Name)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleTreeList(conn net.Conn) bool {
	s.mu.Lock()
	openJobsView := s.pendingJobsView
	s.pendingJobsView = false
	s.mu.Unlock()
	return s.sendMsg(conn, &protocol.Msg{
		Type: protocol.MsgTreeListOK,
		Payload: protocol.PayloadTreeData{
			Groups:       s.jobs.AllGroups(),
			Sessions:     s.jobs.AllSessionInfo(),
			Jobs:         s.jobs.AllInfo(),
			MaxSlots:     s.scheduler.MaxSlots(),
			OpenJobsView: openJobsView,
		},
	})
}

func (s *Server) handleRequestJobsView(conn net.Conn) bool {
	s.mu.Lock()
	s.pendingJobsView = true
	s.mu.Unlock()
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgRequestJobsViewOK})
}

func (s *Server) handleGroupList(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgGroupListOK,
		Payload: protocol.PayloadGroupList{Groups: s.jobs.AllGroups()},
	})
}

func (s *Server) handleGroupCreate(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.CreateGroup(payload.Name) {
		return s.sendError(conn, "cannot create group")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgGroupCreateOK})
}

func (s *Server) handleGroupRename(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSessionRename](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.RenameGroup(payload.OldName, payload.NewName) {
		return s.sendError(conn, "cannot rename group")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgGroupRenameOK})
}

func (s *Server) handleGroupDelete(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSession](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	ok, reason := s.jobs.DeleteGroup(payload.Name)
	if !ok {
		return s.sendError(conn, reason)
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgGroupDeleteOK})
}

func (s *Server) handleSessionMove(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadSessionMove](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if !s.jobs.MoveSession(payload.Session, payload.Group) {
		return s.sendError(conn, "cannot move session")
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgSessionMoveOK})
}
