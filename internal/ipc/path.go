package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/han/qrush/internal/config"
)

func SocketPath() string {
	// config.Load resolves the socket through every layer: the QRUSH_SOCKET /
	// TS_SOCKET environment variables win over a `socket` config-file entry.
	if p := config.Load().Socket; p != "" {
		return p
	}

	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}

	if runtime.GOOS == "windows" {
		legacyPath := filepath.Join(tmpDir, "ru-port."+username())
		if legacySocketActive(legacyPath) {
			return legacyPath
		}
		return filepath.Join(tmpDir, "qrush-port."+username())
	}

	legacyPath := filepath.Join(tmpDir, fmt.Sprintf("ru-socket.%d", os.Getuid()))
	if legacySocketActive(legacyPath) {
		return legacyPath
	}
	return filepath.Join(tmpDir, fmt.Sprintf("qrush-socket.%d", os.Getuid()))
}

func legacySocketActive(path string) bool {
	if runtime.GOOS == "windows" {
		_, err := os.Stat(path)
		return err == nil
	}
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func username() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "default"
}
