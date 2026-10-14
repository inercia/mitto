package processors

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const authDispatchTestError = `auxiliary prompt failed: {"code":-32000,"message":"Authentication required: provider authentication required","data":{}}`

func TestIsAuthDispatchErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "JSON-RPC message", err: errors.New(authDispatchTestError), want: true},
		{name: "plain message", err: errors.New("Authentication required"), want: true},
		{name: "wrapped", err: fmt.Errorf("auxiliary prompt failed: %w", errors.New("Authentication required")), want: true},
		{name: "nil", err: nil, want: false},
		{name: "unrelated", err: errors.New("context deadline exceeded"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAuthDispatchErr(tt.err); got != tt.want {
				t.Fatalf("isAuthDispatchErr(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
	if isTransientAuxUnavailableDispatchErr(errors.New(authDispatchTestError)) {
		t.Fatal("authentication error was classified as transient auxiliary-unavailable")
	}
}

func TestRunDispatchRetryLoop_AuthErrorStopsWithoutBackoff(t *testing.T) {
	const workspaceUUID = "ws-auth-retry-loop"
	clearAuthDispatchBlock(workspaceUUID)
	t.Cleanup(func() { clearAuthDispatchBlock(workspaceUUID) })

	originalDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Minute
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = originalDelay })

	var calls int
	m := NewManager("", nil)
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		calls++
		return errors.New(authDispatchTestError)
	})

	_, attempts, waited, err := m.runDispatchRetryLoopTracked(
		context.Background(), workspaceUUID, "identify-user-data", "dispatch-auth-loop", "prompt", time.Second, "skip", &dispatchRetryLogState{})
	if !isAuthDispatchErr(err) {
		t.Fatalf("runDispatchRetryLoopTracked() error = %v, want auth error", err)
	}
	if calls != 1 || attempts != 1 {
		t.Fatalf("prompt calls/attempts = %d/%d, want 1/1", calls, attempts)
	}
	if waited >= dispatchPromptRetryBaseDelay {
		t.Fatalf("waited %s, want immediate return before retry backoff %s", waited, dispatchPromptRetryBaseDelay)
	}
}

func TestDispatchWithRetry_AuthErrorSpoolsOnceWithoutDeadLetter(t *testing.T) {
	const workspaceUUID = "ws-auth-initial-dispatch"
	clearAuthDispatchBlock(workspaceUUID)
	t.Cleanup(func() { clearAuthDispatchBlock(workspaceUUID) })

	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	var calls int
	var notifyCalls int
	m.SetNotifyFunc(func(gotWorkspaceUUID, name string, lastErr error) {
		notifyCalls++
		if gotWorkspaceUUID != workspaceUUID || name != "identify-user-data" || !isAuthDispatchErr(lastErr) {
			t.Errorf("notify callback = (%q, %q, %v), want auth failure for identify-user-data in %q", gotWorkspaceUUID, name, lastErr, workspaceUUID)
		}
	})
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		calls++
		return PromptCompletion{}, errors.New(authDispatchTestError)
	})

	m.dispatchWithRetry(workspaceUUID, "identify-user-data", "prompt", time.Second, "skip", "failed", false)

	if calls != 1 {
		t.Fatalf("promptCompletionFn calls = %d, want 1", calls)
	}
	if notifyCalls != 1 {
		t.Fatalf("notifyFunc calls = %d, want 1", notifyCalls)
	}
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("pending entries = %d, want 1", len(entries))
	}
	if entries[0].Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", entries[0].Attempts)
	}
	if !strings.Contains(entries[0].LastError, "Authentication required") {
		t.Fatalf("LastError = %q, want authentication error", entries[0].LastError)
	}
	replayed, err := store.ReplayDeadLetters(workspaceUUID, 0)
	if err != nil {
		t.Fatalf("ReplayDeadLetters() error = %v", err)
	}
	if len(replayed) != 0 {
		t.Fatalf("dead-letter replay entries = %d, want none", len(replayed))
	}
}

