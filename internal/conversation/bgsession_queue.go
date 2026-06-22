package conversation

// Queue processing cluster for BackgroundSession: thin delegators to the
// queueDispatcher collaborator, plus the queueDeps implementation that supplies
// it with the session's live dependencies.

import (
	"log/slog"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// --- Public delegators ---

// TryProcessQueuedMessage checks if the session is idle and enough time has passed since the last
// response, then processes the next queued message. This is used for startup initialization
// and periodic queue checking. Returns true if a message was sent.
func (bs *BackgroundSession) TryProcessQueuedMessage() bool {
	return bs.queueDisp.tryProcess(bs)
}

// NotifyQueueUpdated notifies all observers about a queue state change.
// This is called by the queue API handlers when the queue is modified externally.
func (bs *BackgroundSession) NotifyQueueUpdated(queueLength int, action string, messageID string) {
	bs.queueDisp.notifyUpdated(bs, queueLength, action, messageID)
}

// NotifyQueueReordered notifies all observers about a queue reorder.
// This is called by the queue API handlers when the queue order changes.
func (bs *BackgroundSession) NotifyQueueReordered(messages []session.QueuedMessage) {
	bs.queueDisp.notifyReordered(bs, messages)
}

// --- Unexported delegators (called from other files in this package) ---

// hasImmediateQueuedMessages returns true if there are queued messages that will be processed
// immediately (queue processing is enabled, queue is not empty, and no delay is configured).
func (bs *BackgroundSession) hasImmediateQueuedMessages() bool {
	return bs.queueDisp.hasImmediateQueued(bs)
}

// processNextQueuedMessage checks the queue and sends the next message if queue processing is enabled.
// Returns true if a queued message was popped and dispatched.
func (bs *BackgroundSession) processNextQueuedMessage() bool {
	return bs.queueDisp.processNext(bs)
}

// sendQueuedMessage sends a message that was popped from the queue.
func (bs *BackgroundSession) sendQueuedMessage(queue *session.Queue, msg session.QueuedMessage) {
	bs.queueDisp.send(bs, queue, msg)
}

// --- queueDeps implementation (supplies live session dependencies to queueDispatcher) ---

// queueProcessingEnabled reports whether queue processing is enabled.
func (bs *BackgroundSession) queueProcessingEnabled() bool {
	return bs.queueConfig == nil || bs.queueConfig.IsEnabled()
}

// queueDelaySeconds returns the configured delay in seconds (0 = no delay).
func (bs *BackgroundSession) queueDelaySeconds() int {
	if bs.queueConfig == nil {
		return 0
	}
	return bs.queueConfig.GetDelaySeconds()
}

// queueForSession returns the Queue for this session, or nil if unavailable.
func (bs *BackgroundSession) queueForSession() *session.Queue {
	if bs.store == nil || bs.persistedID == "" {
		return nil
	}
	return bs.store.Queue(bs.persistedID)
}

// queueIsPrompting reports whether a prompt is currently being processed.
func (bs *BackgroundSession) queueIsPrompting() bool {
	return bs.IsPrompting()
}

// queueIsClosed reports whether the session has been closed.
func (bs *BackgroundSession) queueIsClosed() bool {
	return bs.IsClosed()
}

// lastResponseCompleteTime returns when the agent last completed a response.
func (bs *BackgroundSession) lastResponseCompleteTime() time.Time {
	return bs.GetLastResponseCompleteTime()
}

// promptWithMeta sends a message with metadata through the normal prompt path.
func (bs *BackgroundSession) promptWithMeta(message string, meta PromptMeta) error {
	return bs.PromptWithMeta(message, meta)
}

// queueLogger returns the session-scoped logger.
func (bs *BackgroundSession) queueLogger() *slog.Logger { return bs.logger }

// queueSessionID returns the persisted session ID.
func (bs *BackgroundSession) queueSessionID() string { return bs.persistedID }
