//go:build !windows

package client

import (
	"os/exec"
	"syscall"
)

func setDaemonProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
}
