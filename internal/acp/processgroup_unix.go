//go:build !windows

package acp

import (
	"os"
	"syscall"
)

// newProcessGroupSysProcAttr returns SysProcAttr configured to create a new
// process group on Unix systems. This allows killing all child processes
// at once via the negative-PID group kill.
func newProcessGroupSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// KillProcessGroup kills the entire process group for the given PID.
// Falls back to killing just the process if the group cannot be determined.
func KillProcessGroup(pid int) {
	pgid, err := syscall.Getpgid(pid)
	if err == nil {
		// Kill the entire process group (negative PID signals the group)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		// Fallback: kill just the direct process
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
}
