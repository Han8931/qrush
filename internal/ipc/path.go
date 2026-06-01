package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func SocketPath() string {
	if p := os.Getenv("QRUSH_SOCKET"); p != "" {
		return p
	}
	if p := os.Getenv("TS_SOCKET"); p != "" {
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
