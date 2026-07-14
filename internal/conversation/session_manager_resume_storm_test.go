package conversation

import (
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// TestEvaluateResumeStormPredicate exercises the pure predicate in isolation.
// It is intentionally decoupled from the rate-limiter (mitto-7o2).
func TestEvaluateResumeStormPredicate(t *testing.T) {
	const capacity = maxConcurrentSessionResumes // 5
	cases := []struct {
		name      string
		queued    int
		inFlight  int
		coldCount int
		waitMS    int64
		wantStorm bool
	}{
		{"all-zero", 0, 0, 0, 0, false},
		{"below-thresholds", 2, 3, 0, 500, false},
		{"just-below-wait", 0, 0, 0, 1999, false},
		{"at-wait-threshold", 0, 0, 0, resumeStormWaitThresholdMs, true},
		{"above-wait-threshold", 0, 0, 0, 5000, true},
		{"queued-at-capacity", capacity, capacity, 0, 0, false},
		{"queued-above-capacity", capacity + 1, capacity, 0, 0, true},
		{"queued-way-above", 20, capacity, 3, 100, true},
		{"cold-alone-not-a-storm", 0, 0, 4, 0, false},
		{"in-flight-alone-not-a-storm", 0, capacity, 0, 0, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateResumeStorm(tc.queued, tc.inFlight, capacity, tc.coldCount, tc.waitMS)
			if got != tc.wantStorm {
				t.Errorf("evaluateResumeStorm(queued=%d inFlight=%d cap=%d cold=%d waitMS=%d) = %v, want %v",
					tc.queued, tc.inFlight, capacity, tc.coldCount, tc.waitMS, got, tc.wantStorm)
			}
		})
	}
}

// TestResumeStormRateLimiter verifies that shouldEmitResumeStormLog throttles
// consecutive calls within the interval and re-arms after resetResumeStormRateLimit
// (mitto-7o2).
func TestResumeStormRateLimiter(t *testing.T) {
	sm := NewSessionManager("", "test-server", true, nil)

	// First call must emit.
	if !sm.shouldEmitResumeStormLog() {
		t.Fatal("first shouldEmitResumeStormLog() = false, want true")
	}
	// Second call in the same window must be throttled.
	if sm.shouldEmitResumeStormLog() {
		t.Error("second shouldEmitResumeStormLog() = true, want false (throttled)")
	}
	// A third rapid call is still throttled.
	if sm.shouldEmitResumeStormLog() {
		t.Error("third shouldEmitResumeStormLog() = true, want false (throttled)")
	}
	// After a reset, the next call must emit again.
	sm.resetResumeStormRateLimit(t)
	if !sm.shouldEmitResumeStormLog() {
		t.Error("post-reset shouldEmitResumeStormLog() = false, want true")
	}
	// And immediately throttled again.
	if sm.shouldEmitResumeStormLog() {
		t.Error("post-reset second shouldEmitResumeStormLog() = true, want false (throttled)")
	}

	// Sanity: the throttle window is at least a few hundred ms — a Sleep-through
	// test would be flaky/slow, so we settle for the reset path above.
	_ = time.Millisecond
}

// TestResumeGaugeSymmetry_SlowPath drives a real resume attempt through the
// semaphore and asserts both gauges return to zero on the failure path.
// Uses a bogus ACP command so the resume fails fast without needing a real agent.
func TestResumeGaugeSymmetry_SlowPath(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := session.Metadata{
		SessionID:  "gauge-sym-slow",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Gauge Sym",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create failed: %v", err)
	}

	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	if got := sm.resumeInFlight.Load(); got != 0 {
		t.Fatalf("pre-resume resumeInFlight = %d, want 0", got)
	}
	if got := sm.resumeQueued.Load(); got != 0 {
		t.Fatalf("pre-resume resumeQueued = %d, want 0", got)
	}

	// The resume will fail (echo is not a valid ACP agent), but that is fine —
	// we only care about gauge symmetry, not the outcome.
	_, _ = sm.ResumeSession("gauge-sym-slow", "Gauge Sym", "/tmp")

	if got := sm.resumeInFlight.Load(); got != 0 {
		t.Errorf("post-resume resumeInFlight = %d, want 0 (leak on release path)", got)
	}
	if got := sm.resumeQueued.Load(); got != 0 {
		t.Errorf("post-resume resumeQueued = %d, want 0 (leak on acquire path)", got)
	}
}

