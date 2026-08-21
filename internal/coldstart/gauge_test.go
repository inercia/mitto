package coldstart

import (
	"context"
	"strings"
	"testing"
	"time"
)

// resetGaugeRing clears the gauge ring buffer state so tests don't observe
// samples appended by other tests (or a prior StartGauge in this test run).
func resetGaugeRing(t *testing.T) {
	t.Helper()
	gaugeRingMu.Lock()
	gaugeRingBuf = [ringCapacity]GaugeSample{}
	gaugeRingLen = 0
	gaugeRingNext = 0
	gaugeRingMu.Unlock()
}

func TestGaugeRingAppendCapAndOrder(t *testing.T) {
	resetGaugeRing(t)

	for i := 0; i < ringCapacity+10; i++ {
		gaugeRingAppend(GaugeSample{
			ContentionSnapshot: ContentionSnapshot{NumGoroutine: i},
			At:                 time.Unix(int64(i), 0),
		})
	}

	all := RecentGaugeSamples(0)
	if len(all) != ringCapacity {
		t.Fatalf("expected len %d, got %d", ringCapacity, len(all))
	}
	// Newest-first: At timestamps should be non-increasing.
	for i := 1; i < len(all); i++ {
		if all[i].At.After(all[i-1].At) {
			t.Errorf("samples not newest-first at %d: %v after %v", i, all[i].At, all[i-1].At)
		}
	}
	// The very last appended sample (NumGoroutine == ringCapacity+9) must be first.
	if all[0].NumGoroutine != ringCapacity+9 {
		t.Errorf("expected newest sample NumGoroutine=%d, got %d", ringCapacity+9, all[0].NumGoroutine)
	}

	small := RecentGaugeSamples(3)
	if len(small) != 3 {
		t.Errorf("expected 3, got %d", len(small))
	}
}

func TestRecentGaugeSamplesEmpty(t *testing.T) {
	resetGaugeRing(t)
	if got := RecentGaugeSamples(0); len(got) != 0 {
		t.Errorf("expected 0 samples on an empty ring, got %d", len(got))
	}
	if got := RecentGaugeSamples(5); len(got) != 0 {
		t.Errorf("expected 0 samples on an empty ring even with k=5, got %d", len(got))
	}
}

func TestStartGaugeSamplesPeriodically(t *testing.T) {
	resetGaugeRing(t)
	logger, buf := newTestLogger()

	stop := StartGauge(context.Background(), logger, 10*time.Millisecond)
	t.Cleanup(stop)

	// Wait for at least 3 ticks.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(RecentGaugeSamples(0)) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	samples := RecentGaugeSamples(0)
	if len(samples) < 3 {
		t.Fatalf("expected at least 3 gauge samples after waiting, got %d", len(samples))
	}
	for _, s := range samples {
		if s.NumGoroutine <= 0 {
			t.Errorf("sample NumGoroutine should be > 0, got %d", s.NumGoroutine)
		}
		if s.At.IsZero() {
			t.Errorf("sample At should not be zero")
		}
	}
	if !strings.Contains(buf.String(), "goroutine_gauge_sample") {
		t.Errorf("expected goroutine_gauge_sample log line, got %q", buf.String())
	}

	// Stop the ticker and confirm no further samples are appended.
	stop()
	before := len(RecentGaugeSamples(0))
	time.Sleep(50 * time.Millisecond)
	after := len(RecentGaugeSamples(0))
	if after != before {
		t.Errorf("expected no new samples after stop(), before=%d after=%d", before, after)
	}
}

func TestStartGaugeStopIsIdempotentAndCtxCancelStops(t *testing.T) {
	resetGaugeRing(t)

	// stop() called multiple times must not panic.
	stop := StartGauge(context.Background(), nil, 10*time.Millisecond)
	stop()
	stop()

	// ctx cancellation must also stop the ticker (nil logger: must not panic).
	ctx, cancel := context.WithCancel(context.Background())
	_ = StartGauge(ctx, nil, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond) // let the goroutine observe ctx.Done()

	before := len(RecentGaugeSamples(0))
	time.Sleep(50 * time.Millisecond)
	after := len(RecentGaugeSamples(0))
	if after != before {
		t.Errorf("expected no new samples after ctx cancellation, before=%d after=%d", before, after)
	}
}

func TestStartGaugeDefaultInterval(t *testing.T) {
	resetGaugeRing(t)
	// interval <= 0 should fall back to DefaultGaugeInterval rather than
	// busy-looping; just confirm it starts and stops cleanly without panicking
	// and without producing a sample within a much shorter window than the
	// (60s) default.
	stop := StartGauge(context.Background(), nil, 0)
	time.Sleep(20 * time.Millisecond)
	stop()
	if got := len(RecentGaugeSamples(0)); got != 0 {
		t.Errorf("expected 0 samples with default 60s interval after 20ms, got %d", got)
	}
}
