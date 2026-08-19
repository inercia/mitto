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

// beadsConfigSetRequest is the JSON body for PUT /api/issues/config.
type beadsConfigSetRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// HandleBeadsConfig handles the per-folder beads config store:
//   - GET    /api/issues/config?working_dir=...            -> "bd config show --json"
//   - PUT    /api/issues/config?working_dir=... (body: key,value) -> "bd config set <key> <value>"
//   - DELETE /api/issues/config?working_dir=...&key=...     -> "bd config unset <key>"
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
		h.writeBeadsError(w, r, err)
		return
	}

	writeJSONOK(w, result)
}

// handleBeadsConfigSet runs "bd config set <key> <value>" in the workspace
// directory. The folder is auto-initialized first when needed so configuring
// an integration in a fresh folder "just works" rather than failing with
// "run 'bd init' first".
func (h *Handlers) handleBeadsConfigSet(w http.ResponseWriter, r *http.Request) {
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

	var req beadsConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	if !beads.IsValidConfigKey(req.Key) {
		writeErrorJSON(w, http.StatusBadRequest, "", "invalid config key")
		return
	}

	if err := h.beadsClient().ConfigSet(r.Context(), workingDir, req.Key, req.Value); err != nil {
		h.writeBeadsError(w, r, err)
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
		h.writeBeadsError(w, r, err)
		return
	}

	writeJSONOK(w, beadsActionResponse{OK: true})
}

// beadsUpstreamRequest is the JSON body for PUT /api/issues/upstream.
type beadsUpstreamRequest struct {
	Upstream string `json:"upstream"`
	// PullPrompt, PushPrompt, SyncPrompt are the workspace prompt names to run for
	// pull/push/sync operations. Only used when Upstream == "prompts". Empty strings
	// are allowed (the corresponding operation is simply unconfigured).
	PullPrompt string `json:"pull_prompt"`
	PushPrompt string `json:"push_prompt"`
	SyncPrompt string `json:"sync_prompt"`
	// PullPromptArgs, PushPromptArgs, SyncPromptArgs are the argument values to
	// forward to the corresponding prompt when it is dispatched.
	PullPromptArgs map[string]string `json:"pull_prompt_args,omitempty"`
	PushPromptArgs map[string]string `json:"push_prompt_args,omitempty"`
	SyncPromptArgs map[string]string `json:"sync_prompt_args,omitempty"`
}

// beadsUpstreamResponse reports the configured upstream task system for a folder.
type beadsUpstreamResponse struct {
	Upstream       string            `json:"upstream"`
	PullPrompt     string            `json:"pull_prompt,omitempty"`
	PushPrompt     string            `json:"push_prompt,omitempty"`
	SyncPrompt     string            `json:"sync_prompt,omitempty"`
	PullPromptArgs map[string]string `json:"pull_prompt_args,omitempty"`
	PushPromptArgs map[string]string `json:"push_prompt_args,omitempty"`
	SyncPromptArgs map[string]string `json:"sync_prompt_args,omitempty"`
}

// beadsDatabaseModeResponse is intentionally independent from beadsUpstreamResponse:
// database replication policy and external-task synchronization are orthogonal.
type beadsDatabaseModeResponse struct {
	DatabaseMode config.BeadsDatabaseMode `json:"database_mode"`
	HasRemote    bool                     `json:"has_remote"`
}

type beadsDatabaseModeRequest struct {
	DatabaseMode config.BeadsDatabaseMode `json:"database_mode"`
}

// HandleBeadsDatabaseMode manages the effective per-folder Beads Dolt policy.
func (h *Handlers) HandleBeadsDatabaseMode(w http.ResponseWriter, r *http.Request) {
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

	client := h.beadsClient()
	switch r.Method {
	case http.MethodGet:
		mode, err := beads.ResolveDatabaseMode(r.Context(), client, workingDir)
		if err != nil {
			h.writeBeadsError(w, r, err)
			return
		}
		hasRemote, err := beads.DoltRemoteConfigured(r.Context(), client, workingDir)
		if err != nil {
			h.writeBeadsError(w, r, err)
			return
		}
		writeJSONOK(w, beadsDatabaseModeResponse{DatabaseMode: mode, HasRemote: hasRemote})
	case http.MethodPut:
		var req beadsDatabaseModeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
			return
		}
		if !config.IsValidBeadsDatabaseMode(req.DatabaseMode) {
			writeErrorJSON(w, http.StatusBadRequest, "", "database_mode must be one of: local, shared")
			return
		}
		hasRemote, err := beads.DoltRemoteConfigured(r.Context(), client, workingDir)
		if err != nil {
			h.writeBeadsError(w, r, err)
			return
		}
		if err := config.SetFolderBeadsDatabaseMode(workingDir, req.DatabaseMode); err != nil {
			h.writeBeadsError(w, r, err)
			return
		}
		writeJSONOK(w, beadsDatabaseModeResponse{DatabaseMode: req.DatabaseMode, HasRemote: hasRemote})
	default:
		methodNotAllowed(w)
	}
}

