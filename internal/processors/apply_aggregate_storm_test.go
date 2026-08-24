package processors

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

// TestDispatchWithRetry_SustainedNonClearingBusy_ConcurrentDispatchesDoNotCoalesce
// reproduces mitto-hjx Facet C: the throughput/backpressure gap left after
// Facet A's admission gate (apply_admission_control_test.go, commit c90f4abb)
// and Facet B's per-dispatch busy ride-out (apply_busy_budget_test.go, commit
// d4b9abcc). Both prior fixes address a SINGLE busy window that eventually
// CLEARS. This test exercises a SUSTAINED, NON-clearing ErrProcessBusy window
// (mirroring an hour-long saturation caused by many concurrent conversations)
// across MULTIPLE concurrent same-workspace dispatches.
//
// Mechanics: admitDispatch's per-workspace gate (apply.go) only serializes the
// RPC-issuing window — it does not coalesce the ride-outs themselves. Dispatch
// #1 acquires the gate and rides out its own busyDeadline (bounded by its own
// `timeout`) before giving up; dispatch #2, which was blocked on the SAME
// gate the whole time, then acquires it and starts its OWN full busyDeadline
// ride-out from scratch — with no way to learn "we already know this
// workspace is busy, skip straight to spool." Aggregate wall-clock for N
// concurrent dispatches therefore scales ~linearly with N (N*timeout) instead
// of staying bounded near a single dispatch's ride-out budget.
//
// Acceptance (mitto-hjx facet C): aggregate wall-clock for a cluster of
// concurrent dispatches against a persistently-busy workspace should stay
// bounded near ONE dispatch's ride-out budget, not grow linearly with cluster
// size. This test FAILS today because dispatches serialize behind the
// admission gate and each independently burns a full ride-out.
func TestDispatchWithRetry_SustainedNonClearingBusy_ConcurrentDispatchesDoNotCoalesce(t *testing.T) {
	origBaseDelay := dispatchPromptRetryBaseDelay
	origBusyInterval := pendingDispatchBusyRetryInterval
	dispatchPromptRetryBaseDelay = time.Millisecond
	pendingDispatchBusyRetryInterval = 2 * time.Millisecond
	t.Cleanup(func() {
		dispatchPromptRetryBaseDelay = origBaseDelay
		pendingDispatchBusyRetryInterval = origBusyInterval
	})

	const workspaceUUID = "ws-sustained-nonclearing-busy"
	const clusterSize = 3
	const perDispatchTimeout = 60 * time.Millisecond

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	// The workspace is PERSISTENTLY busy for the whole test — unlike facet B's
	// single window that eventually clears, this never clears, modeling the
	// sustained hour-long saturation from many concurrent conversations
	// observed in the mitto-hjx 2026-08-24 recurrence.
	var calls atomic.Int32
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		calls.Add(1)
		return fmt.Errorf("failed to get auxiliary session: shared ACP process is saturated: %w", acperrors.ErrProcessBusy)
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < clusterSize; i++ {
		name := fmt.Sprintf("identify-user-data-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			m.dispatchWithRetry(workspaceUUID, name, "prompt", perDispatchTimeout,
				"prompt-mode processor dispatch skipped", "prompt-mode processor dispatch failed")
		}()
	}

	begin := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(begin)

	// Acceptance: aggregate wall-clock for the whole cluster should stay near
	// ONE dispatch's ride-out budget (coalesced), not scale linearly with
	// clusterSize. Generous 1.5x margin over a single dispatch's timeout to
	// absorb scheduling jitter while still failing hard against today's
	// ~clusterSize*perDispatchTimeout serialization.
	maxAllowed := perDispatchTimeout + perDispatchTimeout/2
	if elapsed >= maxAllowed {
		t.Fatalf("aggregate wall-clock for %d concurrent dispatches against a persistently-busy workspace = %v "+
			"(promptFunc calls=%d); want < %v (~1 dispatch's ride-out budget) — mitto-hjx facet C: each dispatch "+
			"independently burns a full busyDeadline ride-out serialized behind the per-workspace admission gate "+
			"instead of coalescing into one shared wait, so aggregate time scales ~linearly with the number of "+
			"concurrent same-workspace dispatches (%d*%v ≈ %v) instead of staying bounded",
			clusterSize, elapsed, calls.Load(), maxAllowed, clusterSize, perDispatchTimeout, time.Duration(clusterSize)*perDispatchTimeout)
	}
}
