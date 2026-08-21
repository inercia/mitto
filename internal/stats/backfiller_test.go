package stats

// Tests for the Backfiller (mitto-a86b.5). Cover the acceptance criteria from
// the plan comment on the bead:
//
//   * cursor idempotency — running twice does not double-count.
//   * estimator-version bump — persisted counts are wiped and rebuilt when the
//     package-level EstimatorVersion moves ahead of stats_meta.
//   * empty / missing session events — non-fatal (ErrSessionNotFound short-
//     circuits the replay of that one session, other sessions still advance).
//   * workspace resolution — the SessionContext returned by WorkspaceResolver
//     is honored on the deltas the aggregator emits.
//   * lifecycle — Close returns even when Start's periodic loop has fired at
//     least once; InProgress reflects the current pass.
//
// Tests share a lightweight fakeLister that satisfies SessionLister without
// touching the on-disk session.Store, so the whole file runs in <1s and stays
// hermetic. The estimator-bump path uses the real SQLiteStore so we exercise
// the transactional wipe end-to-end.

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// -----------------------------------------------------------------------------
// Fake SessionLister
// -----------------------------------------------------------------------------

// fakeLister returns fixed session metadata + events. ReadEventsFrom pages the
// backing slice by afterSeq (strictly greater) and limit, matching the real
// session.Store contract (see internal/session/store.go).
type fakeLister struct {
	mu       sync.Mutex
	metas    []session.Metadata
	events   map[string][]session.Event
	notFound map[string]bool  // sessions whose ReadEventsFrom returns ErrSessionNotFound
	failList error            // if non-nil, List returns this error
	reads    map[string]int64 // count of ReadEventsFrom calls per session
}

func newFakeLister() *fakeLister {
	return &fakeLister{
		events:   map[string][]session.Event{},
		notFound: map[string]bool{},
		reads:    map[string]int64{},
	}
}

func (f *fakeLister) List() ([]session.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList != nil {
		return nil, f.failList
	}
	out := make([]session.Metadata, len(f.metas))
	copy(out, f.metas)
	return out, nil
}

func (f *fakeLister) ReadEventsFrom(sessionID string, afterSeq int64, limit int) ([]session.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[sessionID]++
	if f.notFound[sessionID] {
		return nil, session.ErrSessionNotFound
	}
	all := f.events[sessionID]
	out := make([]session.Event, 0, limit)
	for _, ev := range all {
		if ev.Seq > afterSeq {
			out = append(out, ev)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeLister) addSession(id, workingDir, acpServer string, evs ...session.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metas = append(f.metas, session.Metadata{
		SessionID:  id,
		WorkingDir: workingDir,
		ACPServer:  acpServer,
	})
	f.events[id] = append(f.events[id], evs...)
}

func (f *fakeLister) readCount(id string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[id]
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// bfTS returns a UTC RFC3339 timestamp; keeps test data reproducible.
func bfTS(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse ts %q: %v", s, err)
	}
	return ts.UTC()
}

// promptEv builds a user_prompt event with the given seq/text. The aggregator
// increments MetricPrompts by 1 per event; input_tokens_est is (len+3)/4.
func promptEv(seq int64, ts time.Time, msg string) session.Event {
	return session.Event{
		Seq:       seq,
		Type:      session.EventTypeUserPrompt,
		Timestamp: ts,
		Data:      session.UserPromptData{Message: msg},
	}
}

// bfTestAggregator returns an aggregator that never timer-flushes and never
// batch-flushes during a test. Tests call agg.Flush() explicitly.
func bfTestAggregator(store Store) Aggregator {
	return NewAggregator(store, AggregatorOptions{
		FlushInterval: time.Hour,
		MaxBatch:      1_000_000,
		BufferSize:    4096,
	})
}

// resolverFor returns a WorkspaceResolver that maps session IDs to workspace
// UUIDs from the provided lookup table. Missing IDs get empty Workspace,
// matching the "workspace-less sessions are legal" contract.
func resolverFor(m map[string]string) WorkspaceResolver {
	return func(meta session.Metadata) SessionContext {
		return SessionContext{
			SessionID:  meta.SessionID,
			Workspace:  m[meta.SessionID],
			WorkingDir: meta.WorkingDir,
			ACPServer:  meta.ACPServer,
		}
	}
}

// -----------------------------------------------------------------------------
// Cursor idempotency: Run twice → same totals as one Run
// -----------------------------------------------------------------------------

func TestBackfiller_Run_Idempotent_UsingSQLiteCursor(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	lister.addSession("s1", "/w", "acp",
		promptEv(1, ts, "hello"),
		promptEv(2, ts, "world"),
		promptEv(3, ts, "again"),
	)

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval:     -1, // disable the periodic loop; we drive Run manually
		StartupDelay: 0,
	})

	// First pass: seeds cursors, records 3 prompts.
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush #1: %v", err)
	}

	pointsAfterFirst := queryPrompts(t, store, "s1")
	if pointsAfterFirst != 3 {
		t.Fatalf("after Run #1: prompts=%d, want 3", pointsAfterFirst)
	}

	// Second pass: cursor is at seq=3, so ReadEventsFrom returns nothing that
	// the aggregator should count. Totals must be unchanged.
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush #2: %v", err)
	}
	pointsAfterSecond := queryPrompts(t, store, "s1")
	if pointsAfterSecond != 3 {
		t.Fatalf("after Run #2: prompts=%d, want 3 (idempotent)", pointsAfterSecond)
	}
}

