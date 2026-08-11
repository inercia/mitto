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
	// LoopConfigured is true when a loop config exists for this session.
	// Controls editor UI mode (shows frequency panel and lock/unlock buttons).
	// A conversation with LoopConfigured=true but LoopEnabled=false is
	// a "draft" loop — editor visible but runs not yet active.
	LoopConfigured bool `json:"loop_configured"`
	// LoopEnabled is true when loop runs are active (config.Enabled == true).
	// Drives the sidebar LOOP category and clock icon. A paused/draft loop
	// conversation has LoopConfigured=true but LoopEnabled=false and falls
	// into the regular Conversations group.
	LoopEnabled bool `json:"loop_enabled"`
	// NextScheduledAt is the next scheduled time for loop sessions (nil if not loop or not scheduled).
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
	// LoopFrequency is the frequency configuration for loop sessions (nil if not loop).
	LoopFrequency *session.Frequency `json:"loop_frequency,omitempty"`
	// IsWaitingForChildren is true when the session is currently blocked on mitto_children_tasks_wait.
	// This is a runtime state (not persisted) tracked by the SessionManager.
	IsWaitingForChildren bool `json:"is_waiting_for_children,omitempty"`
	// IsStreaming is true when the session is currently prompting (agent streaming).
	// This is a runtime state (not persisted) tracked by the SessionManager.
	IsStreaming bool `json:"is_streaming,omitempty"`
	// IsWaitingForUserInput is true when the session currently has a blocking UI
	// prompt (mitto_ui_options/mitto_ui_form/mitto_ui_textbox) awaiting a response.
	// Runtime state (not persisted) tracked by the SessionManager. Used to
	// restore the sidebar "?" indicator on page reload.
	IsWaitingForUserInput bool `json:"is_waiting_for_user_input,omitempty"`
	// AckedUIPromptRequestID is the RequestID of the currently active UI prompt
	// that the user has dismissed from the sidebar. When it equals the active
	// prompt's RequestID, the "?" indicator stays hidden across reloads and
	// connected browsers. Cleared when the prompt resolves.
	AckedUIPromptRequestID string `json:"acked_ui_prompt_request_id,omitempty"`
	// LoopStoppedReason is the reason the loop was auto-stopped (empty when still running).
	LoopStoppedReason string `json:"loop_stopped_reason,omitempty"`
	// LoopAcknowledgedStoppedReason is the StoppedReason the user has dismissed
	// (persisted server-side so the sidebar warning stays hidden across reloads
	// and connected browsers). The UI hides the amber warning icon when this
	// equals LoopStoppedReason.
	LoopAcknowledgedStoppedReason string `json:"loop_acknowledged_stopped_reason,omitempty"`
	// LoopTrigger is the primary trigger (resolved via EffectiveTrigger so schedule loops
	// always report "schedule", never the empty-string default). Kept as the legacy scalar;
	// new consumers read LoopTriggers.
	LoopTrigger string `json:"loop_trigger,omitempty"`
	// LoopTriggers is the full armed trigger set of a multi-trigger loop (mitto-r6j).
	LoopTriggers []string `json:"loop_triggers,omitempty"`
	// LoopIterationCount is the number of scheduled runs delivered so far.
	LoopIterationCount int `json:"loop_iteration_count,omitempty"`
	// LoopMaxIterations is the per-prompt cap on scheduled runs (0 = unlimited).
	LoopMaxIterations int `json:"loop_max_iterations,omitempty"`
	// LoopDelaySeconds is the wait in seconds after agent idle before the next onCompletion run.
	LoopDelaySeconds int `json:"loop_delay_seconds,omitempty"`
	// LoopMaxDurationSeconds is the wall-clock cap in seconds since iterating started (0 = unlimited).
	LoopMaxDurationSeconds int `json:"loop_max_duration_seconds,omitempty"`
	// LoopHasPrompt is true when the loop config has a prompt set
	// (either a free-text Prompt body or a named PromptName).
	LoopHasPrompt bool `json:"loop_has_prompt,omitempty"`
	// LoopPromptPreview is a short preview of the free-text Prompt body only
	// (first line, trimmed, truncated to ~80 runes). Empty for named-prompt-only configs.
	LoopPromptPreview string `json:"loop_prompt_preview,omitempty"`
	// WorkspaceUUID is the UUID of the workspace this session belongs to
	// (mitto-pscc.5.1). Derived at list time, never persisted on
	// session.Metadata: a live session resolves it from the running
	// BackgroundSession; otherwise it is looked up from the workspace
	// registry by (WorkingDir, ACPServer). Empty when no workspace matches —
	// deliberately not defaulted, so this field never fabricates membership.
	WorkspaceUUID string `json:"workspace_uuid,omitempty"`
	// WorkspaceName is the resolved workspace's friendly display name (may be
	// empty even when WorkspaceUUID is set, since Name is optional on
	// WorkspaceSettings). Included so CLI/UI consumers can filter or display
	// by name without a second round trip to a workspaces endpoint.
	WorkspaceName string `json:"workspace_name,omitempty"`
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

	// Build response with loop status and scheduling info
	response := make([]SessionListResponse, len(sessions))
	for i := range sessions {
		meta := sessions[i]
		response[i] = SessionListResponse{
			Metadata:       meta,
			LoopConfigured: false, // Default to false
			LoopEnabled:    false, // Default to false
		}
		// Check if a loop config exists for this session
		loopStore := store.Loop(meta.SessionID)
		if loop, err := loopStore.Get(); err == nil && loop != nil {
			// Loop config exists — show editor UI regardless of enabled state
			response[i].LoopConfigured = true
			// LoopEnabled reflects whether runs are active (config.Enabled)
			response[i].LoopEnabled = loop.Enabled
			// Include scheduling info for progress indicator
			if loop.NextScheduledAt != nil && !loop.NextScheduledAt.IsZero() {
				response[i].NextScheduledAt = loop.NextScheduledAt
			}
			response[i].LoopFrequency = &loop.Frequency
			if loop.StoppedReason != "" {
				response[i].LoopStoppedReason = string(loop.StoppedReason)
			}
			if loop.AcknowledgedStoppedReason != "" {
				response[i].LoopAcknowledgedStoppedReason = string(loop.AcknowledgedStoppedReason)
			}
			// Glance fields for conversation header display.
			response[i].LoopTrigger = string(loop.EffectiveTrigger())
			for _, t := range loop.EffectiveTriggers() {
				response[i].LoopTriggers = append(response[i].LoopTriggers, string(t))
			}
			response[i].LoopIterationCount = loop.IterationCount
			response[i].LoopMaxIterations = loop.MaxIterations
			response[i].LoopDelaySeconds = loop.DelaySeconds
			response[i].LoopMaxDurationSeconds = loop.MaxDurationSeconds
			// Prompt presence flag and free-text preview for the selector UI.
			response[i].LoopHasPrompt = loop.HasPrompt()
			response[i].LoopPromptPreview = loop.PromptPreview()
		}
		// Check if session is currently waiting for children (runtime state from SessionManager)
		if h.deps.SessionManager != nil {
			response[i].IsWaitingForChildren = h.deps.SessionManager.IsWaitingForChildren(meta.SessionID)
			response[i].IsStreaming = h.deps.SessionManager.IsStreaming(meta.SessionID)
			response[i].IsWaitingForUserInput = h.deps.SessionManager.IsWaitingForUserInput(meta.SessionID)
			response[i].AckedUIPromptRequestID = h.deps.SessionManager.GetAckedUIPromptRequestID(meta.SessionID)

			// Resolve workspace identity (mitto-pscc.5.1): prefer the live
			// session's own resolved UUID (agrees with runtime), else fall
			// back to a (WorkingDir, ACPServer) registry lookup for
			// not-currently-loaded sessions. Left empty if neither
			// resolves — never guess the default workspace here, since a
			// wrong UUID would be worse than an absent one for a filter.
			workspaceUUID := h.deps.SessionManager.GetWorkspaceUUIDForSession(meta.SessionID)
			if workspaceUUID == "" {
				if ws := h.deps.SessionManager.GetWorkspaceByDirAndACP(meta.WorkingDir, meta.ACPServer); ws != nil {
					workspaceUUID = ws.UUID
				}
			}
			if workspaceUUID != "" {
				response[i].WorkspaceUUID = workspaceUUID
				if ws := h.deps.SessionManager.GetWorkspaceByUUID(workspaceUUID); ws != nil {
					response[i].WorkspaceName = ws.Name
				}
			}
		}
	}

	writeJSONOK(w, response)
}
