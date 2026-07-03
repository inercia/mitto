// Package session provides session persistence and management for Mitto.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

const (
	queueFileName = "queue.json"
)

var (
	// ErrQueueEmpty is returned when trying to pop from an empty queue.
	ErrQueueEmpty = errors.New("queue is empty")
	// ErrMessageNotFound is returned when a message ID is not found in the queue.
	ErrMessageNotFound = errors.New("message not found in queue")
	// ErrQueueFull is returned when trying to add to a queue that has reached its maximum size.
	ErrQueueFull = errors.New("queue is full")
)

// ParseScheduleTime parses a schedule time string that can be either:
//   - An absolute RFC 3339 / ISO 8601 timestamp (e.g., "2024-01-15T10:30:00Z")
//   - A relative duration from now using Go duration syntax (e.g., "2m", "1h", "30s", "1h30m")
//
// Returns the resolved absolute time, or an error if the string is not a valid format.
func ParseScheduleTime(s string) (time.Time, error) {
	// Try RFC 3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try Go duration format (e.g., "2m", "1h", "30s", "1h30m")
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("duration must be positive, got %s", s)
		}
		return time.Now().Add(d), nil
	}

	return time.Time{}, fmt.Errorf("invalid schedule_time %q: must be an RFC 3339 timestamp (e.g., 2024-01-15T10:30:00Z) or a relative duration (e.g., 5m, 1h, 2h30m)", s)
}

// ParseHistoryTime parses a time-filter string used for querying past history.
// It accepts the same two formats as ParseScheduleTime, but a relative duration
// is interpreted as "ago" (i.e., now minus the duration) rather than a future time:
//   - An absolute RFC 3339 / ISO 8601 timestamp (e.g., "2024-01-15T10:30:00Z")
//   - A relative duration using Go duration syntax meaning "ago" (e.g., "3m", "1h", "2h30m")
//
// Returns the resolved absolute time, or an error if the string is not a valid format.
func ParseHistoryTime(s string) (time.Time, error) {
	// Try RFC 3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try Go duration format, interpreted as "ago" (e.g., "3m", "1h", "2h30m")
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("duration must be positive, got %s", s)
		}
		return time.Now().Add(-d), nil
	}

	return time.Time{}, fmt.Errorf("invalid time %q: must be an RFC 3339 timestamp (e.g., 2024-01-15T10:30:00Z) or a relative duration ago (e.g., 3m, 1h, 2h30m)", s)
}

// QueuedMessage represents a message waiting to be sent to the agent.
type QueuedMessage struct {
	// ID is the unique identifier for this queued message (auto-assigned).
	ID string `json:"id"`
	// Message is the text content to send.
	Message string `json:"message"`
	// ImageIDs are optional attached image IDs.
	ImageIDs []string `json:"image_ids,omitempty"`
	// FileIDs are optional attached file IDs.
	FileIDs []string `json:"file_ids,omitempty"`
	// QueuedAt is when the message was added to the queue.
	QueuedAt time.Time `json:"queued_at"`
	// ClientID identifies the client that queued this message (for UI tracking).
	ClientID string `json:"client_id,omitempty"`
	// Title is an optional short title for the message (auto-generated asynchronously).
	Title string `json:"title,omitempty"`
	// ScheduledTime is when this message should be delivered. If nil, deliver immediately.
	ScheduledTime *time.Time `json:"scheduled_time,omitempty"`
	// Arguments are optional Go-template .Args values applied to
	// the message text when it is sent. Empty/nil means no rendering.
	Arguments map[string]string `json:"arguments,omitempty"`
	// PromptName is the name of the workspace prompt to send by name (resolved to
	// full text at dispatch). Empty for ad-hoc messages.
	PromptName string `json:"prompt_name,omitempty"`
}

// QueueFile represents the persisted queue state.
type QueueFile struct {
	// Messages is the ordered list of queued messages (FIFO).
	Messages []QueuedMessage `json:"messages"`
	// UpdatedAt is when the queue was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// Queue manages the message queue for a single session.
// It is safe for concurrent use.
type Queue struct {
	sessionDir string
	mu         sync.Mutex
}

// NewQueue creates a new Queue for the given session directory.
func NewQueue(sessionDir string) *Queue {
	return &Queue{
		sessionDir: sessionDir,
	}
}

// queuePath returns the path to the queue file.
func (q *Queue) queuePath() string {
	return filepath.Join(q.sessionDir, queueFileName)
}

// generateMessageID creates a unique message ID.
// Format: q-{unix_timestamp}-{random_hex_8chars}
func generateMessageID() string {
	timestamp := time.Now().Unix()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-only ID
		return fmt.Sprintf("q-%d", timestamp)
	}
	return fmt.Sprintf("q-%d-%s", timestamp, hex.EncodeToString(b))
}

