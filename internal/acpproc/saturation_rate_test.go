package acpproc

import (
	"testing"
)

// TestSaturationRate_IntermittentStormTripsRate verifies the mitto-5eq acceptance
// criterion (a): an intermittently-degraded process — full-deadline timeouts
// interleaved with successes and budget-exhaustion bails, none of them 3-in-a-row —
// is flagged saturated by the new rate/rolling-window trigger even though the
// consecutive-timeout fast path alone would miss it.
func TestSaturationRate_IntermittentStormTripsRate(t *testing.T) {
	proc := newTestSharedProcess()

	// Interleave the events so consecutiveRPCTimeouts never reaches
	// sessionSaturationTimeoutThreshold (3): each timeout/bail run is broken up
	// by a success that zeroes the consecutive counter. This is the exact real
	// pattern from the 2026-07-06 incident (2065 interspersed successes reset
	// the counter every time). Sequence: T,S,B,T,S,B,T,S,T,B,T,S,B — 6 timeouts,
	// 4 bails, 4 successes → 10/14 ≈ 71% failure ratio over 14 samples, which is
	// ≥ saturationWindowFailRatio (0.5) with total ≥ saturationWindowMinSamples (8).
	sequence := []string{"T", "S", "B", "T", "S", "B", "T", "S", "T", "B", "T", "S", "B", "T"}
	for _, ev := range sequence {
		switch ev {
		case "T":
			proc.recordRPCTimeout()
		case "B":
			proc.recordRPCBudgetBail()
		case "S":
			// A success intentionally clears saturatedUntil/consecutive counter
			// (preserved semantics) but the rolling-window sample survives.
			proc.recordRPCSuccess()
		}
	}

	if !proc.IsSaturated() {
		t.Fatalf("expected IsSaturated()=true after intermittent storm; level=%d", proc.SaturationLevel())
	}
	// The consecutive counter must NEVER have crossed threshold on this
	// sequence — a success comes after every 1-2 failures. If the rate trigger
	// weren't in place this test would fail.
	proc.saturationMu.Lock()
	consec := proc.consecutiveRPCTimeouts
	proc.saturationMu.Unlock()
	if consec >= sessionSaturationTimeoutThreshold {
		t.Fatalf("test setup invariant: consecutive counter reached %d, threshold %d — sequence needs more interleaved successes", consec, sessionSaturationTimeoutThreshold)
	}
}

// TestSaturationRate_SteadyHealthyDoesNotTrip verifies acceptance criterion:
// a mostly-healthy process (occasional single timeout below the fail-ratio
// threshold) is NOT flagged. Guards against a false-positive on light traffic.
func TestSaturationRate_SteadyHealthyDoesNotTrip(t *testing.T) {
	proc := newTestSharedProcess()

	// 30 successes with 1 timeout sprinkled in → 1/31 ≈ 3.2% failure ratio,
	// well below saturationWindowFailRatio (0.5).
	for i := 0; i < 15; i++ {
		proc.recordRPCSuccess()
	}
	proc.recordRPCTimeout()
	for i := 0; i < 15; i++ {
		proc.recordRPCSuccess()
	}

	if proc.IsSaturated() {
		t.Fatalf("steady-healthy process should NOT be saturated; level=%d", proc.SaturationLevel())
	}
	if proc.SaturationLevel() != 0 {
		t.Fatalf("expected saturationLevel=0, got %d", proc.SaturationLevel())
	}
}

// TestSaturationRate_MinSamplesGuard verifies the min-sample guard prevents a
// 1/1 (or otherwise tiny) failure ratio from tripping the rate trigger. This is
// the primary false-positive-on-light-traffic protection.
func TestSaturationRate_MinSamplesGuard(t *testing.T) {
	proc := newTestSharedProcess()

	// Only 1 timeout + 1 bail = 2 samples, well below saturationWindowMinSamples.
	// Ratio 100% but sample size too small → must not trip.
	proc.recordRPCTimeout()
	proc.recordRPCBudgetBail()

	if proc.IsSaturated() {
		t.Fatalf("rate trigger fired below min-sample threshold; level=%d", proc.SaturationLevel())
	}
}

