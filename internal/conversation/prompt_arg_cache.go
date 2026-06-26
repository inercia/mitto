package conversation

// prompt_arg_cache.go — per-conversation in-memory cache for prompt argument values.
//
// Owned by BackgroundSession (composition). Concurrency-safe: all map access is
// guarded by the component's own mutex. Observers and other session locks are NEVER
// acquired while this mutex is held (deadlock prevention rule).

import (
	"sort"
	"sync"
	"time"
)

// promptArgCacheEntry holds a single cached argument value.
// A zero expiresAt means the entry never expires (conversation lifetime).
type promptArgCacheEntry struct {
	value     string
	expiresAt time.Time
}

// isExpired reports whether the entry has passed its TTL.
func (e *promptArgCacheEntry) isExpired() bool {
	return !e.expiresAt.IsZero() && time.Now().After(e.expiresAt)
}

// promptArgCache is a per-conversation in-memory store for prompt argument values.
// The map key is a composite of promptName and paramName joined by a NUL byte to
// prevent collisions between names that happen to share a prefix.
type promptArgCache struct {
	mu      sync.Mutex
	entries map[string]promptArgCacheEntry
}

// newPromptArgCache constructs a ready-to-use promptArgCache.
func newPromptArgCache() *promptArgCache {
	return &promptArgCache{
		entries: make(map[string]promptArgCacheEntry),
	}
}

// cacheKey builds a collision-free composite key.
func promptArgCacheKey(promptName, paramName string) string {
	return promptName + "\x00" + paramName
}

// Get returns the cached value for (promptName, paramName).
// Returns ("", false) if the entry is absent or has expired; expired entries are
// lazily removed from the map on the first Get that encounters them.
func (c *promptArgCache) Get(promptName, paramName string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := promptArgCacheKey(promptName, paramName)
	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if entry.isExpired() {
		delete(c.entries, key)
		return "", false
	}
	return entry.value, true
}

// Set stores value for (promptName, paramName).
// When ttl <= 0 the entry never expires (expiresAt is left zero).
func (c *promptArgCache) Set(promptName, paramName, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := promptArgCacheEntry{value: value}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}
	c.entries[promptArgCacheKey(promptName, paramName)] = entry
}

// FreshNames returns the non-expired parameter names cached for promptName, sorted
// ascending. Expired entries encountered during the scan are lazily removed.
func (c *promptArgCache) FreshNames(promptName string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := promptName + "\x00"
	var names []string
	for key, entry := range c.entries {
		if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		if entry.isExpired() {
			delete(c.entries, key)
			continue
		}
		names = append(names, key[len(prefix):])
	}
	sort.Strings(names)
	return names
}
