package session

import (
	"strings"
	"testing"
)

func TestRecorder_StartAndEnd(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)

	// Session should not be started yet
	if recorder.IsStarted() {
		t.Error("Session should not be started before Start()")
	}

	// Start the session
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !recorder.IsStarted() {
		t.Error("Session should be started after Start()")
	}

	// Verify session was created
	if !store.Exists(recorder.SessionID()) {
		t.Error("Session should exist in store after Start()")
	}

	// End the session
	if err := recorder.End("user_quit"); err != nil {
		t.Fatalf("End failed: %v", err)
	}

	// Verify metadata status
	meta, err := store.GetMetadata(recorder.SessionID())
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.Status != SessionStatusCompleted {
		t.Errorf("Status = %q, want %q", meta.Status, SessionStatusCompleted)
	}
}

func TestRecorder_RecordEvents(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record various events
	if err := recorder.RecordUserPrompt("Hello, agent!"); err != nil {
		t.Errorf("RecordUserPrompt failed: %v", err)
	}

	if err := recorder.RecordAgentMessage("Hello! How can I help?"); err != nil {
		t.Errorf("RecordAgentMessage failed: %v", err)
	}

	if err := recorder.RecordAgentThought("Thinking about the request..."); err != nil {
		t.Errorf("RecordAgentThought failed: %v", err)
	}

	if err := recorder.RecordToolCall("tc-1", "Reading file", "running", "file_read", nil, nil); err != nil {
		t.Errorf("RecordToolCall failed: %v", err)
	}

	status := "completed"
	if err := recorder.RecordToolCallUpdate("tc-1", &status, nil); err != nil {
		t.Errorf("RecordToolCallUpdate failed: %v", err)
	}

	if err := recorder.RecordError("Something went wrong", 500); err != nil {
		t.Errorf("RecordError failed: %v", err)
	}

	// Read events and verify
	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Should have: session_start + 6 recorded events = 7 total
	if len(events) != 7 {
		t.Errorf("got %d events, want %d", len(events), 7)
	}

	// Verify first event is session start
	if events[0].Type != EventTypeSessionStart {
		t.Errorf("first event type = %q, want %q", events[0].Type, EventTypeSessionStart)
	}
}

func TestRecorder_EventCount(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)

	// Before starting, event count should be 0
	if count := recorder.EventCount(); count != 0 {
		t.Errorf("EventCount before start = %d, want 0", count)
	}

	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// After starting, event count should be 1 (session_start event)
	if count := recorder.EventCount(); count != 1 {
		t.Errorf("EventCount after start = %d, want 1", count)
	}

	// Record some events
	if err := recorder.RecordUserPrompt("Hello!"); err != nil {
		t.Fatalf("RecordUserPrompt failed: %v", err)
	}
	if count := recorder.EventCount(); count != 2 {
		t.Errorf("EventCount after user prompt = %d, want 2", count)
	}

	if err := recorder.RecordAgentMessage("Hi there!"); err != nil {
		t.Fatalf("RecordAgentMessage failed: %v", err)
	}
	if count := recorder.EventCount(); count != 3 {
		t.Errorf("EventCount after agent message = %d, want 3", count)
	}

	// Verify the count matches the actual events in the store
	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if len(events) != recorder.EventCount() {
		t.Errorf("EventCount() = %d, but actual events = %d", recorder.EventCount(), len(events))
	}
}

func TestRecorder_RecordBeforeStart(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)

	// Try to record without starting
	err = recorder.RecordUserPrompt("Hello")
	if err == nil {
		t.Error("Expected error when recording before Start()")
	}
	if !strings.Contains(err.Error(), "session not started") {
		t.Errorf("Expected 'session not started' error, got: %v", err)
	}
}

func TestRecorder_SessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	sessionID := recorder.SessionID()

	if sessionID == "" {
		t.Error("SessionID should not be empty")
	}

	// Session ID should contain timestamp format
	if !strings.Contains(sessionID, "-") {
		t.Error("SessionID should contain dashes (timestamp format)")
	}
}

