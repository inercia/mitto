//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestPeriodicOnCompletionE2E verifies the on-completion periodic trigger and
// maxDuration auto-stop end-to-end against the mock ACP server.
//
// Trigger flow recap:
//
//   - After each turn completes, OnConversationIdle fires the next run after
//     DelaySeconds (clamped to the global floor, default 5 s).
//   - RunPeriodicNow boots the loop: it delivers run 1 and, via OnConversationIdle,
//     arms the ~5 s timer for the next auto-fire.
//   - max_iterations: once iteration_count >= cap the runner sets enabled=false.
//   - max_duration:   at the next firing, if now-FirstRunAt >= MaxDurationSeconds,
//     the runner sets enabled=false WITHOUT delivering (so iteration_count stays at 1).
//
// Note: GetPeriodic polling is the auto-stop assertion; the disable and the
// WebSocket broadcast are the same server action so no WS observer is needed.
func TestPeriodicOnCompletionE2E(t *testing.T) {
	ts := SetupTestServer(t)

	// -------------------------------------------------------------------------
	// Subtest 1: max_iterations auto-stop
	//
	//   Configure MaxIterations=2.  RunPeriodicNow delivers run 1 (count→1) and
	//   arms a 5 s timer.  After ~5 s the timer fires, delivers run 2 (count→2),
	//   and the runner disables the periodic (count >= cap).
	// -------------------------------------------------------------------------
	t.Run("max_iterations_auto_stop", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "oncomplete-maxiter"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		// Zero Frequency is intentional: onCompletion skips frequency validation.
		// DelaySeconds=0 is clamped to the server floor (5 s).
		cfg, err := ts.Client.SetPeriodic(sess.SessionID, client.SetPeriodicRequest{
			Prompt:        "ping",
			Trigger:       "onCompletion",
			DelaySeconds:  0,
			MaxIterations: 2,
			Enabled:       true,
		})
		if err != nil {
			t.Fatalf("SetPeriodic failed: %v", err)
		}
		if cfg.Trigger != "onCompletion" {
			t.Fatalf("expected trigger=onCompletion, got %q", cfg.Trigger)
		}
		if !cfg.Enabled {
			t.Fatalf("expected enabled=true after SetPeriodic, got false")
		}

		// Boot the loop: delivers run 1, sets FirstRunAt, increments iteration_count
		// to 1, and arms the on-completion timer (~5 s) via OnConversationIdle.
		if err := ts.Client.RunPeriodicNow(sess.SessionID, true); err != nil {
			t.Fatalf("RunPeriodicNow failed: %v", err)
		}

		// Poll until the runner disables the periodic after reaching MaxIterations=2.
		deadline := time.Now().Add(30 * time.Second)
		var last *client.PeriodicConfig
		for time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			got, err := ts.Client.GetPeriodic(sess.SessionID)
			if err != nil {
				t.Logf("GetPeriodic transient error: %v", err)
				continue
			}
			last = got
			if !got.Enabled {
				break
			}
		}

		if last == nil || last.Enabled {
			var enabled bool
			var count int
			if last != nil {
				enabled, count = last.Enabled, last.IterationCount
			}
			t.Fatalf("periodic not auto-stopped within 30 s: enabled=%v iteration_count=%d", enabled, count)
		}
		if last.IterationCount != 2 {
			t.Errorf("expected iteration_count=2 at auto-stop, got %d", last.IterationCount)
		}
		t.Logf("max_iterations_auto_stop: stopped at iteration_count=%d ✓", last.IterationCount)
	})

	// -------------------------------------------------------------------------
	// Subtest 2: max_duration auto-stop
	//
	//   Configure MaxDurationSeconds=4.  RunPeriodicNow delivers run 1 (count→1,
	//   FirstRunAt=T0) and arms a ~5 s timer.  At T0+5 s the timer fires; elapsed
	//   (≈5 s) >= MaxDurationSeconds (4 s) so the runner disables WITHOUT
	//   delivering run 2 — iteration_count remains 1.
	// -------------------------------------------------------------------------
	t.Run("max_duration_auto_stop", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "oncomplete-maxdur"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		cfg, err := ts.Client.SetPeriodic(sess.SessionID, client.SetPeriodicRequest{
			Prompt:             "ping",
			Trigger:            "onCompletion",
			DelaySeconds:       0, // clamped to 5 s floor
			MaxDurationSeconds: 4, // elapsed ≈5 s at next firing >= 4 s cap → stop
			MaxIterations:      0, // no iteration cap
			Enabled:            true,
		})
		if err != nil {
			t.Fatalf("SetPeriodic failed: %v", err)
		}
		if cfg.Trigger != "onCompletion" {
			t.Fatalf("expected trigger=onCompletion, got %q", cfg.Trigger)
		}
		if !cfg.Enabled {
			t.Fatalf("expected enabled=true after SetPeriodic, got false")
		}

		// Boot the loop: run 1 delivered, FirstRunAt=now, count→1, timer armed (~5 s).
		if err := ts.Client.RunPeriodicNow(sess.SessionID, true); err != nil {
			t.Fatalf("RunPeriodicNow failed: %v", err)
		}

		// Poll until the runner disables (max duration reached at the next firing).
		deadline := time.Now().Add(30 * time.Second)
		var last *client.PeriodicConfig
		for time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			got, err := ts.Client.GetPeriodic(sess.SessionID)
			if err != nil {
				t.Logf("GetPeriodic transient error: %v", err)
				continue
			}
			last = got
			if !got.Enabled {
				break
			}
		}

		if last == nil || last.Enabled {
			var enabled bool
			var count int
			if last != nil {
				enabled, count = last.Enabled, last.IterationCount
			}
			t.Fatalf("periodic not auto-stopped within 30 s (max_duration): enabled=%v iteration_count=%d", enabled, count)
		}
		// The second run must NOT have been delivered; the stop preceded delivery.
		if last.IterationCount != 1 {
			t.Errorf("expected iteration_count=1 (no second delivery), got %d", last.IterationCount)
		}
		t.Logf("max_duration_auto_stop: stopped at iteration_count=%d ✓", last.IterationCount)
	})
}
