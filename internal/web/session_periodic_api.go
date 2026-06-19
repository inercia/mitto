package web

import (
	"encoding/json"
	"net/http"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// PeriodicPromptRequest is the request body for creating/updating a periodic prompt.
type PeriodicPromptRequest struct {
	Prompt        string            `json:"prompt"`
	PromptName    string            `json:"prompt_name,omitempty"`
	Frequency     session.Frequency `json:"frequency"`
	Enabled       bool              `json:"enabled"`
	FreshContext  bool              `json:"fresh_context,omitempty"`
	MaxIterations int               `json:"max_iterations,omitempty"`
	// Trigger selects how the prompt fires: "" or "schedule" (frequency-based, default)
	// vs "onCompletion" (event-driven, after the agent stops + DelaySeconds).
	Trigger session.PeriodicTrigger `json:"trigger,omitempty"`
	// DelaySeconds is the wait after the agent stops before the next run (onCompletion only).
	// Clamped to the global floor on write.
	DelaySeconds int `json:"delay_seconds,omitempty"`
	// MaxDurationSeconds is the wall-clock cap since iterating started (0 = unlimited).
	MaxDurationSeconds int `json:"max_duration_seconds,omitempty"`
}

// PeriodicPromptPatchRequest is the request body for partial updates.
type PeriodicPromptPatchRequest struct {
	Prompt        *string            `json:"prompt,omitempty"`
	PromptName    *string            `json:"prompt_name,omitempty"`
	Frequency     *session.Frequency `json:"frequency,omitempty"`
	Enabled       *bool              `json:"enabled,omitempty"`
	FreshContext  *bool              `json:"fresh_context,omitempty"`
	MaxIterations *int               `json:"max_iterations,omitempty"`
	// Trigger, DelaySeconds, MaxDurationSeconds are partial updates for the on-completion fields.
	Trigger            *session.PeriodicTrigger `json:"trigger,omitempty"`
	DelaySeconds       *int                     `json:"delay_seconds,omitempty"`
	MaxDurationSeconds *int                     `json:"max_duration_seconds,omitempty"`
}

// periodicDelayFloor returns the configured global floor for the on-completion delay.
// Falls back to the package default when the periodic runner is unavailable (e.g. tests).
func (s *Server) periodicDelayFloor() int {
	if s.periodicRunner != nil {
		return s.periodicRunner.MinPeriodicCompletionDelaySeconds()
	}
	return configPkg.DefaultMinPeriodicCompletionDelaySeconds
}

// handleSessionPeriodic handles periodic prompt operations for a session.
// Routes: GET, PUT, PATCH, DELETE /api/sessions/{id}/periodic
// Route: POST /api/sessions/{id}/periodic/run-now (immediate delivery)
func (s *Server) handleSessionPeriodic(w http.ResponseWriter, r *http.Request, sessionID, subPath string) {
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Verify session exists
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get session", http.StatusInternalServerError)
		return
	}

	// Prevent setting periodic on child sessions - only parents/top-level sessions can be periodic
	if r.Method != http.MethodGet && meta.ParentSessionID != "" {
		http.Error(w, "Cannot set periodic on a child conversation. Only parent or top-level conversations can be periodic.", http.StatusBadRequest)
		return
	}

	// Handle run-now sub-path
	if subPath == "run-now" {
		s.handleRunPeriodicNow(w, r, sessionID)
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
		Prompt:             req.Prompt,
		PromptName:         req.PromptName,
		Frequency:          req.Frequency,
		Enabled:            req.Enabled,
		FreshContext:       req.FreshContext,
		MaxIterations:      req.MaxIterations,
		Trigger:            req.Trigger,
		DelaySeconds:       req.DelaySeconds,
		MaxDurationSeconds: req.MaxDurationSeconds,
	}
	// Clamp the on-completion delay to the global floor on write (no-op for schedule trigger).
	p.ClampDelay(s.periodicDelayFloor())

	if err := ps.Set(p); err != nil {
		if err == session.ErrInvalidFrequency || err == session.ErrPromptEmpty || err == session.ErrInvalidMaxIterations ||
			err == session.ErrInvalidTrigger || err == session.ErrInvalidDelay || err == session.ErrInvalidMaxDuration {
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

	// If the session has no title, trigger title generation from the periodic prompt.
	if s.sessionManager != nil && SessionNeedsTitle(s.Store(), sessionID) {
		if bs := s.sessionManager.GetSession(sessionID); bs != nil {
			bs.TriggerTitleGenerationFromPeriodic(req.Prompt, req.PromptName)
		}
	}

	// Broadcast periodic state change to all clients (includes full config)
	s.BroadcastPeriodicUpdated(sessionID, updated)

	// Kick off the very first run for a fresh onCompletion conversation.
	if s.periodicRunner != nil {
		s.periodicRunner.BootstrapOnCompletion(sessionID)
	}

	writeJSONOK(w, updated)
}

// handlePatchPeriodic handles PATCH /api/sessions/{id}/periodic
func (s *Server) handlePatchPeriodic(w http.ResponseWriter, r *http.Request, sessionID string, ps *session.PeriodicStore) {
	var req PeriodicPromptPatchRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	// Clamp the on-completion delay to the global floor on write. The effective trigger
	// is the patched value when provided, otherwise the currently-stored trigger.
	if req.DelaySeconds != nil {
		floor := s.periodicDelayFloor()
		if *req.DelaySeconds < floor {
			effTrigger := session.PeriodicTrigger("")
			if req.Trigger != nil {
				effTrigger = *req.Trigger
			} else if cur, err := ps.Get(); err == nil && cur != nil {
				effTrigger = cur.Trigger
			}
			if effTrigger == session.TriggerOnCompletion {
				clamped := floor
				req.DelaySeconds = &clamped
			}
		}
	}

	if err := ps.Update(req.Prompt, req.PromptName, req.Frequency, req.Enabled, req.FreshContext, req.MaxIterations, req.Trigger, req.DelaySeconds, req.MaxDurationSeconds); err != nil {
		if err == session.ErrPeriodicNotFound {
			http.Error(w, "No periodic prompt configured", http.StatusNotFound)
			return
		}
		if err == session.ErrInvalidFrequency || err == session.ErrPromptEmpty || err == session.ErrInvalidMaxIterations ||
			err == session.ErrInvalidTrigger || err == session.ErrInvalidDelay || err == session.ErrInvalidMaxDuration {
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

	// If the session has no title, trigger title generation from the periodic prompt.
	if s.sessionManager != nil && SessionNeedsTitle(s.Store(), sessionID) {
		if bs := s.sessionManager.GetSession(sessionID); bs != nil {
			var pPrompt, pName string
			if updated != nil {
				pPrompt = updated.Prompt
				pName = updated.PromptName
			}
			bs.TriggerTitleGenerationFromPeriodic(pPrompt, pName)
		}
	}

	// Broadcast periodic state change to all clients (includes full config)
	s.BroadcastPeriodicUpdated(sessionID, updated)

	// Kick off the very first run for a fresh onCompletion conversation.
	if s.periodicRunner != nil {
		s.periodicRunner.BootstrapOnCompletion(sessionID)
	}

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

// RunPeriodicNowRequest is the optional request body for POST /api/sessions/{id}/periodic/run-now.
type RunPeriodicNowRequest struct {
	ResetTimer *bool `json:"reset_timer,omitempty"`
}

// handleRunPeriodicNow handles POST /api/sessions/{id}/periodic/run-now
// Triggers immediate delivery of the periodic prompt, bypassing the normal schedule.
func (s *Server) handleRunPeriodicNow(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Check if periodic runner is available
	if s.periodicRunner == nil {
		http.Error(w, "Periodic runner not available", http.StatusInternalServerError)
		return
	}

	// Parse optional request body to determine whether to reset the countdown timer.
	// Default is true (matches existing behaviour).
	var req RunPeriodicNowRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}
	resetTimer := true // default: reset the countdown after a manual run
	if req.ResetTimer != nil {
		resetTimer = *req.ResetTimer
	}

	// Trigger immediate delivery
	if err := s.periodicRunner.TriggerNow(sessionID, resetTimer); err != nil {
		switch err {
		case session.ErrPeriodicNotFound:
			http.Error(w, "No periodic prompt configured", http.StatusNotFound)
		case ErrPeriodicNotEnabled:
			http.Error(w, "Periodic is not enabled for this session", http.StatusBadRequest)
		case ErrSessionBusy:
			http.Error(w, "Session is currently processing a prompt", http.StatusConflict)
		default:
			if s.logger != nil {
				s.logger.Error("Failed to trigger periodic prompt", "error", err, "session_id", sessionID)
			}
			http.Error(w, "Failed to trigger periodic prompt", http.StatusInternalServerError)
		}
		return
	}

	// Return success with the updated periodic config
	store := s.Store()
	if store != nil {
		periodicStore := store.Periodic(sessionID)
		if updated, err := periodicStore.Get(); err == nil {
			writeJSONOK(w, updated)
			return
		}
	}

	// Fallback: just return success status
	writeNoContent(w)
}
