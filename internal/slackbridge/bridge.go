package slackbridge

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// TriggerSlack is the firedBy provenance value recorded on the delivered
// PromptMeta.LoopTrigger when the Slack bridge fires a loop (mitto-qewp
// PoC). It is intentionally NOT part of the persisted/armable loop trigger
// schema (session.LoopPrompt.Triggers) — see package doc.
const TriggerSlack session.LoopTrigger = session.TriggerOnSlack

// minReconnectBackoff/maxReconnectBackoff bound Bridge.Run's outer retry
// loop when a Source.Run call returns early (e.g. a forced disconnect the
// source itself did not absorb).
const (
	minReconnectBackoff = 1 * time.Second
	maxReconnectBackoff = 30 * time.Second
	// maxSlackTextRunes bounds untrusted external text copied into prompt context.
	maxSlackTextRunes = 4000
)

// LoopTriggerer is the minimal slice of *conversation.LoopRunner the bridge
// needs, so tests can substitute a fake without constructing a real
// LoopRunner/SessionManager/Store graph.
type LoopTriggerer interface {
	TriggerNowWithSlackEvent(sessionID string, resetTimer bool, firedBy session.LoopTrigger, evt *conversation.PromptSlackContext) error
}

// Bridge filters, deduplicates, and routes accepted Slack events to exactly
// one runtime-configured target loop conversation via LoopTriggerer,
// reusing LoopRunner's existing enabled/idle checks and dispatch/coalescing
// path (mitto-qewp PoC).
type Bridge struct {
	cfg    Config
	runner LoopTriggerer
	logger *slog.Logger
	dedupe *dedupeSet

	selfMu     sync.RWMutex
	selfUserID string
}

// NewBridge constructs a Bridge for cfg, dispatching accepted events through
// runner. logger may be nil.
func NewBridge(cfg Config, runner LoopTriggerer, logger *slog.Logger) *Bridge {
	return &Bridge{
		cfg:    cfg,
		runner: runner,
		logger: logger,
		dedupe: newDedupeSet(0),
	}
}

// SetSelfUserID records the bridge's own Slack app's bot user ID, so events
// authored by it are ignored (acceptance criterion #2, defense-in-depth
// alongside any source-side self-filtering). Safe to call concurrently with
// Run/handleEvent.
func (b *Bridge) SetSelfUserID(userID string) {
	b.selfMu.Lock()
	b.selfUserID = userID
	b.selfMu.Unlock()
}

func (b *Bridge) getSelfUserID() string {
	b.selfMu.RLock()
	defer b.selfMu.RUnlock()
	return b.selfUserID
}

// Run drives src until ctx is cancelled. If src.Run returns a non-nil error
// without ctx being done, Run treats it as a forced disconnect and retries
// with a bounded exponential backoff (acceptance criterion #5) — this is a
// second line of defense on top of whatever internal reconnect the source
// implementation already performs.
func (b *Bridge) Run(ctx context.Context, src Source) {
	backoff := minReconnectBackoff
	for {
		if ctx.Err() != nil {
			return
		}

		err := src.Run(ctx, b.handleEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil && b.logger != nil {
			// Do not log the raw transport error: SDK errors may embed connection
			// details. The operational state and retry delay are sufficient here.
			b.logger.Warn("slackbridge: event source disconnected, reconnecting", "retry_in", backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxReconnectBackoff {
			backoff = maxReconnectBackoff
		}
	}
}

// handleEvent applies the filter/dedupe/dispatch pipeline to a single
// accepted event. Never logs or persists evt.Text (untrusted external
// content) beyond the bounded template context threaded through
// LoopTriggerer.
func (b *Bridge) handleEvent(evt Event) {
	if evt.EventID == "" {
		if b.logger != nil {
			b.logger.Debug("slackbridge: dropping event with no event_id (cannot dedupe)")
		}
		return
	}
	if self := b.getSelfUserID(); self != "" && evt.AuthorID == self {
		return // Mitto's own Slack app — never self-trigger.
	}
	if evt.TeamID != b.cfg.TeamID {
		return
	}
	if evt.ChannelID != b.cfg.ChannelID {
		return
	}
	if b.dedupe.SeenBefore(evt.EventID) {
		if b.logger != nil {
			b.logger.Debug("slackbridge: dropping duplicate event_id")
		}
		return
	}

	promptCtx := &conversation.PromptSlackContext{
		EventID:         evt.EventID,
		ChannelID:       evt.ChannelID,
		Kind:            evt.Kind,
		AuthorID:        evt.AuthorID,
		Timestamp:       evt.Timestamp,
		ThreadTimestamp: evt.ThreadTimestamp,
		Text:            boundSlackText(evt.Text),
	}

	if err := b.runner.TriggerNowWithSlackEvent(b.cfg.TargetSessionID, true, TriggerSlack, promptCtx); err != nil {
		if b.logger != nil {
			b.logger.Warn("slackbridge: failed to trigger target loop", "error", err, "target_session_id", b.cfg.TargetSessionID)
		}
	}
}

func boundSlackText(text string) string {
	runes := []rune(text)
	if len(runes) <= maxSlackTextRunes {
		return text
	}
	return string(runes[:maxSlackTextRunes])
}
