package client

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/han/qrush/internal/config"
	"github.com/han/qrush/internal/ipc"
	"github.com/han/qrush/internal/protocol"
)

type Client struct {
	conn net.Conn
}

func Connect() (*Client, error) {
	path := ipc.SocketPath()
	dialer := ipc.NewDialer(path)
	conn, err := dialer.Dial()
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) Send(msg *protocol.Msg) error {
	return protocol.Send(c.conn, msg)
}

func (c *Client) Recv() (*protocol.Msg, error) {
	return protocol.Recv(c.conn)
}

func EnsureServer() error {
	path := ipc.SocketPath()
	dialer := ipc.NewDialer(path)
	conn, err := dialer.Dial()
	if err == nil {
		version, vErr := serverVersion(conn)
		conn.Close()
		switch {
		case vErr == nil && version == protocol.ProtocolVersion:
			return nil
		case vErr == nil:
			// A daemon is listening but speaks a different protocol. Never
			// kill it automatically: it may be running jobs and hosting live
			// shell panes. The user decides when those may die.
			return fmt.Errorf("running daemon speaks protocol v%d, this ru speaks v%d — finish its work, then `ru -K` and retry",
				version, protocol.ProtocolVersion)
		default:
			var refused errDaemonRefused
			if errors.As(vErr, &refused) {
				// Our daemon, but it turned us away (e.g. connection cap).
				return fmt.Errorf("daemon: %s", refused.msg)
			}
			return fmt.Errorf("something unrecognised is listening on %s — stop it or point QRUSH_SOCKET elsewhere", path)
		}
	}

	return startServer(dialer)
}

// errDaemonRefused marks a well-formed MsgError refusal from a live qrush
// daemon (as opposed to garbage from an unrelated listener).
type errDaemonRefused struct{ msg string }

func (e errDaemonRefused) Error() string { return e.msg }

func serverVersion(conn net.Conn) (int, error) {
	if err := protocol.Send(conn, &protocol.Msg{Type: protocol.MsgGetVersion}); err != nil {
		return 0, err
	}
	msg, err := protocol.Recv(conn)
	if err != nil {
		return 0, err
	}
	payload, err := protocol.PayloadAs[protocol.PayloadVersion](msg)
	if err != nil {
		if msg.Type == protocol.MsgError {
			return 0, errDaemonRefused{msg: err.Error()}
		}
		return 0, err
	}
	return payload.Version, nil
}

func startServer(dialer ipc.Dialer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	cmd := exec.Command(exe, "--server")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	setDaemonProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	cmd.Process.Release()

	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		conn, err := dialer.Dial()
		if err == nil {
			conn.Close()
			return nil
		}
	}
	hint := ""
	if p := config.DaemonLogPath(); p != "" {
		hint = " (see " + p + ")"
	}
	return fmt.Errorf("server failed to start within 5 seconds%s", hint)
}
