package handlers

import (
	"encoding/json"
	"net/http"

	configPkg "github.com/inercia/mitto/internal/config"
)

// HandleWorkspaceMetadata handles GET and PUT /api/workspaces/{uuid}/metadata.
func (h *Handlers) HandleWorkspaceMetadata(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	ws := h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleWorkspaceMetadataGet(w, r, ws.WorkingDir)
	case http.MethodPut:
		h.handleWorkspaceMetadataPut(w, r, ws.WorkingDir)
	default:
		methodNotAllowed(w)
	}
}

// handleWorkspaceMetadataGet handles GET /api/workspaces/{uuid}/metadata.
// Returns workspace metadata (description, URL) from the .mittorc file.
func (h *Handlers) handleWorkspaceMetadataGet(w http.ResponseWriter, r *http.Request, workingDir string) {
	// Load workspace RC file
	rc, err := configPkg.LoadWorkspaceRC(workingDir)
	if err != nil {
		// Log error but return empty metadata
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Failed to load workspace RC for metadata", "working_dir", workingDir, "error", err)
		}
		writeJSONOK(w, map[string]interface{}{})
		return
	}

	if rc == nil || rc.Metadata == nil {
		writeJSONOK(w, map[string]interface{}{})
		return
	}

	writeJSONOK(w, rc.Metadata)
}

// handleWorkspaceMetadataPut handles PUT /api/workspaces/{uuid}/metadata.
// Saves description, URL, and group to the workspace .mittorc file.
func (h *Handlers) handleWorkspaceMetadataPut(w http.ResponseWriter, r *http.Request, workingDir string) {
	var req struct {
		Description string `json:"description"`
		URL         string `json:"url"`
		Group       string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	if err := configPkg.SaveWorkspaceMetadata(workingDir, req.Description, req.URL, req.Group); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save workspace metadata", "working_dir", workingDir, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save metadata: "+err.Error())
		return
	}

	// Invalidate the workspace RC cache so subsequent reads pick up the new data
	if h.deps.SessionManager != nil {
		h.deps.SessionManager.InvalidateWorkspaceRC(workingDir)
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("Workspace metadata saved", "working_dir", workingDir)
	}

	writeJSONOK(w, map[string]string{"status": "ok"})
}
