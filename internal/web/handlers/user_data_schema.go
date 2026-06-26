package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

// HandleWorkspaceUserDataSchema dispatches GET and PUT /api/workspace/user-data-schema.
func (h *Handlers) HandleWorkspaceUserDataSchema(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.HandleWorkspaceUserDataSchemaGet(w, r)
	case http.MethodPut:
		h.HandleWorkspaceUserDataSchemaPut(w, r)
	default:
		methodNotAllowed(w)
	}
}

// HandleWorkspaceUserDataSchemaGet handles GET /api/workspace/user-data-schema?working_dir=...
func (h *Handlers) HandleWorkspaceUserDataSchemaGet(w http.ResponseWriter, r *http.Request) {
	// Get the working directory from query parameter
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir query parameter is required")
		return
	}

	// Validate that this is a known workspace
	workingDir = strings.TrimSpace(workingDir)
	workspace := h.deps.SessionManager.GetWorkspace(workingDir)
	if workspace == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Unknown workspace")
		return
	}

	// Get the schema from workspace RC
	schema := h.deps.SessionManager.GetUserDataSchema(workingDir)

	// Return empty schema if none defined (no attributes allowed - validation will reject any)
	if schema == nil {
		writeJSONOK(w, map[string]interface{}{
			"fields":      []interface{}{},
			"working_dir": workingDir,
		})
		return
	}

	writeJSONOK(w, map[string]interface{}{
		"fields":      schema.Fields,
		"working_dir": workingDir,
	})
}

// HandleWorkspaceUserDataSchemaPut handles PUT /api/workspace/user-data-schema.
// Saves the user data schema to the workspace .mittorc file.
func (h *Handlers) HandleWorkspaceUserDataSchemaPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkingDir string                       `json:"working_dir"`
		Fields     []config.UserDataSchemaField `json:"fields"`
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

	// Validate each field
	for i, f := range req.Fields {
		if strings.TrimSpace(f.Name) == "" {
			writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("field[%d]: name is required", i))
			return
		}
		if f.Type != "" && !f.Type.IsValid() {
			writeErrorJSON(w, http.StatusBadRequest, "", fmt.Sprintf("field[%d]: invalid type %q (must be 'string' or 'url')", i, f.Type))
			return
		}
	}

	if err := config.SaveWorkspaceUserDataSchema(req.WorkingDir, req.Fields); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save workspace user data schema", "working_dir", req.WorkingDir, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save user data schema: "+err.Error())
		return
	}

	// Invalidate the workspace RC cache so subsequent reads pick up the new data
	if h.deps.SessionManager != nil {
		h.deps.SessionManager.InvalidateWorkspaceRC(req.WorkingDir)
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("Workspace user data schema saved", "working_dir", req.WorkingDir, "fields", len(req.Fields))
	}

	writeJSONOK(w, map[string]string{"status": "ok"})
}
