package web

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/processors"
)

// These tests pin the mitto-7ds wiring in startPendingDispatchSweep: the
// present-but-idle warm-up logic itself is covered exhaustively at the
// processors.SweepPendingDispatchDir level (see
// internal/processors/pending_dispatch_sweep_test.go); here we only need to
// prove the server-layer plumbing — the ensureDispatchable closure built
// from ensureWorkspaceProcess, the one-shot startup sweep timing, and clean
// shutdown — is wired correctly end-to-end through the real function.

// seedFreshSpoolEntry writes one non-expired pending-dispatch batch for
// workspaceUUID directly to spoolDir, mirroring the fixtures used by
// internal/processors/pending_dispatch_sweep_test.go.
func seedFreshSpoolEntry(t *testing.T, spoolDir, workspaceUUID string) {
	t.Helper()
	store := &processors.FilePendingDispatchStore{BaseDir: spoolDir}
	entry := processors.PendingDispatchEntry{
		WorkspaceUUID: workspaceUUID,
		Name:          "extract-memories-on-close",
		Prompt:        "persist memories",
		SavedAt:       time.Now(),
		Attempts:      1,
	}
	if err := store.Replace(workspaceUUID, []processors.PendingDispatchEntry{entry}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
}

// TestStartPendingDispatchSweep_StartupSweep_WarmsPresentIdleWorkspace pins
// mitto-7ds: the one-shot startup sweep must actually invoke
// ensureWorkspaceProcess for a registered (workspaceExists true) workspace
// with a fresh spool entry, without waiting for the 30-minute periodic
// ticker. pendingDispatchStartupSweepDelay is shrunk so the test doesn't
// sleep the real 10s production delay.
func TestStartPendingDispatchSweep_StartupSweep_WarmsPresentIdleWorkspace(t *testing.T) {
	orig := pendingDispatchStartupSweepDelay
	pendingDispatchStartupSweepDelay = 20 * time.Millisecond
	defer func() { pendingDispatchStartupSweepDelay = orig }()

	spoolDir := t.TempDir()
	const wsUUID = "ws-server-wiring-warm-ok"
	seedFreshSpoolEntry(t, spoolDir, wsUUID)

	workspaceExists := func(workspaceUUID string) bool { return workspaceUUID == wsUUID }

	calledCh := make(chan string, 1)
	ensureWorkspaceProcess := func(workspaceUUID string) error {
		select {
		case calledCh <- workspaceUUID:
		default:
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stop := startPendingDispatchSweep(ctx, nil, nil, spoolDir, nil, workspaceExists, ensureWorkspaceProcess)
	defer stop()

	select {
	case got := <-calledCh:
		if got != wsUUID {
			t.Fatalf("ensureWorkspaceProcess called with %q, want %q", got, wsUUID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ensureWorkspaceProcess was not invoked by the one-shot startup sweep within 2s (mitto-7ds wiring)")
	}
}

// TestStartPendingDispatchSweep_StartupSweep_SkipsOrphanedWorkspace pins the
// complementary wiring guarantee: an orphaned workspace (workspaceExists
// false) must never be offered a warm-up, even though it has a fresh spool
// entry — a regression guard against ever swapping the positional
// workspaceExists/ensureDispatchable wiring at the call site.
func TestStartPendingDispatchSweep_StartupSweep_SkipsOrphanedWorkspace(t *testing.T) {
	orig := pendingDispatchStartupSweepDelay
	pendingDispatchStartupSweepDelay = 20 * time.Millisecond
	defer func() { pendingDispatchStartupSweepDelay = orig }()

	spoolDir := t.TempDir()
	const wsUUID = "ws-server-wiring-orphaned"
	seedFreshSpoolEntry(t, spoolDir, wsUUID)

	workspaceExists := func(string) bool { return false } // orphaned: not in registry

	var mu sync.Mutex
	ensureCalls := 0
	ensureWorkspaceProcess := func(string) error {
		mu.Lock()
		ensureCalls++
		mu.Unlock()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stop := startPendingDispatchSweep(ctx, nil, nil, spoolDir, nil, workspaceExists, ensureWorkspaceProcess)
	// Give the shortened startup sweep time to run before asserting.
	time.Sleep(200 * time.Millisecond)
	stop()

	mu.Lock()
	defer mu.Unlock()
	if ensureCalls != 0 {
		t.Fatalf("ensureWorkspaceProcess calls = %d, want 0 (an orphaned workspace must never be warmed)", ensureCalls)
	}
}

// TestStartPendingDispatchSweep_StopCancelsPromptly proves stop() cancels
// the sweep goroutine immediately via context cancellation instead of
// blocking until pendingDispatchStartupSweepDelay elapses — required for a
// clean, bounded Shutdown(). Uses the real (unshrunk) production delay to
// prove cancellation short-circuits it.
func TestStartPendingDispatchSweep_StopCancelsPromptly(t *testing.T) {
	spoolDir := t.TempDir()
	workspaceExists := func(string) bool { return false }
	ensureWorkspaceProcess := func(string) error { return nil }

	stop := startPendingDispatchSweep(context.Background(), nil, nil, spoolDir, nil, workspaceExists, ensureWorkspaceProcess)

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() did not return within 2s; it should cancel immediately instead of waiting out pendingDispatchStartupSweepDelay")
	}
}
