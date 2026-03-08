//go:build windows

package acp

import (
	"os"
	"syscall"
)

// newProcessGroupSysProcAttr returns nil on Windows since Setpgid is not
// supported. The caller should check for nil before assigning to
// cmd.SysProcAttr.
func newProcessGroupSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// KillProcessGroup performs a best-effort kill of the process with the given
// PID on Windows. Process group management is not supported, so only the
// direct process is killed.
func KillProcessGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
