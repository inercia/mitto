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
