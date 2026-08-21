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

// TestDispatchPromptBatch_PostTurnCluster_NoAdmissionControl_StampedesSharedProcess
// reproduces mitto-hjx: the shared ACP process is a single concurrency-limited
// resource guarded by a proactive threshold-1 load-shed (mitto-9gt,
// auxSessionCreateBusyRPCThreshold=1 in acp_process_manager.go). When several
// conversations in the same workspace finish turns near-simultaneously (a
// "post-turn cluster"), each fires its own prompt-mode processor dispatch via
// dispatchPromptBatch, which spawns dispatchWithRetry with a bare `go` and NO
// jitter/staggering/admission-control. Every concurrent dispatch races for the
// same threshold-1 slot; only one can win, and — because ErrProcessBusy is
// deliberately excluded from isSaturationDispatchErr (apply.go:1827) and so
// only gets the short ordinary retry budget (dispatchPromptMaxRetries=2,
// exponential backoff from dispatchPromptRetryBaseDelay) — every loser
// exhausts its retries and is persisted-for-retry at ERROR before the winner
// even finishes its RPC. In production this produced 18 "batch persisted for
// later retry" ERRORs / 35 WARNs across repeated bursts (see bead evidence).
//
// Acceptance criteria (mitto-hjx) requires a post-turn cluster to no longer
// produce a sustained load-shedding storm: ERRORs should be "isolated events,
// not 18+ per window" — i.e. at most one shed per cluster, not clusterSize-1.
// This test fails today because there is no admission control: it asserts at
// most 1 entry lands in the persisted-dispatch spool after one cluster of 4
// concurrent processors, but the current stampede persists clusterSize-1 (3).
func TestDispatchPromptBatch_PostTurnCluster_NoAdmissionControl_StampedesSharedProcess(t *testing.T) {
	origDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = 2 * time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origDelay })

	const workspaceUUID = "ws-post-turn-cluster"
	const clusterSize = 4
	// Real ACP session/new RPCs take tens of ms; the fake slot-holder sleeps
	// long enough that every loser's whole ordinary retry budget (attempt1 +
	// 2ms + 4ms backoff, well under 10ms) is guaranteed to expire while the
	// winner is still holding the single threshold-1 slot.
	const rpcHoldDuration = 100 * time.Millisecond

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	// Simulate the shared ACP process's proactive threshold-1 load-shed
	// (auxSessionCreateBusyRPCThreshold=1, acp_process_manager.go:1171):
	// exactly one auxiliary session/new can be in flight; any concurrent
	// caller is bailed immediately with ErrProcessBusy, exactly like
	// getOrCreateAuxiliarySession does at acp_process_manager.go:1396-1416.
	var slotHeld atomic.Bool
	m.SetPromptFunc(func(_ context.Context, _, _, _ string) error {
		if !slotHeld.CompareAndSwap(false, true) {
			return fmt.Errorf("failed to get auxiliary session: shared ACP process is saturated: %w", acperrors.ErrProcessBusy)
		}
		defer slotHeld.Store(false)
		time.Sleep(rpcHoldDuration)
		return nil
	})

	// Fire a post-turn cluster: clusterSize independent processor dispatches
	// (as if clusterSize conversations in the same workspace ended their
	// turns near-simultaneously) with no coordination between them, exactly
	// as dispatchPromptBatch does today (bare `go m.dispatchWithRetry(...)`).
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < clusterSize; i++ {
		name := fmt.Sprintf("identify-user-data-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			m.dispatchWithRetry(workspaceUUID, name, "prompt", time.Second,
				"prompt-mode processor dispatch skipped", "prompt-mode processor dispatch failed")
		}()
	}
	close(start)
	wg.Wait()

	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) > 1 {
		t.Fatalf("pending-dispatch spool after one post-turn cluster of %d processors = %d entries (%v); "+
			"want at most 1 isolated shed — mitto-hjx: no admission control/smoothing lets %d concurrent "+
			"dispatches all race the threshold-1 aux-session slot and exhaust their short ordinary retry "+
			"budget simultaneously instead of being staggered",
			clusterSize, len(entries), pendingDispatchNames(entries), clusterSize-1)
	}

	var shedErrors int
	for _, rec := range handler.snapshot() {
		if rec.Level == slog.LevelError &&
			rec.Message == "prompt-mode processor dispatch failed; batch persisted for later retry" {
			shedErrors++
		}
	}
	if shedErrors > 1 {
		t.Fatalf("persisted-for-retry ERROR log lines = %d, want at most 1 isolated shed per post-turn "+
			"cluster (acceptance criteria: not a sustained storm)", shedErrors)
	}
}
