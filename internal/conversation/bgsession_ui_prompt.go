package conversation

// UI prompt cluster for BackgroundSession.
// Implements the mcpserver.UIPrompter interface.

import (
	"context"
	"fmt"
	"time"
)

// =============================================================================
// UIPrompter Implementation
// =============================================================================

// UIPrompt displays an interactive prompt to the user and blocks until they respond
// or the timeout expires. This implements the mcpserver.UIPrompter interface.
//
// If a new prompt is sent while one is pending, the previous prompt is
// dismissed (with reason "replaced") and replaced by the new one.
func (bs *BackgroundSession) UIPrompt(ctx context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	bs.activePromptMu.Lock()

	// Dismiss any existing prompt (new prompt replaces old one)
	if bs.activePrompt != nil {
		bs.dismissActivePromptLocked("replaced")
	}

	// Create timeout context
	timeoutDuration := time.Duration(req.TimeoutSeconds) * time.Second
	if timeoutDuration <= 0 {
		timeoutDuration = 5 * time.Minute // Default timeout
	}
	promptCtx, cancel := context.WithTimeout(ctx, timeoutDuration)

	// Create response channel
	responseCh := make(chan UIPromptResponse, 1)
	bs.activePrompt = &activeUIPrompt{
		request:    req,
		responseCh: responseCh,
		cancelFn:   cancel,
	}

	bs.activePromptMu.Unlock()

	if bs.logger != nil {
		bs.logger.Info("UI prompt started",
			"session_id", bs.persistedID,
			"request_id", req.RequestID,
			"prompt_type", req.Type,
			"question", req.Question,
			"option_count", len(req.Options),
			"timeout_seconds", req.TimeoutSeconds)
	}

	// Flush markdown buffer before sending UI prompt.
	// This ensures any buffered content (tables, lists, code blocks) is sent to
	// observers before the prompt, so users see the full context of what the
	// agent said before being asked to make a decision.
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}

	// Broadcast to all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnUIPrompt(req)
	})

	// Notify callback that a blocking UI prompt started
	if req.Blocking && bs.onUIPromptStateChanged != nil {
		bs.onUIPromptStateChanged(bs.persistedID, true)
		defer bs.onUIPromptStateChanged(bs.persistedID, false)
	}

	// Wait for response, timeout, or cancellation
	select {
	case resp := <-responseCh:
		cancel()
		if bs.logger != nil {
			bs.logger.Info("UI prompt answered",
				"session_id", bs.persistedID,
				"request_id", req.RequestID,
				"option_id", resp.OptionID,
				"label", resp.Label)
		}
		return resp, nil

	case <-promptCtx.Done():
		bs.activePromptMu.Lock()
		// Only dismiss if this prompt is still the active one. When a prompt is
		// replaced by a newer one, both responseCh and promptCtx.Done() fire
		// simultaneously (the replacer cancels our context). If select picks
		// Done(), we must not dismiss the replacement prompt.
		if bs.activePrompt != nil && bs.activePrompt.request.RequestID == req.RequestID {
			bs.dismissActivePromptLocked("timeout")
		}
		bs.activePromptMu.Unlock()
		if bs.logger != nil {
			bs.logger.Info("UI prompt timed out",
				"session_id", bs.persistedID,
				"request_id", req.RequestID,
				"has_observers", bs.HasObservers())
		}
		// Notify all clients if the user was not actively viewing this session.
		// This triggers a native OS notification so the user knows they missed a prompt.
		if req.Blocking && !bs.HasObservers() && bs.onUIPromptTimeout != nil {
			sessionName := ""
			if bs.store != nil {
				if meta, err := bs.store.GetMetadata(bs.persistedID); err == nil {
					sessionName = meta.Name
				}
			}
			go bs.onUIPromptTimeout(bs.persistedID, req, sessionName)
		}
		return UIPromptResponse{RequestID: req.RequestID, TimedOut: true}, nil

	case <-bs.ctx.Done():
		// Session closed
		bs.activePromptMu.Lock()
		if bs.activePrompt != nil && bs.activePrompt.request.RequestID == req.RequestID {
			bs.dismissActivePromptLocked("cancelled")
		}
		bs.activePromptMu.Unlock()
		return UIPromptResponse{}, bs.ctx.Err()
	}
}