// readQueue reads the queue file from disk.
// Returns an empty QueueFile if the file doesn't exist.
func (q *Queue) readQueue() (*QueueFile, error) {
	var qf QueueFile
	err := fileutil.ReadJSON(q.queuePath(), &qf)
	if err != nil {
		if os.IsNotExist(err) {
			return &QueueFile{Messages: []QueuedMessage{}}, nil
		}
		return nil, fmt.Errorf("failed to read queue file: %w", err)
	}
	if qf.Messages == nil {
		qf.Messages = []QueuedMessage{}
	}
	return &qf, nil
}

// writeQueue writes the queue file to disk atomically.
func (q *Queue) writeQueue(qf *QueueFile) error {
	qf.UpdatedAt = time.Now()
	if err := fileutil.WriteJSONAtomic(q.queuePath(), qf, 0644); err != nil {
		return fmt.Errorf("failed to write queue file: %w", err)
	}
	return nil
}

// Add adds a message to the queue and returns the assigned message.
// If maxSize > 0 and the queue already has maxSize messages, ErrQueueFull is returned.
// If maxSize <= 0, no size limit is enforced.
// If scheduledTime is non-nil, the message will only be delivered after that time.
// If arguments is non-empty, Go-template .Args rendering is applied to the
// message text when it is sent to the agent.
// If promptName is non-empty, the message is stored by name and resolved to full text
// at dispatch via PromptWithMeta (message should be empty in this case).
func (q *Queue) Add(message string, imageIDs, fileIDs []string, clientID string, scheduledTime *time.Time, maxSize int, arguments map[string]string, promptName string) (QueuedMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return QueuedMessage{}, err
	}

	// Check queue size limit
	if maxSize > 0 && len(qf.Messages) >= maxSize {
		return QueuedMessage{}, ErrQueueFull
	}

	msg := QueuedMessage{
		ID:            generateMessageID(),
		Message:       message,
		ImageIDs:      imageIDs,
		FileIDs:       fileIDs,
		QueuedAt:      time.Now(),
		ClientID:      clientID,
		ScheduledTime: scheduledTime,
		Arguments:     arguments,
		PromptName:    promptName,
	}

	qf.Messages = append(qf.Messages, msg)

	if err := q.writeQueue(qf); err != nil {
		return QueuedMessage{}, err
	}

	return msg, nil
}

// List returns all queued messages in FIFO order.
func (q *Queue) List() ([]QueuedMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return nil, err
	}

	// Return a copy to prevent external modification
	result := make([]QueuedMessage, len(qf.Messages))
	copy(result, qf.Messages)
	return result, nil
}

// Get returns a specific message by ID.
func (q *Queue) Get(id string) (QueuedMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return QueuedMessage{}, err
	}

	for _, msg := range qf.Messages {
		if msg.ID == id {
			return msg, nil
		}
	}

	return QueuedMessage{}, ErrMessageNotFound
}

// Remove removes a specific message by ID.
func (q *Queue) Remove(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return err
	}

	found := false
	newMessages := make([]QueuedMessage, 0, len(qf.Messages))
	for _, msg := range qf.Messages {
		if msg.ID == id {
			found = true
			continue
		}
		newMessages = append(newMessages, msg)
	}

	if !found {
		return ErrMessageNotFound
	}

	qf.Messages = newMessages
	return q.writeQueue(qf)
}

// UpdateTitle updates the title of a specific message by ID.
// Returns ErrMessageNotFound if the message doesn't exist.
func (q *Queue) UpdateTitle(id, title string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return err
	}

	found := false
	for i := range qf.Messages {
		if qf.Messages[i].ID == id {
			qf.Messages[i].Title = title
			found = true
			break
		}
	}

	if !found {
		return ErrMessageNotFound
	}

	return q.writeQueue(qf)
}

// Clear removes all queued messages.
func (q *Queue) Clear() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf := &QueueFile{Messages: []QueuedMessage{}}
	return q.writeQueue(qf)
}

