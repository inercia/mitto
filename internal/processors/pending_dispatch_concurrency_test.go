package processors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestFilePendingDispatchStore_ConcurrentAppendAcrossInstances verifies
// mitto-gfr1: independently constructed stores targeting one workspace must
// serialize the whole read-modify-write transaction and retain every entry.
func TestFilePendingDispatchStore_ConcurrentAppendAcrossInstances(t *testing.T) {
	spoolDir := t.TempDir()
	const wsUUID = "ws-concurrent-append"
	stores := []*FilePendingDispatchStore{
		{BaseDir: spoolDir},
		{BaseDir: spoolDir},
	}

	start := make(chan struct{})
	errCh := make(chan error, pendingDispatchMaxEntries)
	var wg sync.WaitGroup
	for i := 0; i < pendingDispatchMaxEntries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := stores[i%len(stores)].Append(PendingDispatchEntry{
				WorkspaceUUID: wsUUID,
				Name:          fmt.Sprintf("batch-%02d", i),
				Prompt:        fmt.Sprintf("prompt-%02d", i),
				SavedAt:       time.Now(),
			})
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("Append() error = %v", err)
	}

	entries, err := stores[0].Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != pendingDispatchMaxEntries {
		t.Fatalf("Load() returned %d entries, want %d", len(entries), pendingDispatchMaxEntries)
	}
	prompts := make(map[string]bool, len(entries))
	IDs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			t.Fatal("persisted entry has empty audit ID")
		}
		if IDs[entry.ID] {
			t.Fatalf("duplicate audit ID %q", entry.ID)
		}
		IDs[entry.ID] = true
		prompts[entry.Prompt] = true
	}
	for i := 0; i < pendingDispatchMaxEntries; i++ {
		if prompt := fmt.Sprintf("prompt-%02d", i); !prompts[prompt] {
			t.Errorf("missing %s after concurrent appends", prompt)
		}
	}
}

// TestFlushPendingDispatches_AppendDuringFlushPreservesNewEntry reproduces
// mitto-gfr1: a second store appending after a flush claims the spool must not
// have its entry overwritten when the flush writes a failed entry back.
func TestFlushPendingDispatches_AppendDuringFlushPreservesNewEntry(t *testing.T) {
	origMaxRetries := dispatchPromptMaxRetries
	dispatchPromptMaxRetries = 0
	t.Cleanup(func() { dispatchPromptMaxRetries = origMaxRetries })

	spoolDir := t.TempDir()
	const wsUUID = "ws-append-during-flush"
	flushStore := &FilePendingDispatchStore{BaseDir: spoolDir}
	appendStore := &FilePendingDispatchStore{BaseDir: spoolDir}
	if _, err := flushStore.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID, Name: "batch-a", Prompt: "prompt-a",
		TimeoutSeconds: 1, SavedAt: time.Now(), Attempts: 1,
	}); err != nil {
		t.Fatalf("Append(batch-a) error = %v", err)
	}

	promptEntered := make(chan struct{})
	releasePrompt := make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(releasePrompt)
		}
	})

	m := NewManager("", nil)
	m.SetPendingDispatchStore(flushStore)
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		close(promptEntered)
		<-releasePrompt
		return fmt.Errorf("forced dispatch failure")
	})

	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		m.FlushPendingDispatches(context.Background(), wsUUID)
	}()

	select {
	case <-promptEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not reach prompt dispatch after claiming the spool")
	}
	if _, err := appendStore.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID, Name: "batch-b", Prompt: "prompt-b",
		TimeoutSeconds: 1, SavedAt: time.Now(), Attempts: 1,
	}); err != nil {
		t.Fatalf("Append(batch-b) error = %v", err)
	}
	close(releasePrompt)
	released = true

	select {
	case <-flushDone:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not finish after prompt release")
	}

	remaining, err := appendStore.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	counts := make(map[string]int, len(remaining))
	IDs := make(map[string]bool, len(remaining))
	for _, entry := range remaining {
		if entry.ID == "" {
			t.Fatal("persisted entry has empty audit ID")
		}
		if IDs[entry.ID] {
			t.Fatalf("duplicate audit ID %q", entry.ID)
		}
		IDs[entry.ID] = true
		counts[entry.Prompt]++
	}
	if counts["prompt-a"] != 1 || counts["prompt-b"] != 1 || len(remaining) != 2 {
		t.Fatalf("remaining prompts = %v, want prompt-a and prompt-b exactly once", counts)
	}
}

