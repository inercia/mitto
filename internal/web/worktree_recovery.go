package web

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/git"
	"github.com/inercia/mitto/internal/session"
)

// recoverOrphanedWorktrees is the startup crash-recovery for git worktrees left
// behind by the worktree lifecycle hooks. It scans appdir.WorktreesDir() for
// worktree directories whose <session-id> has no matching session in the store
// (deleted or crashed mid-create) and removes them best-effort. It also clears
// stale worktree metadata for sessions whose worktree directory no longer exists
// on disk (manually removed) so a later delete does not error.
//
// It is gated quickly on the worktrees directory existing, never blocks startup,
// and logs every cleanup. All failures are swallowed.
func recoverOrphanedWorktrees(store *session.Store, logger *slog.Logger) {
	if store == nil {
		return
	}
	worktreesDir, err := appdir.WorktreesDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		// Absent/empty worktrees dir => the feature is unused: nothing to do.
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
	for _, m := range sessions {
		known[m.SessionID] = struct{}{}
	}

	// 1) Remove orphaned worktree dirs (no matching session).
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sid := e.Name()
		if _, ok := known[sid]; ok {
			continue
		}
		removeOrphanWorktree(filepath.Join(worktreesDir, sid), git.BranchName(sid), logger)
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

// removeOrphanWorktree removes a single orphaned worktree. When the owning repo
// can be resolved (the worktree's gitdir points back via git rev-parse
// --git-common-dir), it runs git.RemoveWorktree + git.DeleteBranch. When the
// repo cannot be resolved (stray dir or git unavailable), it removes the stray
// directory directly. All failures are best-effort and logged.
func removeOrphanWorktree(worktreePath, branch string, logger *slog.Logger) {
	repoRoot := gitMainWorktreeRoot(worktreePath)
	if repoRoot == "" {
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
		return
	}

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
}
