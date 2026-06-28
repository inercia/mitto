package session

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// pruneRaceLogCapture is a minimal slog.Handler that records WARN+ messages
// for the concurrent-prune race tests (mitto-yom).
// It is safe for concurrent use.
type pruneRaceLogCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (c *pruneRaceLogCapture) Enabled(_ context.Context, lv slog.Level) bool {
	return lv >= slog.LevelWarn
}

func (c *pruneRaceLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, r.Message)
	c.mu.Unlock()
	return nil
}

// WithAttrs returns the same receiver so that the component-filter wrapper in
// logging.WithComponent keeps routing records here.
func (c *pruneRaceLogCapture) WithAttrs(_ []slog.Attr) slog.Handler { return c }
func (c *pruneRaceLogCapture) WithGroup(_ string) slog.Handler       { return c }

// findWarns returns the subset of captured messages that contain substr.
func (c *pruneRaceLogCapture) findWarns(substr string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.msgs {
		if strings.Contains(m, substr) {
			out = append(out, m)
		}
	}
	return out
}

// reset clears all captured messages.
func (c *pruneRaceLogCapture) reset() {
	c.mu.Lock()
	c.msgs = nil
	c.mu.Unlock()
}

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

	// Verify sequences are PRESERVED across pruning.
	// Original seqs were 1-10; keeping the last 5 means seqs 6,7,8,9,10.
	// Renumbering would break the WebSocket sync protocol (load_events after_seq).
	for i, event := range events {
		expectedSeq := int64(6 + i) // seqs 6,7,8,9,10
		if event.Seq != expectedSeq {
			t.Errorf("Event %d has Seq %d, want %d (seqs must be preserved, not renumbered)", i, event.Seq, expectedSeq)
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

	if err := recorder.Start("test-server", "/test/dir", ""); err != nil {
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
	if err := recorder.Start("test-server", "/test/dir", ""); err != nil {
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

// TestPruneIfNeeded_PreservesMaxSeq verifies that MaxSeq reflects the highest
// seq among the remaining events after pruning (not the event count).
// Seqs are monotonic identifiers stable across pruning — they must never be
// renumbered so that WebSocket sync (load_events after_seq) remains correct.
func TestPruneIfNeeded_PreservesMaxSeq(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-maxseq-reset"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Record 10 events via RecordEvent with seq 1..10 (so MaxSeq = 10)
	for i := 1; i <= 10; i++ {
		event := Event{
			Seq:       int64(i),
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: "Message " + string(rune('A'+i-1))},
		}
		if err := store.RecordEvent(sessionID, event); err != nil {
			t.Fatalf("RecordEvent %d failed: %v", i, err)
		}
	}

	// Verify initial state: EventCount = 10, MaxSeq = 10
	metaBefore, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata before prune failed: %v", err)
	}
	if metaBefore.EventCount != 10 {
		t.Errorf("EventCount before prune = %d, want 10", metaBefore.EventCount)
	}
	if metaBefore.MaxSeq != 10 {
		t.Errorf("MaxSeq before prune = %d, want 10", metaBefore.MaxSeq)
	}

	// Prune to 5 events
	config := &PruneConfig{MaxMessages: 5}
	result, err := store.PruneIfNeeded(sessionID, config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.EventsRemoved != 5 {
		t.Errorf("EventsRemoved = %d, want 5", result.EventsRemoved)
	}

	// Assert: meta.EventCount == 5
	metaAfter, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata after prune failed: %v", err)
	}
	if metaAfter.EventCount != 5 {
		t.Errorf("EventCount after prune = %d, want 5", metaAfter.EventCount)
	}

	// Assert: meta.MaxSeq == 10 (highest seq among the kept events 6-10)
	if metaAfter.MaxSeq != 10 {
		t.Errorf("MaxSeq after prune = %d, want 10 (highest seq among remaining events)", metaAfter.MaxSeq)
	}

	// Read events and verify they are PRESERVED as seqs 6..10 (not renumbered 1..5)
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents after prune failed: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("Expected 5 events after prune, got %d", len(events))
	}
	for i, event := range events {
		expectedSeq := int64(6 + i) // seqs 6,7,8,9,10
		if event.Seq != expectedSeq {
			t.Errorf("Event %d has Seq %d, want %d (seqs must be preserved, not renumbered)", i, event.Seq, expectedSeq)
		}
	}
}

// TestPruneIfNeeded_RecordEventAfterPrune_NoMismatch verifies that RecordEvent
// works correctly after pruning without seq mismatch errors.
func TestPruneIfNeeded_RecordEventAfterPrune_NoMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-record-after-prune"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Record 10 events via RecordEvent with seq 1..10
	for i := 1; i <= 10; i++ {
		event := Event{
			Seq:       int64(i),
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: "Message " + string(rune('A'+i-1))},
		}
		if err := store.RecordEvent(sessionID, event); err != nil {
			t.Fatalf("RecordEvent %d failed: %v", i, err)
		}
	}

	// Prune to 5 events
	config := &PruneConfig{MaxMessages: 5}
	_, err = store.PruneIfNeeded(sessionID, config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	// Verify state after prune
	metaAfterPrune, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata after prune failed: %v", err)
	}
	if metaAfterPrune.EventCount != 5 {
		t.Errorf("EventCount after prune = %d, want 5", metaAfterPrune.EventCount)
	}
	// After preserving seqs: remaining events have seqs 6-10, so MaxSeq = 10
	if metaAfterPrune.MaxSeq != 10 {
		t.Errorf("MaxSeq after prune = %d, want 10 (highest seq among remaining events 6-10)", metaAfterPrune.MaxSeq)
	}

	// Record a new event via RecordEvent with seq = 11 (max(EventCount=5, MaxSeq=10)+1 = 11)
	newEvent := Event{
		Seq:       11,
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      UserPromptData{Message: "New message after prune"},
	}
	if err := store.RecordEvent(sessionID, newEvent); err != nil {
		t.Fatalf("RecordEvent after prune failed: %v (MaxSeq should be 10 after prune, so next seq is 11)", err)
	}

	// Assert: meta.EventCount == 6, meta.MaxSeq == 11
	metaFinal, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata after new event failed: %v", err)
	}
	if metaFinal.EventCount != 6 {
		t.Errorf("EventCount after new event = %d, want 6", metaFinal.EventCount)
	}
	if metaFinal.MaxSeq != 11 {
		t.Errorf("MaxSeq after new event = %d, want 11", metaFinal.MaxSeq)
	}

	// Verify events are preserved seqs 6-10 plus new seq 11 (NOT renumbered 1-6)
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents after new event failed: %v", err)
	}
	if len(events) != 6 {
		t.Fatalf("Expected 6 events, got %d", len(events))
	}
	expectedSeqs := []int64{6, 7, 8, 9, 10, 11}
	for i, event := range events {
		if event.Seq != expectedSeqs[i] {
			t.Errorf("Event %d has Seq %d, want %d (seqs must be preserved)", i, event.Seq, expectedSeqs[i])
		}
	}
}

