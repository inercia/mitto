package session

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

func TestQueue_AddAndList(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// Initially empty
	messages, err := q.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("List() = %d messages, want 0", len(messages))
	}

	// Add first message (0 = no limit)
	msg1, err := q.Add("Hello", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if msg1.ID == "" {
		t.Error("Add() returned empty ID")
	}
	if msg1.Message != "Hello" {
		t.Errorf("Add() message = %q, want %q", msg1.Message, "Hello")
	}
	if msg1.ClientID != "client1" {
		t.Errorf("Add() clientID = %q, want %q", msg1.ClientID, "client1")
	}

	// Add second message with images
	msg2, err := q.Add("World", []string{"img1", "img2"}, nil, "client2", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if len(msg2.ImageIDs) != 2 {
		t.Errorf("Add() imageIDs = %v, want 2 items", msg2.ImageIDs)
	}

	// List should return both in FIFO order
	messages, err = q.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("List() = %d messages, want 2", len(messages))
	}
	if messages[0].ID != msg1.ID {
		t.Errorf("List()[0].ID = %q, want %q", messages[0].ID, msg1.ID)
	}
	if messages[1].ID != msg2.ID {
		t.Errorf("List()[1].ID = %q, want %q", messages[1].ID, msg2.ID)
	}
}

func TestQueue_AddWithOrigin(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// AddWithOrigin with QueueOriginAgent persists Origin == QueueOriginAgent.
	agentMsg, err := q.AddWithOrigin("agent message", nil, nil, "client1", nil, 0, nil, "", QueueOriginAgent)
	if err != nil {
		t.Fatalf("AddWithOrigin() error = %v", err)
	}
	if agentMsg.Origin != QueueOriginAgent {
		t.Errorf("AddWithOrigin() origin = %q, want %q", agentMsg.Origin, QueueOriginAgent)
	}

	// Plain Add() defaults to QueueOriginUser.
	userMsg, err := q.Add("user message", nil, nil, "client2", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if userMsg.Origin != QueueOriginUser {
		t.Errorf("Add() origin = %q, want %q", userMsg.Origin, QueueOriginUser)
	}
}

func TestQueue_Get(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg, err := q.Add("Test message", nil, nil, "", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	// Get existing message
	got, err := q.Get(msg.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("Get() ID = %q, want %q", got.ID, msg.ID)
	}
	if got.Message != msg.Message {
		t.Errorf("Get() Message = %q, want %q", got.Message, msg.Message)
	}

	// Get non-existent message
	_, err = q.Get("nonexistent")
	if err != ErrMessageNotFound {
		t.Errorf("Get(nonexistent) error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueue_Remove(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg1, _ := q.Add("First", nil, nil, "", nil, 0, nil, "")
	msg2, _ := q.Add("Second", nil, nil, "", nil, 0, nil, "")
	msg3, _ := q.Add("Third", nil, nil, "", nil, 0, nil, "")

	// Remove middle message
	if err := q.Remove(msg2.ID); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	// Verify it's gone
	messages, _ := q.List()
	if len(messages) != 2 {
		t.Fatalf("List() = %d messages, want 2", len(messages))
	}
	if messages[0].ID != msg1.ID || messages[1].ID != msg3.ID {
		t.Error("Remove() did not remove the correct message")
	}

	// Remove non-existent message
	err := q.Remove("nonexistent")
	if err != ErrMessageNotFound {
		t.Errorf("Remove(nonexistent) error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueue_UpdateTitle(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg1, _ := q.Add("First message", nil, nil, "", nil, 0, nil, "")
	msg2, _ := q.Add("Second message", nil, nil, "", nil, 0, nil, "")

	// Update title of first message
	if err := q.UpdateTitle(msg1.ID, "First Title"); err != nil {
		t.Fatalf("UpdateTitle() error = %v", err)
	}

	// Verify title was updated
	updated, err := q.Get(msg1.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if updated.Title != "First Title" {
		t.Errorf("UpdateTitle() title = %q, want %q", updated.Title, "First Title")
	}

	// Verify second message title is still empty
	msg2Updated, _ := q.Get(msg2.ID)
	if msg2Updated.Title != "" {
		t.Errorf("Second message title = %q, want empty", msg2Updated.Title)
	}

	// Update non-existent message
	err = q.UpdateTitle("nonexistent", "Some Title")
	if err != ErrMessageNotFound {
		t.Errorf("UpdateTitle(nonexistent) error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueue_Clear(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	q.Add("First", nil, nil, "", nil, 0, nil, "")
	q.Add("Second", nil, nil, "", nil, 0, nil, "")
	q.Add("Third", nil, nil, "", nil, 0, nil, "")

	if err := q.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	messages, _ := q.List()
	if len(messages) != 0 {
		t.Errorf("List() after Clear() = %d messages, want 0", len(messages))
	}
}

func TestQueue_Pop(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// Pop from empty queue
	_, err := q.Pop()
	if err != ErrQueueEmpty {
		t.Errorf("Pop() on empty queue error = %v, want ErrQueueEmpty", err)
	}

	msg1, _ := q.Add("First", nil, nil, "", nil, 0, nil, "")
	msg2, _ := q.Add("Second", nil, nil, "", nil, 0, nil, "")

	// Pop first message
	popped, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop() error = %v", err)
	}
	if popped.ID != msg1.ID {
		t.Errorf("Pop() ID = %q, want %q", popped.ID, msg1.ID)
	}

	// Pop second message
	popped, err = q.Pop()
	if err != nil {
		t.Fatalf("Pop() error = %v", err)
	}
	if popped.ID != msg2.ID {
		t.Errorf("Pop() ID = %q, want %q", popped.ID, msg2.ID)
	}

	// Queue should be empty now
	_, err = q.Pop()
	if err != ErrQueueEmpty {
		t.Errorf("Pop() on empty queue error = %v, want ErrQueueEmpty", err)
	}
}

func TestQueue_Len(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	length, err := q.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	if length != 0 {
		t.Errorf("Len() = %d, want 0", length)
	}

	q.Add("First", nil, nil, "", nil, 0, nil, "")
	q.Add("Second", nil, nil, "", nil, 0, nil, "")

	length, err = q.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	if length != 2 {
		t.Errorf("Len() = %d, want 2", length)
	}
}

func TestQueue_IsEmpty(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	empty, err := q.IsEmpty()
	if err != nil {
		t.Fatalf("IsEmpty() error = %v", err)
	}
	if !empty {
		t.Error("IsEmpty() = false, want true")
	}

	q.Add("Test", nil, nil, "", nil, 0, nil, "")

	empty, err = q.IsEmpty()
	if err != nil {
		t.Fatalf("IsEmpty() error = %v", err)
	}
	if empty {
		t.Error("IsEmpty() = true, want false")
	}
}

func TestQueue_Delete(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	q.Add("Test", nil, nil, "", nil, 0, nil, "")

	// Verify queue file exists
	queuePath := filepath.Join(dir, queueFileName)
	if _, err := os.Stat(queuePath); os.IsNotExist(err) {
		t.Fatal("Queue file should exist after Add()")
	}

	// Delete the queue
	if err := q.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify queue file is gone
	if _, err := os.Stat(queuePath); !os.IsNotExist(err) {
		t.Error("Queue file should not exist after Delete()")
	}

	// Delete again should not error
	if err := q.Delete(); err != nil {
		t.Errorf("Delete() on non-existent queue error = %v", err)
	}
}

func TestQueue_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	const numGoroutines = 10
	const messagesPerGoroutine = 5

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Concurrent adds (0 = no limit)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				_, err := q.Add("message", nil, nil, "", nil, 0, nil, "")
				if err != nil {
					t.Errorf("Concurrent Add() error = %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify all messages were added
	length, err := q.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	expected := numGoroutines * messagesPerGoroutine
	if length != expected {
		t.Errorf("Len() = %d, want %d", length, expected)
	}
}

// TestQueue_MultiInstanceConcurrentPop_mitto_pr0 reproduces mitto-pr0: "Queued
// message dispatched twice on cold resume: MCP auto-resume drain races
// SessionManager drain ('prompt already in progress')".
//
// Root cause: Store.Queue(sessionID) (internal/session/store_dispensers.go)
// vends a FRESH *Queue — with a FRESH sync.Mutex — on every call. Pop()'s
// mutex only serializes callers that share the SAME *Queue instance; it does
// nothing to serialize two independent instances pointed at the same session
// directory. In production, SessionManager's post-resume sweep
// (session_manager.go:2714 go bs.TryProcessQueuedMessage()) and the MCP
// auto-resume path (tools_prompt_dispatch.go:274 go bs.TryProcessQueuedMessage())
// each call queueForSession(), which allocates a brand-new session.Queue per
// call, and both race Pop() against the same on-disk queue.json. Because
// Pop() is a plain readQueue-then-writeQueue (atomic rename) with no
// cross-instance coordination, both instances can read the file before
// either's write lands, so both return the SAME message — one dispatch wins,
// the second later fails downstream with "prompt already in progress".
//
// This test drives many goroutines, each minting its OWN NewQueue(dir) over
// the same directory (mirroring Store.Queue's per-call allocation), racing
// Pop() against a single queued message. Correct behavior requires exactly
// one non-ErrQueueEmpty result across all goroutines.
func TestQueue_MultiInstanceConcurrentPop_mitto_pr0(t *testing.T) {
	dir := t.TempDir()

	seed := NewQueue(dir)
	msg, err := seed.Add("only message", nil, nil, "", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	const attempts = 200
	var wg sync.WaitGroup
	wg.Add(attempts)

	start := make(chan struct{})
	results := make([]QueuedMessage, attempts)
	errs := make([]error, attempts)

	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			// Each goroutine obtains its OWN Queue instance over the SAME
			// session directory — exactly what Store.Queue(sessionID) does
			// on every call (no per-sessionID caching/singleton).
			q := NewQueue(dir)
			<-start
			results[i], errs[i] = q.Pop()
		}(i)
	}

	close(start)
	wg.Wait()

	successCount := 0
	for i := 0; i < attempts; i++ {
		switch errs[i] {
		case nil:
			successCount++
			if results[i].ID != msg.ID {
				t.Errorf("Pop() returned unexpected message ID = %q, want %q", results[i].ID, msg.ID)
			}
		case ErrQueueEmpty:
			// Expected for the goroutines that lose the race.
		default:
			t.Errorf("Pop() unexpected error = %v", errs[i])
		}
	}

	if successCount != 1 {
		t.Errorf("mitto-pr0: %d goroutines popped the single queued message (message_id=%s), want exactly 1 — "+
			"Store.Queue() vends a fresh *Queue (fresh mutex) per call, so two instances over the same "+
			"session directory provide no mutual exclusion and both can Pop() the same entry, causing a "+
			"duplicate dispatch (\"Sending queued message\" logged twice / \"prompt already in progress\")",
			successCount, msg.ID)
	}
}

func TestQueue_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Create queue and add messages
	q1 := NewQueue(dir)
	msg1, _ := q1.Add("First", nil, nil, "client1", nil, 0, nil, "")
	msg2, _ := q1.Add("Second", []string{"img1"}, nil, "client2", nil, 0, nil, "")

	// Create new queue instance pointing to same directory
	q2 := NewQueue(dir)

	// Should see the same messages
	messages, err := q2.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("List() = %d messages, want 2", len(messages))
	}
	if messages[0].ID != msg1.ID {
		t.Errorf("Persisted message[0].ID = %q, want %q", messages[0].ID, msg1.ID)
	}
	if messages[1].ID != msg2.ID {
		t.Errorf("Persisted message[1].ID = %q, want %q", messages[1].ID, msg2.ID)
	}
	if messages[1].ImageIDs[0] != "img1" {
		t.Errorf("Persisted message[1].ImageIDs = %v, want [img1]", messages[1].ImageIDs)
	}
}

func TestQueue_MaxSize(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	const maxSize = 3

	// Add messages up to the limit
	for i := 0; i < maxSize; i++ {
		_, err := q.Add("message", nil, nil, "", nil, maxSize, nil, "")
		if err != nil {
			t.Fatalf("Add() error = %v (message %d)", err, i+1)
		}
	}

	// Verify queue is at capacity
	length, _ := q.Len()
	if length != maxSize {
		t.Errorf("Len() = %d, want %d", length, maxSize)
	}

	// Try to add one more - should fail with ErrQueueFull
	_, err := q.Add("overflow", nil, nil, "", nil, maxSize, nil, "")
	if err != ErrQueueFull {
		t.Errorf("Add() when full error = %v, want ErrQueueFull", err)
	}

	// Queue length should still be at max
	length, _ = q.Len()
	if length != maxSize {
		t.Errorf("Len() after failed add = %d, want %d", length, maxSize)
	}

	// Remove one message
	q.Pop()

	// Now we should be able to add again
	_, err = q.Add("new message", nil, nil, "", nil, maxSize, nil, "")
	if err != nil {
		t.Errorf("Add() after Pop() error = %v", err)
	}

	// Queue should be at capacity again
	length, _ = q.Len()
	if length != maxSize {
		t.Errorf("Len() after re-add = %d, want %d", length, maxSize)
	}
}

func TestQueue_MaxSize_Zero_NoLimit(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// With maxSize=0, there should be no limit
	for i := 0; i < 100; i++ {
		_, err := q.Add("message", nil, nil, "", nil, 0, nil, "")
		if err != nil {
			t.Fatalf("Add() with no limit error = %v (message %d)", err, i+1)
		}
	}

	length, _ := q.Len()
	if length != 100 {
		t.Errorf("Len() = %d, want 100", length)
	}
}

func TestQueue_MaxSize_Negative_NoLimit(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	// With maxSize<0, there should be no limit
	for i := 0; i < 10; i++ {
		_, err := q.Add("message", nil, nil, "", nil, -1, nil, "")
		if err != nil {
			t.Fatalf("Add() with negative limit error = %v (message %d)", err, i+1)
		}
	}

	length, _ := q.Len()
	if length != 10 {
		t.Errorf("Len() = %d, want 10", length)
	}
}

func TestQueue_Move(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg1, _ := q.Add("First", nil, nil, "", nil, 0, nil, "")
	msg2, _ := q.Add("Second", nil, nil, "", nil, 0, nil, "")
	msg3, _ := q.Add("Third", nil, nil, "", nil, 0, nil, "")

	// Move second message up (should swap with first)
	messages, err := q.Move(msg2.ID, "up")
	if err != nil {
		t.Fatalf("Move(up) error = %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("Move() returned %d messages, want 3", len(messages))
	}
	if messages[0].ID != msg2.ID {
		t.Errorf("After Move(up), messages[0].ID = %q, want %q", messages[0].ID, msg2.ID)
	}
	if messages[1].ID != msg1.ID {
		t.Errorf("After Move(up), messages[1].ID = %q, want %q", messages[1].ID, msg1.ID)
	}
	if messages[2].ID != msg3.ID {
		t.Errorf("After Move(up), messages[2].ID = %q, want %q", messages[2].ID, msg3.ID)
	}

	// Move first message down (should swap with second)
	messages, err = q.Move(msg2.ID, "down")
	if err != nil {
		t.Fatalf("Move(down) error = %v", err)
	}
	if messages[0].ID != msg1.ID {
		t.Errorf("After Move(down), messages[0].ID = %q, want %q", messages[0].ID, msg1.ID)
	}
	if messages[1].ID != msg2.ID {
		t.Errorf("After Move(down), messages[1].ID = %q, want %q", messages[1].ID, msg2.ID)
	}
}

func TestQueue_Move_AtBoundary(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg1, _ := q.Add("First", nil, nil, "", nil, 0, nil, "")
	_, _ = q.Add("Second", nil, nil, "", nil, 0, nil, "") // msg2 not used in this test
	msg3, _ := q.Add("Third", nil, nil, "", nil, 0, nil, "")

	// Move first message up (already at top, should be no-op)
	messages, err := q.Move(msg1.ID, "up")
	if err != nil {
		t.Fatalf("Move(up) at top error = %v", err)
	}
	if messages[0].ID != msg1.ID {
		t.Errorf("Move(up) at top changed order unexpectedly")
	}

	// Move last message down (already at bottom, should be no-op)
	messages, err = q.Move(msg3.ID, "down")
	if err != nil {
		t.Fatalf("Move(down) at bottom error = %v", err)
	}
	if messages[2].ID != msg3.ID {
		t.Errorf("Move(down) at bottom changed order unexpectedly")
	}
}

func TestQueue_Move_NotFound(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	q.Add("First", nil, nil, "", nil, 0, nil, "")

	// Move non-existent message
	_, err := q.Move("nonexistent", "up")
	if err != ErrMessageNotFound {
		t.Errorf("Move(nonexistent) error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueue_Move_InvalidDirection(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)

	msg, _ := q.Add("First", nil, nil, "", nil, 0, nil, "")

	// Move with invalid direction
	_, err := q.Move(msg.ID, "invalid")
	if err == nil {
		t.Error("Move(invalid direction) should return error")
	}
}

func TestQueue_Move_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Create queue and add messages
	q1 := NewQueue(dir)
	msg1, _ := q1.Add("First", nil, nil, "", nil, 0, nil, "")
	msg2, _ := q1.Add("Second", nil, nil, "", nil, 0, nil, "")

	// Move second message up
	_, err := q1.Move(msg2.ID, "up")
	if err != nil {
		t.Fatalf("Move() error = %v", err)
	}

	// Create new queue instance pointing to same directory
	q2 := NewQueue(dir)

	// Should see the reordered messages
	messages, err := q2.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("List() = %d messages, want 2", len(messages))
	}
	if messages[0].ID != msg2.ID {
		t.Errorf("Persisted message[0].ID = %q, want %q (msg2)", messages[0].ID, msg2.ID)
	}
	if messages[1].ID != msg1.ID {
		t.Errorf("Persisted message[1].ID = %q, want %q (msg1)", messages[1].ID, msg1.ID)
	}
}

func TestParseScheduleTime(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		errSubstr string
		// For absolute times, check exact match; for durations, check approximate offset from now.
		checkExact    bool
		exactTimeStr  string  // RFC 3339 string for exact match
		approxOffset  float64 // expected offset in seconds from now (for duration inputs)
		approxEpsilon float64 // allowed deviation in seconds
	}{
		// --- Valid RFC 3339 timestamps ---
		{
			name:         "RFC 3339 UTC",
			input:        "2024-01-15T10:30:00Z",
			checkExact:   true,
			exactTimeStr: "2024-01-15T10:30:00Z",
		},
		{
			name:         "RFC 3339 with timezone offset",
			input:        "2024-06-01T14:00:00+02:00",
			checkExact:   true,
			exactTimeStr: "2024-06-01T14:00:00+02:00",
		},
		{
			name:         "RFC 3339 with fractional seconds",
			input:        "2024-12-31T23:59:59.999Z",
			checkExact:   true,
			exactTimeStr: "2024-12-31T23:59:59.999Z",
		},

		// --- Valid relative durations ---
		{
			name:          "duration seconds",
			input:         "30s",
			approxOffset:  30,
			approxEpsilon: 2,
		},
		{
			name:          "duration minutes",
			input:         "5m",
			approxOffset:  300,
			approxEpsilon: 2,
		},
		{
			name:          "duration hours",
			input:         "2h",
			approxOffset:  7200,
			approxEpsilon: 2,
		},
		{
			name:          "duration combined",
			input:         "1h30m",
			approxOffset:  5400,
			approxEpsilon: 2,
		},
		{
			name:          "duration zero",
			input:         "0s",
			approxOffset:  0,
			approxEpsilon: 2,
		},
		{
			name:          "duration milliseconds",
			input:         "500ms",
			approxOffset:  0.5,
			approxEpsilon: 2,
		},

		// --- Invalid inputs ---
		{
			name:      "negative duration",
			input:     "-5m",
			wantErr:   true,
			errSubstr: "duration must be positive",
		},
		{
			name:      "negative duration hours",
			input:     "-1h",
			wantErr:   true,
			errSubstr: "duration must be positive",
		},
		{
			name:      "empty string",
			input:     "",
			wantErr:   true,
			errSubstr: "invalid schedule_time",
		},
		{
			name:      "random text",
			input:     "tomorrow",
			wantErr:   true,
			errSubstr: "invalid schedule_time",
		},
		{
			name:      "date only no time",
			input:     "2024-01-15",
			wantErr:   true,
			errSubstr: "invalid schedule_time",
		},
		{
			name:      "unix timestamp",
			input:     "1705312200",
			wantErr:   true,
			errSubstr: "invalid schedule_time",
		},
		{
			name:      "number without unit",
			input:     "300",
			wantErr:   true,
			errSubstr: "invalid schedule_time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			got, err := ParseScheduleTime(tt.input)
			after := time.Now()

			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseScheduleTime(%q) = %v, want error containing %q", tt.input, got, tt.errSubstr)
				}
				if tt.errSubstr != "" && !contains(err.Error(), tt.errSubstr) {
					t.Errorf("ParseScheduleTime(%q) error = %q, want substring %q", tt.input, err.Error(), tt.errSubstr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseScheduleTime(%q) unexpected error: %v", tt.input, err)
			}

			if tt.checkExact {
				expected, _ := time.Parse(time.RFC3339Nano, tt.exactTimeStr)
				if !got.Equal(expected) {
					t.Errorf("ParseScheduleTime(%q) = %v, want %v", tt.input, got, expected)
				}
			} else {
				// Check that result is approximately now + approxOffset
				expectedLow := before.Add(time.Duration(tt.approxOffset * float64(time.Second)))
				expectedHigh := after.Add(time.Duration(tt.approxOffset * float64(time.Second)))
				epsilon := time.Duration(tt.approxEpsilon * float64(time.Second))

				if got.Before(expectedLow.Add(-epsilon)) || got.After(expectedHigh.Add(epsilon)) {
					t.Errorf("ParseScheduleTime(%q) = %v, want approximately now+%vs (between %v and %v)",
						tt.input, got, tt.approxOffset, expectedLow.Add(-epsilon), expectedHigh.Add(epsilon))
				}
			}
		})
	}
}

// TestQueue_Add_DeletedSessionDir pins the mitto-32ef fix: writeQueue must
// not recreate a session directory that was concurrently removed (e.g. by
// Store.Delete's os.RemoveAll racing an in-flight Add call). Previously,
// fileutil.WriteJSONAtomic's internal MkdirAll would silently resurrect the
// directory containing only queue.json, leaking an orphan with no
// metadata.json/events.jsonl. Add must now fail instead of succeeding into
// a resurrected directory.
func TestQueue_Add_DeletedSessionDir(t *testing.T) {
	tmpDir := t.TempDir()
	deletedSessionDir := filepath.Join(tmpDir, "deleted-session")
	// Deliberately never created, simulating a session dir already removed
	// by Store.Delete before this Queue's Add call reaches writeQueue.
	q := NewQueue(deletedSessionDir)

	_, err := q.Add("hello", nil, nil, "client1", nil, 0, nil, "")
	if err == nil {
		t.Fatal("Add() on a deleted session dir should fail, got nil error")
	}
	if !errors.Is(err, fileutil.ErrParentDirMissing) {
		t.Errorf("Add() error = %v, want wrapped fileutil.ErrParentDirMissing", err)
	}

	if _, statErr := os.Stat(deletedSessionDir); !os.IsNotExist(statErr) {
		t.Errorf("expected %q to remain absent (no orphan created), but it exists", deletedSessionDir)
	}
}
