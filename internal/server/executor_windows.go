//go:build windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/han/qrush/internal/protocol"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killProcessGroup(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	if err := kill.Run(); err != nil {
		return p.Kill()
	}
	return nil
}

// forceKillProcessGroup is an alias on Windows: taskkill /F is already forceful.
func forceKillProcessGroup(pid int) error {
	return killProcessGroup(pid)
}

func fillSignalInfo(result *protocol.Result, ps *os.ProcessState) {
	if ps == nil {
		return
	}
	if result.ExitCode < 0 || result.ExitCode > 128 {
		result.DiedBySignal = true
		result.Signal = result.ExitCode
	}
}
