package web

import (
	"sync"

	"github.com/inercia/mitto/internal/acpproc/procstart"
)

// stderrPatternsCache is a simple concurrent cache keyed by ACP server name that
// memoizes the compiled per-agent stderr regex patterns for that server (mitto-k6h).
// The cache stores nil values explicitly (via the ok bool from get) so a
// negative lookup — "this server has no per-agent patterns" — is not re-resolved
// against agent metadata on every GetOrCreateProcess call. Entries are compiled
// once at first request and reused thereafter.
//
// Invalidation is intentionally NOT provided: agent metadata changes require a
// server restart to pick up new stderr patterns, matching the existing
// discovery-time lifecycle for AgentDefaults.
type stderrPatternsCache struct {
	mu      sync.RWMutex
	entries map[string]*procstart.CompiledStderrPatterns
	// present tracks negative lookups so callers can distinguish "not cached"
	// from "cached-as-nil" (the latter is a valid, terminal result).
	present map[string]bool
}

func newStderrPatternsCache() *stderrPatternsCache {
	return &stderrPatternsCache{
		entries: make(map[string]*procstart.CompiledStderrPatterns),
		present: make(map[string]bool),
	}
}

func (c *stderrPatternsCache) get(key string) (*procstart.CompiledStderrPatterns, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.present[key] {
		return nil, false
	}
	return c.entries[key], true
}

func (c *stderrPatternsCache) put(key string, val *procstart.CompiledStderrPatterns) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = val
	c.present[key] = true
}
