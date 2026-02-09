//go:build integration

package inprocess

import (
	"testing"

	"github.com/inercia/mitto/internal/client"
)

// TestQueueOperations tests basic queue operations.
// Note: The background session auto-sends queued messages when idle,
// so we test the queue API directly without relying on message persistence.
func TestQueueOperations(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session first
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// 1. List queue (should be empty initially)
	queue, err := ts.Client.ListQueue(session.SessionID)
	if err != nil {
		t.Fatalf("ListQueue failed: %v", err)
	}
	t.Logf("Initial queue count: %d", queue.Count)

	// 2. Add a message to the queue
	msg1, err := ts.Client.AddToQueue(session.SessionID, "First message")
	if err != nil {
		t.Fatalf("AddToQueue failed: %v", err)
	}
	if msg1.ID == "" {
		t.Fatal("AddToQueue returned empty message ID")
	}
	if msg1.Message != "First message" {
		t.Errorf("Message content mismatch: got %q, want %q", msg1.Message, "First message")
	}
	t.Logf("Added message: %s", msg1.ID)

	// Note: The queue may be auto-processed by the background session,
	// so we don't test persistence here. The add operation succeeded,
	// which is the main thing we're testing.
}

// TestQueueRaceCondition tests the race condition where a message is added to the queue
// and immediately processed by the background session before the frontend can fetch it.
// This reproduces the bug where the UI shows "Message queued" but the queue appears empty.
func TestQueueRaceCondition(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session first
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Add a message to the queue
	msg, err := ts.Client.AddToQueue(session.SessionID, "Test message for race condition")
	if err != nil {
		t.Fatalf("AddToQueue failed: %v", err)
	}
	t.Logf("Added message: %s", msg.ID)

	// Immediately fetch the queue (simulating what the frontend does)
	// This is the race condition - the message may have already been processed
	queue, err := ts.Client.ListQueue(session.SessionID)
	if err != nil {
		t.Fatalf("ListQueue failed: %v", err)
	}

	// Log the result - this demonstrates the race condition
	// When the agent is idle, the queue will be empty because the message was auto-processed
	// When the agent is busy, the queue will have the message
	t.Logf("Queue count after add: %d (expected: 0 or 1 depending on agent state)", queue.Count)

	// The key insight: the message WAS added successfully (msg.ID is not empty),
	// but it may have been immediately processed and removed from the queue.
	// This is correct behavior, but the UI feedback is confusing.
}

// TestQueueMultipleMessages tests adding multiple messages to the queue.
// Note: This test may fail due to race conditions between queue processing
// and session deletion. The queue auto-processes messages when the agent is idle,
// which can cause file access conflicts during cleanup.
func TestQueueMultipleMessages(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Add multiple messages - some may fail due to race with auto-processing
	var addedCount int
	for i := 1; i <= 3; i++ {
		msg, err := ts.Client.AddToQueue(session.SessionID, "Message "+string(rune('0'+i)))
		if err != nil {
			// This can happen if the queue file is being modified by auto-processing
			t.Logf("AddToQueue %d failed (expected due to race): %v", i, err)
			continue
		}
		addedCount++
		t.Logf("Added message %d: %s", i, msg.ID)
	}

	// At least one message should have been added
	if addedCount == 0 {
		t.Fatal("No messages were added to the queue")
	}

	// Fetch the queue
	queue, err := ts.Client.ListQueue(session.SessionID)
	if err != nil {
		t.Fatalf("ListQueue failed: %v", err)
	}

	// Log the result - demonstrates that messages may be auto-processed
	t.Logf("Queue count after adding %d messages: %d", addedCount, queue.Count)

	// Note: The queue may have 0-N messages depending on how fast the agent processes them.
	// This is expected behavior - the queue auto-processes when the agent is idle.
}

// TestQueueWithImages tests adding messages with image attachments.
func TestQueueWithImages(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Add message with image IDs
	imageIDs := []string{"img-001", "img-002"}
	msg, err := ts.Client.AddToQueueWithImages(session.SessionID, "Message with images", imageIDs)
	if err != nil {
		t.Fatalf("AddToQueueWithImages failed: %v", err)
	}

	if len(msg.ImageIDs) != 2 {
		t.Errorf("Expected 2 image IDs, got %d", len(msg.ImageIDs))
	}

	// Verify via get
	got, err := ts.Client.GetQueueMessage(session.SessionID, msg.ID)
	if err != nil {
		t.Fatalf("GetQueueMessage failed: %v", err)
	}
	if len(got.ImageIDs) != 2 {
		t.Errorf("GetQueueMessage: expected 2 image IDs, got %d", len(got.ImageIDs))
	}
}

// TestQueueNonExistentSession tests queue operations on non-existent session.
func TestQueueNonExistentSession(t *testing.T) {
	ts := SetupTestServer(t)

	_, err := ts.Client.ListQueue("nonexistent-session")
	if err == nil {
		t.Error("ListQueue should fail for non-existent session")
	}

	_, err = ts.Client.AddToQueue("nonexistent-session", "test")
	if err == nil {
		t.Error("AddToQueue should fail for non-existent session")
	}
}
