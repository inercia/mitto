package web

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/appdir"
)

// acpPIDDir returns the directory used for ACP PID files, creating it if needed.
func acpPIDDir() (string, error) {
	base, err := appdir.Dir()
	if err != nil {
		return "", fmt.Errorf("failed to get app dir: %w", err)
	}
	dir := filepath.Join(base, "acp_pids")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create ACP PID dir: %w", err)
	}
	return dir, nil
}

// pidFileName returns the PID file name for a workspace UUID.
func pidFileName(workspaceUUID string, isAux bool) string {
	if isAux {
		return workspaceUUID + ".aux.pid"
	}
	return workspaceUUID + ".pid"
}

// writeACPPIDFile writes a PID file for the given workspace ACP process.
func writeACPPIDFile(workspaceUUID string, pid int, isAux bool) error {
	dir, err := acpPIDDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, pidFileName(workspaceUUID, isAux))
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0644)
}

// removeACPPIDFile removes the PID file for the given workspace ACP process.
// Silent if the file doesn't exist.
func removeACPPIDFile(workspaceUUID string, isAux bool) error {
	dir, err := acpPIDDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, pidFileName(workspaceUUID, isAux))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// cleanupOrphanedACPProcesses checks the acp_pids/ directory for PID files from a
// previous Mitto instance. Any alive processes are killed; all PID files are removed.
func cleanupOrphanedACPProcesses(logger *slog.Logger) {
	dir, err := acpPIDDir()
	if err != nil {
		if logger != nil {
			logger.Warn("cleanupOrphanedACPProcesses: failed to get PID dir", "error", err)
		}
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if logger != nil {
			logger.Warn("cleanupOrphanedACPProcesses: failed to read PID dir", "error", err)
		}
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}

		path := filepath.Join(dir, entry.Name())

		content, err := os.ReadFile(path)
		if err != nil {
			if logger != nil {
				logger.Warn("cleanupOrphanedACPProcesses: failed to read PID file",
					"file", entry.Name(), "error", err)
			}
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
		if err != nil {
			if logger != nil {
				logger.Warn("cleanupOrphanedACPProcesses: invalid PID in file",
					"file", entry.Name(), "error", err)
			}
			_ = os.Remove(path)
			continue
		}

		// Check if process is still alive (signal 0 = existence check)
		if err := syscall.Kill(pid, 0); err == nil {
			// Process is alive — kill the entire process group
			if logger != nil {
				logger.Warn("cleanupOrphanedACPProcesses: killing orphaned ACP process group",
					"pid", pid, "file", entry.Name())
			}
			mittoAcp.KillProcessGroup(pid)
		} else {
			if logger != nil {
				logger.Info("cleanupOrphanedACPProcesses: stale PID file (process already gone)",
					"pid", pid, "file", entry.Name())
			}
		}

		// Always remove the PID file
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			if logger != nil {
				logger.Warn("cleanupOrphanedACPProcesses: failed to remove PID file",
					"file", entry.Name(), "error", removeErr)
			}
		}
	}
}
