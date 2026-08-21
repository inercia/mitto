package stats

// End-to-end retention tests against a real SQLiteStore. Covers the bead's
// two explicit acceptance criteria (mitto-a86b.9):
//
//   1. Seed rows with mixed timestamps → prune → only strictly-older rows
//      removed (boundary survives).
//   2. Retention=24h against yesterday-seeded rows → yesterday and older
//      are gone, today survives (the "stats.retention_hours = 24" wiring
//      contract).
//
// nextFire / jitter behaviour is also exercised here since it is worker-
// specific and needs no SQLite fixture.

import (
	"context"
	"math/rand"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// SQLite-backed prune boundary (bead acceptance #1)
// -----------------------------------------------------------------------------

func TestRetentionWorker_SQLite_PruneBoundary(t *testing.T) {
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
		t.Fatalf("UpsertDeltas: %v", err)
	}

	// Retention window chosen so fireAt - Retention == tCut exactly.
	fireAt := tCut.Add(24 * time.Hour)
	w := NewRetentionWorker(s, RetentionWorkerOptions{Retention: 24 * time.Hour})
	if err := w.RunOnce(ctx, fireAt); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Boundary row (== cutoff) survives strict-less-than prune; older gone.
	if got := countAt(t, s, tOld, MetricPrompts, "s", "w"); got != 0 {
		t.Errorf("older row survived prune: value = %d, want 0", got)
	}
	if got := countAt(t, s, tCut, MetricPrompts, "s", "w"); got != 2 {
		t.Errorf("boundary row (==cutoff) = %d, want 2 (must survive)", got)
	}
	if got := countAt(t, s, tNew, MetricPrompts, "s", "w"); got != 3 {
		t.Errorf("newer row = %d, want 3 (must survive)", got)
	}
}

// -----------------------------------------------------------------------------
// SQLite-backed 24h retention (bead acceptance #2)
// -----------------------------------------------------------------------------

func TestRetentionWorker_SQLite_RetentionHours24_PrunesEverythingOlderThanYesterday(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()

	now := hourBucket(t, "2026-07-15T12:00:00Z")
	twoDaysAgo := now.Add(-48 * time.Hour) // strictly older than 24h → pruned
	oneDayAgo := now.Add(-24 * time.Hour)  // == cutoff → survives (strict <)
	today := now.Add(-1 * time.Hour)       // younger than cutoff → survives

	if err := s.UpsertDeltas(ctx, []Delta{
		{TSBucket: twoDaysAgo, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 10},
		{TSBucket: oneDayAgo, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 20},
		{TSBucket: today, Metric: MetricPrompts, SessionID: "s", Workspace: "w", Value: 30},
	}); err != nil {
		t.Fatalf("UpsertDeltas: %v", err)
	}

	// Simulates the config wiring: settings.stats.retention_hours = 24 →
	// StatsConfig.GetRetention() == 24h → passed to NewRetentionWorker.
	w := NewRetentionWorker(s, RetentionWorkerOptions{Retention: 24 * time.Hour})
	if err := w.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := countAt(t, s, twoDaysAgo, MetricPrompts, "s", "w"); got != 0 {
		t.Errorf("row from 48h ago survived: value = %d, want 0", got)
	}
	if got := countAt(t, s, oneDayAgo, MetricPrompts, "s", "w"); got != 20 {
		t.Errorf("boundary row (==cutoff) = %d, want 20 (must survive)", got)
	}
	if got := countAt(t, s, today, MetricPrompts, "s", "w"); got != 30 {
		t.Errorf("today's row = %d, want 30 (must survive)", got)
	}
}

// -----------------------------------------------------------------------------
// nextFire / jitter
// -----------------------------------------------------------------------------

func TestRetentionWorker_NextFire_Defaults0315(t *testing.T) {
	fs := &retentionFake{}
	// Deterministic RNG source so jitter is reproducible.
	w := NewRetentionWorker(fs, RetentionWorkerOptions{
		Rand:      rand.New(rand.NewSource(1)),
		JitterMax: DefaultRetentionJitter,
	})
	// A time strictly before today's 03:15 slot → nextFire is today 03:15±jitter.
	from := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.Local)
	got := w.nextFire(from)
	slot := time.Date(2026, time.July, 13, 3, 15, 0, 0, time.Local)
	if got.Before(slot.Add(-DefaultRetentionJitter)) || got.After(slot.Add(DefaultRetentionJitter)) {
		t.Errorf("nextFire = %v, want within %v of %v", got, DefaultRetentionJitter, slot)
	}
	if !got.After(from) {
		t.Errorf("nextFire = %v, must be strictly after from = %v", got, from)
	}
}

func TestRetentionWorker_NextFire_RollsToTomorrowWhenSlotPassed(t *testing.T) {
	fs := &retentionFake{}
	w := NewRetentionWorker(fs, RetentionWorkerOptions{
		Rand: rand.New(rand.NewSource(1)),
	})
	// A time strictly after today's 03:15 slot → nextFire is tomorrow 03:15.
	from := time.Date(2026, time.July, 13, 10, 0, 0, 0, time.Local)
	got := w.nextFire(from)
	tomorrowSlot := time.Date(2026, time.July, 14, 3, 15, 0, 0, time.Local)
	if got.Before(tomorrowSlot.Add(-DefaultRetentionJitter)) || got.After(tomorrowSlot.Add(DefaultRetentionJitter)) {
		t.Errorf("nextFire = %v, want within %v of %v", got, DefaultRetentionJitter, tomorrowSlot)
	}
}

func TestRetentionWorker_Jitter_ZeroJitterMaxIsExact(t *testing.T) {
	fs := &retentionFake{}
	w := NewRetentionWorker(fs, RetentionWorkerOptions{JitterMax: -1})
	// JitterMax<=0 disables jitter → nextFire is exactly the 03:15 slot.
	from := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.Local)
	got := w.nextFire(from)
	want := time.Date(2026, time.July, 13, 3, 15, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("nextFire with zero jitter = %v, want %v", got, want)
	}
}
