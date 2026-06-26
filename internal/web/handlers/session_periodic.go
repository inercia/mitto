package handlers

import (
	"net/http"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
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
	// Arguments holds user-supplied values for ${VAR}/${VAR:-default} substitution
	// when PromptName is set. Ignored for free-text prompts.
	Arguments map[string]string `json:"arguments,omitempty"`
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
	// Arguments is a partial update for the substitution arguments map.
	// nil = leave unchanged; non-nil = replace the entire map (including empty map to clear it).
	Arguments *map[string]string `json:"arguments,omitempty"`
	// ResetCounters, when true, resets IterationCount=0, FirstRunAt=nil, and
	// LastSentAt=nil so the elapsed iterations and elapsed time start from zero and
	// the loop looks never-sent. Used when restoring a conversation that auto-stopped
	// after reaching its max-iterations/max-duration cap. Clearing LastSentAt makes
	// the restore fire its first run immediately (like an initial run) instead of
	// waiting out the onCompletion delay.
	ResetCounters *bool `json:"reset_counters,omitempty"`
}

// RunPeriodicNowRequest is the optional request body for POST /api/sessions/{id}/periodic/run-now.
type RunPeriodicNowRequest struct {
	ResetTimer *bool `json:"reset_timer,omitempty"`
}

// periodicDelayFloor returns the configured global floor for the on-completion delay.
// Falls back to the package default when the periodic runner is unavailable (e.g. tests).
func (h *Handlers) periodicDelayFloor() int {
	if h.deps.PeriodicDelayFloor != nil {
		return h.deps.PeriodicDelayFloor()
	}
	return configPkg.DefaultMinPeriodicCompletionDelaySeconds
}

// HandleSessionPeriodic handles periodic prompt operations for a session.
// Routes: GET, PUT, PATCH, DELETE /api/sessions/{id}/periodic
// Route: POST /api/sessions/{id}/periodic/run-now (immediate delivery)
func (h *Handlers) HandleSessionPeriodic(w http.ResponseWriter, r *http.Request, sessionID, subPath string) {
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	// Verify session exists
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get session")
		return
	}

	// Prevent setting periodic on child sessions - only parents/top-level sessions can be periodic
	if r.Method != http.MethodGet && meta.ParentSessionID != "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Cannot set periodic on a child conversation. Only parent or top-level conversations can be periodic.")
		return
	}

	// Handle run-now sub-path
	if subPath == "run-now" {
		h.handleRunPeriodicNow(w, r, sessionID)
		return
	}

	periodicStore := store.Periodic(sessionID)

	switch r.Method {
	case http.MethodGet:
		h.handleGetPeriodic(w, periodicStore)
	case http.MethodPut:
		h.handleSetPeriodic(w, r, sessionID, periodicStore)
	case http.MethodPatch:
		h.handlePatchPeriodic(w, r, sessionID, periodicStore)
	case http.MethodDelete:
		h.handleDeletePeriodic(w, sessionID, periodicStore)
	default:
		methodNotAllowed(w)
	}
}

// handleGetPeriodic handles GET /api/sessions/{id}/periodic
func (h *Handlers) handleGetPeriodic(w http.ResponseWriter, ps *session.PeriodicStore) {
	p, err := ps.Get()
	if err != nil {
		if err == session.ErrPeriodicNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No periodic prompt configured")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get periodic prompt", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get periodic prompt")
		return
	}

	writeJSONOK(w, p)
}

// triggerTitleFromPeriodic triggers title generation from a periodic prompt when
// the session has no title yet. Shared by the PUT and PATCH handlers.
func (h *Handlers) triggerTitleFromPeriodic(sessionID, prompt, promptName string) {
	if h.deps.SessionManager != nil && conversation.SessionNeedsTitle(h.deps.Store, sessionID) {
		if bs := h.deps.SessionManager.GetSession(sessionID); bs != nil {
			bs.TriggerTitleGenerationFromPeriodic(prompt, promptName)
		}
	}
}

// broadcastPeriodic broadcasts a periodic-config change when a broadcaster is wired.
func (h *Handlers) broadcastPeriodic(sessionID string, updated *session.PeriodicPrompt) {
	if h.deps.BroadcastPeriodicUpdated != nil {
		h.deps.BroadcastPeriodicUpdated(sessionID, updated)
	}
}