// queryPrompts returns the total MetricPrompts value across all buckets
// visible to Store.Query. The Query interface does not filter by SessionID
// (dashboard reads are workspace-scoped), so the sessionID parameter is only
// used as a label in test failure messages. Callers that need per-session
// isolation should verify via Store.GetCursor.
func queryPrompts(t *testing.T, store Store, sessionID string) int64 {
	t.Helper()
	_ = sessionID // reserved for future per-session filtering; unused today
	pts, err := store.Query(context.Background(), Query{
		RangeFrom: bfTS(t, "2020-01-01T00:00:00Z"),
		RangeTo:   bfTS(t, "2030-01-01T00:00:00Z"),
		Bucket:    BucketHour,
		Metrics:   []string{MetricPrompts},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var total int64
	for _, p := range pts {
		total += p.Value
	}
	return total
}

// -----------------------------------------------------------------------------
// Estimator-version bump: persisted counts are wiped, then re-ingested
// -----------------------------------------------------------------------------

func TestBackfiller_Run_EstimatorBump_TriggersReset(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	lister.addSession("s1", "/w", "acp",
		promptEv(1, ts, "hello"),
		promptEv(2, ts, "world"),
	)

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval: -1,
	})

	// First run: seeds estimator_version = current, records 2 prompts.
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush #1: %v", err)
	}
	if got := queryPrompts(t, store, "s1"); got != 2 {
		t.Fatalf("after Run #1: prompts=%d, want 2", got)
	}

	// Simulate an estimator regression by rolling back stats_meta.estimator_version
	// to a value below EstimatorVersion. The next Run must detect this and
	// call ResetForEstimatorBump, wiping events + cursors.
	if err := store.SetMeta(context.Background(), "estimator_version", strconv.Itoa(EstimatorVersion-1)); err != nil {
		t.Fatalf("SetMeta rollback: %v", err)
	}

	// Second run: the estimator gate resets, then Run replays every event
	// from the fresh cursor. Total prompts should be 2 again (not 4).
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush #2: %v", err)
	}
	if got := queryPrompts(t, store, "s1"); got != 2 {
		t.Fatalf("after estimator-bump reset: prompts=%d, want 2", got)
	}

	// The version meta row must be back at EstimatorVersion after the reset.
	v, err := store.GetMeta(context.Background(), "estimator_version")
	if err != nil {
		t.Fatalf("GetMeta estimator_version: %v", err)
	}
	if v != strconv.Itoa(EstimatorVersion) {
		t.Errorf("estimator_version = %q, want %q", v, strconv.Itoa(EstimatorVersion))
	}
}

