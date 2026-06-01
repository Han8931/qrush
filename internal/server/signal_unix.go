//go:build !windows

package server

import (
	"os/signal"
	"syscall"
)

func IgnoreSIGPIPE() {
	signal.Ignore(syscall.SIGPIPE)
}
