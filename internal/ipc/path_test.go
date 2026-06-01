package ipc

import (
	"os"
	"strings"
	"testing"
)

func TestSocketPathDefault(t *testing.T) {
	t.Setenv("QRUSH_SOCKET", "")
	t.Setenv("TS_SOCKET", "")
	t.Setenv("TMPDIR", t.TempDir())
	path := SocketPath()
	if path == "" {
		t.Error("expected non-empty socket path")
	}
	if !strings.Contains(path, "qrush-") {
		t.Errorf("expected path to contain 'qrush-', got %q", path)
	}
}

func TestSocketPathFromEnv(t *testing.T) {
	t.Setenv("QRUSH_SOCKET", "/custom/path.sock")
	path := SocketPath()
	if path != "/custom/path.sock" {
		t.Errorf("expected '/custom/path.sock', got %q", path)
	}
}

func TestSocketPathFromLegacyEnv(t *testing.T) {
	t.Setenv("QRUSH_SOCKET", "")
	t.Setenv("TS_SOCKET", "/legacy/path.sock")
	path := SocketPath()
	if path != "/legacy/path.sock" {
		t.Errorf("expected '/legacy/path.sock', got %q", path)
	}
}

func TestUsername(t *testing.T) {
	orig := os.Getenv("USER")
	defer os.Setenv("USER", orig)

	os.Setenv("USER", "testuser")
	if got := username(); got != "testuser" {
		t.Errorf("expected 'testuser', got %q", got)
	}
}
