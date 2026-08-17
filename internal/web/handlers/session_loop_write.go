package handlers

import (
	"errors"
	"net/http"
	"slices"
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
		Triggers:           req.Triggers,
		ChildEvents:        req.ChildEvents,
		SlackSubscriptions: req.SlackSubscriptions,
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
	if req.CoalesceDuringBusy != nil {
		v := *req.CoalesceDuringBusy
		p.CoalesceDuringBusy = &v
	}
	if req.RunOnStart != nil {
		v := *req.RunOnStart
		p.RunOnStart = &v
	}
	if req.SettleWindowSeconds != nil {
		v := *req.SettleWindowSeconds
		p.SettleWindowSeconds = &v
	}

	// Auto-apply the seeded prompt's loop: frontmatter block (mitto-le4.1). When
	// req.PromptName resolves to a workspace prompt that carries a loop: block,
	// its fields fill any empty fields on p. Explicit request values win. The
	// merge is disabled when req.LoopApplyPromptDefaults is *false, mirroring the
	// MCP tool argument of the same name.
	if req.PromptName != "" && h.deps.GetWorkspacePromptsAll != nil && h.deps.Store != nil {
		if meta, err := h.deps.Store.GetMetadata(sessionID); err == nil && meta.WorkingDir != "" {
			for _, wp := range h.deps.GetWorkspacePromptsAll(meta.WorkingDir) {
				if strings.EqualFold(wp.Name, req.PromptName) && wp.Loop != nil {
					applyPromptLoopDefaultsToLoopPrompt(p, wp.Loop, req.LoopApplyPromptDefaults)
					break
				}
			}
		}
	}

	// Clamp the on-completion delay to the global floor on write (no-op for schedule trigger).
	p.ClampDelay(h.loopDelayFloor())

	if err := ps.Set(p); err != nil {
		// errors.Is, not ==: Validate() wraps ErrInvalidTrigger (duplicate trigger)
		// and ErrInvalidChildEvent (unknown event) with %w + context, so a direct
		// equality check would miss them and fall through to the generic 500 below.
		if errors.Is(err, session.ErrInvalidFrequency) || errors.Is(err, session.ErrPromptEmpty) || errors.Is(err, session.ErrInvalidMaxIterations) ||
			errors.Is(err, session.ErrInvalidTrigger) || errors.Is(err, session.ErrInvalidChildEvent) || errors.Is(err, session.ErrOnChildAlone) ||
			errors.Is(err, session.ErrInvalidSlackSubscription) || errors.Is(err, session.ErrSlackSubscriptionsRequired) ||
			errors.Is(err, session.ErrInvalidDelay) || errors.Is(err, session.ErrInvalidMaxDuration) ||
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
	// A freshly-defined loop supersedes any previously-detached settings, so drop
	// the saved slot. This keeps the un-loop⇄re-loop toggle symmetric: the saved
	// slot only ever holds the last active config (see Detach), never a stale one.
	if err := ps.ClearSaved(); err != nil && h.deps.Logger != nil {
		h.deps.Logger.Warn("Failed to clear stale saved loop settings on set", "error", err)
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

	// Clamp the on-completion delay to the global floor on write. Membership —
	// not primacy — decides whether the clamp applies: the effective trigger
	// set is the patched value when provided, otherwise the currently-stored
	// set (mitto-r6j.5: a multi-trigger config may list onCompletion alongside
	// other triggers).
	if req.DelaySeconds != nil {
		floor := h.loopDelayFloor()
		if *req.DelaySeconds < floor {
			isOnCompletion := false
			if req.Triggers != nil {
				isOnCompletion = slices.Contains(*req.Triggers, session.TriggerOnCompletion)
			} else if cur, err := ps.Get(); err == nil && cur != nil {
				isOnCompletion = cur.HasTrigger(session.TriggerOnCompletion)
			}
			if isOnCompletion {
				clamped := floor
				req.DelaySeconds = &clamped
			}
		}
	}

	if err := ps.Update(session.LoopUpdate{
		Prompt:              req.Prompt,
		PromptName:          req.PromptName,
		Frequency:           req.Frequency,
		Enabled:             req.Enabled,
		FreshContext:        req.FreshContext,
		MaxIterations:       req.MaxIterations,
		Triggers:            req.Triggers,
		ChildEvents:         req.ChildEvents,
		SlackSubscriptions:  req.SlackSubscriptions,
		DelaySeconds:        req.DelaySeconds,
		MaxDurationSeconds:  req.MaxDurationSeconds,
		Arguments:           req.Arguments,
		Condition:           req.Condition,
		ConditionPreset:     req.ConditionPreset,
		CooldownSeconds:     req.CooldownSeconds,
		CoalesceDuringBusy:  req.CoalesceDuringBusy,
		RunOnStart:          req.RunOnStart,
		SettleWindowSeconds: req.SettleWindowSeconds,
	}); err != nil {
		if errors.Is(err, session.ErrLoopNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "", "No loop prompt configured")
			return
		}
		// errors.Is, not ==: see the matching comment in handleSetLoop above.
		if errors.Is(err, session.ErrInvalidFrequency) || errors.Is(err, session.ErrPromptEmpty) || errors.Is(err, session.ErrInvalidMaxIterations) ||
			errors.Is(err, session.ErrInvalidTrigger) || errors.Is(err, session.ErrInvalidChildEvent) || errors.Is(err, session.ErrOnChildAlone) ||
			errors.Is(err, session.ErrInvalidSlackSubscription) || errors.Is(err, session.ErrSlackSubscriptionsRequired) ||
			errors.Is(err, session.ErrInvalidDelay) || errors.Is(err, session.ErrInvalidMaxDuration) ||
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