// TestFlushPendingDispatches_TerminalLogsIncludeAuditID verifies mitto-gfr1's
// reconciliation key is carried into both delivery and bounded-drop outcomes.
func TestFlushPendingDispatches_TerminalLogsIncludeAuditID(t *testing.T) {
	tests := []struct {
		name       string
		attempts   int
		wantLogMsg string
	}{
		{name: "delivery", attempts: 1, wantLogMsg: "pending-dispatch flush: delivered spooled batch"},
		{name: "max-attempt drop", attempts: pendingDispatchMaxAttempts, wantLogMsg: "pending-dispatch flush: dropping entry after exceeding max attempts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const wsUUID = "ws-audit-log"
			const dispatchID = "audit-id-123"
			store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
			if err := store.Replace(wsUUID, []PendingDispatchEntry{{
				ID: dispatchID, WorkspaceUUID: wsUUID, Name: "batch",
				Prompt: "prompt", SavedAt: time.Now(), Attempts: tt.attempts,
			}}); err != nil {
				t.Fatalf("Replace() error = %v", err)
			}

			handler := &recordingLogHandler{}
			m := NewManager("", slog.New(handler))
			m.SetPendingDispatchStore(store)
			m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })
			m.FlushPendingDispatches(context.Background(), wsUUID)

			for _, record := range handler.snapshot() {
				if record.Message == tt.wantLogMsg {
					if got := record.Attrs["dispatch_id"]; got != dispatchID {
						t.Fatalf("dispatch_id = %v, want %q", got, dispatchID)
					}
					return
				}
			}
			t.Fatalf("missing log message %q", tt.wantLogMsg)
		})
	}
}

// TestFlushPendingDispatches_TransientTimeoutMustNotPermanentlyDropBatch
// reproduces mitto-unc: a close-phase memory/user-data batch that fails ONLY
// with a transient "auxiliary prompt cancelled ... context deadline
// exceeded" error (never ErrProcessBusy, never "no shared process", never
// saturation) is currently treated identically to a genuinely poisoned
// batch. Once Attempts reaches pendingDispatchMaxAttempts,
// FlushPendingDispatches Acknowledges (permanently removes) the entry from
// the spool — even though every failure was transient and the auxiliary
// process might have recovered moments later.
//
// This test asserts the EXPECTED (bug-free) behavior per mitto-unc's
// acceptance criterion #1 — "A memory/user-data close-phase batch is not
// silently discarded solely because the auxiliary process was transiently
// unavailable across 3 attempts" — by requiring the entry to still be
// recoverable from the store after exhausting the attempt ceiling on
// purely-transient errors. Before the mitto-unc fix, FlushPendingDispatches
// unconditionally Acknowledged (dropped) the entry once Attempts reached the
// cap, regardless of whether every failure was transient; it now recognizes
// isTransientAuxUnavailableDispatchErr and resets Attempts to
// pendingDispatchTransientRetryAttempts instead of dropping, so the batch
// keeps being retried (bounded only by pendingDispatchMaxAge) rather than
// being permanently lost.
func TestFlushPendingDispatches_TransientTimeoutMustNotPermanentlyDropBatch(t *testing.T) {
	const wsUUID = "ws-transient-timeout-drop"
	const memoryBatchName = "extract-memories-on-close+claude-update-memory+memorize-preferences+auggie-update-rules+curate-memories-on-close"
	transientErr := errors.New("auxiliary prompt cancelled: {code:-32603, Internal error, data.error: context deadline exceeded}")

	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	if _, err := store.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID, Name: memoryBatchName, Prompt: "memory batch prompt",
		TimeoutSeconds: 1, SavedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	var promptAttempts int
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		promptAttempts++
		return transientErr
	})
	var notifiedErr error
	m.SetNotifyFunc(func(_, _ string, err error) { notifiedErr = err })

	// Drive one flush per Attempts increment, mirroring separate flush
	// opportunities (e.g. successive workspace-becomes-dispatchable events)
	// rather than one flush retrying internally — FlushPendingDispatches
	// itself performs a single dispatch attempt per entry per call (retries
	// within runDispatchRetryLoopTracked are bounded by
	// dispatchPromptMaxRetries and only cover ordinary in-call backoff, not
	// the cross-flush Attempts counter that this bug is about).
	origMaxRetries := dispatchPromptMaxRetries
	dispatchPromptMaxRetries = 0
	t.Cleanup(func() { dispatchPromptMaxRetries = origMaxRetries })

	// The entry starts at Attempts=1 (Append's floor, mirroring the initial
	// dispatchWithRetry failure that first spooled it), so it takes
	// pendingDispatchMaxAttempts-1 subsequent flush cycles to reach today's
	// drop threshold. Run a bounded number of extra cycles beyond that so a
	// fixed implementation (which may keep retrying transient failures
	// indefinitely, e.g. via longer backoff) still gets exercised without
	// looping forever.
	maxCycles := pendingDispatchMaxAttempts + 2
	for i := 0; i < maxCycles; i++ {
		m.FlushPendingDispatches(context.Background(), wsUUID)
	}

	if promptAttempts == 0 {
		t.Fatal("promptFunc was never invoked; test did not exercise the transient-failure path")
	}

	remaining, err := store.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// EXPECTED (post-fix) behavior: a purely-transient failure history must
	// not cause the batch to be permanently discarded from the spool.
	if len(remaining) == 0 {
		t.Fatalf("mitto-unc: transient-timeout batch was permanently dropped from the spool "+
			"after %d dispatch attempts (all transient 'context deadline exceeded' errors) — "+
			"acceptance criterion #1 requires it survive for later retry/dead-letter capture "+
			"instead of being silently Acknowledged away; notifyFunc error was: %v",
			promptAttempts, notifiedErr)
	}
}

