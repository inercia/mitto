package stats

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeBeadsLister returns canned per-directory bd list payloads.
type fakeBeadsLister struct {
	payloads map[string][]byte
	err      error
	calls    int
}

func (f *fakeBeadsLister) List(_ context.Context, dir string) ([]byte, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.payloads[dir], nil
}

func wsLister(ws ...BeadsWorkspace) BeadsWorkspaceLister {
	return func() []BeadsWorkspace { return ws }
}

func newBeadsTestSource(t *testing.T, store Store, lister BeadsLister, ws BeadsWorkspaceLister, now time.Time) *BeadsSource {
	t.Helper()
	return NewBeadsSource(store, lister, ws, BeadsSourceOptions{
		Interval:     -1, // disable periodic loop; tests call Run directly
		StartupDelay: 0,
		Now:          func() time.Time { return now },
	})
}

func TestBeadsSource_FoldsOpenedClosedAndCycleTime(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-04-10T12:00:00Z")

	payload := []byte(`[
		{"id":"a-1","status":"open","created_at":"2026-04-10T01:00:00Z"},
		{"id":"a-2","status":"closed","created_at":"2026-04-09T00:00:00Z","closed_at":"2026-04-10T02:00:00Z","started_at":"2026-04-09T23:00:00Z"},
		{"id":"a-3","status":"closed","created_at":"2026-04-10T00:00:00Z","closed_at":"2026-04-10T02:30:00Z","metadata":{"claimed_at":"2026-04-10T01:30:00Z"}}
	]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/a": payload}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-a", Dir: "/ws/a"}), now)

	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	openedBucket := hourBucket(t, "2026-04-10T02:00:00Z")
	if got := countAt(t, s, hourBucket(t, "2026-04-10T01:00:00Z"), MetricBeadsOpened, BeadsSentinelSessionID, "ws-a"); got != 1 {
		t.Errorf("beads_opened @01:00 = %d, want 1", got)
	}
	if got := countAt(t, s, openedBucket, MetricBeadsClosed, BeadsSentinelSessionID, "ws-a"); got != 2 {
		t.Errorf("beads_closed @02:00 = %d, want 2 (a-2 and a-3 both close in [02:00,03:00))", got)
	}
	// a-2 cycle: 02:00 - 23:00(prev day) = 3h = 10800s (started_at, no metadata markers).
	// a-3 cycle: 02:30 - 01:30 = 1h = 3600s (metadata.claimed_at, no work_started_at).
	if got := countAt(t, s, openedBucket, MetricBeadsCycleSecondsSum, BeadsSentinelSessionID, "ws-a"); got != 10800+3600 {
		t.Errorf("beads_cycle_seconds_sum @02:00 = %d, want %d", got, 10800+3600)
	}
	if got := countAt(t, s, openedBucket, MetricBeadsCycleClosedCount, BeadsSentinelSessionID, "ws-a"); got != 2 {
		t.Errorf("beads_cycle_closed_count @02:00 = %d, want 2", got)
	}
}

func TestBeadsSource_MarkerPrecedenceWorkStartedBeatsOthers(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-04-11T00:00:00Z")
	payload := []byte(`[{"id":"p-1","status":"closed","created_at":"2026-04-10T00:00:00Z","closed_at":"2026-04-10T10:00:00Z","started_at":"2026-04-10T00:00:00Z","metadata":{"claimed_at":"2026-04-10T05:00:00Z","work_started_at":"2026-04-10T09:00:00Z"}}]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/p": payload}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-p", Dir: "/ws/p"}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bucket := hourBucket(t, "2026-04-10T10:00:00Z")
	// work_started_at (09:00) wins over claimed_at (05:00) and started_at (00:00): 1h = 3600s.
	if got := countAt(t, s, bucket, MetricBeadsCycleSecondsSum, BeadsSentinelSessionID, "ws-p"); got != 3600 {
		t.Errorf("cycle seconds = %d, want 3600 (work_started_at should win)", got)
	}
}

