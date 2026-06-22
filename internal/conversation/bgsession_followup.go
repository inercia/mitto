package conversation

// Follow-up suggestions cluster for BackgroundSession.

import (
	"context"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// sendCachedActionButtonsTo sends cached action buttons to a single observer.
// Called when a new client connects to ensure they see the current suggestions,
// even if they connected after the suggestions were originally generated.
// This solves the problem of users switching devices or refreshing and missing suggestions.
func (bs *BackgroundSession) sendCachedActionButtonsTo(observer SessionObserver) {
	buttons := bs.GetActionButtons()
	if len(buttons) == 0 {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Sending cached action buttons to new observer", "button_count", len(buttons))
	}

	observer.OnActionButtons(buttons)
}

// analyzeFollowUpQuestions asynchronously analyzes an agent message for follow-up questions.
// It uses the auxiliary conversation to identify questions and sends suggested responses
// to observers via OnActionButtons. This is non-blocking and runs in a goroutine.
// userPrompt provides context about what the user asked.
func (bs *BackgroundSession) analyzeFollowUpQuestions(userPrompt, agentMessage string) {
	// Prevent concurrent analysis — only one goroutine should analyze at a time.
	// If another analysis is already in progress, skip this one.
	// The in-progress analysis will produce the same results since the session
	// state hasn't changed (no new prompts while both are running).
	if !bs.followUpInProgress.CompareAndSwap(false, true) {
		if bs.logger != nil {
			bs.logger.Debug("follow-up analysis: skipped, another analysis already in progress")
		}
		return
	}
	defer bs.followUpInProgress.Store(false)

	// Use a generous timeout for the auxiliary follow-up prompt.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Check if session is still valid before starting
	if bs.IsClosed() {
		bs.logger.Debug("follow-up analysis skipped: session closed")
		return
	}

	bs.logger.Debug("follow-up analysis: starting",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage),
		"workspace_uuid", bs.workspaceUUID)

	// Check if we have an auxiliary manager
	if bs.auxiliaryManager == nil {
		bs.logger.Debug("follow-up analysis: no auxiliary manager available")
		return
	}

	// Use the workspace-scoped auxiliary conversation to analyze the message
	suggestions, err := bs.auxiliaryManager.AnalyzeFollowUpQuestions(ctx, bs.workspaceUUID, userPrompt, agentMessage)
	if err != nil {
		bs.logger.Debug("follow-up analysis failed",
			"error", err,
			"workspace_uuid", bs.workspaceUUID)
		return
	}

	if len(suggestions) == 0 {
		bs.logger.Debug("follow-up analysis: no suggestions found")
		return
	}

	// Check again if session is still valid and not prompting
	// If the user has already sent a new message, don't show stale suggestions
	if bs.IsClosed() {
		bs.logger.Debug("follow-up analysis: session closed before sending buttons")
		return
	}
	if bs.IsPrompting() {
		bs.logger.Debug("follow-up analysis: session is prompting, discarding buttons")
		return
	}

	// Convert auxiliary suggestions to ActionButton format
	buttons := make([]ActionButton, 0, len(suggestions))
	for _, s := range suggestions {
		buttons = append(buttons, ActionButton{
			Label:    s.Label,
			Response: s.Value,
		})
	}

	// Cache in memory
	bs.actionButtonsMu.Lock()
	bs.cachedActionButtons = buttons
	bs.actionButtonsMu.Unlock()

	// Persist to disk
	if bs.store != nil && bs.persistedID != "" {
		abStore := bs.store.ActionButtons(bs.persistedID)
		// Convert to session.ActionButton for storage
		sessionButtons := make([]session.ActionButton, len(buttons))
		for i, b := range buttons {
			sessionButtons[i] = session.ActionButton{
				Label:    b.Label,
				Response: b.Response,
			}
		}
		eventCount := bs.GetEventCount()
		if err := abStore.Set(sessionButtons, int64(eventCount)); err != nil {
			bs.logger.Debug("failed to persist action buttons", "error", err)
		}
	}

	bs.logger.Debug("follow-up analysis: sending buttons to observers", "count", len(buttons))
	bs.notifyObservers(func(o SessionObserver) {
		o.OnActionButtons(buttons)
	})
}

