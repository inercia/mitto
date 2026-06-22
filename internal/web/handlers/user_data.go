package handlers

import (
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// UserDataUpdateRequest represents the request body for PUT /api/sessions/{id}/user-data
type UserDataUpdateRequest struct {
	Attributes []session.UserDataAttribute `json:"attributes"`
}

// HandleSessionUserData handles GET and PUT /api/sessions/{id}/user-data
func (h *Handlers) HandleSessionUserData(w http.ResponseWriter, r *http.Request, sessionID string) {
	switch r.Method {
	case http.MethodGet:
		h.HandleGetSessionUserData(w, r, sessionID)
	case http.MethodPut:
		h.HandlePutSessionUserData(w, r, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// HandleGetSessionUserData handles GET /api/sessions/{id}/user-data
func (h *Handlers) HandleGetSessionUserData(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := h.deps.Store
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
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get user data", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to get user data", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, data)
}

// HandlePutSessionUserData handles PUT /api/sessions/{id}/user-data
func (h *Handlers) HandlePutSessionUserData(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req UserDataUpdateRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	store := h.deps.Store
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
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get session metadata", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to get session metadata", http.StatusInternalServerError)
		return
	}

	// Create user data from request
	userData := &session.UserData{
		Attributes: req.Attributes,
	}

	// Validate against workspace schema if available. Relative filename paths are
	// resolved against the conversation's working directory.
	schema := h.deps.SessionManager.GetUserDataSchema(meta.WorkingDir)
	if err := userData.Validate(schema, meta.WorkingDir); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	// Save user data
	if err := store.SetUserData(sessionID, userData); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save user data", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to save user data", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, userData)
}
