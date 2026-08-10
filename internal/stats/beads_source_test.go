package stats

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

// blockingBeadsLister lets a test hold one Run pass "in flight" (blocked
// inside List) while other concurrent Run calls pile up, so the test can
// deterministically observe how many full passes actually execute.
type blockingBeadsLister struct {
	calls   atomic.Int64
	entered chan struct{} // closed-once signal: at least one caller is blocked in List
	once    sync.Once
	release chan struct{} // closed by the test to unblock every blocked/future caller
}

func newBlockingBeadsLister() *blockingBeadsLister {
	return &blockingBeadsLister{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingBeadsLister) List(ctx context.Context, dir string) ([]byte, error) {
	b.calls.Add(1)
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []byte(`[]`), nil
}

// TestBeadsSource_Run_ConcurrentTriggersDoNotCoalesce reproduces mitto-2o5e:
// BeadsSource.Run's inProgress.CompareAndSwap guard is checked AFTER runMu is
// already held (see the package doc + Run's comment claiming "Concurrent Run
// calls serialise on runMu"), so by the time a waiter acquires runMu the
// previous holder has already reset inProgress to false in its own deferred
// cleanup (Store(false) runs before Unlock(), since defers execute LIFO).
// The guard therefore never observes "true" and never actually coalesces:
// every one of N concurrent triggers runs its own full pass back-to-back,
// which is exactly the "26 full 22-workspace stats passes" storm reported on
// the bead (internal/web/server.go's watcher subscriber fires one goroutine
// per debounced fs event with no additional rate limiting, relying entirely
// on this guard to collapse overlapping triggers).
//
// A working coalescing guard would collapse N overlapping triggers into at
// most 2 executions (the one already in flight, plus at most one more
// representing every request that arrived while it ran). This test starts 5
// concurrent Run calls against a lister blocked on the first pass, releases
// them once all 5 have had a chance to queue up, and asserts at most 2
// List invocations occurred. On the current buggy guard this fails with 5.
func TestBeadsSource_Run_ConcurrentTriggersDoNotCoalesce(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-08-08T00:00:00Z")
	lister := newBlockingBeadsLister()
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-x", Dir: "/ws/x"}), now)

	const triggers = 5
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(triggers)
	for i := 0; i < triggers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_ = src.Run(context.Background())
		}()
	}
	close(start)

	// Wait for the first goroutine to actually be blocked inside List.
	select {
	case <-lister.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no Run call entered List within 2s")
	}
	// Give the remaining goroutines a moment to queue up behind runMu before
	// releasing the first pass — this is what makes the triggers genuinely
	// overlapping/concurrent rather than accidentally sequential.
	time.Sleep(100 * time.Millisecond)
	close(lister.release)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Run calls did not complete within 5s")
	}

	if got := lister.calls.Load(); got > 2 {
		t.Errorf("mitto-2o5e: List invoked %d times for %d concurrent overlapping Run triggers, want <= 2 (inProgress CompareAndSwap is checked after runMu is acquired, so it never observes an in-flight pass and never coalesces — every trigger runs its own full pass)", got, triggers)
	}
}

// -----------------------------------------------------------------------------
// downtimeBetween (mitto-c45m)
// -----------------------------------------------------------------------------

func TestDowntimeBetween_FullyUp(t *testing.T) {
	from := hourBucket(t, "2026-05-01T09:00:00Z")
	to := hourBucket(t, "2026-05-01T09:30:00Z")
	uptime := map[int64]int64{hourBucket(t, "2026-05-01T09:00:00Z").Unix(): 3600}
	if got := downtimeBetween(from, to, uptime); got != 0 {
		t.Errorf("downtime = %v, want 0 (bucket fully up)", got)
	}
}

func TestDowntimeBetween_FullyDown(t *testing.T) {
	from := hourBucket(t, "2026-05-01T09:00:00Z")
	to := hourBucket(t, "2026-05-01T09:30:00Z")
	uptime := map[int64]int64{hourBucket(t, "2026-05-01T09:00:00Z").Unix(): 0}
	if got := downtimeBetween(from, to, uptime); got != 30*time.Minute {
		t.Errorf("downtime = %v, want 30m (bucket fully down)", got)
	}
}

