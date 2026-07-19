package client

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/han/qrush/internal/ipc"
	"github.com/han/qrush/internal/protocol"
)

// shortSocketPath returns a socket path in a short temp dir (t.TempDir embeds
// the test name, which can exceed the 104-byte sun_path limit on macOS).
func shortSocketPath(t *testing.T) string {
	dir, err := os.MkdirTemp("", "qrush")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// fakeDaemon listens on sock and answers MsgGetVersion with version.
func fakeDaemon(t *testing.T, sock string, version int) {
	t.Helper()
	l, err := ipc.NewListener(sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					msg, err := protocol.Recv(c)
					if err != nil {
						return
					}
					if msg.Type == protocol.MsgGetVersion {
						_ = protocol.Send(c, &protocol.Msg{
							Type:    protocol.MsgVersion,
							Payload: protocol.PayloadVersion{Version: version},
						})
					}
				}
			}(conn)
		}
	}()
}

func isolateEnv(t *testing.T, sock string) {
	t.Helper()
	t.Setenv("QRUSH_SOCKET", sock)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// A daemon speaking an older protocol must be refused — not killed. It may be
// running jobs and hosting live panes; only the user decides when those die.
func TestEnsureServerRefusesVersionMismatch(t *testing.T) {
	sock := shortSocketPath(t)
	isolateEnv(t, sock)
	fakeDaemon(t, sock, 1)

	err := EnsureServer()
	if err == nil {
		t.Fatal("expected an error on protocol mismatch")
	}
	if !strings.Contains(err.Error(), "protocol v1") || !strings.Contains(err.Error(), "ru -K") {
		t.Errorf("error should name the versions and the remedy, got %q", err)
	}

	// The old daemon must still be alive afterwards.
	conn, dErr := ipc.NewDialer(sock).Dial()
	if dErr != nil {
		t.Fatalf("mismatched daemon should not have been killed: %v", dErr)
	}
	conn.Close()
}

func TestEnsureServerAcceptsMatchingVersion(t *testing.T) {
	sock := shortSocketPath(t)
	isolateEnv(t, sock)
	fakeDaemon(t, sock, protocol.ProtocolVersion)

	if err := EnsureServer(); err != nil {
		t.Fatalf("expected matching daemon to be accepted, got %v", err)
	}
}