func TestNewRecorderWithID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	customID := "custom-session-id"
	recorder := NewRecorderWithID(store, customID)

	if recorder.SessionID() != customID {
		t.Errorf("SessionID = %q, want %q", recorder.SessionID(), customID)
	}
}

func TestRecorder_Resume(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create and start a session
	recorder1 := NewRecorder(store)
	sessionID := recorder1.SessionID()
	if err := recorder1.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record some events
	if err := recorder1.RecordUserPrompt("Hello"); err != nil {
		t.Fatalf("RecordUserPrompt failed: %v", err)
	}
	if err := recorder1.RecordAgentMessage("Hi there!"); err != nil {
		t.Fatalf("RecordAgentMessage failed: %v", err)
	}

	// End the session
	if err := recorder1.End("user_quit"); err != nil {
		t.Fatalf("End failed: %v", err)
	}

	// Create a new recorder with the same session ID and resume
	recorder2 := NewRecorderWithID(store, sessionID)
	if err := recorder2.Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if !recorder2.IsStarted() {
		t.Error("Session should be started after Resume()")
	}

	// Record more events
	if err := recorder2.RecordUserPrompt("How are you?"); err != nil {
		t.Fatalf("RecordUserPrompt after resume failed: %v", err)
	}
	if err := recorder2.RecordAgentMessage("I'm doing well!"); err != nil {
		t.Fatalf("RecordAgentMessage after resume failed: %v", err)
	}

	// Verify all events are present
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Should have: session_start, user_prompt, agent_message, session_end, user_prompt, agent_message
	if len(events) != 6 {
		t.Errorf("Expected 6 events, got %d", len(events))
	}
}

func TestRecorder_ResumeNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Try to resume a non-existent session
	recorder := NewRecorderWithID(store, "non-existent-session")
	err = recorder.Resume()
	if err == nil {
		t.Error("Resume should fail for non-existent session")
	}
}

// TestRecorder_ResumeCompletedSessionUpdatesStatus tests that resuming a completed session
// updates its status back to active, preventing duplicate session_end events.
func TestRecorder_ResumeCompletedSessionUpdatesStatus(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create and start a session
	recorder1 := NewRecorder(store)
	sessionID := recorder1.SessionID()
	if err := recorder1.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record a user prompt
	if err := recorder1.RecordUserPrompt("Hello"); err != nil {
		t.Fatalf("RecordUserPrompt failed: %v", err)
	}

	// End the session - this should set status to completed
	if err := recorder1.End("server_shutdown"); err != nil {
		t.Fatalf("End failed: %v", err)
	}

	// Verify status is completed
	meta1, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta1.Status != SessionStatusCompleted {
		t.Errorf("Expected status %q after End, got %q", SessionStatusCompleted, meta1.Status)
	}

	// Resume the session - this should update status back to active
	recorder2 := NewRecorderWithID(store, sessionID)
	if err := recorder2.Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	// Verify status is now active
	meta2, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata after resume failed: %v", err)
	}
	if meta2.Status != SessionStatusActive {
		t.Errorf("Expected status %q after Resume, got %q", SessionStatusActive, meta2.Status)
	}

	// End the session again
	if err := recorder2.End("server_shutdown"); err != nil {
		t.Fatalf("Second End failed: %v", err)
	}

	// Verify we have exactly 2 session_end events (one from each End call)
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	sessionEndCount := 0
	for _, e := range events {
		if e.Type == EventTypeSessionEnd {
			sessionEndCount++
		}
	}

	// We expect 2 session_end events: one from recorder1.End() and one from recorder2.End()
	// The bug was that without the fix, resuming wouldn't update the status,
	// but End() would still write session_end because r.started was true.
	// With the fix, the status is properly tracked and each End() writes exactly one session_end.
	if sessionEndCount != 2 {
		t.Errorf("Expected 2 session_end events, got %d", sessionEndCount)
		for i, e := range events {
			t.Logf("Event %d: seq=%d type=%s", i, e.Seq, e.Type)
		}
	}
}

