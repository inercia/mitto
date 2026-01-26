package session

import (
	"testing"
	"time"
)

func TestPlayer_BasicPlayback(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with events
	recorder := NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	recorder.RecordUserPrompt("Hello")
	recorder.RecordAgentMessage("Hi there!")
	recorder.RecordUserPrompt("How are you?")
	recorder.End("user_quit")

	// Create player
	player, err := NewPlayer(store, recorder.SessionID())
	if err != nil {
		t.Fatalf("NewPlayer failed: %v", err)
	}

	// Verify event count
	if player.EventCount() != 5 { // session_start + 3 events + session_end
		t.Errorf("EventCount = %d, want %d", player.EventCount(), 5)
	}

	// Test playback
	if !player.HasNext() {
		t.Error("HasNext should be true at start")
	}

	event, ok := player.Next()
	if !ok {
		t.Error("Next should return true")
	}
	if event.Type != EventTypeSessionStart {
		t.Errorf("First event type = %q, want %q", event.Type, EventTypeSessionStart)
	}

	// Test position
	if player.Position() != 1 {
		t.Errorf("Position = %d, want %d", player.Position(), 1)
	}

	// Test peek
	peeked, ok := player.Peek()
	if !ok {
		t.Error("Peek should return true")
	}
	if peeked.Type != EventTypeUserPrompt {
		t.Errorf("Peeked event type = %q, want %q", peeked.Type, EventTypeUserPrompt)
	}
	// Position should not change after peek
	if player.Position() != 1 {
		t.Errorf("Position after Peek = %d, want %d", player.Position(), 1)
	}
}

func TestPlayer_Seek(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	recorder.Start("test-server", "/test/dir")
	recorder.RecordUserPrompt("Message 1")
	recorder.RecordUserPrompt("Message 2")
	recorder.RecordUserPrompt("Message 3")
	recorder.End("user_quit")

	player, err := NewPlayer(store, recorder.SessionID())
	if err != nil {
		t.Fatalf("NewPlayer failed: %v", err)
	}

	// Seek to position 3
	if err := player.Seek(3); err != nil {
		t.Fatalf("Seek failed: %v", err)
	}
	if player.Position() != 3 {
		t.Errorf("Position = %d, want %d", player.Position(), 3)
	}

	// Seek out of range should fail
	if err := player.Seek(100); err == nil {
		t.Error("Seek out of range should fail")
	}

	// Reset
	player.Reset()
	if player.Position() != 0 {
		t.Errorf("Position after Reset = %d, want %d", player.Position(), 0)
	}
}

func TestPlayer_EventsOfType(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	recorder.Start("test-server", "/test/dir")
	recorder.RecordUserPrompt("User 1")
	recorder.RecordAgentMessage("Agent 1")
	recorder.RecordUserPrompt("User 2")
	recorder.RecordAgentMessage("Agent 2")
	recorder.End("user_quit")

	player, err := NewPlayer(store, recorder.SessionID())
	if err != nil {
		t.Fatalf("NewPlayer failed: %v", err)
	}

	userPrompts := player.EventsOfType(EventTypeUserPrompt)
	if len(userPrompts) != 2 {
		t.Errorf("got %d user prompts, want %d", len(userPrompts), 2)
	}

	agentMessages := player.EventsOfType(EventTypeAgentMessage)
	if len(agentMessages) != 2 {
		t.Errorf("got %d agent messages, want %d", len(agentMessages), 2)
	}
}

func TestDecodeEventData(t *testing.T) {
	event := Event{
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"message": "Hello"},
	}

	decoded, err := DecodeEventData(event)
	if err != nil {
		t.Fatalf("DecodeEventData failed: %v", err)
	}

	data, ok := decoded.(UserPromptData)
	if !ok {
		t.Fatalf("Expected UserPromptData, got %T", decoded)
	}
	if data.Message != "Hello" {
		t.Errorf("Message = %q, want %q", data.Message, "Hello")
	}
}
