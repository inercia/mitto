package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// MergeBackReason classifies the outcome of a MergeBack attempt that did not
// complete successfully. It is empty when the merge-back succeeded.
type MergeBackReason string

const (
	// MergeBackDirtyWorktree means the session worktree has uncommitted changes,
	// so it cannot be rebased/merged safely.
	MergeBackDirtyWorktree MergeBackReason = "dirty_worktree"
	// MergeBackTargetCheckedOutDirty means the target branch is checked out in a
	// working tree that has uncommitted changes, so it cannot be advanced safely.
	MergeBackTargetCheckedOutDirty MergeBackReason = "target_checked_out_dirty"
	// MergeBackConflict means the rebase/merge stopped on conflicts and was aborted.
	MergeBackConflict MergeBackReason = "conflict"
	// MergeBackError means a precondition or git command failed for another reason.
	MergeBackError MergeBackReason = "error"
)

// Supported merge-back strategies.
const (
	MergeStrategyRebase = "rebase"
	MergeStrategyMerge  = "merge"
)

// MergeBackOptions configures a merge-back of a session worktree branch into a
// target branch.
type MergeBackOptions struct {
	RepoDir      string // main repository root (where Target may be checked out)
	WorktreeDir  string // session worktree dir (has SourceBranch checked out)
	SourceBranch string // the worktree branch, e.g. mitto-<sid>
	Target       string // existing target branch to merge into (ignored when NewBranch is set)
	NewBranch    string // when set, create this branch off the repo default and merge into it
	Strategy     string // MergeStrategyRebase (default) or MergeStrategyMerge
}

// MergeBackResult reports the outcome of MergeBack. Reason is empty on success.
type MergeBackResult struct {
	Target string          // resolved target branch
	Reason MergeBackReason // empty on success
	Detail string          // human-readable detail (git output) on failure
}

// MergeBack merges a session worktree branch back into a target branch using the
// configured strategy (rebase by default). It never force-moves a branch that is
// checked out with uncommitted changes, and aborts cleanly on conflict. A
// non-nil error is only returned for programmer errors (bad options); all
// expected failures are reported via MergeBackResult.Reason.
func MergeBack(ctx context.Context, opts MergeBackOptions) (MergeBackResult, error) {
	res := MergeBackResult{}
	if opts.RepoDir == "" || opts.WorktreeDir == "" || opts.SourceBranch == "" {
		return res, fmt.Errorf("mergeback: RepoDir, WorktreeDir and SourceBranch are required")
	}
	strategy := opts.Strategy
	if strategy == "" {
		strategy = MergeStrategyRebase
	}

	// Resolve the target branch, creating it from the default branch when requested.
	target := opts.Target
	if opts.NewBranch != "" {
		def := DefaultBranch(ctx, opts.RepoDir)
		if def == "" {
			return fail(res, MergeBackError, "could not determine the repository default branch to create the new branch from"), nil
		}
		if RefExists(ctx, opts.RepoDir, opts.NewBranch) {
			return fail(res, MergeBackError, fmt.Sprintf("branch %q already exists", opts.NewBranch)), nil
		}
		if out, err := gitCombined(ctx, opts.RepoDir, "branch", opts.NewBranch, def); err != nil {
			return fail(res, MergeBackError, fmt.Sprintf("create branch %q from %q: %s", opts.NewBranch, def, out)), nil
		}
		target = opts.NewBranch
	}
	if target == "" {
		return res, fmt.Errorf("mergeback: a Target or NewBranch is required")
	}
	res.Target = target
	if !RefExists(ctx, opts.RepoDir, target) {
		return fail(res, MergeBackError, fmt.Sprintf("target branch %q does not exist", target)), nil
	}

	// Never touch anything if the worktree has uncommitted work.
	if IsDirty(ctx, opts.WorktreeDir) {
		return fail(res, MergeBackDirtyWorktree, ""), nil
	}

	// Bail early if the target is checked out in a dirty working tree.
	wtBranches, err := WorktreeBranches(ctx, opts.RepoDir)
	if err != nil {
		return fail(res, MergeBackError, err.Error()), nil
	}
	targetPath, targetCheckedOut := wtBranches[target]
	if targetCheckedOut && IsDirty(ctx, targetPath) {
		return fail(res, MergeBackTargetCheckedOutDirty, ""), nil
	}

	switch strategy {
	case MergeStrategyRebase:
		// Replay the worktree's commits on top of the target tip, then advance
		// the target to the rebased SourceBranch tip (a guaranteed fast-forward).
		if out, err := gitCombined(ctx, opts.WorktreeDir, "rebase", target); err != nil {
			_, _ = gitCombined(ctx, opts.WorktreeDir, "rebase", "--abort")
			return fail(res, MergeBackConflict, out), nil
		}
		return advanceTarget(ctx, opts, target, targetPath, targetCheckedOut, res), nil
	case MergeStrategyMerge:
		if targetCheckedOut {
			msg := fmt.Sprintf("Merge %s into %s", opts.SourceBranch, target)
			if out, err := gitCombined(ctx, targetPath, "merge", "--no-ff", "-m", msg, opts.SourceBranch); err != nil {
				_, _ = gitCombined(ctx, targetPath, "merge", "--abort")
				return fail(res, MergeBackConflict, out), nil
			}
			return res, nil
		}
		// Target not checked out: only a fast-forward is safe without a working tree.
		if IsAncestor(ctx, opts.RepoDir, target, opts.SourceBranch) {
			return advanceTarget(ctx, opts, target, targetPath, false, res), nil
		}
		return fail(res, MergeBackError, fmt.Sprintf("target %q is not checked out and has diverged; use the rebase strategy", target)), nil
	default:
		return res, fmt.Errorf("mergeback: unknown strategy %q", strategy)
	}
}

// advanceTarget fast-forwards the target branch to the SourceBranch tip, either
// through the target's working tree (when checked out) or via `branch -f`.
func advanceTarget(ctx context.Context, opts MergeBackOptions, target, targetPath string, checkedOut bool, res MergeBackResult) MergeBackResult {
	if checkedOut {
		if out, err := gitCombined(ctx, targetPath, "merge", "--ff-only", opts.SourceBranch); err != nil {
			return fail(res, MergeBackError, out)
		}
		return res
	}
	if out, err := gitCombined(ctx, opts.RepoDir, "branch", "-f", target, opts.SourceBranch); err != nil {
		return fail(res, MergeBackError, out)
	}
	return res
}

func fail(res MergeBackResult, reason MergeBackReason, detail string) MergeBackResult {
	res.Reason = reason
	res.Detail = detail
	return res
}

func gitCombined(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