// Pop removes and returns the next ready message from the queue.
// A message is "ready" if it has no ScheduledTime, or if its ScheduledTime <= now.
// Among ready messages, non-scheduled (immediate) messages are returned first (FIFO),
// then scheduled messages ordered by their ScheduledTime (earliest first).
// Returns ErrQueueEmpty if the queue is empty or no messages are ready.
func (q *Queue) Pop() (QueuedMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return QueuedMessage{}, err
	}

	if len(qf.Messages) == 0 {
		return QueuedMessage{}, ErrQueueEmpty
	}

	now := time.Now()
	readyIdx := -1

	// First pass: find the first non-scheduled (immediate) message
	for i, msg := range qf.Messages {
		if msg.ScheduledTime == nil {
			readyIdx = i
			break
		}
	}

	// Second pass: if no immediate message, find the earliest due scheduled message
	if readyIdx == -1 {
		var earliestTime time.Time
		for i, msg := range qf.Messages {
			if msg.ScheduledTime != nil && !msg.ScheduledTime.After(now) {
				if readyIdx == -1 || msg.ScheduledTime.Before(earliestTime) {
					readyIdx = i
					earliestTime = *msg.ScheduledTime
				}
			}
		}
	}

	if readyIdx == -1 {
		return QueuedMessage{}, ErrQueueEmpty
	}

	msg := qf.Messages[readyIdx]
	qf.Messages = append(qf.Messages[:readyIdx], qf.Messages[readyIdx+1:]...)

	if err := q.writeQueue(qf); err != nil {
		return QueuedMessage{}, err
	}

	return msg, nil
}

// Len returns the number of queued messages.
func (q *Queue) Len() (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return 0, err
	}

	return len(qf.Messages), nil
}

// IsEmpty returns true if the queue has no messages.
func (q *Queue) IsEmpty() (bool, error) {
	length, err := q.Len()
	if err != nil {
		return true, err
	}
	return length == 0, nil
}

// Move moves a message up or down in the queue.
// direction should be "up" (towards front, lower index) or "down" (towards back, higher index).
// Returns the new list of messages after the move.
func (q *Queue) Move(id, direction string) ([]QueuedMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return nil, err
	}

	// Find the message index
	idx := -1
	for i, msg := range qf.Messages {
		if msg.ID == id {
			idx = i
			break
		}
	}

	if idx == -1 {
		return nil, ErrMessageNotFound
	}

	// Calculate new index based on direction
	var newIdx int
	switch direction {
	case "up":
		if idx == 0 {
			// Already at the top, return current list
			result := make([]QueuedMessage, len(qf.Messages))
			copy(result, qf.Messages)
			return result, nil
		}
		newIdx = idx - 1
	case "down":
		if idx == len(qf.Messages)-1 {
			// Already at the bottom, return current list
			result := make([]QueuedMessage, len(qf.Messages))
			copy(result, qf.Messages)
			return result, nil
		}
		newIdx = idx + 1
	default:
		return nil, fmt.Errorf("invalid direction: %s (must be 'up' or 'down')", direction)
	}

	// Swap the messages
	qf.Messages[idx], qf.Messages[newIdx] = qf.Messages[newIdx], qf.Messages[idx]

	if err := q.writeQueue(qf); err != nil {
		return nil, err
	}

	// Return a copy of the updated list
	result := make([]QueuedMessage, len(qf.Messages))
	copy(result, qf.Messages)
	return result, nil
}

// HasScheduledMessages returns true if there are any scheduled messages in the queue
// (regardless of whether they are due or not).
func (q *Queue) HasScheduledMessages() (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return false, err
	}

	for _, msg := range qf.Messages {
		if msg.ScheduledTime != nil {
			return true, nil
		}
	}
	return false, nil
}

// NextScheduledTime returns the earliest scheduled time of any pending scheduled message.
// Returns nil if there are no scheduled messages.
func (q *Queue) NextScheduledTime() (*time.Time, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	qf, err := q.readQueue()
	if err != nil {
		return nil, err
	}

	var earliest *time.Time
	for _, msg := range qf.Messages {
		if msg.ScheduledTime != nil {
			if earliest == nil || msg.ScheduledTime.Before(*earliest) {
				t := *msg.ScheduledTime
				earliest = &t
			}
		}
	}
	return earliest, nil
}

// Delete removes the queue file from disk.
// This is typically called when deleting a session.
func (q *Queue) Delete() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	err := os.Remove(q.queuePath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete queue file: %w", err)
	}
	return nil
}
