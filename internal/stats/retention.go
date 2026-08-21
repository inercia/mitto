package stats

// RetentionWorker (mitto-a86b.9): nightly housekeeping loop for the stats DB.
//
// Two responsibilities, both best-effort:
//
//   1. Prune hourly rows strictly older than `now - Retention` — the retention
//      window defaults to 90 days (see config.DefaultStatsRetentionHours).
//   2. Once per week (Sunday), issue a VACUUM to reclaim pages freed by prior
//      prunes so the DB file does not grow unbounded.
//
// Both operations run inside a serialised goroutine and their errors are
// logged (Warn) but never propagated — the server must not crash on
// housekeeping failure. Retention == 0 disables pruning entirely; VACUUM is
// still attempted on Sundays because it is cheap and safe on an already-small
// DB.
//
// Scheduling: the ticker fires daily at TickAt local time (default 03:15) with
// ±JitterMax (default 10m) applied to the fire time, not the interval — so
// jitter cannot compound across nights.

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// Defaults for RetentionWorkerOptions.
const (
	// DefaultRetentionTickHour is the local-time hour at which the daily
	// retention pass fires.
	DefaultRetentionTickHour = 3
	// DefaultRetentionTickMinute is the local-time minute component.
	DefaultRetentionTickMinute = 15
	// DefaultRetentionJitter is the maximum ±jitter applied to the fire time.
	DefaultRetentionJitter = 10 * time.Minute
)

// RetentionWorkerOptions configures NewRetentionWorker. Zero-valued fields
// fall back to the defaults documented on each field.
type RetentionWorkerOptions struct {
	// Retention is the age threshold: hourly rows with bucket-start strictly
	// older than `now - Retention` are pruned. A zero (or negative) value
	// disables pruning entirely; Sunday VACUUM still runs.
	Retention time.Duration

	// TickHour / TickMinute pick the local-time daily fire point.
	// Defaults: 03:15.
	TickHour   int
	TickMinute int

	// JitterMax is the maximum ±jitter applied to each fire time. Default
	// DefaultRetentionJitter (10m). Zero disables jitter.
	JitterMax time.Duration

	// Now returns the current wall-clock time; overridable for tests.
	Now func() time.Time

	// Rand seeds the jitter; when nil a package-level source is used.
	Rand *rand.Rand

	// Logger is optional; when non-nil the worker emits INFO on each fire
	// (start/finish + rows-pruned) and WARN on any Prune/Vacuum error.
	Logger *slog.Logger
}

