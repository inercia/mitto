// Package bdexec coordinates bd subprocess execution across Mitto packages.
package bdexec

import (
	"context"
	"log/slog"
	"time"
)

// MaxConcurrent bounds process-wide bd subprocess concurrency. Embedded Dolt
// serializes many operations internally, so allowing a large spawn fan-out only
// compounds lock contention without increasing useful throughput (mitto-i2ep).
const MaxConcurrent = 2

var slots = make(chan struct{}, MaxConcurrent)

// Acquire waits for a process-wide bd execution slot and verifies that the bd
// executable is safe before any workspace command can run. The optional binary
// argument supports processors that invoke bd by absolute path.
func Acquire(ctx context.Context, binaries ...string) (release func(), err error) {
	waitStarted := time.Now()
	select {
	case slots <- struct{}{}:
		waitDuration := time.Since(waitStarted)
		binary := "bd"
		if len(binaries) > 0 && binaries[0] != "" {
			binary = binaries[0]
		}
		if err := ensureCompatible(ctx, binary); err != nil {
			<-slots
			slog.Warn("bd compatibility check blocked execution", "binary", binary, "error", err)
			return nil, err
		}
		executionStarted := time.Now()
		return func() {
			executionDuration := time.Since(executionStarted)
			<-slots
			slog.Debug("bd limiter slot released",
				"bd_limiter_wait_ms", waitDuration.Milliseconds(),
				"bd_execution_ms", executionDuration.Milliseconds(),
				"slots_in_use", len(slots),
				"max_concurrent", MaxConcurrent)
		}, nil
	case <-ctx.Done():
		slog.Debug("bd limiter acquire failed",
			"bd_limiter_wait_ms", time.Since(waitStarted).Milliseconds(),
			"error", ctx.Err())
		return nil, ctx.Err()
	}
}