// TestFlushPendingDispatches_AgeCapDropsPurelyTransientBatchRegardlessOfClassification
// reproduces mitto-unc's REOPENED (P1) recurrence gap #1: the fix above only
// guards the max-attempts cap-exhaustion sites in FlushPendingDispatches. The
// 24h age-cap enforced independently at Load/Claim time
// (partitionPendingDispatchEntries, keyed purely on SavedAt vs
// pendingDispatchMaxAge) has NO knowledge of isTransientAuxUnavailableDispatchErr
// and silently discards a batch the instant it ages out — even when its
// entire recorded failure history is transient-aux-unavailable. Production
// evidence: dispatch_id 1707fab1 (workspace da4bafec) was permanently lost
// this way after the max-attempts fix (147a4648) was already deployed,
// because the age cap fired first.
//
// This test asserts the EXPECTED (post-recurrence-fix) behavior per
// acceptance criterion #1 — "No close-phase memory batch is discarded while
// its whole failure history is transient, within a reasonable extended
// budget" — by requiring a purely-transient-failure entry to remain
// claimable even once its SavedAt exceeds pendingDispatchMaxAge. Today the
// age-cap drop is unconditional, so this fails.
func TestFlushPendingDispatches_AgeCapDropsPurelyTransientBatchRegardlessOfClassification(t *testing.T) {
	const wsUUID = "ws-agecap-transient-drop"
	const memoryBatchName = "extract-memories-on-close+claude-update-memory+memorize-preferences+auggie-update-rules+curate-memories-on-close"
	const transientErr = "auxiliary prompt cancelled: {code:-32603, Internal error, data.error: context deadline exceeded}"

	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	staleSavedAt := time.Now().Add(-(pendingDispatchMaxAge + time.Hour))
	if _, err := store.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID, Name: memoryBatchName, Prompt: "memory batch prompt",
		TimeoutSeconds: 1, SavedAt: staleSavedAt, Attempts: 1, LastError: transientErr,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	claim, err := store.Claim(wsUUID)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	for _, expired := range claim.Expired {
		if expired.Name == memoryBatchName {
			t.Fatalf("mitto-unc (recurrence, gap #1): a purely-transient-failure memory batch "+
				"(LastError=%q) was silently discarded at the 24h age-cap (SavedAt=%s, "+
				"pendingDispatchMaxAge=%s) with no failure-classification gate — acceptance "+
				"criterion #1 requires it survive (or at minimum a loud WARN/notify) within a "+
				"reasonable extended budget instead of a silent, unconditional drop",
				expired.LastError, staleSavedAt, pendingDispatchMaxAge)
		}
	}
	if len(claim.Entries) != 1 {
		t.Fatalf("claim.Entries = %d entries, want 1 (the transient-failure batch should remain "+
			"claimable, not expired)", len(claim.Entries))
	}
}