// First-boot invariant: with NO estimator_version row present, Run seeds the
// row without invoking ResetForEstimatorBump — a fresh DB has nothing to wipe.
func TestBackfiller_Run_FirstBoot_SeedsEstimatorWithoutReset(t *testing.T) {
	store := &countingStore{NoopStore: NoopStore{}}
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	lister := newFakeLister() // no sessions — Run should still seed the meta
	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval: -1,
	})

	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if store.resetCalls.Load() != 0 {
		t.Errorf("ResetForEstimatorBump called %d times, want 0 on first-boot",
			store.resetCalls.Load())
	}
	if store.setMetaCalls("estimator_version") == 0 {
		t.Errorf("SetMeta(estimator_version) not called on first-boot")
	}
}

// -----------------------------------------------------------------------------
// Missing events file: session with ErrSessionNotFound is skipped, others advance
// -----------------------------------------------------------------------------

func TestBackfiller_Run_MissingEventsFile_IsNonFatal(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	// s-missing has metadata but no events.jsonl — the ReadEventsFrom call
	// should surface session.ErrSessionNotFound.
	lister.addSession("s-missing", "/w", "acp")
	lister.notFound["s-missing"] = true
	// s-live has real events; it must still be replayed.
	lister.addSession("s-live", "/w", "acp", promptEv(1, ts, "hi"))

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval: -1,
	})
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// s-live's prompt should be counted; s-missing contributes nothing so
	// the global total is 1.
	if got := queryPrompts(t, store, "s-live"); got != 1 {
		t.Errorf("total prompts=%d, want 1 (missing peer must not block or contribute)", got)
	}
	// s-live's cursor advanced to seq=1; s-missing's cursor never wrote
	// (no events reached the aggregator).
	liveCur, err := store.GetCursor(context.Background(), "s-live")
	if err != nil {
		t.Fatalf("GetCursor s-live: %v", err)
	}
	if liveCur.LastEventSeq != 1 {
		t.Errorf("s-live cursor.LastEventSeq=%d, want 1", liveCur.LastEventSeq)
	}
	if _, err := store.GetCursor(context.Background(), "s-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("s-missing cursor err=%v, want ErrNotFound (no events replayed)", err)
	}
}

// Empty SessionID rows in the lister must be skipped silently.
func TestBackfiller_Run_SkipsEmptySessionID(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	lister := newFakeLister()
	lister.metas = append(lister.metas, session.Metadata{SessionID: ""}) // skipped
	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister.addSession("s1", "/w", "acp", promptEv(1, ts, "x"))

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval: -1,
	})
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// Empty session must not have generated a ReadEventsFrom call.
	if lister.readCount("") != 0 {
		t.Errorf("empty session read %d times, want 0", lister.readCount(""))
	}
	if got := queryPrompts(t, store, "s1"); got != 1 {
		t.Errorf("s1 prompts=%d, want 1", got)
	}
}

// -----------------------------------------------------------------------------
// Workspace resolver: deltas carry the resolved workspace UUID
// -----------------------------------------------------------------------------

