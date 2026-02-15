package web

import (
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// PeriodicPromptRequest is the request body for creating/updating a periodic prompt.
type PeriodicPromptRequest struct {
	Prompt    string            `json:"prompt"`
	Frequency session.Frequency `json:"frequency"`
	Enabled   bool              `json:"enabled"`
}

// PeriodicPromptPatchRequest is the request body for partial updates.
type PeriodicPromptPatchRequest struct {
	Prompt    *string            `json:"prompt,omitempty"`
	Frequency *session.Frequency `json:"frequency,omitempty"`
	Enabled   *bool              `json:"enabled,omitempty"`
}

// handleSessionPeriodic handles periodic prompt operations for a session.
// Routes: GET, PUT, PATCH, DELETE /api/sessions/{id}/periodic
func (s *Server) handleSessionPeriodic(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Verify session exists
	if _, err := store.GetMetadata(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get session", http.StatusInternalServerError)
		return
	}

	periodicStore := store.Periodic(sessionID)

	switch r.Method {
	case http.MethodGet:
		s.handleGetPeriodic(w, periodicStore)
	case http.MethodPut:
		s.handleSetPeriodic(w, r, sessionID, periodicStore)
	case http.MethodPatch:
		s.handlePatchPeriodic(w, r, sessionID, periodicStore)
	case http.MethodDelete:
		s.handleDeletePeriodic(w, sessionID, periodicStore)
	default:
		methodNotAllowed(w)
	}
}

// handleGetPeriodic handles GET /api/sessions/{id}/periodic
func (s *Server) handleGetPeriodic(w http.ResponseWriter, ps *session.PeriodicStore) {
	p, err := ps.Get()
	if err != nil {
		if err == session.ErrPeriodicNotFound {
			http.Error(w, "No periodic prompt configured", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to get periodic prompt", "error", err)
		}
		http.Error(w, "Failed to get periodic prompt", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, p)
}

// handleSetPeriodic handles PUT /api/sessions/{id}/periodic
func (s *Server) handleSetPeriodic(w http.ResponseWriter, r *http.Request, sessionID string, ps *session.PeriodicStore) {
	var req PeriodicPromptRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	p := &session.PeriodicPrompt{
		Prompt:    req.Prompt,
		Frequency: req.Frequency,
		Enabled:   req.Enabled,
	}

	if err := ps.Set(p); err != nil {
		if err == session.ErrInvalidFrequency ||
			(err != nil && err.Error() == "prompt cannot be empty") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to set periodic prompt", "error", err)
		}
		http.Error(w, "Failed to set periodic prompt", http.StatusInternalServerError)
		return
	}

	// Return the updated periodic prompt
	updated, err := ps.Get()
	if err != nil {
		http.Error(w, "Failed to get updated periodic prompt", http.StatusInternalServerError)
		return
	}

	// Broadcast periodic state change to all clients (includes full config)
	s.BroadcastPeriodicUpdated(sessionID, updated)

	writeJSONOK(w, updated)
}

// handlePatchPeriodic handles PATCH /api/sessions/{id}/periodic
func (s *Server) handlePatchPeriodic(w http.ResponseWriter, r *http.Request, sessionID string, ps *session.PeriodicStore) {
	var req PeriodicPromptPatchRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if err := ps.Update(req.Prompt, req.Frequency, req.Enabled); err != nil {
		if err == session.ErrPeriodicNotFound {
			http.Error(w, "No periodic prompt configured", http.StatusNotFound)
			return
		}
		if err == session.ErrInvalidFrequency {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to update periodic prompt", "error", err)
		}
		http.Error(w, "Failed to update periodic prompt", http.StatusInternalServerError)
		return
	}

	// Return the updated periodic prompt
	updated, err := ps.Get()
	if err != nil {
		http.Error(w, "Failed to get updated periodic prompt", http.StatusInternalServerError)
		return
	}

	// Broadcast periodic state change to all clients (includes full config)
	s.BroadcastPeriodicUpdated(sessionID, updated)

	writeJSONOK(w, updated)
}

// handleDeletePeriodic handles DELETE /api/sessions/{id}/periodic
func (s *Server) handleDeletePeriodic(w http.ResponseWriter, sessionID string, ps *session.PeriodicStore) {
	if err := ps.Delete(); err != nil {
		if err == session.ErrPeriodicNotFound {
			http.Error(w, "No periodic prompt configured", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to delete periodic prompt", "error", err)
		}
		http.Error(w, "Failed to delete periodic prompt", http.StatusInternalServerError)
		return
	}

	// Broadcast periodic disabled to all clients (nil means deleted)
	s.BroadcastPeriodicUpdated(sessionID, nil)

	writeNoContent(w)
}
