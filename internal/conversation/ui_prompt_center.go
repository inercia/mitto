package conversation

// UI prompt + notify collaborator — stateless; state lives on BackgroundSession.

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// uiPromptDeps is the minimal interface uiPromptCenter needs from BackgroundSession.
// All methods are prefixed with "up" to avoid clashing with BackgroundSession's public API.
type uiPromptDeps interface {
	upSessionID() string
	upLogger() *slog.Logger
	upIsClosed() bool
	upSessionCtx() context.Context

	// activePromptMu operations — callers coordinate lock/unlock manually.
	upLockPromptMu()
	upUnlockPromptMu()
	// Locked variants: caller must hold activePromptMu before calling.
	upGetActivePrompt() *activeUIPrompt
	upSetActivePrompt(p *activeUIPrompt)
	upDismissActivePromptLocked(reason string) // caller holds lock

	// Observer fan-out.
	upNotifyObservers(fn func(SessionObserver))
	upHasObservers() bool

	// ACP markdown flush — no-op if no ACP client.
	upFlushMarkdown()

	// Optional hooks (concrete impl returns false / no-ops when not configured).
	upHasUIPromptStateChangedHook() bool
	upNotifyUIPromptStateChanged(active bool) // no-op if hook not set
	upHasUIPromptTimeoutHook() bool
	upTriggerUIPromptTimeout(req UIPromptRequest) // no-op if hook/store not set

	// Recorder — no-op if no recorder.
	upRecordUIPromptAnswer(requestID, optionID, label string)
}

// uiPromptCenter is a stateless collaborator that owns the blocking UI prompt
// and fire-and-forget UINotify logic previously living in bgsession_ui_prompt.go.
type uiPromptCenter struct{}

func (c uiPromptCenter) uiPrompt(d uiPromptDeps, ctx context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	d.upLockPromptMu()

	// Dismiss any existing prompt (new prompt replaces old one).
	if d.upGetActivePrompt() != nil {
		d.upDismissActivePromptLocked("replaced")
	}

	timeoutDuration := time.Duration(req.TimeoutSeconds) * time.Second
	if timeoutDuration <= 0 {
		timeoutDuration = 5 * time.Minute
	}
	promptCtx, cancel := context.WithTimeout(ctx, timeoutDuration)

	responseCh := make(chan UIPromptResponse, 1)
	d.upSetActivePrompt(&activeUIPrompt{
		request:    req,
		responseCh: responseCh,
		cancelFn:   cancel,
	})

	d.upUnlockPromptMu()

	if l := d.upLogger(); l != nil {
		l.Info("UI prompt started",
			"session_id", d.upSessionID(),
			"request_id", req.RequestID,
			"prompt_type", req.Type,
			"question", req.Question,
			"option_count", len(req.Options),
			"timeout_seconds", req.TimeoutSeconds)
	}

	d.upFlushMarkdown()

	d.upNotifyObservers(func(o SessionObserver) { o.OnUIPrompt(req) })

	if req.Blocking && d.upHasUIPromptStateChangedHook() {
		d.upNotifyUIPromptStateChanged(true)
		defer d.upNotifyUIPromptStateChanged(false)
	}

	select {
	case resp := <-responseCh:
		cancel()
		if l := d.upLogger(); l != nil {
			l.Info("UI prompt answered",
				"session_id", d.upSessionID(),
				"request_id", req.RequestID,
				"option_id", resp.OptionID,
				"label", resp.Label)
		}
		return resp, nil

	case <-promptCtx.Done():
		d.upLockPromptMu()
		if ap := d.upGetActivePrompt(); ap != nil && ap.request.RequestID == req.RequestID {
			d.upDismissActivePromptLocked("timeout")
		}
		d.upUnlockPromptMu()
		if l := d.upLogger(); l != nil {
			l.Info("UI prompt timed out",
				"session_id", d.upSessionID(),
				"request_id", req.RequestID,
				"has_observers", d.upHasObservers())
		}
		if req.Blocking && !d.upHasObservers() && d.upHasUIPromptTimeoutHook() {
			go d.upTriggerUIPromptTimeout(req)
		}
		return UIPromptResponse{RequestID: req.RequestID, TimedOut: true}, nil

	case <-d.upSessionCtx().Done():
		d.upLockPromptMu()
		if ap := d.upGetActivePrompt(); ap != nil && ap.request.RequestID == req.RequestID {
			d.upDismissActivePromptLocked("cancelled")
		}
		d.upUnlockPromptMu()
		return UIPromptResponse{}, d.upSessionCtx().Err()
	}
}

func (c uiPromptCenter) dismissPrompt(d uiPromptDeps, requestID string) {
	d.upLockPromptMu()
	defer d.upUnlockPromptMu()
	ap := d.upGetActivePrompt()
	if ap == nil || ap.request.RequestID != requestID {
		return
	}
	d.upDismissActivePromptLocked("cancelled")
}

func (c uiPromptCenter) dismissActiveUIPrompt(d uiPromptDeps) {
	d.upLockPromptMu()
	defer d.upUnlockPromptMu()
	if d.upGetActivePrompt() == nil {
		return
	}
	if l := d.upLogger(); l != nil {
		l.Debug("Dismissing active UI prompt due to session cancel",
			"session_id", d.upSessionID(),
			"request_id", d.upGetActivePrompt().request.RequestID)
	}
	d.upDismissActivePromptLocked("cancelled")
}

func (c uiPromptCenter) handleUIPromptAnswer(d uiPromptDeps, requestID, optionID, label, freeText string) {
	d.upLockPromptMu()

	ap := d.upGetActivePrompt()
	if ap == nil || ap.request.RequestID != requestID {
		if l := d.upLogger(); l != nil {
			l.Debug("UI prompt answer ignored (no matching prompt)",
				"session_id", d.upSessionID(),
				"request_id", requestID)
		}
		d.upUnlockPromptMu()
		return
	}

	select {
	case ap.responseCh <- UIPromptResponse{
		RequestID: requestID,
		OptionID:  optionID,
		Label:     label,
		FreeText:  freeText,
		Aborted:   optionID == "abort",
	}:
	default:
	}

	d.upRecordUIPromptAnswer(requestID, optionID, label)
	ap.cancelFn()
	d.upSetActivePrompt(nil)
	d.upUnlockPromptMu()

	go d.upNotifyObservers(func(o SessionObserver) { o.OnUIPromptDismiss(requestID, "answered") })
}

func (c uiPromptCenter) getActiveUIPrompt(d uiPromptDeps) *UIPromptRequest {
	d.upLockPromptMu()
	defer d.upUnlockPromptMu()
	ap := d.upGetActivePrompt()
	if ap == nil {
		return nil
	}
	req := ap.request
	return &req
}

func (c uiPromptCenter) uiNotify(d uiPromptDeps, req UINotifyRequest) error {
	if d.upIsClosed() {
		return fmt.Errorf("session is closed")
	}
	d.upNotifyObservers(func(o SessionObserver) { o.OnNotification(req) })
	return nil
}
