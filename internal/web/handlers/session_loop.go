package handlers

import (
	"net/http"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// LoopPromptRequest is the request body for creating/updating a loop prompt.
type LoopPromptRequest struct {
	Prompt        string            `json:"prompt"`
	PromptName    string            `json:"prompt_name,omitempty"`
	Frequency     session.Frequency `json:"frequency"`
	Enabled       bool              `json:"enabled"`
	FreshContext  bool              `json:"fresh_context,omitempty"`
	MaxIterations int               `json:"max_iterations,omitempty"`
	// Triggers is the list of triggers that arm this loop: any of "schedule",
	// "onCompletion", "onTasks", "onChild", or "onSlack". Empty
	// defaults to ["schedule"]. Replaces the legacy scalar "trigger" key, which
	// is no longer accepted on this request DTO (mitto-r6j.5) — the response
	// still emits both "trigger" (primary/first, back-compat) and "triggers".
	Triggers []session.LoopTrigger `json:"triggers,omitempty"`
	// ChildEvents lists the child-conversation lifecycle events that arm the
	// onChild trigger: "anyEndResponse", "anyDeleted", and/or "anyLoopStopped"
	// (fires once when a child's own loop transitions into the stopped
	// state). Empty defaults to anyEndResponse + anyDeleted (anyLoopStopped
	// is opt-in only). Only meaningful when "onChild" is among Triggers.
	ChildEvents []session.ChildEvent `json:"child_events,omitempty"`
	// SlackSubscriptions contains credential-free installation/channel refs for
	// onSlack. PUT replaces the complete canonicalized list.
	SlackSubscriptions []session.SlackSubscription `json:"slack_subscriptions,omitempty"`
	// DelaySeconds is the wait after the agent stops before the next run (onCompletion only).
	// Clamped to the global floor on write.
	DelaySeconds int `json:"delay_seconds,omitempty"`
	// MaxDurationSeconds is the wall-clock cap since iterating started (0 = unlimited).
	MaxDurationSeconds int `json:"max_duration_seconds,omitempty"`
	// Arguments holds user-supplied values for Go-template .Args placeholders
	// when PromptName is set. Ignored for free-text prompts.
	Arguments map[string]string `json:"arguments,omitempty"`
	// Condition is a CEL expression gating onTasks firing. Empty means fire on
	// ANY beads/task change. Only meaningful when onTasks is armed.
	Condition *string `json:"condition,omitempty"`
	// ConditionPreset is an optional UI preset id that was compiled into Condition.
	ConditionPreset *string `json:"condition_preset,omitempty"`
	// CooldownSeconds is the per-conversation cooldown floor honoured by the runner
	// between onTasks firings. 0/nil means use the global floor.
	CooldownSeconds *int `json:"cooldown_seconds,omitempty"`
	// CoalesceDuringBusy controls how the onTasks trigger handles beads changes
	// that arrive while the loop's subtree is busy. Nil or true = silently absorb
	// (default). False = fire once more with the accumulated delta after quiescence.
	CoalesceDuringBusy *bool `json:"coalesce_during_busy,omitempty"`
	// RunOnStart, when *true, causes the loop to fire exactly once shortly after
	// Mitto boots (after the interactive-resume startup delay, with an anti-flap
	// window suppressing the pulse when the loop already ran very recently).
	// Nil or false = do not fire on start (default).
	RunOnStart *bool `json:"run_on_start,omitempty"`
	// SettleWindowSeconds is an optional pre-fire debounce window (seconds) for
	// the onTasks trigger; 0/nil = fire immediately on the first delta (default).
	// Only meaningful when "onTasks" is among Triggers.
	SettleWindowSeconds *int `json:"settle_window_seconds,omitempty"`
	// LoopApplyPromptDefaults, when non-nil and *false, disables auto-merging of
	// the resolved prompt's loop: frontmatter defaults into empty request fields.
	// Defaults to enabled (nil or *true). Mirrors the MCP tool argument of the
	// same name (see mcpserver.applyPromptLoopDefaultsToStartInput).
	LoopApplyPromptDefaults *bool `json:"loop_apply_prompt_defaults,omitempty"`
}

// LoopPromptPatchRequest is the request body for partial updates.
type LoopPromptPatchRequest struct {
	Prompt        *string            `json:"prompt,omitempty"`
	PromptName    *string            `json:"prompt_name,omitempty"`
	Frequency     *session.Frequency `json:"frequency,omitempty"`
	Enabled       *bool              `json:"enabled,omitempty"`
	FreshContext  *bool              `json:"fresh_context,omitempty"`
	MaxIterations *int               `json:"max_iterations,omitempty"`
	// Triggers, DelaySeconds, MaxDurationSeconds are partial updates for the
	// trigger list and on-completion fields. Triggers, when non-nil, REPLACES
	// the stored trigger list wholesale (nil = leave unchanged). Replaces the
	// legacy scalar "trigger" key, which is no longer accepted on this DTO
	// (mitto-r6j.5).
	Triggers *[]session.LoopTrigger `json:"triggers,omitempty"`
	// ChildEvents is a partial update for the onChild event list; nil = leave
	// unchanged, non-nil REPLACES the stored list wholesale (same semantics as
	// Triggers).
	ChildEvents *[]session.ChildEvent `json:"child_events,omitempty"`
	// SlackSubscriptions is nil to leave unchanged; a present slice replaces the
	// whole list, including an empty slice to clear it.
	SlackSubscriptions *[]session.SlackSubscription `json:"slack_subscriptions,omitempty"`
	DelaySeconds       *int                         `json:"delay_seconds,omitempty"`
	MaxDurationSeconds *int                         `json:"max_duration_seconds,omitempty"`
	// Arguments is a partial update for the substitution arguments map.
	// nil = leave unchanged; non-nil = replace the entire map (including empty map to clear it).
	Arguments *map[string]string `json:"arguments,omitempty"`
	// Condition, ConditionPreset, CooldownSeconds, CoalesceDuringBusy are partial updates for the onTasks fields.
	Condition          *string `json:"condition,omitempty"`
	ConditionPreset    *string `json:"condition_preset,omitempty"`
	CooldownSeconds    *int    `json:"cooldown_seconds,omitempty"`
	CoalesceDuringBusy *bool   `json:"coalesce_during_busy,omitempty"`
	// RunOnStart is a partial update for the boot-pulse toggle. Nil = unchanged.
	RunOnStart *bool `json:"run_on_start,omitempty"`
	// SettleWindowSeconds is a partial update for the onTasks pre-fire debounce
	// window (seconds). Nil = unchanged.
	SettleWindowSeconds *int `json:"settle_window_seconds,omitempty"`
	// ResetCounters, when true, resets IterationCount=0, FirstRunAt=nil, and
	// LastSentAt=nil so the elapsed iterations and elapsed time start from zero and
	// the loop looks never-sent. Used when restoring a conversation that auto-stopped
	// after reaching its max-iterations/max-duration cap. Clearing LastSentAt makes
	// the restore fire its first run immediately (like an initial run) instead of
	// waiting out the onCompletion delay.
	ResetCounters *bool `json:"reset_counters,omitempty"`
}

// RunLoopNowRequest is the optional request body for POST /api/sessions/{id}/loop/run-now.
type RunLoopNowRequest struct {
	ResetTimer *bool `json:"reset_timer,omitempty"`
}

// loopDelayFloor returns the configured global floor for the on-completion delay.
// Falls back to the package default when the loop runner is unavailable (e.g. tests).
func (h *Handlers) loopDelayFloor() int {
	if h.deps.LoopDelayFloor != nil {
		return h.deps.LoopDelayFloor()
	}
	return configPkg.DefaultMinLoopCompletionDelaySeconds
}

// HandleSessionLoop handles loop prompt operations for a session.
// Routes: GET, PUT, PATCH, DELETE /api/sessions/{id}/loop
// Route: POST /api/sessions/{id}/loop/run-now (immediate delivery)
// Route: POST /api/sessions/{id}/loop/restore (restore detached settings)
func (h *Handlers) HandleSessionLoop(w http.ResponseWriter, r *http.Request, sessionID, subPath string) {
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

	// Prevent setting loop on child sessions - only parents/top-level sessions can be loops
	if r.Method != http.MethodGet && meta.ParentSessionID != "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Cannot set loop on a child conversation. Only parent or top-level conversations can be loops.")
		return
	}

	// Handle run-now sub-path
	if subPath == "run-now" {
		h.handleRunLoopNow(w, r, sessionID)
		return
	}

	loopStore := store.Loop(sessionID)

	// Handle restore sub-path (re-loop from previously-saved settings).
	if subPath == "restore" {
		h.handleRestoreLoop(w, r, sessionID, loopStore)
		return
	}

	// Handle acknowledge-stopped-reason sub-path — records the user's
	// dismissal of the current StoppedReason so the sidebar warning icon
	// disappears in every connected browser and survives page reloads.
	if subPath == "acknowledge-stopped-reason" {
		h.handleAcknowledgeLoopStoppedReason(w, r, sessionID, loopStore)
		return
	}

	// Handle suggest-from-recent sub-path — read-only lookup that returns a
	// LoopPrompt draft pre-filled from the most recent named prompt's loop:
	// frontmatter block (mitto-qff). Never writes session state.
	if subPath == "suggest-from-recent" {
		h.handleSuggestLoopFromRecent(w, r, sessionID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetLoop(w, loopStore)
	case http.MethodPut:
		h.handleSetLoop(w, r, sessionID, loopStore)
	case http.MethodPatch:
		h.handlePatchLoop(w, r, sessionID, loopStore)
	case http.MethodDelete:
		h.handleDeleteLoop(w, sessionID, loopStore)
	default:
		methodNotAllowed(w)
	}
}

