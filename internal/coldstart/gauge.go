package coldstart

// Periodic goroutine gauge with per-category attribution (mitto-x3x).
//
// Unlike the cold-start Trace/Summary ring above (sampled only when a cold
// start occurs), StartGauge samples Contention() on a fixed interval so a
// goroutine-count time series exists independent of cold-start frequency.
// Samples are kept in their own small ring (same ringCapacity convention)
// and are readable via RecentGaugeSamples without grepping mitto.log.

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultGaugeInterval is the default period between goroutine gauge samples.
const DefaultGaugeInterval = 60 * time.Second

// gaugeLogDelta is the minimum absolute change in NumGoroutine since the last
// INFO-logged sample required to promote a tick from DEBUG to INFO. Keeps
// mitto.log from gaining a per-minute INFO line while still surfacing real
// movement.
const gaugeLogDelta = 10

// GaugeSample is one periodic Contention() reading kept in the gauge ring.
type GaugeSample struct {
	ContentionSnapshot
	At time.Time `json:"at"`
}

var (
	gaugeRingMu   sync.Mutex
	gaugeRingBuf  [ringCapacity]GaugeSample
	gaugeRingLen  int
	gaugeRingNext int
)

func gaugeRingAppend(s GaugeSample) {
	gaugeRingMu.Lock()
	gaugeRingBuf[gaugeRingNext] = s
	gaugeRingNext = (gaugeRingNext + 1) % ringCapacity
	if gaugeRingLen < ringCapacity {
		gaugeRingLen++
	}
	gaugeRingMu.Unlock()
}

// RecentGaugeSamples returns up to k most-recent periodic gauge samples,
// newest first. k<=0 returns all held samples.
func RecentGaugeSamples(k int) []GaugeSample {
	gaugeRingMu.Lock()
	defer gaugeRingMu.Unlock()
	n := gaugeRingLen
	if k > 0 && k < n {
		n = k
	}
	out := make([]GaugeSample, 0, n)
	idx := gaugeRingNext - 1
	for i := 0; i < n; i++ {
		if idx < 0 {
			idx += ringCapacity
		}
		out = append(out, gaugeRingBuf[idx])
		idx--
	}
	return out
}

// StartGauge launches a background goroutine that samples Contention() every
// interval (DefaultGaugeInterval when interval <= 0), appends the sample to
// the gauge ring, and logs "goroutine_gauge_sample" — at DEBUG normally,
// promoted to INFO when NumGoroutine has moved by at least gaugeLogDelta
// since the last INFO-logged sample. The returned stop func cancels the
// ticker and BLOCKS until the background goroutine has fully exited, so no
// sample can ever be appended after stop() returns (closing the race where a
// tick already in flight when cancel() fires would otherwise land a straggler
// sample). ctx cancellation also stops it, though callers relying on that
// path alone must poll/wait themselves for the same guarantee. Both stop()
// and ctx cancellation are safe to trigger more than once. Nil-safe logger: a
// nil logger simply skips logging.
func StartGauge(ctx context.Context, logger *slog.Logger, interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = DefaultGaugeInterval
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		lastLoggedNumGoroutine := -1
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				snap := Contention()
				sample := GaugeSample{ContentionSnapshot: snap, At: time.Now()}
				gaugeRingAppend(sample)

				if logger == nil {
					continue
				}
				level := slog.LevelDebug
				delta := snap.NumGoroutine - lastLoggedNumGoroutine
				if delta < 0 {
					delta = -delta
				}
				if lastLoggedNumGoroutine < 0 || delta >= gaugeLogDelta {
					level = slog.LevelInfo
					lastLoggedNumGoroutine = snap.NumGoroutine
				}
				logger.Log(runCtx, level, "goroutine_gauge_sample", snap.LogAttrs()...)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(cancel)
		<-done
	}
}
