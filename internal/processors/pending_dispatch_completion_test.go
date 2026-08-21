package processors

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestFilePendingDispatchStore_ClaimSurvivesRestartUntilAcknowledged reproduces
// mitto-930n: claiming a batch must not erase the only durable copy before the
// auxiliary turn reaches a terminal, acknowledged outcome.
func TestFilePendingDispatchStore_ClaimSurvivesRestartUntilAcknowledged(t *testing.T) {
	const (
		workspaceUUID = "ws-claim-restart"
		dispatchID    = "dispatch-awaiting-ack"
	)
	spoolDir := t.TempDir()
	store := &FilePendingDispatchStore{BaseDir: spoolDir}
	if _, err := store.Append(PendingDispatchEntry{
		ID:            dispatchID,
		WorkspaceUUID: workspaceUUID,
		Name:          "extract-memories-on-close",
		Prompt:        "persist memories",
		SavedAt:       time.Now(),
		Attempts:      1,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	claim, err := store.Claim(workspaceUUID)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(claim.Entries) != 1 || claim.Entries[0].ID != dispatchID {
		t.Fatalf("Claim() entries = %#v, want dispatch %q", claim.Entries, dispatchID)
	}

	// Simulate a process crash after Claim but before auxiliary completion and
	// acknowledgement by constructing a fresh store without calling Requeue.
	restarted := &FilePendingDispatchStore{BaseDir: spoolDir}
	recovered, err := restarted.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() after restart error = %v", err)
	}
	if len(recovered) != 1 || recovered[0].ID != dispatchID {
		t.Fatalf("Load() after restart = %#v, want unacknowledged dispatch %q recoverable", recovered, dispatchID)
	}
	if err := restarted.Acknowledge(workspaceUUID, []string{dispatchID}); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
	acknowledged, err := restarted.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() after acknowledge error = %v", err)
	}
	if len(acknowledged) != 0 {
		t.Fatalf("Load() after acknowledge = %#v, want empty spool", acknowledged)
	}
}

func TestDispatchWithRetry_TrackedCompletionPersistsBeforeExecutionAndAcknowledgesAfterward(t *testing.T) {
	const workspaceUUID = "ws-tracked-completion"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)

	m.SetPromptCompletionFunc(func(_ context.Context, workspace, name, dispatchID, _ string) (PromptCompletion, error) {
		entries, err := store.Load(workspace)
		if err != nil {
			t.Fatalf("Load() during execution error = %v", err)
		}
		if len(entries) != 1 || entries[0].ID != dispatchID || entries[0].ClaimedBy == "" {
			t.Fatalf("durable entry during execution = %#v, want claimed dispatch %q", entries, dispatchID)
		}
		if name != "extract-memories-on-close" {
			t.Fatalf("processor name = %q", name)
		}
		return PromptCompletion{SaveCount: 2, SaveCountKnown: true}, nil
	})

	m.dispatchWithRetry(workspaceUUID, "extract-memories-on-close", "persist memories", time.Second, "skip", "fail")
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() after completion error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Load() after terminal success = %#v, want acknowledged empty spool", entries)
	}
}

func TestDispatchWithRetry_TrackedFailureReleasesDurableClaimForRetry(t *testing.T) {
	origMaxRetries := dispatchPromptMaxRetries
	dispatchPromptMaxRetries = 0
	t.Cleanup(func() { dispatchPromptMaxRetries = origMaxRetries })

	const workspaceUUID = "ws-tracked-failure"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		return PromptCompletion{}, errors.New("auxiliary terminal failure")
	})

	m.dispatchWithRetry(workspaceUUID, "memory", "persist", time.Second, "skip", "fail")
	entries, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() after failure error = %v", err)
	}
	if len(entries) != 1 || entries[0].ClaimedBy != "" || entries[0].LastError == "" {
		t.Fatalf("durable entry after failure = %#v, want one unclaimed retryable entry", entries)
	}
}

func TestFilePendingDispatchStore_AppendClaimedNeverEvictsInFlightEntries(t *testing.T) {
	const workspaceUUID = "ws-claimed-capacity"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	entries := make([]PendingDispatchEntry, pendingDispatchMaxEntries)
	for i := range entries {
		entries[i] = PendingDispatchEntry{
			ID: "active-" + string(rune('a'+i)), WorkspaceUUID: workspaceUUID,
			Name: "active", Prompt: "persist", SavedAt: time.Now(), Attempts: 1,
			ClaimedBy: "current-process", ClaimedAt: time.Now(),
		}
	}
	if err := store.Replace(workspaceUUID, entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	result, err := store.AppendClaimed(PendingDispatchEntry{
		ID: "new-active", WorkspaceUUID: workspaceUUID, Name: "new", Prompt: "persist", SavedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("AppendClaimed() error = %v", err)
	}
	if len(result.Dropped) != 0 {
		t.Fatalf("AppendClaimed() dropped in-flight entries = %#v", result.Dropped)
	}
	got, err := store.Load(workspaceUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != pendingDispatchMaxEntries+1 {
		t.Fatalf("Load() count = %d, want temporary claimed overflow %d", len(got), pendingDispatchMaxEntries+1)
	}
}

func TestFilePendingDispatchStore_ClaimRecoversPreviousProcessOwner(t *testing.T) {
	const workspaceUUID = "ws-recover-owner"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	if err := store.Replace(workspaceUUID, []PendingDispatchEntry{{
		ID: "abandoned", WorkspaceUUID: workspaceUUID, Name: "memory", Prompt: "persist",
		SavedAt: time.Now(), Attempts: 1, ClaimedBy: "previous-process", ClaimedAt: time.Now(),
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	claim, err := store.Claim(workspaceUUID)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(claim.Entries) != 1 || claim.Entries[0].ID != "abandoned" || claim.Entries[0].ClaimedBy == "previous-process" {
		t.Fatalf("Claim() recovered entries = %#v", claim.Entries)
	}
}
