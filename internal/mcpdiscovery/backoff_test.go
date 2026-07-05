package mcpdiscovery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackoffPolicy_NextDelay_Schedule(t *testing.T) {
	p := BackoffPolicy{Base: time.Second, Factor: 2.0, Max: 30 * time.Second}

	want := map[int]time.Duration{
		0: time.Second,
		1: 2 * time.Second,
		2: 4 * time.Second,
		3: 8 * time.Second,
		4: 16 * time.Second,
		5: 30 * time.Second, // capped
		6: 30 * time.Second, // capped
	}
	for attempt, wantDelay := range want {
		if got := p.nextDelay(attempt); got != wantDelay {
			t.Errorf("nextDelay(%d) = %v, want %v", attempt, got, wantDelay)
		}
	}
}

func TestBackoffPolicy_NextDelay_Guards(t *testing.T) {
	t.Run("zero base falls back to 1s", func(t *testing.T) {
		p := BackoffPolicy{Base: 0, Factor: 2.0, Max: 30 * time.Second}
		if got := p.nextDelay(0); got != time.Second {
			t.Errorf("nextDelay(0) = %v, want 1s", got)
		}
	})

	t.Run("uncapped growth when Max<=0", func(t *testing.T) {
		p := BackoffPolicy{Base: time.Second, Factor: 2.0, Max: 0}
		got5 := p.nextDelay(5)
		got6 := p.nextDelay(6)
		if got5 >= got6 {
			t.Errorf("expected uncapped growth: nextDelay(5)=%v should be < nextDelay(6)=%v", got5, got6)
		}
		if got5 != 32*time.Second {
			t.Errorf("nextDelay(5) = %v, want 32s (uncapped)", got5)
		}
	})
}

func TestRetryUntilReachable_DiscoveredQuickly(t *testing.T) {
	const unreachableCount = 3
	var calls int32
	var onReachableCalls int32
	var gotResult ServerToolsResult

	probe := func(ctx context.Context) ServerToolsResult {
		n := atomic.AddInt32(&calls, 1)
		if int(n) <= unreachableCount {
			return ServerToolsResult{Server: "s", Err: errors.New("connect refused")}
		}
		return ServerToolsResult{Server: "s", Reachable: true, Tools: []string{"t1"}}
	}
	onReachable := func(r ServerToolsResult) {
		atomic.AddInt32(&onReachableCalls, 1)
		gotResult = r
	}

	policy := BackoffPolicy{Base: time.Millisecond, Factor: 2.0, Max: 10 * time.Millisecond, MaxAttempts: 20}
	start := time.Now()
	res, discovered := RetryUntilReachable(context.Background(), policy, probe, onReachable)
	elapsed := time.Since(start)

	if !discovered {
		t.Fatalf("discovered = false, want true")
	}
	if !res.Reachable {
		t.Fatalf("res.Reachable = false, want true")
	}
	if atomic.LoadInt32(&onReachableCalls) != 1 {
		t.Errorf("onReachable called %d times, want 1", onReachableCalls)
	}
	if gotResult.Server != "s" || len(gotResult.Tools) != 1 {
		t.Errorf("onReachable result = %+v, want Tools=[t1]", gotResult)
	}
	if got := atomic.LoadInt32(&calls); got != unreachableCount+1 {
		t.Errorf("probe called %d times, want %d", got, unreachableCount+1)
	}
	if elapsed > time.Second {
		t.Errorf("elapsed = %v, want a fast discovery (tiny Base), not a flat long wait", elapsed)
	}
}

func TestRetryUntilReachable_NoNegativeOnTimeout(t *testing.T) {
	var calls int32
	var onReachableCalls int32

	probe := func(ctx context.Context) ServerToolsResult {
		atomic.AddInt32(&calls, 1)
		return ServerToolsResult{Server: "s", Err: context.DeadlineExceeded}
	}
	onReachable := func(r ServerToolsResult) {
		atomic.AddInt32(&onReachableCalls, 1)
	}

	policy := BackoffPolicy{Base: time.Millisecond, Factor: 2.0, Max: 5 * time.Millisecond, MaxAttempts: 3}
	res, discovered := RetryUntilReachable(context.Background(), policy, probe, onReachable)

	if discovered {
		t.Fatalf("discovered = true, want false")
	}
	if res.Reachable {
		t.Fatalf("res.Reachable = true, want false (unreachable/keep-last-known-good)")
	}
	if atomic.LoadInt32(&onReachableCalls) != 0 {
		t.Errorf("onReachable called %d times, want 0", onReachableCalls)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("probe called %d times, want 3 (MaxAttempts)", got)
	}
}

func TestRetryUntilReachable_ContextCancellation(t *testing.T) {
	var calls int32
	var onReachableCalls int32
	ctx, cancel := context.WithCancel(context.Background())

	probe := func(ctx context.Context) ServerToolsResult {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			cancel() // cancel right after the first unreachable probe
		}
		return ServerToolsResult{Server: "s", Err: errors.New("unreachable")}
	}
	onReachable := func(r ServerToolsResult) {
		atomic.AddInt32(&onReachableCalls, 1)
	}

	// Long Base so a real (non-cancelled) run would take a while; cancellation
	// must return promptly instead of waiting out the delay.
	policy := BackoffPolicy{Base: time.Hour, Factor: 2.0, Max: time.Hour, MaxAttempts: 0}
	start := time.Now()
	res, discovered := RetryUntilReachable(ctx, policy, probe, onReachable)
	elapsed := time.Since(start)

	if discovered {
		t.Fatalf("discovered = true, want false")
	}
	if res.Reachable {
		t.Fatalf("res.Reachable = true, want false")
	}
	if atomic.LoadInt32(&onReachableCalls) != 0 {
		t.Errorf("onReachable called %d times, want 0", onReachableCalls)
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %v, want prompt return on ctx cancellation, not waiting out Base", elapsed)
	}
}

func TestScheduleRetries_OnlyProbesUnreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var probedMu sync.Mutex
	probed := map[string]int{}

	var wg sync.WaitGroup
	wg.Add(1) // "down" must reach onReachable exactly once

	results := []ServerToolsResult{
		{Server: "up", Reachable: true, Tools: []string{"already"}},
		{Server: "down", Err: errors.New("unreachable")},
	}

	probeFor := func(server string) ProbeFunc {
		return func(ctx context.Context) ServerToolsResult {
			probedMu.Lock()
			probed[server]++
			n := probed[server]
			probedMu.Unlock()
			if n >= 2 {
				return ServerToolsResult{Server: server, Reachable: true, Tools: []string{"recovered"}}
			}
			return ServerToolsResult{Server: server, Err: errors.New("still down")}
		}
	}

	var onReachableCalled int32
	onReachable := func(r ServerToolsResult) {
		atomic.AddInt32(&onReachableCalled, 1)
		wg.Done()
	}

	policy := BackoffPolicy{Base: time.Millisecond, Factor: 2.0, Max: 5 * time.Millisecond, MaxAttempts: 10}
	ScheduleRetries(ctx, results, policy, probeFor, onReachable)

	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()
	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ScheduleRetries to discover the unreachable server")
	}

	probedMu.Lock()
	_, upProbed := probed["up"]
	probedMu.Unlock()
	if upProbed {
		t.Errorf("probeFor(\"up\") was called, want reachable servers to be skipped entirely")
	}
	if atomic.LoadInt32(&onReachableCalled) != 1 {
		t.Errorf("onReachable called %d times, want exactly 1", onReachableCalled)
	}
}
