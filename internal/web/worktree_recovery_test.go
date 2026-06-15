package web

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
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

func TestRecoverOrphanedWorktrees_PreservesUnmergedCommits(t *testing.T) {
	store := setupWorktreeRecoveryEnv(t)
	repo := initTestRepo(t)
	worktreesDir := appdir.WorkspaceWorktreesDir(repo)
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	ctx := context.Background()

	// Orphaned worktree with a commit ahead of main — must be PRESERVED.
	orphanID := "20260614-000010-ahead000"
	orphanWT := filepath.Join(worktreesDir, orphanID)
	if err := git.AddWorktree(ctx, repo, orphanWT, git.BranchName(orphanID), ""); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	// Commit a new file inside the orphan worktree so its branch is ahead.
	if err := os.WriteFile(filepath.Join(orphanWT, "work.txt"), []byte("unmerged\n"), 0o644); err != nil {
		t.Fatalf("write work.txt: %v", err)
	}
	runGitT(t, orphanWT, "add", "work.txt")
	runGitT(t, orphanWT, "-c", "commit.gpgsign=false", "commit", "-m", "unmerged work")

	recoverOrphanedWorktrees(store, nil)

	if _, err := os.Stat(orphanWT); err != nil {
		t.Errorf("orphan worktree with unmerged commit was removed: %v", err)
	}
	if !branchExists(t, repo, git.BranchName(orphanID)) {
		t.Errorf("orphan branch %q was deleted despite unmerged commits", git.BranchName(orphanID))
	}
}

func TestRecoverOrphanedWorktrees_PreservesDirtyWorktree(t *testing.T) {
	store := setupWorktreeRecoveryEnv(t)
	repo := initTestRepo(t)
	worktreesDir := appdir.WorkspaceWorktreesDir(repo)
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	ctx := context.Background()

	// Orphaned worktree with an uncommitted file — must be PRESERVED.
	orphanID := "20260614-000011-dirty000"
	orphanWT := filepath.Join(worktreesDir, orphanID)
	if err := git.AddWorktree(ctx, repo, orphanWT, git.BranchName(orphanID), ""); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	// Write an uncommitted file (dirty working tree).
	if err := os.WriteFile(filepath.Join(orphanWT, "dirty.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatalf("write dirty.txt: %v", err)
	}

	recoverOrphanedWorktrees(store, nil)

	if _, err := os.Stat(orphanWT); err != nil {
		t.Errorf("dirty orphan worktree was removed: %v", err)
	}
	if !branchExists(t, repo, git.BranchName(orphanID)) {
		t.Errorf("dirty orphan branch %q was deleted", git.BranchName(orphanID))
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

// stubWorktreeAddFn temporarily replaces the worktreeAddFn seam and restores it
// when the test finishes.
func stubWorktreeAddFn(t *testing.T, fn func(ctx context.Context, repoDir, worktreePath, branch, startPoint string) error) {
	t.Helper()
	orig := worktreeAddFn
	worktreeAddFn = fn
	t.Cleanup(func() { worktreeAddFn = orig })
}

// TestWorktreeAddFn_RetryThenSuccess verifies the bounded retry absorbs a
// transient index.lock failure: the first attempt fails, the second succeeds,
// and no error is returned.
func TestWorktreeAddFn_RetryThenSuccess(t *testing.T) {
	var calls int
	stubWorktreeAddFn(t, func(_ context.Context, _, _, _, _ string) error {
		calls++
		if calls < 2 {
			return errors.New("fatal: Unable to create index.lock: File exists")
		}
		return nil
	})

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	if err := sm.addWorktreeWithRetry("/repo", filepath.Join(t.TempDir(), "wt"), "mitto/x", ""); err != nil {
		t.Fatalf("addWorktreeWithRetry: unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("worktreeAddFn called %d times, want 2 (one transient failure absorbed)", calls)
	}
}

// TestWorktreeAddFn_ExhaustedFailure verifies that when worktree creation fails
// on every attempt, CreateSessionWithWorkspace surfaces ErrWorktreeCreationFailed
// instead of silently falling back to the shared working dir, retries the
// configured number of times, and registers no session.
func TestWorktreeAddFn_ExhaustedFailure(t *testing.T) {
	setupWorktreeRecoveryEnv(t) // hermetic git identity + MITTO_DIR
	repo := initTestRepo(t)

	var calls int
	stubWorktreeAddFn(t, func(_ context.Context, _, _, _, _ string) error {
		calls++
		return errors.New("fatal: Unable to create index.lock: File exists")
	})

	ws := &config.WorkspaceSettings{ACPServer: "mock-acp", WorkingDir: repo}
	ws.EnsureUUID()
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{*ws},
	})

	_, err := sm.CreateSessionWithWorkspace(context.Background(), "wt", repo, ws)
	if !errors.Is(err, ErrWorktreeCreationFailed) {
		t.Fatalf("CreateSessionWithWorkspace error = %v, want ErrWorktreeCreationFailed", err)
	}
	if calls != maxWorktreeAddAttempts {
		t.Errorf("worktreeAddFn called %d times, want %d", calls, maxWorktreeAddAttempts)
	}
	if n := sm.SessionCount(); n != 0 {
		t.Errorf("SessionCount = %d, want 0 (no session registered on worktree failure)", n)
	}
}