// promptOriginFromSenderID maps a PromptMeta.SenderID to the canonical origin tag used
// by after-phase processors in their excludeOrigins filter.
//
// Canonical origin strings (kept in sync with processors.AfterProcessorInput.Origin docs):
//
//	"user"             – direct user prompt from a WebSocket client
//	"queue"            – message injected via the queue (includes mcp-send-prompt, which
//	                     cannot be distinguished from regular queue messages at this layer)
//	"periodic-runner"  – message sent by the periodic runner goroutine
//
// If a new origin is introduced (e.g. mcp-send-prompt queued with a dedicated SenderID),
// add it here and update the AfterProcessorInput.Origin godoc in types.go.
func promptOriginFromSenderID(senderID string) string {
	switch senderID {
	case "periodic-runner":
		return "periodic-runner"
	case "queue":
		// Covers both direct queue messages and MCP mitto_conversation_send_prompt,
		// which are indistinguishable at this layer (both use SenderID="queue").
		// TODO: when mcp-send-prompt gets a dedicated SenderID, add a case here.
		return "queue"
	default:
		// Empty SenderID (Prompt/PromptWithImages) or a WebSocket client UUID.
		return "user"
	}
}

// applyAfterProcessors runs the after-phase processor pipeline (agentResponded + agentIdle)
// after an ACP turn completes. It is called synchronously in the prompt goroutine, after
// follow-up suggestion analysis, so all events are already flushed and persisted at this point.
// sessionIdle reports whether the queue was drained after this turn; it gates agentIdle
// processors so they fire only once the agent has finished its burst of work.
//
// Results are dispatched as follows:
//   - Notifications → bs.UINotify (fire-and-forget toast)
//   - ActionButtons → appended to the existing action-buttons cache/store and broadcast
//   - UserDataPatch → merged into the session's user-data file
//   - Errors        → logged as warnings (non-fatal)
func (bs *BackgroundSession) applyAfterProcessors(
	ctx context.Context,
	userPrompt string,
	senderID string,
	stopReason string,
	startedAt, endedAt time.Time,
	promptResp acp.PromptResponse,
	sessionIdle bool,
) {
	// Build agent messages from the last persisted agent message.
	var agentMessages []string
	if bs.store != nil {
		if events, err := bs.store.ReadEvents(bs.persistedID); err == nil {
			if msg := session.GetLastAgentMessage(events); msg != "" {
				agentMessages = []string{msg}
			}
		}
	}

	// Build token usage snapshot.
	// Use actual ACP usage when available; otherwise estimate from message text
	// so that cadence token thresholds (everyNTokens) can still be met.
	var tokenUsage *processors.AfterTokenUsage
	if promptResp.Usage != nil {
		tokenUsage = &processors.AfterTokenUsage{
			Input:  int64(promptResp.Usage.InputTokens),
			Output: int64(promptResp.Usage.OutputTokens),
			Total:  int64(promptResp.Usage.TotalTokens),
		}
	} else {
		// Fallback: estimate tokens from user prompt + agent response text.
		estimated := int64(processors.EstimateTokens(userPrompt))
		for _, msg := range agentMessages {
			estimated += int64(processors.EstimateTokens(msg))
		}
		if estimated > 0 {
			tokenUsage = &processors.AfterTokenUsage{
				Total: estimated,
			}
		}
	}

	// Resolve session directory for processor state persistence (cadence + match:first).
	var sessionDir string
	if bs.store != nil && bs.persistedID != "" {
		sessionDir = bs.store.SessionDir(bs.persistedID)
	}

	input := processors.AfterProcessorInput{
		SessionID:     bs.persistedID,
		SessionDir:    sessionDir,
		WorkspaceUUID: bs.workspaceUUID,
		WorkingDir:    bs.workingDir,
		Origin:        promptOriginFromSenderID(senderID),
		StopReason:    stopReason,
		UserPrompt:    userPrompt,
		AgentMessages: agentMessages,
		ToolCalls:     nil, // TODO: populate from turn events in a future pass
		TokenUsage:    tokenUsage,
		StartedAt:     startedAt,
		EndedAt:       endedAt,
		SessionIdle:   sessionIdle,
	}

	result := bs.processorManager.ApplyAfter(ctx, input)

	// Log non-fatal processor errors as warnings.
	for _, pe := range result.Errors {
		if bs.logger != nil {
			bs.logger.Warn("after-phase processor error (non-fatal)",
				"processor", pe.ProcessorName,
				"error", pe.Error)
		}
	}

	// Dispatch notifications via UINotify (uses OnNotification observer path).
	for _, n := range result.Notifications {
		req := UINotifyRequest{
			Title:   n.Title,
			Message: n.Message,
			Style:   n.Style,
		}
		if err := bs.UINotify(req); err != nil && bs.logger != nil {
			bs.logger.Warn("after-phase: failed to dispatch notification",
				"title", n.Title,
				"error", err)
		}
	}

	// Append action buttons to the existing store and notify observers.
	if len(result.ActionButtons) > 0 {
		buttons := make([]ActionButton, 0, len(result.ActionButtons))
		for _, ab := range result.ActionButtons {
			buttons = append(buttons, ActionButton{
				Label:    ab.Label,
				Response: ab.Prompt,
			})
		}

		// Merge with any existing cached buttons (e.g. from follow-up analysis).
		bs.actionButtonsMu.Lock()
		merged := make([]ActionButton, 0, len(bs.cachedActionButtons)+len(buttons))
		merged = append(merged, bs.cachedActionButtons...)
		merged = append(merged, buttons...)
		bs.cachedActionButtons = merged
		bs.actionButtonsMu.Unlock()

		// Persist to disk.
		if bs.store != nil && bs.persistedID != "" {
			abStore := bs.store.ActionButtons(bs.persistedID)
			sessionButtons := make([]session.ActionButton, len(merged))
			for i, b := range merged {
				sessionButtons[i] = session.ActionButton{Label: b.Label, Response: b.Response}
			}
			if err := abStore.Set(sessionButtons, int64(bs.GetEventCount())); err != nil && bs.logger != nil {
				bs.logger.Debug("after-phase: failed to persist action buttons", "error", err)
			}
		}

		bs.notifyObservers(func(o SessionObserver) {
			o.OnActionButtons(merged)
		})
	}

	// Merge UserDataPatch into the session's user-data file.
	if len(result.UserDataPatch) > 0 && bs.store != nil && bs.persistedID != "" {
		// Read current user data.
		current, err := bs.store.GetUserData(bs.persistedID)
		if err != nil {
			if bs.logger != nil {
				bs.logger.Warn("after-phase: failed to read user data for patch", "error", err)
			}
		} else {
			// Build a name→value map of existing attributes for fast lookup.
			attrMap := make(map[string]string, len(current.Attributes))
			for _, a := range current.Attributes {
				attrMap[a.Name] = a.Value
			}
			// Apply patch (later processors override earlier on key collision).
			patchedKeys := 0
			for k, v := range result.UserDataPatch {
				attrMap[k] = v
				patchedKeys++
			}
			// Reconstruct ordered slice: keep existing order, then append new keys.
			newAttrs := make([]session.UserDataAttribute, 0, len(attrMap))
			seen := make(map[string]bool)
			for _, a := range current.Attributes {
				newAttrs = append(newAttrs, session.UserDataAttribute{Name: a.Name, Value: attrMap[a.Name]})
				seen[a.Name] = true
			}
			for k, v := range result.UserDataPatch {
				if !seen[k] {
					newAttrs = append(newAttrs, session.UserDataAttribute{Name: k, Value: v})
				}
			}
			if err := bs.store.SetUserData(bs.persistedID, &session.UserData{Attributes: newAttrs}); err != nil {
				if bs.logger != nil {
					bs.logger.Warn("after-phase: failed to persist user data patch",
						"patched_keys", patchedKeys,
						"error", err)
				}
			} else if bs.logger != nil {
				bs.logger.Debug("after-phase: user data patched",
					"patched_keys", patchedKeys,
					"total_keys", len(newAttrs))
			}
		}
	}
}

