package web

import (
	"context"
	"net/http"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/git"
)

const worktreeGitTimeout = 30 * time.Second

// MergeBackSessionBusy is a web-layer reason reported when a merge-back is
// refused because the session's agent is actively streaming a response.
const MergeBackSessionBusy = "session_busy"

// WorktreeStatusResponse describes the merge-back state of a session worktree.
type WorktreeStatusResponse struct {
	HasWorktree        bool   `json:"has_worktree"`
	WorktreeDir        string `json:"worktree_dir,omitempty"`
	RepoDir            string `json:"repo_dir,omitempty"`
	SourceBranch       string `json:"source_branch,omitempty"`
	BaseBranch         string `json:"base_branch,omitempty"`
	Dirty              bool   `json:"dirty"`
	Ahead              int    `json:"ahead"`
	Behind             int    `json:"behind"`
	HasUnmergedWork    bool   `json:"has_unmerged_work"`
	MergeStrategy      string `json:"merge_strategy"`
	DefaultMergeTarget string `json:"default_merge_target,omitempty"`
}

// conversationsConfig returns the global conversations config, or nil. The
// getters on *config.ConversationsConfig are nil-safe.
func (s *Server) conversationsConfig() *config.ConversationsConfig {
	if s.config.MittoConfig != nil {
		return s.config.MittoConfig.Conversations
	}
	return nil
}

// handleSessionWorktreeStatus handles GET /api/sessions/{id}/worktree-status.
// It reports whether the session runs in a git worktree and whether that
// worktree has unmerged work (uncommitted changes or commits ahead of its base
// branch), so the UI can warn before deleting the conversation.
func (s *Server) handleSessionWorktreeStatus(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	conv := s.conversationsConfig()
	resp := WorktreeStatusResponse{
		MergeStrategy:      conv.GetMergeStrategy(),
		DefaultMergeTarget: conv.GetDefaultMergeTarget(),
	}

	store := s.Store()
	if store == nil {
		writeJSONOK(w, resp)
		return
	}
	meta, err := store.GetMetadata(sessionID)
	if err != nil || meta.WorktreePath == "" {
		writeJSONOK(w, resp)
		return
	}
	s.ensureWorktreeRepoDir(&meta)

	resp.HasWorktree = true
	resp.WorktreeDir = meta.WorktreePath
	resp.RepoDir = meta.WorktreeRepoDir
	resp.SourceBranch = meta.WorktreeBranch
	resp.BaseBranch = meta.WorktreeBaseBranch

	ctx, cancel := context.WithTimeout(r.Context(), worktreeGitTimeout)
	defer cancel()

	resp.Dirty = git.IsDirty(ctx, meta.WorktreePath)
	if meta.WorktreeBaseBranch != "" && meta.WorktreeBranch != "" &&
		git.RefExists(ctx, meta.WorktreePath, meta.WorktreeBaseBranch) {
		if ahead, behind, aErr := git.AheadBehind(ctx, meta.WorktreePath, meta.WorktreeBaseBranch, meta.WorktreeBranch); aErr == nil {
			resp.Ahead = ahead
			resp.Behind = behind
		}
	}
	resp.HasUnmergedWork = resp.Dirty || resp.Ahead > 0
	writeJSONOK(w, resp)
}

// WorktreeBranchesResponse lists candidate merge-back target branches.
type WorktreeBranchesResponse struct {
	Branches           []string        `json:"branches"`
	CheckedOut         map[string]bool `json:"checked_out"`
	DefaultBranch      string          `json:"default_branch,omitempty"`
	DefaultMergeTarget string          `json:"default_merge_target,omitempty"`
	SourceBranch       string          `json:"source_branch,omitempty"`
}

