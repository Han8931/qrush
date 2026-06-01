//go:build !windows

package server

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/han/qrush/internal/protocol"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}

func forceKillProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

func fillSignalInfo(result *protocol.Result, ps *os.ProcessState) {
	if ps == nil {
		return
	}
	if status, ok := ps.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			result.DiedBySignal = true
			result.Signal = int(status.Signal())
		}
	}
}
