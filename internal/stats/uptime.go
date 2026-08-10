package stats

// UptimeRecorder (mitto-c45m): periodic heartbeat that records how many
// seconds the Mitto server process was alive, so the beads active-cycle
// fold (beads_source.go) can subtract process-down time from the raw
// claim->close interval.
//
// Each tick computes the elapsed wall-clock span since the previous tick (or
// since Start, for the first tick), split at any hour boundary(ies) it
// crosses so each bucket only receives the portion of the span that
// actually falls inside it (otherwise a tick landing just after :00 would
// misattribute up to a full interval's worth of seconds to the previous
// hour). The recorder keeps an in-memory running total per still-open hour
// bucket and writes that CUMULATIVE total — not just the tick's own
// increment — via Store.UpsertDeltas. This is deliberate: SQLiteStore's
// UpsertDeltas is last-write-wins REPLACE on (ts_bucket, metric, session_id,
// workspace, model), not additive across separate calls for the same key
// (see its doc comment), so writing only the incremental slice on every
// tick would make each write clobber the previous one instead of
// accumulating — the bucket would settle on the LAST tick's ~60s rather
// than the hour's true total. Writing the running cumulative total is
// correct under REPLACE semantics and is naturally idempotent under retry.
//
// Uptime is a process-global fact, not per-workspace: every delta is
// written with Workspace="", Model="", SessionID=UptimeSentinelSessionID.

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultUptimeInterval is the default period between heartbeat ticks.
const DefaultUptimeInterval = 60 * time.Second

// UptimeRecorderOptions configures NewUptimeRecorder. Zero-valued fields
// fall back to the defaults documented on each field.
type UptimeRecorderOptions struct {
	// Interval is the period between heartbeat ticks. Default: 60s.
	Interval time.Duration

	// Now returns the current wall-clock time; overridable for tests.
	Now func() time.Time

	// Logger is optional; when non-nil the recorder emits WARN on any
	// UpsertDeltas failure.
	Logger *slog.Logger
}

func (o UptimeRecorderOptions) withDefaults() UptimeRecorderOptions {
	if o.Interval <= 0 {
		o.Interval = DefaultUptimeInterval
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// UptimeRecorder is a Store-agnostic background heartbeat. Construct with
// NewUptimeRecorder, launch with Start, tear down with Close.
type UptimeRecorder struct {
	store Store
	opts  UptimeRecorderOptions

	lastTick time.Time
	// bucketTotals is the running cumulative uptime-seconds this process
	// instance has recorded for each still-relevant hour bucket (keyed by
	// bucket-start Unix seconds). Written in full on every tick that touches
	// the bucket, since Store.UpsertDeltas is last-write-wins REPLACE (see
	// the package doc comment) rather than additive. Reset only by process
	// restart (a fresh UptimeRecorder), which is correct: a restart means a
	// real gap in this process's own uptime, so it is appropriate for the
	// new instance to resume counting from what it can itself observe.
	bucketTotals map[int64]int64

	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

// NewUptimeRecorder constructs an UptimeRecorder. store must not be nil. The
// returned recorder has not started its loop yet; call Start to begin.
func NewUptimeRecorder(store Store, opts UptimeRecorderOptions) *UptimeRecorder {
	if store == nil {
		panic("stats: NewUptimeRecorder: store is nil")
	}
	return &UptimeRecorder{
		store:        store,
		opts:         opts.withDefaults(),
		bucketTotals: make(map[int64]int64),
		closed:       make(chan struct{}),
		done:         make(chan struct{}),
	}
}

// Start launches the periodic heartbeat loop. Safe to call at most once.
func (r *UptimeRecorder) Start(ctx context.Context) {
	r.lastTick = r.opts.Now().UTC()
	go r.loop(ctx)
}

// Close signals the loop to stop and blocks until it has returned.
// Idempotent.
func (r *UptimeRecorder) Close() error {
	r.closeOnce.Do(func() {
		close(r.closed)
		<-r.done
	})
	return nil
}

// loop fires tick every Interval until ctx.Done or Close.
func (r *UptimeRecorder) loop(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.closed:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// tick attributes the elapsed span since the last tick to the hour bucket(s)
// it overlaps (via splitByHour), folds each segment's incremental seconds
// into this process's running bucketTotals, then writes the resulting
// CUMULATIVE per-bucket totals (not the raw per-segment increments) so the
// store's last-write-wins REPLACE semantics correctly accumulate across
// ticks instead of clobbering the previous tick's value. Advances lastTick
// to now regardless of whether any segment was produced. Exported seam for
// tests via the package (lower-case, but colocated in _test.go with access
// to unexported fields).
func (r *UptimeRecorder) tick(ctx context.Context) {
	now := r.opts.Now().UTC()
	from := r.lastTick
	if from.IsZero() || now.Before(from) {
		from = now
	}
	segments := splitByHour(from, now)
	r.lastTick = now
	if len(segments) == 0 {
		return
	}
	deltas := make([]Delta, 0, len(segments))
	for _, seg := range segments {
		bucket := seg.TSBucket.Unix()
		r.bucketTotals[bucket] += seg.Value
		deltas = append(deltas, Delta{
			TSBucket:  seg.TSBucket,
			Metric:    MetricUptimeSeconds,
			SessionID: UptimeSentinelSessionID,
			Value:     r.bucketTotals[bucket],
		})
	}
	if err := r.store.UpsertDeltas(ctx, deltas); err != nil {
		r.logWarn("uptime recorder: upsert deltas failed", "error", err)
	}
}

// splitByHour returns one Delta per hour bucket overlapping [from, now),
// each valued at the number of seconds of that span falling in the bucket.
func splitByHour(from, now time.Time) []Delta {
	if !now.After(from) {
		return nil
	}
	var deltas []Delta
	cursor := from
	for cursor.Before(now) {
		bucketStart := cursor.Truncate(time.Hour)
		bucketEnd := bucketStart.Add(time.Hour)
		segEnd := now
		if bucketEnd.Before(segEnd) {
			segEnd = bucketEnd
		}
		secs := int64(segEnd.Sub(cursor).Seconds())
		if secs > 0 {
			deltas = append(deltas, Delta{
				TSBucket:  bucketStart,
				Metric:    MetricUptimeSeconds,
				SessionID: UptimeSentinelSessionID,
				Value:     secs,
			})
		}
		cursor = segEnd
	}
	return deltas
}

// logWarn is the WARN counterpart used by tick.
func (r *UptimeRecorder) logWarn(msg string, args ...any) {
	if r.opts.Logger != nil {
		r.opts.Logger.Warn(msg, args...)
	}
}
