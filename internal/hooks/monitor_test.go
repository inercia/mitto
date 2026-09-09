package hooks

import (
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

// waitForRestartCountAtLeast polls counter until it reaches want or timeout elapses.
func waitForRestartCountAtLeast(t *testing.T, counter *atomic.Int64, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if counter.Load() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for restart count >= %d (got %d)", want, counter.Load())
}

// TestHealthMonitor_SustainedUnreachability_RestartChurnIsUnbounded reproduces
// mitto-8n4: "External address unreachable -> repeated hook restarts
// (self-healing flap)". HealthMonitor.run() has no circuit breaker — every
// confirmed-unreachable cycle triggers a full down/up restartHooks(), even
// though the up-hook always exits 0 ("self-healing") while the external
// address never actually becomes reachable again. With a sustained (not
// just brief-blip) outage, this produces unbounded restart churn instead of
// the churn plateauing once it's evident restarts aren't restoring
// reachability.
//
// This test shrinks the monitor's real multi-minute timing constants to
// milliseconds (restored via defer) so the multi-cycle behavior can be
// observed in a fast unit test, and points the monitor at a closed
// httptest server so every health check fails fast with "connection
// refused" — simulating a sustained "External address unreachable".
//
// EXPECTED (post-fix) behavior: once the monitor has confirmed the address
// is not coming back after some bounded number of restarts, further
// confirmed failures must NOT keep triggering new restarts — the restart
// count must plateau. CURRENT (buggy) behavior: restart count keeps
// growing for as long as the outage persists, with no plateau.
func TestHealthMonitor_SustainedUnreachability_RestartChurnIsUnbounded(t *testing.T) {
	// Point Address at a closed httptest server: connections are refused
	// immediately, so checkHealth() fails fast without waiting out
	// monitorRequestTimeout on every probe.
	srv := httptest.NewServer(nil)
	unreachableAddr := srv.URL
	srv.Close()

	// Shrink all timing so the monitor's loop runs in milliseconds instead
	// of minutes. Restored via defer so other tests are unaffected.
	origInitial, origCheck, origPostRestart := monitorInitialDelay, monitorCheckInterval, monitorPostRestartDelay
	origPreRestart, origReqTimeout, origMaxCheck := monitorPreRestartWait, monitorRequestTimeout, monitorMaxCheckInterval
	origRetries, origRetryDelay := monitorFailureRetries, monitorRetryDelay
	defer func() {
		monitorInitialDelay, monitorCheckInterval, monitorPostRestartDelay = origInitial, origCheck, origPostRestart
		monitorPreRestartWait, monitorRequestTimeout, monitorMaxCheckInterval = origPreRestart, origReqTimeout, origMaxCheck
		monitorFailureRetries, monitorRetryDelay = origRetries, origRetryDelay
	}()
	monitorInitialDelay = 1 * time.Millisecond
	monitorCheckInterval = 5 * time.Millisecond
	monitorPostRestartDelay = 2 * time.Millisecond
	monitorPreRestartWait = 1 * time.Millisecond
	monitorRequestTimeout = 50 * time.Millisecond
	monitorMaxCheckInterval = 20 * time.Millisecond
	monitorFailureRetries = 2
	monitorRetryDelay = 1 * time.Millisecond

	var restartCount atomic.Int64
	m := NewHealthMonitor(HealthMonitorConfig{
		Address:   unreachableAddr,
		APIPrefix: "",
		// Up/down hooks always "succeed" (exit 0), mirroring the bead's
		// evidence: "Up hook completed successfully" on every restart even
		// though the external address stays unreachable.
		UpHook:   config.WebHook{Command: "exit 0", Name: "up"},
		DownHook: config.WebHook{Command: "exit 0", Name: "down"},
		Port:     0,
		OnRestart: func(attempt int) {
			restartCount.Add(1)
		},
	})

	m.Start()
	defer m.Stop()

	// Establish that the failure-detection + restart mechanism is being
	// exercised at all.
	waitForRestartCountAtLeast(t, &restartCount, 2, 2*time.Second)
	countAfterFirstBatch := restartCount.Load()

	// The address never recovers for the remainder of the test. If the
	// monitor had a circuit breaker bounding restart churn on sustained
	// failure, restartCount would plateau here. Today it keeps growing on
	// every confirmed-failure cycle (mitto-8n4) — this assertion is what
	// the fix phase must make pass.
	time.Sleep(300 * time.Millisecond) // many more would-be cycles at these tiny intervals
	countAfterSustainedFailure := restartCount.Load()

	if countAfterSustainedFailure > countAfterFirstBatch {
		t.Fatalf("mitto-8n4: restart count kept growing during sustained unreachability with no circuit breaker: %d -> %d; expected it to plateau once a breaker opens",
			countAfterFirstBatch, countAfterSustainedFailure)
	}
}

// TestHealthMonitor_FlapStats_BoundsRingBuffer verifies mitto-3sl's flap-history
// ring buffer never grows past maxFlapHistoryEvents, while FlapStats' total
// restart count keeps counting unboundedly (it mirrors restartCount, which is
// a plain monotonic counter, not the bounded ring buffer).
func TestHealthMonitor_FlapStats_BoundsRingBuffer(t *testing.T) {
	m := NewHealthMonitor(HealthMonitorConfig{Address: "http://example.invalid"})

	base := time.Now()
	const totalFlaps = maxFlapHistoryEvents + 10

	for i := 0; i < totalFlaps; i++ {
		m.mu.Lock()
		m.restartCount++
		m.recordFlapLocked(base.Add(time.Duration(i) * time.Second))
		m.mu.Unlock()
	}

	m.mu.Lock()
	bufLen := len(m.flapTimes)
	m.mu.Unlock()
	if bufLen != maxFlapHistoryEvents {
		t.Fatalf("expected flapTimes bounded to %d entries, got %d", maxFlapHistoryEvents, bufLen)
	}

	total, inWindow := m.FlapStats(flapHistoryWindow)
	if total != totalFlaps {
		t.Fatalf("expected FlapStats total (mirrors restartCount) = %d, got %d", totalFlaps, total)
	}
	// All recorded flaps are within seconds of "now", well inside the 24h
	// window, so the windowed count should equal the bounded buffer length.
	if inWindow != maxFlapHistoryEvents {
		t.Fatalf("expected FlapStats inWindow = %d (bounded buffer, all recent), got %d", maxFlapHistoryEvents, inWindow)
	}
}

// TestHealthMonitor_FlapStats_WindowExcludesOldEvents verifies FlapStats'
// windowed count only includes flaps at or after the trailing window cutoff,
// while the total keeps counting every restart ever recorded.
func TestHealthMonitor_FlapStats_WindowExcludesOldEvents(t *testing.T) {
	m := NewHealthMonitor(HealthMonitorConfig{Address: "http://example.invalid"})

	now := time.Now()
	m.mu.Lock()
	m.restartCount = 3
	m.recordFlapLocked(now.Add(-48 * time.Hour)) // outside a 24h window
	m.recordFlapLocked(now.Add(-1 * time.Hour))  // inside a 24h window
	m.recordFlapLocked(now)                      // inside a 24h window
	m.mu.Unlock()

	total, inWindow := m.FlapStats(24 * time.Hour)
	if total != 3 {
		t.Fatalf("expected FlapStats total = 3, got %d", total)
	}
	if inWindow != 2 {
		t.Fatalf("expected FlapStats inWindow = 2 (excluding the 48h-old flap), got %d", inWindow)
	}
}

// TestHealthMonitor_SustainedUnreachability_FlapStatsTracksRestarts is an
// integration-style test (mirrors TestHealthMonitor_SustainedUnreachability_
// RestartChurnIsUnbounded's harness) verifying that FlapStats reflects the
// real restarts performed by HealthMonitor.run() during a sustained outage —
// not just the lower-level ring-buffer unit behavior above.
func TestHealthMonitor_SustainedUnreachability_FlapStatsTracksRestarts(t *testing.T) {
	srv := httptest.NewServer(nil)
	unreachableAddr := srv.URL
	srv.Close()

	origInitial, origCheck, origPostRestart := monitorInitialDelay, monitorCheckInterval, monitorPostRestartDelay
	origPreRestart, origReqTimeout, origMaxCheck := monitorPreRestartWait, monitorRequestTimeout, monitorMaxCheckInterval
	origRetries, origRetryDelay := monitorFailureRetries, monitorRetryDelay
	defer func() {
		monitorInitialDelay, monitorCheckInterval, monitorPostRestartDelay = origInitial, origCheck, origPostRestart
		monitorPreRestartWait, monitorRequestTimeout, monitorMaxCheckInterval = origPreRestart, origReqTimeout, origMaxCheck
		monitorFailureRetries, monitorRetryDelay = origRetries, origRetryDelay
	}()
	monitorInitialDelay = 1 * time.Millisecond
	monitorCheckInterval = 5 * time.Millisecond
	monitorPostRestartDelay = 2 * time.Millisecond
	monitorPreRestartWait = 1 * time.Millisecond
	monitorRequestTimeout = 50 * time.Millisecond
	monitorMaxCheckInterval = 20 * time.Millisecond
	monitorFailureRetries = 2
	monitorRetryDelay = 1 * time.Millisecond

	var restartCount atomic.Int64
	m := NewHealthMonitor(HealthMonitorConfig{
		Address:   unreachableAddr,
		APIPrefix: "",
		UpHook:    config.WebHook{Command: "exit 0", Name: "up"},
		DownHook:  config.WebHook{Command: "exit 0", Name: "down"},
		Port:      0,
		OnRestart: func(attempt int) {
			restartCount.Add(1)
		},
	})

	m.Start()
	defer m.Stop()

	waitForRestartCountAtLeast(t, &restartCount, 2, 2*time.Second)

	total, inWindow := m.FlapStats(flapHistoryWindow)
	observedRestarts := restartCount.Load()
	if int64(total) < observedRestarts {
		t.Fatalf("expected FlapStats total >= observed restarts %d, got %d", observedRestarts, total)
	}
	// Every flap just happened, so all of them must fall within the 24h window.
	if inWindow != total {
		t.Fatalf("expected FlapStats inWindow == total (all flaps are recent): inWindow=%d total=%d", inWindow, total)
	}
	if inWindow == 0 {
		t.Fatalf("expected at least one flap recorded in the window, got 0")
	}
}
