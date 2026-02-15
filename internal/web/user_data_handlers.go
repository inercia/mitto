package web

import (
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/session"
)

// handleSessionUserData handles GET and PUT /api/sessions/{id}/user-data
func (s *Server) handleSessionUserData(w http.ResponseWriter, r *http.Request, sessionID string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetSessionUserData(w, r, sessionID)
	case http.MethodPut:
		s.handlePutSessionUserData(w, r, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// handleGetSessionUserData handles GET /api/sessions/{id}/user-data
func (s *Server) handleGetSessionUserData(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	data, err := store.GetUserData(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to get user data", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to get user data", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, data)
}

// UserDataUpdateRequest represents the request body for PUT /api/sessions/{id}/user-data
type UserDataUpdateRequest struct {
	Attributes []session.UserDataAttribute `json:"attributes"`
}

// handlePutSessionUserData handles PUT /api/sessions/{id}/user-data
func (s *Server) handlePutSessionUserData(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req UserDataUpdateRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Get the session's working directory to find the workspace schema
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to get session metadata", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to get session metadata", http.StatusInternalServerError)
		return
	}

	// Create user data from request
	userData := &session.UserData{
		Attributes: req.Attributes,
	}

	// Validate against workspace schema if available
	schema := s.sessionManager.GetUserDataSchema(meta.WorkingDir)
	if err := userData.Validate(schema); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	// Save user data
	if err := store.SetUserData(sessionID, userData); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to save user data", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to save user data", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, userData)
}

// handleWorkspaceUserDataSchema handles GET /api/workspace/user-data-schema
func (s *Server) handleWorkspaceUserDataSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Get the working directory from query parameter
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		http.Error(w, "working_dir query parameter is required", http.StatusBadRequest)
		return
	}

	// Validate that this is a known workspace
	workingDir = strings.TrimSpace(workingDir)
	workspace := s.sessionManager.GetWorkspace(workingDir)
	if workspace == nil {
		http.Error(w, "Unknown workspace", http.StatusNotFound)
		return
	}

	// Get the schema from workspace RC
	schema := s.sessionManager.GetUserDataSchema(workingDir)

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