// TriggerFollowUpSuggestions triggers follow-up suggestions analysis for a resumed session.
// This reads the last agent message from stored events and analyzes it asynchronously.
// It only works for sessions with message history and when follow-up suggestions are enabled.
// If cached action buttons already exist, they are loaded and no new analysis is triggered.
// This is non-blocking and runs the analysis in a goroutine.
// Returns true if the analysis was triggered or cached buttons were loaded, false if skipped.
func (bs *BackgroundSession) TriggerFollowUpSuggestions() bool {
	// Check if follow-up suggestions are enabled
	if !bs.actionButtonsConfig.IsEnabled() {
		bs.logger.Debug("follow-up suggestions: disabled in config")
		return false
	}

	// Check if session is prompting (don't interfere with active prompts)
	if bs.IsPrompting() {
		bs.logger.Debug("follow-up suggestions: session is prompting, skipping")
		return false
	}

	// Check if session is closed
	if bs.IsClosed() {
		bs.logger.Debug("follow-up suggestions: session is closed, skipping")
		return false
	}

	// Need store to read events
	if bs.store == nil {
		bs.logger.Debug("follow-up suggestions: no store, skipping")
		return false
	}

	// Check if we already have cached action buttons (from disk)
	// If so, load them into memory cache - no need to re-analyze
	cachedButtons := bs.GetActionButtons()
	if len(cachedButtons) > 0 {
		bs.logger.Debug("follow-up suggestions: using cached buttons from disk",
			"button_count", len(cachedButtons))
		return true
	}

	// Read stored events for this session
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		bs.logger.Debug("follow-up suggestions: failed to read events", "error", err)
		return false
	}

	// Get the last user prompt and agent message from stored events
	userPrompt := session.GetLastUserPrompt(events)
	agentMessage := session.GetLastAgentMessage(events)
	if agentMessage == "" {
		bs.logger.Debug("follow-up suggestions: no agent message found in history")
		return false
	}

	bs.logger.Debug("follow-up suggestions: triggering analysis for resumed session",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage))

	// Check if analysis is already in progress (e.g., from prompt completion racing with session resume)
	if bs.followUpInProgress.Load() {
		bs.logger.Debug("follow-up suggestions: analysis already in progress, skipping")
		return true
	}

	// Run analysis asynchronously
	go bs.analyzeFollowUpQuestions(userPrompt, agentMessage)
	return true
}

