package web

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/auxiliary"
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
	stop := startPendingDispatchSweep(ctx, nil, nil, spoolDir, nil, workspaceExists, ensureWorkspaceProcess, nil, nil)
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
	stop := startPendingDispatchSweep(ctx, nil, nil, spoolDir, nil, workspaceExists, ensureWorkspaceProcess, nil, nil)
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

	stop := startPendingDispatchSweep(context.Background(), nil, nil, spoolDir, nil, workspaceExists, ensureWorkspaceProcess, nil, nil)

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

type blockingSweepProvider struct {
	entered chan struct{}
	once    sync.Once
}

func (p *blockingSweepProvider) PromptAuxiliary(ctx context.Context, _, _, _ string) (string, error) {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return "", ctx.Err()
}

func (*blockingSweepProvider) PromptAuxiliaryAsync(context.Context, string, string, string) error {
	return nil
}

func (*blockingSweepProvider) CloseWorkspaceAuxiliary(string) error { return nil }

func (*blockingSweepProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

// TestStartPendingDispatchSweep_StopCancelsActiveFlush pins mitto-ctg: an
// already-running startup sweep must receive shutdown cancellation through
// FlushPendingDispatches and its active auxiliary prompt. Otherwise stop waits
// for the processor's multi-minute timeout and Server.Shutdown never returns.
func TestStartPendingDispatchSweep_StopCancelsActiveFlush(t *testing.T) {
	orig := pendingDispatchStartupSweepDelay
	pendingDispatchStartupSweepDelay = 20 * time.Millisecond
	defer func() { pendingDispatchStartupSweepDelay = orig }()

	spoolDir := t.TempDir()
	const wsUUID = "ws-server-active-flush"
	seedFreshSpoolEntry(t, spoolDir, wsUUID)

	provider := &blockingSweepProvider{entered: make(chan struct{})}
	auxMgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)
	stop := startPendingDispatchSweep(
		context.Background(), nil, auxMgr, spoolDir, nil,
		func(workspaceUUID string) bool { return workspaceUUID == wsUUID },
		func(string) error { return nil },
		nil, nil,
	)

	select {
	case <-provider.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("startup sweep did not enter the blocking auxiliary prompt")
	}

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() did not cancel the active pending-dispatch flush")
	}

	entries, err := (&processors.FilePendingDispatchStore{BaseDir: spoolDir}).Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Attempts != 1 || entries[0].LastError != "" {
		t.Fatalf("entry after cancellation = %+v, want one unchanged entry", entries)
	}
}

type authFailureSweepProvider struct {
	err error
}

func (p *authFailureSweepProvider) PromptAuxiliary(context.Context, string, string, string) (string, error) {
	return "", p.err
}

func (*authFailureSweepProvider) PromptAuxiliaryAsync(context.Context, string, string, string) error {
	return nil
}

func (*authFailureSweepProvider) CloseWorkspaceAuxiliary(string) error { return nil }

