package processors

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

// TestFlushPendingDispatches_PersistentlyBusyHeadBlocksDeliverableTail
// reproduces mitto-pa1q.1 (head-of-line blocking in FlushPendingDispatches).
//
// When the head entry of the spool stays busy (ErrProcessBusy) for its
// entire per-entry timeout window, FlushPendingDispatches requeues the head
// AND every remaining entry (entries[i:]) and returns — even when those
// later entries' dispatch calls would have succeeded immediately, had they
// ever been attempted. This test seeds three entries ("A" head = always
// busy, "B" and "C" = would succeed on first try) and asserts that "B" and
// "C" are never invoked and are requeued back into the spool unattempted.
func TestFlushPendingDispatches_PersistentlyBusyHeadBlocksDeliverableTail(t *testing.T) {
	origMaxRetries := dispatchPromptMaxRetries
	dispatchPromptMaxRetries = 0
	t.Cleanup(func() { dispatchPromptMaxRetries = origMaxRetries })
	origRetryBaseDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origRetryBaseDelay })
	origBusyInterval := pendingDispatchBusyRetryInterval
	pendingDispatchBusyRetryInterval = 5 * time.Millisecond
	t.Cleanup(func() { pendingDispatchBusyRetryInterval = origBusyInterval })

	const wsUUID = "ws-hol-blocking"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	entries := []PendingDispatchEntry{
		// Small TimeoutSeconds so the busy-deadline expires quickly instead
		// of exhausting the test's own timeout budget.
		{WorkspaceUUID: wsUUID, Name: "A-always-busy", Prompt: "prompt-a", SavedAt: time.Now(), TimeoutSeconds: 0.05},
		{WorkspaceUUID: wsUUID, Name: "B-would-succeed", Prompt: "prompt-b", SavedAt: time.Now(), TimeoutSeconds: 0.05},
		{WorkspaceUUID: wsUUID, Name: "C-would-succeed", Prompt: "prompt-c", SavedAt: time.Now(), TimeoutSeconds: 0.05},
	}
	if err := store.Replace(wsUUID, entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	var bInvoked, cInvoked atomic.Bool
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	m.SetPromptFunc(func(_ context.Context, _, name, _ string) error {
		switch name {
		case "A-always-busy":
			// The shared ACP process never frees up for this entry: every
			// attempt sees the threshold-1 busy gate.
			return fmt.Errorf("failed to get auxiliary session: %w", acperrors.ErrProcessBusy)
		case "B-would-succeed":
			bInvoked.Store(true)
			return nil
		case "C-would-succeed":
			cInvoked.Store(true)
			return nil
		default:
			return fmt.Errorf("unexpected dispatch name %q", name)
		}
	})

	m.FlushPendingDispatches(context.Background(), wsUUID)

	remaining, err := store.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// EXPECTED (fixed) behavior: "B" and "C" are independently dispatchable
	// and must be attempted/delivered even though "A" never clears its busy
	// state. This assertion FAILS today (mitto-pa1q.1) because
	// FlushPendingDispatches requeues the whole remaining tail the moment
	// the head entry's busy-deadline expires, so neither dispatch func is
	// ever invoked for "B" or "C".
	if !bInvoked.Load() || !cInvoked.Load() {
		t.Fatalf("head-of-line blocking (mitto-pa1q.1): B-would-succeed invoked=%v, C-would-succeed invoked=%v; want both true — "+
			"a persistently-busy head entry must not prevent independently-deliverable tail entries from being attempted; remaining spool=%v",
			bInvoked.Load(), cInvoked.Load(), pendingDispatchNames(remaining))
	}

	t.Logf("B-would-succeed and C-would-succeed were both delivered despite A-always-busy staying busy; remaining spool=%v", pendingDispatchNames(remaining))
}
