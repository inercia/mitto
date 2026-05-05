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
//
// When we create child processes with Setpgid: true, the PGID equals the
// child's PID. We first try Getpgid() to discover the actual PGID, but if
// that fails (e.g., the group leader was already killed by exec.CommandContext
// cancellation before we get here), we fall back to Kill(-pid) directly.
// Sending a signal to a negative PID targets the process GROUP, which works
// as long as any process in the group is still alive — even if the group
// leader (whose PID == PGID due to Setpgid) is already dead.
func KillProcessGroup(pid int) {
	pgid, err := syscall.Getpgid(pid)
	if err == nil {
		// Kill the entire process group (negative PID signals the group)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}

	// Getpgid failed — the group leader process is likely already dead
	// (e.g., killed by exec.CommandContext context cancellation).
	// Since we set Setpgid: true, PGID == PID, so try killing the group
	// directly. This works as long as any process in the group is alive.
	if syscall.Kill(-pid, syscall.SIGKILL) == nil {
		return
	}

	// Last resort: kill just the direct process
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