// DismissPrompt cancels any active prompt with the given request ID.
// This is called when the prompt should be dismissed (e.g., session activity).
func (bs *BackgroundSession) DismissPrompt(requestID string) {
	bs.activePromptMu.Lock()
	defer bs.activePromptMu.Unlock()

	if bs.activePrompt == nil || bs.activePrompt.request.RequestID != requestID {
		return
	}

	bs.dismissActivePromptLocked("cancelled")
}

// DismissActiveUIPrompt dismisses any active UI prompt, regardless of its request ID.
// This is called when the session is cancelled (e.g., user presses Stop button)
// to clean up any MCP tool UI prompts that are waiting for user input.
func (bs *BackgroundSession) DismissActiveUIPrompt() {
	bs.activePromptMu.Lock()
	defer bs.activePromptMu.Unlock()

	if bs.activePrompt == nil {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Dismissing active UI prompt due to session cancel",
			"session_id", bs.persistedID,
			"request_id", bs.activePrompt.request.RequestID)
	}

	bs.dismissActivePromptLocked("cancelled")
}

// HandleUIPromptAnswer processes a user's response to a UI prompt.
// This is called by SessionWSClient when it receives a ui_prompt_answer message.
func (bs *BackgroundSession) HandleUIPromptAnswer(requestID, optionID, label, freeText string) {
	bs.activePromptMu.Lock()

	if bs.activePrompt == nil || bs.activePrompt.request.RequestID != requestID {
		if bs.logger != nil {
			bs.logger.Debug("UI prompt answer ignored (no matching prompt)",
				"session_id", bs.persistedID,
				"request_id", requestID)
		}
		bs.activePromptMu.Unlock()
		return
	}

	// Send response (non-blocking - channel has buffer of 1)
	select {
	case bs.activePrompt.responseCh <- UIPromptResponse{
		RequestID: requestID,
		OptionID:  optionID,
		Label:     label,
		FreeText:  freeText,
		Aborted:   optionID == "abort",
	}:
	default:
		// Already received a response - ignore duplicate
	}

	// Record in history
	if bs.recorder != nil {
		bs.recorder.RecordUIPromptAnswer(requestID, optionID, label)
	}

	// Clean up
	bs.activePrompt.cancelFn()
	bs.activePrompt = nil

	bs.activePromptMu.Unlock()

	// Notify frontend to dismiss (do this in a goroutine to avoid blocking,
	// matching the pattern used in dismissActivePromptLocked)
	// The frontend also clears optimistically, but this ensures the prompt
	// is dismissed even if there's a race condition
	go bs.notifyObservers(func(o SessionObserver) {
		o.OnUIPromptDismiss(requestID, "answered")
	})
}

// dismissActivePromptLocked dismisses the active prompt with the given reason.
// Must be called with activePromptMu held.
func (bs *BackgroundSession) dismissActivePromptLocked(reason string) {
	if bs.activePrompt == nil {
		return
	}

	requestID := bs.activePrompt.request.RequestID
	bs.activePrompt.cancelFn()

	// Send timeout response to unblock the waiting goroutine
	select {
	case bs.activePrompt.responseCh <- UIPromptResponse{RequestID: requestID, TimedOut: true}:
	default:
	}

	bs.activePrompt = nil

	// Notify frontend to dismiss (do this outside the lock to avoid deadlock)
	go bs.notifyObservers(func(o SessionObserver) {
		o.OnUIPromptDismiss(requestID, reason)
	})
}

// GetActiveUIPrompt returns the currently active UI prompt, if any.
// Used to send cached prompt to new observers.
func (bs *BackgroundSession) GetActiveUIPrompt() *UIPromptRequest {
	bs.activePromptMu.Lock()
	defer bs.activePromptMu.Unlock()

	if bs.activePrompt == nil {
		return nil
	}

	// Return a copy
	req := bs.activePrompt.request
	return &req
}

// UINotify sends a fire-and-forget notification to all UI observers.
// This implements the mcpserver.UIPrompter interface (UINotify method).
// Unlike UIPrompt, this is non-blocking — it dispatches the notification
// to all observers and returns immediately without waiting for any response.
func (bs *BackgroundSession) UINotify(req UINotifyRequest) error {
	if bs.IsClosed() {
		return fmt.Errorf("session is closed")
	}
	bs.notifyObservers(func(o SessionObserver) {
		o.OnNotification(req)
	})
	return nil
}
