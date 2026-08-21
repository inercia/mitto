package processors

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

// TestDispatchWithRetry_SaturationThenNoSharedProcess_PersistsBatch reproduces
// mitto-jvl2: GC can recycle a saturated process between dispatch attempts, but
// the terminal no-shared-process result must defer the close-phase batch rather
// than bypassing the durable workspace spool.
func TestDispatchWithRetry_SaturationThenNoSharedProcess_PersistsBatch(t *testing.T) {
	origInterval := dispatchSaturationRetryInterval
	dispatchSaturationRetryInterval = time.Millisecond
	t.Cleanup(func() { dispatchSaturationRetryInterval = origInterval })

	const workspaceUUID = "ws-gc-recycled"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	var notifiedErr error
	m.SetNotifyFunc(func(_, _ string, err error) { notifiedErr = err })

	attempts := 0
	m.SetPromptFunc(func(_ context.Context, workspace, _, _ string) error {
		attempts++
		if attempts == 1 {
			return fmt.Errorf("MCP-init gated: %w", acperrors.ErrProcessSaturated)
		}
		return fmt.Errorf("no shared process for workspace %s", workspace)
	})

	m.dispatchWithRetry(workspaceUUID, "close-memory-batch", "durable prompt",
		time.Second, "dispatch skipped", "dispatch failed")

	if attempts != 2 {
		t.Fatalf("dispatch attempts = %d, want 2 (saturation followed by GC recycle)", attempts)
	}
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("load pending-dispatch spool: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("pending-dispatch entries = %d, want 1; no-shared-process discarded the batch", len(entries))
	}
	if entries[0].Prompt != "durable prompt" {
		t.Fatalf("persisted prompt = %q, want durable prompt", entries[0].Prompt)
	}
	if notifiedErr == nil || !strings.Contains(notifiedErr.Error(), "delivery deferred") {
		t.Fatalf("notification error = %v, want explicit deferred-delivery status", notifiedErr)
	}

	deliveries := 0
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		deliveries++
		return nil
	})
	m.FlushPendingDispatches(context.Background(), workspaceUUID)
	if deliveries != 1 {
		t.Fatalf("healthy-process replay deliveries = %d, want 1", deliveries)
	}
	entries, err = store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("load pending-dispatch spool after replay: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending-dispatch entries after healthy replay = %d, want 0", len(entries))
	}
}

// TestDispatchWithRetry_ThreeCloseBatchesSurviveHealthRecycle reproduces the
// three deleted-child batches observed in mitto-a1ca and verifies none are lost.
func TestDispatchWithRetry_ThreeCloseBatchesSurviveHealthRecycle(t *testing.T) {
	origInterval := dispatchSaturationRetryInterval
	dispatchSaturationRetryInterval = time.Millisecond
	t.Cleanup(func() { dispatchSaturationRetryInterval = origInterval })

	const workspaceUUID = "ws-three-close-batches"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)

	attempts := make(map[string]int)
	m.SetPromptFunc(func(_ context.Context, workspace, name, _ string) error {
		attempts[name]++
		if attempts[name] == 1 {
			return fmt.Errorf("MCP-init gated: %w", acperrors.ErrProcessSaturated)
		}
		return fmt.Errorf("no shared process for workspace %s", workspace)
	})

	names := []string{"close-child-1", "close-child-2", "close-child-3"}
	for _, name := range names {
		m.dispatchWithRetry(workspaceUUID, name, "durable "+name,
			time.Second, "dispatch skipped", "dispatch failed")
	}

	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("load pending-dispatch spool: %v", err)
	}
	if len(entries) != len(names) {
		t.Fatalf("pending-dispatch entries = %d, want %d", len(entries), len(names))
	}

	deliveries := 0
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		deliveries++
		return nil
	})
	m.FlushPendingDispatches(context.Background(), workspaceUUID)
	if deliveries != len(names) {
		t.Fatalf("healthy-process replay deliveries = %d, want %d", deliveries, len(names))
	}
	entries, err = store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("load pending-dispatch spool after replay: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending-dispatch entries after healthy replay = %d, want 0", len(entries))
	}
}
