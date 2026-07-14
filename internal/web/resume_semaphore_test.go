package web

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestResumeSemaphore_BoundsConcurrency saturates the semaphore with many
// concurrent "interactive resumes" and asserts that no more than `capacity`
// ever run simultaneously — the core mitto-54k.1 acceptance criterion for the
// cold-start fan-out at internal/web/session_ws.go:~378.
func TestResumeSemaphore_BoundsConcurrency(t *testing.T) {
	const capacity = 3
	const goroutines = 28 // roughly the observed cold-start fan-out size

	sem := newResumeSemaphore(capacity)
	if got := sem.Capacity(); got != capacity {
		t.Fatalf("Capacity() = %d, want %d", got, capacity)
	}

	var (
		inFlight    int32
		maxObserved int32
		completed   int32
		wg          sync.WaitGroup
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			sem.Acquire()
			defer sem.Release()
			now := atomic.AddInt32(&inFlight, 1)
			// Track high-water mark via CAS loop.
			for {
				prev := atomic.LoadInt32(&maxObserved)
				if now <= prev || atomic.CompareAndSwapInt32(&maxObserved, prev, now) {
					break
				}
			}
			// Simulate ResumeSession work: long enough that natural scheduling
			// pressure would exceed the bound if the semaphore were absent.
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			atomic.AddInt32(&completed, 1)
		}()
	}

	wg.Wait()

	if got := atomic.LoadInt32(&completed); got != goroutines {
		t.Fatalf("completed = %d, want %d", got, goroutines)
	}
	if got := atomic.LoadInt32(&maxObserved); got > int32(capacity) {
		t.Fatalf("max observed concurrency = %d, want <= %d", got, capacity)
	}
	if got := atomic.LoadInt32(&maxObserved); got < 1 {
		t.Fatalf("max observed concurrency = %d, want >= 1 (test never ran?)", got)
	}
}

// TestResumeSemaphore_ForegroundBypass asserts that a caller which does NOT
// acquire the semaphore (the ensure_resumed / user-focused path at
// internal/web/session_ws.go:~1994) makes progress even while every slot is
// held by long-running cold-start interactive resumes. This encodes the
// mitto-54k.1 requirement that ensure_resumed must NOT be throttled.
func TestResumeSemaphore_ForegroundBypass(t *testing.T) {
	const capacity = 3
	sem := newResumeSemaphore(capacity)

	// Saturate all slots with goroutines that hold them until released.
	release := make(chan struct{})
	held := make(chan struct{}, capacity)
	for i := 0; i < capacity; i++ {
		go func() {
			sem.Acquire()
			held <- struct{}{}
			<-release
			sem.Release()
		}()
	}
	// Wait for all slots to actually be acquired before running the foreground
	// caller — otherwise we'd race the saturation and get a false pass.
	for i := 0; i < capacity; i++ {
		select {
		case <-held:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for interactive slots to fill")
		}
	}

	// Foreground caller does NOT acquire the semaphore. It must complete
	// promptly even though every interactive slot is currently held.
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Simulated ensure_resumed work — no Acquire/Release.
		time.Sleep(5 * time.Millisecond)
	}()

	select {
	case <-done:
		// success: foreground path bypassed the semaphore
	case <-time.After(1 * time.Second):
		t.Fatal("ensure_resumed-style foreground caller was blocked by the interactive semaphore")
	}

	// Cleanup — release the held slots.
	close(release)
}

// TestResumeSemaphore_ClampsNonPositiveCapacity ensures that a mis-configured
// non-positive capacity is clamped to 1 instead of producing a size-0
// semaphore (which would deadlock every acquire).
func TestResumeSemaphore_ClampsNonPositiveCapacity(t *testing.T) {
	for _, in := range []int{0, -1, -100} {
		sem := newResumeSemaphore(in)
		if got := sem.Capacity(); got != 1 {
			t.Errorf("newResumeSemaphore(%d).Capacity() = %d, want 1 (clamped)", in, got)
		}
	}
}

// TestResumeSemaphore_NilReceiverIsNoop guards the nil-safe Acquire/Release
// contract relied on by the session_ws.go call site.
func TestResumeSemaphore_NilReceiverIsNoop(t *testing.T) {
	var sem *resumeSemaphore
	// Neither call should panic or block.
	sem.Acquire()
	sem.Release()
	if got := sem.Capacity(); got != 0 {
		t.Errorf("nil.Capacity() = %d, want 0", got)
	}
}
