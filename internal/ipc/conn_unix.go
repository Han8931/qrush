//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"time"
)

type UnixListener struct {
	listener *net.UnixListener
	path     string
}

func NewListener(path string) (*UnixListener, error) {
	if _, err := os.Stat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("server already running on %s", path)
		}
		os.Remove(path)
	}

	addr := &net.UnixAddr{Name: path, Net: "unix"}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, err
	}
	return &UnixListener{listener: l, path: path}, nil
}

func (l *UnixListener) Accept() (net.Conn, error) {
	return l.listener.Accept()
}

func (l *UnixListener) Close() error {
	err := l.listener.Close()
	os.Remove(l.path)
	return err
}

func (l *UnixListener) Addr() string {
	return l.path
}

type UnixDialer struct {
	path string
}

func NewDialer(path string) *UnixDialer {
	return &UnixDialer{path: path}
}

func (d *UnixDialer) Dial() (net.Conn, error) {
	addr := &net.UnixAddr{Name: d.path, Net: "unix"}
	return net.DialUnix("unix", nil, addr)
}
