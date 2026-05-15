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

// mittoPID is this process's own PID, captured once at startup.
// It is written into every ACP PID file so that cleanup can verify
// a PID file belongs to a *different* (presumably dead) Mitto instance
// before killing anything. This prevents a second running Mitto instance
// from killing ACP processes owned by a first running instance.
var mittoPID = os.Getpid()

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
// The file contains two lines: the Mitto server PID (owner) and the ACP process PID.
// The Mitto PID is used by cleanupOrphanedACPProcesses to skip files that belong to
// a currently-running Mitto instance (preventing cross-instance process killing).
func writeACPPIDFile(workspaceUUID string, pid int, isAux bool) error {
	dir, err := acpPIDDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, pidFileName(workspaceUUID, isAux))
	// Format: "mitto=<mitto_pid>\nacp=<acp_pid>\n"
	// Two lines so the format is self-documenting and parseable.
	content := fmt.Sprintf("mitto=%d\nacp=%d\n", mittoPID, pid)
	return os.WriteFile(path, []byte(content), 0644)
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

// pidFileContent holds the parsed content of an ACP PID file.
type pidFileContent struct {
	mittoPID int // PID of the Mitto server that owns this ACP process (0 = unknown/old format)
	acpPID   int // PID of the ACP subprocess
}

// parseACPPIDFile parses the content of an ACP PID file.
// Supports two formats:
//   - New: "mitto=<mitto_pid>\nacp=<acp_pid>\n"
//   - Old (backwards compat): plain "<pid>\n"
func parseACPPIDFile(content string) (pidFileContent, error) {
	content = strings.TrimSpace(content)
	lines := strings.Split(content, "\n")

	// New format: two key=value lines
	if len(lines) >= 2 && strings.HasPrefix(lines[0], "mitto=") {
		var result pidFileContent
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				return pidFileContent{}, fmt.Errorf("invalid key=value line: %q", line)
			}
			val, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return pidFileContent{}, fmt.Errorf("invalid PID value in line %q: %w", line, err)
			}
			switch parts[0] {
			case "mitto":
				result.mittoPID = val
			case "acp":
				result.acpPID = val
			}
		}
		if result.acpPID == 0 {
			return pidFileContent{}, fmt.Errorf("missing acp= line in PID file")
		}
		return result, nil
	}

	// Old format: plain PID number
	pid, err := strconv.Atoi(content)
	if err != nil {
		return pidFileContent{}, fmt.Errorf("not a valid PID: %w", err)
	}
	return pidFileContent{acpPID: pid}, nil
}

// cleanupOrphanedACPProcesses checks the acp_pids/ directory for PID files from a
// previous Mitto instance. Any alive processes are killed; all PID files are removed.
//
// Safety: files whose Mitto owner PID is still alive are skipped — they belong to
// a concurrently-running Mitto instance, not to a crashed one.
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

		pids, err := parseACPPIDFile(string(content))
		if err != nil {
			if logger != nil {
				logger.Warn("cleanupOrphanedACPProcesses: invalid PID file content",
					"file", entry.Name(), "error", err)
			}
			_ = os.Remove(path)
			continue
		}

		// If the Mitto owner PID is known and still running, this file belongs to
		// a concurrent Mitto instance — do NOT touch its ACP process.
		if pids.mittoPID != 0 && pids.mittoPID != mittoPID {
			if syscall.Kill(pids.mittoPID, 0) == nil {
				// Owner Mitto is alive → skip
				if logger != nil {
					logger.Info("cleanupOrphanedACPProcesses: skipping ACP process owned by running Mitto instance",
						"mitto_pid", pids.mittoPID, "acp_pid", pids.acpPID, "file", entry.Name())
				}
				continue
			}
		}

		// Owner Mitto is gone (or PID was 0 / old format). Check whether the ACP
		// process is still alive and, if so, kill it.
		if err := syscall.Kill(pids.acpPID, 0); err == nil {
			// Process is alive — kill the entire process group
			if logger != nil {
				logger.Warn("cleanupOrphanedACPProcesses: killing orphaned ACP process group",
					"acp_pid", pids.acpPID, "file", entry.Name())
			}
			mittoAcp.KillProcessGroup(pids.acpPID)
		} else {
			if logger != nil {
				logger.Info("cleanupOrphanedACPProcesses: stale PID file (process already gone)",
					"acp_pid", pids.acpPID, "file", entry.Name())
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