// TestRecorder_EventOrdering verifies that events maintain correct chronological order
// with proper sequence numbers regardless of event type.
func TestRecorder_EventOrdering(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record a realistic sequence of events with different types
	// This simulates: user prompt -> agent thinks -> tool call -> agent message -> tool call -> agent message
	if err := recorder.RecordUserPrompt("Fix the bug in main.go"); err != nil {
		t.Fatalf("RecordUserPrompt failed: %v", err)
	}
	if err := recorder.RecordAgentThought("Let me analyze the file first..."); err != nil {
		t.Fatalf("RecordAgentThought failed: %v", err)
	}
	if err := recorder.RecordToolCall("tc-1", "Read main.go", "running", "file_read", nil, nil); err != nil {
		t.Fatalf("RecordToolCall failed: %v", err)
	}
	status := "completed"
	if err := recorder.RecordToolCallUpdate("tc-1", &status, nil); err != nil {
		t.Fatalf("RecordToolCallUpdate failed: %v", err)
	}
	if err := recorder.RecordAgentMessage("I found the issue. Let me fix it."); err != nil {
		t.Fatalf("RecordAgentMessage failed: %v", err)
	}
	if err := recorder.RecordToolCall("tc-2", "Edit main.go", "running", "file_write", nil, nil); err != nil {
		t.Fatalf("RecordToolCall failed: %v", err)
	}
	if err := recorder.RecordToolCallUpdate("tc-2", &status, nil); err != nil {
		t.Fatalf("RecordToolCallUpdate failed: %v", err)
	}
	if err := recorder.RecordAgentMessage("Done! I fixed the bug."); err != nil {
		t.Fatalf("RecordAgentMessage failed: %v", err)
	}

	// Read events and verify ordering
	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Verify sequence numbers are monotonically increasing
	for i := 0; i < len(events); i++ {
		expectedSeq := int64(i + 1)
		if events[i].Seq != expectedSeq {
			t.Errorf("Event %d: seq = %d, want %d", i, events[i].Seq, expectedSeq)
		}
	}

	// Verify timestamps are non-decreasing
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			t.Errorf("Event %d timestamp (%v) is before event %d timestamp (%v)",
				i, events[i].Timestamp, i-1, events[i-1].Timestamp)
		}
	}

	// Verify the expected event type order
	expectedTypes := []EventType{
		EventTypeSessionStart,
		EventTypeUserPrompt,
		EventTypeAgentThought,
		EventTypeToolCall,
		EventTypeToolCallUpdate,
		EventTypeAgentMessage,
		EventTypeToolCall,
		EventTypeToolCallUpdate,
		EventTypeAgentMessage,
	}

	if len(events) != len(expectedTypes) {
		t.Fatalf("got %d events, want %d", len(events), len(expectedTypes))
	}

	for i, expected := range expectedTypes {
		if events[i].Type != expected {
			t.Errorf("Event %d: type = %q, want %q", i, events[i].Type, expected)
		}
	}
}

