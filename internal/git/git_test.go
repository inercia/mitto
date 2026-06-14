package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitEnv returns a hermetic environment so git commands never touch the user's
// global or system config.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test",
	)
}

// runGit runs a git command in dir with the hermetic environment and fails the
// test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, string(out))
	}
}

// initRepo creates a fresh git repo with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", "init")
	return dir
}

// requireGit skips the test if the git binary is not available.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not found in PATH")
	}
}

func TestIsGitRepo(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	if !IsGitRepo(repo) {
		t.Errorf("IsGitRepo(%q) = false, want true", repo)
	}
	nonRepo := t.TempDir()
	if IsGitRepo(nonRepo) {
		t.Errorf("IsGitRepo(%q) = true, want false", nonRepo)
	}
}

func TestAddRemoveWorktree(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "wt")
	branch := BranchName("test-session")

	ctx := context.Background()
	if err := AddWorktree(ctx, repo, worktreePath, branch); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree path not created: %v", err)
	}
	if !IsGitRepo(worktreePath) {
		t.Errorf("IsGitRepo(%q) = false, want true", worktreePath)
	}

	if err := RemoveWorktree(ctx, repo, worktreePath); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree path still exists after remove: err=%v", err)
	}

	if err := DeleteBranch(ctx, repo, branch); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
}

func TestBranchName(t *testing.T) {
	if got := BranchName("20260614-abcd"); got != "mitto-20260614-abcd" {
		t.Errorf("BranchName = %q, want %q", got, "mitto-20260614-abcd")
	}
}
