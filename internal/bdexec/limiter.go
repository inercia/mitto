// Package bdexec coordinates bd subprocess execution across Mitto packages.
package bdexec

import "context"

// MaxConcurrent bounds process-wide bd subprocess concurrency. Embedded Dolt
// serializes many operations internally, so allowing a large spawn fan-out only
// compounds lock contention without increasing useful throughput (mitto-i2ep).
const MaxConcurrent = 2

var slots = make(chan struct{}, MaxConcurrent)

// Acquire waits for a process-wide bd execution slot. Waiting is bounded by ctx;
// callers whose budget expires never spawn another process into the contention.
func Acquire(ctx context.Context) (release func(), err error) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