// HandleBeadsUpstream manages the per-folder beads upstream task system stored
// in folders.json (folder-native, not a bd config value):
//   - GET /api/issues/upstream?working_dir=...        -> {"upstream":"none|jira|github|gitlab|linear|prompts","pull_prompt","push_prompt","sync_prompt","pull_prompt_args","push_prompt_args","sync_prompt_args"}
//   - PUT /api/issues/upstream?working_dir=... (body: upstream,pull_prompt,push_prompt,sync_prompt,pull_prompt_args,push_prompt_args,sync_prompt_args) -> persists the choice
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
	pullArgs, pushArgs, syncArgs := config.FolderBeadsPromptArgs(workingDir)
	writeJSONOK(w, beadsUpstreamResponse{
		Upstream:       upstream,
		PullPrompt:     pull,
		PushPrompt:     push,
		SyncPrompt:     sync,
		PullPromptArgs: pullArgs,
		PushPromptArgs: pushArgs,
		SyncPromptArgs: syncArgs,
	})
}

func (h *Handlers) handleBeadsUpstreamSet(w http.ResponseWriter, r *http.Request) {
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

	var req beadsUpstreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	if !beads.IsValidUpstream(req.Upstream) {
		writeErrorJSON(w, http.StatusBadRequest, "", "upstream must be one of: none, jira, github, gitlab, linear, prompts")
		return
	}

	if req.Upstream == "prompts" {
		// Validate each non-empty prompt name exists in the folder's effective
		// prompt list. Parametrized prompts are allowed: their arguments are
		// supplied via req.*PromptArgs and forwarded at dispatch time.
		var allPrompts []config.WebPrompt
		if h.deps.GetWorkspacePromptsAll != nil {
			allPrompts = h.deps.GetWorkspacePromptsAll(workingDir)
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
			if _, ok := promptIdx[strings.ToLower(name)]; !ok {
				writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("%s: prompt %q not found in this folder's prompt list", field, name))
				return
			}
		}
		if err := config.SetFolderBeadsPromptUpstream(workingDir, req.PullPrompt, req.PushPrompt, req.SyncPrompt,
			req.PullPromptArgs, req.PushPromptArgs, req.SyncPromptArgs); err != nil {
			h.writeBeadsError(w, r, err)
			return
		}
	} else {
		if err := config.SetFolderBeadsUpstream(workingDir, req.Upstream); err != nil {
			h.writeBeadsError(w, r, err)
			return
		}
	}

	upstream := req.Upstream
	if upstream == "" {
		upstream = "none"
	}
	pull, push, sync := config.FolderBeadsPrompts(workingDir)
	pullArgs, pushArgs, syncArgs := config.FolderBeadsPromptArgs(workingDir)
	writeJSONOK(w, beadsUpstreamResponse{
		Upstream:       upstream,
		PullPrompt:     pull,
		PushPrompt:     push,
		SyncPrompt:     sync,
		PullPromptArgs: pullArgs,
		PushPromptArgs: pushArgs,
		SyncPromptArgs: syncArgs,
	})
}

// beadsSyncRequest is the JSON body for POST /api/issues/sync.
// Action must be "pull", "push", "sync", or "status".
type beadsSyncRequest struct {
	Action string `json:"action"`
}

// beadsSyncResponse carries the captured bd output on success.
type beadsSyncResponse struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

// HandleBeadsSync handles POST /api/issues/sync?working_dir=... It runs the
// configured upstream's pull/push/sync/status command for the folder. The
// integration is read authoritatively from folders.json — the client only
// chooses the action.
// Requires authentication via the standard auth middleware (same as other API endpoints).
func (h *Handlers) HandleBeadsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

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

	var req beadsSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	// The integration is read from folders.json, never trusted from the client.
	upstream := config.FolderBeadsUpstream(workingDir)
	if upstream == "" || upstream == "none" {
		writeErrorJSON(w, http.StatusInternalServerError, "", "no upstream task system is configured for this folder")
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

	out, err := h.beadsClient().Sync(r.Context(), workingDir, upstream, req.Action)
	if err != nil {
		h.writeBeadsError(w, r, err)
		return
	}

	writeJSONOK(w, beadsSyncResponse{OK: true, Output: out})
}
