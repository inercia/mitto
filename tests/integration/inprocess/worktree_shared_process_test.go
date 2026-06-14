//go:build integration

package inprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/client"
)

// TestWorktreeSharedProcess verifies that two sessions whose working directories
// differ (the registered workspace root and a subdirectory inside it) share a
// SINGLE shared ACP process, while each session keeps its own per-session cwd.
//
// This is the git-agnostic plumbing for the worktree epic: the shared process is
// keyed by the owning workspace UUID (repo root), but the per-session cwd flows
// independently to session/new.
func TestWorktreeSharedProcess(t *testing.T) {
	ts := SetupTestServer(t)

	// The default workspace dir is <tmp>/workspace (see SetupTestServer).
	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	subDir := filepath.Join(workspaceDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	// Session A: working dir == registered workspace root (exact match).
	sessA, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "A",
		WorkingDir: workspaceDir,
	})
	if err != nil {
		t.Fatalf("CreateSession A failed: %v", err)
	}

	// Session B: working dir == subdirectory of the workspace (owned, not exact).
	sessB, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name:       "B",
		WorkingDir: subDir,
	})
	if err != nil {
		t.Fatalf("CreateSession B failed: %v", err)
	}

	// Both sessions must share ONE shared ACP process. The shared process is
	// created eagerly at session-create time (getSharedProcess), so no prompt is
	// needed for this assertion.
	if count := ts.Server.GetSessionManager().ACPProcessCount(); count != 1 {
		t.Fatalf("expected 1 shared ACP process, got %d", count)
	}

	// Each session must keep its own per-session cwd: A at the workspace root,
	// B at the subdirectory. This proves the per-session cwd is decoupled from
	// the workspace identity even though they share a process.
	bsA := ts.Server.GetSessionManager().GetSession(sessA.SessionID)
	bsB := ts.Server.GetSessionManager().GetSession(sessB.SessionID)
	if bsA == nil || bsB == nil {
		t.Fatalf("expected both background sessions to exist (A=%v, B=%v)", bsA != nil, bsB != nil)
	}

	if got := bsA.GetWorkingDir(); got != workspaceDir {
		t.Errorf("session A working dir = %q, want %q", got, workspaceDir)
	}
	if got := bsB.GetWorkingDir(); got != subDir {
		t.Errorf("session B working dir = %q, want %q", got, subDir)
	}
	if bsA.GetWorkingDir() == bsB.GetWorkingDir() {
		t.Errorf("expected distinct per-session working dirs, both = %q", bsA.GetWorkingDir())
	}
}
