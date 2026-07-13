package beads

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// defaultCacheTTL is the backstop expiry for cached read payloads. Writer-side
// invalidation is the primary freshness mechanism; the TTL just bounds staleness
// when an external process mutates the bd database without going through this
// CachingClient (e.g. bd run manually from the shell).
const defaultCacheTTL = 60 * time.Second

// cacheEntry is a single cached read payload. For JSON reads (List, Ready,
// Status, ListAllLabels) payload holds the raw JSON bytes. For ConfigShow the
// configMap field holds the decoded map[string]string; payload is unused.
// The two cases are stored in separate maps so we never mix representations.
type cacheEntry struct {
	payload    []byte
	configMap  map[string]string
	capturedAt time.Time
}

// CachingClient wraps a Client and caches the payloads of read methods keyed
// by (workingDir, methodTag). Writes pass through unchanged and invalidate the
// workspace's cache slot. Entries expire after a TTL floor as a backstop
// against missed external invalidation events.
type CachingClient struct {
	inner Client
	ttl   time.Duration

	mu      sync.RWMutex
	entries map[string]map[string]cacheEntry // dir -> methodTag -> entry

	sf singleflight.Group
}

// NewCachingClient wraps inner with an in-memory read cache. Callers get back
// a *CachingClient (concrete) so they can reach Invalidate/InvalidateAll from
// wiring code.
func NewCachingClient(inner Client) *CachingClient {
	return &CachingClient{
		inner:   inner,
		ttl:     defaultCacheTTL,
		entries: make(map[string]map[string]cacheEntry),
	}
}

// Invalidate drops every cached entry for the given workspace directory.
// Safe to call for a dir that has no cached entries (no-op).
func (c *CachingClient) Invalidate(dir string) {
	c.mu.Lock()
	delete(c.entries, dir)
	c.mu.Unlock()
}

// InvalidateAll drops every cached entry across all workspaces.
func (c *CachingClient) InvalidateAll() {
	c.mu.Lock()
	c.entries = make(map[string]map[string]cacheEntry)
	c.mu.Unlock()
}

// lookup returns a cached entry for (dir, tag) if present and not expired.
func (c *CachingClient) lookup(dir, tag string) (cacheEntry, bool) {
	c.mu.RLock()
	entry, ok := c.entries[dir][tag]
	c.mu.RUnlock()
	if !ok {
		return cacheEntry{}, false
	}
	if time.Since(entry.capturedAt) > c.ttl {
		c.mu.Lock()
		if cur, ok := c.entries[dir][tag]; ok && cur.capturedAt.Equal(entry.capturedAt) {
			delete(c.entries[dir], tag)
			if len(c.entries[dir]) == 0 {
				delete(c.entries, dir)
			}
		}
		c.mu.Unlock()
		return cacheEntry{}, false
	}
	return entry, true
}

// store records a cache entry for (dir, tag).
func (c *CachingClient) store(dir, tag string, entry cacheEntry) {
	c.mu.Lock()
	slot, ok := c.entries[dir]
	if !ok {
		slot = make(map[string]cacheEntry)
		c.entries[dir] = slot
	}
	slot[tag] = entry
	c.mu.Unlock()
}

