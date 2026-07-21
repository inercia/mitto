package stats

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// hourBucket returns a UTC-truncated time useful as a Delta.TSBucket. Tests
// use fixed timestamps (never time.Now) so failure messages are reproducible.
func hourBucket(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse ts %q: %v", s, err)
	}
	return ts.UTC()
}

// openTestStore opens a fresh SQLiteStore under t.TempDir() and registers a
// Close cleanup. Returns store + db path so tests can also reopen the file.
func openTestStore(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "stats.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dbPath
}

// readPragma returns an integer PRAGMA value; used to assert WAL / busy_timeout
// / foreign_keys after Open.
func readPragma(t *testing.T, s *SQLiteStore, pragma string) string {
	t.Helper()
	var v string
	if err := s.db.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", pragma, err)
	}
	return v
}

// -----------------------------------------------------------------------------
// DSN / PRAGMA behavior
// -----------------------------------------------------------------------------

func TestSQLiteStore_Open_EnablesWAL(t *testing.T) {
	s, _ := openTestStore(t)
	got := readPragma(t, s, "journal_mode")
	if got != "wal" {
		t.Errorf("PRAGMA journal_mode = %q, want %q", got, "wal")
	}
}

func TestSQLiteStore_Open_EnablesForeignKeys(t *testing.T) {
	s, _ := openTestStore(t)
	got := readPragma(t, s, "foreign_keys")
	if got != "1" {
		t.Errorf("PRAGMA foreign_keys = %q, want %q", got, "1")
	}
}

func TestSQLiteStore_Open_BusyTimeout(t *testing.T) {
	s, _ := openTestStore(t)
	got := readPragma(t, s, "busy_timeout")
	if got != "5000" {
		t.Errorf("PRAGMA busy_timeout = %q, want %q", got, "5000")
	}
}

func TestSQLiteStore_Open_EmptyPathErrors(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Errorf("Open(\"\") returned nil error, want non-nil")
	}
}

// -----------------------------------------------------------------------------
// Migrations
// -----------------------------------------------------------------------------

func TestSQLiteStore_Migrations_SeedsMetaOnFirstOpen(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()

	got := map[string]string{}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM stats_meta`)
	if err != nil {
		t.Fatalf("SELECT stats_meta: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if got["schema_version"] != "1" {
		t.Errorf("schema_version = %q, want %q", got["schema_version"], "1")
	}
	if got["estimator_version"] != strconv.Itoa(EstimatorVersion) {
		t.Errorf("estimator_version = %q, want %q", got["estimator_version"], strconv.Itoa(EstimatorVersion))
	}
	if _, ok := got["last_full_backfill_at"]; !ok {
		t.Errorf("last_full_backfill_at row missing")
	}
	if got["last_full_backfill_at"] != "" {
		t.Errorf("last_full_backfill_at = %q, want empty seed value", got["last_full_backfill_at"])
	}
}

func TestSQLiteStore_Migrations_NotReappliedOnReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stats.db")
	ctx := context.Background()

	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	var n int
	if err := s2.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stats_meta WHERE key='schema_version'`).Scan(&n); err != nil {
		t.Fatalf("count schema_version: %v", err)
	}
	if n != 1 {
		t.Errorf("schema_version row count = %d after reopen, want 1", n)
	}
}

// -----------------------------------------------------------------------------
// UpsertDeltas
// -----------------------------------------------------------------------------

// countAt returns the value stored at a specific bucket-key. Zero if absent.
func countAt(t *testing.T, s *SQLiteStore, ts time.Time, metric, sess, ws string) int64 {
	t.Helper()
	var v int64
	err := s.db.QueryRowContext(context.Background(),
		`SELECT value FROM stats_events
		 WHERE ts_bucket=? AND metric=? AND session_id=? AND workspace=?`,
		ts.UTC().Unix(), metric, sess, ws,
	).Scan(&v)
	if err != nil {
		return 0
	}
	return v
}

