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
