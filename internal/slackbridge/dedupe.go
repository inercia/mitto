package slackbridge

import "sync"

// defaultDedupeCapacity bounds how many recent event IDs are remembered for
// de-duplication. Old entries are evicted FIFO once the cap is reached. This
// is intentionally an in-memory, best-effort bound (not a durable backlog) —
// see docs/devel/slack-bridge.md for the production gap this leaves open.
const defaultDedupeCapacity = 1000

// dedupeSet is a small bounded, thread-safe "seen before" set keyed by Slack
// event_id. It is process-scoped and non-persistent by design (PoC scope).
type dedupeSet struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	order    []string
	capacity int
}

// newDedupeSet constructs a dedupeSet with the given capacity. A capacity <=
// 0 falls back to defaultDedupeCapacity.
func newDedupeSet(capacity int) *dedupeSet {
	if capacity <= 0 {
		capacity = defaultDedupeCapacity
	}
	return &dedupeSet{
		seen:     make(map[string]struct{}, capacity),
		order:    make([]string, 0, capacity),
		capacity: capacity,
	}
}

// SeenBefore reports whether id was already recorded, and records it (as
// seen) if it was not. Empty ids are always reported as "seen" so callers
// treat an event with no id as non-deduplicable (i.e. the caller should drop
// it rather than dispatch, since acceptance criterion #3 requires an id to
// dedupe against).
func (d *dedupeSet) SeenBefore(id string) bool {
	if id == "" {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.seen[id]; ok {
		return true
	}

	d.seen[id] = struct{}{}
	d.order = append(d.order, id)
	if len(d.order) > d.capacity {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
	return false
}