// TestRecorder_InterleavedToolCallsAndMessages tests the specific bug scenario where
// tool calls and agent messages are interleaved.
func TestRecorder_InterleavedToolCallsAndMessages(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record interleaved tool calls and messages
	recorder.RecordUserPrompt("Do something")
	recorder.RecordToolCall("tool-1", "First tool", "running", "test", nil, nil)
	recorder.RecordAgentMessage("First message")
	recorder.RecordToolCall("tool-2", "Second tool", "running", "test", nil, nil)
	recorder.RecordAgentMessage("Second message")
	recorder.RecordToolCall("tool-3", "Third tool", "running", "test", nil, nil)
	recorder.RecordAgentMessage("Third message")

	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Extract just the tool calls and agent messages (skip session_start and user_prompt)
	var relevantEvents []Event
	for _, e := range events {
		if e.Type == EventTypeToolCall || e.Type == EventTypeAgentMessage {
			relevantEvents = append(relevantEvents, e)
		}
	}

	// Verify the interleaved order is preserved
	expectedOrder := []EventType{
		EventTypeToolCall,     // tool-1
		EventTypeAgentMessage, // First message
		EventTypeToolCall,     // tool-2
		EventTypeAgentMessage, // Second message
		EventTypeToolCall,     // tool-3
		EventTypeAgentMessage, // Third message
	}

	if len(relevantEvents) != len(expectedOrder) {
		t.Fatalf("got %d relevant events, want %d", len(relevantEvents), len(expectedOrder))
	}

	for i, expected := range expectedOrder {
		if relevantEvents[i].Type != expected {
			t.Errorf("Relevant event %d: type = %q, want %q", i, relevantEvents[i].Type, expected)
		}
	}

	// Verify sequence numbers are correct for the interleaved events
	for i := 1; i < len(relevantEvents); i++ {
		if relevantEvents[i].Seq <= relevantEvents[i-1].Seq {
			t.Errorf("Event %d seq (%d) should be greater than event %d seq (%d)",
				i, relevantEvents[i].Seq, i-1, relevantEvents[i-1].Seq)
		}
	}
}

// TestRecorder_RapidEventRecording tests that events recorded in rapid succession
// maintain correct ordering and unique sequence numbers.
func TestRecorder_RapidEventRecording(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record many events as fast as possible
	const numEvents = 100
	for i := 0; i < numEvents; i++ {
		switch i % 4 {
		case 0:
			recorder.RecordUserPrompt("Message " + string(rune('A'+i%26)))
		case 1:
			recorder.RecordAgentMessage("Response " + string(rune('A'+i%26)))
		case 2:
			recorder.RecordToolCall("tool-"+string(rune('0'+i%10)), "Tool", "running", "test", nil, nil)
		case 3:
			recorder.RecordAgentThought("Thinking...")
		}
	}

	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Should have session_start + numEvents
	expectedCount := 1 + numEvents
	if len(events) != expectedCount {
		t.Fatalf("got %d events, want %d", len(events), expectedCount)
	}

	// Verify all sequence numbers are unique and monotonically increasing
	seenSeqs := make(map[int64]bool)
	for i, e := range events {
		if seenSeqs[e.Seq] {
			t.Errorf("Duplicate sequence number %d at event %d", e.Seq, i)
		}
		seenSeqs[e.Seq] = true

		expectedSeq := int64(i + 1)
		if e.Seq != expectedSeq {
			t.Errorf("Event %d: seq = %d, want %d", i, e.Seq, expectedSeq)
		}
	}

	// Verify timestamps are non-decreasing (some may be equal due to rapid recording)
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			t.Errorf("Event %d timestamp (%v) is before event %d timestamp (%v)",
				i, events[i].Timestamp, i-1, events[i-1].Timestamp)
		}
	}
}

// TestRecorder_ConcurrentRecording tests that concurrent recording attempts
// are properly serialized (the recorder uses a mutex).
func TestRecorder_ConcurrentRecording(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record events from multiple goroutines
	const numGoroutines = 10
	const eventsPerGoroutine = 10
	done := make(chan bool, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			for i := 0; i < eventsPerGoroutine; i++ {
				recorder.RecordAgentMessage("Message from goroutine")
			}
			done <- true
		}(g)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Should have session_start + (numGoroutines * eventsPerGoroutine)
	expectedCount := 1 + (numGoroutines * eventsPerGoroutine)
	if len(events) != expectedCount {
		t.Fatalf("got %d events, want %d", len(events), expectedCount)
	}

	// Verify all sequence numbers are unique
	seenSeqs := make(map[int64]bool)
	for i, e := range events {
		if seenSeqs[e.Seq] {
			t.Errorf("Duplicate sequence number %d at event %d", e.Seq, i)
		}
		seenSeqs[e.Seq] = true
	}

	// Verify sequence numbers are contiguous (1 to expectedCount)
	for i := 1; i <= expectedCount; i++ {
		if !seenSeqs[int64(i)] {
			t.Errorf("Missing sequence number %d", i)
		}
	}
}