// TestFlushPendingDispatches_RequeueMustNotResurrectAlreadyExpiredEntry
// reproduces mitto-unc's REOPENED (P1) recurrence gap #2 (double-drop): an
// overlapping, slower flush pass captures an entry via an EARLIER Claim()
// call — before it ages past pendingDispatchMaxAge — and later requeues its
// in-memory copy unchanged (a failure path such as isNonRetryableDispatchErr
// never refreshes SavedAt). If a SEPARATE, faster flush pass claims the spool
// in between and finds the same entry already past pendingDispatchMaxAge, it
// logs+removes it as expired — but Requeue() blindly merges whatever it is
// given back onto disk with no staleness check, so the slower pass's stale
// in-memory copy resurrects the very entry that was just dropped. The next
// Claim() then finds it expired again and drops it a SECOND time. Production
// evidence: dispatch_ids 28358948 / 7dfa9367 (workspace 736f40f8) were both
// logged "dropping expired entry" at 08:05:38 AND again at 09:00:42, with a
// "requeued unresolved entry" line for the same ids in between.
//
// This test asserts the EXPECTED (post-recurrence-fix) behavior per
// acceptance criterion #2 — "Each expired entry is dropped exactly once" —
// by driving the exact sequence above and requiring the second Claim() to
// report nothing for the already-dropped dispatch_id. Today Requeue()
// performs no staleness check, so this fails.
func TestFlushPendingDispatches_RequeueMustNotResurrectAlreadyExpiredEntry(t *testing.T) {
	const wsUUID = "ws-double-drop-resurrection"
	const memoryBatchName = "extract-memories-on-close+memorize-preferences+auggie-update-rules"

	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	appendResult, err := store.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID, Name: memoryBatchName, Prompt: "memory batch prompt",
		TimeoutSeconds: 1,
		// Fresh enough to have been claimable by an earlier, still-running
		// (slower) flush pass, but already past pendingDispatchMaxAge by the
		// time a separate, later flush pass claims the spool.
		SavedAt:  time.Now().Add(-(pendingDispatchMaxAge + time.Minute)),
		Attempts: 1,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	staleEntry := appendResult.Entry

	// Models the slower/overlapping flush pass: it claimed this exact entry
	// via an earlier Claim() (not replayed here — its in-memory copy is
	// `staleEntry`, still carrying the original stale SavedAt) and is about
	// to requeue it unchanged after a non-retryable dispatch error, a path
	// that never refreshes SavedAt.
	slowFlushInMemoryCopy := staleEntry

	// A separate, faster flush pass claims the spool first and finds the
	// entry already past pendingDispatchMaxAge -> drop #1.
	firstClaim, err := store.Claim(wsUUID)
	if err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	if len(firstClaim.Expired) != 1 || firstClaim.Expired[0].ID != staleEntry.ID {
		t.Fatalf("first Claim() Expired = %+v, want exactly the stale entry (setup invariant broken)",
			firstClaim.Expired)
	}
	if remaining, loadErr := store.Load(wsUUID); loadErr != nil || len(remaining) != 0 {
		t.Fatalf("entry should be gone from disk after the first (expiring) Claim(); remaining=%+v err=%v",
			remaining, loadErr)
	}

	// The slower flush pass now finishes its already-doomed dispatch attempt
	// and requeues its stale in-memory copy, unaware the entry was already
	// dropped by the faster pass above.
	if _, err := store.Requeue(wsUUID, []PendingDispatchEntry{slowFlushInMemoryCopy}); err != nil {
		t.Fatalf("Requeue() error = %v", err)
	}

	secondClaim, err := store.Claim(wsUUID)
	if err != nil {
		t.Fatalf("second Claim() error = %v", err)
	}
	for _, expired := range secondClaim.Expired {
		if expired.ID == staleEntry.ID {
			t.Fatalf("mitto-unc (recurrence, gap #2): dispatch_id %s (%s) was dropped as expired "+
				"TWICE — Requeue() resurrected an already-dropped entry using its stale SavedAt "+
				"after an earlier Claim() had already removed it, so the next Claim() dropped it "+
				"again (acceptance criterion #2: each expired entry must be dropped exactly once)",
				expired.ID, expired.Name)
		}
	}
	if len(secondClaim.Entries) != 0 || len(secondClaim.Expired) != 0 {
		t.Fatalf("second Claim() = %+v, want nothing at all (nothing should have been resurrected)",
			secondClaim)
	}
}