// TestPruneIfNeeded_MaxSeqMatchesKeptEvents verifies that MaxSeq reflects the
// highest seq among kept events after pruning. When only 1 event with seq=1
// is kept, MaxSeq must be 1 (its preserved seq). The implementation currently
// keeps at least 1 event even with MaxMessages: 0.
func TestPruneIfNeeded_MaxSeqMatchesKeptEvents(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-maxseq-matches-kept"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create 1 event
	event := Event{
		Seq:       1,
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      UserPromptData{Message: "Single message"},
	}
	if err := store.RecordEvent(sessionID, event); err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	// Verify initial state
	metaBefore, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata before prune failed: %v", err)
	}
	if metaBefore.EventCount != 1 {
		t.Errorf("EventCount before prune = %d, want 1", metaBefore.EventCount)
	}
	if metaBefore.MaxSeq != 1 {
		t.Errorf("MaxSeq before prune = %d, want 1", metaBefore.MaxSeq)
	}

	// Prune with MaxMessages: 0
	// The implementation keeps at least 1 event (see TestStore_PruneIfNeeded_KeepsAtLeastOneEvent)
	config := &PruneConfig{MaxMessages: 0}
	result, err := store.PruneIfNeeded(sessionID, config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}

	// Verify MaxSeq matches the number of kept events
	metaAfter, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata after prune failed: %v", err)
	}

	// The implementation keeps at least 1 event, so we expect EventCount = 1, MaxSeq = 1
	if metaAfter.EventCount != 1 {
		t.Errorf("EventCount after prune = %d, want 1 (implementation keeps at least 1 event)", metaAfter.EventCount)
	}
	if metaAfter.MaxSeq != 1 {
		t.Errorf("MaxSeq after prune = %d, want 1 (should match the kept event)", metaAfter.MaxSeq)
	}
	// Result may be nil if no events were actually removed (trying to prune to 0 but keeping 1)
	if result != nil && result.EventsRemoved != 0 {
		t.Errorf("EventsRemoved = %d, want 0 (no events should be removed when already at minimum)", result.EventsRemoved)
	}
	t.Logf("Note: Implementation keeps at least 1 event even with MaxMessages: 0")
}

