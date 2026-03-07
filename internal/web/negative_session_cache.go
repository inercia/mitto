package web

import (
	"sync"
	"time"
)

// negativeSessionCacheTTL is how long a "session not found" result is cached.
// Short TTL to allow session creation to work quickly, but long enough to
// absorb bursts of retries for genuinely deleted sessions.
const negativeSessionCacheTTL = 30 * time.Second

// NegativeSessionCache caches session IDs that are known to not exist,
// preventing repeated filesystem lookups for the same deleted session.
//
// This is part of the circuit breaker pattern to prevent "Session not found"
// error storms. When a client repeatedly tries to connect to a deleted session,
// the first lookup populates the cache; subsequent lookups are short-circuited
// without hitting the filesystem.
//
// It is safe for concurrent use.
type NegativeSessionCache struct {
	mu      sync.RWMutex
	entries map[string]time.Time // sessionID -> expiry time
}

// NewNegativeSessionCache creates a new empty cache.
func NewNegativeSessionCache() *NegativeSessionCache {
	return &NegativeSessionCache{
		entries: make(map[string]time.Time),
	}
}

// MarkNotFound records that a session ID was not found.
// The entry will expire after negativeSessionCacheTTL.
func (c *NegativeSessionCache) MarkNotFound(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[sessionID] = time.Now().Add(negativeSessionCacheTTL)
}

// IsNotFound returns true if the session ID is in the negative cache
// and the entry has not expired. Expired entries are lazily removed.
func (c *NegativeSessionCache) IsNotFound(sessionID string) bool {
	c.mu.RLock()
	expiry, ok := c.entries[sessionID]
	c.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		// Expired — lazily remove
		c.mu.Lock()
		// Re-check under write lock to avoid race
		if exp, exists := c.entries[sessionID]; exists && time.Now().After(exp) {
			delete(c.entries, sessionID)
		}
		c.mu.Unlock()
		return false
	}
	return true
}

// Remove removes a session ID from the negative cache.
// Called when a session is created or resumed to ensure it's not
// incorrectly cached as non-existent.
func (c *NegativeSessionCache) Remove(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, sessionID)
}

// Cleanup removes all expired entries. Can be called periodically
// to prevent unbounded growth of the entries map.
func (c *NegativeSessionCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, expiry := range c.entries {
		if now.After(expiry) {
			delete(c.entries, id)
		}
	}
}

// Len returns the number of entries (including expired ones that haven't
// been cleaned up yet). Useful for testing and metrics.
func (c *NegativeSessionCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
