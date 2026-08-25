package processors

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

// TestDispatchWithRetry_SustainedNonClearingBusy_NoAttemptCountCircuitBreaker
// reproduces mitto-qvs: dispatchWithRetry's busy ride-out loop (apply.go,
// mitto-hjx facet B/C) bounds a SUSTAINED, NON-clearing acperrors.ErrProcessBusy
// window ONLY by wall-clock (busyDeadline = observeSustainedBusy(...).Add(timeout))
// — there is no cap on the TOTAL number of RPC attempts made while riding it
// out. Each outer-loop iteration re-invokes runDispatchRetryLoopTracked, which
// (because ErrProcessBusy is excluded from isSaturationDispatchErr,
// apply.go:1827, mitto-xhsj) burns its own dispatchPromptMaxRetries+1 (=3)
// attempts with exponential backoff before returning, then the outer loop
// sleeps only pendingDispatchBusyRetryInterval (100ms) before looping again —
// with NO jitter and NO attempt-count cap. Against a persistently-busy shared
// process (e.g. a sustained multi-workspace RPC burst), this produced the
// reported 63-attempts/7m6s churn for a single close-phase batch
// (memorize-preferences + friends, timeout: 300s) before eventually failing
// on a doomed 60s NewSession burn — see the mitto-qvs Investigation comment
// for the full root-cause trace.
//
// This facet is orthogonal to apply_aggregate_storm_test.go (which covers N
// CONCURRENT dispatches failing to coalesce their ride-outs): this test uses
// a SINGLE dispatch and asserts on total RPC ATTEMPT COUNT, not aggregate
// wall-clock across siblings.
//
// Acceptance (mitto-qvs): a busy ride-out against a persistently-saturated
// process should be bounded by a reasonable ATTEMPT-COUNT circuit breaker
// (not just wall-clock), so it fails fast and spools instead of churning
// dozens of RPC attempts. This test FAILS today because dispatchWithRetry has
// no such cap: within a short, test-shrunk timeout window, promptFunc is
// still called far more than any reasonable retry budget.
func TestDispatchWithRetry_SustainedNonClearingBusy_NoAttemptCountCircuitBreaker(t *testing.T) {
	origBaseDelay := dispatchPromptRetryBaseDelay
	origBusyInterval := pendingDispatchBusyRetryInterval
	dispatchPromptRetryBaseDelay = time.Millisecond
	pendingDispatchBusyRetryInterval = time.Millisecond
	t.Cleanup(func() {
		dispatchPromptRetryBaseDelay = origBaseDelay
		pendingDispatchBusyRetryInterval = origBusyInterval
	})

	const workspaceUUID = "ws-qvs-sustained-nonclearing-busy-single"
	// A single dispatch (no concurrent siblings) against a shared process
	// that NEVER clears its busy signal for the whole ride-out window.
	const timeout = 300 * time.Millisecond

	// maxReasonableAttempts is the acceptance bound: a sane circuit breaker
	// on the busy ride-out (e.g. capping at a handful of RPC attempts,
	// mirroring dispatchPromptMaxRetries's shape for ordinary errors) should
	// keep total attempts well under this even while riding out the full
	// wall-clock window. Deliberately generous (5x the ordinary
	// dispatchPromptMaxRetries+1 budget) so the test only fails on the
	// reported unbounded-churn behavior, not on reasonable timing jitter.
	maxReasonableAttempts := 5 * (dispatchPromptMaxRetries + 1)

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	var calls atomic.Int32
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		calls.Add(1)
		return fmt.Errorf("failed to get auxiliary session: shared ACP process is saturated: %w", acperrors.ErrProcessBusy)
	})

	m.dispatchWithRetry(workspaceUUID, "extract-memories-on-close", "prompt", timeout,
		"prompt-mode processor dispatch skipped", "prompt-mode processor dispatch failed")

	if got := calls.Load(); got > int32(maxReasonableAttempts) {
		t.Fatalf("promptFunc call count = %d, want <= %d (a reasonable attempt-count circuit breaker) — "+
			"mitto-qvs: dispatchWithRetry's busy ride-out bounds a persistently-busy window ONLY by "+
			"wall-clock (timeout=%v), with no cap on total RPC attempts, letting a single sustained-busy "+
			"dispatch churn far more attempts than any ordinary retry budget before giving up",
			got, maxReasonableAttempts, timeout)
	}
}
