package eventprojection

import "sync"

// PromptCorrelation tracks optimistic local prompt IDs against the upstream
// identity they eventually surface as, once known. A Projector consults it
// to decide whether an OriginRemote event confirms a local prompt (and so
// must not be re-projected as a new external action) or is genuinely
// externally-originated (see ADR agent-backend-architecture.md §3, Origin
// tracking).
//
// Safe for concurrent use.
type PromptCorrelation struct {
	mu   sync.Mutex
	byID map[string]string // upstream identity -> local prompt ID
}

// NewPromptCorrelation returns an empty PromptCorrelation.
func NewPromptCorrelation() *PromptCorrelation {
	return &PromptCorrelation{byID: make(map[string]string)}
}

// Link records that upstreamIdentity is the upstream echo of localPromptID.
// Once linked, Resolve(upstreamIdentity) reports ok=true until Forget is
// called (Projector forgets a link once it has resolved it, so a link is
// consumed exactly once).
func (c *PromptCorrelation) Link(localPromptID, upstreamIdentity string) {
	if upstreamIdentity == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byID[upstreamIdentity] = localPromptID
}

// Resolve reports whether upstreamIdentity is a known echo of a local
// prompt. ok=true means the event confirms local work and must not be
// re-projected as a new external action; ok=false means it is genuinely
// externally-originated (or unresolvable) and should be projected with
// SuppressLocalAutomation set.
func (c *PromptCorrelation) Resolve(upstreamIdentity string) (localPromptID string, ok bool) {
	if upstreamIdentity == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.byID[upstreamIdentity]
	return id, ok
}

// Forget releases a link once it has been resolved, bounding memory growth
// for long-running sessions.
func (c *PromptCorrelation) Forget(upstreamIdentity string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byID, upstreamIdentity)
}

// Pending reports the number of unresolved links, for tests/diagnostics.
func (c *PromptCorrelation) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byID)
}
