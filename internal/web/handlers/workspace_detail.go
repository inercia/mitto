package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// HandleWorkspaceDetail dispatches sub-resource requests under
// /api/workspaces/{uuid}/... to the appropriate handler.
func (h *Handlers) HandleWorkspaceDetail(w http.ResponseWriter, r *http.Request) {
	// Extract the path after "/api/workspaces/", stripping apiPrefix first (mirrors handleSessionDetail).
	path := r.URL.Path
	path = strings.TrimPrefix(path, h.deps.APIPrefix)
	path = strings.TrimPrefix(path, "/api/workspaces/")

	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	uuid := parts[0]
	subPath := parts[1]

	switch subPath {
	case "effective-runner-config":
		h.handleEffectiveRunnerConfig(w, r, uuid)
	case "restart-acp":
		h.handleRestartWorkspaceACP(w, r, uuid)
	default:
		http.NotFound(w, r)
	}
}

// EffectiveRunnerConfigResponse is the response for GET /api/workspaces/{uuid}/effective-runner-config.
// It returns the resolved runner config from global + agent levels (no workspace overrides),
// so the UI can show what restrictions a workspace would inherit.
type EffectiveRunnerConfigResponse struct {
	RunnerType   string                        `json:"runner_type"`
	Restrictions *configPkg.RunnerRestrictions `json:"restrictions,omitempty"`
}

// handleEffectiveRunnerConfig handles GET /api/workspaces/{uuid}/effective-runner-config.
// Returns the effective runner config resolved from global and agent levels only.
func (h *Handlers) handleEffectiveRunnerConfig(w http.ResponseWriter, r *http.Request, uuid string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	ws := h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}

	// Get global runner configs
	sm := h.deps.SessionManager
	globalRunnersByType, mittoConfig := sm.GetGlobalRunnerInfo()

	// Get agent-specific runner configs
	var agentRunnersByType map[string]*configPkg.WorkspaceRunnerConfig
	if mittoConfig != nil && ws.ACPServer != "" {
		if server, err := mittoConfig.GetServer(ws.ACPServer); err == nil && server != nil {
			agentRunnersByType = server.RestrictedRunners
		}
	}

	// Resolve global + agent levels only (no workspace level)
	resolved := runner.ResolveEffectiveConfig(globalRunnersByType, agentRunnersByType)

	resp := EffectiveRunnerConfigResponse{
		RunnerType:   resolved.Type,
		Restrictions: resolved.Restrictions,
	}

	writeJSONOK(w, resp)
}

// handleRestartWorkspaceACP handles POST /api/workspaces/{uuid}/restart-acp.
// Restarts the shared ACP process for a workspace so that MCP changes take effect.
func (h *Handlers) handleRestartWorkspaceACP(w http.ResponseWriter, r *http.Request, workspaceUUID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Verify workspace exists
	ws := h.deps.SessionManager.GetWorkspaceByUUID(workspaceUUID)
	if ws == nil {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}

	// Check if the process manager exists (nil RestartWorkspaceACP means unavailable).
	if h.deps.RestartWorkspaceACP == nil {
		http.Error(w, "ACP process manager not available", http.StatusInternalServerError)
		return
	}

	// Restart the shared ACP process
	if err := h.deps.RestartWorkspaceACP(workspaceUUID); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to restart ACP process for workspace",
				"workspace_uuid", workspaceUUID,
				"error", err)
		}
		http.Error(w, "Failed to restart ACP: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("Restarted ACP process for workspace via API",
			"workspace_uuid", workspaceUUID,
			"acp_server", ws.ACPServer)
	}

	writeJSONOK(w, map[string]interface{}{
		"success": true,
		"message": "ACP process restarted successfully",
	})
}

// HandleFolderGroup handles PUT /api/folder-group.
// Sets (or clears) the folder-level organizational group label shared by all
// workspaces in the given working directory. An empty group clears the
// assignment ("ungrouped"). The group is folder-level: SetWorkspaces hoists it
// into the authoritative folders.json (and merges it back on load), so updating
// the in-memory workspaces and re-saving is sufficient.
func (h *Handlers) HandleFolderGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}

	var req struct {
		WorkingDir string `json:"working_dir"`
		Group      string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	workingDir := strings.TrimSpace(req.WorkingDir)
	group := strings.TrimSpace(req.Group)
	if workingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}

	// Validate that this is a known workspace directory.
	if h.deps.SessionManager.GetWorkspace(workingDir) == nil {
		http.Error(w, "Unknown workspace", http.StatusNotFound)
		return
	}

	// Update the group on every workspace sharing this folder, then persist.
	// SetWorkspaces hoists the folder-level group into folders.json (shared by
	// all workspaces in the folder) and triggers the save callback.
	workspaces := h.deps.SessionManager.GetWorkspaces()
	for i := range workspaces {
		if workspaces[i].WorkingDir == workingDir {
			workspaces[i].Group = group
		}
	}
	h.deps.SessionManager.SetWorkspaces(workspaces)
	if h.deps.SyncConfigWorkspaces != nil {
		h.deps.SyncConfigWorkspaces()
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("Folder group updated", "working_dir", workingDir, "group", group)
	}

	writeJSONOK(w, map[string]string{"group": group})
}