// clearActionButtons clears the cached action buttons from memory and disk.
// Called when new conversation activity occurs (user sends a prompt) because
// the existing suggestions become stale—they were generated for the previous
// agent response, not the upcoming one. New suggestions will be generated
// when the agent completes its next response.
func (bs *BackgroundSession) clearActionButtons() {
	// Clear in-memory cache
	bs.actionButtonsMu.Lock()
	hadButtons := len(bs.cachedActionButtons) > 0
	bs.cachedActionButtons = nil
	bs.actionButtonsMu.Unlock()

	// Clear from disk
	if bs.store != nil && bs.persistedID != "" {
		abStore := bs.store.ActionButtons(bs.persistedID)
		if err := abStore.Clear(); err != nil && bs.logger != nil {
			bs.logger.Debug("failed to clear action buttons from disk", "error", err)
		}
	}

	// Notify observers that buttons are cleared (send empty array)
	if hadButtons {
		bs.notifyObservers(func(o SessionObserver) {
			o.OnActionButtons([]ActionButton{})
		})
	}
}

// GetActionButtons returns the current action buttons.
// Uses a two-tier lookup: memory cache first (fast), then disk (persistent).
// The disk fallback ensures suggestions survive server restarts.
// Returns nil if no suggestions are available.
func (bs *BackgroundSession) GetActionButtons() []ActionButton {
	// Check in-memory cache first
	bs.actionButtonsMu.RLock()
	if bs.cachedActionButtons != nil {
		result := make([]ActionButton, len(bs.cachedActionButtons))
		copy(result, bs.cachedActionButtons)
		bs.actionButtonsMu.RUnlock()
		return result
	}
	bs.actionButtonsMu.RUnlock()

	// Fall back to disk
	if bs.store == nil || bs.persistedID == "" {
		return nil
	}

	abStore := bs.store.ActionButtons(bs.persistedID)
	buttons, err := abStore.Get()
	if err != nil {
		if bs.logger != nil {
			bs.logger.Debug("failed to read action buttons from disk", "error", err)
		}
		return nil
	}

	// Convert session.ActionButton to web.ActionButton
	result := make([]ActionButton, len(buttons))
	for i, b := range buttons {
		result[i] = ActionButton{
			Label:    b.Label,
			Response: b.Response,
		}
	}

	// Cache in memory for future access
	if len(result) > 0 {
		bs.actionButtonsMu.Lock()
		bs.cachedActionButtons = result
		bs.actionButtonsMu.Unlock()
	}

	return result
}
