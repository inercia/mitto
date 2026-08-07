package conversation

// queueDispatcher owns the queue tick / dispatch logic for BackgroundSession. It is a
// stateless collaborator of BackgroundSession (held by composition, zero value is
// ready to use) and is unit-testable in isolation via the queueDeps seam.

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// queueTransientRetryDelays is the backoff schedule send() uses to retry
// promptWithMeta failures that look like a transient template-compile-race
// (fragment registry not yet refreshed by the fs-watcher; see mitto-omu).
// Entry i is the sleep BEFORE attempt i+2, so total attempts is
// 1 + len(queueTransientRetryDelays). Package-var so tests can override to
// []time.Duration{0, 0, ...} for speed.
var queueTransientRetryDelays = []time.Duration{
	50 * time.Millisecond,
	200 * time.Millisecond,
	500 * time.Millisecond,
}

// queueTransientRetrySleep is the sleep function used between retry attempts.
// Package-var so tests can override to a no-op recorder without waiting.
var queueTransientRetrySleep = time.Sleep

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
	// queueRecordErrorEvent persists an error event so a failed queued send is
	// visible in the conversation history instead of leaving it frozen. No-op when no recorder.
	queueRecordErrorEvent(msg string)
	// setLastQueueSendError records the most recent queued-send failure for the parent's wait loop.
	setLastQueueSendError(msg string)
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
	// mitto-xlwh: unlike tryProcess, processNext is reached synchronously from
	// the prompt-completion tail (pdProcessNextQueuedMessage), which can run
	// after the session has been closed/deleted (WaitForResponseComplete only
	// waits on isPrompting, not on tail completion). Without this guard,
	// queue.Pop() below destroys the message from the durable queue before
	// the resulting "session is closed" promptWithMeta failure is observed,
	// silently losing the queued prompt.
	if d.queueIsClosed() {
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

// send sends a message that was popped from the queue. On a transient
// template-compile-race error (isTransientPromptCompileRace) it retries the
// promptWithMeta call with the queueTransientRetryDelays backoff schedule
// before falling back to the durable-error path (mitto-omu). Durable errors
// (unknown prompt, disabled prompt, other transport failures) skip retries
// and go straight to the error path so behaviour is unchanged for non-race
// cases. OnQueueUpdated(removed) fires exactly once before the retry loop, and
// exactly one of OnQueueMessageSent (success) or OnError (durable / exhausted)
// fires after the loop — no observer regression vs. the pre-retry behaviour.
func (queueDispatcher) send(d queueDeps, queue *session.Queue, msg session.QueuedMessage) {
	if lg := d.queueLogger(); lg != nil {
		lg.Info("Sending queued message", "session_id", d.queueSessionID(), "message_id", msg.ID, "message", msg.Message)
	}
	queueLen, _ := queue.Len()
	d.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLen, "removed", msg.ID)
	})
	meta := PromptMeta{
		SenderID:    "queue",
		PromptID:    msg.ID,
		ImageIDs:    msg.ImageIDs,
		Arguments:   msg.Arguments,
		PromptName:  msg.PromptName,
		QueueOrigin: msg.Origin,
	}

	// mitto-omu: bounded in-process retry for transient template-compile-race
	// failures. maxAttempts = 1 + len(queueTransientRetryDelays). Durable
	// errors short-circuit on the first attempt.
	maxAttempts := 1 + len(queueTransientRetryDelays)
	var err error
	var lastAttempt int
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastAttempt = attempt
		err = d.promptWithMeta(msg.Message, meta)
		if err == nil {
			d.notifyObservers(func(o SessionObserver) {
				o.OnQueueMessageSent(msg.ID)
			})
			return
		}
		if !isTransientPromptCompileRace(err) {
			break
		}
		if attempt >= maxAttempts {
			break
		}
		delay := queueTransientRetryDelays[attempt-1]
		if lg := d.queueLogger(); lg != nil {
			lg.Warn("Queued message hit transient prompt compile race; retrying",
				"error", err,
				"message_id", msg.ID,
				"attempt", attempt,
				"next_delay", delay)
		}
		queueTransientRetrySleep(delay)
	}

	// Durable failure or retries exhausted. Preserve the historical
	// "Failed to send queued message: <err>" prefix on OnError so no frontend
	// regression; on exhaustion inject a distinguishing suffix so ops can
	// separate the two classes from logs and event history.
	exhausted := isTransientPromptCompileRace(err) && lastAttempt >= maxAttempts
	var logMsg string
	if exhausted {
		logMsg = fmt.Sprintf("Failed to send queued message (retries_exhausted=true attempts=%d): %s",
			lastAttempt, err.Error())
	} else {
		logMsg = "Failed to send queued message: " + err.Error()
	}
	if lg := d.queueLogger(); lg != nil {
		if exhausted {
			lg.Error("Failed to send queued message after transient-race retries",
				"error", err,
				"message_id", msg.ID,
				"attempts", lastAttempt,
				"retries_exhausted", true)
		} else {
			lg.Error("Failed to send queued message", "error", err, "message_id", msg.ID)
		}
	}
	d.queueRecordErrorEvent(logMsg)
	d.setLastQueueSendError(err.Error())
	d.notifyObservers(func(o SessionObserver) {
		o.OnError(logMsg)
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