// TestRecorder_ResumePreservesOrdering tests that resuming a session preserves
// the ordering of existing events and continues with correct sequence numbers.
func TestRecorder_ResumePreservesOrdering(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create and record initial events
	recorder1 := NewRecorder(store)
	if err := recorder1.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder1.SessionID()

	recorder1.RecordUserPrompt("First prompt")
	recorder1.RecordAgentMessage("First response")
	recorder1.RecordToolCall("tool-1", "First tool", "completed", "test", nil, nil)

	// Read events before resume
	eventsBefore, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	lastSeqBefore := eventsBefore[len(eventsBefore)-1].Seq

	// Resume the session with a new recorder
	recorder2 := NewRecorderWithID(store, sessionID)
	if err := recorder2.Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	// Record more events after resume
	recorder2.RecordUserPrompt("Second prompt")
	recorder2.RecordAgentMessage("Second response")
	recorder2.RecordToolCall("tool-2", "Second tool", "completed", "test", nil, nil)

	// Read all events
	eventsAfter, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Verify the original events are unchanged
	for i := 0; i < len(eventsBefore); i++ {
		if eventsAfter[i].Seq != eventsBefore[i].Seq {
			t.Errorf("Event %d seq changed: was %d, now %d", i, eventsBefore[i].Seq, eventsAfter[i].Seq)
		}
		if eventsAfter[i].Type != eventsBefore[i].Type {
			t.Errorf("Event %d type changed: was %q, now %q", i, eventsBefore[i].Type, eventsAfter[i].Type)
		}
	}

	// Verify new events have correct sequence numbers (continuing from last)
	for i := len(eventsBefore); i < len(eventsAfter); i++ {
		expectedSeq := lastSeqBefore + int64(i-len(eventsBefore)+1)
		if eventsAfter[i].Seq != expectedSeq {
			t.Errorf("New event %d: seq = %d, want %d", i, eventsAfter[i].Seq, expectedSeq)
		}
	}

	// Verify all sequence numbers are unique and monotonically increasing
	for i := 1; i < len(eventsAfter); i++ {
		if eventsAfter[i].Seq <= eventsAfter[i-1].Seq {
			t.Errorf("Event %d seq (%d) should be greater than event %d seq (%d)",
				i, eventsAfter[i].Seq, i-1, eventsAfter[i-1].Seq)
		}
	}
}

// TestRecorder_ResumeWithInterleavedEvents tests that resuming a session with
// interleaved tool calls and messages preserves the correct order.
func TestRecorder_ResumeWithInterleavedEvents(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create session with interleaved events
	recorder1 := NewRecorder(store)
	if err := recorder1.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder1.SessionID()

	recorder1.RecordUserPrompt("Do something")
	recorder1.RecordToolCall("tool-1", "First tool", "running", "test", nil, nil)
	recorder1.RecordAgentMessage("First message")
	recorder1.RecordToolCall("tool-2", "Second tool", "running", "test", nil, nil)

	// Resume and add more interleaved events
	recorder2 := NewRecorderWithID(store, sessionID)
	if err := recorder2.Resume(); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	recorder2.RecordAgentMessage("Second message")
	recorder2.RecordToolCall("tool-3", "Third tool", "running", "test", nil, nil)
	recorder2.RecordAgentMessage("Third message")

	// Read all events
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Extract tool calls and agent messages
	var relevantEvents []Event
	for _, e := range events {
		if e.Type == EventTypeToolCall || e.Type == EventTypeAgentMessage {
			relevantEvents = append(relevantEvents, e)
		}
	}

	// Verify the interleaved order is preserved across resume
	expectedOrder := []EventType{
		EventTypeToolCall,     // tool-1 (before resume)
		EventTypeAgentMessage, // First message (before resume)
		EventTypeToolCall,     // tool-2 (before resume)
		EventTypeAgentMessage, // Second message (after resume)
		EventTypeToolCall,     // tool-3 (after resume)
		EventTypeAgentMessage, // Third message (after resume)
	}

	if len(relevantEvents) != len(expectedOrder) {
		t.Fatalf("got %d relevant events, want %d", len(relevantEvents), len(expectedOrder))
	}

	for i, expected := range expectedOrder {
		if relevantEvents[i].Type != expected {
			t.Errorf("Relevant event %d: type = %q, want %q", i, relevantEvents[i].Type, expected)
		}
	}
}

