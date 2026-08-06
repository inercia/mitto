//go:build integration

package inprocess

import (
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/session"
)

// TestLoopMultiTriggerRunOnStartE2E pins the mitto-r6j.2/.5 acceptance
// property for RunOnStart on a multi-trigger loop: when Triggers arms BOTH
// schedule and onCompletion (and RunOnStart=true), the boot pulse must
// deliver exactly ONE prompt for the session — not once per armed trigger.
// The once-per-process guard (runOnStartFired) is loop-wide, not
// per-trigger, so a subsequent FireOnStartPulses must be a no-op even
// though multiple triggers are armed.
//
// This complements the single-trigger TestLoopRunOnStartE2E: it exercises
// the same code path (FireOnStartPulses → triggerNowFull → deliverPrompt)
// but with a multi-trigger config, catching a regression where the runner
// might re-arm the pulse once per trigger under a multi-trigger loop.
func TestLoopMultiTriggerRunOnStartE2E(t *testing.T) {
	ts := SetupTestServer(t)
	runner := ts.Server.LoopRunner()

	runner.SetRunOnStartAntiFlapSeconds(300)
	runner.SetStartupDelay(200 * time.Millisecond)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "multitrigger-runonstart"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Configure a multi-trigger loop directly via the Store so we can set
	// Triggers=[schedule, onCompletion] (the REST client's SetLoopRequest
	// only exposes the legacy scalar `trigger`).
	//
	// Schedule frequency is 1 day so no scheduled fire races the boot pulse
	// during this test's runtime, and onCompletion has a large delay for
	// the same reason — only the boot pulse (RunOnStart) can drive
	// iteration_count during this test window.
	runOnStart := true
	loopCfg := &session.LoopPrompt{
		Prompt:       "boot pulse ping",
		Triggers:     []session.LoopTrigger{session.TriggerSchedule, session.TriggerOnCompletion},
		Frequency:    session.Frequency{Value: 1, Unit: session.FrequencyDays, At: "09:00"},
		DelaySeconds: 3600,
		Enabled:      true,
		RunOnStart:   &runOnStart,
	}
	if err := ts.Store.Loop(sess.SessionID).Set(loopCfg); err != nil {
		t.Fatalf("Store.Loop.Set failed: %v", err)
	}

	got, err := ts.Store.Loop(sess.SessionID).Get()
	if err != nil {
		t.Fatalf("Store.Loop.Get failed: %v", err)
	}
	if len(got.Triggers) != 2 || !got.HasTrigger(session.TriggerSchedule) || !got.HasTrigger(session.TriggerOnCompletion) {
		t.Fatalf("expected Triggers=[schedule onCompletion], got %v", got.Triggers)
	}
	if got.IterationCount != 0 {
		t.Fatalf("expected iteration_count=0 before boot pulse, got %d", got.IterationCount)
	}
	if runner.HasFiredRunOnStart(sess.SessionID) {
		t.Fatal("HasFiredRunOnStart=true before FireOnStartPulses; per-process guard already tripped")
	}

	// -------------------------------------------------------------------------
	// Boot pulse #1 on a multi-trigger loop: exactly ONE delivery expected.
	// -------------------------------------------------------------------------
	runner.FireOnStartPulses()

	if !runner.HasFiredRunOnStart(sess.SessionID) {
		t.Fatal("HasFiredRunOnStart=false after FireOnStartPulses; boot pulse was not dispatched")
	}

	waitLoopIterationCount(t, ts, sess.SessionID, 1)

	after, err := ts.Store.Loop(sess.SessionID).Get()
	if err != nil {
		t.Fatalf("Store.Loop.Get after boot pulse failed: %v", err)
	}
	if after.IterationCount != 1 {
		t.Errorf("after boot pulse: iteration_count = %d, want 1 (multi-trigger loop must fire exactly ONCE)",
			after.IterationCount)
	}

	waitLoopSessionIdle(t, ts, sess.SessionID)

	// -------------------------------------------------------------------------
	// Boot pulse #2 (idempotence on a multi-trigger loop): the runOnStartFired
	// guard is loop-wide, not per-trigger, so a second FireOnStartPulses must
	// remain a no-op even though multiple triggers are armed.
	// -------------------------------------------------------------------------
	runner.FireOnStartPulses()
	time.Sleep(500 * time.Millisecond)

	after2, err := ts.Store.Loop(sess.SessionID).Get()
	if err != nil {
		t.Fatalf("Store.Loop.Get after second FireOnStartPulses failed: %v", err)
	}
	if after2.IterationCount != 1 {
		t.Errorf("after second FireOnStartPulses on multi-trigger loop: iteration_count = %d, want 1 (idempotence)",
			after2.IterationCount)
	}
	if !runner.HasFiredRunOnStart(sess.SessionID) {
		t.Error("HasFiredRunOnStart flipped back to false; loop-wide guard must stay set")
	}
}