// TestResumeGaugeSymmetry_ConcurrentBurst runs several ResumeSession calls in
// parallel against a bogus ACP command and asserts both gauges settle at zero
// once all callers return (mitto-7o2).
func TestResumeGaugeSymmetry_ConcurrentBurst(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const sessions = 8
	for i := 0; i < sessions; i++ {
		meta := session.Metadata{
			SessionID:  fmtSessionID("gauge-burst", i),
			ACPServer:  "test-server",
			WorkingDir: "/tmp",
			Name:       "Gauge Burst",
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("store.Create failed: %v", err)
		}
	}

	sm := NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _ = sm.ResumeSession(fmtSessionID("gauge-burst", i), "Gauge Burst", "/tmp")
		}(i)
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("concurrent burst deadlocked or timed out")
	}

	if got := sm.resumeInFlight.Load(); got != 0 {
		t.Errorf("post-burst resumeInFlight = %d, want 0", got)
	}
	if got := sm.resumeQueued.Load(); got != 0 {
		t.Errorf("post-burst resumeQueued = %d, want 0", got)
	}
}

// TestResumeGaugeSymmetry_FastCoalescePathIgnoresGauges verifies that a caller
// coalescing onto an in-progress pendingResumes entry does NOT touch the gauges
// (the fast path returns before reaching the semaphore) (mitto-7o2).
func TestResumeGaugeSymmetry_FastCoalescePath(t *testing.T) {
	sm := NewSessionManager("", "test-server", true, nil)

	const sessionID = "gauge-coalesce"
	expectedBS := NewMinimalBackgroundSession(sessionID, "", "")

	// Simulate a primary goroutine that has already registered a pending resume.
	pr := &pendingResumeResult{done: make(chan struct{})}
	sm.mu.Lock()
	sm.pendingResumes[sessionID] = pr
	sm.mu.Unlock()

	// Snapshot gauges before the coalesce.
	if got := sm.resumeInFlight.Load(); got != 0 {
		t.Fatalf("pre-coalesce resumeInFlight = %d, want 0", got)
	}
	if got := sm.resumeQueued.Load(); got != 0 {
		t.Fatalf("pre-coalesce resumeQueued = %d, want 0", got)
	}

	const waiters = 4
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = sm.ResumeSession(sessionID, "Coalesce", "/tmp")
		}()
	}

	// Let the waiters reach the pendingResumes fast path.
	time.Sleep(50 * time.Millisecond)

	// While waiters are blocked on pr.done, gauges MUST remain zero (they hit
	// the fast path and returned before the semaphore section).
	if got := sm.resumeQueued.Load(); got != 0 {
		t.Errorf("during-coalesce resumeQueued = %d, want 0 (fast path must not touch gauge)", got)
	}
	if got := sm.resumeInFlight.Load(); got != 0 {
		t.Errorf("during-coalesce resumeInFlight = %d, want 0 (fast path must not touch gauge)", got)
	}

	// Release the coalesced waiters.
	pr.bs = expectedBS
	pr.err = nil
	close(pr.done)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("coalesced waiters deadlocked")
	}

	if got := sm.resumeInFlight.Load(); got != 0 {
		t.Errorf("post-coalesce resumeInFlight = %d, want 0", got)
	}
	if got := sm.resumeQueued.Load(); got != 0 {
		t.Errorf("post-coalesce resumeQueued = %d, want 0", got)
	}
}

// fmtSessionID is a tiny helper to build distinct session ids without pulling in
// strconv/fmt at the call sites above.
func fmtSessionID(prefix string, i int) string {
	// Keep it dependency-free and deterministic: prefix-<i>.
	digits := []byte{}
	if i == 0 {
		digits = []byte{'0'}
	} else {
		for n := i; n > 0; n /= 10 {
			digits = append([]byte{byte('0' + n%10)}, digits...)
		}
	}
	return prefix + "-" + string(digits)
}
