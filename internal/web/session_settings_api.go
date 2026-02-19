package web

import (
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// SettingsResponse represents the response for GET/PATCH /api/sessions/{id}/settings.
type SettingsResponse struct {
	Settings map[string]bool `json:"settings"`
}

// SettingsUpdateRequest represents the request body for PATCH /api/sessions/{id}/settings.
type SettingsUpdateRequest struct {
	Settings map[string]bool `json:"settings"`
}

// handleSessionSettings handles GET and PATCH /api/sessions/{id}/settings.
func (s *Server) handleSessionSettings(w http.ResponseWriter, r *http.Request, sessionID string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetSessionSettings(w, r, sessionID)
	case http.MethodPatch:
		s.handleUpdateSessionSettings(w, r, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// handleGetSessionSettings handles GET /api/sessions/{id}/settings.
// Returns the current advanced settings for a session.
func (s *Server) handleGetSessionSettings(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

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

	// Return empty object instead of null for nil map
	settings := meta.AdvancedSettings
	if settings == nil {
		settings = map[string]bool{}
	}

	writeJSONOK(w, SettingsResponse{Settings: settings})
}

// handleUpdateSessionSettings handles PATCH /api/sessions/{id}/settings.
// Performs a partial update of advanced settings (merges with existing settings).
func (s *Server) handleUpdateSessionSettings(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req SettingsUpdateRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Update metadata with new settings (partial merge)
	err := store.UpdateMetadata(sessionID, func(meta *session.Metadata) {
		// Initialize map if nil
		if meta.AdvancedSettings == nil {
			meta.AdvancedSettings = make(map[string]bool)
		}
		// Merge new settings into existing
		for key, value := range req.Settings {
			meta.AdvancedSettings[key] = value
		}
	})
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to update session settings", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to update session settings", http.StatusInternalServerError)
		return
	}

	// Get updated metadata to return the full settings
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to get updated metadata", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to get updated settings", http.StatusInternalServerError)
		return
	}

	// Broadcast the settings change to all connected clients
	s.BroadcastSessionSettingsUpdated(sessionID, meta.AdvancedSettings)

	// Return the full settings after update
	settings := meta.AdvancedSettings
	if settings == nil {
		settings = map[string]bool{}
	}

	writeJSONOK(w, SettingsResponse{Settings: settings})
}
