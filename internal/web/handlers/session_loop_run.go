package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// handleDeleteLoop handles DELETE /api/sessions/{id}/loop
func (h *Handlers) handleDeleteLoop(w http.ResponseWriter, sessionID string, ps *session.LoopStore) {
	if err := ps.Delete(); err != nil {
		if err == session.ErrLoopNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No loop prompt configured")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to delete loop prompt", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to delete loop prompt")
		return
	}

	// Broadcast loop disabled to all clients (nil means deleted)
	h.broadcastLoop(sessionID, nil)

	writeNoContent(w)
}

// handleRunLoopNow handles POST /api/sessions/{id}/loop/run-now
// Triggers immediate delivery of the loop prompt, bypassing the normal schedule.
func (h *Handlers) handleRunLoopNow(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Check if loop runner is available
	if h.deps.TriggerLoopNow == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Loop runner not available")
		return
	}

	// Parse optional request body to determine whether to reset the countdown timer.
	// Default is true (matches existing behaviour).
	var req RunLoopNowRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
			return
		}
	}
	resetTimer := true // default: reset the countdown after a manual run
	if req.ResetTimer != nil {
		resetTimer = *req.ResetTimer
	}

	// Trigger immediate delivery, bounded by auxBackedRequestTimeout so a slow
	// auto-resume (TriggerNow -> ResumeSession) returns a fast, clear retryable
	// 503 instead of blocking until the 30s middleware cap emits an opaque one
	// (mitto-n36h). TriggerLoopNow/ResumeSession are not context-aware, so we
	// bound the call here: run it in a goroutine and race it against the deadline.
	// The buffered channel lets that goroutine finish (ResumeSession has its own
	// internal resume cap) without leaking even after we have already responded;
	// the resume/delivery then completes in the background and a client retry will
	// observe the now-running session.
	ctx, cancel := context.WithTimeout(r.Context(), auxBackedRequestTimeout)
	defer cancel()

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- h.deps.TriggerLoopNow(sessionID, resetTimer)
	}()

	var err error
	select {
	case <-ctx.Done():
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Loop run-now timed out resuming session; returning retryable 503",
				"session_id", sessionID)
		}
		writeRetryableUnavailable(w, "The conversation is resuming. Please try again in a few seconds.", 5)
		return
	case err = <-resultCh:
	}

	if err != nil {
		switch err {
		case session.ErrLoopNotFound:
			writeErrorJSON(w, http.StatusNotFound, "", "No loop prompt configured")
		case h.deps.ErrLoopNotEnabled:
			writeErrorJSON(w, http.StatusBadRequest, "", "Loop is not enabled for this session")
		case h.deps.ErrSessionBusy:
			writeErrorJSON(w, http.StatusConflict, "", "Session is currently processing a prompt")
		default:
			if h.deps.Logger != nil {
				h.deps.Logger.Error("Failed to trigger loop prompt", "error", err, "session_id", sessionID)
			}
			writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to trigger loop prompt")
		}
		return
	}

	// Return success with the updated loop config
	store := h.deps.Store
	if store != nil {
		loopStore := store.Loop(sessionID)
		if updated, err := loopStore.Get(); err == nil {
			writeJSONOK(w, updated)
			return
		}
	}

	// Fallback: just return success status
	writeNoContent(w)
}
