//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"fmt"
	"testing"
	"time"

	"github.com/inercia/mitto/pkg/api"
)

// TestLoopRunOnStartE2E verifies the mitto-ystk boot-pulse trigger end-to-end
// against the mock ACP server.
//
// Contract under test:
//
//   - A loop configured with schedule trigger and RunOnStart=true fires exactly
//     once shortly after Mitto boots (i.e. once fireOnStartPulses runs).
//   - The delivered PromptMeta carries IsLoopRunOnStart=true; this is verified
//     indirectly through the observable side effects on the loop store
//     (iteration_count increments to 1) and the runner's per-process guard
//     (HasFiredRunOnStart(sessionID) becomes true) — both of which are only
//     set through the isRunOnStart=true code path in fireOnStartPulses ->
//     triggerNowFull -> deliverPrompt (see internal/conversation/loop_runner.go).
//   - A subsequent call to FireOnStartPulses does NOT deliver a second boot
//     pulse for the same session (once-per-process idempotence via the
//     runOnStartFired guard).
//
// The boot pulse is invoked directly via the exported LoopRunner test hook
// FireOnStartPulses rather than by waiting for pollLoop's default 15 s
// startupDelay: production startup delay is much longer than what an
// integration test can reasonably wait, and the test hook exercises the exact
// same code path that boot invokes.
func TestLoopRunOnStartE2E(t *testing.T) {
	ts := SetupTestServer(t)
	runner := ts.Server.LoopRunner()

	// Large anti-flap window so a rapid restart heuristic never suppresses the
	// pulse under test. In production this defaults to 60 s to catch fast
	// restarts; here we want the pulse to always fire on a fresh loop.
	runner.SetRunOnStartAntiFlapSeconds(300)
	// Short startup delay is only cosmetic here (we drive fireOnStartPulses
	// directly), but it mirrors the task brief and matches how a real boot
	// would reach the pulse promptly.
	runner.SetStartupDelay(200 * time.Millisecond)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "runonstart-boot"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure a schedule-triggered loop with RunOnStart=true. The schedule
	// (1 day) is intentionally long so no scheduled fire races the boot pulse
	// during this test's runtime; only the boot pulse (mitto-ystk) can drive
	// iteration_count to 1 in this window.
	runOnStart := true
	cfg, err := ts.Client.SetLoop(sess.SessionID, api.SetLoopRequest{
		Prompt:     "boot pulse ping",
		Triggers:   []string{"schedule"},
		Frequency:  api.LoopFrequency{Value: 1, Unit: "days", At: "09:00"},
		Enabled:    true,
		RunOnStart: &runOnStart,
	})
	if err != nil {
		t.Fatalf("SetLoop failed: %v", err)
	}
	if cfg.RunOnStart == nil || !*cfg.RunOnStart {
		t.Fatalf("expected RunOnStart=true after SetLoop, got %v", cfg.RunOnStart)
	}
	if cfg.IterationCount != 0 {
		t.Fatalf("expected iteration_count=0 before boot pulse, got %d", cfg.IterationCount)
	}

	// Sanity: no boot pulse has been dispatched yet.
	if runner.HasFiredRunOnStart(sess.SessionID) {
		t.Fatal("HasFiredRunOnStart=true before FireOnStartPulses; per-process guard already tripped")
	}

	// -------------------------------------------------------------------------
	// Boot pulse #1: fireOnStartPulses must deliver exactly ONE prompt, mark
	// the session in runOnStartFired, and increment iteration_count to 1.
	// -------------------------------------------------------------------------
	runner.FireOnStartPulses()

	// The pulse guard is set synchronously inside fireOnStartPulses (before
	// the async triggerNowFull dispatch), so it flips to true immediately.
	if !runner.HasFiredRunOnStart(sess.SessionID) {
		t.Fatal("HasFiredRunOnStart=false after FireOnStartPulses; boot pulse was not dispatched")
	}

	// The delivery itself is asynchronous; wait for iteration_count to reach 1.
	waitLoopIterationCount(t, ts, sess.SessionID, 1)

	got, err := ts.Client.GetLoop(sess.SessionID)
	if err != nil {
		t.Fatalf("GetLoop after boot pulse failed: %v", err)
	}
	if got.IterationCount != 1 {
		t.Errorf("after boot pulse: iteration_count = %d, want 1", got.IterationCount)
	}
	if got.RunOnStart == nil || !*got.RunOnStart {
		t.Errorf("after boot pulse: RunOnStart persisted flag = %v, want *true", got.RunOnStart)
	}

	waitLoopSessionIdle(t, ts, sess.SessionID)

	// -------------------------------------------------------------------------
	// Boot pulse #2 (idempotence): calling FireOnStartPulses a second time
	// must be a no-op for this session — the once-per-process guard blocks
	// re-firing. iteration_count must stay at 1.
	// -------------------------------------------------------------------------
	runner.FireOnStartPulses()

	// Give any (unwanted) async dispatch a window to land; 500ms is plenty
	// since deliverPrompt increments iteration_count synchronously in
	// RecordSent before dispatching the ACP prompt in a goroutine.
	time.Sleep(500 * time.Millisecond)

	got2, err := ts.Client.GetLoop(sess.SessionID)
	if err != nil {
		t.Fatalf("GetLoop after second FireOnStartPulses failed: %v", err)
	}
	if got2.IterationCount != 1 {
		t.Errorf("after second FireOnStartPulses: iteration_count = %d, want 1 (idempotence)", got2.IterationCount)
	}
	if !runner.HasFiredRunOnStart(sess.SessionID) {
		t.Error("HasFiredRunOnStart flipped back to false after second call; guard should stay set")
	}

	// -------------------------------------------------------------------------
	// A regular RunOnce() poll cycle must not resurrect the boot pulse either.
	// (Schedule is 1 day away so RunOnce will not naturally fire the loop.)
	// -------------------------------------------------------------------------
	runner.RunOnce()
	time.Sleep(200 * time.Millisecond)

	got3, err := ts.Client.GetLoop(sess.SessionID)
	if err != nil {
		t.Fatalf("GetLoop after RunOnce failed: %v", err)
	}
	if got3.IterationCount != 1 {
		t.Errorf("after RunOnce: iteration_count = %d, want 1 (RunOnce must not re-fire the boot pulse)", got3.IterationCount)
	}
}

// waitLoopIterationCount polls until the loop's iteration_count reaches want.
func waitLoopIterationCount(t *testing.T, ts *TestServer, sessionID string, want int) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		got, err := ts.Client.GetLoop(sessionID)
		return err == nil && got.IterationCount == want
	}, fmt.Sprintf("iteration_count to reach %d for session %s", want, sessionID))
}

// waitLoopSessionIdle polls until the session is no longer prompting.
func waitLoopSessionIdle(t *testing.T, ts *TestServer, sessionID string) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		bs := ts.Server.GetSessionManager().GetSession(sessionID)
		return bs != nil && !bs.IsPrompting()
	}, "session "+sessionID+" to go idle")
}
