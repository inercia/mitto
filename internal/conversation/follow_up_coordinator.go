package conversation

// Follow-up suggestions + action-button collaborator — stateless; state lives on BackgroundSession.

import (
	"context"
	"log/slog"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// followUpDeps is the minimal interface followUpCoordinator needs from BackgroundSession.
// All methods are prefixed with "fu" to avoid clashes with BackgroundSession's public API.
type followUpDeps interface {
	// Identity / lifecycle
	fuSessionID() string
	fuLogger() *slog.Logger
	fuIsClosed() bool
	fuIsPrompting() bool
	fuWorkspaceUUID() string
	fuWorkingDir() string
	fuSessionDir() string // session directory for after-processor state; empty if no store

	// Follow-up analysis atomic flag.
	fuCASFollowUpInProgress() bool // CompareAndSwap false→true; true = successfully claimed
	fuLoadFollowUpInProgress() bool
	fuStoreFollowUpInProgressFalse()

	// Action buttons in-memory cache (lock ops + locked accessors).
	fuRLockActionButtons()
	fuRUnlockActionButtons()
	fuLockActionButtons()
	fuUnlockActionButtons()
	fuGetCachedActionButtons() []ActionButton  // caller holds any lock variant
	fuSetCachedActionButtons(b []ActionButton) // caller holds Lock

	// Action buttons disk store (nil if no store/session).
	fuGetActionButtonsStore() *session.ActionButtonsStore
	fuGetEventCount() int

	// Auxiliary follow-up analysis (result already converted to ActionButton slice).
	fuHasAuxiliaryManager() bool
	fuAnalyzeFollowUpQuestions(ctx context.Context, workspaceUUID, userPrompt, agentMessage string) ([]ActionButton, error)

	// Processor pipeline.
	fuApplyAfterProcessors(ctx context.Context, input processors.AfterProcessorInput) processors.ApplyAfterResult
	// fuWorkspaceProcessorArgOverrides returns the per-workspace processor argument overrides
	// from the folder's .mittorc (procName → argName → value). Used to populate
	// AfterProcessorInput.ProcessorArgOverrides for Go-template .Args in prompt-mode processors.
	fuWorkspaceProcessorArgOverrides() map[string]map[string]string

	// Session store.
	fuIsStoreAvailable() bool
	fuReadEvents() ([]session.Event, error)
	fuGetUserData() (*session.UserData, error)
	fuSetUserData(data *session.UserData) error

	// Config.
	fuActionButtonsEnabled() bool

	// Observers + fire-and-forget notifications.
	fuNotifyObservers(fn func(SessionObserver))
	fuUINotify(req UINotifyRequest) error
}

// followUpCoordinator is a stateless collaborator that owns follow-up suggestion
// analysis, action-button cache management, after-processor orchestration, and disk
// persistence previously living in bgsession_followup.go.
type followUpCoordinator struct{}

// sendCachedActionButtonsTo sends cached action buttons to a single observer.
func (c followUpCoordinator) sendCachedActionButtonsTo(d followUpDeps, observer SessionObserver) {
	buttons := c.getActionButtons(d)
	if len(buttons) == 0 {
		return
	}
	if l := d.fuLogger(); l != nil {
		l.Debug("Sending cached action buttons to new observer", "button_count", len(buttons))
	}
	observer.OnActionButtons(buttons)
}

// analyzeFollowUpQuestions asynchronously analyzes an agent message for follow-up questions.
// It guards against concurrent analysis using the followUpInProgress atomic flag (CAS).
func (c followUpCoordinator) analyzeFollowUpQuestions(d followUpDeps, userPrompt, agentMessage string) {
	if !d.fuCASFollowUpInProgress() {
		if l := d.fuLogger(); l != nil {
			l.Debug("follow-up analysis: skipped, another analysis already in progress")
		}
		return
	}
	defer d.fuStoreFollowUpInProgressFalse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if d.fuIsClosed() {
		d.fuLogger().Debug("follow-up analysis skipped: session closed")
		return
	}
	d.fuLogger().Debug("follow-up analysis: starting",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage),
		"workspace_uuid", d.fuWorkspaceUUID())

	if !d.fuHasAuxiliaryManager() {
		d.fuLogger().Debug("follow-up analysis: no auxiliary manager available")
		return
	}

	buttons, err := d.fuAnalyzeFollowUpQuestions(ctx, d.fuWorkspaceUUID(), userPrompt, agentMessage)
	if err != nil {
		d.fuLogger().Debug("follow-up analysis failed", "error", err, "workspace_uuid", d.fuWorkspaceUUID())
		return
	}
	if len(buttons) == 0 {
		d.fuLogger().Debug("follow-up analysis: no suggestions found")
		return
	}
	if d.fuIsClosed() {
		d.fuLogger().Debug("follow-up analysis: session closed before sending buttons")
		return
	}
	if d.fuIsPrompting() {
		d.fuLogger().Debug("follow-up analysis: session is prompting, discarding buttons")
		return
	}

	d.fuLockActionButtons()
	d.fuSetCachedActionButtons(buttons)
	d.fuUnlockActionButtons()

	if abStore := d.fuGetActionButtonsStore(); abStore != nil {
		sessionButtons := make([]session.ActionButton, len(buttons))
		for i, b := range buttons {
			sessionButtons[i] = session.ActionButton{Label: b.Label, Response: b.Response}
		}
		if err := abStore.Set(sessionButtons, int64(d.fuGetEventCount())); err != nil {
			d.fuLogger().Debug("failed to persist action buttons", "error", err)
		}
	}

	d.fuLogger().Debug("follow-up analysis: sending buttons to observers", "count", len(buttons))
	d.fuNotifyObservers(func(o SessionObserver) { o.OnActionButtons(buttons) })
}

