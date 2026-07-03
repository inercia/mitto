package conversation

import (
	"fmt"
	"sync"
	"testing"
)

// TestCallbackIndex_RegisterAndLookup verifies basic registration and lookup.
func TestCallbackIndex_RegisterAndLookup(t *testing.T) {
	ci := NewCallbackIndex()

	token := "test-token-123"
	sessionID := "session-456"

	ci.Register(token, sessionID)

	got, ok := ci.Lookup(token)
	if !ok {
		t.Fatal("Lookup failed, expected token to be found")
	}
	if got != sessionID {
		t.Errorf("Lookup returned wrong sessionID: got %q, want %q", got, sessionID)
	}
}

// TestCallbackIndex_LookupNotFound verifies lookup returns false for non-existent token.
func TestCallbackIndex_LookupNotFound(t *testing.T) {
	ci := NewCallbackIndex()

	_, ok := ci.Lookup("non-existent-token")
	if ok {
		t.Error("Lookup returned true for non-existent token")
	}
}

// TestCallbackIndex_Remove verifies token removal.
func TestCallbackIndex_Remove(t *testing.T) {
	ci := NewCallbackIndex()

	token := "test-token-123"
	sessionID := "session-456"

	ci.Register(token, sessionID)
	ci.Remove(token)

	_, ok := ci.Lookup(token)
	if ok {
		t.Error("Lookup returned true after removal")
	}
}

// TestCallbackIndex_RemoveBySessionID verifies removal by session ID.
func TestCallbackIndex_RemoveBySessionID(t *testing.T) {
	ci := NewCallbackIndex()

	sessionID := "session-456"
	token1 := "token-1"
	token2 := "token-2"

	ci.Register(token1, sessionID)
	ci.Register(token2, sessionID)
	ci.Register("other-token", "other-session")

	ci.RemoveBySessionID(sessionID)

	if _, ok := ci.Lookup(token1); ok {
		t.Error("token1 should be removed")
	}
	if _, ok := ci.Lookup(token2); ok {
		t.Error("token2 should be removed")
	}
	if _, ok := ci.Lookup("other-token"); !ok {
		t.Error("other-token should still exist")
	}
}

// TestCallbackIndex_RemoveBySessionID_NoMatch verifies no panic when session ID has no tokens.
func TestCallbackIndex_RemoveBySessionID_NoMatch(t *testing.T) {
	ci := NewCallbackIndex()

	ci.Register("token-1", "session-1")

	ci.RemoveBySessionID("session-2")

	if _, ok := ci.Lookup("token-1"); !ok {
		t.Error("token-1 should still exist")
	}
}

// TestCallbackIndex_Concurrent tests concurrent access to the index.
func TestCallbackIndex_Concurrent(t *testing.T) {
	ci := NewCallbackIndex()

	const goroutines = 10
	const operations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operations; j++ {
				token := fmt.Sprintf("token-%d-%d", id, j)
				sessionID := fmt.Sprintf("session-%d", id)
				ci.Register(token, sessionID)
			}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operations; j++ {
				token := fmt.Sprintf("token-%d-%d", id, j)
				ci.Lookup(token)
			}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operations; j++ {
				sessionID := fmt.Sprintf("session-%d", id)
				ci.RemoveBySessionID(sessionID)
			}
		}(i)
	}

	wg.Wait()
}

// TestCallbackRateLimiter_Allow verifies rate limiting behavior.
func TestCallbackRateLimiter_Allow(t *testing.T) {
	crl := NewCallbackRateLimiter()

	token := "test-token"

	// First callbackBurst requests should succeed.
	for i := 0; i < callbackBurst; i++ {
		if !crl.Allow(token) {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}

	// Next request should be rate limited.
	if crl.Allow(token) {
		t.Error("4th request should be rate limited (burst exceeded)")
	}

	// Different token should have its own limit.
	if !crl.Allow("other-token") {
		t.Error("Different token should be allowed")
	}
}

// TestCallbackRateLimiter_Remove verifies limiter cleanup.
func TestCallbackRateLimiter_Remove(t *testing.T) {
	crl := NewCallbackRateLimiter()

	token := "test-token"

	for i := 0; i < callbackBurst+1; i++ {
		crl.Allow(token)
	}

	if crl.Allow(token) {
		t.Error("Should be rate limited before removal")
	}

	crl.Remove(token)

	if !crl.Allow(token) {
		t.Error("Should be allowed after removal (fresh limiter)")
	}
}