// handleGetLoop handles GET /api/sessions/{id}/loop
func (h *Handlers) handleGetLoop(w http.ResponseWriter, ps *session.LoopStore) {
	p, err := ps.Get()
	if err != nil {
		if err == session.ErrLoopNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No loop prompt configured")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get loop prompt", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get loop prompt")
		return
	}

	writeJSONOK(w, p)
}

// triggerTitleFromLoop triggers title generation from a loop prompt when
// the session has no title yet. Shared by the PUT and PATCH handlers.
func (h *Handlers) triggerTitleFromLoop(sessionID, prompt, promptName string) {
	if h.deps.SessionManager != nil && conversation.SessionNeedsTitle(h.deps.Store, sessionID) {
		if bs := h.deps.SessionManager.GetSession(sessionID); bs != nil {
			bs.TriggerTitleGenerationFromLoop(prompt, promptName)
		}
	}
}

// broadcastLoop broadcasts a loop-config change when a broadcaster is wired.
func (h *Handlers) broadcastLoop(sessionID string, updated *session.LoopPrompt) {
	if h.deps.BroadcastLoopUpdated != nil {
		h.deps.BroadcastLoopUpdated(sessionID, updated)
	}
}

// handleAcknowledgeLoopStoppedReason handles POST /api/sessions/{id}/loop/acknowledge-stopped-reason.
// Records the user's dismissal of the current StoppedReason so the sidebar
// warning icon disappears in every connected browser and survives page reloads.
// No-op (200 OK, no broadcast) when the loop has no StoppedReason or the
// current reason is already acknowledged.
func (h *Handlers) handleAcknowledgeLoopStoppedReason(w http.ResponseWriter, r *http.Request, sessionID string, ps *session.LoopStore) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	updated, changed, err := ps.AcknowledgeStoppedReason()
	if err != nil {
		if err == session.ErrLoopNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No loop prompt configured")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to acknowledge loop stopped reason", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to acknowledge loop stopped reason")
		return
	}

	if changed {
		h.broadcastLoop(sessionID, updated)
	}
	writeJSONOK(w, updated)
}
