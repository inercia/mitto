package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/git"
	"github.com/inercia/mitto/internal/session"
)

// setupWorktreeRecoveryEnv points appdir at a temp dir with hermetic git
// identity and returns a fresh store. In-project worktree roots are created
// per-repo by the caller via appdir.WorkspaceWorktreesDir.
func setupWorktreeRecoveryEnv(t *testing.T) *session.Store {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not found in PATH")
	}
	t.Setenv("MITTO_DIR", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@test")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@test")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sessionsDir, err := appdir.SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir: %v", err)
	}
	store, err := session.NewStore(sessionsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGitT(t, dir, "add", "README.md")
	runGitT(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", "init")
	return dir
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "branch", "--list", branch).Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	return strings.TrimSpace(string(out)) != ""
}

func TestRecoverOrphanedWorktrees(t *testing.T) {
	store := setupWorktreeRecoveryEnv(t)
	repo := initTestRepo(t)
	// Worktrees live in-project under <repo>/.mitto/worktrees/<session-id>.
	worktreesDir := appdir.WorkspaceWorktreesDir(repo)
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	ctx := context.Background()

	// Valid session with a live worktree (must be preserved).
	validID := "20260614-000001-valid000"
	validWT := filepath.Join(worktreesDir, validID)
	if err := git.AddWorktree(ctx, repo, validWT, git.BranchName(validID), ""); err != nil {
		t.Fatalf("AddWorktree valid: %v", err)
	}
	if err := store.Create(session.Metadata{
		SessionID:      validID,
		WorkingDir:     validWT,
		WorktreePath:   validWT,
		WorktreeBranch: git.BranchName(validID),
	}); err != nil {
		t.Fatalf("store.Create valid: %v", err)
	}

	// Orphaned worktree (real git worktree, no session) — must be removed.
	orphanID := "20260614-000002-orphan00"
	orphanWT := filepath.Join(worktreesDir, orphanID)
	if err := git.AddWorktree(ctx, repo, orphanWT, git.BranchName(orphanID), ""); err != nil {
		t.Fatalf("AddWorktree orphan: %v", err)
	}

	// Stray non-git dir (no session) — must be removed directly.
	strayWT := filepath.Join(worktreesDir, "20260614-000003-stray000")
	if err := os.MkdirAll(strayWT, 0o755); err != nil {
		t.Fatalf("mkdir stray: %v", err)
	}

	recoverOrphanedWorktrees(store, nil)

	if _, err := os.Stat(validWT); err != nil {
		t.Errorf("valid worktree was removed: %v", err)
	}
	if _, err := os.Stat(orphanWT); !os.IsNotExist(err) {
		t.Errorf("orphan worktree still exists: err=%v", err)
	}
	if branchExists(t, repo, git.BranchName(orphanID)) {
		t.Errorf("orphan branch %q was not deleted", git.BranchName(orphanID))
	}
	if _, err := os.Stat(strayWT); !os.IsNotExist(err) {
		t.Errorf("stray dir still exists: err=%v", err)
	}
}

func TestRecoverOrphanedWorktrees_ClearsStaleMetadata(t *testing.T) {
	store := setupWorktreeRecoveryEnv(t)

	sid := "20260614-000004-stale000"
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	if err := store.Create(session.Metadata{
		SessionID:      sid,
		WorktreePath:   gone,
		WorktreeBranch: git.BranchName(sid),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	recoverOrphanedWorktrees(store, nil)

	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.WorktreePath != "" || meta.WorktreeBranch != "" {
		t.Errorf("stale metadata not cleared: path=%q branch=%q", meta.WorktreePath, meta.WorktreeBranch)
	}
}
