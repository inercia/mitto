package stats

import (
	"context"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// splitByHour
// -----------------------------------------------------------------------------

func TestSplitByHour_WithinSingleBucket(t *testing.T) {
	from := hourBucket(t, "2026-05-01T10:00:00Z")
	now := hourBucket(t, "2026-05-01T10:00:45Z")
	deltas := splitByHour(from, now)
	if len(deltas) != 1 {
		t.Fatalf("len(deltas) = %d, want 1", len(deltas))
	}
	if !deltas[0].TSBucket.Equal(hourBucket(t, "2026-05-01T10:00:00Z")) {
		t.Errorf("bucket = %v, want 10:00", deltas[0].TSBucket)
	}
	if deltas[0].Value != 45 {
		t.Errorf("value = %d, want 45", deltas[0].Value)
	}
	if deltas[0].Metric != MetricUptimeSeconds || deltas[0].SessionID != UptimeSentinelSessionID {
		t.Errorf("metric/session = %s/%s, want %s/%s", deltas[0].Metric, deltas[0].SessionID, MetricUptimeSeconds, UptimeSentinelSessionID)
	}
}

// TestSplitByHour_CrossesHourBoundary asserts a tick straddling :00 is split
// proportionally between the two buckets it overlaps, per the plan's
// non-negotiable "no misattribution across the hour boundary" requirement.
func TestSplitByHour_CrossesHourBoundary(t *testing.T) {
	from := hourBucket(t, "2026-05-01T10:59:30Z")
	now := hourBucket(t, "2026-05-01T11:00:30Z")
	deltas := splitByHour(from, now)
	if len(deltas) != 2 {
		t.Fatalf("len(deltas) = %d, want 2", len(deltas))
	}
	if !deltas[0].TSBucket.Equal(hourBucket(t, "2026-05-01T10:00:00Z")) || deltas[0].Value != 30 {
		t.Errorf("first segment = %+v, want bucket 10:00 value 30", deltas[0])
	}
	if !deltas[1].TSBucket.Equal(hourBucket(t, "2026-05-01T11:00:00Z")) || deltas[1].Value != 30 {
		t.Errorf("second segment = %+v, want bucket 11:00 value 30", deltas[1])
	}
}

func TestSplitByHour_ZeroOrNegativeSpanReturnsNil(t *testing.T) {
	now := hourBucket(t, "2026-05-01T10:00:00Z")
	if got := splitByHour(now, now); got != nil {
		t.Errorf("equal from/now: got %v, want nil", got)
	}
	if got := splitByHour(now, now.Add(-time.Second)); got != nil {
		t.Errorf("now before from: got %v, want nil", got)
	}
}

// -----------------------------------------------------------------------------
// UptimeRecorder.tick (exported test seam, real SQLiteStore)
// -----------------------------------------------------------------------------

func TestUptimeRecorder_TickPersistsElapsedSeconds(t *testing.T) {
	s, _ := openTestStore(t)
	cur := hourBucket(t, "2026-05-02T09:00:00Z")
	r := NewUptimeRecorder(s, UptimeRecorderOptions{Now: func() time.Time { return cur }})
	r.Start(context.Background())

	cur = cur.Add(90 * time.Second)
	r.tick(context.Background())

	if got := countAt(t, s, hourBucket(t, "2026-05-02T09:00:00Z"), MetricUptimeSeconds, UptimeSentinelSessionID, ""); got != 90 {
		t.Errorf("uptime_seconds = %d, want 90", got)
	}
}

// TestUptimeRecorder_TicksAccumulateAdditively asserts that three sequential
// 60s ticks into the SAME hour bucket sum to 180s in the store, not 60s.
// Store.UpsertDeltas is last-write-wins REPLACE per key (see its doc
// comment), so this only holds because the recorder itself tracks a running
// cumulative total per bucket and re-writes the full total on every tick
// (see tick's doc comment) — writing just each tick's own increment would
// make every write clobber the previous one.
func TestUptimeRecorder_TicksAccumulateAdditively(t *testing.T) {
	s, _ := openTestStore(t)
	cur := hourBucket(t, "2026-05-03T09:00:00Z")
	r := NewUptimeRecorder(s, UptimeRecorderOptions{Now: func() time.Time { return cur }})
	r.Start(context.Background())

	for i := 0; i < 3; i++ {
		cur = cur.Add(60 * time.Second)
		r.tick(context.Background())
	}

	if got := countAt(t, s, hourBucket(t, "2026-05-03T09:00:00Z"), MetricUptimeSeconds, UptimeSentinelSessionID, ""); got != 180 {
		t.Errorf("uptime_seconds after 3 ticks = %d, want 180 (additive UpsertDeltas)", got)
	}
}

func TestUptimeRecorder_TickAcrossHourBoundarySplitsBuckets(t *testing.T) {
	s, _ := openTestStore(t)
	cur := hourBucket(t, "2026-05-04T10:59:00Z")
	r := NewUptimeRecorder(s, UptimeRecorderOptions{Now: func() time.Time { return cur }})
	r.Start(context.Background())

	cur = cur.Add(120 * time.Second) // -> 11:01:00: 60s in the 10:00 bucket, 60s in the 11:00 bucket.
	r.tick(context.Background())

	if got := countAt(t, s, hourBucket(t, "2026-05-04T10:00:00Z"), MetricUptimeSeconds, UptimeSentinelSessionID, ""); got != 60 {
		t.Errorf("uptime_seconds @10:00 = %d, want 60", got)
	}
	if got := countAt(t, s, hourBucket(t, "2026-05-04T11:00:00Z"), MetricUptimeSeconds, UptimeSentinelSessionID, ""); got != 60 {
		t.Errorf("uptime_seconds @11:00 = %d, want 60", got)
	}
}

func TestUptimeRecorder_CloseIsIdempotent(t *testing.T) {
	s, _ := openTestStore(t)
	r := NewUptimeRecorder(s, UptimeRecorderOptions{Interval: time.Hour})
	r.Start(context.Background())
	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
