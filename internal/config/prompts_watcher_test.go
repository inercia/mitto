package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockSubscriber implements PromptsSubscriber for testing.
type mockSubscriber struct {
	mu       sync.Mutex
	events   []PromptsChangeEvent
	notified chan struct{}
}

func newMockSubscriber() *mockSubscriber {
	return &mockSubscriber{
		notified: make(chan struct{}, 10),
	}
}

func (m *mockSubscriber) OnPromptsChanged(event PromptsChangeEvent) {
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()

	select {
	case m.notified <- struct{}{}:
	default:
	}
}

func (m *mockSubscriber) EventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func (m *mockSubscriber) LastEvent() PromptsChangeEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return PromptsChangeEvent{}
	}
	return m.events[len(m.events)-1]
}

func (m *mockSubscriber) WaitForEvent(timeout time.Duration) bool {
	select {
	case <-m.notified:
		return true
	case <-time.After(timeout):
		return false
	}
}

func TestPromptsWatcher_BasicUsage(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Create watcher
	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	// Use short debounce for testing
	pw.SetDebounceDelay(20 * time.Millisecond)

	// Start the watcher
	pw.Start()

	// Create subscriber and subscribe
	sub := newMockSubscriber()
	if err := pw.Subscribe(sub, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Create a .md file
	mdFile := filepath.Join(tmpDir, "test.md")
	if err := os.WriteFile(mdFile, []byte("# Test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Wait for notification
	if !sub.WaitForEvent(2 * time.Second) {
		t.Fatal("Timed out waiting for prompts changed event")
	}

	// Verify event
	if sub.EventCount() == 0 {
		t.Fatal("Expected at least one event")
	}
	event := sub.LastEvent()
	if len(event.ChangedDirs) == 0 {
		t.Error("Expected changed_dirs to be populated")
	}
}

func TestPromptsWatcher_Debouncing(t *testing.T) {
	tmpDir := t.TempDir()

	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	// Use short debounce for testing
	pw.SetDebounceDelay(50 * time.Millisecond)
	pw.Start()

	sub := newMockSubscriber()
	if err := pw.Subscribe(sub, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Create multiple files rapidly
	for i := 0; i < 5; i++ {
		mdFile := filepath.Join(tmpDir, "test"+string(rune('0'+i))+".md")
		if err := os.WriteFile(mdFile, []byte("# Test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Wait for debounce to settle
	time.Sleep(150 * time.Millisecond)

	// Should have received only 1-2 events due to debouncing
	// (exact number depends on timing, but should be much less than 5)
	count := sub.EventCount()
	if count > 3 {
		t.Errorf("Expected debouncing to reduce events, got %d events", count)
	}
	if count == 0 {
		t.Error("Expected at least one event")
	}
}

func TestPromptsWatcher_MultipleSubscribers(t *testing.T) {
	tmpDir := t.TempDir()

	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	pw.SetDebounceDelay(20 * time.Millisecond)
	pw.Start()

	// Create multiple subscribers
	sub1 := newMockSubscriber()
	sub2 := newMockSubscriber()

	if err := pw.Subscribe(sub1, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe sub1: %v", err)
	}
	if err := pw.Subscribe(sub2, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe sub2: %v", err)
	}

	if pw.SubscriberCount() != 2 {
		t.Errorf("Expected 2 subscribers, got %d", pw.SubscriberCount())
	}

	// Should only have one watch for the directory
	if pw.WatchedDirCount() != 1 {
		t.Errorf("Expected 1 watched dir (shared), got %d", pw.WatchedDirCount())
	}

	// Create a file
	mdFile := filepath.Join(tmpDir, "shared.md")
	if err := os.WriteFile(mdFile, []byte("# Shared"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Both subscribers should be notified
	if !sub1.WaitForEvent(2 * time.Second) {
		t.Fatal("sub1 timed out waiting for event")
	}
	if !sub2.WaitForEvent(2 * time.Second) {
		t.Fatal("sub2 timed out waiting for event")
	}

	// Verify both got events
	if sub1.EventCount() == 0 {
		t.Error("sub1 expected events")
	}
	if sub2.EventCount() == 0 {
		t.Error("sub2 expected events")
	}
}

func TestPromptsWatcher_Unsubscribe(t *testing.T) {
	tmpDir := t.TempDir()

	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	pw.SetDebounceDelay(20 * time.Millisecond)
	pw.Start()

	sub := newMockSubscriber()
	if err := pw.Subscribe(sub, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Verify subscription
	if pw.SubscriberCount() != 1 {
		t.Errorf("Expected 1 subscriber, got %d", pw.SubscriberCount())
	}

	// Unsubscribe
	pw.Unsubscribe(sub)

	if pw.SubscriberCount() != 0 {
		t.Errorf("Expected 0 subscribers after unsubscribe, got %d", pw.SubscriberCount())
	}

	// Watch should be removed since no more subscribers
	if pw.WatchedDirCount() != 0 {
		t.Errorf("Expected 0 watched dirs after unsubscribe, got %d", pw.WatchedDirCount())
	}
}

func TestPromptsWatcher_RefCounting(t *testing.T) {
	tmpDir := t.TempDir()

	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	pw.Start()

	sub1 := newMockSubscriber()
	sub2 := newMockSubscriber()

	// Both subscribe to same directory
	if err := pw.Subscribe(sub1, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe sub1: %v", err)
	}
	if err := pw.Subscribe(sub2, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe sub2: %v", err)
	}

	// Only one watch
	if pw.WatchedDirCount() != 1 {
		t.Errorf("Expected 1 watch, got %d", pw.WatchedDirCount())
	}

	// Unsubscribe first subscriber
	pw.Unsubscribe(sub1)

	// Watch should still exist
	if pw.WatchedDirCount() != 1 {
		t.Errorf("Expected 1 watch after first unsubscribe, got %d", pw.WatchedDirCount())
	}

	// Unsubscribe second subscriber
	pw.Unsubscribe(sub2)

	// Now watch should be removed
	if pw.WatchedDirCount() != 0 {
		t.Errorf("Expected 0 watches after all unsubscribed, got %d", pw.WatchedDirCount())
	}
}

func TestPromptsWatcher_OnlyMDFiles(t *testing.T) {
	tmpDir := t.TempDir()

	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	pw.SetDebounceDelay(20 * time.Millisecond)
	pw.Start()

	sub := newMockSubscriber()
	if err := pw.Subscribe(sub, []string{tmpDir}); err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Create a non-.md file
	txtFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(txtFile, []byte("text"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Wait a bit - should NOT receive notification
	time.Sleep(100 * time.Millisecond)

	if sub.EventCount() > 0 {
		t.Errorf("Should not notify for .txt files, got %d events", sub.EventCount())
	}

	// Now create a .md file
	mdFile := filepath.Join(tmpDir, "test.md")
	if err := os.WriteFile(mdFile, []byte("# Test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should receive notification
	if !sub.WaitForEvent(2 * time.Second) {
		t.Fatal("Timed out waiting for .md file event")
	}
}

func TestPromptsWatcher_NonExistentDirectory(t *testing.T) {
	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	pw.Start()

	sub := newMockSubscriber()

	// Subscribe to non-existent directory (should not fail, just log warning)
	nonExistent := filepath.Join(t.TempDir(), "does-not-exist")
	err = pw.Subscribe(sub, []string{nonExistent})
	if err != nil {
		t.Fatalf("Subscribe should not fail for non-existent dir: %v", err)
	}

	// Should still be subscribed
	if pw.SubscriberCount() != 1 {
		t.Errorf("Expected 1 subscriber, got %d", pw.SubscriberCount())
	}
}

func TestPromptsWatcher_ConcurrentSubscribes(t *testing.T) {
	tmpDir := t.TempDir()

	pw, err := NewPromptsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer pw.Close()

	pw.Start()

	// Concurrent subscribes and unsubscribes
	var wg sync.WaitGroup
	var subscribed int32

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := newMockSubscriber()
			if err := pw.Subscribe(sub, []string{tmpDir}); err != nil {
				t.Errorf("Subscribe failed: %v", err)
				return
			}
			atomic.AddInt32(&subscribed, 1)
			time.Sleep(10 * time.Millisecond)
			pw.Unsubscribe(sub)
		}()
	}

	wg.Wait()

	// All should have successfully subscribed
	if atomic.LoadInt32(&subscribed) != 20 {
		t.Errorf("Expected 20 successful subscribes, got %d", subscribed)
	}

	// After all unsubscribes, should have 0 subscribers
	if pw.SubscriberCount() != 0 {
		t.Errorf("Expected 0 subscribers, got %d", pw.SubscriberCount())
	}
}