func TestRecorder_RecordPlan(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	entries := []PlanEntry{
		{Content: "Task 1", Priority: "high", Status: "completed"},
		{Content: "Task 2", Priority: "medium", Status: "in_progress"},
		{Content: "Task 3", Priority: "low", Status: "pending"},
	}

	if err := recorder.RecordPlan(entries); err != nil {
		t.Fatalf("RecordPlan failed: %v", err)
	}

	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Should have: session_start + plan = 2 events
	if len(events) != 2 {
		t.Errorf("got %d events, want 2", len(events))
	}

	// Verify the plan event
	planEvent := events[1]
	if planEvent.Type != EventTypePlan {
		t.Errorf("event type = %q, want %q", planEvent.Type, EventTypePlan)
	}

	// After JSON round-trip, data comes back as map[string]interface{}
	planDataMap, ok := planEvent.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event data is not map[string]interface{}, got %T", planEvent.Data)
	}

	entriesRaw, ok := planDataMap["entries"].([]interface{})
	if !ok {
		t.Fatalf("entries is not []interface{}, got %T", planDataMap["entries"])
	}

	if len(entriesRaw) != 3 {
		t.Errorf("got %d plan entries, want 3", len(entriesRaw))
	}
}

func TestRecorder_RecordPermission(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := recorder.RecordPermission("Allow file access?", "yes", "approved"); err != nil {
		t.Fatalf("RecordPermission failed: %v", err)
	}

	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	if len(events) != 2 {
		t.Errorf("got %d events, want 2", len(events))
	}

	permEvent := events[1]
	if permEvent.Type != EventTypePermission {
		t.Errorf("event type = %q, want %q", permEvent.Type, EventTypePermission)
	}

	// After JSON round-trip, data comes back as map[string]interface{}
	permDataMap, ok := permEvent.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event data is not map[string]interface{}, got %T", permEvent.Data)
	}

	if title, ok := permDataMap["title"].(string); !ok || title != "Allow file access?" {
		t.Errorf("title = %v, want %q", permDataMap["title"], "Allow file access?")
	}
	if selectedOption, ok := permDataMap["selected_option"].(string); !ok || selectedOption != "yes" {
		t.Errorf("selected_option = %v, want %q", permDataMap["selected_option"], "yes")
	}
	if outcome, ok := permDataMap["outcome"].(string); !ok || outcome != "approved" {
		t.Errorf("outcome = %v, want %q", permDataMap["outcome"], "approved")
	}
}

