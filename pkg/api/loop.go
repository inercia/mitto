package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// --- Loop API ---

// LoopFrequency represents a loop schedule frequency.
type LoopFrequency struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"`
	At    string `json:"at,omitempty"` // HH:MM in UTC, only for unit=days
}

// SlackSubscription is a credential-free reference to one Slack channel.
type SlackSubscription struct {
	InstallationID string `json:"installation_id"`
	ChannelID      string `json:"channel_id"`
	EventMode      string `json:"event_mode,omitempty"`
	ThreadPolicy   string `json:"thread_policy,omitempty"`
}

// SetLoopRequest is the request body for PUT /api/sessions/{id}/loop.
type SetLoopRequest struct {
	PromptName    string        `json:"prompt_name,omitempty"`
	Prompt        string        `json:"prompt,omitempty"`
	Frequency     LoopFrequency `json:"frequency"`
	Enabled       bool          `json:"enabled"`
	MaxIterations int           `json:"max_iterations,omitempty"`
	// Triggers is the list of triggers that arm this loop: any of "schedule",
	// "onCompletion", "onTasks", "onChild", or "onSlack". Empty defaults to ["schedule"]
	// server-side. Replaces the legacy scalar "trigger" key on write (the
	// server no longer accepts it here); the response DTO (LoopConfig) still
	// emits both for back-compat (mitto-r6j.5).
	Triggers []string `json:"triggers,omitempty"`
	// ChildEvents lists the child-conversation lifecycle events that arm the
	// onChild trigger: "anyEndResponse" and/or "anyDeleted". Empty defaults to
	// both. Only meaningful when "onChild" is among Triggers.
	ChildEvents        []string            `json:"child_events,omitempty"`
	SlackSubscriptions []SlackSubscription `json:"slack_subscriptions,omitempty"`
	// DelaySeconds is the wait after the agent stops before the next run
	// (onCompletion only). Clamped to the server floor.
	DelaySeconds int `json:"delay_seconds,omitempty"`
	// MaxDurationSeconds is the wall-clock cap since iterating started (0 = unlimited).
	MaxDurationSeconds int `json:"max_duration_seconds,omitempty"`
	// Condition is a CEL expression gating onTasks firing; empty = fire on ANY beads change.
	Condition string `json:"condition,omitempty"`
	// ConditionPreset is an optional UI preset id that compiled to Condition.
	ConditionPreset string `json:"condition_preset,omitempty"`
	// CooldownSeconds is the per-conversation cooldown floor; 0 = use the global floor.
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`
	// RunOnStart, when *true, causes the loop to fire exactly once shortly after
	// Mitto boots (mitto-ystk). Nil or false = do not fire on start (default).
	RunOnStart *bool `json:"run_on_start,omitempty"`
	// FreshContext starts each run with a clean agent context (no history
	// injection, new ACP session) when true.
	FreshContext bool `json:"fresh_context,omitempty"`
	// Arguments holds user-supplied values for Go-template .Args placeholders
	// when PromptName is set. Ignored for free-text prompts.
	Arguments map[string]string `json:"arguments,omitempty"`
	// CoalesceDuringBusy controls how the onTasks trigger handles beads changes
	// that arrive while the loop's subtree is busy. Nil or true = silently
	// absorb (default). False = fire once more with the accumulated delta.
	CoalesceDuringBusy *bool `json:"coalesce_during_busy,omitempty"`
	// SettleWindowSeconds is an optional pre-fire debounce window (seconds) for
	// the onTasks trigger; 0/nil = fire immediately on the first delta.
	SettleWindowSeconds *int `json:"settle_window_seconds,omitempty"`
	// LoopApplyPromptDefaults, when non-nil and *false, disables auto-merging
	// of the resolved prompt's loop: frontmatter defaults into empty request
	// fields. Defaults to enabled (nil or *true).
	LoopApplyPromptDefaults *bool `json:"loop_apply_prompt_defaults,omitempty"`
}

// LoopPatchRequest is the request body for PATCH /api/sessions/{id}/loop
// (partial update; nil/absent fields leave the corresponding setting
// unchanged). Mirrors internal/web/handlers.LoopPromptPatchRequest.
type LoopPatchRequest struct {
	Prompt              *string              `json:"prompt,omitempty"`
	PromptName          *string              `json:"prompt_name,omitempty"`
	Frequency           *LoopFrequency       `json:"frequency,omitempty"`
	Enabled             *bool                `json:"enabled,omitempty"`
	FreshContext        *bool                `json:"fresh_context,omitempty"`
	MaxIterations       *int                 `json:"max_iterations,omitempty"`
	Triggers            *[]string            `json:"triggers,omitempty"`
	ChildEvents         *[]string            `json:"child_events,omitempty"`
	SlackSubscriptions  *[]SlackSubscription `json:"slack_subscriptions,omitempty"`
	DelaySeconds        *int                 `json:"delay_seconds,omitempty"`
	MaxDurationSeconds  *int                 `json:"max_duration_seconds,omitempty"`
	Arguments           map[string]string    `json:"arguments,omitempty"`
	Condition           *string              `json:"condition,omitempty"`
	ConditionPreset     *string              `json:"condition_preset,omitempty"`
	CooldownSeconds     *int                 `json:"cooldown_seconds,omitempty"`
	CoalesceDuringBusy  *bool                `json:"coalesce_during_busy,omitempty"`
	RunOnStart          *bool                `json:"run_on_start,omitempty"`
	SettleWindowSeconds *int                 `json:"settle_window_seconds,omitempty"`
	// ResetCounters, when true, resets IterationCount=0, FirstRunAt=nil, and
	// LastSentAt=nil so the loop looks never-sent.
	ResetCounters *bool `json:"reset_counters,omitempty"`
}

// LoopConfig represents the loop configuration for a session. It mirrors the
// wire shape of the server's session.LoopPrompt; timestamps are kept as their
// raw RFC 3339 strings (as NextScheduledAt already was) rather than time.Time
// so the whole type decodes uniformly.
type LoopConfig struct {
	Prompt          string            `json:"prompt,omitempty"`
	PromptName      string            `json:"prompt_name,omitempty"`
	Arguments       map[string]string `json:"arguments,omitempty"`
	Frequency       LoopFrequency     `json:"frequency"`
	Enabled         bool              `json:"enabled"`
	MaxIterations   int               `json:"max_iterations,omitempty"`
	NextScheduledAt string            `json:"next_scheduled_at,omitempty"`
	// Trigger is the legacy scalar primary trigger (back-compat; equals Triggers[0]).
	Trigger            string              `json:"trigger,omitempty"`
	Triggers           []string            `json:"triggers,omitempty"`
	ChildEvents        []string            `json:"child_events,omitempty"`
	SlackSubscriptions []SlackSubscription `json:"slack_subscriptions,omitempty"`
	DelaySeconds       int                 `json:"delay_seconds,omitempty"`
	MaxDurationSeconds int                 `json:"max_duration_seconds,omitempty"`
	IterationCount     int                 `json:"iteration_count,omitempty"`
	FreshContext       bool                `json:"fresh_context,omitempty"`
	Condition          string              `json:"condition,omitempty"`
	ConditionPreset    string              `json:"condition_preset,omitempty"`
	CooldownSeconds    int                 `json:"cooldown_seconds,omitempty"`
	StoppedReason      string              `json:"stopped_reason,omitempty"`
	// StoppedAt is when the loop stopped itself (empty while running).
	StoppedAt string `json:"stopped_at,omitempty"`
	// AcknowledgedStoppedReason is the StoppedReason the user last dismissed;
	// see AcknowledgeLoopStoppedReason.
	AcknowledgedStoppedReason string `json:"acknowledged_stopped_reason,omitempty"`
	// CreatedAt / UpdatedAt are the loop configuration's own timestamps.
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	// FirstRunAt is when the loop first ran (the MaxDurationSeconds origin);
	// LastSentAt is when a run was last dispatched. Both empty when never run.
	FirstRunAt string `json:"first_run_at,omitempty"`
	LastSentAt string `json:"last_sent_at,omitempty"`
	// CoalesceDuringBusy mirrors the schema field; nil = unset (server default).
	CoalesceDuringBusy *bool `json:"coalesce_during_busy,omitempty"`
	// SettleWindowSeconds mirrors the schema field; nil = unset (server default).
	SettleWindowSeconds *int `json:"settle_window_seconds,omitempty"`
	// RunOnStart mirrors the schema field (mitto-ystk). Nil = unset/default.
	RunOnStart *bool `json:"run_on_start,omitempty"`
}

// LoopSuggestion is the response for GET /api/sessions/{id}/loop/suggest-from-recent:
// a validated LoopPrompt draft (Enabled=false) pre-filled from the most recent
// named prompt's loop: frontmatter block, suitable for passing verbatim to SetLoop.
type LoopSuggestion = LoopConfig

// SetLoop configures a loop schedule on a session via PUT.
func (c *Client) SetLoop(sessionID string, req SetLoopRequest) (*LoopConfig, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("set loop: marshal: %w", err)
	}

	httpReq, err := c.newRequest(http.MethodPut,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("set loop: build request: %w", err)
	}

	resp, err := c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("set loop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("set loop", resp)
	}

	var config LoopConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("set loop: decode: %w", err)
	}
	return &config, nil
}

// GetLoop returns the loop configuration for a session.
func (c *Client) GetLoop(sessionID string) (*LoopConfig, error) {
	req, err := c.newRequest(http.MethodGet, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop"), "", nil)
	if err != nil {
		return nil, fmt.Errorf("get loop: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get loop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &APIError{Op: "get loop", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("loop not configured for session: %s", sessionID),
			Details: map[string]any{"session_id": sessionID}}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("get loop", resp)
	}

	var config LoopConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("get loop: decode: %w", err)
	}
	return &config, nil
}

// PatchLoop partially updates the loop configuration for a session. Only
// non-nil fields in req are applied; all others are left untouched.
func (c *Client) PatchLoop(sessionID string, req LoopPatchRequest) (*LoopConfig, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("patch loop: marshal: %w", err)
	}

	httpReq, err := c.newRequest(http.MethodPatch,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("patch loop: build request: %w", err)
	}

	resp, err := c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("patch loop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &APIError{Op: "patch loop", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("loop not configured for session: %s", sessionID),
			Details: map[string]any{"session_id": sessionID}}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("patch loop", resp)
	}

	var config LoopConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("patch loop: decode: %w", err)
	}
	return &config, nil
}

// DeleteLoop detaches the loop configuration from a session (the "un-loop"
// action). The settings are preserved server-side so a later SetLoop call
// against the same session can be undone by re-configuring; this mirrors the
// UI's "Make non-loop" action. Returns nil on both 200 and 204, matching the
// other loop/queue mutators in this file.
func (c *Client) DeleteLoop(sessionID string) error {
	req, err := c.newRequest(http.MethodDelete, c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop"), "", nil)
	if err != nil {
		return fmt.Errorf("delete loop: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("delete loop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &APIError{Op: "delete loop", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("loop not configured for session: %s", sessionID),
			Details: map[string]any{"session_id": sessionID}}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("delete loop", resp)
	}
	return nil
}

// RestoreLoop re-loops a session from the settings preserved by a previous
// DeleteLoop (un-loop) call, resetting iteration/duration counters so the
// restored loop starts fresh. Returns a 404 *APIError (ErrNotFound) when
// there is nothing saved to restore, and a 409 *APIError (ErrConflict) when
// an active loop already exists on the session.
func (c *Client) RestoreLoop(sessionID string) (*LoopConfig, error) {
	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop/restore"),
		"", nil,
	)
	if err != nil {
		return nil, fmt.Errorf("restore loop: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("restore loop: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("restore loop", resp)
	}

	var config LoopConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("restore loop: decode: %w", err)
	}
	return &config, nil
}

// AcknowledgeLoopStoppedReason records the caller's dismissal of the loop's
// current StoppedReason so the sidebar warning icon disappears in every
// connected browser and survives page reloads. No-op (200 OK, unchanged
// response) when the loop has no StoppedReason or it is already acknowledged.
func (c *Client) AcknowledgeLoopStoppedReason(sessionID string) (*LoopConfig, error) {
	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop/acknowledge-stopped-reason"),
		"", nil,
	)
	if err != nil {
		return nil, fmt.Errorf("acknowledge loop stopped reason: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("acknowledge loop stopped reason: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &APIError{Op: "acknowledge loop stopped reason", Status: http.StatusNotFound, Code: CodeNotFound,
			Message: fmt.Sprintf("loop not configured for session: %s", sessionID),
			Details: map[string]any{"session_id": sessionID}}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("acknowledge loop stopped reason", resp)
	}

	var config LoopConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("acknowledge loop stopped reason: decode: %w", err)
	}
	return &config, nil
}

// SuggestLoopFromRecent is a read-only lookup that returns a LoopPrompt draft
// pre-filled from the most recent named prompt's loop: frontmatter block, if
// any. Returns a 404 *APIError (ErrNotFound) when no suggestion is available
// (letting the caller fall back to a blank draft). Never writes session state.
func (c *Client) SuggestLoopFromRecent(sessionID string) (*LoopSuggestion, error) {
	req, err := c.newRequest(http.MethodGet,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop/suggest-from-recent"),
		"", nil,
	)
	if err != nil {
		return nil, fmt.Errorf("suggest loop from recent: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("suggest loop from recent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.apiError("suggest loop from recent", resp)
	}

	var suggestion LoopSuggestion
	if err := json.NewDecoder(resp.Body).Decode(&suggestion); err != nil {
		return nil, fmt.Errorf("suggest loop from recent: decode: %w", err)
	}
	return &suggestion, nil
}

// RunLoopNow triggers an immediate run of the loop prompt.
// resetTimer controls whether the next scheduled run timer is reset.
func (c *Client) RunLoopNow(sessionID string, resetTimer bool) error {
	reqBody := struct {
		ResetTimer bool `json:"reset_timer"`
	}{ResetTimer: resetTimer}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("run loop now: marshal: %w", err)
	}

	req, err := c.newRequest(http.MethodPost,
		c.apiURL("/api/sessions/"+url.PathEscape(sessionID)+"/loop/run-now"),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("run loop now: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("run loop now: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.apiError("run loop now", resp)
	}
	return nil
}
