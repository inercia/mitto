package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// handleDeleteLoop handles DELETE /api/sessions/{id}/loop
//
// This is the "un-loop" action. Instead of hard-deleting the config, it detaches
// it: the settings are preserved in loop.saved.json so a later "Make loop" can
// restore them (mirroring the archive/unarchive loop-persistence flow). The
// active loop.json is removed, so the conversation reverts to a regular one
// (loop_configured=false), exactly as before.
func (h *Handlers) handleDeleteLoop(w http.ResponseWriter, sessionID string, ps *session.LoopStore) {
	if err := ps.Detach(); err != nil {
		if err == session.ErrLoopNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No loop prompt configured")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to detach loop prompt", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to remove loop prompt")
		return
	}

	// Broadcast loop disabled to all clients (nil means removed)
	h.broadcastLoop(sessionID, nil)

	writeNoContent(w)
}

// handleRestoreLoop handles POST /api/sessions/{id}/loop/restore
//
// This is the re-loop counterpart of handleDeleteLoop: it restores a loop
// configuration previously preserved by Detach (loop.saved.json), preserving the
// enabled state it had at un-loop time so loop ⇄ un-loop is a symmetric toggle.
// Iteration/duration counters and the stopped reason are reset so the restored
// loop starts its budget fresh. Returns 404 when there is nothing saved to
// restore, letting the frontend fall back to creating a blank draft.
func (h *Handlers) handleRestoreLoop(w http.ResponseWriter, r *http.Request, sessionID string, ps *session.LoopStore) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	saved, err := ps.GetSaved()
	if err != nil {
		if err == session.ErrLoopNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No saved loop settings to restore")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to read saved loop settings", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to read saved loop settings")
		return
	}

	// Reset the run counters/anchors and stopped reason so the restored loop
	// starts fresh; keep the saved enabled state so an actively-looping
	// conversation resumes and a paused draft comes back paused.
	saved.IterationCount = 0
	saved.FirstRunAt = nil
	saved.LastSentAt = nil
	saved.StoppedReason = ""
	saved.StoppedAt = nil
	saved.NextScheduledAt = nil

	if err := ps.Set(saved); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to restore loop settings", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to restore loop settings")
		return
	}
	if err := ps.ClearSaved(); err != nil && h.deps.Logger != nil {
		h.deps.Logger.Warn("Failed to clear saved loop settings after restore", "error", err)
	}
	h.resetLoopContinuation(sessionID)

	updated, err := ps.Get()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get restored loop prompt")
		return
	}

	// Broadcast the restored loop state (includes full config).
	h.broadcastLoop(sessionID, updated)

	// Kick off the first run for a restored, enabled onCompletion conversation.
	if h.deps.BootstrapOnCompletion != nil {
		h.deps.BootstrapOnCompletion(sessionID)
	}

	writeJSONOK(w, updated)
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
