package beads

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultCacheTTL is the backstop expiry for cached read payloads. Writer-side
// invalidation (defer c.Invalidate(dir) on every mutating method) plus the
// .beads/ fsnotify watcher are the primary freshness mechanisms; this TTL only
// bounds staleness in the corner cases those two paths can miss:
//   - fsnotify holes on NFS / SMB / other network mounts,
//   - Linux inotify per-user watch-limit exhaustion,
//   - the watch-registration race (a workspace read before its .beads/ watch is
//     installed),
//   - the 2 s BeadsSelfSuppressGrace window hiding a rapid external write.
//
// A 10 min backstop is a substantial hit-rate win over the historical 60 s
// default (mitto-9ni) while remaining a strict upper bound for the corner
// cases above. Overridable per-deployment via web.beads.read_cache_ttl in
// settings.json / .mittorc.
const DefaultCacheTTL = 10 * time.Minute

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

	// Metrics counters (mitto-is2.5). Cumulative since construction, read via
	// Metrics(). Reason-tagged so the invalidation counters add up (each drop
	// bumps exactly one of writer/watcher/workspaceRemoved/ttl).
	hits                    atomic.Int64
	misses                  atomic.Int64
	invalidWriter           atomic.Int64
	invalidWatcher          atomic.Int64
	invalidWorkspaceRemoved atomic.Int64
	invalidTTL              atomic.Int64
	singleflightShared      atomic.Int64
}

// CacheMetrics is a point-in-time snapshot of *CachingClient counters.
// Fields are cumulative since process start (except EntriesCurrent, a gauge).
type CacheMetrics struct {
	Hits                          int64   `json:"hits"`
	Misses                        int64   `json:"misses"`
	BdInvocationsAvoided          int64   `json:"bd_invocations_avoided"` // == Hits
	InvalidationsWriter           int64   `json:"invalidations_writer"`
	InvalidationsWatcher          int64   `json:"invalidations_watcher"`
	InvalidationsWorkspaceRemoved int64   `json:"invalidations_workspace_removed"`
	InvalidationsTTL              int64   `json:"invalidations_ttl"`
	SingleflightShared            int64   `json:"singleflight_shared"`
	EntriesCurrent                int64   `json:"entries_current"`
	HitRate                       float64 `json:"hit_rate"`
}

// NewCachingClient wraps inner with an in-memory read cache. Callers get back
// a *CachingClient (concrete) so they can reach Invalidate/InvalidateAll from
// wiring code.
func NewCachingClient(inner Client) *CachingClient {
	return &CachingClient{
		inner:   inner,
		ttl:     DefaultCacheTTL,
		entries: make(map[string]map[string]cacheEntry),
	}
}

// NewCachingClientWithTTL wraps inner with an in-memory read cache using
// the supplied ttl. Non-positive ttl falls back to DefaultCacheTTL so
// callers can pass a config-derived value without pre-validation.
func NewCachingClientWithTTL(inner Client, ttl time.Duration) *CachingClient {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &CachingClient{
		inner:   inner,
		ttl:     ttl,
		entries: make(map[string]map[string]cacheEntry),
	}
}

// evictDir drops every cached entry for dir under the write lock. Shared by all
// per-dir invalidation entry points; each caller then bumps its own reason
// counter so we never double-count.
func (c *CachingClient) evictDir(dir string) {
	c.mu.Lock()
	delete(c.entries, dir)
	c.mu.Unlock()
}

// Invalidate drops every cached entry for the given workspace directory
// (writer-side invalidation semantics; counted under InvalidationsWriter).
// Safe to call for a dir that has no cached entries (no-op).
func (c *CachingClient) Invalidate(dir string) {
	c.evictDir(dir)
	c.invalidWriter.Add(1)
}

// InvalidateFromWatcher drops every cached entry for dir with watcher semantics
// (counted under InvalidationsWatcher). Called by external-change subscribers
// (e.g. BeadsWatcher fsnotify events) when an out-of-process mutation is
// detected. Kept as a distinct method so metrics can distinguish in-process
// writer invalidations from external ones.
func (c *CachingClient) InvalidateFromWatcher(dir string) {
	c.evictDir(dir)
	c.invalidWatcher.Add(1)
}

// InvalidateAll drops every cached entry across all workspaces (counted once
// under InvalidationsWorkspaceRemoved per call, regardless of how many dirs
// were held).
func (c *CachingClient) InvalidateAll() {
	c.mu.Lock()
	c.entries = make(map[string]map[string]cacheEntry)
	c.mu.Unlock()
	c.invalidWorkspaceRemoved.Add(1)
}

// lookup returns a cached entry for (dir, tag) if present and not expired.
// A TTL-driven eviction is counted under InvalidationsTTL.
func (c *CachingClient) lookup(dir, tag string) (cacheEntry, bool) {
	c.mu.RLock()
	entry, ok := c.entries[dir][tag]
	c.mu.RUnlock()
	if !ok {
		return cacheEntry{}, false
	}
	if time.Since(entry.capturedAt) > c.ttl {
		c.mu.Lock()
		evicted := false
		if cur, ok := c.entries[dir][tag]; ok && cur.capturedAt.Equal(entry.capturedAt) {
			delete(c.entries[dir], tag)
			if len(c.entries[dir]) == 0 {
				delete(c.entries, dir)
			}
			evicted = true
		}
		c.mu.Unlock()
		if evicted {
			c.invalidTTL.Add(1)
		}
		return cacheEntry{}, false
	}
	return entry, true
}

