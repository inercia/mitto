package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// handleDeletePeriodic handles DELETE /api/sessions/{id}/periodic
func (h *Handlers) handleDeletePeriodic(w http.ResponseWriter, sessionID string, ps *session.PeriodicStore) {
	if err := ps.Delete(); err != nil {
		if err == session.ErrPeriodicNotFound {
			http.Error(w, "No periodic prompt configured", http.StatusNotFound)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to delete periodic prompt", "error", err)
		}
		http.Error(w, "Failed to delete periodic prompt", http.StatusInternalServerError)
		return
	}

	// Broadcast periodic disabled to all clients (nil means deleted)
	h.broadcastPeriodic(sessionID, nil)

	writeNoContent(w)
}

// handleRunPeriodicNow handles POST /api/sessions/{id}/periodic/run-now
// Triggers immediate delivery of the periodic prompt, bypassing the normal schedule.
func (h *Handlers) handleRunPeriodicNow(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Check if periodic runner is available
	if h.deps.TriggerPeriodicNow == nil {
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
	if err := h.deps.TriggerPeriodicNow(sessionID, resetTimer); err != nil {
		switch err {
		case session.ErrPeriodicNotFound:
			http.Error(w, "No periodic prompt configured", http.StatusNotFound)
		case h.deps.ErrPeriodicNotEnabled:
			http.Error(w, "Periodic is not enabled for this session", http.StatusBadRequest)
		case h.deps.ErrSessionBusy:
			http.Error(w, "Session is currently processing a prompt", http.StatusConflict)
		default:
			if h.deps.Logger != nil {
				h.deps.Logger.Error("Failed to trigger periodic prompt", "error", err, "session_id", sessionID)
			}
			http.Error(w, "Failed to trigger periodic prompt", http.StatusInternalServerError)
		}
		return
	}

	// Return success with the updated periodic config
	store := h.deps.Store
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
