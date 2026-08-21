package acpproc

import (
	"errors"
	"fmt"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// TestIsAgentQueryClosedErr_TruthTable mirrors the classifier truth table in
// acperrors.IsAgentQueryClosedErr for the package-private copy used directly
// by NewSession/LoadSession/isRetryableCreateError (mitto-aoo). The two
// implementations must classify identically.
func TestIsAgentQueryClosedErr_TruthTable(t *testing.T) {
	wedgeErr := &acp.RequestError{
		Code:    -32603,
		Message: "Internal error",
		Data:    map[string]string{"details": "Query closed before response received"},
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"exact wedge signature", wedgeErr, true},
		{"wedge wrapped by fmt.Errorf %w", fmt.Errorf("session/new: %w", wedgeErr), true},
		{
			"wrong code (-32000) same message",
			&acp.RequestError{Code: -32000, Message: "Internal error", Data: map[string]string{"details": "query closed before response received"}},
			false,
		},
		{
			"right code, deadline-exceeded message (the OTHER wedge)",
			&acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]string{"details": "context deadline exceeded"}},
			false,
		},
		{"plain non-RequestError error", errors.New("query closed before response received"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAgentQueryClosedErr(tt.err); got != tt.want {
				t.Errorf("isAgentQueryClosedErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsRetryableCreateError_WedgeIsRetryable verifies the wedge signature is
// treated as retryable so the bounded sessionCreateMaxAttempts loop in
// NewSession accumulates multiple recordRPCWedgeFailure() samples within a
// single call (mitto-aoo).
func TestIsRetryableCreateError_WedgeIsRetryable(t *testing.T) {
	wedgeErr := &acp.RequestError{
		Code:    -32603,
		Message: "Internal error",
		Data:    map[string]string{"details": "Query closed before response received"},
	}
	if !isRetryableCreateError(wedgeErr) {
		t.Fatal("expected the query-closed wedge to be retryable")
	}
}

// TestWedgeFailure_ConsecutiveTripsSaturation verifies recordRPCWedgeFailure
// feeds the SAME consecutive-failure fast path as recordRPCTimeout: three
// consecutive wedge replies trip saturation at level 1, mirroring
// TestSaturationRate_ConsecutiveFastPathStillTrips for the timeout case.
func TestWedgeFailure_ConsecutiveTripsSaturation(t *testing.T) {
	proc := newTestSharedProcess()

	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCWedgeFailure()
	}

	if !proc.IsSaturated() {
		t.Fatalf("expected IsSaturated()=true after %d consecutive wedge failures; level=%d", sessionSaturationTimeoutThreshold, proc.SaturationLevel())
	}
	if proc.SaturationLevel() != 1 {
		t.Fatalf("expected saturationLevel=1 on first consecutive trip, got %d", proc.SaturationLevel())
	}
}

// TestWedgeFailure_SuccessResetsConsecutiveCounter verifies a success between
// wedge failures resets the consecutive-failure fast path counter, so two
// wedges separated by a success do NOT trip saturation (same reset semantics
// recordRPCTimeout already has via recordRPCSuccess).
func TestWedgeFailure_SuccessResetsConsecutiveCounter(t *testing.T) {
	proc := newTestSharedProcess()

	proc.recordRPCWedgeFailure()
	proc.recordRPCWedgeFailure()
	proc.recordRPCSuccess()
	proc.recordRPCWedgeFailure()

	if proc.IsSaturated() {
		t.Fatalf("success between wedges should have reset the consecutive counter; level=%d", proc.SaturationLevel())
	}
	proc.saturationMu.Lock()
	consec := proc.consecutiveRPCTimeouts
	proc.saturationMu.Unlock()
	if consec != 1 {
		t.Fatalf("expected consecutiveRPCTimeouts=1 after reset + one wedge, got %d", consec)
	}
}

// TestWedgeFailure_DuringProbeEscalatesToConfirmedDegraded verifies a wedge
// failure that lands during the post-cooldown probe window immediately
// escalates to confirmed-degraded (saturationLevel 2), exactly like a timeout
// during probe (see driveToConfirmedDegraded / recordRPCFailureLocked's
// shared inProbe branch). This is the "wedge never heals on its own" case
// that originally produced 38 consecutive failures over 9h.
func TestWedgeFailure_DuringProbeEscalatesToConfirmedDegraded(t *testing.T) {
	proc := newTestSharedProcess()

	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCWedgeFailure()
	}
	if proc.SaturationLevel() != 1 {
		t.Fatalf("test setup: expected saturationLevel 1 after initial trip, got %d", proc.SaturationLevel())
	}

	// Force the cooldown to have already elapsed so isSaturated() opens the probe.
	proc.saturationMu.Lock()
	proc.saturatedUntil = time.Now().Add(-time.Millisecond)
	proc.saturationMu.Unlock()
	if proc.isSaturated() {
		t.Fatalf("test setup: expected isSaturated()=false once cooldown elapses (probe opens)")
	}
	proc.saturationMu.Lock()
	inProbe := proc.inProbe
	proc.saturationMu.Unlock()
	if !inProbe {
		t.Fatalf("test setup: expected inProbe=true after cooldown elapse")
	}

	// The probe RPC itself wedges (not a plain timeout) -> escalates to level 2.
	proc.recordRPCWedgeFailure()
	if !proc.IsConfirmedDegraded() {
		t.Fatalf("expected IsConfirmedDegraded()=true after wedge-during-probe, saturationLevel=%d", proc.SaturationLevel())
	}
}
