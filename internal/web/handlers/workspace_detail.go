package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// HandleWorkspaceEffectiveRunnerConfig handles GET /api/workspaces/{uuid}/effective-runner-config.
// The {uuid} wildcard is extracted by the mux via r.PathValue("uuid").
func (h *Handlers) HandleWorkspaceEffectiveRunnerConfig(w http.ResponseWriter, r *http.Request) {
	h.handleEffectiveRunnerConfig(w, r, r.PathValue("uuid"))
}

// HandleWorkspaceRestartACP handles POST /api/workspaces/{uuid}/restart-acp.
// The {uuid} wildcard is extracted by the mux via r.PathValue("uuid").
func (h *Handlers) HandleWorkspaceRestartACP(w http.ResponseWriter, r *http.Request) {
	h.handleRestartWorkspaceACP(w, r, r.PathValue("uuid"))
}

// HandleWorkspaceACPStatus handles GET /api/workspaces/{uuid}/acp-status.
// The {uuid} wildcard is extracted by the mux via r.PathValue("uuid").
func (h *Handlers) HandleWorkspaceACPStatus(w http.ResponseWriter, r *http.Request) {
	h.handleWorkspaceACPStatus(w, r, r.PathValue("uuid"))
}

// handleWorkspaceACPStatus handles GET /api/workspaces/{uuid}/acp-status.
// Reports whether the workspace has a live shared ACP process, so the UI can
// decide whether an MCP install/remove needs an ACP restart to take effect.
func (h *Handlers) handleWorkspaceACPStatus(w http.ResponseWriter, r *http.Request, workspaceUUID string) {
	ws := h.deps.SessionManager.GetWorkspaceByUUID(workspaceUUID)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}
	alive := false
	if h.deps.HasLiveWorkspaceACP != nil {
		alive = h.deps.HasLiveWorkspaceACP(workspaceUUID)
	}
	writeJSONOK(w, map[string]interface{}{"alive": alive})
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
	ws := h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
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
	// Verify workspace exists
	ws := h.deps.SessionManager.GetWorkspaceByUUID(workspaceUUID)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}

	// Check if the process manager exists (nil RestartWorkspaceACP means unavailable).
	if h.deps.RestartWorkspaceACP == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "ACP process manager not available")
		return
	}

	// Restart the shared ACP process
	if err := h.deps.RestartWorkspaceACP(workspaceUUID); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to restart ACP process for workspace",
				"workspace_uuid", workspaceUUID,
				"error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to restart ACP: "+err.Error())
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

// HandleFolderGroup handles PUT /api/workspaces/{uuid}/folder-group.
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

	uuid := r.PathValue("uuid")
	ws := h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}
	workingDir := ws.WorkingDir

	var req struct {
		Group string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	group := strings.TrimSpace(req.Group)

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
