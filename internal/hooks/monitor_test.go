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