func TestRecorder_RecordFileOperations(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Test RecordFileRead
	if err := recorder.RecordFileRead("/path/to/file.txt", 1024); err != nil {
		t.Fatalf("RecordFileRead failed: %v", err)
	}

	// Test RecordFileWrite
	if err := recorder.RecordFileWrite("/path/to/output.txt", 2048); err != nil {
		t.Fatalf("RecordFileWrite failed: %v", err)
	}

	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Should have: session_start + file_read + file_write = 3 events
	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
	}

	// Verify file read event
	readEvent := events[1]
	if readEvent.Type != EventTypeFileRead {
		t.Errorf("event[1] type = %q, want %q", readEvent.Type, EventTypeFileRead)
	}

	// After JSON round-trip, data comes back as map[string]interface{}
	readDataMap, ok := readEvent.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event[1] data is not map[string]interface{}, got %T", readEvent.Data)
	}

	if path, ok := readDataMap["path"].(string); !ok || path != "/path/to/file.txt" {
		t.Errorf("readData.path = %v, want %q", readDataMap["path"], "/path/to/file.txt")
	}
	// JSON unmarshals numbers as float64
	if size, ok := readDataMap["size"].(float64); !ok || int(size) != 1024 {
		t.Errorf("readData.size = %v, want 1024", readDataMap["size"])
	}

	// Verify file write event
	writeEvent := events[2]
	if writeEvent.Type != EventTypeFileWrite {
		t.Errorf("event[2] type = %q, want %q", writeEvent.Type, EventTypeFileWrite)
	}

	writeDataMap, ok := writeEvent.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event[2] data is not map[string]interface{}, got %T", writeEvent.Data)
	}

	if path, ok := writeDataMap["path"].(string); !ok || path != "/path/to/output.txt" {
		t.Errorf("writeData.path = %v, want %q", writeDataMap["path"], "/path/to/output.txt")
	}
	if size, ok := writeDataMap["size"].(float64); !ok || int(size) != 2048 {
		t.Errorf("writeData.size = %v, want 2048", writeDataMap["size"])
	}
}

func TestRecorder_Suspend(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record an event
	if err := recorder.RecordUserPrompt("Hello"); err != nil {
		t.Fatalf("RecordUserPrompt failed: %v", err)
	}

	if !recorder.IsStarted() {
		t.Error("Session should be started before Suspend()")
	}

	// Suspend the session
	if err := recorder.Suspend(); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	// Session should no longer be started
	if recorder.IsStarted() {
		t.Error("Session should not be started after Suspend()")
	}

	// Recording should fail after suspend
	err = recorder.RecordUserPrompt("This should fail")
	if err == nil {
		t.Error("RecordUserPrompt should fail after Suspend()")
	}

	// Verify metadata status is still active (not ended)
	meta, err := store.GetMetadata(recorder.SessionID())
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}

	// Status should still be active (Suspend doesn't change status)
	if meta.Status != SessionStatusActive {
		t.Errorf("Status after Suspend = %q, want %q", meta.Status, SessionStatusActive)
	}
}

func TestRecorder_SuspendBeforeStart(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)

	// Suspend before starting should be a no-op (not an error)
	if err := recorder.Suspend(); err != nil {
		t.Errorf("Suspend before Start should not error, got: %v", err)
	}
}

func TestRecorder_RecordUserPromptWithImages(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	images := []ImageRef{
		{ID: "img-1", Name: "screenshot.png", MimeType: "image/png"},
		{ID: "img-2", Name: "photo.jpeg", MimeType: "image/jpeg"},
	}

	if err := recorder.RecordUserPromptWithImages("Here's a screenshot", images); err != nil {
		t.Fatalf("RecordUserPromptWithImages failed: %v", err)
	}

	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	if len(events) != 2 {
		t.Errorf("got %d events, want 2", len(events))
	}

	promptEvent := events[1]
	if promptEvent.Type != EventTypeUserPrompt {
		t.Errorf("event type = %q, want %q", promptEvent.Type, EventTypeUserPrompt)
	}

	// After JSON round-trip, data comes back as map[string]interface{}
	promptDataMap, ok := promptEvent.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event data is not map[string]interface{}, got %T", promptEvent.Data)
	}

	imagesRaw, ok := promptDataMap["images"].([]interface{})
	if !ok {
		t.Fatalf("images is not []interface{}, got %T", promptDataMap["images"])
	}

	if len(imagesRaw) != 2 {
		t.Errorf("got %d images, want 2", len(imagesRaw))
	}
}
