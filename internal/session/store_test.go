package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_CreateAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-1",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}

	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify session directory was created
	sessionDir := filepath.Join(tmpDir, "test-session-1")
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		t.Error("Session directory was not created")
	}

	// Verify metadata can be retrieved
	gotMeta, err := store.GetMetadata("test-session-1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}

	if gotMeta.SessionID != meta.SessionID {
		t.Errorf("SessionID = %q, want %q", gotMeta.SessionID, meta.SessionID)
	}
	if gotMeta.ACPServer != meta.ACPServer {
		t.Errorf("ACPServer = %q, want %q", gotMeta.ACPServer, meta.ACPServer)
	}
	if gotMeta.Status != SessionStatusActive {
		t.Errorf("Status = %q, want %q", gotMeta.Status, SessionStatusActive)
	}
}

func TestStore_AppendAndReadEvents(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-2",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}

	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append events
	events := []Event{
		{Type: EventTypeUserPrompt, Timestamp: time.Now(), Data: UserPromptData{Message: "Hello"}},
		{Type: EventTypeAgentMessage, Timestamp: time.Now(), Data: AgentMessageData{Text: "Hi there!"}},
	}

	for _, event := range events {
		if err := store.AppendEvent("test-session-2", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Read events back
	gotEvents, err := store.ReadEvents("test-session-2")
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	if len(gotEvents) != len(events) {
		t.Fatalf("got %d events, want %d", len(gotEvents), len(events))
	}

	// Verify event count in metadata
	gotMeta, err := store.GetMetadata("test-session-2")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if gotMeta.EventCount != 2 {
		t.Errorf("EventCount = %d, want %d", gotMeta.EventCount, 2)
	}

	// Verify sequence numbers are assigned
	if gotEvents[0].Seq != 1 {
		t.Errorf("First event Seq = %d, want 1", gotEvents[0].Seq)
	}
	if gotEvents[1].Seq != 2 {
		t.Errorf("Second event Seq = %d, want 2", gotEvents[1].Seq)
	}
}

func TestStore_ReadEventsFrom(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-sync",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}

	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append 5 events
	for i := 0; i < 5; i++ {
		event := Event{
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: "Message " + string(rune('A'+i))},
		}
		if err := store.AppendEvent("test-session-sync", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Read all events (afterSeq = 0)
	allEvents, err := store.ReadEventsFrom("test-session-sync", 0)
	if err != nil {
		t.Fatalf("ReadEventsFrom(0) failed: %v", err)
	}
	if len(allEvents) != 5 {
		t.Errorf("ReadEventsFrom(0) got %d events, want 5", len(allEvents))
	}

	// Read events after seq 2 (should get events 3, 4, 5)
	partialEvents, err := store.ReadEventsFrom("test-session-sync", 2)
	if err != nil {
		t.Fatalf("ReadEventsFrom(2) failed: %v", err)
	}
	if len(partialEvents) != 3 {
		t.Errorf("ReadEventsFrom(2) got %d events, want 3", len(partialEvents))
	}
	if partialEvents[0].Seq != 3 {
		t.Errorf("First event after seq 2 has Seq = %d, want 3", partialEvents[0].Seq)
	}

	// Read events after seq 5 (should get 0 events)
	noEvents, err := store.ReadEventsFrom("test-session-sync", 5)
	if err != nil {
		t.Fatalf("ReadEventsFrom(5) failed: %v", err)
	}
	if len(noEvents) != 0 {
		t.Errorf("ReadEventsFrom(5) got %d events, want 0", len(noEvents))
	}
}

func TestStore_List(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create multiple sessions
	for i := 0; i < 3; i++ {
		meta := Metadata{
			SessionID:  "session-" + string(rune('a'+i)),
			ACPServer:  "test-server",
			WorkingDir: "/test/dir",
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(sessions) != 3 {
		t.Errorf("got %d sessions, want %d", len(sessions), 3)
	}
}

func TestStore_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-delete",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}

	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if !store.Exists("test-session-delete") {
		t.Error("Session should exist after creation")
	}

	if err := store.Delete("test-session-delete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if store.Exists("test-session-delete") {
		t.Error("Session should not exist after deletion")
	}
}
