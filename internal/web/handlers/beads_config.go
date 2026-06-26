package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
)

// beadsConfigSetRequest is the JSON body for PUT /api/beads/config.
type beadsConfigSetRequest struct {
	WorkingDir string `json:"working_dir"`
	Key        string `json:"key"`
	Value      string `json:"value"`
}

// HandleBeadsConfig handles the per-folder beads config store:
//   - GET    /api/beads/config?working_dir=...            -> "bd config show --json"
//   - PUT    /api/beads/config (body: working_dir,key,value) -> "bd config set <key> <value>"
//   - DELETE /api/beads/config?working_dir=...&key=...     -> "bd config unset <key>"
//
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleBeadsConfigGet(w, r)
	case http.MethodPut:
		h.handleBeadsConfigSet(w, r)
	case http.MethodDelete:
		h.handleBeadsConfigUnset(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleBeadsConfigGet runs "bd config show --json" in the workspace directory
// and returns a flat {key: value} map of user-set configuration.
//
// We use "show" rather than "list" because "list" only reports keys stored in
// the beads database, omitting integration keys (e.g. github.token) that live
// in .beads/config.yaml. "show" reports all effective config with provenance;
// we filter to user-set sources and flatten the array into the flat-map shape
// the frontend expects.
func (h *Handlers) handleBeadsConfigGet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	result, err := h.beadsClient().ConfigShow(r.Context(), workingDir)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, result)
}