func TestBackfiller_Run_ResolverStampsWorkspaceOnDeltas(t *testing.T) {
	// Use a fake Store here so we can inspect the raw deltas the aggregator
	// wrote (the SQLite store already collapses per-workspace rows on read).
	fs := &fakeStore{}
	agg := bfTestAggregator(fs)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	lister.addSession("s-a", "/wa", "acp", promptEv(1, ts, "a"))
	lister.addSession("s-b", "/wb", "acp", promptEv(1, ts, "b"))

	// Only s-a gets a workspace UUID; s-b's Workspace stays "" (legal).
	resolver := resolverFor(map[string]string{"s-a": "ws-alpha"})

	bf := NewBackfiller(fs, agg, lister, resolver, BackfillerOptions{
		Interval: -1,
	})
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	sawA, sawB := false, false
	for _, c := range fs.snapshot() {
		for _, d := range c.deltas {
			if d.Metric != MetricPrompts {
				continue
			}
			switch d.SessionID {
			case "s-a":
				sawA = true
				if d.Workspace != "ws-alpha" {
					t.Errorf("s-a workspace=%q, want %q", d.Workspace, "ws-alpha")
				}
			case "s-b":
				sawB = true
				if d.Workspace != "" {
					t.Errorf("s-b workspace=%q, want empty", d.Workspace)
				}
			}
		}
	}
	if !sawA || !sawB {
		t.Errorf("missing deltas: sawA=%v sawB=%v", sawA, sawB)
	}
}

// -----------------------------------------------------------------------------
// Chunked replay: sessions with >ChunkSize events page through
// -----------------------------------------------------------------------------

func TestBackfiller_Run_PagesLargeSession(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	// 250 prompts, ChunkSize = 100 → 3 pages (100, 100, 50).
	evs := make([]session.Event, 0, 250)
	for i := int64(1); i <= 250; i++ {
		evs = append(evs, promptEv(i, ts, "x"))
	}
	lister.addSession("big", "/w", "acp", evs...)

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval:  -1,
		ChunkSize: 100,
	})
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := queryPrompts(t, store, "big"); got != 250 {
		t.Errorf("big prompts=%d, want 250 across 3 pages", got)
	}
	if got := lister.readCount("big"); got != 3 {
		t.Errorf("big page count=%d, want 3 (100+100+50)", got)
	}
}

// -----------------------------------------------------------------------------
// List error: propagates up, aggregator remains usable
// -----------------------------------------------------------------------------

func TestBackfiller_Run_ListError_Propagates(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	lister := newFakeLister()
	sentinel := errors.New("list boom")
	lister.failList = sentinel

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval: -1,
	})
	err := bf.Run(context.Background())
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("Run err=%v, want wrap of %v", err, sentinel)
	}
}

// -----------------------------------------------------------------------------
// Context cancellation aborts the Run loop
// -----------------------------------------------------------------------------

func TestBackfiller_Run_ContextCancelled(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	for i := 0; i < 5; i++ {
		lister.addSession("s"+strconv.Itoa(i), "/w", "acp", promptEv(1, ts, "x"))
	}
	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval: -1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := bf.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run err=%v, want context.Canceled", err)
	}
}

// -----------------------------------------------------------------------------
// Lifecycle: Start → first Run → Close returns cleanly
// -----------------------------------------------------------------------------

func TestBackfiller_StartAndClose(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	lister.addSession("s1", "/w", "acp", promptEv(1, ts, "hi"))

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		StartupDelay: 5 * time.Millisecond,
		Interval:     10 * time.Millisecond,
		Logger:       slog.Default(),
	})

	bf.Start(context.Background())

	// Wait for the first pass to complete: last_full_backfill_at gets stamped
	// only after a successful Run. Poll for up to 2s to keep the test hermetic
	// on a slow CI box.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, err := store.GetMeta(context.Background(), "last_full_backfill_at"); err == nil && v != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Verify Close returns promptly. If the periodic loop leaked, this hangs.
	closed := make(chan error, 1)
	go func() { closed <- bf.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within 2s — periodic loop leaked")
	}

	// Close is idempotent.
	if err := bf.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// StartupDelay=0 with Interval<0 → loop exits immediately after the first pass.