// TestSaturationRate_BudgetBailOnlyStormTrips verifies acceptance criterion (c):
// a storm made ENTIRELY of budget-exhaustion bails (the dominant real failure mode
// observed in the incident — 17 SetSessionModel failures via
// shouldFailFastCreateAttempt) trips the rate trigger. Before mitto-5eq those bails
// intentionally skipped recordRPCTimeout, so this scenario produced ZERO
// saturation signal and no GC recycle.
func TestSaturationRate_BudgetBailOnlyStormTrips(t *testing.T) {
	proc := newTestSharedProcess()

	for i := 0; i < saturationWindowMinSamples; i++ {
		proc.recordRPCBudgetBail()
	}

	if !proc.IsSaturated() {
		t.Fatalf("expected IsSaturated()=true after %d budget bails; level=%d", saturationWindowMinSamples, proc.SaturationLevel())
	}
	// The consecutive counter must be untouched — budget bails feed only the
	// rate signal, never the consecutive fast path (see recordRPCBudgetBail
	// doc comment for rationale).
	proc.saturationMu.Lock()
	consec := proc.consecutiveRPCTimeouts
	proc.saturationMu.Unlock()
	if consec != 0 {
		t.Fatalf("recordRPCBudgetBail must not touch consecutiveRPCTimeouts; got %d", consec)
	}
}

// TestSaturationRate_ConsecutiveFastPathStillTrips verifies acceptance criterion
// (d): the pre-existing consecutive-timeout fast path is unaffected. Three
// back-to-back full timeouts (below the rate trigger's min-sample size) must still
// trip saturation via the classic path — this is the fully-wedged-process case
// which must NOT regress.
func TestSaturationRate_ConsecutiveFastPathStillTrips(t *testing.T) {
	proc := newTestSharedProcess()

	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}

	if !proc.IsSaturated() {
		t.Fatalf("consecutive fast path did not trip after %d timeouts; level=%d", sessionSaturationTimeoutThreshold, proc.SaturationLevel())
	}
	if proc.SaturationLevel() != 1 {
		t.Fatalf("expected saturationLevel=1 on first consecutive trip, got %d", proc.SaturationLevel())
	}
}

// TestSaturationRate_SuccessDoesNotWipeWindow verifies the design decision
// documented on recordRPCSuccess: a success adds a sample to the rolling window
// but does NOT clear the window itself. If it did, an intermittent-storm process
// could always be "reset" by a single lucky success and we'd reintroduce the exact
// reset-on-success bug that made the consecutive-timeout trigger inert. Concretely:
// after a rate-trip is cleared by one success, the very next timeout must re-trip
// via the still-populated window rather than start from scratch.
func TestSaturationRate_SuccessDoesNotWipeWindow(t *testing.T) {
	proc := newTestSharedProcess()

	// Drive to a rate-based trip with lots of bails and a couple of successes.
	for i := 0; i < saturationWindowMinSamples; i++ {
		proc.recordRPCBudgetBail()
	}
	if !proc.IsSaturated() {
		t.Fatalf("test setup: rate trigger did not fire after %d bails", saturationWindowMinSamples)
	}

	// A single success clears the fast-path state (existing preserved semantics)…
	proc.recordRPCSuccess()
	if proc.IsSaturated() {
		t.Fatalf("recordRPCSuccess did not clear saturatedUntil (fast-path reset semantics broken)")
	}

	// …but the next single timeout must immediately re-trip via the still-live
	// rolling window (bails still counted, ratio still ≥ threshold, samples
	// still above minimum). This is the whole point of not wiping the window.
	proc.recordRPCTimeout()
	if !proc.IsSaturated() {
		t.Fatalf("expected re-trip on next timeout because rolling window was not wiped; level=%d", proc.SaturationLevel())
	}
}
