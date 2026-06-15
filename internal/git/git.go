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
// When startPoint is non-empty the new branch is created from it (e.g.
// "origin/main"); otherwise it is created from the repo's current HEAD.
// It runs `git -C repoDir worktree add worktreePath -b branch [startPoint]`.
func AddWorktree(ctx context.Context, repoDir, worktreePath, branch, startPoint string) error {
	args := []string{"-C", repoDir, "worktree", "add", worktreePath, "-b", branch}
	if startPoint != "" {
		args = append(args, startPoint)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
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
	return CommitOf(dir, "HEAD")
}

// CommitOf returns the full SHA that rev resolves to in dir, or "" on any error
// (missing git, not a repo, unknown revision). Used to record the base commit a
// session worktree was created from when the start point is not HEAD.
func CommitOf(dir, rev string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// DefaultBranchRef returns the remote default branch as recorded by origin/HEAD,
// e.g. "origin/main". It returns "" when there is no origin remote, origin/HEAD
// is not configured, or on any error. This is used as the default start point
// for new session worktrees so they branch from the canonical upstream tip
// rather than whatever the main checkout currently has checked out.
func DefaultBranchRef(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
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

// EnsureGitExcluded makes a best-effort, idempotent attempt to ensure pattern is
// ignored in repoDir WITHOUT modifying any tracked file. If git already ignores
// pattern (via any .gitignore, global excludes, a prior exclude entry, etc.) it
// does nothing; otherwise it appends pattern to the repository's
// .git/info/exclude — an untracked, per-clone file — preceded by a labeled
// comment line. For linked worktrees the entry is written to the MAIN
// repository's exclude file (resolved via the git common dir) so it applies
// repo-wide without dirtying the user's version-controlled .gitignore. The file
// is created if needed and a separating newline is ensured before appending.
func EnsureGitExcluded(repoDir, pattern, comment string) error {
	if IsIgnored(repoDir, pattern) {
		return nil
	}
	common := CommonDir(repoDir)
	if common == "" {
		return fmt.Errorf("could not resolve git common dir for %q", repoDir)
	}
	infoDir := filepath.Join(common, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(infoDir, "exclude")
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