func TestBackfiller_Start_NegativeInterval_NoLoop(t *testing.T) {
	store := &NoopStore{}
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()
	lister := newFakeLister()

	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		StartupDelay: 0,
		Interval:     -1, // disables the periodic loop AND the initial run
	})
	bf.Start(context.Background())

	// Close must return without ever having fired Run — verifies we don't
	// leak a goroutine when the caller opts out of the periodic loop.
	done := make(chan error, 1)
	go func() { done <- bf.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close hung on Interval<0 start")
	}
}

// -----------------------------------------------------------------------------
// InProgress toggles across a Run
// -----------------------------------------------------------------------------

func TestBackfiller_InProgress_TogglesAroundRun(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	// A slow-listing lister lets us catch InProgress==true while Run is
	// actively pumping. We block List until the goroutine sees InProgress.
	slow := &blockingLister{
		fakeLister: newFakeLister(),
		gate:       make(chan struct{}),
	}
	bf := NewBackfiller(store, agg, slow, resolverFor(nil), BackfillerOptions{
		Interval: -1,
	})

	if bf.InProgress() {
		t.Fatal("InProgress==true before any Run")
	}

	runDone := make(chan error, 1)
	go func() { runDone <- bf.Run(context.Background()) }()

	// Wait until Run enters its List phase (InProgress toggles first).
	deadline := time.Now().Add(2 * time.Second)
	for !bf.InProgress() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !bf.InProgress() {
		close(slow.gate)
		<-runDone
		t.Fatal("InProgress never became true during Run")
	}

	// Release the lister and wait for Run to finish.
	close(slow.gate)
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bf.InProgress() {
		t.Error("InProgress==true after Run returned")
	}
}

// blockingLister wraps fakeLister and blocks List until gate is closed. Used
// only by TestBackfiller_InProgress_TogglesAroundRun.
type blockingLister struct {
	*fakeLister
	gate chan struct{}
}

func (b *blockingLister) List() ([]session.Metadata, error) {
	<-b.gate
	return b.fakeLister.List()
}

// -----------------------------------------------------------------------------
// countingStore: intrumented NoopStore to observe first-boot semantics
// -----------------------------------------------------------------------------

type countingStore struct {
	NoopStore
	resetCalls atomic.Int64

	mu   sync.Mutex
	sets map[string]int
}

func (c *countingStore) ResetForEstimatorBump(ctx context.Context) error {
	c.resetCalls.Add(1)
	return c.NoopStore.ResetForEstimatorBump(ctx)
}

func (c *countingStore) SetMeta(ctx context.Context, key, value string) error {
	c.mu.Lock()
	if c.sets == nil {
		c.sets = map[string]int{}
	}
	c.sets[key]++
	c.mu.Unlock()
	return c.NoopStore.SetMeta(ctx, key, value)
}

func (c *countingStore) setMetaCalls(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sets[key]
}

// -----------------------------------------------------------------------------
// last_full_backfill_at is stamped after a successful Run
// -----------------------------------------------------------------------------

func TestBackfiller_Run_StampsLastFullBackfillAt(t *testing.T) {
	store, _ := openTestStore(t)
	agg := bfTestAggregator(store)
	defer func() { _ = agg.Close() }()

	ts := bfTS(t, "2026-01-01T00:00:00Z")
	lister := newFakeLister()
	lister.addSession("s1", "/w", "acp", promptEv(1, ts, "hi"))

	fixed := bfTS(t, "2026-06-15T12:00:00Z")
	bf := NewBackfiller(store, agg, lister, resolverFor(nil), BackfillerOptions{
		Interval: -1,
		Now:      func() time.Time { return fixed },
	})
	if err := bf.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	v, err := store.GetMeta(context.Background(), "last_full_backfill_at")
	if err != nil {
		t.Fatalf("GetMeta last_full_backfill_at: %v", err)
	}
	if v != fixed.Format(time.RFC3339) {
		t.Errorf("last_full_backfill_at = %q, want %q", v, fixed.Format(time.RFC3339))
	}
}

// openTestStore is defined in sqlite_store_test.go and shared across every
// _test.go in this package.
