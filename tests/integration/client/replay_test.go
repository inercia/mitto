//go:build integration

// Package client contains integration tests for event replay functionality.
// These tests verify that events are correctly replayed when clients reconnect.
package client

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// =============================================================================
// Event Replay Tests
// =============================================================================

// TestReplay_ReconnectGetsAllEvents verifies that reconnecting to a session
// after events have occurred allows the client to retrieve all events via sync.
func TestReplay_ReconnectGetsAllEvents(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "replay-reconnect",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First connection - send multiple prompts
	prompts := []string{"Hello first!", "fix the file", "Hello third!"}
	var lastEventCount int
	for _, prompt := range prompts {
		result, err := c.PromptAndWait(ctx, session.SessionID, prompt)
		if err != nil {
			t.Fatalf("PromptAndWait failed: %v", err)
		}
		lastEventCount = result.EventCount
	}
	t.Logf("After all prompts: event_count=%d", lastEventCount)

	// Reconnect and sync from the beginning
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

	// Sync from the beginning
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

	// Should have all events
	if len(syncEvents) != lastEventCount {
		t.Errorf("Got %d events, expected %d", len(syncEvents), lastEventCount)
	}

	// Count event types
	eventTypes := make(map[string]int)
	for _, e := range syncEvents {
		eventTypes[e.Type]++
	}

	t.Logf("Replay got %d events: %v", len(syncEvents), eventTypes)

	// Should have user_prompt events (one per prompt)
	if eventTypes["user_prompt"] != len(prompts) {
		t.Errorf("Expected %d user_prompt events, got %d", len(prompts), eventTypes["user_prompt"])
	}

	// Should have agent_message events
	if eventTypes["agent_message"] == 0 {
		t.Error("Expected at least one agent_message event")
	}
}

// TestReplay_EventTypesPreserved verifies that all event types are correctly
// preserved and can be replayed.
func TestReplay_EventTypesPreserved(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "replay-types",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Send a prompt that generates tool calls
	result, err := c.PromptAndWait(ctx, session.SessionID, "fix the file")
	if err != nil {
		t.Fatalf("PromptAndWait failed: %v", err)
	}
	t.Logf("Prompt generated: messages=%d, thoughts=%d, tool_calls=%d",
		len(result.Messages), len(result.Thoughts), len(result.ToolCalls))

	// Reconnect and sync
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

	// Count event types
	eventTypes := make(map[string]int)
	for _, e := range syncEvents {
		eventTypes[e.Type]++
	}

	t.Logf("Event types in replay: %v", eventTypes)

	// Verify expected event types are present
	expectedTypes := []string{"user_prompt", "agent_message"}
	for _, et := range expectedTypes {
		if eventTypes[et] == 0 {
			t.Errorf("Expected at least one %s event in replay", et)
		}
	}

	// If we had tool calls in the original, they should be in the replay
	if len(result.ToolCalls) > 0 {
		if eventTypes["tool_call"] == 0 {
			t.Error("Original had tool calls but replay has none")
		}
	}
}

// TestReplay_SequenceNumbersMatch verifies that replayed events have the
// same sequence numbers as when they were originally created.
func TestReplay_SequenceNumbersMatch(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "replay-seq",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Send prompts and track event counts
	var eventCounts []int
	prompts := []string{"Hello!", "fix the file"}
	for _, prompt := range prompts {
		result, err := c.PromptAndWait(ctx, session.SessionID, prompt)
		if err != nil {
			t.Fatalf("PromptAndWait failed: %v", err)
		}
		eventCounts = append(eventCounts, result.EventCount)
	}

	// Sync twice - once from beginning, once from middle
	// Both should return consistent sequence numbers

	var sync1Events, sync2Events []client.SyncEvent
	var mu sync.Mutex

	// First sync - all events
	sync1Done := make(chan struct{})
	connected1 := make(chan struct{})
	sess1, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(connected1)
		},
		OnSessionSync: func(events []client.SyncEvent, ec int) {
			mu.Lock()
			sync1Events = events
			mu.Unlock()
			close(sync1Done)
		},
	})
	if err != nil {
		t.Fatalf("Connect 1 failed: %v", err)
	}

	<-connected1
	sess1.Sync(0)
	<-sync1Done
	sess1.Close()

	// Second sync - from middle
	sync2Done := make(chan struct{})
	connected2 := make(chan struct{})
	sess2, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(connected2)
		},
		OnSessionSync: func(events []client.SyncEvent, ec int) {
			mu.Lock()
			sync2Events = events
			mu.Unlock()
			close(sync2Done)
		},
	})
	if err != nil {
		t.Fatalf("Connect 2 failed: %v", err)
	}

	<-connected2
	sess2.Sync(eventCounts[0])
	<-sync2Done
	sess2.Close()

	mu.Lock()
	defer mu.Unlock()

	// Events from sync2 should be a suffix of sync1
	// Find where sync2 events start in sync1
	if len(sync2Events) > 0 {
		firstSync2Seq := sync2Events[0].Seq
		var matchingEvents []client.SyncEvent
		for _, e := range sync1Events {
			if e.Seq >= firstSync2Seq {
				matchingEvents = append(matchingEvents, e)
			}
		}

		if len(matchingEvents) != len(sync2Events) {
			t.Errorf("Sync2 has %d events, but sync1 suffix has %d", len(sync2Events), len(matchingEvents))
		}

		// Verify sequence numbers match
		for i := range sync2Events {
			if i < len(matchingEvents) && sync2Events[i].Seq != matchingEvents[i].Seq {
				t.Errorf("Event %d: sync2 seq=%d, sync1 seq=%d", i, sync2Events[i].Seq, matchingEvents[i].Seq)
			}
		}
	}

	t.Logf("Sync1: %d events, Sync2: %d events", len(sync1Events), len(sync2Events))
}

// TestReplay_LargeSession verifies replay works correctly with many events.
func TestReplay_LargeSession(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "replay-large",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Send many prompts
	numPrompts := 5
	var lastEventCount int
	for i := 0; i < numPrompts; i++ {
		result, err := c.PromptAndWait(ctx, session.SessionID, fmt.Sprintf("Message %d", i+1))
		if err != nil {
			t.Fatalf("PromptAndWait %d failed: %v", i+1, err)
		}
		lastEventCount = result.EventCount
	}
	t.Logf("After %d prompts: event_count=%d", numPrompts, lastEventCount)

	// Sync all events
	var syncEvents []client.SyncEvent
	syncReceived := make(chan struct{})
	connected := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(connected)
		},
		OnSessionSync: func(events []client.SyncEvent, ec int) {
			syncEvents = events
			close(syncReceived)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	<-connected
	sess.Sync(0)

	select {
	case <-syncReceived:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for sync response")
	}

	// Verify we got all events
	if len(syncEvents) != lastEventCount {
		t.Errorf("Got %d events, expected %d", len(syncEvents), lastEventCount)
	}

	// Count user prompts
	userPrompts := 0
	for _, e := range syncEvents {
		if e.Type == "user_prompt" {
			userPrompts++
		}
	}

	if userPrompts != numPrompts {
		t.Errorf("Got %d user_prompt events, expected %d", userPrompts, numPrompts)
	}

	t.Logf("Large session replay: %d events, %d user prompts", len(syncEvents), userPrompts)
}