// TestDowntimeBetween_AbsentBucketTreatedAsFullyUp pins the fail-open
// contract documented on downtimeBetween: a bucket with NO evidence (never
// recorded, or pruned by retention) must read as fully up, not fully down —
// otherwise historical/pruned beads would collapse to active==0 instead of
// degrading to the calendar series.
func TestDowntimeBetween_AbsentBucketTreatedAsFullyUp(t *testing.T) {
	from := hourBucket(t, "2026-05-01T09:00:00Z")
	to := hourBucket(t, "2026-05-01T09:30:00Z")
	uptime := map[int64]int64{}
	if got := downtimeBetween(from, to, uptime); got != 0 {
		t.Errorf("downtime = %v, want 0 (absent bucket => fully up)", got)
	}
}

// TestDowntimeBetween_PartialOverlapAcrossTwoBuckets exercises the walk
// across an hour boundary with two different uptime fractions per bucket.
func TestDowntimeBetween_PartialOverlapAcrossTwoBuckets(t *testing.T) {
	from := hourBucket(t, "2026-05-01T09:30:00Z")
	to := hourBucket(t, "2026-05-01T10:30:00Z")
	uptime := map[int64]int64{
		hourBucket(t, "2026-05-01T09:00:00Z").Unix(): 1800, // half up -> 30min overlap * 0.5 down = 15min
		hourBucket(t, "2026-05-01T10:00:00Z").Unix(): 3600, // fully up -> 0 down
	}
	if got, want := downtimeBetween(from, to, uptime), 15*time.Minute; got != want {
		t.Errorf("downtime = %v, want %v", got, want)
	}
}

// TestDowntimeBetween_FractionAboveOneIsClamped defends against a corrupted
// or double-counted uptime row (>3600s in one hour bucket) producing
// negative downtime.
func TestDowntimeBetween_FractionAboveOneIsClamped(t *testing.T) {
	from := hourBucket(t, "2026-05-01T09:00:00Z")
	to := hourBucket(t, "2026-05-01T09:10:00Z")
	uptime := map[int64]int64{hourBucket(t, "2026-05-01T09:00:00Z").Unix(): 7200}
	if got := downtimeBetween(from, to, uptime); got != 0 {
		t.Errorf("downtime = %v, want 0 (fraction > 1 must clamp, not go negative)", got)
	}
}

func TestDowntimeBetween_ZeroOrNegativeSpanReturnsZero(t *testing.T) {
	ts := hourBucket(t, "2026-05-01T09:00:00Z")
	if got := downtimeBetween(ts, ts, nil); got != 0 {
		t.Errorf("equal from/to: downtime = %v, want 0", got)
	}
	if got := downtimeBetween(ts, ts.Add(-time.Minute), nil); got != 0 {
		t.Errorf("to before from: downtime = %v, want 0", got)
	}
}

// -----------------------------------------------------------------------------
// Active-cycle fold, end-to-end via Run (mitto-c45m)
// -----------------------------------------------------------------------------

// seedUptime writes MetricUptimeSeconds rows directly, mirroring what
// UptimeRecorder would have persisted, so BeadsSource.loadUptime observes
// them on its next pass.
func seedUptime(t *testing.T, s Store, bucketSeconds map[string]int64) {
	t.Helper()
	deltas := make([]Delta, 0, len(bucketSeconds))
	for ts, secs := range bucketSeconds {
		deltas = append(deltas, Delta{
			TSBucket:  hourBucket(t, ts),
			Metric:    MetricUptimeSeconds,
			SessionID: UptimeSentinelSessionID,
			Value:     secs,
		})
	}
	if err := s.UpsertDeltas(context.Background(), deltas); err != nil {
		t.Fatalf("seedUptime: %v", err)
	}
}

