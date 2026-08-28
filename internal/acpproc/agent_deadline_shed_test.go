package acpproc

import (
	"testing"
	"time"
)

// TestAgentInternalDeadline_ArmsAfterSingleOccurrence verifies mitto-pic:
// unlike the consecutive-timeout fast path (sessionSaturationTimeoutThreshold=3
// failures needed before IsSaturated() trips), RecentlyHitAgentInternalDeadline()
// arms after a SINGLE recordAgentInternalDeadline() call.
func TestAgentInternalDeadline_ArmsAfterSingleOccurrence(t *testing.T) {
	proc := newTestSharedProcess()

	if proc.RecentlyHitAgentInternalDeadline() {
		t.Fatal("expected RecentlyHitAgentInternalDeadline()=false before any occurrence")
	}

	proc.recordAgentInternalDeadline()

	if !proc.RecentlyHitAgentInternalDeadline() {
		t.Fatal("expected RecentlyHitAgentInternalDeadline()=true after a single occurrence")
	}
}

// TestAgentInternalDeadline_CooldownExpires verifies the marker self-clears once
// auxAgentDeadlineShedCooldown has elapsed, bounding the over-shedding risk if the
// process recovers without a foreground RPC succeeding to clear it explicitly.
func TestAgentInternalDeadline_CooldownExpires(t *testing.T) {
	proc := newTestSharedProcess()
	proc.recordAgentInternalDeadline()

	// Backdate the marker past the cooldown window (same pattern other tests in
	// this package use to force saturatedUntil into the past — see wedge_test.go
	// TestWedgeFailure_DuringProbeEscalatesToConfirmedDegraded).
	proc.saturationMu.Lock()
	proc.lastAgentInternalDeadlineAt = time.Now().Add(-auxAgentDeadlineShedCooldown - time.Second)
	proc.saturationMu.Unlock()

	if proc.RecentlyHitAgentInternalDeadline() {
		t.Fatal("expected RecentlyHitAgentInternalDeadline()=false once the cooldown has elapsed")
	}
}

// TestAgentInternalDeadline_ClearedByRecordRPCSuccess verifies a genuine
// recovery (recordRPCSuccess) clears the marker immediately rather than waiting
// out the full cooldown, mirroring the existing saturation-state reset semantics.
func TestAgentInternalDeadline_ClearedByRecordRPCSuccess(t *testing.T) {
	proc := newTestSharedProcess()
	proc.recordAgentInternalDeadline()

	if !proc.RecentlyHitAgentInternalDeadline() {
		t.Fatal("test setup: expected marker to be armed before recordRPCSuccess")
	}

	proc.recordRPCSuccess()

	if proc.RecentlyHitAgentInternalDeadline() {
		t.Fatal("expected recordRPCSuccess to clear the agent-internal-deadline marker")
	}
}

// TestAgentInternalDeadline_NotArmedByPlainRPCTimeout verifies the marker is
// scoped ONLY to the agent-internal-deadline flavor of failure (mitto-pic
// plan: "do NOT broaden to plain context.DeadlineExceeded / query-closed in
// this increment"). recordRPCTimeout() alone — used for both the agent-
// internal-deadline site and the plain-DeadlineExceeded site in NewSession —
// must not arm this marker; only the dedicated recordAgentInternalDeadline()
// call does.
func TestAgentInternalDeadline_NotArmedByPlainRPCTimeout(t *testing.T) {
	proc := newTestSharedProcess()

	proc.recordRPCTimeout()
	proc.recordRPCWedgeFailure()

	if proc.RecentlyHitAgentInternalDeadline() {
		t.Fatal("recordRPCTimeout/recordRPCWedgeFailure must not arm RecentlyHitAgentInternalDeadline; only recordAgentInternalDeadline does")
	}
}

// TestAgentInternalDeadline_IndependentOfSaturationState verifies arming the
// agent-internal-deadline marker does not itself perturb the unrelated
// saturation counters (consecutiveRPCTimeouts / saturationLevel) — it is a
// companion signal, not a replacement for the existing state machine.
func TestAgentInternalDeadline_IndependentOfSaturationState(t *testing.T) {
	proc := newTestSharedProcess()

	proc.recordAgentInternalDeadline()

	proc.saturationMu.Lock()
	consec := proc.consecutiveRPCTimeouts
	level := proc.saturationLevel
	proc.saturationMu.Unlock()

	if consec != 0 || level != 0 {
		t.Fatalf("recordAgentInternalDeadline must not touch saturation counters; consecutiveRPCTimeouts=%d saturationLevel=%d", consec, level)
	}
	if proc.IsSaturated() {
		t.Fatal("recordAgentInternalDeadline alone must not flip IsSaturated()")
	}
}
