package web

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/inercia/mitto/internal/git"
	"github.com/inercia/mitto/internal/session"
)

// recoverOrphanedWorktrees is the startup crash-recovery for git worktrees left
// behind by the worktree lifecycle hooks. Worktrees live in-project under
// <repoRoot>/.mitto/worktrees/<session-id>, so there is no single central
// directory to scan. Instead it derives the candidate worktree-root directories
// from the WorktreePath of every known session (their parent
// <repoRoot>/.mitto/worktrees dir) and scans each for <session-id> directories
// whose session no longer exists (deleted or crashed mid-create), removing them
// best-effort. It also clears stale worktree metadata for sessions whose
// worktree directory no longer exists on disk (manually removed) so a later
// delete does not error.
//
// A repository whose sessions have all been removed will not be discovered (no
// known session points at its worktree root); such fully-orphaned roots are
// left for the per-session delete path to have already cleaned up.
//
// It never blocks startup and logs every cleanup. All failures are swallowed.
func recoverOrphanedWorktrees(store *session.Store, logger *slog.Logger) {
	if store == nil {
		return
	}

	sessions, err := store.List()
	if err != nil {
		if logger != nil {
			logger.Warn("Worktree recovery: failed to list sessions", "error", err)
		}
		return
	}
	known := make(map[string]struct{}, len(sessions))
	roots := make(map[string]struct{})
	for _, m := range sessions {
		known[m.SessionID] = struct{}{}
		if m.WorktreePath != "" {
			roots[filepath.Dir(m.WorktreePath)] = struct{}{}
		}
	}

	// 1) Remove orphaned worktree dirs (no matching session) in each known root.
	for root := range roots {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sid := e.Name()
			if _, ok := known[sid]; ok {
				continue
			}
			removeOrphanWorktree(filepath.Join(root, sid), git.BranchName(sid), logger)
		}
	}

	// 2) Clear stale worktree metadata for sessions whose worktree dir is gone.
	for _, m := range sessions {
		if m.WorktreePath == "" {
			continue
		}
		if _, statErr := os.Stat(m.WorktreePath); statErr == nil {
			continue // worktree still exists
		}
		sid := m.SessionID
		if uErr := store.UpdateMetadata(sid, func(md *session.Metadata) {
			md.WorktreePath = ""
			md.WorktreeBranch = ""
		}); uErr != nil {
			if logger != nil {
				logger.Warn("Worktree recovery: failed to clear stale metadata",
					"error", uErr, "session_id", sid)
			}
		} else if logger != nil {
			logger.Info("Worktree recovery: cleared stale worktree metadata", "session_id", sid)
		}
	}
}

// removeOrphanWorktree removes a single orphaned worktree. A genuine linked
// worktree (identified by a .git FILE pointing at its gitdir) is removed with
// git.RemoveWorktree + git.DeleteBranch against its owning repo. Anything else
// (a stray plain directory, which — now that worktrees live in-project under
// <repoRoot>/.mitto/worktrees — would otherwise resolve to the enclosing repo
// via directory ancestry and be misclassified as a worktree) is removed
// directly. All failures are best-effort and logged.
func removeOrphanWorktree(worktreePath, branch string, logger *slog.Logger) {
	if isLinkedWorktreeDir(worktreePath) {
		if repoRoot := gitMainWorktreeRoot(worktreePath); repoRoot != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := git.RemoveWorktree(ctx, repoRoot, worktreePath); err != nil {
				if logger != nil {
					logger.Warn("Worktree recovery: git worktree remove failed",
						"error", err, "worktree_path", worktreePath, "repo", repoRoot)
				}
				return
			}
			if branch != "" {
				if err := git.DeleteBranch(ctx, repoRoot, branch); err != nil && logger != nil {
					logger.Warn("Worktree recovery: git branch delete failed",
						"error", err, "branch", branch, "repo", repoRoot)
				}
			}
			if logger != nil {
				logger.Info("Worktree recovery: removed orphaned worktree",
					"worktree_path", worktreePath, "branch", branch, "repo", repoRoot)
			}
			return
		}
	}

	// Stray directory (no .git gitdir pointer) or repo unresolvable: remove it
	// directly rather than attempting a git worktree remove that would fail.
	if err := os.RemoveAll(worktreePath); err != nil {
		if logger != nil {
			logger.Warn("Worktree recovery: failed to remove stray worktree dir",
				"error", err, "worktree_path", worktreePath)
		}
		return
	}
	if logger != nil {
		logger.Info("Worktree recovery: removed stray worktree dir", "worktree_path", worktreePath)
	}
}

// isLinkedWorktreeDir reports whether path is a registered git linked worktree.
// A linked worktree contains a .git FILE (a gitdir pointer to
// <main>/.git/worktrees/<name>), whereas a stray plain directory has no .git
// entry and a main checkout has a .git DIRECTORY. This distinguishes a real
// worktree from a stray dir that merely sits inside a repository's working tree.
func isLinkedWorktreeDir(path string) bool {
	fi, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && !fi.IsDir()
}
