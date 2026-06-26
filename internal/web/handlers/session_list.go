package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// SessionListResponse extends session.Metadata with additional runtime fields.
type SessionListResponse struct {
	session.Metadata
	// PeriodicConfigured is true when a periodic config exists for this session.
	// Controls editor UI mode (shows frequency panel and lock/unlock buttons).
	// A conversation with PeriodicConfigured=true but PeriodicEnabled=false is
	// a "draft" periodic — editor visible but runs not yet active.
	PeriodicConfigured bool `json:"periodic_configured"`
	// PeriodicEnabled is true when periodic runs are active (config.Enabled == true).
	// Drives the sidebar PERIODIC category and clock icon. A paused/draft periodic
	// conversation has PeriodicConfigured=true but PeriodicEnabled=false and falls
	// into the regular Conversations group.
	PeriodicEnabled bool `json:"periodic_enabled"`
	// NextScheduledAt is the next scheduled time for periodic sessions (nil if not periodic or not scheduled).
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
	// PeriodicFrequency is the frequency configuration for periodic sessions (nil if not periodic).
	PeriodicFrequency *session.Frequency `json:"periodic_frequency,omitempty"`
	// IsWaitingForChildren is true when the session is currently blocked on mitto_children_tasks_wait.
	// This is a runtime state (not persisted) tracked by the SessionManager.
	IsWaitingForChildren bool `json:"is_waiting_for_children,omitempty"`
	// IsStreaming is true when the session is currently prompting (agent streaming).
	// This is a runtime state (not persisted) tracked by the SessionManager.
	IsStreaming bool `json:"is_streaming,omitempty"`
	// PeriodicStoppedReason is the reason the periodic loop was auto-stopped (empty when still running).
	PeriodicStoppedReason string `json:"periodic_stopped_reason,omitempty"`
	// PeriodicTrigger is "schedule" or "onCompletion" (resolved via EffectiveTrigger so schedule loops
	// always report "schedule", never the empty-string default).
	PeriodicTrigger string `json:"periodic_trigger,omitempty"`
	// PeriodicIterationCount is the number of scheduled runs delivered so far.
	PeriodicIterationCount int `json:"periodic_iteration_count,omitempty"`
	// PeriodicMaxIterations is the per-prompt cap on scheduled runs (0 = unlimited).
	PeriodicMaxIterations int `json:"periodic_max_iterations,omitempty"`
	// PeriodicDelaySeconds is the wait in seconds after agent idle before the next onCompletion run.
	PeriodicDelaySeconds int `json:"periodic_delay_seconds,omitempty"`
	// PeriodicMaxDurationSeconds is the wall-clock cap in seconds since iterating started (0 = unlimited).
	PeriodicMaxDurationSeconds int `json:"periodic_max_duration_seconds,omitempty"`
	// PeriodicHasPrompt is true when the periodic config has a prompt set
	// (either a free-text Prompt body or a named PromptName).
	PeriodicHasPrompt bool `json:"periodic_has_prompt,omitempty"`
	// PeriodicPromptPreview is a short preview of the free-text Prompt body only
	// (first line, trimmed, truncated to ~80 runes). Empty for named-prompt-only configs.
	PeriodicPromptPreview string `json:"periodic_prompt_preview,omitempty"`
}

// HandleListSessions handles GET /api/sessions
func (h *Handlers) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	sessions, err := store.List()
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to list sessions", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to list sessions")
		return
	}

	// Sort by update time, most recently used first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	// Build response with periodic status and scheduling info
	response := make([]SessionListResponse, len(sessions))
	for i := range sessions {
		meta := sessions[i]
		response[i] = SessionListResponse{
			Metadata:           meta,
			PeriodicConfigured: false, // Default to false
			PeriodicEnabled:    false, // Default to false
		}
		// Check if a periodic config exists for this session
		periodicStore := store.Periodic(meta.SessionID)
		if periodic, err := periodicStore.Get(); err == nil && periodic != nil {
			// Periodic config exists — show editor UI regardless of enabled state
			response[i].PeriodicConfigured = true
			// PeriodicEnabled reflects whether runs are active (config.Enabled)
			response[i].PeriodicEnabled = periodic.Enabled
			// Include scheduling info for progress indicator
			if periodic.NextScheduledAt != nil && !periodic.NextScheduledAt.IsZero() {
				response[i].NextScheduledAt = periodic.NextScheduledAt
			}
			response[i].PeriodicFrequency = &periodic.Frequency
			if periodic.StoppedReason != "" {
				response[i].PeriodicStoppedReason = string(periodic.StoppedReason)
			}
			// Glance fields for conversation header display.
			response[i].PeriodicTrigger = string(periodic.EffectiveTrigger())
			response[i].PeriodicIterationCount = periodic.IterationCount
			response[i].PeriodicMaxIterations = periodic.MaxIterations
			response[i].PeriodicDelaySeconds = periodic.DelaySeconds
			response[i].PeriodicMaxDurationSeconds = periodic.MaxDurationSeconds
			// Prompt presence flag and free-text preview for the selector UI.
			response[i].PeriodicHasPrompt = periodic.Prompt != "" || periodic.PromptName != ""
			response[i].PeriodicPromptPreview = periodic.PromptPreview()
		}
		// Check if session is currently waiting for children (runtime state from SessionManager)
		if h.deps.SessionManager != nil {
			response[i].IsWaitingForChildren = h.deps.SessionManager.IsWaitingForChildren(meta.SessionID)
			response[i].IsStreaming = h.deps.SessionManager.IsStreaming(meta.SessionID)
		}
	}

	writeJSONOK(w, response)
}