// handleBeadsConfigSet runs "bd config set <key> <value>" in the workspace
// directory. The folder is auto-initialized first when needed so configuring
// an integration in a fresh folder "just works" rather than failing with
// "run 'bd init' first".
func (h *Handlers) handleBeadsConfigSet(w http.ResponseWriter, r *http.Request) {
	var req beadsConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	if req.WorkingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !beads.IsValidConfigKey(req.Key) {
		writeErrorJSON(w, http.StatusBadRequest, "", "invalid config key")
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	if err := h.beadsClient().ConfigSet(r.Context(), req.WorkingDir, req.Key, req.Value); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// handleBeadsConfigUnset runs "bd config unset <key>" in the workspace directory.
func (h *Handlers) handleBeadsConfigUnset(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	key := r.URL.Query().Get("key")

	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !beads.IsValidConfigKey(key) {
		writeErrorJSON(w, http.StatusBadRequest, "", "invalid config key")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	if err := h.beadsClient().ConfigUnset(r.Context(), workingDir, key); err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsUpstreamRequest is the JSON body for PUT /api/beads/upstream.
type beadsUpstreamRequest struct {
	WorkingDir string `json:"working_dir"`
	Upstream   string `json:"upstream"`
	// PullPrompt, PushPrompt, SyncPrompt are the workspace prompt names to run for
	// pull/push/sync operations. Only used when Upstream == "prompts". Empty strings
	// are allowed (the corresponding operation is simply unconfigured).
	PullPrompt string `json:"pull_prompt"`
	PushPrompt string `json:"push_prompt"`
	SyncPrompt string `json:"sync_prompt"`
}

// beadsUpstreamResponse reports the configured upstream task system for a folder.
type beadsUpstreamResponse struct {
	Upstream   string `json:"upstream"`
	PullPrompt string `json:"pull_prompt,omitempty"`
	PushPrompt string `json:"push_prompt,omitempty"`
	SyncPrompt string `json:"sync_prompt,omitempty"`
}

// HandleBeadsUpstream manages the per-folder beads upstream task system stored
// in folders.json (folder-native, not a bd config value):
//   - GET /api/beads/upstream?working_dir=...        -> {"upstream":"none|jira|github|gitlab|linear|prompts","pull_prompt","push_prompt","sync_prompt"}
//   - PUT /api/beads/upstream (body: working_dir,upstream,pull_prompt,push_prompt,sync_prompt) -> persists the choice
//
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsUpstream(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleBeadsUpstreamGet(w, r)
	case http.MethodPut:
		h.handleBeadsUpstreamSet(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handlers) handleBeadsUpstreamGet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	upstream := config.FolderBeadsUpstream(workingDir)
	if upstream == "" {
		upstream = "none"
	}
	pull, push, sync := config.FolderBeadsPrompts(workingDir)
	writeJSONOK(w, beadsUpstreamResponse{
		Upstream:   upstream,
		PullPrompt: pull,
		PushPrompt: push,
		SyncPrompt: sync,
	})
}

func (h *Handlers) handleBeadsUpstreamSet(w http.ResponseWriter, r *http.Request) {
	var req beadsUpstreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	if req.WorkingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !beads.IsValidUpstream(req.Upstream) {
		writeErrorJSON(w, http.StatusBadRequest, "", "upstream must be one of: none, jira, github, gitlab, linear, prompts")
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	if req.Upstream == "prompts" {
		// Validate each non-empty prompt name: it must exist in the folder's
		// effective prompt list and must have no parameters (len(Parameters)==0).
		var allPrompts []config.WebPrompt
		if h.deps.GetWorkspacePromptsAll != nil {
			allPrompts = h.deps.GetWorkspacePromptsAll(req.WorkingDir)
		}
		promptIdx := make(map[string]config.WebPrompt, len(allPrompts))
		for _, p := range allPrompts {
			promptIdx[strings.ToLower(p.Name)] = p
		}
		for field, name := range map[string]string{
			"pull_prompt": req.PullPrompt,
			"push_prompt": req.PushPrompt,
			"sync_prompt": req.SyncPrompt,
		} {
			if name == "" {
				continue // empty is allowed; operation simply unconfigured
			}
			p, ok := promptIdx[strings.ToLower(name)]
			if !ok {
				writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("%s: prompt %q not found in this folder's prompt list", field, name))
				return
			}
			if len(p.Parameters) > 0 {
				writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("%s: prompt %q requires parameters and cannot be used as a beads action prompt", field, name))
				return
			}
		}
		if err := config.SetFolderBeadsPromptUpstream(req.WorkingDir, req.PullPrompt, req.PushPrompt, req.SyncPrompt); err != nil {
			writeJSONOK(w, beadsErrorResponse{Error: err.Error()})
			return
		}
	} else {
		if err := config.SetFolderBeadsUpstream(req.WorkingDir, req.Upstream); err != nil {
			writeJSONOK(w, beadsErrorResponse{Error: err.Error()})
			return
		}
	}

	upstream := req.Upstream
	if upstream == "" {
		upstream = "none"
	}
	pull, push, sync := config.FolderBeadsPrompts(req.WorkingDir)
	writeJSONOK(w, beadsUpstreamResponse{
		Upstream:   upstream,
		PullPrompt: pull,
		PushPrompt: push,
		SyncPrompt: sync,
	})
}

// beadsSyncRequest is the JSON body for POST /api/beads/sync.
// Action must be "pull", "push", "sync", or "status".
type beadsSyncRequest struct {
	WorkingDir string `json:"working_dir"`
	Action     string `json:"action"`
}

// beadsSyncResponse carries the captured bd output on success.
type beadsSyncResponse struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

// HandleBeadsSync handles POST /api/beads/sync. It runs the configured
// upstream's pull/push/sync/status command for the folder. The integration is
// read authoritatively from folders.json — the client only chooses the action.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req beadsSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	if req.WorkingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(req.WorkingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	// The integration is read from folders.json, never trusted from the client.
	upstream := config.FolderBeadsUpstream(req.WorkingDir)
	if upstream == "" || upstream == "none" {
		writeJSONOK(w, beadsErrorResponse{Error: "no upstream task system is configured for this folder"})
		return
	}

	// Validate the action before invoking bd (keeps HTTP 400 for invalid actions).
	switch req.Action {
	case "pull", "push", "sync", "status":
		// valid
	default:
		writeErrorJSON(w, http.StatusBadRequest, "", "action must be one of: pull, push, sync, status")
		return
	}

	out, err := h.beadsClient().Sync(r.Context(), req.WorkingDir, upstream, req.Action)
	if err != nil {
		writeJSONOK(w, beadsErrorResponse{Error: err.Error(), Stderr: beads.StderrOf(err)})
		return
	}

	writeJSONOK(w, beadsSyncResponse{OK: true, Output: out})
}
