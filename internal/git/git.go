// Package git provides small helpers around the git CLI for managing
// per-session worktrees. It shells out to the git binary using the same
// `git -C <dir> ...` convention used elsewhere in the codebase and depends
// on no external libraries.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IsGitRepo reports whether dir is inside a git working tree.
// It swallows any error (missing git, not a repo, etc.) and returns false.
func IsGitRepo(dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// AddWorktree creates a new worktree at worktreePath on a new branch.
// It runs `git -C repoDir worktree add worktreePath -b branch`.
func AddWorktree(ctx context.Context, repoDir, worktreePath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "add", worktreePath, "-b", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveWorktree force-removes the worktree at worktreePath.
// It runs `git -C repoDir worktree remove -f worktreePath`.
func RemoveWorktree(ctx context.Context, repoDir, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "remove", "-f", worktreePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteBranch force-deletes the given branch.
// It runs `git -C repoDir branch -D branch`.
func DeleteBranch(ctx context.Context, repoDir, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "branch", "-D", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch -D: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CommonDir returns the absolute path to the git common directory for dir
// (the main repository's .git, shared by all linked worktrees). For a linked
// worktree this is <main-repo>/.git; for a regular checkout it is <repo>/.git.
//
// It is used to grant a restricted runner write access to the shared git
// metadata (objects, refs, and the per-worktree gitdir at
// <main>/.git/worktrees/<name>), which lives outside a worktree's cwd.
//
// It swallows any error (missing git, not a repo, etc.) and returns "".
func CommonDir(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BranchName returns the worktree branch name for a session ID.
func BranchName(sessionID string) string {
	return fmt.Sprintf("mitto-%s", sessionID)
}

// CurrentBranch returns the name of the branch currently checked out in dir,
// or "" when dir is in a detached HEAD state (or on any error). This is used to
// record the base branch a session worktree was created from.
func CurrentBranch(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return "" // detached HEAD
	}
	return branch
}

// CurrentCommit returns the full SHA of the commit currently checked out in dir,
// or "" on any error. This is used to record the base commit a session worktree
// was created from.
func CurrentCommit(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IsIgnored reports whether relPath (relative to repoDir) is ignored by git in
// repoDir, honoring .gitignore files, global excludes, and so on. It returns
// false on any error (missing git, not a repo).
func IsIgnored(repoDir, relPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// `git check-ignore -q <path>` exits 0 when ignored, 1 when not ignored.
	return exec.CommandContext(ctx, "git", "-C", repoDir, "check-ignore", "-q", relPath).Run() == nil
}

// EnsureGitignored makes a best-effort, idempotent attempt to ensure pattern is
// ignored in repoDir. If git already ignores pattern (via any .gitignore,
// global excludes, etc.) it does nothing; otherwise it appends pattern to
// <repoDir>/.gitignore, preceded by a labeled comment line. The file is created
// if needed and a separating newline is ensured before appending.
func EnsureGitignored(repoDir, pattern, comment string) error {
	if IsIgnored(repoDir, pattern) {
		return nil
	}
	path := filepath.Join(repoDir, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil // already listed verbatim
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	var b strings.Builder
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	if comment != "" {
		b.WriteString("# " + comment + "\n")
	}
	b.WriteString(pattern + "\n")
	_, err = f.WriteString(b.String())
	return err
}