// triggerFollowUpSuggestions triggers follow-up analysis for a resumed session.
// Mirrors the original TriggerFollowUpSuggestions behavior exactly.
func (c followUpCoordinator) triggerFollowUpSuggestions(d followUpDeps) bool {
	if !d.fuActionButtonsEnabled() {
		d.fuLogger().Debug("follow-up suggestions: disabled in config")
		return false
	}
	if d.fuIsPrompting() {
		d.fuLogger().Debug("follow-up suggestions: session is prompting, skipping")
		return false
	}
	if d.fuIsClosed() {
		d.fuLogger().Debug("follow-up suggestions: session is closed, skipping")
		return false
	}
	if !d.fuIsStoreAvailable() {
		d.fuLogger().Debug("follow-up suggestions: no store, skipping")
		return false
	}

	// Use cached buttons if available — no need to re-analyze.
	if cached := c.getActionButtons(d); len(cached) > 0 {
		d.fuLogger().Debug("follow-up suggestions: using cached buttons from disk", "button_count", len(cached))
		return true
	}

	events, err := d.fuReadEvents()
	if err != nil {
		d.fuLogger().Debug("follow-up suggestions: failed to read events", "error", err)
		return false
	}
	userPrompt := session.GetLastUserPrompt(events)
	agentMessage := session.GetLastAgentMessage(events)
	if agentMessage == "" {
		d.fuLogger().Debug("follow-up suggestions: no agent message found in history")
		return false
	}
	d.fuLogger().Debug("follow-up suggestions: triggering analysis for resumed session",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage))

	// Let analyzeFollowUpQuestions' CAS guard handle concurrency.
	if d.fuLoadFollowUpInProgress() {
		d.fuLogger().Debug("follow-up suggestions: analysis already in progress, skipping")
		return true
	}
	go c.analyzeFollowUpQuestions(d, userPrompt, agentMessage)
	return true
}

// clearActionButtons clears cached action buttons from memory and disk.
func (c followUpCoordinator) clearActionButtons(d followUpDeps) {
	d.fuLockActionButtons()
	hadButtons := len(d.fuGetCachedActionButtons()) > 0
	d.fuSetCachedActionButtons(nil)
	d.fuUnlockActionButtons()

	if abStore := d.fuGetActionButtonsStore(); abStore != nil {
		if err := abStore.Clear(); err != nil {
			if l := d.fuLogger(); l != nil {
				l.Debug("failed to clear action buttons from disk", "error", err)
			}
		}
	}

	if hadButtons {
		d.fuNotifyObservers(func(o SessionObserver) { o.OnActionButtons([]ActionButton{}) })
	}
}

// getActionButtons uses a two-tier lookup: memory cache first, then disk.
func (c followUpCoordinator) getActionButtons(d followUpDeps) []ActionButton {
	d.fuRLockActionButtons()
	cached := d.fuGetCachedActionButtons()
	if cached != nil {
		result := make([]ActionButton, len(cached))
		copy(result, cached)
		d.fuRUnlockActionButtons()
		return result
	}
	d.fuRUnlockActionButtons()

	if !d.fuIsStoreAvailable() {
		return nil
	}
	abStore := d.fuGetActionButtonsStore()
	if abStore == nil {
		return nil
	}
	buttons, err := abStore.Get()
	if err != nil {
		if l := d.fuLogger(); l != nil {
			l.Debug("failed to read action buttons from disk", "error", err)
		}
		return nil
	}
	result := make([]ActionButton, len(buttons))
	for i, b := range buttons {
		result[i] = ActionButton{Label: b.Label, Response: b.Response}
	}
	if len(result) > 0 {
		d.fuLockActionButtons()
		d.fuSetCachedActionButtons(result)
		d.fuUnlockActionButtons()
	}
	return result
}