func (o RetentionWorkerOptions) withDefaults() RetentionWorkerOptions {
	if o.TickHour == 0 && o.TickMinute == 0 {
		o.TickHour = DefaultRetentionTickHour
		o.TickMinute = DefaultRetentionTickMinute
	}
	if o.JitterMax == 0 {
		o.JitterMax = DefaultRetentionJitter
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// RetentionWorker is a Store-agnostic background pruner + weekly vacuumer.
// Construct with NewRetentionWorker, launch with Start, tear down with Close.
// RunOnce is exported for tests and manual admin triggers.
type RetentionWorker struct {
	store Store
	opts  RetentionWorkerOptions

	runMu sync.Mutex

	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

// NewRetentionWorker constructs a RetentionWorker. store must not be nil. The
// returned worker has not started its loop yet; call Start to begin.
func NewRetentionWorker(store Store, opts RetentionWorkerOptions) *RetentionWorker {
	if store == nil {
		panic("stats: NewRetentionWorker: store is nil")
	}
	return &RetentionWorker{
		store:  store,
		opts:   opts.withDefaults(),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start launches the daily loop. Safe to call at most once.
func (w *RetentionWorker) Start(ctx context.Context) {
	go w.loop(ctx)
}

// Close signals the loop to stop and blocks until it has returned. Idempotent.
func (w *RetentionWorker) Close() error {
	w.closeOnce.Do(func() {
		close(w.closed)
		<-w.done
	})
	return nil
}

// loop sleeps until the next fire point, runs one pass, and repeats. Exits on
// ctx.Done or Close.
func (w *RetentionWorker) loop(ctx context.Context) {
	defer close(w.done)
	for {
		fireAt := w.nextFire(w.opts.Now())
		wait := time.Until(fireAt)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-w.closed:
			timer.Stop()
			return
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		// Use the *scheduled* fire time (in local time) for the weekday
		// decision so a run that slips past midnight cannot double-vacuum.
		if err := w.RunOnce(ctx, fireAt); err != nil {
			w.logWarn("retention pass failed", "error", err)
		}
	}
}

// RunOnce performs a single retention pass using fireAt as the reference
// wall-clock. The retention cutoff is `fireAt.UTC() - Retention`; the VACUUM
// decision uses fireAt.Local().Weekday(). Errors from Prune and Vacuum are
// returned wrapped in a joined error but never bubble out of the goroutine.
// Serialised on runMu so manual and periodic invocations never overlap.
func (w *RetentionWorker) RunOnce(ctx context.Context, fireAt time.Time) error {
	w.runMu.Lock()
	defer w.runMu.Unlock()

	var (
		pruneErr  error
		vacuumErr error
		pruned    int64
	)

	if w.opts.Retention > 0 {
		cutoff := fireAt.Add(-w.opts.Retention).UTC()
		n, err := w.store.Prune(ctx, cutoff)
		pruned = n
		pruneErr = err
		if err != nil {
			w.logWarn("stats: prune failed", "error", err, "cutoff", cutoff)
		} else if w.opts.Logger != nil {
			w.opts.Logger.Info("stats: prune completed",
				"rows", n, "cutoff", cutoff, "retention", w.opts.Retention)
		}
	}

	if fireAt.Local().Weekday() == time.Sunday {
		if err := w.store.Vacuum(ctx); err != nil {
			vacuumErr = err
			w.logWarn("stats: vacuum failed", "error", err)
		} else if w.opts.Logger != nil {
			w.opts.Logger.Info("stats: vacuum completed", "pruned_this_pass", pruned)
		}
	}

	switch {
	case pruneErr != nil && vacuumErr != nil:
		return errorsJoin(pruneErr, vacuumErr)
	case pruneErr != nil:
		return pruneErr
	case vacuumErr != nil:
		return vacuumErr
	}
	return nil
}

// nextFire computes the next daily fire time relative to `from`. Uses the
// worker's TickHour/TickMinute in the local zone, then applies ±JitterMax.
// If today's slot has already passed (or the jittered result is still <= from),
// it rolls to tomorrow.
func (w *RetentionWorker) nextFire(from time.Time) time.Time {
	local := from.Local()
	slot := time.Date(local.Year(), local.Month(), local.Day(),
		w.opts.TickHour, w.opts.TickMinute, 0, 0, local.Location())
	// If today's un-jittered slot has already passed, advance to tomorrow.
	if !slot.After(local) {
		slot = slot.Add(24 * time.Hour)
	}
	jittered := slot.Add(w.jitter())
	// A negative jitter on the earliest slot could still fall behind `from`;
	// bump forward another day if so.
	if !jittered.After(local) {
		jittered = jittered.Add(24 * time.Hour)
	}
	return jittered
}

// jitter returns a value in [-JitterMax, +JitterMax]. Uses the injected Rand
// when provided so tests can be fully deterministic.
func (w *RetentionWorker) jitter() time.Duration {
	if w.opts.JitterMax <= 0 {
		return 0
	}
	span := int64(2*w.opts.JitterMax) + 1
	var n int64
	if w.opts.Rand != nil {
		n = w.opts.Rand.Int63n(span)
	} else {
		n = rand.Int63n(span)
	}
	return time.Duration(n) - w.opts.JitterMax
}

func (w *RetentionWorker) logWarn(msg string, args ...any) {
	if w.opts.Logger == nil {
		return
	}
	w.opts.Logger.Warn(msg, args...)
}

// errorsJoin returns a single error that carries both `a` and `b`. Extracted
// so we can avoid depending on errors.Join's exact format in tests.
func errorsJoin(a, b error) error { return joinedErr{a: a, b: b} }

type joinedErr struct{ a, b error }

func (e joinedErr) Error() string { return e.a.Error() + "; " + e.b.Error() }
func (e joinedErr) Unwrap() []error {
	return []error{e.a, e.b}
}
