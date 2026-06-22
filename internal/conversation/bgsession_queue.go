package conversation

// Queue processing cluster for BackgroundSession.

import (
	"time"

	"github.com/inercia/mitto/internal/session"
)

// hasImmediateQueuedMessages returns true if there are queued messages that will be processed
// immediately (queue processing is enabled, queue is not empty, and no delay is configured).
// This is used to skip follow-up suggestion analysis when the suggestions would be stale
// by the time they arrive (because the next message will be sent immediately).
func (bs *BackgroundSession) hasImmediateQueuedMessages() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return false
	}

	// Check if there's a delay configured - if so, suggestions might still be useful
	if bs.queueConfig != nil && bs.queueConfig.GetDelaySeconds() > 0 {
		return false
	}

	// Check if we have a store and queue
	if bs.store == nil || bs.persistedID == "" {
		return false
	}

	// Check if queue has messages
	queue := bs.store.Queue(bs.persistedID)
	queueLen, err := queue.Len()
	if err != nil {
		return false
	}

	return queueLen > 0
}

// processNextQueuedMessage checks the queue and sends the next message if queue processing is enabled.
// This is called after a prompt completes and applies the configured delay before sending.
// It returns true if a queued message was popped and dispatched (a new turn is starting,
// so the session is NOT idle), and false if the queue was empty/disabled (the session is idle).
func (bs *BackgroundSession) processNextQueuedMessage() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		bs.restoreBaselineIfOverride()
		return false
	}

	// Get the queue for this session
	if bs.store == nil {
		bs.restoreBaselineIfOverride()
		return false
	}
	queue := bs.store.Queue(bs.persistedID)

	// Pop the next message from the queue
	msg, err := queue.Pop()
	if err != nil {
		// Queue is empty: restore the baseline model if a per-prompt override is active.
		bs.restoreBaselineIfOverride()
		return false
	}

	// Signal delivery in progress so idle-detection polls (e.g. mitto_children_tasks_wait)
	// don't prematurely classify this session as agent_idle while we sleep through the delay.
	bs.setQueuedDeliveryInProgress(true)
	defer bs.setQueuedDeliveryInProgress(false)

	// Notify observers that we're sending a queued message
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	// Apply delay if configured
	if bs.queueConfig != nil && bs.queueConfig.GetDelaySeconds() > 0 {
		time.Sleep(time.Duration(bs.queueConfig.GetDelaySeconds()) * time.Second)
	}

	bs.sendQueuedMessage(queue, msg)
	return true
}

// TryProcessQueuedMessage checks if the session is idle and enough time has passed since the last
// response, then processes the next queued message. This is used for startup initialization
// and periodic queue checking. Returns true if a message was sent.
func (bs *BackgroundSession) TryProcessQueuedMessage() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return false
	}

	// Check if session is currently prompting
	if bs.IsPrompting() {
		return false
	}

	// Check if session is closed
	if bs.IsClosed() {
		return false
	}

	// Get the queue for this session
	if bs.store == nil {
		return false
	}
	queue := bs.store.Queue(bs.persistedID)

	// Check if queue has messages
	queueLen, err := queue.Len()
	if err != nil || queueLen == 0 {
		return false
	}

	// Check if delay has elapsed since last response
	delaySeconds := 0
	if bs.queueConfig != nil {
		delaySeconds = bs.queueConfig.GetDelaySeconds()
	}

	if delaySeconds > 0 {
		lastResponse := bs.GetLastResponseCompleteTime()
		// If lastResponse is zero, we can proceed (no previous response means agent is idle)
		if !lastResponse.IsZero() {
			elapsed := time.Since(lastResponse)
			if elapsed < time.Duration(delaySeconds)*time.Second {
				// Not enough time has passed
				return false
			}
		}
	}

	// Pop and send the next message
	msg, err := queue.Pop()
	if err != nil {
		// Queue is empty or error
		return false
	}

	// Notify observers that we're sending a queued message
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	bs.sendQueuedMessage(queue, msg)
	return true
}

// sendQueuedMessage sends a message that was popped from the queue.
func (bs *BackgroundSession) sendQueuedMessage(queue *session.Queue, msg session.QueuedMessage) {
	if bs.logger != nil {
		bs.logger.Info("Sending queued message", "session_id", bs.persistedID, "message_id", msg.ID, "message", msg.Message)
	}
	// Get updated queue length for notification
	queueLen, _ := queue.Len()

	// Notify observers about queue update (message removed)
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLen, "removed", msg.ID)
	})

	// Send the queued message
	meta := PromptMeta{
		SenderID:   "queue",
		PromptID:   msg.ID,
		ImageIDs:   msg.ImageIDs,
		Arguments:  msg.Arguments,
		PromptName: msg.PromptName,
	}
	if err := bs.PromptWithMeta(msg.Message, meta); err != nil {
		if bs.logger != nil {
			bs.logger.Error("Failed to send queued message", "error", err, "message_id", msg.ID)
		}
		bs.notifyObservers(func(o SessionObserver) {
			o.OnError("Failed to send queued message: " + err.Error())
		})
		return
	}

	// Notify observers that the message was sent
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSent(msg.ID)
	})
}

// NotifyQueueUpdated notifies all observers about a queue state change.
// This is called by the queue API handlers when the queue is modified externally.
func (bs *BackgroundSession) NotifyQueueUpdated(queueLength int, action string, messageID string) {
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLength, action, messageID)
	})
}

// NotifyQueueReordered notifies all observers about a queue reorder.
// This is called by the queue API handlers when the queue order changes.
func (bs *BackgroundSession) NotifyQueueReordered(messages []session.QueuedMessage) {
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueReordered(messages)
	})
}
