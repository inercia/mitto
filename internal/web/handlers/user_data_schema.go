package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

// HandleWorkspaceUserDataSchema dispatches GET and PUT /api/workspaces/{uuid}/user-data-schema.
func (h *Handlers) HandleWorkspaceUserDataSchema(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	ws := h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Workspace not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.HandleWorkspaceUserDataSchemaGet(w, r, ws.WorkingDir)
	case http.MethodPut:
		h.HandleWorkspaceUserDataSchemaPut(w, r, ws.WorkingDir)
	default:
		methodNotAllowed(w)
	}
}

// HandleWorkspaceUserDataSchemaGet handles GET /api/workspaces/{uuid}/user-data-schema.
func (h *Handlers) HandleWorkspaceUserDataSchemaGet(w http.ResponseWriter, r *http.Request, workingDir string) {
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

// HandleWorkspaceUserDataSchemaPut handles PUT /api/workspaces/{uuid}/user-data-schema.
// Saves the user data schema to the workspace .mittorc file.
func (h *Handlers) HandleWorkspaceUserDataSchemaPut(w http.ResponseWriter, r *http.Request, workingDir string) {
	var req struct {
		Fields []config.UserDataSchemaField `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
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

	if err := config.SaveWorkspaceUserDataSchema(workingDir, req.Fields); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save workspace user data schema", "working_dir", workingDir, "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save user data schema: "+err.Error())
		return
	}

	// Invalidate the workspace RC cache so subsequent reads pick up the new data
	if h.deps.SessionManager != nil {
		h.deps.SessionManager.InvalidateWorkspaceRC(workingDir)
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Info("Workspace user data schema saved", "working_dir", workingDir, "fields", len(req.Fields))
	}

	writeJSONOK(w, map[string]string{"status": "ok"})
}