// handleSessionBranches handles GET /api/sessions/{id}/branches. It returns the
// local branches of the session's repository (most-recently-committed first) so
// the merge-back dialog can offer a target, excluding the session's own branch.
func (s *Server) handleSessionBranches(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	conv := s.conversationsConfig()
	resp := WorktreeBranchesResponse{
		Branches:           []string{},
		CheckedOut:         map[string]bool{},
		DefaultMergeTarget: conv.GetDefaultMergeTarget(),
	}

	store := s.Store()
	if store == nil {
		writeJSONOK(w, resp)
		return
	}
	meta, err := store.GetMetadata(sessionID)
	if err != nil || meta.WorktreePath == "" {
		writeJSONOK(w, resp)
		return
	}
	s.ensureWorktreeRepoDir(&meta)
	repoDir := meta.WorktreeRepoDir
	if repoDir == "" {
		repoDir = meta.WorktreePath
	}
	resp.SourceBranch = meta.WorktreeBranch

	ctx, cancel := context.WithTimeout(r.Context(), worktreeGitTimeout)
	defer cancel()

	resp.DefaultBranch = git.DefaultBranch(ctx, repoDir)
	if branches, bErr := git.ListBranches(ctx, repoDir); bErr == nil {
		for _, b := range branches {
			if b == meta.WorktreeBranch {
				continue // never offer the session's own branch as a target
			}
			resp.Branches = append(resp.Branches, b)
		}
	}
	if wt, wErr := git.WorktreeBranches(ctx, repoDir); wErr == nil {
		for b := range wt {
			resp.CheckedOut[b] = true
		}
	}
	writeJSONOK(w, resp)
}

// MergeRequest is the body of POST /api/sessions/{id}/merge. Exactly one of
// Target (an existing branch) or NewBranch (created off the repo default branch)
// must be set. Strategy defaults to the configured merge strategy when empty.
type MergeRequest struct {
	Target    string `json:"target,omitempty"`
	NewBranch string `json:"new_branch,omitempty"`
	Strategy  string `json:"strategy,omitempty"`
}

// MergeResponse reports the outcome of a merge-back attempt. Success is false
// with a Reason when the merge could not complete (dirty worktree, conflict,
// busy session, etc.); the frontend uses Reason to keep the conversation and,
// on conflict, offer to hand the rebase to the agent.
type MergeResponse struct {
	Success bool   `json:"success"`
	Target  string `json:"target,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// handleSessionMerge handles POST /api/sessions/{id}/merge. It merges the
// session's worktree branch back into the requested target using the configured
// strategy (rebase by default), never moving a branch that is checked out with
// uncommitted changes and aborting cleanly on conflict.
func (s *Server) handleSessionMerge(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req MergeRequest
	if !parseJSONBody(w, r, &req) {
		return
	}
	if req.Target == "" && req.NewBranch == "" {
		http.Error(w, "A target or new_branch is required", http.StatusBadRequest)
		return
	}

	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}
	meta, err := store.GetMetadata(sessionID)
	if err != nil || meta.WorktreePath == "" || meta.WorktreeBranch == "" {
		http.Error(w, "Session has no worktree to merge", http.StatusBadRequest)
		return
	}
	s.ensureWorktreeRepoDir(&meta)
	repoDir := meta.WorktreeRepoDir
	if repoDir == "" {
		repoDir = meta.WorktreePath
	}

	// Refuse to merge while the agent is actively streaming; route to the
	// agent-prompt path instead (handled by the frontend on this reason).
	if bs := s.sessionManager.GetSession(sessionID); bs != nil && bs.IsPrompting() {
		writeJSONOK(w, MergeResponse{Success: false, Reason: MergeBackSessionBusy})
		return
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = s.conversationsConfig().GetMergeStrategy()
	}

	ctx, cancel := context.WithTimeout(r.Context(), worktreeGitTimeout)
	defer cancel()

	result, mErr := git.MergeBack(ctx, git.MergeBackOptions{
		RepoDir:      repoDir,
		WorktreeDir:  meta.WorktreePath,
		SourceBranch: meta.WorktreeBranch,
		Target:       req.Target,
		NewBranch:    req.NewBranch,
		Strategy:     strategy,
	})
	if mErr != nil {
		http.Error(w, mErr.Error(), http.StatusBadRequest)
		return
	}
	writeJSONOK(w, MergeResponse{
		Success: result.Reason == "",
		Target:  result.Target,
		Reason:  string(result.Reason),
		Detail:  result.Detail,
	})
}
