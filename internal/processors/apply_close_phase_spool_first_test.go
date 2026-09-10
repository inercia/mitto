package processors

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestDispatchWithRetry_DeferrableWhenBusy_PredicateTrue_AppendPath is the
// mitto-z4w acceptance case (a) on the fire-and-forget path (no
// promptCompletionFunc, so the entry is not AppendClaimed before dispatch):
// when the dispatch is close-phase (deferrableWhenBusy=true) and
// shouldShedProactiveAuxFunc reports the shared process would shed a
// proactive auxiliary session, dispatchWithRetry must skip straight to the
// durable spool — zero RPC attempts, an unclaimed spool entry, an INFO (not
// ERROR) log line, and no notifyFunc call.
func TestDispatchWithRetry_DeferrableWhenBusy_PredicateTrue_AppendPath(t *testing.T) {
	const workspaceUUID = "ws-z4w-defer-append"
	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	var promptCalls atomic.Int32
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		promptCalls.Add(1)
		return nil
	})
	var notified atomic.Bool
	m.SetNotifyFunc(func(_, _ string, _ error) { notified.Store(true) })
	m.SetShouldDeferDispatchFunc(func(ws string) bool { return ws == workspaceUUID })

	m.dispatchWithRetry(workspaceUUID, "extract-memories-on-close", "persist memories",
		time.Second, "prompt-mode processor dispatch skipped", "prompt-mode processor dispatch failed", true)

	if got := promptCalls.Load(); got != 0 {
		t.Fatalf("promptFunc call count = %d, want 0 (predicted shed must skip every RPC attempt)", got)
	}
	if notified.Load() {
		t.Fatal("notifyFunc must NOT be called for a planned deferral (expected, not an error)")
	}

	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("pending-dispatch entries = %d, want 1", len(entries))
	}
	if entries[0].ClaimedBy != "" {
		t.Fatalf("deferred entry ClaimedBy = %q, want unclaimed so the sweep/flush can pick it up", entries[0].ClaimedBy)
	}
	if entries[0].Prompt != "persist memories" {
		t.Fatalf("persisted prompt = %q, want %q", entries[0].Prompt, "persist memories")
	}

	var sawInfo, sawError bool
	for _, rec := range handler.snapshot() {
		if rec.Level == slog.LevelError {
			sawError = true
		}
		if rec.Level == slog.LevelInfo && strings.Contains(rec.Message, "deferred to spool") {
			sawInfo = true
		}
	}
	if sawError {
		t.Error("expected no ERROR log record — a planned deferral is not retry exhaustion")
	}
	if !sawInfo {
		t.Error("expected an INFO log record mentioning 'deferred to spool'")
	}
}

// TestDispatchWithRetry_DeferrableWhenBusy_PredicateTrue_ReleasesTrackedClaim
// is acceptance case (a) on the completion-aware path: with a
// promptCompletionFunc configured, the entry is AppendClaimed before the
// predicate check, so a predicted shed must Requeue (release the claim)
// rather than leave it claimed — otherwise the sweep would never recover it.
func TestDispatchWithRetry_DeferrableWhenBusy_PredicateTrue_ReleasesTrackedClaim(t *testing.T) {
	const workspaceUUID = "ws-z4w-defer-tracked"
	m := NewManager("", nil)
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	var completionCalls atomic.Int32
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		completionCalls.Add(1)
		return PromptCompletion{}, nil
	})
	m.SetShouldDeferDispatchFunc(func(string) bool { return true })

	m.dispatchWithRetry(workspaceUUID, "extract-memories-on-close", "persist memories",
		time.Second, "skip", "fail", true)

	if got := completionCalls.Load(); got != 0 {
		t.Fatalf("promptCompletionFunc call count = %d, want 0", got)
	}
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("pending-dispatch entries = %d, want 1", len(entries))
	}
	if entries[0].ClaimedBy != "" {
		t.Fatalf("deferred entry ClaimedBy = %q, want unclaimed (claim released via Requeue)", entries[0].ClaimedBy)
	}
}

// TestDispatchWithRetry_DeferrableWhenBusy_PredicateFalse_UnchangedBehavior is
// acceptance case (b): when the dispatch is close-phase but the predicate
// reports the process is healthy, dispatchWithRetry must proceed through the
// normal dispatch path exactly as before — no deferral, no early return.
func TestDispatchWithRetry_DeferrableWhenBusy_PredicateFalse_UnchangedBehavior(t *testing.T) {
	const workspaceUUID = "ws-z4w-predicate-false"
	m := NewManager("", nil)
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	var promptCalls atomic.Int32
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		promptCalls.Add(1)
		return nil
	})
	m.SetShouldDeferDispatchFunc(func(string) bool { return false })

	m.dispatchWithRetry(workspaceUUID, "extract-memories-on-close", "persist memories",
		time.Second, "skip", "fail", true)

	if got := promptCalls.Load(); got != 1 {
		t.Fatalf("promptFunc call count = %d, want 1 (predicate-false must dispatch normally)", got)
	}
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending-dispatch entries after a successful non-deferred dispatch = %d, want 0", len(entries))
	}
}

// TestDispatchWithRetry_NonCloseDispatch_NeverDefers is acceptance case (c):
// non-close-phase dispatches (Apply/ApplyAfter, deferrableWhenBusy=false)
// must never defer to the spool even if shouldDeferDispatchFunc reports the
// process would shed — that predicate is scoped to close-phase batches only,
// since mid-conversation dispatch semantics must stay exactly as before.
func TestDispatchWithRetry_NonCloseDispatch_NeverDefers(t *testing.T) {
	const workspaceUUID = "ws-z4w-non-close"
	m := NewManager("", nil)
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)

	var promptCalls atomic.Int32
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		promptCalls.Add(1)
		return nil
	})
	// Predicate would defer if consulted — deferrableWhenBusy=false must
	// prevent it from ever being consulted.
	m.SetShouldDeferDispatchFunc(func(string) bool { return true })

	m.dispatchWithRetry(workspaceUUID, "identify-user-data", "prompt",
		time.Second, "skip", "fail", false)

	if got := promptCalls.Load(); got != 1 {
		t.Fatalf("promptFunc call count = %d, want 1 (non-close dispatch must never defer)", got)
	}
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pending-dispatch entries = %d, want 0 (successful non-close dispatch is never spooled)", len(entries))
	}
}
