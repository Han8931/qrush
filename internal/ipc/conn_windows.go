//go:build windows

package ipc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Windows has no unix sockets we can rely on, so the daemon listens on
// localhost TCP — which any local user can reach. Authentication comes from
// the port file: it is written with mode 0600 and holds "addr\ntoken\n", and
// every connection must present the token before any protocol traffic. Only
// users able to read the owner's files can produce it.

const tokenHexLen = 32

type WindowsListener struct {
	listener  net.Listener
	portFile  string
	token     []byte
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func NewListener(path string) (*WindowsListener, error) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	var b [tokenHexLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		inner.Close()
		return nil, err
	}
	token := hex.EncodeToString(b[:])

	addr := inner.Addr().String()
	if err := os.WriteFile(path, []byte(addr+"\n"+token+"\n"), 0600); err != nil {
		inner.Close()
		return nil, err
	}

	l := &WindowsListener{
		listener: inner,
		portFile: path,
		token:    []byte(token),
		conns:    make(chan net.Conn),
		done:     make(chan struct{}),
	}
	go l.acceptLoop()
	return l, nil
}

// acceptLoop authenticates each connection in its own goroutine, so a client
// that connects and then stalls can't block other connections.
func (l *WindowsListener) acceptLoop() {
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			return
		}
		go l.authenticate(conn)
	}
}

func (l *WindowsListener) authenticate(conn net.Conn) {
	buf := make([]byte, tokenHexLen)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if subtle.ConstantTimeCompare(buf, l.token) != 1 {
		conn.Close()
		return
	}
	select {
	case l.conns <- conn:
	case <-l.done:
		conn.Close()
	}
}

func (l *WindowsListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, errors.New("listener closed")
	}
}

func (l *WindowsListener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.done)
		os.Remove(l.portFile)
		err = l.listener.Close()
	})
	return err
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
	addr, token, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	conn, err := net.DialTimeout("tcp", strings.TrimSpace(addr), 5*time.Second)
	if err != nil {
		return nil, err
	}
	// Present the token before any protocol traffic. A legacy port file
	// without one sends nothing (and a token-checking daemon rejects it).
	if token = strings.TrimSpace(token); token != "" {
		if _, err := conn.Write([]byte(token)); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}
