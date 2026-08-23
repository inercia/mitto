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

// TestDispatchWithRetry_SustainedProcessBusy_PersistsInsteadOfRidingOutBusyWindow
// reproduces mitto-hjx Facet B (the recurrence root cause after the admission-
// control fix in apply_admission_control_test.go, which only addresses
// aux-vs-aux sibling stampedes): a SINGLE close-phase dispatch that keeps
// observing acperrors.ErrProcessBusy — e.g. because one foreground user-facing
// RPC holds the shared process's threshold-1 slot
// (auxSessionCreateBusyRPCThreshold, acp_process_manager.go:1171) for longer
// than a few seconds — has no admission-gate contention to wait on, yet still
// fails.
//
// ErrProcessBusy is deliberately excluded from isSaturationDispatchErr
// (apply.go:1827, mitto-xhsj) because it is not GC-recycle-shaped, so it falls
// into the ordinary retry branch bounded by dispatchPromptMaxRetries (2 => 3
// total attempts, ~6s with exponential backoff). Once busyAttempts below
// exceeds that budget, dispatchWithRetry gives up and persists the batch to
// the durable spool at ERROR — even though the busy condition is purely
// transient and about to clear, exactly like FlushPendingDispatches' OWN
// busyDeadline poll loop (apply.go:2328-2371) already handles it correctly on
// the flush path. This asymmetry (initial dispatch: short give-up; flush:
// long poll) is what produced the observed 09:00 storm (57 min, 332 sheds,
// representative attempts=3 waited=6.0s) even after the admission-control fix
// (c90f4abb) closed the aux-vs-aux stampede.
//
// This test is orthogonal to
// TestDispatchPromptBatch_PostTurnCluster_NoAdmissionControl_StampedesSharedProcess:
// that test exercises N CONCURRENT siblings racing the threshold-1 slot and is
// already green; this test exercises ONE dispatch against a SUSTAINED busy
// signal that outlasts the ordinary retry budget, which the admission gate
// cannot help with (there is no sibling to serialize against — the
// contention is aux-vs-user, not aux-vs-aux).
//
// Acceptance (mitto-hjx): the dispatch should ride a longer busy-wait bucket
// and be DELIVERED once the busy signal clears, not persisted at ERROR. This
// test fails today because dispatchWithRetry gives up after
// dispatchPromptMaxRetries+1 attempts (3), well before busyAttempts (5)
// clears.
func TestDispatchWithRetry_SustainedProcessBusy_PersistsInsteadOfRidingOutBusyWindow(t *testing.T) {
	origDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origDelay })

	const workspaceUUID = "ws-sustained-busy"
	// dispatchPromptMaxRetries=2 (3 total attempts) is exhausted well before
	// this many attempts; a proper busy-wait bucket (mirroring
	// FlushPendingDispatches' busyDeadline loop) would keep polling past 3
	// attempts and eventually deliver once the busy signal clears.
	const busyAttempts = 5

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	var calls atomic.Int32
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		n := calls.Add(1)
		if n <= busyAttempts {
			return fmt.Errorf("failed to get auxiliary session: shared ACP process is saturated: %w", acperrors.ErrProcessBusy)
		}
		return nil
	})

	m.dispatchWithRetry(workspaceUUID, "extract-memories-on-close", "prompt", time.Second,
		"prompt-mode processor dispatch skipped", "prompt-mode processor dispatch failed")

	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending-dispatch spool after a sustained (but eventually clearing) ErrProcessBusy window = %d entries (%v); "+
			"want 0 — mitto-hjx facet B: a busy signal that outlasts the ordinary retry budget "+
			"(dispatchPromptMaxRetries=%d, ~%v total) should ride a longer busy-wait bucket "+
			"(mirroring FlushPendingDispatches' busyDeadline loop) and be DELIVERED, not persisted at ERROR",
			len(entries), pendingDispatchNames(entries), dispatchPromptMaxRetries, dispatchPromptRetryBaseDelay*4)
	}
	if got := calls.Load(); got <= int32(dispatchPromptMaxRetries+1) {
		t.Fatalf("promptFunc call count = %d, want > %d (dispatchPromptMaxRetries+1) — the dispatch gave up "+
			"after the short ordinary budget instead of continuing to poll through the busy window until it cleared",
			got, dispatchPromptMaxRetries+1)
	}
}