// Metrics returns a point-in-time snapshot of the cache counters.
func (c *CachingClient) Metrics() CacheMetrics {
	hits := c.hits.Load()
	misses := c.misses.Load()
	var hitRate float64
	if total := hits + misses; total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	c.mu.RLock()
	var entries int64
	for _, slot := range c.entries {
		entries += int64(len(slot))
	}
	c.mu.RUnlock()

	return CacheMetrics{
		Hits:                          hits,
		Misses:                        misses,
		BdInvocationsAvoided:          hits,
		InvalidationsWriter:           c.invalidWriter.Load(),
		InvalidationsWatcher:          c.invalidWatcher.Load(),
		InvalidationsWorkspaceRemoved: c.invalidWorkspaceRemoved.Load(),
		InvalidationsTTL:              c.invalidTTL.Load(),
		SingleflightShared:            c.singleflightShared.Load(),
		EntriesCurrent:                entries,
		HitRate:                       hitRate,
	}
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
//
// Uses sf.DoChan (not sf.Do) so followers can abandon the wait on ctx
// cancellation rather than block on the leader's WaitGroup past their own
// request deadline (mitto-kij). The leader continues fetching in the
// background regardless; when it finishes the cache is populated for later
// callers.
func (c *CachingClient) doJSON(ctx context.Context, dir, tag string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	if !isInitialized(dir) {
		return fetch(ctx)
	}
	if entry, ok := c.lookup(dir, tag); ok {
		c.hits.Add(1)
		return entry.payload, nil
	}
	key := dir + "\x00" + tag
	ch := c.sf.DoChan(key, func() (any, error) {
		if entry, ok := c.lookup(dir, tag); ok {
			c.hits.Add(1)
			return entry.payload, nil
		}
		c.misses.Add(1)
		// Detach from the caller's ctx: this closure runs as the singleflight
		// leader on behalf of any number of followers; using the leader's ctx
		// would let its early cancellation abort the shared fetch and starve
		// unrelated followers. Use context.Background() so the leader always
		// runs to completion (bounded downstream by cliClient.runJSONRead's
		// own timeout).
		out, err := fetch(context.Background())
		if err != nil {
			return nil, err
		}
		c.store(dir, tag, cacheEntry{payload: out, capturedAt: time.Now()})
		return out, nil
	})
	select {
	case r := <-ch:
		if r.Shared {
			c.singleflightShared.Add(1)
		}
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.([]byte), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
// not collide. Mirrors doJSON's DoChan + ctx-observing select so followers
// do not block past their request deadline (mitto-kij).
func (c *CachingClient) ConfigShow(ctx context.Context, dir string) (map[string]string, error) {
	const tag = "configshow"
	if !isInitialized(dir) {
		return c.inner.ConfigShow(ctx, dir)
	}
	if entry, ok := c.lookup(dir, tag); ok && entry.configMap != nil {
		c.hits.Add(1)
		return entry.configMap, nil
	}
	key := dir + "\x00" + tag
	ch := c.sf.DoChan(key, func() (any, error) {
		if entry, ok := c.lookup(dir, tag); ok && entry.configMap != nil {
			c.hits.Add(1)
			return entry.configMap, nil
		}
		c.misses.Add(1)
		// Detach from caller's ctx; see doJSON for rationale.
		out, err := c.inner.ConfigShow(context.Background(), dir)
		if err != nil {
			return nil, err
		}
		c.store(dir, tag, cacheEntry{configMap: out, capturedAt: time.Now()})
		return out, nil
	})
	select {
	case r := <-ch:
		if r.Shared {
			c.singleflightShared.Add(1)
		}
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(map[string]string), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Show returns the cached payload for `bd show <id>` in dir, populating on
// miss. Entries are keyed under tag "show:"+id in the same per-dir slot as the
// other cached reads, so any writer/watcher invalidation (evictDir) drops all
// per-id entries alongside the list/ready/status/labels payloads — no per-id
// invalidation bookkeeping needed. Rationale (mitto-y21): the beads viewer's
// per-ticket load path is the dominant miss for the viewer UX; a bare
// pass-through here forced a fresh `bd show` on every ticket re-open.
func (c *CachingClient) Show(ctx context.Context, dir, id string) ([]byte, error) {
	return c.doJSON(ctx, dir, "show:"+id, func(ctx context.Context) ([]byte, error) {
		return c.inner.Show(ctx, dir, id)
	})
}

// ---------------------------------------------------------------------------
// Pass-through reads (not cached)
// ---------------------------------------------------------------------------

// ListClosedIDs is only called during cleanup preflight and is not worth
// caching.
func (c *CachingClient) ListClosedIDs(ctx context.Context, dir string) ([]string, error) {
	return c.inner.ListClosedIDs(ctx, dir)
}

// Statuses is called from mitto_conversation_wait's beads_issues_reached_state
// branch. Waiters need to observe fresh statuses on every re-evaluation, so
// this deliberately bypasses the cache.
func (c *CachingClient) Statuses(ctx context.Context, dir string, ids []string) (map[string]string, error) {
	return c.inner.Statuses(ctx, dir, ids)
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

// MigrateRemote invalidates dir then delegates to inner. A schema migration
// rewrites the underlying store, so every cached read for dir is stale.
func (c *CachingClient) MigrateRemote(ctx context.Context, dir string) ([]byte, error) {
	defer c.Invalidate(dir)
	return c.inner.MigrateRemote(ctx, dir)
}

// Bootstrap invalidates dir then delegates to inner. Bootstrap can replace
// the entire local database with a remote clone, so every cached read for
// dir is stale.
func (c *CachingClient) Bootstrap(ctx context.Context, dir string) ([]byte, error) {
	defer c.Invalidate(dir)
	return c.inner.Bootstrap(ctx, dir)
}

// Compile-time assertion that *CachingClient satisfies the full Client API.
var _ Client = (*CachingClient)(nil)
