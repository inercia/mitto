package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCommonDir(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)

	// Non-repo returns "".
	if got := CommonDir(t.TempDir()); got != "" {
		t.Errorf("CommonDir(non-repo) = %q, want \"\"", got)
	}

	// Regular checkout: common dir is <repo>/.git, under the repo.
	repoReal, _ := filepath.EvalSymlinks(repo)
	common := CommonDir(repo)
	if common == "" {
		t.Fatal("CommonDir(repo) returned empty")
	}
	commonReal, _ := filepath.EvalSymlinks(common)
	wantRepoGit, _ := filepath.EvalSymlinks(filepath.Join(repoReal, ".git"))
	if commonReal != wantRepoGit {
		t.Errorf("CommonDir(repo) = %q, want %q", commonReal, wantRepoGit)
	}

	// Linked worktree: common dir is the MAIN repo's .git, OUTSIDE the worktree.
	worktreePath := filepath.Join(t.TempDir(), "wt")
	branch := BranchName("commondir-session")
	if err := AddWorktree(context.Background(), repo, worktreePath, branch); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	wtCommon := CommonDir(worktreePath)
	wtCommonReal, _ := filepath.EvalSymlinks(wtCommon)
	if wtCommonReal != wantRepoGit {
		t.Errorf("CommonDir(worktree) = %q, want main .git %q", wtCommonReal, wantRepoGit)
	}
	// It must be outside the worktree cwd (otherwise no extra allow-list entry needed).
	wtReal, _ := filepath.EvalSymlinks(worktreePath)
	if rel, err := filepath.Rel(wtReal, wtCommonReal); err == nil && !filepathEscapes(rel) {
		t.Errorf("CommonDir(worktree) %q unexpectedly inside worktree %q (rel=%q)", wtCommonReal, wtReal, rel)
	}
}

// filepathEscapes reports whether a relative path points outside its base.
func filepathEscapes(rel string) bool {
	return rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

func TestCurrentBranchAndCommit(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)

	// Non-repo returns "" for both.
	nonRepo := t.TempDir()
	if got := CurrentBranch(nonRepo); got != "" {
		t.Errorf("CurrentBranch(non-repo) = %q, want \"\"", got)
	}
	if got := CurrentCommit(nonRepo); got != "" {
		t.Errorf("CurrentCommit(non-repo) = %q, want \"\"", got)
	}

	// Regular checkout on main.
	if got := CurrentBranch(repo); got != "main" {
		t.Errorf("CurrentBranch(repo) = %q, want %q", got, "main")
	}
	commit := CurrentCommit(repo)
	if len(commit) != 40 {
		t.Errorf("CurrentCommit(repo) = %q, want 40-char SHA", commit)
	}

	// Detached HEAD returns "" for the branch but a valid commit.
	runGit(t, repo, "checkout", "--detach", "HEAD")
	if got := CurrentBranch(repo); got != "" {
		t.Errorf("CurrentBranch(detached) = %q, want \"\"", got)
	}
	if got := CurrentCommit(repo); got != commit {
		t.Errorf("CurrentCommit(detached) = %q, want %q", got, commit)
	}
}

func TestBranchName(t *testing.T) {
	if got := BranchName("20260614-abcd"); got != "mitto-20260614-abcd" {
		t.Errorf("BranchName = %q, want %q", got, "mitto-20260614-abcd")
	}
}

func TestEnsureGitignoredAndIsIgnored(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	const pattern = ".mitto/worktrees/"

	if IsIgnored(repo, pattern) {
		t.Fatal("path unexpectedly ignored before EnsureGitignored")
	}

	if err := EnsureGitignored(repo, pattern, "test comment"); err != nil {
		t.Fatalf("EnsureGitignored: %v", err)
	}
	if !IsIgnored(repo, pattern) {
		t.Error("path not ignored after EnsureGitignored")
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), pattern) {
		t.Errorf(".gitignore missing pattern: %q", string(data))
	}
	if !strings.Contains(string(data), "# test comment") {
		t.Errorf(".gitignore missing comment: %q", string(data))
	}

	// Idempotent: a second call must not duplicate the pattern.
	if err := EnsureGitignored(repo, pattern, "test comment"); err != nil {
		t.Fatalf("EnsureGitignored (2nd): %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if n := strings.Count(string(data2), pattern); n != 1 {
		t.Errorf("pattern count = %d, want 1: %q", n, string(data2))
	}
}

func TestEnsureGitignoredSkipsWhenAlreadyIgnored(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)

	// Pre-ignore the whole .mitto/ dir, mirroring repos that already ignore it.
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".mitto/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := EnsureGitignored(repo, ".mitto/worktrees/", "test comment"); err != nil {
		t.Fatalf("EnsureGitignored: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if strings.Contains(string(data), ".mitto/worktrees/") {
		t.Errorf("EnsureGitignored appended despite parent dir already ignored: %q", string(data))
	}
}