func TestBeadsSource_ActiveCycle_FullUptimeEqualsCalendar(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-05-10T12:00:00Z")
	seedUptime(t, s, map[string]int64{"2026-05-10T09:00:00Z": 3600})

	payload := []byte(`[{"id":"u-1","status":"closed","created_at":"2026-05-10T09:00:00Z","closed_at":"2026-05-10T09:30:00Z","metadata":{"claimed_at":"2026-05-10T09:00:00Z"}}]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/u": payload}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-u", Dir: "/ws/u"}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	bucket := hourBucket(t, "2026-05-10T09:00:00Z")
	if got := countAt(t, s, bucket, MetricBeadsCycleSecondsSum, BeadsSentinelSessionID, "ws-u"); got != 1800 {
		t.Fatalf("calendar cycle sum = %d, want 1800", got)
	}
	if got := countAt(t, s, bucket, MetricBeadsActiveCycleSecondsSum, BeadsSentinelSessionID, "ws-u"); got != 1800 {
		t.Errorf("active cycle sum = %d, want 1800 (fully up => active == calendar)", got)
	}
	if got := countAt(t, s, bucket, MetricBeadsActiveCycleClosedCount, BeadsSentinelSessionID, "ws-u"); got != 1 {
		t.Errorf("active cycle closed count = %d, want 1", got)
	}
}

func TestBeadsSource_ActiveCycle_SubtractsPartialDowntime(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-05-11T13:00:00Z")
	seedUptime(t, s, map[string]int64{
		"2026-05-11T09:00:00Z": 3600, // fully up
		"2026-05-11T10:00:00Z": 1800, // half up -> 30min down
	})

	// Cycle: 09:00 -> 11:00 = 2h = 7200s, spanning the two seeded buckets.
	payload := []byte(`[{"id":"u-2","status":"closed","created_at":"2026-05-11T09:00:00Z","closed_at":"2026-05-11T11:00:00Z","metadata":{"claimed_at":"2026-05-11T09:00:00Z"}}]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/u2": payload}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-u2", Dir: "/ws/u2"}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	closedBucket := hourBucket(t, "2026-05-11T11:00:00Z")
	if got := countAt(t, s, closedBucket, MetricBeadsCycleSecondsSum, BeadsSentinelSessionID, "ws-u2"); got != 7200 {
		t.Fatalf("calendar cycle sum = %d, want 7200", got)
	}
	// downtime = 0 (09:00 bucket, fully up) + 1800 (10:00 bucket, half up) = 1800s.
	if got := countAt(t, s, closedBucket, MetricBeadsActiveCycleSecondsSum, BeadsSentinelSessionID, "ws-u2"); got != 5400 {
		t.Errorf("active cycle sum = %d, want 5400 (7200 - 1800 downtime)", got)
	}
}

