package server_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/config"
	"github.com/han/qrush/internal/protocol"
	"github.com/han/qrush/internal/server"
)

// TestTerminalPersistenceOverSocket drives the real client→daemon→PTY→stream
// path over a unix socket: open a pane, run a command, detach, reattach, and
// confirm the backlog still replays (persistence). Also checks layout get/set
// and reaping via the wire.
func TestTerminalPersistenceOverSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "qrush.sock")
	t.Setenv("QRUSH_SOCKET", sock)
	t.Setenv("SHELL", "/bin/sh")

	srv, err := server.New(config.Load())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Run(ctx)
	t.Cleanup(func() { srv.Shutdown(); cancel() })

	waitForSocket(t, sock)

	pane, err := client.OpenTerminal("s", 80, 24)
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}

	// Attach, run a command, observe its output, then detach (leaves pane alive).
	c1, err := client.AttachTerminal("s", pane, 80, 24)
	if err != nil {
		t.Fatalf("AttachTerminal: %v", err)
	}
	_ = c1.Send(&protocol.Msg{
		Type:    protocol.MsgTerminalInput,
		Payload: protocol.PayloadTerminalData{Data: []byte("printf qrush-persist-ok\\n\r")},
	})
	if !recvUntil(t, c1, []byte("qrush-persist-ok"), 3*time.Second) {
		t.Fatal("did not observe command output on first attach")
	}
	c1.Close() // detach

	// Reattach: the daemon should replay the backlog containing the output.
	c2, err := client.AttachTerminal("s", pane, 80, 24)
	if err != nil {
		t.Fatalf("re-AttachTerminal: %v", err)
	}
	defer c2.Close()
	if !recvUntil(t, c2, []byte("qrush-persist-ok"), 3*time.Second) {
		t.Fatal("backlog not replayed on reattach — persistence broken")
	}

	// Layout get/set over the wire, and reaping of panes not kept.
	if err := client.SetTerminalLayout("s", []byte("blob"), []string{pane}); err != nil {
		t.Fatalf("SetTerminalLayout: %v", err)
	}
	blob, alive, err := client.GetTerminalLayout("s")
	if err != nil {
		t.Fatalf("GetTerminalLayout: %v", err)
	}
	if string(blob) != "blob" {
		t.Fatalf("layout blob = %q, want blob", blob)
	}
	if len(alive) != 1 || alive[0] != pane {
		t.Fatalf("alive = %v, want [%s]", alive, pane)
	}
}

// TestRequestJobsViewSignal verifies the open-jobs-view signal: a request sets
// a one-shot flag that the next tree poll observes and clears.
func TestRequestJobsViewSignal(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "qrush.sock")
	t.Setenv("QRUSH_SOCKET", sock)

	srv, err := server.New(config.Load())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Run(ctx)
	t.Cleanup(func() { srv.Shutdown(); cancel() })
	waitForSocket(t, sock)

	// No request yet: the flag is clear.
	if data, err := client.TreeData(); err != nil {
		t.Fatalf("TreeData: %v", err)
	} else if data.OpenJobsView {
		t.Fatal("OpenJobsView set before any request")
	}

	if err := client.RequestJobsView(); err != nil {
		t.Fatalf("RequestJobsView: %v", err)
	}

	// The next poll observes the flag.
	data, err := client.TreeData()
	if err != nil {
		t.Fatalf("TreeData after request: %v", err)
	}
	if !data.OpenJobsView {
		t.Fatal("OpenJobsView not set after request")
	}

	// And it is one-shot: a subsequent poll sees it cleared.
	data, err = client.TreeData()
	if err != nil {
		t.Fatalf("TreeData second poll: %v", err)
	}
	if data.OpenJobsView {
		t.Fatal("OpenJobsView still set on second poll; flag not consumed")
	}
}

func waitForSocket(t *testing.T, sock string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if c, err := client.Connect(); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start listening")
}

func recvUntil(t *testing.T, c *client.Client, needle []byte, timeout time.Duration) bool {
	t.Helper()
	found := make(chan bool, 1)
	go func() {
		var buf []byte
		for {
			msg, err := c.Recv()
			if err != nil {
				found <- false
				return
			}
			if msg.Type == protocol.MsgTerminalOutput {
				if p, perr := protocol.PayloadAs[protocol.PayloadTerminalData](msg); perr == nil {
					buf = append(buf, p.Data...)
					if bytes.Contains(buf, needle) {
						found <- true
						return
					}
				}
			}
		}
	}()
	select {
	case ok := <-found:
		return ok
	case <-time.After(timeout):
		return false
	}
}
