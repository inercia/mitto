package web

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNegativeSessionCache_BasicOperations(t *testing.T) {
	cache := NewNegativeSessionCache()

	// Initially not found
	if cache.IsNotFound("session-1") {
		t.Error("expected session-1 to not be in cache initially")
	}

	// Mark as not found
	cache.MarkNotFound("session-1")
	if !cache.IsNotFound("session-1") {
		t.Error("expected session-1 to be in cache after MarkNotFound")
	}

	// Other sessions unaffected
	if cache.IsNotFound("session-2") {
		t.Error("expected session-2 to not be in cache")
	}

	// Len reflects entries
	if cache.Len() != 1 {
		t.Errorf("expected Len() == 1, got %d", cache.Len())
	}

	// Remove
	cache.Remove("session-1")
	if cache.IsNotFound("session-1") {
		t.Error("expected session-1 to not be in cache after Remove")
	}
	if cache.Len() != 0 {
		t.Errorf("expected Len() == 0 after Remove, got %d", cache.Len())
	}
}

func TestNegativeSessionCache_RemoveNonExistent(t *testing.T) {
	cache := NewNegativeSessionCache()

	// Remove a session that was never added — should not panic
	cache.Remove("never-added")
	if cache.Len() != 0 {
		t.Errorf("expected Len() == 0, got %d", cache.Len())
	}
}

func TestNegativeSessionCache_Expiry(t *testing.T) {
	cache := NewNegativeSessionCache()

	// Manually insert an already-expired entry
	cache.mu.Lock()
	cache.entries["expired-session"] = time.Now().Add(-1 * time.Second)
	cache.mu.Unlock()

	// Should return false for expired entry
	if cache.IsNotFound("expired-session") {
		t.Error("expected expired entry to return false from IsNotFound")
	}

	// Expired entry should be lazily removed
	if cache.Len() != 0 {
		t.Errorf("expected expired entry to be lazily removed, Len() = %d", cache.Len())
	}
}

func TestNegativeSessionCache_ValidEntryNotExpired(t *testing.T) {
	cache := NewNegativeSessionCache()

	// Insert an entry that expires far in the future
	cache.mu.Lock()
	cache.entries["future-session"] = time.Now().Add(1 * time.Hour)
	cache.mu.Unlock()

	if !cache.IsNotFound("future-session") {
		t.Error("expected future entry to be found in cache")
	}
}

func TestNegativeSessionCache_MarkNotFoundOverwrite(t *testing.T) {
	cache := NewNegativeSessionCache()

	// Mark and then mark again — should update the expiry
	cache.MarkNotFound("session-1")
	time.Sleep(10 * time.Millisecond)
	cache.MarkNotFound("session-1")

	// Should still be found
	if !cache.IsNotFound("session-1") {
		t.Error("expected session-1 to still be in cache after re-marking")
	}
	if cache.Len() != 1 {
		t.Errorf("expected Len() == 1, got %d", cache.Len())
	}
}

func TestNegativeSessionCache_Cleanup(t *testing.T) {
	cache := NewNegativeSessionCache()

	// Add a mix of expired and valid entries
	cache.mu.Lock()
	cache.entries["expired1"] = time.Now().Add(-1 * time.Second)
	cache.entries["expired2"] = time.Now().Add(-5 * time.Second)
	cache.entries["valid1"] = time.Now().Add(30 * time.Second)
	cache.entries["valid2"] = time.Now().Add(1 * time.Hour)
	cache.mu.Unlock()

	cache.Cleanup()

	if cache.Len() != 2 {
		t.Errorf("expected 2 entries after cleanup, got %d", cache.Len())
	}

	if !cache.IsNotFound("valid1") {
		t.Error("expected valid1 to survive cleanup")
	}
	if !cache.IsNotFound("valid2") {
		t.Error("expected valid2 to survive cleanup")
	}
}

func TestNegativeSessionCache_CleanupEmpty(t *testing.T) {
	cache := NewNegativeSessionCache()

	// Cleanup on empty cache should not panic
	cache.Cleanup()
	if cache.Len() != 0 {
		t.Errorf("expected Len() == 0, got %d", cache.Len())
	}
}

func TestNegativeSessionCache_ConcurrentAccess(t *testing.T) {
	cache := NewNegativeSessionCache()
	var wg sync.WaitGroup

	// Run concurrent writes, reads, and removes
	for i := 0; i < 100; i++ {
		wg.Add(4)
		id := fmt.Sprintf("session-%d", i)

		go func() {
			defer wg.Done()
			cache.MarkNotFound(id)
		}()

		go func() {
			defer wg.Done()
			cache.IsNotFound(id)
		}()

		go func() {
			defer wg.Done()
			cache.Remove(id)
		}()

		go func() {
			defer wg.Done()
			cache.Cleanup()
		}()
	}

	wg.Wait()
	// Test passes if no race conditions or panics occurred
}

func TestNegativeSessionCache_InvalidateAfterCreate(t *testing.T) {
	// Simulate the real workflow: session is deleted → cached as not found → re-created
	cache := NewNegativeSessionCache()

	cache.MarkNotFound("session-abc")
	if !cache.IsNotFound("session-abc") {
		t.Error("expected session to be cached as not found")
	}

	// Simulate session re-creation by invalidating the cache
	cache.Remove("session-abc")
	if cache.IsNotFound("session-abc") {
		t.Error("expected session to no longer be cached after Remove")
	}
}
