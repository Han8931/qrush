package client

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

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
		ok := serverVersionOK(conn)
		conn.Close()
		if ok {
			return nil
		}
		_ = killExistingServer(dialer)
		waitForServerStop(dialer)
	}

	return startServer(dialer)
}

func serverVersionOK(conn net.Conn) bool {
	if err := protocol.Send(conn, &protocol.Msg{Type: protocol.MsgGetVersion}); err != nil {
		return false
	}
	msg, err := protocol.Recv(conn)
	if err != nil || msg.Type != protocol.MsgVersion {
		return false
	}
	payload, err := protocol.PayloadAs[protocol.PayloadVersion](msg)
	if err != nil {
		return false
	}
	return payload.Version == protocol.ProtocolVersion
}

func killExistingServer(dialer ipc.Dialer) error {
	conn, err := dialer.Dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	return protocol.Send(conn, &protocol.Msg{Type: protocol.MsgKillServer})
}

func waitForServerStop(dialer ipc.Dialer) {
	for i := 0; i < 20; i++ {
		conn, err := dialer.Dial()
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
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
	return fmt.Errorf("server failed to start within 5 seconds")
}
