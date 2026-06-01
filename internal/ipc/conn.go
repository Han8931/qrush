package ipc

import "net"

type Listener interface {
	Accept() (net.Conn, error)
	Close() error
	Addr() string
}

type Dialer interface {
	Dial() (net.Conn, error)
}