func TestFlushPendingDispatches_AuthErrorRequeuesWorkspaceBatchUnchanged(t *testing.T) {
	const workspaceUUID = "ws-auth-flush"
	clearAuthDispatchBlock(workspaceUUID)
	t.Cleanup(func() { clearAuthDispatchBlock(workspaceUUID) })

	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	for _, entry := range []PendingDispatchEntry{
		{WorkspaceUUID: workspaceUUID, Name: "first", Prompt: "first prompt", TimeoutSeconds: 1, SavedAt: time.Now(), Attempts: 2},
		{WorkspaceUUID: workspaceUUID, Name: "second", Prompt: "second prompt", TimeoutSeconds: 1, SavedAt: time.Now(), Attempts: 1, LastError: "previous error"},
	} {
		if _, err := store.Append(entry); err != nil {
			t.Fatalf("Append(%q) error = %v", entry.Name, err)
		}
	}

	var calls int
	var notified bool
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	m.SetNotifyFunc(func(gotWorkspaceUUID, name string, lastErr error) {
		if gotWorkspaceUUID != workspaceUUID || name != "first" || !isAuthDispatchErr(lastErr) {
			t.Errorf("notify callback = (%q, %q, %v), want auth failure for first entry in %q", gotWorkspaceUUID, name, lastErr, workspaceUUID)
		}
		notified = true
	})
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		calls++
		return errors.New(authDispatchTestError)
	})
	m.FlushPendingDispatches(context.Background(), workspaceUUID)

	if calls != 1 {
		t.Fatalf("promptFunc calls = %d, want 1; later workspace entries must not be attempted", calls)
	}
	if !notified {
		t.Fatal("auth failure during pending flush did not invoke the notify callback")
	}
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("pending entries = %d, want both entries retained", len(entries))
	}
	if entries[0].Name != "first" || entries[0].Attempts != 2 || !strings.Contains(entries[0].LastError, "Authentication required") {
		t.Fatalf("first requeued entry = %+v, want attempts unchanged at 2 and auth LastError", entries[0])
	}
	if entries[1].Name != "second" || entries[1].Attempts != 1 || entries[1].LastError != "previous error" {
		t.Fatalf("unattempted entry changed: %+v", entries[1])
	}
	replayed, err := store.ReplayDeadLetters(workspaceUUID, 0)
	if err != nil {
		t.Fatalf("ReplayDeadLetters() error = %v", err)
	}
	if len(replayed) != 0 {
		t.Fatalf("dead-letter replay entries = %d, want none", len(replayed))
	}
}

func TestDispatchWithRetry_AuthCooldownSuppressesDispatchAndFlushUntilSuccess(t *testing.T) {
	const workspaceUUID = "ws-auth-cooldown"
	clearAuthDispatchBlock(workspaceUUID)
	t.Cleanup(func() { clearAuthDispatchBlock(workspaceUUID) })

	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	var calls int
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		calls++
		return PromptCompletion{}, errors.New(authDispatchTestError)
	})

	m.dispatchWithRetry(workspaceUUID, "first", "prompt", time.Second, "skip", "failed", false)
	if calls != 1 {
		t.Fatalf("initial prompt calls = %d, want 1", calls)
	}
	if !ClaimAuthDispatchNotice(workspaceUUID) {
		t.Fatal("first auth notice claim was suppressed")
	}
	if ClaimAuthDispatchNotice(workspaceUUID) {
		t.Fatal("second auth notice claim during cooldown was not deduplicated")
	}
	// A repeated failure can extend the cooldown but must preserve its notice bit.
	blockAuthDispatch(workspaceUUID, errors.New(authDispatchTestError))
	if ClaimAuthDispatchNotice(workspaceUUID) {
		t.Fatal("repeated auth failure reset the notice bit during the active cooldown")
	}
	m.dispatchWithRetry(workspaceUUID, "second", "prompt", time.Second, "skip", "failed", false)
	m.FlushPendingDispatches(context.Background(), workspaceUUID)
	if calls != 1 {
		t.Fatalf("prompt calls during auth cooldown = %d, want still 1", calls)
	}

	// A successful dispatch clears the workspace cooldown so pending work can
	// be attempted again after the user re-authenticates.
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		calls++
		return PromptCompletion{}, nil
	})
	_, _, _, err := m.runDispatchRetryLoopTracked(
		context.Background(), workspaceUUID, "recovered", "dispatch-recovered", "prompt", time.Second, "skip", &dispatchRetryLogState{})
	if err != nil {
		t.Fatalf("successful retry loop error = %v", err)
	}
	if _, blocked := authDispatchBlocked(workspaceUUID); blocked {
		t.Fatal("successful dispatch did not clear workspace auth cooldown")
	}
	// The next auth failure after recovery starts a fresh cooldown and can notify.
	blockAuthDispatch(workspaceUUID, errors.New(authDispatchTestError))
	if !ClaimAuthDispatchNotice(workspaceUUID) {
		t.Fatal("successful recovery did not reset the workspace auth notice")
	}
}

func TestDispatchWithRetry_NonAuthErrorsKeepThreeAttempts(t *testing.T) {
	const workspaceUUID = "ws-auth-regression"
	clearAuthDispatchBlock(workspaceUUID)
	t.Cleanup(func() { clearAuthDispatchBlock(workspaceUUID) })

	originalDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = 0
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = originalDelay })

	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	var calls int
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		calls++
		return errors.New("ordinary transient failure")
	})
	m.dispatchWithRetry(workspaceUUID, "ordinary", "prompt", time.Second, "skip", "failed", false)
	if calls != dispatchPromptMaxRetries+1 {
		t.Fatalf("ordinary transient prompt calls = %d, want %d", calls, dispatchPromptMaxRetries+1)
	}
}
