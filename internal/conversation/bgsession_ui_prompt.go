package conversation

// UI prompt cluster for BackgroundSession.
// Implements the mcpserver.UIPrompter interface.
//
// All logic lives in ui_prompt_center.go (uiPromptCenter collaborator).
// The methods below are thin delegators that pass bs as the uiPromptDeps seam.

import (
	"context"
	"log/slog"
)

// =============================================================================
// UIPrompter — thin delegators
// =============================================================================

// UIPrompt displays an interactive prompt to the user and blocks until they respond
// or the timeout expires. This implements the mcpserver.UIPrompter interface.
func (bs *BackgroundSession) UIPrompt(ctx context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	return bs.uiPromptCtr.uiPrompt(bs, ctx, req)
}

// DismissPrompt cancels any active prompt with the given request ID.
func (bs *BackgroundSession) DismissPrompt(requestID string) {
	bs.uiPromptCtr.dismissPrompt(bs, requestID)
}

// DismissActiveUIPrompt dismisses any active UI prompt, regardless of its request ID.
func (bs *BackgroundSession) DismissActiveUIPrompt() {
	bs.uiPromptCtr.dismissActiveUIPrompt(bs)
}

// HandleUIPromptAnswer processes a user's response to a UI prompt.
func (bs *BackgroundSession) HandleUIPromptAnswer(requestID, optionID, label, freeText string) {
	bs.uiPromptCtr.handleUIPromptAnswer(bs, requestID, optionID, label, freeText)
}

// GetActiveUIPrompt returns the currently active UI prompt, if any.
func (bs *BackgroundSession) GetActiveUIPrompt() *UIPromptRequest {
	return bs.uiPromptCtr.getActiveUIPrompt(bs)
}

// UINotify sends a fire-and-forget notification to all UI observers.
// This implements the mcpserver.UIPrompter interface (UINotify method).
func (bs *BackgroundSession) UINotify(req UINotifyRequest) error {
	return bs.uiPromptCtr.uiNotify(bs, req)
}

// dismissActivePromptLocked dismisses the active prompt with the given reason.
// Must be called with activePromptMu held.
func (bs *BackgroundSession) dismissActivePromptLocked(reason string) {
	if bs.activePrompt == nil {
		return
	}

	requestID := bs.activePrompt.request.RequestID
	bs.activePrompt.cancelFn()

	// Send timeout response to unblock the waiting goroutine.
	select {
	case bs.activePrompt.responseCh <- UIPromptResponse{RequestID: requestID, TimedOut: true}:
	default:
	}

	bs.activePrompt = nil

	// Notify frontend to dismiss (outside the lock to avoid deadlock).
	go bs.notifyObservers(func(o SessionObserver) {
		o.OnUIPromptDismiss(requestID, reason)
	})
}

// =============================================================================
// uiPromptDeps concrete implementation on *BackgroundSession
// =============================================================================

func (bs *BackgroundSession) upSessionID() string    { return bs.persistedID }
func (bs *BackgroundSession) upLogger() *slog.Logger { return bs.logger }
func (bs *BackgroundSession) upIsClosed() bool       { return bs.IsClosed() }
func (bs *BackgroundSession) upSessionCtx() context.Context {
	return bs.ctx
}

func (bs *BackgroundSession) upLockPromptMu()   { bs.activePromptMu.Lock() }
func (bs *BackgroundSession) upUnlockPromptMu() { bs.activePromptMu.Unlock() }

func (bs *BackgroundSession) upGetActivePrompt() *activeUIPrompt { return bs.activePrompt }
func (bs *BackgroundSession) upSetActivePrompt(p *activeUIPrompt) {
	bs.activePrompt = p
}
func (bs *BackgroundSession) upDismissActivePromptLocked(reason string) {
	bs.dismissActivePromptLocked(reason)
}

func (bs *BackgroundSession) upNotifyObservers(fn func(SessionObserver)) {
	bs.notifyObservers(fn)
}
func (bs *BackgroundSession) upHasObservers() bool { return bs.HasObservers() }

func (bs *BackgroundSession) upFlushMarkdown() {
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}
}

func (bs *BackgroundSession) upHasUIPromptStateChangedHook() bool {
	return bs.onUIPromptStateChanged != nil
}
func (bs *BackgroundSession) upNotifyUIPromptStateChanged(active bool) {
	if bs.onUIPromptStateChanged != nil {
		bs.onUIPromptStateChanged(bs.persistedID, active)
	}
}

func (bs *BackgroundSession) upHasUIPromptTimeoutHook() bool {
	return bs.onUIPromptTimeout != nil
}
func (bs *BackgroundSession) upTriggerUIPromptTimeout(req UIPromptRequest) {
	if bs.onUIPromptTimeout == nil {
		return
	}
	sessionName := ""
	if bs.store != nil {
		if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil {
			sessionName = meta.Name
		}
	}
	bs.onUIPromptTimeout(bs.persistedID, req, sessionName)
}

func (bs *BackgroundSession) upRecordUIPromptAnswer(requestID, optionID, label string) {
	if bs.recorder != nil {
		bs.recorder.RecordUIPromptAnswer(requestID, optionID, label)
	}
}
