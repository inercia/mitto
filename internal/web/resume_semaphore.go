package web

// resumeSemaphore is a small counting semaphore used to bound the number of
// concurrent interactive ResumeSession calls issued from the cold-start
// WebSocket fan-out (mitto-54k.1).
//
// On cold start, ~N sessions in a workspace reconnect their WebSockets nearly
// simultaneously. Without a bound, each spawns its own goroutine that calls
// ResumeSession, saturating the Mitto process and starving the agent's inbound
// HTTP `initialize`/`tools/list` to Mitto's own :5757/mcp MCP endpoint.
//
// The user-focused `ensure_resumed` path (foreground) MUST NOT acquire this
// semaphore — the session the user is actively looking at should resume first.
type resumeSemaphore struct {
	ch chan struct{}
}

// newResumeSemaphore returns a semaphore with the given capacity. A capacity
// <1 is clamped to 1 — a size-0 semaphore would deadlock every acquire.
func newResumeSemaphore(capacity int) *resumeSemaphore {
	if capacity < 1 {
		capacity = 1
	}
	return &resumeSemaphore{ch: make(chan struct{}, capacity)}
}

// Acquire blocks until a slot is available. A nil receiver is a no-op so
// callers can guard optional wiring with a simple nil check upstream and still
// call Acquire unconditionally in tests.
func (s *resumeSemaphore) Acquire() {
	if s == nil {
		return
	}
	s.ch <- struct{}{}
}

// Release frees a previously-acquired slot. Callers must pair each successful
// Acquire with exactly one Release (typically via defer).
func (s *resumeSemaphore) Release() {
	if s == nil {
		return
	}
	<-s.ch
}

// Capacity reports the configured maximum concurrency. Returns 0 for a nil
// receiver.
func (s *resumeSemaphore) Capacity() int {
	if s == nil {
		return 0
	}
	return cap(s.ch)
}
