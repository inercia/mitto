package handlers

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

// HandleSessionSettings handles GET and PATCH /api/sessions/{id}/settings.
func (h *Handlers) HandleSessionSettings(w http.ResponseWriter, r *http.Request, sessionID string) {
	switch r.Method {
	case http.MethodGet:
		h.HandleGetSessionSettings(w, r, sessionID)
	case http.MethodPatch:
		h.HandleUpdateSessionSettings(w, r, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// HandleGetSessionSettings handles GET /api/sessions/{id}/settings.
// Returns the current advanced settings for a session.
func (h *Handlers) HandleGetSessionSettings(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get session metadata", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get session metadata")
		return
	}

	// Return empty object instead of null for nil map
	settings := meta.AdvancedSettings
	if settings == nil {
		settings = map[string]bool{}
	}

	writeJSONOK(w, SettingsResponse{Settings: settings})
}

// HandleUpdateSessionSettings handles PATCH /api/sessions/{id}/settings.
// Performs a partial update of advanced settings (merges with existing settings).
func (h *Handlers) HandleUpdateSessionSettings(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req SettingsUpdateRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
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
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to update session settings", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to update session settings")
		return
	}

	// Get updated metadata to return the full settings
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get updated metadata", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get updated settings")
		return
	}

	// Broadcast the settings change to all connected clients
	if h.deps.BroadcastSettingsUpdated != nil {
		h.deps.BroadcastSettingsUpdated(sessionID, meta.AdvancedSettings)
	}

	// Return the full settings after update
	settings := meta.AdvancedSettings
	if settings == nil {
		settings = map[string]bool{}
	}

	writeJSONOK(w, SettingsResponse{Settings: settings})
}
