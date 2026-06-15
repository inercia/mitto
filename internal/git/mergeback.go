package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// IsAncestor reports whether ancestor is an ancestor of descendant in dir.
// It runs `git -C dir merge-base --is-ancestor <ancestor> <descendant>` and
// returns false on any error (missing refs, not a repo, etc.).
func IsAncestor(ctx context.Context, dir, ancestor, descendant string) bool {
	err := exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", ancestor, descendant).Run()
	return err == nil
}

// RefExists reports whether ref resolves to a commit in dir.
func RefExists(ctx context.Context, dir, ref string) bool {
	err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Run()
	return err == nil
}

// IsDirty reports whether dir's working tree has uncommitted changes (staged,
// unstaged, or untracked). It runs `git -C dir status --porcelain` and returns
// false on any error.
func IsDirty(ctx context.Context, dir string) bool {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// AheadBehind returns how many commits branch is ahead of and behind base in
// dir, computed from `git rev-list --left-right --count base...branch` (left =
// commits only on base = behind; right = commits only on branch = ahead).
func AheadBehind(ctx context.Context, dir, base, branch string) (ahead, behind int, err error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"rev-list", "--left-right", "--count", base+"..."+branch).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("git rev-list: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", string(out))
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind, nil
}

// ListBranches returns the local branch names in dir, most-recently-committed
// first. It runs `git for-each-ref --sort=-committerdate refs/heads/`.
func ListBranches(ctx context.Context, dir string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"for-each-ref", "--format=%(refname:short)", "--sort=-committerdate", "refs/heads/").Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	branches := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// WorktreeBranches returns a map from branch name to worktree path for every
// branch currently checked out in any worktree (including the main one) of
// dir's repository. Parsed from `git -C dir worktree list --porcelain`.
func WorktreeBranches(ctx context.Context, dir string) (map[string]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	result := make(map[string]string)
	var curPath string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			curPath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			branch := strings.TrimPrefix(ref, "refs/heads/")
			if branch != "" && curPath != "" {
				result[branch] = curPath
			}
		}
	}
	return result, nil
}

// DefaultBranch returns the repository's default branch name for dir. It prefers
// the target of origin/HEAD, then a local "main", then "master"; returns "" if
// none can be determined.
func DefaultBranch(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		if ref := strings.TrimSpace(string(out)); ref != "" {
			return strings.TrimPrefix(ref, "origin/")
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if RefExists(ctx, dir, candidate) {
			return candidate
		}
	}
	return ""
}
