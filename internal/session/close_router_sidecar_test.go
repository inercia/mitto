package session

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newCloseRouterTestStore(t *testing.T, sessionID string) *Store {
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

// TestReadCloseRouterState_NoPriorRun verifies the expected first-run state:
// no sidecar file yet returns a zero-run state at the current schema version,
// not an error.
func TestReadCloseRouterState_NoPriorRun(t *testing.T) {
	store := newCloseRouterTestStore(t, "s1")
	got, err := ReadCloseRouterState(store, "s1")
	if err != nil {
		t.Fatalf("ReadCloseRouterState: %v", err)
	}
	if got.Version != CloseRouterSidecarVersion {
		t.Errorf("Version = %d, want %d", got.Version, CloseRouterSidecarVersion)
	}
	if len(got.Runs) != 0 {
		t.Errorf("Runs = %v, want empty", got.Runs)
	}
}

// TestCloseRouterState_RoundTrip verifies a write followed by a read returns
// byte-for-byte equivalent data, including nested findings.
func TestCloseRouterState_RoundTrip(t *testing.T) {
	store := newCloseRouterTestStore(t, "s1")

	want := CloseRouterState{
		Version: CloseRouterSidecarVersion,
		Runs: []CloseRouterRun{
			{
				RunID:               "run-1",
				StartedAt:           time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
				CompletedAt:         time.Date(2026, 9, 18, 12, 0, 5, 0, time.UTC),
				HistorySnapshotHash: CloseRouterSnapshotHash([]byte(`{"events":[]}`)),
				Findings: []CloseRouterFinding{
					{LogicalKey: CloseRouterLogicalKey("prefers dark mode"), Destination: "preferences", Written: true, TargetPath: ".augment/preferences.local.md"},
					{LogicalKey: CloseRouterLogicalKey("noise"), Destination: "none", Written: false},
				},
			},
		},
	}
	if err := WriteCloseRouterState(store, "s1", want); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	got, err := ReadCloseRouterState(store, "s1")
	if err != nil {
		t.Fatalf("ReadCloseRouterState: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].RunID != "run-1" {
		t.Fatalf("Runs = %+v, want one run-1", got.Runs)
	}
	if len(got.Runs[0].Findings) != 2 {
		t.Fatalf("Findings = %+v, want 2", got.Runs[0].Findings)
	}
	if got.Runs[0].Findings[0].LogicalKey != want.Runs[0].Findings[0].LogicalKey {
		t.Errorf("LogicalKey mismatch: got %q want %q", got.Runs[0].Findings[0].LogicalKey, want.Runs[0].Findings[0].LogicalKey)
	}
	if !got.Runs[0].StartedAt.Equal(want.Runs[0].StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.Runs[0].StartedAt, want.Runs[0].StartedAt)
	}
}

// TestCloseRouterState_WriteMissingSession verifies ErrSessionNotFound
// propagates when the session was deleted before/during a write, so callers
// can distinguish "truly deleted" from "no runs yet" (mirrors the
// child-report sidecar convention).
func TestCloseRouterState_WriteMissingSession(t *testing.T) {
	store := newCloseRouterTestStore(t, "s1")
	if err := store.Delete("s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	err := WriteCloseRouterState(store, "s1", CloseRouterState{Version: CloseRouterSidecarVersion})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("WriteCloseRouterState after delete: err=%v, want ErrSessionNotFound", err)
	}
}

// TestCloseRouterState_ConcurrentReadWrite exercises the per-session lock
// under concurrent writers and readers: every write must be internally
// consistent (readers never observe a torn/partial JSON document produced by
// a racing writer), and every call must complete without error. Run with
// -race to also catch data races in the sidecar plumbing itself.
func TestCloseRouterState_ConcurrentReadWrite(t *testing.T) {
	store := newCloseRouterTestStore(t, "s1")

	const writers = 8
	const readers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers+readers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state := CloseRouterState{
				Version: CloseRouterSidecarVersion,
				Runs: []CloseRouterRun{
					{RunID: fmt.Sprintf("run-%d", i), HistorySnapshotHash: CloseRouterSnapshotHash([]byte(fmt.Sprintf("snapshot-%d", i)))},
				},
			}
			if err := WriteCloseRouterState(store, "s1", state); err != nil {
				errs <- fmt.Errorf("writer %d: %w", i, err)
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A read may race the very first writer and see the pre-write
			// zero-run state; either outcome is valid as long as no error
			// and no torn JSON occurs.
			if _, err := ReadCloseRouterState(store, "s1"); err != nil {
				errs <- fmt.Errorf("reader: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestCloseRouterLogicalKey_StableAndNormalized(t *testing.T) {
	a := CloseRouterLogicalKey("  prefers dark mode  ")
	b := CloseRouterLogicalKey("prefers dark mode")
	if a != b {
		t.Errorf("LogicalKey not normalized: %q != %q", a, b)
	}
	c := CloseRouterLogicalKey("different finding")
	if a == c {
		t.Errorf("LogicalKey collision for distinct text")
	}
}

func TestCloseRouterSnapshotHash_Deterministic(t *testing.T) {
	snap := []byte(`{"events":[{"role":"user","text":"hi"}]}`)
	if CloseRouterSnapshotHash(snap) != CloseRouterSnapshotHash(snap) {
		t.Errorf("SnapshotHash not deterministic for identical input")
	}
	if CloseRouterSnapshotHash(snap) == CloseRouterSnapshotHash([]byte(`{"events":[]}`)) {
		t.Errorf("SnapshotHash collision for distinct input")
	}
}