// applyAfterProcessors runs the after-phase processor pipeline after an ACP turn completes.
func (c followUpCoordinator) applyAfterProcessors(
	d followUpDeps,
	ctx context.Context,
	userPrompt, senderID, stopReason string,
	startedAt, endedAt time.Time,
	promptResp acp.PromptResponse,
	sessionIdle bool,
) {
	var agentMessages []string
	if events, err := d.fuReadEvents(); err == nil {
		if msg := session.GetLastAgentMessage(events); msg != "" {
			agentMessages = []string{msg}
		}
	}

	var tokenUsage *processors.AfterTokenUsage
	if promptResp.Usage != nil {
		tokenUsage = &processors.AfterTokenUsage{
			Input:  int64(promptResp.Usage.InputTokens),
			Output: int64(promptResp.Usage.OutputTokens),
			Total:  int64(promptResp.Usage.TotalTokens),
		}
	} else {
		estimated := int64(processors.EstimateTokens(userPrompt))
		for _, msg := range agentMessages {
			estimated += int64(processors.EstimateTokens(msg))
		}
		if estimated > 0 {
			tokenUsage = &processors.AfterTokenUsage{Total: estimated}
		}
	}

	input := processors.AfterProcessorInput{
		SessionID:             d.fuSessionID(),
		SessionDir:            d.fuSessionDir(),
		WorkspaceUUID:         d.fuWorkspaceUUID(),
		WorkingDir:            d.fuWorkingDir(),
		Origin:                promptOriginFromSenderID(senderID),
		StopReason:            stopReason,
		UserPrompt:            userPrompt,
		AgentMessages:         agentMessages,
		ToolCalls:             nil,
		TokenUsage:            tokenUsage,
		StartedAt:             startedAt,
		EndedAt:               endedAt,
		SessionIdle:           sessionIdle,
		ProcessorArgOverrides: d.fuWorkspaceProcessorArgOverrides(),
	}

	result := d.fuApplyAfterProcessors(ctx, input)

	for _, pe := range result.Errors {
		if l := d.fuLogger(); l != nil {
			l.Warn("after-phase processor error (non-fatal)", "processor", pe.ProcessorName, "error", pe.Error)
		}
	}

	for _, n := range result.Notifications {
		req := UINotifyRequest{Title: n.Title, Message: n.Message, Style: n.Style}
		if err := d.fuUINotify(req); err != nil {
			if l := d.fuLogger(); l != nil {
				l.Warn("after-phase: failed to dispatch notification", "title", n.Title, "error", err)
			}
		}
	}

	if len(result.ActionButtons) > 0 {
		buttons := make([]ActionButton, 0, len(result.ActionButtons))
		for _, ab := range result.ActionButtons {
			buttons = append(buttons, ActionButton{Label: ab.Label, Response: ab.Prompt})
		}
		d.fuLockActionButtons()
		existing := d.fuGetCachedActionButtons()
		merged := make([]ActionButton, 0, len(existing)+len(buttons))
		merged = append(merged, existing...)
		merged = append(merged, buttons...)
		d.fuSetCachedActionButtons(merged)
		d.fuUnlockActionButtons()

		if abStore := d.fuGetActionButtonsStore(); abStore != nil {
			sessionButtons := make([]session.ActionButton, len(merged))
			for i, b := range merged {
				sessionButtons[i] = session.ActionButton{Label: b.Label, Response: b.Response}
			}
			if err := abStore.Set(sessionButtons, int64(d.fuGetEventCount())); err != nil {
				if l := d.fuLogger(); l != nil {
					l.Debug("after-phase: failed to persist action buttons", "error", err)
				}
			}
		}
		d.fuNotifyObservers(func(o SessionObserver) { o.OnActionButtons(merged) })
	}

	if len(result.UserDataPatch) == 0 || !d.fuIsStoreAvailable() {
		return
	}
	current, err := d.fuGetUserData()
	if err != nil {
		if l := d.fuLogger(); l != nil {
			l.Warn("after-phase: failed to read user data for patch", "error", err)
		}
		return
	}
	attrMap := make(map[string]string, len(current.Attributes))
	for _, a := range current.Attributes {
		attrMap[a.Name] = a.Value
	}
	patchedKeys := 0
	for k, v := range result.UserDataPatch {
		attrMap[k] = v
		patchedKeys++
	}
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
	if err := d.fuSetUserData(&session.UserData{Attributes: newAttrs}); err != nil {
		if l := d.fuLogger(); l != nil {
			l.Warn("after-phase: failed to persist user data patch", "patched_keys", patchedKeys, "error", err)
		}
	} else if l := d.fuLogger(); l != nil {
		l.Debug("after-phase: user data patched", "patched_keys", patchedKeys, "total_keys", len(newAttrs))
	}
}

// promptOriginFromSenderID maps a PromptMeta.SenderID to the canonical origin tag.
func promptOriginFromSenderID(senderID string) string {
	switch senderID {
	case "periodic-runner":
		return "periodic-runner"
	case "queue":
		return "queue"
	default:
		return "user"
	}
}
