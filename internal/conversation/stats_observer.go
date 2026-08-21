package conversation

// Live stats path (mitto-a86b.4): a stateless SessionObserver that forwards
// observed session events into a stats.Aggregator. The aggregator's Ingest is
// non-blocking (internal channel + drop-on-full), so the observer methods stay
// off the notify-critical path and never lock, never log per-event, and never
// return errors — matching the SessionObserver contract that all methods must
// be safe to call from BackgroundSession.notifyObservers under the observers
// RLock.
//
// The backfill path (stats.5) covers persisted permission and error events;
// this live path only wires the four callbacks the v1 aggregator classifies:
// user_prompt, agent_message, agent_thought, and tool_call.

import (
	"time"

	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/stats"
)

// statsObserver forwards observed session events to a stats.Aggregator using
// the session-scoped labels captured at construction time. It holds only the
// aggregator reference and the immutable SessionContext, so every instance is
// safe to attach to a BackgroundSession's observer set for the lifetime of the
// session.
type statsObserver struct {
	agg stats.Aggregator
	sc  stats.SessionContext
}

// newStatsObserver constructs a live-path observer for the given session.
// Returns nil if agg is nil so callers can no-op the wiring without extra
// branches. sc.SessionID must be non-empty; aggregator.foldOwned drops items
// with empty session ids, so the observer emits them anyway (the aggregator
// keeps that guarantee, not the observer).
func newStatsObserver(agg stats.Aggregator, sc stats.SessionContext) *statsObserver {
	if agg == nil {
		return nil
	}
	return &statsObserver{agg: agg, sc: sc}
}

// ingest is the single fan-in point for every callback. Building a
// session.Event here (rather than reusing the persisted one) keeps the
// observer independent of the recorder's event mint order.
func (o *statsObserver) ingest(seq int64, evType session.EventType, data any) {
	if o == nil || o.agg == nil {
		return
	}
	o.agg.Ingest(o.sc, session.Event{
		Seq:       seq,
		Type:      evType,
		Timestamp: time.Now().UTC(),
		Data:      data,
	})
}

// --- SessionObserver methods that map onto v1 metrics ---

func (o *statsObserver) OnAgentMessage(seq int64, html, markdown string) {
	// Token accounting stays keyed on html only (unchanged) to avoid double-counting;
	// markdown is carried through for parity with the persisted event shape.
	o.ingest(seq, session.EventTypeAgentMessage, session.AgentMessageData{Text: html, Markdown: markdown})
}

func (o *statsObserver) OnAgentThought(seq int64, text string) {
	o.ingest(seq, session.EventTypeAgentThought, session.AgentThoughtData{Text: text})
}

func (o *statsObserver) OnToolCall(seq int64, id, title, status string) {
	o.ingest(seq, session.EventTypeToolCall, session.ToolCallData{
		ToolCallID: id,
		Title:      title,
		Status:     status,
	})
}

func (o *statsObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string, promptName string, argumentCount int, arguments map[string]string, provenance *session.PromptProvenance) {
	// Aggregator token estimator counts Images by len() and Files by name
	// length; we don't have full FileRefs at the callback layer, so pass
	// name-only refs sized from the id list. This mirrors what the recorder
	// eventually persists closely enough for v1 length-based estimates.
	var files []session.FileRef
	if len(fileIDs) > 0 {
		files = make([]session.FileRef, 0, len(fileIDs))
		for _, id := range fileIDs {
			files = append(files, session.FileRef{Name: id})
		}
	}
	var images []session.ImageRef
	if len(imageIDs) > 0 {
		images = make([]session.ImageRef, 0, len(imageIDs))
		for _, id := range imageIDs {
			images = append(images, session.ImageRef{Name: id})
		}
	}
	o.ingest(seq, session.EventTypeUserPrompt, session.UserPromptData{
		Message:       message,
		Images:        images,
		Files:         files,
		PromptID:      promptID,
		PromptName:    promptName,
		ArgumentCount: argumentCount,
		Arguments:     arguments,
	})
}

// --- SessionObserver methods that are not consumed by the v1 aggregator ---
//
// These callbacks are required to satisfy the interface but do not correspond
// to any live-path metric today. They intentionally do no work; persisted
// permission_prompted and error events are picked up by the stats.5 backfill
// path from events.jsonl.

// OnSessionChange forwards live session-change timeline events (currently
// used for model switches) into the aggregator so its per-session accumulator
// can retag subsequent token deltas to the new model. Mirrors what the
// backfill path sees from events.jsonl.
func (o *statsObserver) OnSessionChange(seq int64, data session.SessionChangeData) {
	o.ingest(seq, session.EventTypeSessionChange, data)
}

func (o *statsObserver) OnToolUpdate(seq int64, id string, status *string)               {}
func (o *statsObserver) OnPlan(seq int64, entries []PlanEntry)                           {}
func (o *statsObserver) OnFileWrite(seq int64, path string, size int)                    {}
func (o *statsObserver) OnFileRead(seq int64, path string, size int)                     {}
func (o *statsObserver) OnPromptComplete(eventCount int)                                 {}
func (o *statsObserver) OnActionButtons(buttons []ActionButton)                          {}
func (o *statsObserver) OnError(message string)                                          {}
func (o *statsObserver) OnQueueUpdated(queueLength int, action string, messageID string) {}
func (o *statsObserver) OnQueueReordered(messages []session.QueuedMessage)               {}
func (o *statsObserver) OnQueueMessageSending(messageID string)                          {}
func (o *statsObserver) OnQueueMessageSent(messageID string)                             {}
func (o *statsObserver) OnAvailableCommandsUpdated(commands []AvailableCommand)          {}
func (o *statsObserver) OnACPStopped(reason string)                                      {}
func (o *statsObserver) OnACPStarted()                                                   {}
func (o *statsObserver) OnUIPrompt(req UIPromptRequest)                                  {}
func (o *statsObserver) OnUIPromptDismiss(requestID string, reason string)               {}
func (o *statsObserver) OnNotification(req UINotifyRequest)                              {}
func (o *statsObserver) OnContextUsageUpdate(size, used int)                             {}

// Compile-time assertion that statsObserver satisfies the SessionObserver
// contract. Any drift in the interface will fail the build here rather than at
// registration time.
var _ SessionObserver = (*statsObserver)(nil)

// Compile-time assertion that statsObserver also satisfies the optional
// SessionChangeObserver sibling. cmRecordSessionChangeWithSeq type-asserts to
// this interface at notify time, so drift here would silently skip live model
// updates.
var _ SessionChangeObserver = (*statsObserver)(nil)
