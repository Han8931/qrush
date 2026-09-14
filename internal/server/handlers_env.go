package server

import (
	"log"
	"net"
	"os"

	"github.com/han/qrush/internal/config"
	"github.com/han/qrush/internal/protocol"
)

// Request handlers for the daemon's environment and log directory — the
// settings a client can read or change at runtime.

func (s *Server) handleGetEnv(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadEnv](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	val, ok := s.envStore.Get(payload.Key)
	if !ok {
		val = os.Getenv(payload.Key)
	}
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerEnv,
		Payload: protocol.PayloadEnv{Key: payload.Key, Value: val},
	})
}

func (s *Server) handleSetEnv(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadEnv](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Key == "" {
		return s.sendError(conn, "environment key is required")
	}
	s.envStore.Set(payload.Key, payload.Value)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleUnsetEnv(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadEnv](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Key == "" {
		return s.sendError(conn, "environment key is required")
	}
	s.envStore.Unset(payload.Key)
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}

func (s *Server) handleGetLogdir(conn net.Conn) bool {
	return s.sendMsg(conn, &protocol.Msg{
		Type:    protocol.MsgAnswerLogdir,
		Payload: protocol.PayloadLogdir{Path: s.executor.LogDir()},
	})
}

func (s *Server) handleSetLogdir(conn net.Conn, msg *protocol.Msg) bool {
	payload, err := protocol.PayloadAs[protocol.PayloadLogdir](msg)
	if err != nil {
		return s.sendError(conn, err.Error())
	}
	if payload.Path == "" {
		return s.sendError(conn, "log directory is required")
	}
	s.executor.SetLogDir(payload.Path)
	// Persist so the setting survives a daemon restart.
	if err := config.SaveRuntime("logdir", payload.Path); err != nil {
		log.Printf("persist logdir: %v", err)
	}
	return s.sendMsg(conn, &protocol.Msg{Type: protocol.MsgActionOK})
}
