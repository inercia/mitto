package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	configPkg "github.com/inercia/mitto/internal/config"
)

// HandleWorkspaceMetadata handles GET and PUT /api/workspace-metadata.
func (h *Handlers) HandleWorkspaceMetadata(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleWorkspaceMetadataGet(w, r)
	case http.MethodPut:
		h.handleWorkspaceMetadataPut(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleWorkspaceMetadataGet handles GET /api/workspace-metadata?working_dir=...
// Returns workspace metadata (description, URL) from the .mittorc file.
func (h *Handlers) handleWorkspaceMetadataGet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir query parameter is required")
		return
	}

	workingDir = strings.TrimSpace(workingDir)

	// Validate that this is a known workspace
	workspace := h.deps.SessionManager.GetWorkspace(workingDir)
	if workspace == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Unknown workspace")
		return
	}

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

// handleWorkspaceMetadataPut handles PUT /api/workspace-metadata.
// Saves description and URL to the workspace .mittorc file.
func (h *Handlers) handleWorkspaceMetadataPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkingDir  string `json:"working_dir"`
		Description string `json:"description"`
		URL         string `json:"url"`
		Group       string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}
	if req.WorkingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	req.WorkingDir = strings.TrimSpace(req.WorkingDir)

	// Validate that this is a known workspace
	workspace := h.deps.SessionManager.GetWorkspace(req.WorkingDir)
	if workspace == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Unknown workspace")
		return
	}

	if err := configPkg.SaveWorkspaceMetadata(req.WorkingDir, req.Description, req.URL, req.Group); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save workspace metadata", "working_dir", req.WorkingDir, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save metadata: "+err.Error())
		return
	}

	// Invalidate the workspace RC cache so subsequent reads pick up the new data
	if h.deps.SessionManager != nil {
		h.deps.SessionManager.InvalidateWorkspaceRC(req.WorkingDir)
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("Workspace metadata saved", "working_dir", req.WorkingDir)
	}

	writeJSONOK(w, map[string]string{"status": "ok"})
}
