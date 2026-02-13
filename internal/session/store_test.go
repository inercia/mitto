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

func TestStore_ReadEventsLastReverse(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-reverse",
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
		if err := store.AppendEvent("test-session-reverse", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Read last 3 events in reverse order (should get seq 5, 4, 3)
	reverseEvents, err := store.ReadEventsLastReverse("test-session-reverse", 3, 0)
	if err != nil {
		t.Fatalf("ReadEventsLastReverse failed: %v", err)
	}
	if len(reverseEvents) != 3 {
		t.Errorf("ReadEventsLastReverse got %d events, want 3", len(reverseEvents))
	}
	// First event should be the newest (seq 5)
	if reverseEvents[0].Seq != 5 {
		t.Errorf("First event Seq = %d, want 5 (newest)", reverseEvents[0].Seq)
	}
	// Last event should be the oldest of the 3 (seq 3)
	if reverseEvents[2].Seq != 3 {
		t.Errorf("Last event Seq = %d, want 3 (oldest of batch)", reverseEvents[2].Seq)
	}

	// Read all events in reverse order
	allReverse, err := store.ReadEventsLastReverse("test-session-reverse", 10, 0)
	if err != nil {
		t.Fatalf("ReadEventsLastReverse(all) failed: %v", err)
	}
	if len(allReverse) != 5 {
		t.Errorf("ReadEventsLastReverse(all) got %d events, want 5", len(allReverse))
	}
	// Verify order: newest first
	for i, event := range allReverse {
		expectedSeq := int64(5 - i)
		if event.Seq != expectedSeq {
			t.Errorf("Event %d has Seq = %d, want %d", i, event.Seq, expectedSeq)
		}
	}

	// Read events before seq 4 in reverse order (should get seq 3, 2, 1)
	beforeEvents, err := store.ReadEventsLastReverse("test-session-reverse", 10, 4)
	if err != nil {
		t.Fatalf("ReadEventsLastReverse(before=4) failed: %v", err)
	}
	if len(beforeEvents) != 3 {
		t.Errorf("ReadEventsLastReverse(before=4) got %d events, want 3", len(beforeEvents))
	}
	if beforeEvents[0].Seq != 3 {
		t.Errorf("First event before seq 4 has Seq = %d, want 3", beforeEvents[0].Seq)
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

// TestStore_RecordEvent tests that RecordEvent preserves the pre-assigned seq.
func TestStore_RecordEvent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-record-event"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Record an event with pre-assigned seq
	event := Event{
		Seq:       1,
		Type:      EventTypeAgentMessage,
		Timestamp: time.Now(),
		Data:      AgentMessageData{Text: "Hello"},
	}
	if err := store.RecordEvent(sessionID, event); err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	// Read back and verify seq is preserved
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].Seq != 1 {
		t.Errorf("Event seq = %d, want 1", events[0].Seq)
	}

	// Verify MaxSeq is updated in metadata
	gotMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if gotMeta.MaxSeq != 1 {
		t.Errorf("MaxSeq = %d, want 1", gotMeta.MaxSeq)
	}
}

// TestStore_RecordEvent_SeqValidation tests that RecordEvent rejects seq <= 0.
func TestStore_RecordEvent_SeqValidation(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-record-event-validation"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to record an event with seq = 0 (should fail)
	event := Event{
		Seq:       0,
		Type:      EventTypeAgentMessage,
		Timestamp: time.Now(),
		Data:      AgentMessageData{Text: "Hello"},
	}
	err = store.RecordEvent(sessionID, event)
	if err == nil {
		t.Error("RecordEvent should fail with seq = 0")
	}

	// Try to record an event with seq = -1 (should fail)
	event.Seq = -1
	err = store.RecordEvent(sessionID, event)
	if err == nil {
		t.Error("RecordEvent should fail with seq = -1")
	}
}

// TestStore_RecordEvent_MultipleEvents tests recording multiple events with pre-assigned seq.
func TestStore_RecordEvent_MultipleEvents(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-record-multiple"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Record multiple events
	for i := int64(1); i <= 5; i++ {
		event := Event{
			Seq:       i,
			Type:      EventTypeAgentMessage,
			Timestamp: time.Now(),
			Data:      AgentMessageData{Text: "Message"},
		}
		if err := store.RecordEvent(sessionID, event); err != nil {
			t.Fatalf("RecordEvent failed for seq %d: %v", i, err)
		}
	}

	// Read back and verify all seqs are preserved
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	if len(events) != 5 {
		t.Fatalf("Expected 5 events, got %d", len(events))
	}

	for i, e := range events {
		expectedSeq := int64(i + 1)
		if e.Seq != expectedSeq {
			t.Errorf("Event %d: seq = %d, want %d", i, e.Seq, expectedSeq)
		}
	}

	// Verify MaxSeq is updated to highest
	gotMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if gotMeta.MaxSeq != 5 {
		t.Errorf("MaxSeq = %d, want 5", gotMeta.MaxSeq)
	}
	if gotMeta.EventCount != 5 {
		t.Errorf("EventCount = %d, want 5", gotMeta.EventCount)
	}
}