// TestRecorder_ConcurrentRecord_DoesNotRaceOnPrune tests concurrent
// RecordUserPrompt calls with pruning enabled for the events.jsonl.tmp
// rename race described in mitto-yom.
//
// # Scenario A – Single Store (no race expected)
//
// Within one process, Store.mu (sync.RWMutex) serializes every PruneIfNeeded
// call. Even with many goroutines sharing different Recorders on the same
// Store, only one goroutine enters performPrune at a time → race CANNOT occur.
//
// # Scenario B – Two Stores, same baseDir (race expected)
//
// Two Store instances on the same directory simulate two concurrent Mitto
// processes. Each Store has its own mu, so concurrent performPrune calls can
// interleave on the shared events.jsonl.tmp:
//
//	Store1 creates .tmp → Store2 overwrites .tmp → Store1 renames .tmp→events.jsonl
//	→ Store2 tries rename → ENOENT → WARN "failed to prune session after recording event"
//
// This reproduces the production WARN seen in mitto-yom.
func TestRecorder_ConcurrentRecord_DoesNotRaceOnPrune(t *testing.T) {
	const (
		numGoroutines = 8
		eventsEach    = 50
		pruneMax      = 50
	)

	// Override slog default so that logging.Session() → logging.Get() →
	// slog.Default() routes WARN records to our capturer.
	// (In the test binary, globalLogger is nil — no logging.Initialize call —
	// so Get() falls back to slog.Default().)
	cap := &pruneRaceLogCapture{}
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })

	// assertWellFormed checks that events.jsonl is parseable and seq is
	// non-decreasing. It uses the store's own ReadEvents to stay consistent
	// with prune semantics. checkMonotonic should be false when two Stores
	// write concurrently (seq collisions are expected there).
	assertWellFormed := func(t *testing.T, store *Store, sessionID string, checkMonotonic bool, maxEvents int) {
		t.Helper()
		events, err := store.ReadEvents(sessionID)
		if err != nil {
			t.Errorf("ReadEvents failed: %v", err)
			return
		}
		if checkMonotonic {
			var prevSeq int64
			for i, ev := range events {
				if ev.Seq <= prevSeq {
					t.Errorf("seq not monotonic at index %d: got seq %d after %d", i, ev.Seq, prevSeq)
				}
				prevSeq = ev.Seq
			}
		}
		t.Logf("events.jsonl: %d events", len(events))
		if maxEvents > 0 && len(events) > maxEvents {
			t.Errorf("event count %d exceeds expected max %d", len(events), maxEvents)
		}
	}

	// ── Scenario A: Single Store ──────────────────────────────────────────────
	// Store.mu serializes all PruneIfNeeded calls; race is structurally impossible.
	// This sub-test must always pass.
	t.Run("SingleStore_NoRaceExpected", func(t *testing.T) {
		cap.reset()

		tmpDir := t.TempDir()
		store, err := NewStore(tmpDir)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		defer store.Close()

		recorder := NewRecorder(store)
		recorder.SetPruneConfig(&PruneConfig{MaxMessages: pruneMax})
		if err := recorder.Start("test-server", "/test/dir", ""); err != nil {
			t.Fatalf("Start: %v", err)
		}

		var (
			wg      sync.WaitGroup
			errMu   sync.Mutex
			errList []error
		)
		for g := 0; g < numGoroutines; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < eventsEach; i++ {
					if err := recorder.RecordUserPrompt(fmt.Sprintf("g%d-msg%d", id, i)); err != nil {
						errMu.Lock()
						errList = append(errList, err)
						errMu.Unlock()
					}
				}
			}(g)
		}
		wg.Wait()

		// (a) zero errors from RecordUserPrompt
		for _, e := range errList {
			t.Errorf("SingleStore: RecordUserPrompt error: %v", e)
		}
		// (b) zero "failed to prune" WARN lines — Store.mu serializes all prune ops
		pruneWarns := cap.findWarns("failed to prune")
		if len(pruneWarns) > 0 {
			t.Errorf("SingleStore: %d unexpected 'failed to prune' WARN(s): %v", len(pruneWarns), pruneWarns)
		} else {
			t.Logf("SingleStore: 0 'failed to prune' WARNs — race absent as expected (Store.mu serializes)")
		}
		// (c) events.jsonl is well-formed with monotonic seq
		assertWellFormed(t, store, recorder.SessionID(), true /* monotonic */, pruneMax+numGoroutines)
	})

	// ── Scenario B: Two Stores, same baseDir ─────────────────────────────────
	// Simulates two concurrent Mitto processes sharing the same session dir.
	// With the unique-tmp fix applied to both WriteJSONAtomic (metadata.json) and
	// performPrune (events.jsonl), concurrent renames no longer collide:
	//   each caller writes to its own .<pid>.<counter>.tmp file.
	// This sub-test must PASS with the fix in place (regression guard for mitto-yom).
	t.Run("TwoStores_NoRaceWithUniqueTmp", func(t *testing.T) {
		cap.reset()

		tmpDir := t.TempDir()
		store1, err := NewStore(tmpDir)
		if err != nil {
			t.Fatalf("NewStore1: %v", err)
		}
		defer store1.Close()

		// Create the session via store1 / rec1.
		rec1 := NewRecorder(store1)
		rec1.SetPruneConfig(&PruneConfig{MaxMessages: pruneMax})
		if err := rec1.Start("test-server", "/test/dir", ""); err != nil {
			t.Fatalf("rec1.Start: %v", err)
		}
		sessionID := rec1.SessionID()

		// Open a SECOND Store on the same directory — simulates a second process.
		store2, err := NewStore(tmpDir)
		if err != nil {
			t.Fatalf("NewStore2: %v", err)
		}
		defer store2.Close()

		rec2 := NewRecorderWithID(store2, sessionID)
		rec2.SetPruneConfig(&PruneConfig{MaxMessages: pruneMax})
		if err := rec2.Resume(); err != nil {
			t.Fatalf("rec2.Resume: %v", err)
		}

		var (
			wg      sync.WaitGroup
			errMu   sync.Mutex
			errList []error
		)
		recorders := []*Recorder{rec1, rec2}
		for g := 0; g < numGoroutines; g++ {
			wg.Add(1)
			rec := recorders[g%2]
			go func(rec *Recorder, id int) {
				defer wg.Done()
				for i := 0; i < eventsEach; i++ {
					if err := rec.RecordUserPrompt(fmt.Sprintf("g%d-msg%d", id, i)); err != nil {
						errMu.Lock()
						errList = append(errList, err)
						errMu.Unlock()
					}
				}
			}(rec, g)
		}
		wg.Wait()

		// (a) zero errors from RecordUserPrompt — unique-tmp fix eliminates
		// the metadata.json corruption that caused "failed to parse JSON" errors.
		for _, e := range errList {
			t.Errorf("TwoStores: RecordUserPrompt error: %v", e)
		}
		// (b) zero "failed to prune" WARNs — unique-tmp fix eliminates ENOENT on rename.
		pruneWarns := cap.findWarns("failed to prune")
		if len(pruneWarns) > 0 {
			t.Errorf("TwoStores: %d 'failed to prune' WARN(s) after fix — rename race still present: %v",
				len(pruneWarns), pruneWarns)
		} else {
			t.Logf("TwoStores: 0 'failed to prune' WARNs — unique-tmp fix working")
		}
		// (c) events.jsonl is parseable; seq monotonicity may not hold across two
		// independent Stores (metadata EventCount races remain) but the file must
		// be valid JSONL and within a generous bound.
		assertWellFormed(t, store1, sessionID, false /* two-store seq collision possible */, 0)
	})
}

