//go:build windows

package tui

import (
	"fmt"
	"os/exec"
)

func terminateShellProcess(pid int) {
	if pid > 0 {
		_ = exec.Command("taskkill", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
	}
}

func forceKillShellProcess(pid int) {
	if pid > 0 {
		_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
	}
}