func (*authFailureSweepProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

type completionSweepProvider struct{}

func (*completionSweepProvider) PromptAuxiliary(_ context.Context, _, _, prompt string) (string, error) {
	const dispatchIDMarker = `The dispatch ID is "`
	markerStart := strings.Index(prompt, dispatchIDMarker)
	if markerStart < 0 {
		return "", fmt.Errorf("tracked prompt missing dispatch ID")
	}
	dispatchIDStart := markerStart + len(dispatchIDMarker)
	dispatchIDEnd := strings.IndexByte(prompt[dispatchIDStart:], '"')
	if dispatchIDEnd < 0 {
		return "", fmt.Errorf("tracked prompt has unterminated dispatch ID")
	}
	dispatchID := prompt[dispatchIDStart : dispatchIDStart+dispatchIDEnd]
	return fmt.Sprintf(`MITTO_PROCESSOR_COMPLETION {"dispatch_id":%q,"save_count":1}`, dispatchID), nil
}

func (*completionSweepProvider) PromptAuxiliaryAsync(context.Context, string, string, string) error {
	return nil
}

func (*completionSweepProvider) CloseWorkspaceAuxiliary(string) error { return nil }

func (*completionSweepProvider) SendAuxiliaryInstructions(context.Context, string, string, string) error {
	return nil
}

func TestStartPendingDispatchSweep_AuthFailureInvokesNotifyCallback(t *testing.T) {
	orig := pendingDispatchStartupSweepDelay
	pendingDispatchStartupSweepDelay = 20 * time.Millisecond
	defer func() { pendingDispatchStartupSweepDelay = orig }()

	spoolDir := t.TempDir()
	wsUUID := fmt.Sprintf("ws-server-auth-failure-%d", time.Now().UnixNano())
	seedFreshSpoolEntry(t, spoolDir, wsUUID)

	provider := &authFailureSweepProvider{err: errors.New(`authentication required: {"error":"expired-token-secret"}`)}
	auxMgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)
	warmCh := make(chan struct{}, 2)
	ensureWorkspaceProcess := func(string) error {
		warmCh <- struct{}{}
		return nil
	}
	notifyCh := make(chan struct {
		workspaceUUID string
		name          string
		err           error
	}, 1)
	stopFirst := startPendingDispatchSweep(
		context.Background(), nil, auxMgr, spoolDir, nil,
		func(workspaceUUID string) bool { return workspaceUUID == wsUUID },
		ensureWorkspaceProcess,
		func(workspaceUUID, name string, lastErr error) {
			notifyCh <- struct {
				workspaceUUID string
				name          string
				err           error
			}{workspaceUUID: workspaceUUID, name: name, err: lastErr}
		},
		nil,
	)
	defer stopFirst()

	select {
	case got := <-notifyCh:
		if got.workspaceUUID != wsUUID || got.name != "extract-memories-on-close" {
			t.Fatalf("notify callback args = (%q, %q), want (%q, %q)", got.workspaceUUID, got.name, wsUUID, "extract-memories-on-close")
		}
		if got.err == nil || !strings.Contains(got.err.Error(), "authentication required") {
			t.Fatalf("notify callback error = %v, want authentication error", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup sweep did not invoke NotifyFunc for an authentication failure")
	}
	select {
	case <-warmCh:
	case <-time.After(2 * time.Second):
		t.Fatal("startup sweep did not warm the workspace before flushing its spool")
	}
	stopFirst()

	// A separate sweep manager sees the same workspace spool while the auth
	// cooldown is active. It should still discover/warm the workspace, but must
	// not send another re-authentication notice.
	stopSecond := startPendingDispatchSweep(
		context.Background(), nil, auxMgr, spoolDir, nil,
		func(workspaceUUID string) bool { return workspaceUUID == wsUUID },
		ensureWorkspaceProcess,
		func(workspaceUUID, name string, lastErr error) {
			notifyCh <- struct {
				workspaceUUID string
				name          string
				err           error
			}{workspaceUUID: workspaceUUID, name: name, err: lastErr}
		},
		nil,
	)
	defer stopSecond()
	select {
	case <-warmCh:
	case <-time.After(2 * time.Second):
		t.Fatal("second startup sweep did not run during the active auth cooldown")
	}
	time.Sleep(100 * time.Millisecond)
	stopSecond()
	select {
	case duplicate := <-notifyCh:
		t.Fatalf("second sweep emitted duplicate auth notice: %+v", duplicate)
	default:
	}
}

func TestStartPendingDispatchSweep_LateDeliveryInvokesCallback(t *testing.T) {
	orig := pendingDispatchStartupSweepDelay
	pendingDispatchStartupSweepDelay = 20 * time.Millisecond
	defer func() { pendingDispatchStartupSweepDelay = orig }()

	spoolDir := t.TempDir()
	const wsUUID = "ws-server-late-delivery"
	seedFreshSpoolEntry(t, spoolDir, wsUUID)

	provider := &completionSweepProvider{}
	auxMgr := auxiliary.NewWorkspaceAuxiliaryManager(provider, nil)
	type delivery struct {
		workspaceUUID string
		names         []string
	}
	deliveryCh := make(chan delivery, 1)
	stop := startPendingDispatchSweep(
		context.Background(), nil, auxMgr, spoolDir, nil,
		func(workspaceUUID string) bool { return workspaceUUID == wsUUID },
		func(string) error { return nil },
		nil,
		func(workspaceUUID string, names []string) {
			deliveryCh <- delivery{workspaceUUID: workspaceUUID, names: names}
		},
	)
	defer stop()

	select {
	case got := <-deliveryCh:
		if got.workspaceUUID != wsUUID {
			t.Fatalf("late-delivery workspace = %q, want %q", got.workspaceUUID, wsUUID)
		}
		if len(got.names) != 1 || got.names[0] != "extract-memories-on-close" {
			t.Fatalf("late-delivery names = %v, want [extract-memories-on-close]", got.names)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup sweep did not invoke LateDeliveryFunc after successful delivery")
	}
}