func TestBeadsSource_ExcludesClosedBeadWithNoMarker(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-04-12T00:00:00Z")
	payload := []byte(`[{"id":"n-1","status":"closed","created_at":"2026-04-11T00:00:00Z","closed_at":"2026-04-11T05:00:00Z"}]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/n": payload}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-n", Dir: "/ws/n"}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bucket := hourBucket(t, "2026-04-11T05:00:00Z")
	if got := countAt(t, s, bucket, MetricBeadsClosed, BeadsSentinelSessionID, "ws-n"); got != 1 {
		t.Errorf("beads_closed = %d, want 1 (closed count is unaffected by marker absence)", got)
	}
	if got := countAt(t, s, bucket, MetricBeadsCycleClosedCount, BeadsSentinelSessionID, "ws-n"); got != 0 {
		t.Errorf("beads_cycle_closed_count = %d, want 0 (no marker -> excluded)", got)
	}
	if got := countAt(t, s, bucket, MetricBeadsCycleSecondsSum, BeadsSentinelSessionID, "ws-n"); got != 0 {
		t.Errorf("beads_cycle_seconds_sum = %d, want 0", got)
	}
}

func TestBeadsSource_RerunIsIdempotentAndEvictsStaleData(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-04-13T00:00:00Z")
	dir := "/ws/r"
	lister := &fakeBeadsLister{payloads: map[string][]byte{
		dir: []byte(`[{"id":"r-1","status":"open","created_at":"2026-04-12T00:00:00Z"}]`),
	}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-r", Dir: dir}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	bucket := hourBucket(t, "2026-04-12T00:00:00Z")
	if got := countAt(t, s, bucket, MetricBeadsOpened, BeadsSentinelSessionID, "ws-r"); got != 1 {
		t.Fatalf("after first run beads_opened = %d, want 1", got)
	}

	// Re-run with the SAME payload: must not double-count (last-write-wins
	// via ReplaceDeltas, not additive).
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := countAt(t, s, bucket, MetricBeadsOpened, BeadsSentinelSessionID, "ws-r"); got != 1 {
		t.Errorf("after re-run beads_opened = %d, want 1 (not doubled)", got)
	}

	// Bead deleted from the next snapshot: its row must be evicted, not left
	// stale (the core reason ReplaceDeltas exists over UpsertDeltas).
	lister.payloads[dir] = []byte(`[]`)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if got := countAt(t, s, bucket, MetricBeadsOpened, BeadsSentinelSessionID, "ws-r"); got != 0 {
		t.Errorf("after deletion beads_opened = %d, want 0 (stale row not evicted)", got)
	}
}

func TestBeadsSource_ListErrorAbortsWithoutWriting(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-04-14T00:00:00Z")
	dir := "/ws/e"
	// First, a successful pass seeds data.
	lister := &fakeBeadsLister{payloads: map[string][]byte{
		dir: []byte(`[{"id":"e-1","status":"open","created_at":"2026-04-13T00:00:00Z"}]`),
	}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-e", Dir: dir}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("seed Run: %v", err)
	}
	bucket := hourBucket(t, "2026-04-13T00:00:00Z")
	if got := countAt(t, s, bucket, MetricBeadsOpened, BeadsSentinelSessionID, "ws-e"); got != 1 {
		t.Fatalf("seed beads_opened = %d, want 1", got)
	}

	// Now List starts failing: Run must return an error and must NOT touch
	// the store, so the seeded data survives.
	lister.err = errors.New("bd: transient failure")
	if err := src.Run(context.Background()); err == nil {
		t.Fatal("Run with a failing lister returned nil error, want non-nil")
	}
	if got := countAt(t, s, bucket, MetricBeadsOpened, BeadsSentinelSessionID, "ws-e"); got != 1 {
		t.Errorf("after failed pass beads_opened = %d, want 1 (must survive an aborted pass)", got)
	}
}

func TestBeadsSource_SkipsEmptyWorkspaceDir(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-04-15T00:00:00Z")
	lister := &fakeBeadsLister{}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-empty", Dir: ""}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lister.calls != 0 {
		t.Errorf("List called %d times for an empty-Dir workspace, want 0", lister.calls)
	}
}

// TestBeadsSource_NonStringMetadataValueDoesNotAbortPass reproduces mitto-049d:
// bd stores metadata values with their native JSON type (bd's own JIRA-sync
// prompts write jira_synced_comments as an integer), but beadsItem.Metadata
// is declared map[string]string, so json.Unmarshal fails on ANY non-string
// metadata value anywhere in the workspace snapshot. Because Run is
// all-or-nothing (see the package doc comment), this one bad bead aborts the
// ENTIRE pass -- including workspaces with no bad data at all -- which is
// the reported "469 WARN storm in 6h, agentgateway workspace excluded from
// stats" symptom (in production the storm comes from a debounced watcher
// re-running Run against the same permanently-bad snapshot).
//
// This test asserts the bug is worse than "one workspace excluded": a SECOND,
// perfectly clean workspace's opened count is also silently dropped by the
// same aborted pass. It also asserts the fix must not simply drop the
// Metadata field: the bad bead's own claimed_at cycle-time marker must still
// resolve once the parse tolerates the sibling numeric value.
func TestBeadsSource_NonStringMetadataValueDoesNotAbortPass(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-04-16T12:00:00Z")

	// Mirrors the reported production shape: jira_synced_comments as a bare
	// JSON number sitting alongside the string claimed_at marker.
	badPayload := []byte(`[{"id":"g-1","status":"closed","created_at":"2026-04-16T00:00:00Z","closed_at":"2026-04-16T02:00:00Z","metadata":{"jira_synced_comments":55011457,"claimed_at":"2026-04-16T01:00:00Z"}}]`)
	// A second, unrelated workspace with clean data -- proves the blast
	// radius spans every workspace in the pass, not just the offending one.
	cleanPayload := []byte(`[{"id":"c-1","status":"open","created_at":"2026-04-16T01:00:00Z"}]`)

	lister := &fakeBeadsLister{payloads: map[string][]byte{
		"/ws/g": badPayload,
		"/ws/c": cleanPayload,
	}}
	src := newBeadsTestSource(t, s, lister, wsLister(
		BeadsWorkspace{UUID: "ws-g", Dir: "/ws/g"},
		BeadsWorkspace{UUID: "ws-c", Dir: "/ws/c"},
	), now)

	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v (mitto-049d: a numeric metadata value must not abort the whole pass)", err)
	}

	closedBucket := hourBucket(t, "2026-04-16T02:00:00Z")
	if got := countAt(t, s, closedBucket, MetricBeadsClosed, BeadsSentinelSessionID, "ws-g"); got != 1 {
		t.Errorf("beads_closed @ws-g = %d, want 1", got)
	}
	// claimed_at (01:00) -> closed_at (02:00) = 1h = 3600s. Asserted so a
	// naive fix that just deletes the Metadata field (losing the work-start
	// marker) does not pass this test.
	if got := countAt(t, s, closedBucket, MetricBeadsCycleSecondsSum, BeadsSentinelSessionID, "ws-g"); got != 3600 {
		t.Errorf("beads_cycle_seconds_sum @ws-g = %d, want 3600 (claimed_at marker must still resolve)", got)
	}

	openedBucket := hourBucket(t, "2026-04-16T01:00:00Z")
	if got := countAt(t, s, openedBucket, MetricBeadsOpened, BeadsSentinelSessionID, "ws-c"); got != 1 {
		t.Errorf("beads_opened @ws-c = %d, want 1 (unrelated workspace must not be silently dropped by ws-g's bad metadata)", got)
	}
}