// TestStore_PruneIfNeeded_PreservesSeqs is the primary regression test for the
// pruning-renumbering bug. It verifies that:
//  1. After pruning events 1-10 to keep 5, the remaining seqs are 6-10 (not 1-5).
//  2. A new event recorded after pruning gets seq=11 (not seq=6).
//  3. ReadEventsFrom(5) returns the 5 preserved events with seqs 6-10.
//
// Before the fix, performPrune renumbered seqs starting from 1, which broke
// the WebSocket sync protocol: load_events(after_seq=N) returned nothing because
// the file's max_seq was lower than what the client expected.
func TestStore_PruneIfNeeded_PreservesSeqs(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-preserves-seqs"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Step 1: Record 10 events with seqs 1-10.
	for i := 1; i <= 10; i++ {
		event := Event{
			Seq:       int64(i),
			Type:      EventTypeUserPrompt,
			Timestamp: time.Now(),
			Data:      UserPromptData{Message: "Message " + string(rune('A'+i-1))},
		}
		if err := store.RecordEvent(sessionID, event); err != nil {
			t.Fatalf("RecordEvent %d failed: %v", i, err)
		}
	}

	// Step 2: Prune to keep only 5 events.
	config := &PruneConfig{MaxMessages: 5}
	result, err := store.PruneIfNeeded(sessionID, config)
	if err != nil {
		t.Fatalf("PruneIfNeeded failed: %v", err)
	}
	if result == nil || result.EventsRemoved != 5 {
		t.Fatalf("Expected 5 events removed, got %v", result)
	}

	// Step 3: Verify remaining events have seqs [6, 7, 8, 9, 10] — NOT [1, 2, 3, 4, 5].
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents after prune failed: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("Expected 5 events after prune, got %d", len(events))
	}
	for i, ev := range events {
		wantSeq := int64(6 + i)
		if ev.Seq != wantSeq {
			t.Errorf("Event[%d].Seq = %d, want %d (seqs must be preserved, not renumbered)", i, ev.Seq, wantSeq)
		}
	}
	metaAfterPrune, _ := store.GetMetadata(sessionID)
	if metaAfterPrune.MaxSeq != 10 {
		t.Errorf("MaxSeq after prune = %d, want 10 (highest seq among remaining events)", metaAfterPrune.MaxSeq)
	}
	if metaAfterPrune.EventCount != 5 {
		t.Errorf("EventCount after prune = %d, want 5", metaAfterPrune.EventCount)
	}

	// Step 4: Record another event and verify it gets seq=11 (NOT seq=6).
	newEvent := Event{
		Seq:       11,
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      UserPromptData{Message: "Post-prune event"},
	}
	if err := store.RecordEvent(sessionID, newEvent); err != nil {
		t.Fatalf("RecordEvent after prune failed: %v (seq 11 should be valid since MaxSeq=10)", err)
	}
	metaAfterNew, _ := store.GetMetadata(sessionID)
	if metaAfterNew.MaxSeq != 11 {
		t.Errorf("MaxSeq after new event = %d, want 11", metaAfterNew.MaxSeq)
	}

	// Step 5: ReadEventsFrom(5) should return the 5 preserved events with seqs 6-10.
	// (The new seq=11 event is excluded by the limit, but seqs 6-10 must be present.)
	fromSix, err := store.ReadEventsFrom(sessionID, 5, 0)
	if err != nil {
		t.Fatalf("ReadEventsFrom(5) failed: %v", err)
	}
	if len(fromSix) != 6 { // seqs 6,7,8,9,10,11
		t.Errorf("ReadEventsFrom(5) returned %d events, want 6 (seqs 6-11)", len(fromSix))
	}
	if len(fromSix) >= 1 && fromSix[0].Seq != 6 {
		t.Errorf("ReadEventsFrom(5)[0].Seq = %d, want 6", fromSix[0].Seq)
	}
}
