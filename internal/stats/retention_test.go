package stats

// RetentionWorker tests (mitto-a86b.9). Fully deterministic: no real ticker
// runs, no wall-clock reads. RunOnce is invoked directly with an explicit
// fireAt, and a tiny retentionFake records every Prune/Vacuum call.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// retentionFake — Store test double that records every Prune/Vacuum call and can be
// programmed to return canned errors.
// -----------------------------------------------------------------------------

type pruneCall struct {
	olderThan time.Time
}

type retentionFake struct {
	NoopStore
	pruneCalls  []pruneCall
	vacuumCalls int
	pruneErr    error
	vacuumErr   error
	pruneRows   int64
}

func (f *retentionFake) Prune(_ context.Context, olderThan time.Time) (int64, error) {
	f.pruneCalls = append(f.pruneCalls, pruneCall{olderThan: olderThan.UTC()})
	return f.pruneRows, f.pruneErr
}

func (f *retentionFake) Vacuum(_ context.Context) error {
	f.vacuumCalls++
	return f.vacuumErr
}

// -----------------------------------------------------------------------------
// RunOnce — pruning gate on Retention
// -----------------------------------------------------------------------------

// A Sunday at 03:15 local so VACUUM is exercised in the base case.
var sundayFireAt = time.Date(2026, time.July, 12, 3, 15, 0, 0, time.Local)

func TestRetentionWorker_RunOnce_RetentionZero_SkipsPrune(t *testing.T) {
	fs := &retentionFake{}
	w := NewRetentionWorker(fs, RetentionWorkerOptions{Retention: 0})
	if err := w.RunOnce(context.Background(), sundayFireAt); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := len(fs.pruneCalls); got != 0 {
		t.Errorf("Prune calls = %d, want 0 (Retention=0 disables pruning)", got)
	}
	if got := fs.vacuumCalls; got != 1 {
		t.Errorf("Vacuum calls = %d, want 1 (Sunday must still VACUUM)", got)
	}
}

func TestRetentionWorker_RunOnce_ComputesCutoffFromFireAt(t *testing.T) {
	fs := &retentionFake{}
	// 24h retention, non-Sunday, no jitter.
	fireAt := time.Date(2026, time.July, 13, 3, 15, 0, 0, time.Local) // Monday
	w := NewRetentionWorker(fs, RetentionWorkerOptions{Retention: 24 * time.Hour})
	if err := w.RunOnce(context.Background(), fireAt); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := len(fs.pruneCalls); got != 1 {
		t.Fatalf("Prune calls = %d, want 1", got)
	}
	wantCutoff := fireAt.Add(-24 * time.Hour).UTC()
	if !fs.pruneCalls[0].olderThan.Equal(wantCutoff) {
		t.Errorf("Prune cutoff = %v, want %v (fireAt - Retention, UTC)",
			fs.pruneCalls[0].olderThan, wantCutoff)
	}
	if fs.vacuumCalls != 0 {
		t.Errorf("Vacuum calls = %d, want 0 (Monday must not VACUUM)", fs.vacuumCalls)
	}
}

// -----------------------------------------------------------------------------
// RunOnce — VACUUM only on Sunday
// -----------------------------------------------------------------------------

func TestRetentionWorker_RunOnce_VacuumOnlyOnSunday(t *testing.T) {
	// July 12 2026 is a Sunday; iterate one full week to prove exactly one
	// day triggers a VACUUM.
	base := time.Date(2026, time.July, 12, 3, 15, 0, 0, time.Local)
	for i := 0; i < 7; i++ {
		fireAt := base.AddDate(0, 0, i)
		fs := &retentionFake{}
		w := NewRetentionWorker(fs, RetentionWorkerOptions{Retention: 24 * time.Hour})
		if err := w.RunOnce(context.Background(), fireAt); err != nil {
			t.Fatalf("RunOnce(%s): %v", fireAt.Weekday(), err)
		}
		want := 0
		if fireAt.Weekday() == time.Sunday {
			want = 1
		}
		if fs.vacuumCalls != want {
			t.Errorf("weekday=%s: Vacuum calls = %d, want %d",
				fireAt.Weekday(), fs.vacuumCalls, want)
		}
	}
}

// -----------------------------------------------------------------------------
// RunOnce — errors are surfaced but do not cross-abort
// -----------------------------------------------------------------------------

func TestRetentionWorker_RunOnce_PruneErrorDoesNotAbortVacuum(t *testing.T) {
	pruneBoom := errors.New("prune exploded")
	fs := &retentionFake{pruneErr: pruneBoom}
	w := NewRetentionWorker(fs, RetentionWorkerOptions{Retention: 24 * time.Hour})
	err := w.RunOnce(context.Background(), sundayFireAt)
	if !errors.Is(err, pruneBoom) {
		t.Errorf("RunOnce error = %v, want to wrap pruneBoom", err)
	}
	if fs.vacuumCalls != 1 {
		t.Errorf("Vacuum calls = %d, want 1 (Sunday VACUUM must still run after Prune error)",
			fs.vacuumCalls)
	}
}

func TestRetentionWorker_RunOnce_ReturnsJoinedErrorOnBothFailing(t *testing.T) {
	pruneBoom := errors.New("prune exploded")
	vacuumBoom := errors.New("vacuum exploded")
	fs := &retentionFake{pruneErr: pruneBoom, vacuumErr: vacuumBoom}
	w := NewRetentionWorker(fs, RetentionWorkerOptions{Retention: 24 * time.Hour})
	err := w.RunOnce(context.Background(), sundayFireAt)
	if err == nil {
		t.Fatal("RunOnce returned nil, want a joined error")
	}
	if !errors.Is(err, pruneBoom) {
		t.Errorf("joined error should wrap pruneBoom; got %v", err)
	}
	if !errors.Is(err, vacuumBoom) {
		t.Errorf("joined error should wrap vacuumBoom; got %v", err)
	}
}
