package acpproc

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
		acpPid := 12345

		if err := writeACPPIDFile(uuid, acpPid, isAux); err != nil {
			t.Fatalf("writeACPPIDFile(isAux=%v) error: %v", isAux, err)
		}

		dir, _ := acpPIDDir()
		path := filepath.Join(dir, uuid+suffix)

		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("PID file not created at %q: %v", path, err)
		}

		// Parse new "mitto=<N>\nacp=<N>\n" format
		pids, err := parseACPPIDFile(string(content))
		if err != nil {
			t.Fatalf("parseACPPIDFile: %v (content=%q)", err, string(content))
		}
		if pids.acpPID != acpPid {
			t.Errorf("acp PID in file = %d, want %d", pids.acpPID, acpPid)
		}
		if pids.mittoPID != mittoPID {
			t.Errorf("mitto PID in file = %d, want %d (current process)", pids.mittoPID, mittoPID)
		}

		if err := removeACPPIDFile(uuid, isAux); err != nil {
			t.Fatalf("removeACPPIDFile(isAux=%v) error: %v", isAux, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("PID file still exists after removal: %q", path)
		}
	}
}

func TestParseACPPIDFile(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantMitto int
		wantACP   int
		wantErr   bool
	}{
		{
			name:      "new format",
			content:   "mitto=100\nacp=200\n",
			wantMitto: 100,
			wantACP:   200,
		},
		{
			name:      "new format no trailing newline",
			content:   "mitto=100\nacp=200",
			wantMitto: 100,
			wantACP:   200,
		},
		{
			name:    "old format plain PID",
			content: "12345\n",
			wantACP: 12345,
		},
		{
			name:    "old format no newline",
			content: "12345",
			wantACP: 12345,
		},
		{
			name:    "invalid content",
			content: "not-a-pid",
			wantErr: true,
		},
		{
			name:    "missing acp line",
			content: "mitto=100\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseACPPIDFile(tc.content)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseACPPIDFile() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if got.mittoPID != tc.wantMitto {
				t.Errorf("mittoPID = %d, want %d", got.mittoPID, tc.wantMitto)
			}
			if got.acpPID != tc.wantACP {
				t.Errorf("acpPID = %d, want %d", got.acpPID, tc.wantACP)
			}
		})
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

// TestCleanupOrphanedACPProcesses_SkipsLiveMittoOwner verifies that cleanup does NOT
// kill an ACP process whose Mitto owner is still alive (concurrent Mitto instance).
func TestCleanupOrphanedACPProcesses_SkipsLiveMittoOwner(t *testing.T) {
	setTempAppDir(t)

	// Start an "ACP" process to act as the target.
	acpCmd := exec.Command("sleep", "60")
	acpCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := acpCmd.Start(); err != nil {
		t.Fatalf("failed to start ACP subprocess: %v", err)
	}
	defer acpCmd.Process.Kill() // safety net

	// Start a second "Mitto" process to act as the owner.
	ownerCmd := exec.Command("sleep", "60")
	if err := ownerCmd.Start(); err != nil {
		acpCmd.Process.Kill()
		t.Fatalf("failed to start owner Mitto subprocess: %v", err)
	}
	defer ownerCmd.Process.Kill()

	// Write a PID file as if the owner Mitto instance wrote it.
	dir, _ := acpPIDDir()
	path := filepath.Join(dir, "cross-instance-ws.pid")
	content := fmt.Sprintf("mitto=%d\nacp=%d\n", ownerCmd.Process.Pid, acpCmd.Process.Pid)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Cleanup should skip this file because the owner Mitto is alive.
	cleanupOrphanedACPProcesses(discardLogger())

	// PID file should still exist (not removed by cleanup).
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("PID file was removed — cleanup should have skipped it (owner Mitto still running)")
	}

	// ACP process should still be alive.
	if err := syscall.Kill(acpCmd.Process.Pid, 0); err != nil {
		t.Errorf("ACP process was killed — cleanup should have skipped it (owner Mitto still running)")
	}

	// Clean up manually.
	ownerCmd.Process.Kill()
	acpCmd.Process.Kill()
	_ = os.Remove(path)
}

func TestCleanupOrphanedACPProcesses_AliveProcess(t *testing.T) {
	setTempAppDir(t)

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start subprocess: %v", err)
	}
	defer cmd.Process.Kill() // always clean up

	if err := writeACPPIDFile("alive-ws", cmd.Process.Pid, false); err != nil {
		t.Fatalf("writeACPPIDFile: %v", err)
	}

	cleanupOrphanedACPProcesses(discardLogger())

	// PID file should be removed.
	dir, _ := acpPIDDir()
	path := filepath.Join(dir, "alive-ws.pid")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after cleanup")
	}

	// The subprocess should NOT have been killed — cleanup only removes PID files,
	// it never sends signals (to avoid PID-reuse false positives).
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Errorf("subprocess was killed by cleanup but should have been left alive: %v", err)
	}
}