// doJSON is the shared cache-then-singleflight body for read methods returning
// []byte. It skips the cache entirely for uninitialized dirs (see cache.go
// contract note) so the isInitialized short-circuit payload is never stored.
func (c *CachingClient) doJSON(ctx context.Context, dir, tag string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	if !isInitialized(dir) {
		return fetch(ctx)
	}
	if entry, ok := c.lookup(dir, tag); ok {
		return entry.payload, nil
	}
	key := dir + "\x00" + tag
	v, err, _ := c.sf.Do(key, func() (any, error) {
		if entry, ok := c.lookup(dir, tag); ok {
			return entry.payload, nil
		}
		out, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		c.store(dir, tag, cacheEntry{payload: out, capturedAt: time.Now()})
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// ---------------------------------------------------------------------------
// Cached read methods
// ---------------------------------------------------------------------------

// List returns the cached list payload for dir, populating on miss.
func (c *CachingClient) List(ctx context.Context, dir string) ([]byte, error) {
	return c.doJSON(ctx, dir, "list", func(ctx context.Context) ([]byte, error) {
		return c.inner.List(ctx, dir)
	})
}

// Ready returns the cached ready payload for dir, populating on miss.
func (c *CachingClient) Ready(ctx context.Context, dir string) ([]byte, error) {
	return c.doJSON(ctx, dir, "ready", func(ctx context.Context) ([]byte, error) {
		return c.inner.Ready(ctx, dir)
	})
}

// Status returns the cached status payload for dir, populating on miss.
func (c *CachingClient) Status(ctx context.Context, dir string) ([]byte, error) {
	return c.doJSON(ctx, dir, "status", func(ctx context.Context) ([]byte, error) {
		return c.inner.Status(ctx, dir)
	})
}

// ListAllLabels returns the cached labels payload for dir, populating on miss.
func (c *CachingClient) ListAllLabels(ctx context.Context, dir string) ([]byte, error) {
	return c.doJSON(ctx, dir, "labels", func(ctx context.Context) ([]byte, error) {
		return c.inner.ListAllLabels(ctx, dir)
	})
}

// ConfigShow returns the cached bd config map for dir, populating on miss.
// Uses the same singleflight group as the []byte readers, but stores the
// decoded map in a dedicated cacheEntry field so the two representations do
// not collide.
func (c *CachingClient) ConfigShow(ctx context.Context, dir string) (map[string]string, error) {
	const tag = "configshow"
	if !isInitialized(dir) {
		return c.inner.ConfigShow(ctx, dir)
	}
	if entry, ok := c.lookup(dir, tag); ok && entry.configMap != nil {
		return entry.configMap, nil
	}
	key := dir + "\x00" + tag
	v, err, _ := c.sf.Do(key, func() (any, error) {
		if entry, ok := c.lookup(dir, tag); ok && entry.configMap != nil {
			return entry.configMap, nil
		}
		out, err := c.inner.ConfigShow(ctx, dir)
		if err != nil {
			return nil, err
		}
		c.store(dir, tag, cacheEntry{configMap: out, capturedAt: time.Now()})
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(map[string]string), nil
}

// ---------------------------------------------------------------------------
// Pass-through reads (not cached)
// ---------------------------------------------------------------------------

// Show is a per-id read; per-id invalidation is not worth the complexity, so
// it always calls inner directly.
func (c *CachingClient) Show(ctx context.Context, dir, id string) ([]byte, error) {
	return c.inner.Show(ctx, dir, id)
}

// ListClosedIDs is only called during cleanup preflight and is not worth
// caching.
func (c *CachingClient) ListClosedIDs(ctx context.Context, dir string) ([]string, error) {
	return c.inner.ListClosedIDs(ctx, dir)
}

// ---------------------------------------------------------------------------
// Writers: invalidate the workspace slot before returning (even on error, so a
// partially-applied bd write does not leave stale reads visible).
// ---------------------------------------------------------------------------

// Create invalidates dir then delegates to inner.
func (c *CachingClient) Create(ctx context.Context, dir string, p CreateParams) ([]byte, error) {
	defer c.Invalidate(dir)
	return c.inner.Create(ctx, dir, p)
}

// Update invalidates dir then delegates to inner.
func (c *CachingClient) Update(ctx context.Context, dir string, p UpdateParams) error {
	defer c.Invalidate(dir)
	return c.inner.Update(ctx, dir, p)
}

// SetStatus invalidates dir then delegates to inner.
func (c *CachingClient) SetStatus(ctx context.Context, dir, id, action string) error {
	defer c.Invalidate(dir)
	return c.inner.SetStatus(ctx, dir, id, action)
}

// Delete invalidates dir then delegates to inner.
func (c *CachingClient) Delete(ctx context.Context, dir, id string) error {
	defer c.Invalidate(dir)
	return c.inner.Delete(ctx, dir, id)
}

// DeleteIDs invalidates dir then delegates to inner.
func (c *CachingClient) DeleteIDs(ctx context.Context, dir string, ids []string) error {
	defer c.Invalidate(dir)
	return c.inner.DeleteIDs(ctx, dir, ids)
}

// Comment invalidates dir then delegates to inner.
func (c *CachingClient) Comment(ctx context.Context, dir, id, text string) error {
	defer c.Invalidate(dir)
	return c.inner.Comment(ctx, dir, id, text)
}

// Dep invalidates dir then delegates to inner.
func (c *CachingClient) Dep(ctx context.Context, dir string, p DepParams) error {
	defer c.Invalidate(dir)
	return c.inner.Dep(ctx, dir, p)
}

// Label invalidates dir then delegates to inner.
func (c *CachingClient) Label(ctx context.Context, dir string, p LabelParams) error {
	defer c.Invalidate(dir)
	return c.inner.Label(ctx, dir, p)
}

// ConfigSet invalidates dir then delegates to inner.
func (c *CachingClient) ConfigSet(ctx context.Context, dir, key, value string) error {
	defer c.Invalidate(dir)
	return c.inner.ConfigSet(ctx, dir, key, value)
}

// ConfigUnset invalidates dir then delegates to inner.
func (c *CachingClient) ConfigUnset(ctx context.Context, dir, key string) error {
	defer c.Invalidate(dir)
	return c.inner.ConfigUnset(ctx, dir, key)
}

// Sync invalidates dir then delegates to inner.
func (c *CachingClient) Sync(ctx context.Context, dir, integration, action string) (string, error) {
	defer c.Invalidate(dir)
	return c.inner.Sync(ctx, dir, integration, action)
}

// EnsureInitialized invalidates dir then delegates to inner. The invalidation
// matters because it flips the isInitialized short-circuit off, and any prior
// (pre-init) reads that reached inner would have returned the []byte("[]")
// short-circuit value.
func (c *CachingClient) EnsureInitialized(ctx context.Context, dir string) error {
	defer c.Invalidate(dir)
	return c.inner.EnsureInitialized(ctx, dir)
}

// Compile-time assertion that *CachingClient satisfies the full Client API.
var _ Client = (*CachingClient)(nil)
