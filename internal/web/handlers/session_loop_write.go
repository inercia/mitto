package handlers

import (
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/session"
)

// isInvalidConditionErr reports whether err originates from LoopPrompt.Validate's
// CEL condition check (session.ConditionValidator, wired to config.ValidateCondition).
// There is no dedicated sentinel for this — Validate wraps the validator's error with
// the fixed prefix "invalid condition: " — so we match on that prefix to classify it
// as a 400 (bad request) instead of falling through to the generic 500 handler.
func isInvalidConditionErr(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "invalid condition:")
}

// handleSetLoop handles PUT /api/sessions/{id}/loop
func (h *Handlers) handleSetLoop(w http.ResponseWriter, r *http.Request, sessionID string, ps *session.LoopStore) {
	var req LoopPromptRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	p := &session.LoopPrompt{
		Prompt:             req.Prompt,
		PromptName:         req.PromptName,
		Arguments:          req.Arguments,
		Frequency:          req.Frequency,
		Enabled:            req.Enabled,
		FreshContext:       req.FreshContext,
		MaxIterations:      req.MaxIterations,
		Trigger:            req.Trigger,
		DelaySeconds:       req.DelaySeconds,
		MaxDurationSeconds: req.MaxDurationSeconds,
	}
	if req.Condition != nil {
		p.Condition = *req.Condition
	}
	if req.ConditionPreset != nil {
		p.ConditionPreset = *req.ConditionPreset
	}
	if req.CooldownSeconds != nil {
		p.CooldownSeconds = *req.CooldownSeconds
	}
	// Clamp the on-completion delay to the global floor on write (no-op for schedule trigger).
	p.ClampDelay(h.loopDelayFloor())

	if err := ps.Set(p); err != nil {
		if err == session.ErrInvalidFrequency || err == session.ErrPromptEmpty || err == session.ErrInvalidMaxIterations ||
			err == session.ErrInvalidTrigger || err == session.ErrInvalidDelay || err == session.ErrInvalidMaxDuration ||
			isInvalidConditionErr(err) {
			writeErrorJSON(w, http.StatusBadRequest, "", err.Error())
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to set loop prompt", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to set loop prompt")
		return
	}
	h.resetLoopContinuation(sessionID)

	// Return the updated loop prompt
	updated, err := ps.Get()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get updated loop prompt")
		return
	}

	// If the session has no title, trigger title generation from the loop prompt.
	h.triggerTitleFromLoop(sessionID, req.Prompt, req.PromptName)

	// Broadcast loop state change to all clients (includes full config)
	h.broadcastLoop(sessionID, updated)

	// Kick off the very first run for a fresh onCompletion conversation.
	if h.deps.BootstrapOnCompletion != nil {
		h.deps.BootstrapOnCompletion(sessionID)
	}

	writeJSONOK(w, updated)
}

// handlePatchLoop handles PATCH /api/sessions/{id}/loop
func (h *Handlers) handlePatchLoop(w http.ResponseWriter, r *http.Request, sessionID string, ps *session.LoopStore) {
	var req LoopPromptPatchRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	// Clamp the on-completion delay to the global floor on write. The effective trigger
	// is the patched value when provided, otherwise the currently-stored trigger.
	if req.DelaySeconds != nil {
		floor := h.loopDelayFloor()
		if *req.DelaySeconds < floor {
			effTrigger := session.LoopTrigger("")
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

	if err := ps.Update(req.Prompt, req.PromptName, req.Frequency, req.Enabled, req.FreshContext, req.MaxIterations, req.Trigger, req.DelaySeconds, req.MaxDurationSeconds, req.Arguments, req.Condition, req.ConditionPreset, req.CooldownSeconds); err != nil {
		if err == session.ErrLoopNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No loop prompt configured")
			return
		}
		if err == session.ErrInvalidFrequency || err == session.ErrPromptEmpty || err == session.ErrInvalidMaxIterations ||
			err == session.ErrInvalidTrigger || err == session.ErrInvalidDelay || err == session.ErrInvalidMaxDuration ||
			isInvalidConditionErr(err) {
			writeErrorJSON(w, http.StatusBadRequest, "", err.Error())
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to update loop prompt", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to update loop prompt")
		return
	}

	// Reset the iteration/elapsed-time anchors when requested (e.g. restoring a
	// conversation that auto-stopped after reaching its max-iterations/max-duration cap).
	if req.ResetCounters != nil && *req.ResetCounters {
		if err := ps.ResetCounters(); err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Error("Failed to reset loop counters", "error", err)
			}
			writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to reset loop counters")
			return
		}
	}

	// Record WHY the loop was paused so the UI can show an amber "Paused by you"
	// pill (resumable) instead of a blank glance line. Re-enabling clears it.
	if req.Enabled != nil && !*req.Enabled {
		if err := ps.MarkStopped(session.StoppedReasonPausedByUser); err != nil && h.deps.Logger != nil {
			h.deps.Logger.Warn("Failed to record pausedByUser reason", "error", err)
		}
	}
	h.resetLoopContinuation(sessionID)

	// Return the updated loop prompt
	updated, err := ps.Get()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get updated loop prompt")
		return
	}

	// If the session has no title, trigger title generation from the loop prompt.
	var pPrompt, pName string
	if updated != nil {
		pPrompt = updated.Prompt
		pName = updated.PromptName
	}
	h.triggerTitleFromLoop(sessionID, pPrompt, pName)

	// Broadcast loop state change to all clients (includes full config)
	h.broadcastLoop(sessionID, updated)

	// Kick off the very first run for a fresh onCompletion conversation.
	if h.deps.BootstrapOnCompletion != nil {
		h.deps.BootstrapOnCompletion(sessionID)
	}

	writeJSONOK(w, updated)
}

// resetLoopContinuation clears the live BackgroundSession's loop continuation marker
// (mitto-5xjn) so the next loop run after a config change/pause/re-enable renders the
// verbose form. No-op when the session is not currently live.
func (h *Handlers) resetLoopContinuation(sessionID string) {
	if h.deps.SessionManager == nil {
		return
	}
	if bs := h.deps.SessionManager.GetSession(sessionID); bs != nil {
		bs.ResetLoopContinuation()
	}
}
