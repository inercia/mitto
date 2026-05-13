package web

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

// setTempAppDir redirects appdir to a fresh temp directory and resets the cache.
func setTempAppDir(t *testing.T) {
	t.Helper()
	t.Setenv("MITTO_DIR", t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
}

// discardLogger returns a logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestACPPIDDir(t *testing.T) {
	setTempAppDir(t)

	dir, err := acpPIDDir()
	if err != nil {
		t.Fatalf("acpPIDDir() error: %v", err)
	}

	if !strings.HasSuffix(dir, "acp_pids") {
		t.Errorf("acpPIDDir() = %q, want suffix 'acp_pids'", dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("acpPIDDir() path is not a directory: %q", dir)
	}
}

func TestPidFileName(t *testing.T) {
	if got := pidFileName("abc-123", false); got != "abc-123.pid" {
		t.Errorf("pidFileName(false) = %q, want %q", got, "abc-123.pid")
	}
	if got := pidFileName("abc-123", true); got != "abc-123.aux.pid" {
		t.Errorf("pidFileName(true) = %q, want %q", got, "abc-123.aux.pid")
	}
}

func TestWriteAndRemoveACPPIDFile(t *testing.T) {
	setTempAppDir(t)

	for _, isAux := range []bool{false, true} {
		suffix := ".pid"
		if isAux {
			suffix = ".aux.pid"
		}

		uuid := "test-workspace-uuid"
		pid := 12345

		if err := writeACPPIDFile(uuid, pid, isAux); err != nil {
			t.Fatalf("writeACPPIDFile(isAux=%v) error: %v", isAux, err)
		}

		dir, _ := acpPIDDir()
		path := filepath.Join(dir, uuid+suffix)

		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("PID file not created at %q: %v", path, err)
		}
		got, _ := strconv.Atoi(strings.TrimSpace(string(content)))
		if got != pid {
			t.Errorf("PID file content = %d, want %d", got, pid)
		}

		if err := removeACPPIDFile(uuid, isAux); err != nil {
			t.Fatalf("removeACPPIDFile(isAux=%v) error: %v", isAux, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("PID file still exists after removal: %q", path)
		}
	}
}

func TestRemoveACPPIDFile_NonExistent(t *testing.T) {
	setTempAppDir(t)

	if err := removeACPPIDFile("nonexistent-uuid", false); err != nil {
		t.Errorf("removeACPPIDFile on nonexistent file returned error: %v", err)
	}
}

func TestCleanupOrphanedACPProcesses_EmptyDir(t *testing.T) {
	setTempAppDir(t)
	// Should complete without error even with an empty directory.
	cleanupOrphanedACPProcesses(discardLogger())
}

func TestCleanupOrphanedACPProcesses_StaleFiles(t *testing.T) {
	setTempAppDir(t)

	// Write PID files with PIDs that certainly don't exist.
	for _, uuid := range []string{"stale-ws-1", "stale-ws-2"} {
		if err := writeACPPIDFile(uuid, 999999999, false); err != nil {
			t.Fatalf("writeACPPIDFile: %v", err)
		}
	}

	cleanupOrphanedACPProcesses(discardLogger())

	dir, _ := acpPIDDir()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected all stale PID files removed, got %d remaining", len(entries))
	}
}

func TestCleanupOrphanedACPProcesses_InvalidContent(t *testing.T) {
	setTempAppDir(t)

	dir, _ := acpPIDDir()
	path := filepath.Join(dir, "bad-content.pid")
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cleanupOrphanedACPProcesses(discardLogger())

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("invalid PID file not removed after cleanup")
	}
}

func TestCleanupOrphanedACPProcesses_AliveProcess(t *testing.T) {
	setTempAppDir(t)

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start subprocess: %v", err)
	}
	defer cmd.Process.Kill() // safety net

	if err := writeACPPIDFile("alive-ws", cmd.Process.Pid, false); err != nil {
		t.Fatalf("writeACPPIDFile: %v", err)
	}

	cleanupOrphanedACPProcesses(discardLogger())

	// PID file should be removed.
	dir, _ := acpPIDDir()
	path := filepath.Join(dir, "alive-ws.pid")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after cleanup of alive process")
	}

	// The subprocess should have been killed.
	err := cmd.Wait()
	if err == nil {
		t.Errorf("expected subprocess to be killed, but Wait() returned nil")
	}
}
