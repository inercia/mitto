package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/modelcache"
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

// HandleWorkspaceModels handles GET /api/workspaces/{uuid}/models.
func (h *Handlers) HandleWorkspaceModels(w http.ResponseWriter, r *http.Request) {
	h.handleWorkspaceModels(w, r, r.PathValue("uuid"))
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

// WorkspaceModelsResponse is the last-seen advertised catalog for a workspace's ACP server.
type WorkspaceModelsResponse struct {
	ACPServer string `json:"acp_server"`
	// WorkingDir echoes the exact folder scope used for the cache lookup.
	WorkingDir string             `json:"working_dir"`
	Models     []modelcache.Model `json:"models"`
	Live       bool               `json:"live"`
	UpdatedAt  *time.Time         `json:"updated_at,omitempty"`

	// Additive metadata. CatalogState is absent|available|policy_empty
	// ("empty" is a trusted explicit-empty record for the exact
	// server and folder scope).
	CatalogState        string   `json:"catalog_state"`
	HiddenCount         int      `json:"hidden_count"`
	HiddenSavedIDs      []string `json:"hidden_saved_ids,omitempty"`
	UnavailableSavedIDs []string `json:"unavailable_saved_ids,omitempty"`
}

// handleWorkspaceModels serves the cached catalog independently of any one
// running session. Session iteration order is unspecified, so live sessions
// must not be used to select the returned model list.
func (h *Handlers) handleWorkspaceModels(w http.ResponseWriter, r *http.Request, workspaceUUID string) {
	ws := h.deps.SessionManager.GetWorkspaceByUUID(workspaceUUID)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}

	response := WorkspaceModelsResponse{
		ACPServer:    ws.ACPServer,
		WorkingDir:   ws.WorkingDir,
		Models:       []modelcache.Model{},
		CatalogState: "absent",
	}
	if h.deps.ModelCache != nil {
		if catalog, ok := h.deps.ModelCache.Get(ws.ACPServer, ws.WorkingDir); ok {
			updatedAt := catalog.UpdatedAt
			response.UpdatedAt = &updatedAt
			h.applyHiddenModelPolicy(&response, ws, catalog.Models)
		}
	}
	if h.deps.HasLiveWorkspaceACP != nil {
		response.Live = h.deps.HasLiveWorkspaceACP(workspaceUUID)
	}
	writeJSONOK(w, response)
}

// applyHiddenModelPolicy filters the raw cached models through the configured
// ACP server's hidden-model policy and fills the additive metadata. A policy
// that fails to compile fails closed (nothing selectable).
func (h *Handlers) applyHiddenModelPolicy(resp *WorkspaceModelsResponse, ws *configPkg.WorkspaceSettings, raw []modelcache.Model) {
	var rules []string
	if _, cfg := h.deps.SessionManager.GetGlobalRunnerInfo(); cfg != nil {
		if srv, err := cfg.GetServer(ws.ACPServer); err == nil && srv != nil {
			rules = srv.HiddenModels
		}
	}
	policy, err := configPkg.CompileHiddenModels(ws.ACPServer, rules)
	failClosed := err != nil
	if failClosed && h.deps.Logger != nil {
		h.deps.Logger.Warn("hidden-model policy failed to compile; failing closed",
			"acp_server", ws.ACPServer, "error", err)
	}
	hides := func(id, name string) bool {
		return failClosed || (policy != nil && policy.Hides(id, name))
	}
	selectable := make([]modelcache.Model, 0, len(raw))
	byID := make(map[string]modelcache.Model, len(raw))
	for _, m := range raw {
		byID[m.ID] = m
		if !hides(m.ID, m.Name) {
			selectable = append(selectable, m)
		}
	}
	resp.Models = selectable
	resp.HiddenCount = len(raw) - len(selectable)
	resp.CatalogState = "available"
	if len(raw) == 0 {
		resp.CatalogState = "empty"
	} else if len(selectable) == 0 {
		resp.CatalogState = "policy_empty"
	}
	selectableIDs := make(map[string]bool, len(selectable))
	for _, m := range selectable {
		selectableIDs[m.ID] = true
	}
	for _, o := range ws.ModelOverrides {
		if o.ACPServer != ws.ACPServer || o.ModelID == "" {
			continue
		}
		name := ""
		if m, ok := byID[o.ModelID]; ok {
			name = m.Name
		}
		switch {
		case hides(o.ModelID, name):
			resp.HiddenSavedIDs = append(resp.HiddenSavedIDs, o.ModelID)
		case !selectableIDs[o.ModelID]:
			resp.UnavailableSavedIDs = append(resp.UnavailableSavedIDs, o.ModelID)
		}
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
