// Package hooks — sliding-window throttle for transient hook failures.
//
// A single transient DNS blip during cloudflared bootstrap can generate a
// handful of failures within seconds. Rather than surfacing every one as a
// UI toast, we suppress the first `hookFailureThreshold` transient failures
// per hook name inside a rolling `hookFailureWindow` window. A "real"
// (non-transient) failure is never throttled — it always broadcasts.
package hooks

import (
	"sync"
	"time"
)

// hookFailureWindow is the rolling window over which transient failures are
// counted per hook. Declared as var so tests can override.
var hookFailureWindow = 5 * time.Minute

// hookFailureThreshold is the number of transient failures per window that
// are suppressed (not broadcast). The (N+1)-th failure and beyond in the
// same window are allowed through. Declared as var so tests can override.
var hookFailureThreshold = 2

// hookFailureThrottle rate-limits transient hook failures using a per-name
// sliding window. Safe for concurrent use.
type hookFailureThrottle struct {
	mu     sync.Mutex
	events map[string][]time.Time
}

// newHookFailureThrottle constructs an empty throttle.
func newHookFailureThrottle() *hookFailureThrottle {
	return &hookFailureThrottle{events: make(map[string][]time.Time)}
}

// ShouldSuppress records a transient failure for the given hook name at time
// `now` and returns whether it should be suppressed (dropped from the
// broadcast pipeline). It also returns the number of failures now recorded
// within the current window for observability/logging.
//
// Contract:
//   - The first `hookFailureThreshold` transient failures within
//     `hookFailureWindow` return suppress=true.
//   - Any further transient failures in the same window return suppress=false
//     so the user learns the failure is persistent.
//   - Only call this for failures already classified transient — real
//     failures must bypass the throttle entirely.
func (t *hookFailureThrottle) ShouldSuppress(name string, now time.Time) (suppress bool, seenInWindow int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := now.Add(-hookFailureWindow)
	trimmed := t.events[name][:0]
	for _, ts := range t.events[name] {
		if ts.After(cutoff) {
			trimmed = append(trimmed, ts)
		}
	}
	trimmed = append(trimmed, now)
	t.events[name] = trimmed

	seen := len(trimmed)
	return seen <= hookFailureThreshold, seen
}

// packageThrottle is the process-wide throttle shared by all hook call sites
// (StartUp goroutine + RunDown). One instance is enough because hook names
// are already the throttle key and there is only ever one active pair of
// up/down hooks per Mitto process.
var packageThrottle = newHookFailureThrottle()