// TestBeadsSource_ActiveCycle_NearZeroSumStaysCounted pins the "count stays
// visible even when the sum is nearly 0" nuance from the plan: a bead closed
// during an hour that was almost entirely Mitto downtime contributes almost
// no active seconds but still increments activeCycleClosedCount, so the
// sample is not silently dropped from the average's denominator.
//
// Note: a LITERAL zero uptime value cannot be seeded through the normal
// UpsertDeltas path (Store.UpsertDeltas silently skips Value==0 deltas —
// see its doc comment), which also matches production reality: UptimeRecorder
// only ever writes while the process is running, so it can never itself
// produce a "confirmed fully down" row. Value=1 (the smallest representable
// heartbeat) exercises the same near-zero-sum/visible-count behavior via the
// real write path; the true fully-down/clamp arithmetic is covered directly
// by TestDowntimeBetween_FullyDown.
func TestBeadsSource_ActiveCycle_NearZeroSumStaysCounted(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-05-12T12:00:00Z")
	seedUptime(t, s, map[string]int64{"2026-05-12T09:00:00Z": 1})

	payload := []byte(`[{"id":"u-3","status":"closed","created_at":"2026-05-12T09:00:00Z","closed_at":"2026-05-12T10:00:00Z","metadata":{"claimed_at":"2026-05-12T09:00:00Z"}}]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/u3": payload}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-u3", Dir: "/ws/u3"}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	closedBucket := hourBucket(t, "2026-05-12T10:00:00Z")
	// fraction = 1/3600; downtime = 3600*(1-1/3600) = 3599s; active = 3600-3599 = 1s.
	if got := countAt(t, s, closedBucket, MetricBeadsActiveCycleSecondsSum, BeadsSentinelSessionID, "ws-u3"); got != 1 {
		t.Errorf("active cycle sum = %d, want 1 (almost entirely downtime)", got)
	}
	if got := countAt(t, s, closedBucket, MetricBeadsActiveCycleClosedCount, BeadsSentinelSessionID, "ws-u3"); got != 1 {
		t.Errorf("active cycle closed count = %d, want 1 (sample stays visible even though sum is ~0)", got)
	}
}

// TestBeadsSource_ActiveCycle_NoUptimeDataDegradesToCalendar reproduces the
// "history predating the heartbeat" scenario: no MetricUptimeSeconds rows
// exist at all, so loadUptime returns an empty map and every bucket must
// read as fully up.
func TestBeadsSource_ActiveCycle_NoUptimeDataDegradesToCalendar(t *testing.T) {
	s, _ := openTestStore(t)
	now := hourBucket(t, "2026-05-13T12:00:00Z")

	payload := []byte(`[{"id":"u-4","status":"closed","created_at":"2026-05-13T09:00:00Z","closed_at":"2026-05-13T09:30:00Z","metadata":{"claimed_at":"2026-05-13T09:00:00Z"}}]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/u4": payload}}
	src := newBeadsTestSource(t, s, lister, wsLister(BeadsWorkspace{UUID: "ws-u4", Dir: "/ws/u4"}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	bucket := hourBucket(t, "2026-05-13T09:00:00Z")
	if got := countAt(t, s, bucket, MetricBeadsActiveCycleSecondsSum, BeadsSentinelSessionID, "ws-u4"); got != 1800 {
		t.Errorf("active cycle sum = %d, want 1800 (no uptime evidence => degrades to calendar)", got)
	}
}

// queryErrStore wraps a real *SQLiteStore and forces Query to fail, so tests
// can exercise BeadsSource.loadUptime's fail-open path without a bespoke
// in-memory Store reimplementation.
type queryErrStore struct {
	*SQLiteStore
	err error
}

func (q *queryErrStore) Query(ctx context.Context, query Query) ([]Point, error) {
	return nil, q.err
}

// TestBeadsSource_LoadUptime_QueryErrorFailsOpen reproduces the documented
// fail-open contract: if the uptime Query errors, the beads pass must still
// succeed (all-or-nothing only applies to the bead List/parse step) and
// every bucket must be treated as fully up.
func TestBeadsSource_LoadUptime_QueryErrorFailsOpen(t *testing.T) {
	s, _ := openTestStore(t)
	qs := &queryErrStore{SQLiteStore: s, err: errors.New("query: disk I/O error")}
	now := hourBucket(t, "2026-05-14T12:00:00Z")

	payload := []byte(`[{"id":"u-5","status":"closed","created_at":"2026-05-14T09:00:00Z","closed_at":"2026-05-14T09:30:00Z","metadata":{"claimed_at":"2026-05-14T09:00:00Z"}}]`)
	lister := &fakeBeadsLister{payloads: map[string][]byte{"/ws/u5": payload}}
	src := newBeadsTestSource(t, qs, lister, wsLister(BeadsWorkspace{UUID: "ws-u5", Dir: "/ws/u5"}), now)
	if err := src.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v (a failing uptime Query must not abort the beads pass)", err)
	}

	bucket := hourBucket(t, "2026-05-14T09:00:00Z")
	if got := countAt(t, s, bucket, MetricBeadsCycleSecondsSum, BeadsSentinelSessionID, "ws-u5"); got != 1800 {
		t.Fatalf("calendar cycle sum = %d, want 1800", got)
	}
	if got := countAt(t, s, bucket, MetricBeadsActiveCycleSecondsSum, BeadsSentinelSessionID, "ws-u5"); got != 1800 {
		t.Errorf("active cycle sum = %d, want 1800 (uptime query error => fail open to fully up)", got)
	}
}
