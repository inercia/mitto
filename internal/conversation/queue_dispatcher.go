package conversation

// queueDispatcher owns the queue tick / dispatch logic for BackgroundSession. It is a
// stateless collaborator of BackgroundSession (held by composition, zero value is
// ready to use) and is unit-testable in isolation via the queueDeps seam.

import (
	"log/slog"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// queueDeps supplies the live, side-effecting primitives the queueDispatcher
// orchestrates. BackgroundSession satisfies it in production; tests use a fake.
type queueDeps interface {
	// queueProcessingEnabled reports whether queue processing is enabled.
	queueProcessingEnabled() bool
	// queueDelaySeconds returns the configured delay in seconds (0 = no delay).
	queueDelaySeconds() int
	// queueForSession returns the Queue for this session, or nil if unavailable.
	queueForSession() *session.Queue
	// setQueuedDeliveryInProgress sets or clears the delivery-in-progress flag.
	setQueuedDeliveryInProgress(bool)
	// notifyObservers broadcasts a callback to all registered session observers.
	notifyObservers(func(SessionObserver))
	// queueIsPrompting reports whether a prompt is currently being processed.
	queueIsPrompting() bool
	// queueIsClosed reports whether the session has been closed.
	queueIsClosed() bool
	// lastResponseCompleteTime returns when the agent last completed a response.
	lastResponseCompleteTime() time.Time
	// promptWithMeta sends a message with metadata through the normal prompt path.
	promptWithMeta(message string, meta PromptMeta) error
	// restoreBaselineIfOverride restores the baseline model if a per-prompt override is active.
	restoreBaselineIfOverride()
	// queueLogger returns the session-scoped logger (may be nil).
	queueLogger() *slog.Logger
	// queueSessionID returns the persisted session ID.
	queueSessionID() string
}

// queueDispatcher is stateless; all dependencies are passed per call.
type queueDispatcher struct{}

// hasImmediateQueued returns true if there are queued messages that will be processed
// immediately (queue processing is enabled, queue is not empty, and no delay is configured).
func (queueDispatcher) hasImmediateQueued(d queueDeps) bool {
	if !d.queueProcessingEnabled() {
		return false
	}
	if d.queueDelaySeconds() > 0 {
		return false
	}
	queue := d.queueForSession()
	if queue == nil {
		return false
	}
	queueLen, err := queue.Len()
	if err != nil {
		return false
	}
	return queueLen > 0
}

// processNext checks the queue and sends the next message if queue processing is enabled.
// Returns true if a queued message was popped and dispatched.
func (qd queueDispatcher) processNext(d queueDeps) bool {
	if !d.queueProcessingEnabled() {
		d.restoreBaselineIfOverride()
		return false
	}
	queue := d.queueForSession()
	if queue == nil {
		d.restoreBaselineIfOverride()
		return false
	}
	msg, err := queue.Pop()
	if err != nil {
		d.restoreBaselineIfOverride()
		return false
	}

	d.setQueuedDeliveryInProgress(true)
	defer d.setQueuedDeliveryInProgress(false)

	d.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	if delay := d.queueDelaySeconds(); delay > 0 {
		time.Sleep(time.Duration(delay) * time.Second)
	}

	qd.send(d, queue, msg)
	return true
}

// tryProcess checks if the session is idle and enough time has passed since the last
// response, then processes the next queued message. Returns true if a message was sent.
func (qd queueDispatcher) tryProcess(d queueDeps) bool {
	if !d.queueProcessingEnabled() {
		return false
	}
	if d.queueIsPrompting() {
		return false
	}
	if d.queueIsClosed() {
		return false
	}
	queue := d.queueForSession()
	if queue == nil {
		return false
	}
	queueLen, err := queue.Len()
	if err != nil || queueLen == 0 {
		return false
	}

	delaySeconds := d.queueDelaySeconds()
	if delaySeconds > 0 {
		lastResponse := d.lastResponseCompleteTime()
		if !lastResponse.IsZero() {
			elapsed := time.Since(lastResponse)
			if elapsed < time.Duration(delaySeconds)*time.Second {
				return false
			}
		}
	}

	msg, err := queue.Pop()
	if err != nil {
		return false
	}

	d.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	qd.send(d, queue, msg)
	return true
}

// send sends a message that was popped from the queue.
func (queueDispatcher) send(d queueDeps, queue *session.Queue, msg session.QueuedMessage) {
	if lg := d.queueLogger(); lg != nil {
		lg.Info("Sending queued message", "session_id", d.queueSessionID(), "message_id", msg.ID, "message", msg.Message)
	}
	queueLen, _ := queue.Len()
	d.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLen, "removed", msg.ID)
	})
	meta := PromptMeta{
		SenderID:   "queue",
		PromptID:   msg.ID,
		ImageIDs:   msg.ImageIDs,
		Arguments:  msg.Arguments,
		PromptName: msg.PromptName,
	}
	if err := d.promptWithMeta(msg.Message, meta); err != nil {
		if lg := d.queueLogger(); lg != nil {
			lg.Error("Failed to send queued message", "error", err, "message_id", msg.ID)
		}
		d.notifyObservers(func(o SessionObserver) {
			o.OnError("Failed to send queued message: " + err.Error())
		})
		return
	}
	d.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSent(msg.ID)
	})
}

// notifyUpdated notifies all observers about a queue state change.
func (queueDispatcher) notifyUpdated(d queueDeps, queueLength int, action string, messageID string) {
	d.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLength, action, messageID)
	})
}

// notifyReordered notifies all observers about a queue reorder.
func (queueDispatcher) notifyReordered(d queueDeps, messages []session.QueuedMessage) {
	d.notifyObservers(func(o SessionObserver) {
		o.OnQueueReordered(messages)
	})
}