func TestSQLiteStore_UpsertDeltas_ReplaceIdempotent(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-01-01T00:00:00Z")
	d := Delta{TSBucket: ts, Metric: MetricPrompts, SessionID: "sess-a", Workspace: "ws-1", Value: 3}

	if err := s.UpsertDeltas(ctx, []Delta{d}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := s.UpsertDeltas(ctx, []Delta{d}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if got := countAt(t, s, ts, MetricPrompts, "sess-a", "ws-1"); got != 3 {
		t.Errorf("value after double-insert = %d, want 3 (REPLACE-on-conflict)", got)
	}
}

func TestSQLiteStore_UpsertDeltas_UpdatesExistingRow(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-01-01T01:00:00Z")

	if err := s.UpsertDeltas(ctx, []Delta{{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDeltas(ctx, []Delta{{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 7}}); err != nil {
		t.Fatal(err)
	}
	if got := countAt(t, s, ts, MetricPrompts, "s", "w"); got != 7 {
		t.Errorf("value after update = %d, want 7 (last-write-wins)", got)
	}
}

func TestSQLiteStore_UpsertDeltas_ZeroValueSkipped(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-01-01T02:00:00Z")

	if err := s.UpsertDeltas(ctx, []Delta{{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 0}}); err != nil {
		t.Fatal(err)
	}
	if got := countAt(t, s, ts, MetricPrompts, "s", "w"); got != 0 {
		t.Errorf("row present for Value=0 (got %d), want no row", got)
	}

	// Pre-existing row must not be clobbered by a same-key zero delta.
	if err := s.UpsertDeltas(ctx, []Delta{{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 5}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDeltas(ctx, []Delta{{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 0}}); err != nil {
		t.Fatal(err)
	}
	if got := countAt(t, s, ts, MetricPrompts, "s", "w"); got != 5 {
		t.Errorf("value after zero-delta clobber attempt = %d, want 5", got)
	}
}

func TestSQLiteStore_UpsertDeltas_BatchAtomic(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	base := hourBucket(t, "2026-02-01T00:00:00Z")

	deltas := make([]Delta, 0, 100)
	for i := 0; i < 100; i++ {
		deltas = append(deltas, Delta{
			TSBucket:  base.Add(time.Duration(i) * time.Hour),
			Metric:    MetricPrompts,
			SessionID: "s",
			Workspace: "w",
			Value:     int64(i + 1),
		})
	}
	if err := s.UpsertDeltas(ctx, deltas); err != nil {
		t.Fatalf("batch upsert: %v", err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stats_events WHERE workspace='w'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 100 {
		t.Errorf("row count after 100-row batch = %d, want 100", n)
	}
}

func TestSQLiteStore_UpsertDeltas_DedupesWithinBatch(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-03-01T00:00:00Z")

	// Three same-key rows in one batch — must not raise a PK conflict.
	batch := []Delta{
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 1},
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 5},
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 9},
	}
	if err := s.UpsertDeltas(ctx, batch); err != nil {
		t.Fatalf("batch with duplicate keys: %v", err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stats_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("dedup produced %d rows, want 1", n)
	}
}

func TestSQLiteStore_UpsertDeltas_EmptyAndNil(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	if err := s.UpsertDeltas(ctx, nil); err != nil {
		t.Errorf("UpsertDeltas(nil) = %v, want nil", err)
	}
	if err := s.UpsertDeltas(ctx, []Delta{}); err != nil {
		t.Errorf("UpsertDeltas(empty) = %v, want nil", err)
	}
}

// -----------------------------------------------------------------------------
// Cursor round trip + monotonic invariants
// -----------------------------------------------------------------------------

func TestSQLiteStore_GetCursor_MissReturnsNotFound(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()

	got, err := s.GetCursor(ctx, "sess-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if got.SessionID != "sess-missing" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-missing")
	}
	if got.LastEventSeq != 0 || !got.LastEventAt.IsZero() || got.EstimatorVersion != 0 {
		t.Errorf("miss Cursor = %+v, want zero-valued fields", got)
	}
}

func TestSQLiteStore_SetCursor_EmptySessionErrors(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	if err := s.SetCursor(ctx, Cursor{}); err == nil {
		t.Errorf("SetCursor(empty SessionID) = nil, want error")
	}
}

func TestSQLiteStore_SetCursor_RoundTrip(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	when := hourBucket(t, "2026-04-01T00:00:00Z")

	want := Cursor{
		SessionID:        "sess-x",
		LastEventSeq:     42,
		LastEventAt:      when,
		EstimatorVersion: 3,
		UpdatedAt:        when.Add(30 * time.Minute),
	}
	if err := s.SetCursor(ctx, want); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}

	got, err := s.GetCursor(ctx, "sess-x")
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if got.SessionID != want.SessionID ||
		got.LastEventSeq != want.LastEventSeq ||
		!got.LastEventAt.Equal(want.LastEventAt) ||
		got.EstimatorVersion != want.EstimatorVersion ||
		!got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("round-trip mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestSQLiteStore_SetCursor_Monotonic(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	tNew := hourBucket(t, "2026-05-01T10:00:00Z")
	tOld := hourBucket(t, "2026-05-01T05:00:00Z")

	if err := s.SetCursor(ctx, Cursor{
		SessionID: "s", LastEventSeq: 100, LastEventAt: tNew,
		EstimatorVersion: EstimatorVersion, UpdatedAt: tNew,
	}); err != nil {
		t.Fatal(err)
	}
	// Attempt to roll back — must be ignored for last_seq/last_ts,
	// while estimator_version and updated_at should still be overwritten.
	if err := s.SetCursor(ctx, Cursor{
		SessionID: "s", LastEventSeq: 5, LastEventAt: tOld,
		EstimatorVersion: 99, UpdatedAt: tOld,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetCursor(ctx, "s")
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if got.LastEventSeq != 100 {
		t.Errorf("LastEventSeq = %d, want 100 (monotonic)", got.LastEventSeq)
	}
	if !got.LastEventAt.Equal(tNew) {
		t.Errorf("LastEventAt = %v, want %v (monotonic)", got.LastEventAt, tNew)
	}
	if got.EstimatorVersion != 99 {
		t.Errorf("EstimatorVersion = %d, want 99 (overwritten)", got.EstimatorVersion)
	}
	if !got.UpdatedAt.Equal(tOld) {
		t.Errorf("UpdatedAt = %v, want %v (overwritten)", got.UpdatedAt, tOld)
	}
}

// -----------------------------------------------------------------------------
// Query
// -----------------------------------------------------------------------------

func TestSQLiteStore_Query_HourlyGroupBySumsAcrossSessions(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-06-01T00:00:00Z")

	if err := s.UpsertDeltas(ctx, []Delta{
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s1", Workspace: "w", Value: 3},
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s2", Workspace: "w", Value: 4},
	}); err != nil {
		t.Fatal(err)
	}

	pts, err := s.Query(ctx, Query{
		RangeFrom: ts.Add(-time.Hour), RangeTo: ts.Add(time.Hour), Bucket: BucketHour,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 7 || pts[0].Metric != MetricPrompts {
		t.Errorf("Query = %+v, want single Point{Metric=prompts, Value=7}", pts)
	}
	if !pts[0].TS.Equal(ts) {
		t.Errorf("Point.TS = %v, want %v", pts[0].TS, ts)
	}
}

func TestSQLiteStore_Query_WorkspaceFilter(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-06-02T00:00:00Z")

	if err := s.UpsertDeltas(ctx, []Delta{
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w-a", Value: 1},
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w-b", Value: 10},
	}); err != nil {
		t.Fatal(err)
	}

	pts, err := s.Query(ctx, Query{
		RangeFrom: ts.Add(-time.Hour), RangeTo: ts.Add(time.Hour),
		Bucket: BucketHour, Workspace: "w-b",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 10 {
		t.Errorf("filtered Query = %+v, want single Point{Value=10}", pts)
	}
}

func TestSQLiteStore_Query_MetricFilter(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-06-03T00:00:00Z")

	if err := s.UpsertDeltas(ctx, []Delta{
		{TSBucket: ts, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 5},
		{TSBucket: ts, Metric: MetricErrors, SessionID: "s", Workspace: "w", Value: 2},
	}); err != nil {
		t.Fatal(err)
	}

	pts, err := s.Query(ctx, Query{
		RangeFrom: ts.Add(-time.Hour), RangeTo: ts.Add(time.Hour),
		Bucket: BucketHour, Metrics: []string{MetricPrompts},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(pts) != 1 || pts[0].Metric != MetricPrompts || pts[0].Value != 5 {
		t.Errorf("metric-filtered Query = %+v, want Point{Metric=prompts, Value=5}", pts)
	}
}

func TestSQLiteStore_Query_EmptyRangeReturnsEmpty(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	base := hourBucket(t, "2026-06-04T00:00:00Z")

	pts, err := s.Query(ctx, Query{
		RangeFrom: base, RangeTo: base.Add(24 * time.Hour), Bucket: BucketHour,
	})
	if err != nil {
		t.Fatalf("Query empty: %v", err)
	}
	if len(pts) != 0 {
		t.Errorf("empty range returned %d points, want 0", len(pts))
	}
}

func TestSQLiteStore_Query_RangeHalfOpen(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	tsIn := hourBucket(t, "2026-06-05T00:00:00Z")
	tsBoundary := hourBucket(t, "2026-06-05T01:00:00Z")

	if err := s.UpsertDeltas(ctx, []Delta{
		{TSBucket: tsIn, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 1},
		{TSBucket: tsBoundary, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 99},
	}); err != nil {
		t.Fatal(err)
	}

	// [tsIn, tsBoundary) — the boundary row must be excluded.
	pts, err := s.Query(ctx, Query{
		RangeFrom: tsIn, RangeTo: tsBoundary, Bucket: BucketHour,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("half-open range = %+v, want Point{Value=1} only (boundary excluded)", pts)
	}
}

// -----------------------------------------------------------------------------
// Prune
// -----------------------------------------------------------------------------

func TestSQLiteStore_Prune_DeletesStrictlyOlder(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	tOld := hourBucket(t, "2026-01-01T00:00:00Z")
	tCut := hourBucket(t, "2026-06-01T00:00:00Z")
	tNew := hourBucket(t, "2026-12-01T00:00:00Z")

	if err := s.UpsertDeltas(ctx, []Delta{
		{TSBucket: tOld, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 1},
		{TSBucket: tCut, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 2},
		{TSBucket: tNew, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 3},
	}); err != nil {
		t.Fatal(err)
	}

	removed, err := s.Prune(ctx, tCut)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("Prune removed %d rows, want 1 (strictly older than cutoff)", removed)
	}

	// Boundary row and newer row survive.
	if got := countAt(t, s, tCut, MetricPrompts, "s", "w"); got != 2 {
		t.Errorf("cutoff row = %d, want 2 (must survive strict-less-than prune)", got)
	}
	if got := countAt(t, s, tNew, MetricPrompts, "s", "w"); got != 3 {
		t.Errorf("newer row = %d, want 3 (must survive)", got)
	}
}

// -----------------------------------------------------------------------------
// Close: idempotency + ErrClosed everywhere
// -----------------------------------------------------------------------------

func TestSQLiteStore_Close_Idempotent(t *testing.T) {
	s, _ := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v, want nil (idempotent)", err)
	}
}

func TestSQLiteStore_AllMethodsReturnErrClosed(t *testing.T) {
	s, _ := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx := context.Background()

	if err := s.UpsertDeltas(ctx, []Delta{{
		TSBucket: hourBucket(t, "2026-07-01T00:00:00Z"),
		Metric:   MetricPrompts, SessionID: "s", Workspace: "w", Value: 1,
	}}); !errors.Is(err, ErrClosed) {
		t.Errorf("UpsertDeltas after Close = %v, want ErrClosed", err)
	}
	if _, err := s.GetCursor(ctx, "s"); !errors.Is(err, ErrClosed) {
		t.Errorf("GetCursor after Close = %v, want ErrClosed", err)
	}
	if err := s.SetCursor(ctx, Cursor{SessionID: "s"}); !errors.Is(err, ErrClosed) {
		t.Errorf("SetCursor after Close = %v, want ErrClosed", err)
	}
	if _, err := s.Query(ctx, Query{}); !errors.Is(err, ErrClosed) {
		t.Errorf("Query after Close = %v, want ErrClosed", err)
	}
	if _, err := s.Prune(ctx, time.Now()); !errors.Is(err, ErrClosed) {
		t.Errorf("Prune after Close = %v, want ErrClosed", err)
	}
}

// -----------------------------------------------------------------------------
// Concurrency: many goroutines upserting overlapping keys must not lose writes
// or violate the double-insert = no double-count invariant.
// -----------------------------------------------------------------------------

func TestSQLiteStore_UpsertDeltas_ConcurrentWriters(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	ts := hourBucket(t, "2026-08-01T00:00:00Z")

	const writers = 8
	const iters = 50
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				// Every writer touches DISTINCT keys (workspace varies) so we can
				// assert a deterministic final row count with no lost writes,
				// while still exercising the write path in parallel.
				err := s.UpsertDeltas(ctx, []Delta{{
					TSBucket:  ts,
					Metric:    MetricPrompts,
					SessionID: "s-" + strconv.Itoa(w),
					Workspace: "w-" + strconv.Itoa(w),
					Value:     int64(i + 1),
				}})
				if err != nil {
					t.Errorf("writer %d iter %d: %v", w, i, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stats_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != writers {
		t.Errorf("row count after concurrent upserts = %d, want %d", n, writers)
	}

	// Last-write-wins per key: value should be `iters` (the last iteration).
	for w := 0; w < writers; w++ {
		got := countAt(t, s, ts, MetricPrompts, "s-"+strconv.Itoa(w), "w-"+strconv.Itoa(w))
		if got != int64(iters) {
			t.Errorf("writer %d final value = %d, want %d", w, got, iters)
		}
	}
}
