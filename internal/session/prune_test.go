package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPruneConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config PruneConfig
		want   bool
	}{
		{"zero values", PruneConfig{}, false},
		{"only max messages", PruneConfig{MaxMessages: 100}, true},
		{"only max size", PruneConfig{MaxSizeBytes: 1024}, true},
		{"both set", PruneConfig{MaxMessages: 100, MaxSizeBytes: 1024}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStore_PruneIfNeeded_NilConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	result, err := store.PruneIfNeeded("any-session", nil)
	if err != nil {
		t.Errorf("PruneIfNeeded with nil config should not error: %v", err)
	}
	if result != nil {
		t.Errorf("PruneIfNeeded with nil config should return nil result")
	}
}

func TestStore_PruneIfNeeded_DisabledConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	config := &PruneConfig{} // All zeros
	result, err := store.PruneIfNeeded("any-session", config)
	if err != nil {
		t.Errorf("PruneIfNeeded with disabled config should not error: %v", err)
	}
	if result != nil {
		t.Errorf("PruneIfNeeded with disabled config should return nil result")
	}
}

func TestStore_PruneIfNeeded_MessageLimit(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with events
	meta := Metadata{
		SessionID:  "test-prune-msg",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append 10 events
	for i := 0; i < 10; i++ {
		event := Event{
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: "Message " + string(rune('A'+i))},
		}
		if err := store.AppendEvent("test-prune-msg", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Verify 10 events exist
	events, err := store.ReadEvents("test-prune-msg")
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if len(events) != 10 {
		t.Fatalf("Expected 10 events, got %d", len(events))
	}

	// Prune to 5 messages
	config := &PruneConfig{MaxMessages: 5}
	result, err := store.PruneIfNeeded("test-prune-msg", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.EventsRemoved != 5 {
		t.Errorf("EventsRemoved = %d, want 5", result.EventsRemoved)
	}

	// Verify 5 events remain
	events, err = store.ReadEvents("test-prune-msg")
	if err != nil {
		t.Fatalf("ReadEvents after prune failed: %v", err)
	}
	if len(events) != 5 {
		t.Errorf("Expected 5 events after prune, got %d", len(events))
	}

	// Verify sequences are renumbered starting from 1
	for i, event := range events {
		expectedSeq := int64(i + 1)
		if event.Seq != expectedSeq {
			t.Errorf("Event %d has Seq %d, want %d", i, event.Seq, expectedSeq)
		}
	}

	// Verify oldest events were removed (remaining should be F, G, H, I, J)
	if data, ok := events[0].Data.(map[string]interface{}); ok {
		msg := data["message"].(string)
		if msg != "Message F" {
			t.Errorf("First remaining message = %q, want 'Message F'", msg)
		}
	}
}

func TestStore_PruneIfNeeded_UnderLimit(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with events
	meta := Metadata{
		SessionID:  "test-under-limit",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append 3 events
	for i := 0; i < 3; i++ {
		event := Event{
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: "Message"},
		}
		if err := store.AppendEvent("test-under-limit", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Prune with limit of 10 (more than current count)
	config := &PruneConfig{MaxMessages: 10}
	result, err := store.PruneIfNeeded("test-under-limit", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	// Should not prune anything
	if result != nil {
		t.Errorf("Expected nil result when under limit, got EventsRemoved=%d", result.EventsRemoved)
	}

	// Verify all 3 events still exist
	events, err := store.ReadEvents("test-under-limit")
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("Expected 3 events, got %d", len(events))
	}
}

func TestStore_PruneIfNeeded_SizeLimit(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := Metadata{
		SessionID:  "test-size-limit",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append events with substantial content
	for i := 0; i < 10; i++ {
		// Create a message with ~100 bytes of content
		event := Event{
			Type:      EventTypeAgentMessage,
			Timestamp: time.Now(),
			Data:      AgentMessageData{Text: strings.Repeat("x", 100)},
		}
		if err := store.AppendEvent("test-size-limit", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Get current file size
	eventsPath := filepath.Join(tmpDir, "test-size-limit", "events.jsonl")
	info, err := os.Stat(eventsPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	originalSize := info.Size()

	// Prune with a size limit that's about half the current size
	config := &PruneConfig{MaxSizeBytes: originalSize / 2}
	result, err := store.PruneIfNeeded("test-size-limit", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.EventsRemoved == 0 {
		t.Error("Expected some events to be removed")
	}
	if result.BytesReclaimed <= 0 {
		t.Error("Expected some bytes to be reclaimed")
	}

	// Verify file is smaller
	info, err = os.Stat(eventsPath)
	if err != nil {
		t.Fatalf("Stat after prune failed: %v", err)
	}
	if info.Size() >= originalSize {
		t.Errorf("File size after prune (%d) should be smaller than original (%d)", info.Size(), originalSize)
	}
}

func TestStore_PruneIfNeeded_BothLimits(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-both-limits",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append 20 events
	for i := 0; i < 20; i++ {
		event := Event{
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: strings.Repeat("y", 50)},
		}
		if err := store.AppendEvent("test-both-limits", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Get current file size
	eventsPath := filepath.Join(tmpDir, "test-both-limits", "events.jsonl")
	info, _ := os.Stat(eventsPath)
	originalSize := info.Size()

	// Apply both limits: max 10 messages AND max ~half size
	// The more restrictive limit should win
	config := &PruneConfig{
		MaxMessages:  10,
		MaxSizeBytes: originalSize / 2,
	}
	result, err := store.PruneIfNeeded("test-both-limits", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	// Should remove at least 10 events (message limit)
	if result.EventsRemoved < 10 {
		t.Errorf("Expected at least 10 events removed, got %d", result.EventsRemoved)
	}

	// Verify remaining events count
	events, err := store.ReadEvents("test-both-limits")
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if len(events) > 10 {
		t.Errorf("Expected at most 10 events, got %d", len(events))
	}
}

func TestStore_PruneIfNeeded_MetadataUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-metadata-update",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append 10 events
	for i := 0; i < 10; i++ {
		event := Event{
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: "Message"},
		}
		if err := store.AppendEvent("test-metadata-update", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Verify initial event count
	meta1, _ := store.GetMetadata("test-metadata-update")
	if meta1.EventCount != 10 {
		t.Errorf("Initial EventCount = %d, want 10", meta1.EventCount)
	}

	// Prune to 3 messages
	config := &PruneConfig{MaxMessages: 3}
	_, err = store.PruneIfNeeded("test-metadata-update", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	// Verify metadata was updated
	meta2, _ := store.GetMetadata("test-metadata-update")
	if meta2.EventCount != 3 {
		t.Errorf("EventCount after prune = %d, want 3", meta2.EventCount)
	}
}

func TestStore_PruneIfNeeded_KeepsAtLeastOneEvent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-keep-one",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append 5 events with large content
	for i := 0; i < 5; i++ {
		event := Event{
			Type:      EventTypeAgentMessage,
			Timestamp: time.Now(),
			Data:      AgentMessageData{Text: strings.Repeat("large content ", 100)},
		}
		if err := store.AppendEvent("test-keep-one", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Apply extremely small size limit (should not delete all events)
	config := &PruneConfig{MaxSizeBytes: 1} // 1 byte, impossibly small
	_, err = store.PruneIfNeeded("test-keep-one", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	// Verify at least 1 event remains
	events, err := store.ReadEvents("test-keep-one")
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if len(events) < 1 {
		t.Error("Expected at least 1 event to remain")
	}
}

func TestStore_PruneIfNeeded_EmptySession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-empty",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Don't add any events, session is empty

	// Prune should not fail on empty session
	config := &PruneConfig{MaxMessages: 5}
	result, err := store.PruneIfNeeded("test-empty", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed on empty session: %v", err)
	}
	if result != nil {
		t.Errorf("Expected nil result for empty session, got EventsRemoved=%d", result.EventsRemoved)
	}
}

func TestStore_PruneIfNeeded_ClosedStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Close the store
	store.Close()

	// Pruning should return error
	config := &PruneConfig{MaxMessages: 5}
	_, err = store.PruneIfNeeded("any-session", config)
	if err != ErrStoreClosed {
		t.Errorf("Expected ErrStoreClosed, got %v", err)
	}
}

func TestRecorder_WithPruneConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)
	recorder.SetPruneConfig(&PruneConfig{MaxMessages: 5})

	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record 10 user prompts (will trigger pruning on each after the first 5)
	for i := 0; i < 10; i++ {
		if err := recorder.RecordUserPrompt("Message " + string(rune('A'+i))); err != nil {
			t.Fatalf("RecordUserPrompt failed: %v", err)
		}
	}

	// Read events - should have at most 5 after pruning
	// Note: session_start event + up to 5 messages = potentially 6 events
	// But pruning will keep at most 5 total
	events, err := store.ReadEvents(recorder.SessionID())
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	if len(events) > 5 {
		t.Errorf("Expected at most 5 events after pruning, got %d", len(events))
	}

	// End the session
	if err := recorder.End(SessionEndData{Reason: "user_quit"}); err != nil {
		t.Fatalf("End failed: %v", err)
	}
}

func TestRecorder_SetPruneConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := NewRecorder(store)

	// Initially no prune config
	if err := recorder.Start("test-server", "/test/dir"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record some events without pruning
	for i := 0; i < 5; i++ {
		if err := recorder.RecordUserPrompt("Message " + string(rune('A'+i))); err != nil {
			t.Fatalf("RecordUserPrompt failed: %v", err)
		}
	}

	events1, _ := store.ReadEvents(recorder.SessionID())
	initialCount := len(events1)
	if initialCount != 6 { // 1 session_start + 5 messages
		t.Errorf("Expected 6 events before pruning enabled, got %d", initialCount)
	}

	// Now enable pruning with smaller limit
	recorder.SetPruneConfig(&PruneConfig{MaxMessages: 3})

	// Record one more event to trigger pruning
	if err := recorder.RecordUserPrompt("Trigger pruning"); err != nil {
		t.Fatalf("RecordUserPrompt failed: %v", err)
	}

	events2, _ := store.ReadEvents(recorder.SessionID())
	if len(events2) > 3 {
		t.Errorf("Expected at most 3 events after pruning enabled, got %d", len(events2))
	}
}

func TestStore_PruneIfNeeded_ImageCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-image-cleanup",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Save images and create events referencing them
	imageData := []byte("fake-image-data")
	var imageIDs []string

	for i := 0; i < 5; i++ {
		imgInfo, err := store.SaveImage("test-image-cleanup", imageData, "image/png", "test.png")
		if err != nil {
			t.Fatalf("SaveImage failed: %v", err)
		}
		imageIDs = append(imageIDs, imgInfo.ID)

		// Create a user_prompt event with image reference
		event := Event{
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"message": "Message with image",
				"images": []map[string]interface{}{
					{"id": imgInfo.ID},
				},
			},
		}
		if err := store.AppendEvent("test-image-cleanup", event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
	}

	// Verify all images exist
	images, err := store.ListImages("test-image-cleanup")
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}
	if len(images) != 5 {
		t.Errorf("Expected 5 images, got %d", len(images))
	}

	// Prune to keep only 2 events (removing 3 events with images)
	config := &PruneConfig{MaxMessages: 2}
	result, err := store.PruneIfNeeded("test-image-cleanup", config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.EventsRemoved != 3 {
		t.Errorf("EventsRemoved = %d, want 3", result.EventsRemoved)
	}
	if result.ImagesRemoved != 3 {
		t.Errorf("ImagesRemoved = %d, want 3", result.ImagesRemoved)
	}

	// Verify remaining events
	events, err := store.ReadEvents("test-image-cleanup")
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("Expected 2 events after prune, got %d", len(events))
	}

	// Verify only 2 images remain
	images, err = store.ListImages("test-image-cleanup")
	if err != nil {
		t.Fatalf("ListImages after prune failed: %v", err)
	}
	if len(images) != 2 {
		t.Errorf("Expected 2 images after prune, got %d", len(images))
	}

	// The remaining images should be the last 2 (imageIDs[3] and imageIDs[4])
	remainingIDs := make(map[string]bool)
	for _, img := range images {
		remainingIDs[img.ID] = true
	}
	if !remainingIDs[imageIDs[3]] {
		t.Errorf("Expected image %s to remain", imageIDs[3])
	}
	if !remainingIDs[imageIDs[4]] {
		t.Errorf("Expected image %s to remain", imageIDs[4])
	}
}
