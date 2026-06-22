package conversation

// Follow-up suggestions cluster for BackgroundSession.
// All logic lives in follow_up_coordinator.go (followUpCoordinator collaborator).
// The methods below are thin delegators that pass bs as the followUpDeps seam.

import (
	"context"
	"log/slog"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// =============================================================================
// Thin delegators
// =============================================================================

func (bs *BackgroundSession) sendCachedActionButtonsTo(observer SessionObserver) {
	bs.followUpCoord.sendCachedActionButtonsTo(bs, observer)
}

func (bs *BackgroundSession) analyzeFollowUpQuestions(userPrompt, agentMessage string) {
	bs.followUpCoord.analyzeFollowUpQuestions(bs, userPrompt, agentMessage)
}

func (bs *BackgroundSession) applyAfterProcessors(
	ctx context.Context,
	userPrompt, senderID, stopReason string,
	startedAt, endedAt time.Time,
	promptResp acp.PromptResponse,
	sessionIdle bool,
) {
	bs.followUpCoord.applyAfterProcessors(bs, ctx, userPrompt, senderID, stopReason, startedAt, endedAt, promptResp, sessionIdle)
}

// TriggerFollowUpSuggestions triggers follow-up suggestions analysis for a resumed session.
// Returns true if the analysis was triggered or cached buttons were loaded, false if skipped.
func (bs *BackgroundSession) TriggerFollowUpSuggestions() bool {
	return bs.followUpCoord.triggerFollowUpSuggestions(bs)
}

func (bs *BackgroundSession) clearActionButtons() {
	bs.followUpCoord.clearActionButtons(bs)
}

// GetActionButtons returns the current action buttons (memory cache first, then disk).
func (bs *BackgroundSession) GetActionButtons() []ActionButton {
	return bs.followUpCoord.getActionButtons(bs)
}

// =============================================================================
// followUpDeps concrete implementation on *BackgroundSession
// =============================================================================

func (bs *BackgroundSession) fuSessionID() string     { return bs.persistedID }
func (bs *BackgroundSession) fuLogger() *slog.Logger  { return bs.logger }
func (bs *BackgroundSession) fuIsClosed() bool        { return bs.IsClosed() }
func (bs *BackgroundSession) fuIsPrompting() bool     { return bs.IsPrompting() }
func (bs *BackgroundSession) fuWorkspaceUUID() string { return bs.workspaceUUID }
func (bs *BackgroundSession) fuWorkingDir() string    { return bs.workingDir }
func (bs *BackgroundSession) fuSessionDir() string {
	if bs.store == nil || bs.persistedID == "" {
		return ""
	}
	return bs.store.SessionDir(bs.persistedID)
}

func (bs *BackgroundSession) fuCASFollowUpInProgress() bool {
	return bs.followUpInProgress.CompareAndSwap(false, true)
}
func (bs *BackgroundSession) fuLoadFollowUpInProgress() bool {
	return bs.followUpInProgress.Load()
}
func (bs *BackgroundSession) fuStoreFollowUpInProgressFalse() {
	bs.followUpInProgress.Store(false)
}

func (bs *BackgroundSession) fuRLockActionButtons()   { bs.actionButtonsMu.RLock() }
func (bs *BackgroundSession) fuRUnlockActionButtons() { bs.actionButtonsMu.RUnlock() }
func (bs *BackgroundSession) fuLockActionButtons()    { bs.actionButtonsMu.Lock() }
func (bs *BackgroundSession) fuUnlockActionButtons()  { bs.actionButtonsMu.Unlock() }

func (bs *BackgroundSession) fuGetCachedActionButtons() []ActionButton {
	return bs.cachedActionButtons
}
func (bs *BackgroundSession) fuSetCachedActionButtons(b []ActionButton) {
	bs.cachedActionButtons = b
}

func (bs *BackgroundSession) fuGetActionButtonsStore() *session.ActionButtonsStore {
	if bs.store == nil || bs.persistedID == "" {
		return nil
	}
	return bs.store.ActionButtons(bs.persistedID)
}
func (bs *BackgroundSession) fuGetEventCount() int { return bs.GetEventCount() }

func (bs *BackgroundSession) fuHasAuxiliaryManager() bool { return bs.auxiliaryManager != nil }
func (bs *BackgroundSession) fuAnalyzeFollowUpQuestions(ctx context.Context, workspaceUUID, userPrompt, agentMessage string) ([]ActionButton, error) {
	suggestions, err := bs.auxiliaryManager.AnalyzeFollowUpQuestions(ctx, workspaceUUID, userPrompt, agentMessage)
	if err != nil {
		return nil, err
	}
	buttons := make([]ActionButton, 0, len(suggestions))
	for _, s := range suggestions {
		buttons = append(buttons, ActionButton{Label: s.Label, Response: s.Value})
	}
	return buttons, nil
}

func (bs *BackgroundSession) fuApplyAfterProcessors(ctx context.Context, input processors.AfterProcessorInput) processors.ApplyAfterResult {
	return bs.processorManager.ApplyAfter(ctx, input)
}

func (bs *BackgroundSession) fuIsStoreAvailable() bool {
	return bs.store != nil && bs.persistedID != ""
}
func (bs *BackgroundSession) fuReadEvents() ([]session.Event, error) {
	return bs.store.ReadEvents(bs.persistedID)
}
func (bs *BackgroundSession) fuGetUserData() (*session.UserData, error) {
	return bs.store.GetUserData(bs.persistedID)
}
func (bs *BackgroundSession) fuSetUserData(data *session.UserData) error {
	return bs.store.SetUserData(bs.persistedID, data)
}

func (bs *BackgroundSession) fuActionButtonsEnabled() bool {
	return bs.actionButtonsConfig.IsEnabled()
}

func (bs *BackgroundSession) fuNotifyObservers(fn func(SessionObserver)) {
	bs.notifyObservers(fn)
}
func (bs *BackgroundSession) fuUINotify(req UINotifyRequest) error {
	return bs.UINotify(req)
}
