//go:build integration

// Package client contains integration tests for session sync functionality.
// These tests verify that clients can sync missed events after disconnection.
package client

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// =============================================================================
// Sync Tests
// =============================================================================

// TestSync_MissedEvents_Recovery verifies that a client can use sync_session
// to recover events that were missed while disconnected.
func TestSync_MissedEvents_Recovery(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "sync-recovery",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First connection - send a prompt and record the event count
	result1, err := c.PromptAndWait(ctx, session.SessionID, "Hello first!")
	if err != nil {
		t.Fatalf("First PromptAndWait failed: %v", err)
	}
	firstEventCount := result1.EventCount
	t.Logf("After first prompt: event_count=%d", firstEventCount)

	// Second prompt
	result2, err := c.PromptAndWait(ctx, session.SessionID, "Hello second!")
	if err != nil {
		t.Fatalf("Second PromptAndWait failed: %v", err)
	}
	secondEventCount := result2.EventCount
	t.Logf("After second prompt: event_count=%d", secondEventCount)

	// Now connect and request sync from after the first prompt
	var mu sync.Mutex
	var syncEvents []client.SyncEvent
	var syncEventCount int
	syncReceived := make(chan struct{})
	connected := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(connected)
		},
		OnSessionSync: func(events []client.SyncEvent, eventCount int) {
			mu.Lock()
			syncEvents = events
			syncEventCount = eventCount
			mu.Unlock()
			close(syncReceived)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	<-connected

	// Request sync from after the first prompt's events
	if err := sess.Sync(firstEventCount); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Wait for sync response
	select {
	case <-syncReceived:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for sync response")
	}

	mu.Lock()
	defer mu.Unlock()

	// Should have received events from after firstEventCount
	if len(syncEvents) == 0 {
		t.Error("Expected sync events, got none")
	}

	// The sync should include events from the second prompt
	t.Logf("Sync returned %d events, total event_count=%d", len(syncEvents), syncEventCount)

	// Verify event count matches
	if syncEventCount != secondEventCount {
		t.Errorf("Sync event_count=%d, want %d", syncEventCount, secondEventCount)
	}

	// Log the events for debugging
	for i, e := range syncEvents {
		t.Logf("Sync event %d: seq=%d type=%s", i, e.Seq, e.Type)
	}
}

// TestSync_EventOrderingPreserved verifies that synced events maintain
// their original ordering (by sequence number).
func TestSync_EventOrderingPreserved(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "sync-ordering",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Send a prompt that generates multiple events (tool calls + messages)
	result, err := c.PromptAndWait(ctx, session.SessionID, "fix the file")
	if err != nil {
		t.Fatalf("PromptAndWait failed: %v", err)
	}
	eventCount := result.EventCount
	t.Logf("After prompt: event_count=%d, tool_calls=%d", eventCount, len(result.ToolCalls))

	// Connect and sync from the beginning
	var mu sync.Mutex
	var syncEvents []client.SyncEvent
	syncReceived := make(chan struct{})
	connected := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(connected)
		},
		OnSessionSync: func(events []client.SyncEvent, ec int) {
			mu.Lock()
			syncEvents = events
			mu.Unlock()
			close(syncReceived)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	<-connected

	// Request sync from the beginning (seq 0)
	if err := sess.Sync(0); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	select {
	case <-syncReceived:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for sync response")
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify events are in sequence order
	var lastSeq int64 = 0
	for i, e := range syncEvents {
		if e.Seq <= lastSeq {
			t.Errorf("Event %d has seq=%d, but previous was %d (not increasing)", i, e.Seq, lastSeq)
		}
		lastSeq = e.Seq
	}

	t.Logf("Verified %d events in sequence order (last seq=%d)", len(syncEvents), lastSeq)
}

// TestSync_PartialSync verifies that sync correctly returns only events
// after the specified sequence number.
func TestSync_PartialSync(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "sync-partial",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Send three prompts to generate events
	var eventCounts []int
	prompts := []string{"Hello first!", "Hello second!", "Hello third!"}
	for _, prompt := range prompts {
		result, err := c.PromptAndWait(ctx, session.SessionID, prompt)
		if err != nil {
			t.Fatalf("PromptAndWait failed: %v", err)
		}
		eventCounts = append(eventCounts, result.EventCount)
	}
	t.Logf("Event counts after each prompt: %v", eventCounts)

	// Connect and sync from after the second prompt
	var mu sync.Mutex
	var syncEvents []client.SyncEvent
	syncReceived := make(chan struct{})
	connected := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(connected)
		},
		OnSessionSync: func(events []client.SyncEvent, ec int) {
			mu.Lock()
			syncEvents = events
			mu.Unlock()
			close(syncReceived)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	<-connected

	// Sync from after the second prompt
	afterSeq := eventCounts[1]
	if err := sess.Sync(afterSeq); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	select {
	case <-syncReceived:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for sync response")
	}

	mu.Lock()
	defer mu.Unlock()

	// All returned events should have seq > afterSeq
	for i, e := range syncEvents {
		if e.Seq <= int64(afterSeq) {
			t.Errorf("Event %d has seq=%d, but should be > %d", i, e.Seq, afterSeq)
		}
	}

	// Should have events from the third prompt only
	expectedEvents := eventCounts[2] - eventCounts[1]
	if len(syncEvents) != expectedEvents {
		t.Errorf("Got %d events, expected %d (events from third prompt only)", len(syncEvents), expectedEvents)
	}

	t.Logf("Partial sync from seq %d returned %d events", afterSeq, len(syncEvents))
}

// TestSync_EmptyWhenUpToDate verifies that sync returns empty when
// the client is already up to date.
func TestSync_EmptyWhenUpToDate(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "sync-empty",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Send a prompt
	result, err := c.PromptAndWait(ctx, session.SessionID, "Hello!")
	if err != nil {
		t.Fatalf("PromptAndWait failed: %v", err)
	}
	eventCount := result.EventCount

	// Connect and sync from the current event count (should be empty)
	var mu sync.Mutex
	var syncEvents []client.SyncEvent
	syncReceived := make(chan struct{})
	connected := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(connected)
		},
		OnSessionSync: func(events []client.SyncEvent, ec int) {
			mu.Lock()
			syncEvents = events
			mu.Unlock()
			close(syncReceived)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	<-connected

	// Sync from the current event count
	if err := sess.Sync(eventCount); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	select {
	case <-syncReceived:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for sync response")
	}

	mu.Lock()
	defer mu.Unlock()

	// Should have no events (already up to date)
	if len(syncEvents) != 0 {
		t.Errorf("Expected 0 events when up to date, got %d", len(syncEvents))
	}

	t.Logf("Sync from current event count (%d) correctly returned 0 events", eventCount)
}
