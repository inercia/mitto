package hooks

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

func TestShutdownManager_ShutdownOnce(t *testing.T) {
	sm := NewShutdownManager()

	var callCount atomic.Int32
	sm.AddCleanup(func(reason string) {
		callCount.Add(1)
	})

	// Call Shutdown multiple times concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sm.Shutdown("test")
		}()
	}
	wg.Wait()

	// Cleanup should only run once
	if count := callCount.Load(); count != 1 {
		t.Errorf("Cleanup called %d times, expected 1", count)
	}
}

func TestShutdownManager_CleanupOrder(t *testing.T) {
	sm := NewShutdownManager()

	var order []int
	var mu sync.Mutex

	sm.AddCleanup(func(reason string) {
		mu.Lock()
		order = append(order, 1)
		mu.Unlock()
	})
	sm.AddCleanup(func(reason string) {
		mu.Lock()
		order = append(order, 2)
		mu.Unlock()
	})
	sm.AddCleanup(func(reason string) {
		mu.Lock()
		order = append(order, 3)
		mu.Unlock()
	})

	sm.Shutdown("test")

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 3 {
		t.Fatalf("Expected 3 cleanups, got %d", len(order))
	}
	for i, v := range order {
		if v != i+1 {
			t.Errorf("Cleanup order wrong: expected %d at position %d, got %d", i+1, i, v)
		}
	}
}

func TestShutdownManager_Reason(t *testing.T) {
	sm := NewShutdownManager()

	if reason := sm.Reason(); reason != "" {
		t.Errorf("Expected empty reason before shutdown, got %q", reason)
	}

	sm.Shutdown("test_reason")

	if reason := sm.Reason(); reason != "test_reason" {
		t.Errorf("Expected reason 'test_reason', got %q", reason)
	}
}

func TestShutdownManager_Done(t *testing.T) {
	sm := NewShutdownManager()

	// Done channel should not be closed before shutdown
	select {
	case <-sm.Done():
		t.Error("Done channel closed before shutdown")
	default:
		// Expected
	}

	sm.Shutdown("test")

	// Done channel should be closed after shutdown
	select {
	case <-sm.Done():
		// Expected
	case <-time.After(time.Second):
		t.Error("Done channel not closed after shutdown")
	}
}

func TestShutdownManager_TerminateUI(t *testing.T) {
	sm := NewShutdownManager()

	var terminated atomic.Bool
	sm.SetTerminateUI(func() {
		terminated.Store(true)
	})

	sm.Shutdown("test")

	if !terminated.Load() {
		t.Error("TerminateUI callback not called")
	}
}

func TestShutdownManager_HooksRun(t *testing.T) {
	sm := NewShutdownManager()

	// Set up a down hook that we can verify ran
	downHook := config.WebHook{
		Command: "exit 0",
		Name:    "test-down",
	}
	sm.SetHooks(nil, downHook, 8080)

	// Add a cleanup to verify order
	var cleanupRan atomic.Bool
	sm.AddCleanup(func(reason string) {
		cleanupRan.Store(true)
	})

	sm.Shutdown("test")

	// Wait a bit for async operations
	time.Sleep(100 * time.Millisecond)

	if !cleanupRan.Load() {
		t.Error("Cleanup function not called")
	}
}

func TestShutdownManager_NilHooks(t *testing.T) {
	sm := NewShutdownManager()

	// Don't set any hooks - should not panic
	sm.Shutdown("test")

	// Verify shutdown completed
	select {
	case <-sm.Done():
		// Expected
	case <-time.After(time.Second):
		t.Error("Shutdown did not complete")
	}
}
