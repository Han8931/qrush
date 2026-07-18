package server_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/han/qrush/internal/client"
	"github.com/han/qrush/internal/config"
	"github.com/han/qrush/internal/protocol"
	"github.com/han/qrush/internal/server"
)

// shortSocketPath returns a socket path in a freshly created short temp dir.
// t.TempDir() embeds the full test name, which pushes the path past the
// 104-byte sun_path limit on macOS and makes bind fail with EINVAL.
func shortSocketPath(t *testing.T) string {
	dir, err := os.MkdirTemp("", "qrush")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "qrush.sock")
}

// TestTUIAttachTakeover: registering a second interactive TUI displaces the
// first — it is sent MsgTUITakenOver and its connection is closed — so two
// TUIs never mirror the same panes.
func TestTUIAttachTakeover(t *testing.T) {
	sock := shortSocketPath(t)
	t.Setenv("QRUSH_SOCKET", sock)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	srv, err := server.New(config.Load())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Run(ctx)
	t.Cleanup(func() { srv.Shutdown(); cancel() })

	waitForSocket(t, sock)

	first, err := client.AttachTUI()
	if err != nil {
		t.Fatalf("first AttachTUI: %v", err)
	}
	defer first.Close()

	second, err := client.AttachTUI()
	if err != nil {
		t.Fatalf("second AttachTUI: %v", err)
	}
	defer second.Close()

	done := make(chan error, 1)
	go func() {
		msg, err := first.Recv()
		if err != nil {
			done <- err
			return
		}
		if msg.Type != protocol.MsgTUITakenOver {
			t.Errorf("expected MsgTUITakenOver, got %v", msg.Type)
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first TUI should receive MsgTUITakenOver before disconnect, got error %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first TUI was never notified of the takeover")
	}
}

// TestTerminalPersistenceOverSocket drives the real client→daemon→PTY→stream
// path over a unix socket: open a pane, run a command, detach, reattach, and
// confirm the backlog still replays (persistence). Also checks layout get/set
// and reaping via the wire.
func TestTerminalPersistenceOverSocket(t *testing.T) {
	sock := shortSocketPath(t)
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
	sock := shortSocketPath(t)
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
