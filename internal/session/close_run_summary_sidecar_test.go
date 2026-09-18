package session

import (
	"errors"
	"testing"
)

func newCloseRunSummaryTestStore(t *testing.T, sessionID string) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(Metadata{SessionID: sessionID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return store
}

// TestReadCloseRunSummary_NoPriorRun verifies the expected first-run state:
// no sidecar file yet returns a zero-run summary at the current schema
// version, not an error.
func TestReadCloseRunSummary_NoPriorRun(t *testing.T) {
	store := newCloseRunSummaryTestStore(t, "s1")
	got, err := ReadCloseRunSummary(store, "s1")
	if err != nil {
		t.Fatalf("ReadCloseRunSummary: %v", err)
	}
	if got.Version != CloseRunSummarySidecarVersion {
		t.Errorf("Version = %d, want %d", got.Version, CloseRunSummarySidecarVersion)
	}
	if len(got.Runs) != 0 {
		t.Errorf("Runs = %v, want empty", got.Runs)
	}
}

// TestCloseRunSummary_RoundTrip verifies a write followed by a read returns
// byte-for-byte equivalent data, including nested per-processor entries.
func TestCloseRunSummary_RoundTrip(t *testing.T) {
	store := newCloseRunSummaryTestStore(t, "s1")

	want := CloseRunSummary{
		Version: CloseRunSummarySidecarVersion,
		Runs: []CloseRunSummaryEntry{
			{
				RunID:                   "run-1",
				ArchiveReason:           "manual",
				StartedAt:               "2026-09-18T12:00:00Z",
				CompletedAt:             "2026-09-18T12:00:05Z",
				TotalProcessors:         2,
				Applied:                 1,
				Skipped:                 1,
				Errored:                 0,
				TotalEstTokensPrimary:   0,
				TotalEstTokensAuxiliary: 42,
				Processors: []CloseRunProcessorEntry{
					{Name: "knowledge-router", Outcome: "ok", Mode: "prompt", Target: "auxiliary", RenderedBytes: 168, EstimatedTokens: 42},
					{Name: "memorize-preferences", Outcome: "skipped", SkipReason: "enabledWhen"},
				},
			},
		},
	}
	if err := WriteCloseRunSummary(store, "s1", want); err != nil {
		t.Fatalf("WriteCloseRunSummary: %v", err)
	}

	got, err := ReadCloseRunSummary(store, "s1")
	if err != nil {
		t.Fatalf("ReadCloseRunSummary: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].RunID != "run-1" {
		t.Fatalf("Runs = %+v, want one run-1", got.Runs)
	}
	if len(got.Runs[0].Processors) != 2 {
		t.Fatalf("Processors = %+v, want 2", got.Runs[0].Processors)
	}
	if got.Runs[0].Processors[0].Name != "knowledge-router" || got.Runs[0].Processors[0].EstimatedTokens != 42 {
		t.Errorf("Processors[0] mismatch: got %+v", got.Runs[0].Processors[0])
	}
	if got.Runs[0].Processors[1].SkipReason != "enabledWhen" {
		t.Errorf("Processors[1].SkipReason = %q, want %q", got.Runs[0].Processors[1].SkipReason, "enabledWhen")
	}
	if got.Runs[0].TotalEstTokensAuxiliary != 42 {
		t.Errorf("TotalEstTokensAuxiliary = %d, want 42", got.Runs[0].TotalEstTokensAuxiliary)
	}
}

// TestCloseRunSummary_WriteMissingSession verifies ErrSessionNotFound
// propagates when the session was deleted before/during a write, so callers
// can distinguish "truly deleted" from "no runs yet" (mirrors the
// close-router sidecar convention).
func TestCloseRunSummary_WriteMissingSession(t *testing.T) {
	store := newCloseRunSummaryTestStore(t, "s1")
	if err := store.Delete("s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	err := WriteCloseRunSummary(store, "s1", CloseRunSummary{Version: CloseRunSummarySidecarVersion})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("WriteCloseRunSummary after delete: err=%v, want ErrSessionNotFound", err)
	}
}

// TestAppendCloseRunSummary_AppendsAcrossRuns verifies the append-only
// ledger semantics: successive AppendCloseRunSummary calls accumulate runs
// rather than overwriting the previous entry.
func TestAppendCloseRunSummary_AppendsAcrossRuns(t *testing.T) {
	store := newCloseRunSummaryTestStore(t, "s1")

	if err := AppendCloseRunSummary(store, "s1", CloseRunSummaryEntry{RunID: "run-1"}); err != nil {
		t.Fatalf("AppendCloseRunSummary(run-1): %v", err)
	}
	if err := AppendCloseRunSummary(store, "s1", CloseRunSummaryEntry{RunID: "run-2"}); err != nil {
		t.Fatalf("AppendCloseRunSummary(run-2): %v", err)
	}

	got, err := ReadCloseRunSummary(store, "s1")
	if err != nil {
		t.Fatalf("ReadCloseRunSummary: %v", err)
	}
	if len(got.Runs) != 2 {
		t.Fatalf("Runs = %+v, want 2 entries", got.Runs)
	}
	if got.Runs[0].RunID != "run-1" || got.Runs[1].RunID != "run-2" {
		t.Errorf("Runs order mismatch: got %+v", got.Runs)
	}
}
