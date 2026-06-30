package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockBeadsSubscriber implements BeadsSubscriber for testing.
type mockBeadsSubscriber struct {
	mu       sync.Mutex
	events   []BeadsChangeEvent
	notified chan struct{}
}

func newMockBeadsSubscriber() *mockBeadsSubscriber {
	return &mockBeadsSubscriber{
		notified: make(chan struct{}, 10),
	}
}

func (m *mockBeadsSubscriber) OnBeadsChanged(event BeadsChangeEvent) {
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()

	select {
	case m.notified <- struct{}{}:
	default:
	}
}

func (m *mockBeadsSubscriber) EventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func (m *mockBeadsSubscriber) LastEvent() BeadsChangeEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return BeadsChangeEvent{}
	}
	return m.events[len(m.events)-1]
}

func (m *mockBeadsSubscriber) WaitForEvent(timeout time.Duration) bool {
	select {
	case <-m.notified:
		return true
	case <-time.After(timeout):
		return false
	}
}

func TestBeadsWatcher_BasicChange_LastTouched(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("Failed to create .beads dir: %v", err)
	}

	bw, err := NewBeadsWatcher(nil)
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}
	defer bw.Close()

	bw.SetDebounceDelay(20 * time.Millisecond)
	bw.Start()

	sub := newMockBeadsSubscriber()
	if err := bw.Subscribe(sub, []string{beadsDir}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Write the last-touched file — the canonical trigger.
	ltPath := filepath.Join(beadsDir, "last-touched")
	if err := os.WriteFile(ltPath, []byte("1"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !sub.WaitForEvent(2 * time.Second) {
		t.Fatal("Timed out waiting for beads_changed event")
	}

	event := sub.LastEvent()
	if len(event.ChangedDirs) == 0 {
		t.Error("Expected ChangedDirs to be populated")
	}
	if len(event.WorkingDirs) == 0 {
		t.Error("Expected WorkingDirs to be populated")
	}
	// WorkingDirs must be the workspace root (parent of .beads/).
	if event.WorkingDirs[0] != tmpDir {
		t.Errorf("Expected working_dir %q, got %q", tmpDir, event.WorkingDirs[0])
	}
}

func TestBeadsWatcher_NotYetExistingBeadsDir(t *testing.T) {
	// Watch parent; create .beads/ and last-touched later → expect event.
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	// Do NOT create beadsDir yet.

	bw, err := NewBeadsWatcher(nil)
	if err != nil {
		t.Fatalf("NewBeadsWatcher: %v", err)
	}
	defer bw.Close()

	bw.SetDebounceDelay(20 * time.Millisecond)
	bw.Start()

	sub := newMockBeadsSubscriber()
	if err := bw.Subscribe(sub, []string{beadsDir}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Subscriber registered; no panic even though .beads/ doesn't exist.
	if bw.SubscriberCount() != 1 {
		t.Errorf("Expected 1 subscriber, got %d", bw.SubscriberCount())
	}

	// Now create .beads/ and trigger a file write.
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Give fsnotify a moment to detect the directory creation.
	time.Sleep(50 * time.Millisecond)

	ltPath := filepath.Join(beadsDir, "last-touched")
	if err := os.WriteFile(ltPath, []byte("1"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !sub.WaitForEvent(3 * time.Second) {
		t.Fatal("Timed out waiting for event after .beads dir was created")
	}
}

func TestBeadsWatcher_Unsubscribe_RemovesWatch(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	bw, err := NewBeadsWatcher(nil)
	if err != nil {
		t.Fatalf("NewBeadsWatcher: %v", err)
	}
	defer bw.Close()
	bw.Start()

	sub := newMockBeadsSubscriber()
	if err := bw.Subscribe(sub, []string{beadsDir}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if bw.WatchedDirCount() != 1 {
		t.Errorf("Expected 1 watched dir, got %d", bw.WatchedDirCount())
	}

	bw.Unsubscribe(sub)

	if bw.SubscriberCount() != 0 {
		t.Errorf("Expected 0 subscribers, got %d", bw.SubscriberCount())
	}
	if bw.WatchedDirCount() != 0 {
		t.Errorf("Expected 0 watched dirs, got %d", bw.WatchedDirCount())
	}
}

func TestBeadsWatcher_WorkingDirsMapping(t *testing.T) {
	// WorkingDirs in the event must be the workspace roots (parent of .beads/).
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	bw, err := NewBeadsWatcher(nil)
	if err != nil {
		t.Fatalf("NewBeadsWatcher: %v", err)
	}
	defer bw.Close()
	bw.SetDebounceDelay(20 * time.Millisecond)
	bw.Start()

	sub := newMockBeadsSubscriber()
	if err := bw.Subscribe(sub, []string{beadsDir}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ltPath := filepath.Join(beadsDir, "last-touched")
	if err := os.WriteFile(ltPath, []byte("t"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !sub.WaitForEvent(2 * time.Second) {
		t.Fatal("Timed out")
	}

	event := sub.LastEvent()
	if len(event.WorkingDirs) != 1 || event.WorkingDirs[0] != tmpDir {
		t.Errorf("WorkingDirs: want [%q], got %v", tmpDir, event.WorkingDirs)
	}
}

func TestBeadsWatcher_ConcurrentSubscribes(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	bw, err := NewBeadsWatcher(nil)
	if err != nil {
		t.Fatalf("NewBeadsWatcher: %v", err)
	}
	defer bw.Close()
	bw.Start()

	var wg sync.WaitGroup
	var subscribed int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := newMockBeadsSubscriber()
			if err := bw.Subscribe(sub, []string{beadsDir}); err != nil {
				t.Errorf("Subscribe: %v", err)
				return
			}
			atomic.AddInt32(&subscribed, 1)
			time.Sleep(5 * time.Millisecond)
			bw.Unsubscribe(sub)
		}()
	}
	wg.Wait()

	if atomic.LoadInt32(&subscribed) != 20 {
		t.Errorf("Expected 20 subscribes, got %d", subscribed)
	}
	if bw.SubscriberCount() != 0 {
		t.Errorf("Expected 0 subscribers, got %d", bw.SubscriberCount())
	}
}

func TestBeadsWatcher_Debounce(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	bw, err := NewBeadsWatcher(nil)
	if err != nil {
		t.Fatalf("NewBeadsWatcher: %v", err)
	}
	defer bw.Close()

	bw.SetDebounceDelay(60 * time.Millisecond)
	bw.Start()

	sub := newMockBeadsSubscriber()
	if err := bw.Subscribe(sub, []string{beadsDir}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Rapid writes should coalesce into ≤2 events.
	ltPath := filepath.Join(beadsDir, "last-touched")
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(ltPath, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	count := sub.EventCount()
	if count == 0 {
		t.Error("Expected at least one event")
	}
	if count > 3 {
		t.Errorf("Expected debouncing to reduce events, got %d", count)
	}
}

func TestBeadsWatcher_MaxWait_FiresDuringSustainedActivity(t *testing.T) {
	// Under a continuous stream of writes (each within the trailing debounce
	// window), a pure trailing debounce would never fire. The maxWait cap must
	// force a notification mid-stream so subscribers aren't starved.
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	bw, err := NewBeadsWatcher(nil)
	if err != nil {
		t.Fatalf("NewBeadsWatcher: %v", err)
	}
	defer bw.Close()

	bw.SetDebounceDelay(80 * time.Millisecond)
	bw.SetMaxWait(150 * time.Millisecond)
	bw.Start()

	sub := newMockBeadsSubscriber()
	if err := bw.Subscribe(sub, []string{beadsDir}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Write continuously every 20ms (well under the 80ms trailing delay) for
	// 600ms. The writes never pause long enough for the trailing timer to
	// elapse, so only the maxWait cap can trigger a notification.
	stop := make(chan struct{})
	done := make(chan struct{})
	ltPath := filepath.Join(beadsDir, "last-touched")
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				i++
				_ = os.WriteFile(ltPath, []byte{byte(i)}, 0644)
			}
		}
	}()

	// An event must arrive while writes are still ongoing (i.e. well before the
	// 600ms write loop finishes), proving the cap fired mid-stream.
	got := sub.WaitForEvent(400 * time.Millisecond)
	close(stop)
	<-done
	if !got {
		t.Fatal("Expected a maxWait-capped event during sustained writes, got none")
	}
}
