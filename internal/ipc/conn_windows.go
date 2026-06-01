//go:build windows

package ipc

import (
	"net"
	"os"
	"strings"
	"time"
)

type WindowsListener struct {
	listener net.Listener
	portFile string
}

func NewListener(path string) (*WindowsListener, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	addr := l.Addr().String()
	os.WriteFile(path, []byte(addr), 0600)

	return &WindowsListener{listener: l, portFile: path}, nil
}

func (l *WindowsListener) Accept() (net.Conn, error) {
	return l.listener.Accept()
}

func (l *WindowsListener) Close() error {
	os.Remove(l.portFile)
	return l.listener.Close()
}

func (l *WindowsListener) Addr() string {
	return l.listener.Addr().String()
}

type WindowsDialer struct {
	path string
}

func NewDialer(path string) *WindowsDialer {
	return &WindowsDialer{path: path}
}

func (d *WindowsDialer) Dial() (net.Conn, error) {
	data, err := os.ReadFile(d.path)
	if err != nil {
		return nil, err
	}
	addr := strings.TrimSpace(string(data))
	return net.DialTimeout("tcp", addr, 5*time.Second)
}
