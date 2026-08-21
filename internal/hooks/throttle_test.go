package hooks

import (
	"sync"
	"testing"
	"time"
)

func TestThrottle_FirstNSuppressed(t *testing.T) {
	// Restore defaults after test
	origW, origT := hookFailureWindow, hookFailureThreshold
	defer func() { hookFailureWindow, hookFailureThreshold = origW, origT }()
	hookFailureWindow = 5 * time.Minute
	hookFailureThreshold = 2

	th := newHookFailureThrottle()
	now := time.Now()

	// First 2 (== threshold) should be suppressed.
	for i := 1; i <= 2; i++ {
		suppress, seen := th.ShouldSuppress("cf-tunnel", now.Add(time.Duration(i)*time.Second))
		if !suppress {
			t.Errorf("failure #%d should be suppressed (threshold=2), got suppress=false seen=%d", i, seen)
		}
		if seen != i {
			t.Errorf("failure #%d: seen=%d want %d", i, seen, i)
		}
	}

	// 3rd should broadcast.
	suppress, seen := th.ShouldSuppress("cf-tunnel", now.Add(3*time.Second))
	if suppress {
		t.Errorf("failure #3 should NOT be suppressed (past threshold), seen=%d", seen)
	}
	if seen != 3 {
		t.Errorf("failure #3: seen=%d want 3", seen)
	}
}

func TestThrottle_WindowSlides(t *testing.T) {
	origW, origT := hookFailureWindow, hookFailureThreshold
	defer func() { hookFailureWindow, hookFailureThreshold = origW, origT }()
	hookFailureWindow = 1 * time.Second
	hookFailureThreshold = 2

	th := newHookFailureThrottle()
	base := time.Now()

	// Fill window: 2 failures suppressed
	_, _ = th.ShouldSuppress("h", base)
	_, _ = th.ShouldSuppress("h", base.Add(100*time.Millisecond))

	// A failure OUTSIDE the window trims the old ones and behaves like the
	// very first failure in a fresh window — suppress=true.
	suppress, seen := th.ShouldSuppress("h", base.Add(5*time.Second))
	if !suppress {
		t.Errorf("failure outside window should reset counter and be suppressed, got suppress=false seen=%d", seen)
	}
	if seen != 1 {
		t.Errorf("after window slide seen=%d, want 1", seen)
	}
}

func TestThrottle_PerNameIsolation(t *testing.T) {
	origW, origT := hookFailureWindow, hookFailureThreshold
	defer func() { hookFailureWindow, hookFailureThreshold = origW, origT }()
	hookFailureWindow = 5 * time.Minute
	hookFailureThreshold = 2

	th := newHookFailureThrottle()
	now := time.Now()

	// Exhaust "hook-a" window
	_, _ = th.ShouldSuppress("hook-a", now)
	_, _ = th.ShouldSuppress("hook-a", now)
	suppress, _ := th.ShouldSuppress("hook-a", now)
	if suppress {
		t.Fatal("hook-a #3 must broadcast (threshold exhausted)")
	}

	// A completely different hook name has its own counter → still suppresses.
	suppress, seen := th.ShouldSuppress("hook-b", now)
	if !suppress {
		t.Errorf("hook-b #1 must be suppressed independently of hook-a, seen=%d", seen)
	}
}

func TestThrottle_ConcurrentSafe(t *testing.T) {
	origW, origT := hookFailureWindow, hookFailureThreshold
	defer func() { hookFailureWindow, hookFailureThreshold = origW, origT }()
	hookFailureWindow = 5 * time.Minute
	hookFailureThreshold = 5

	th := newHookFailureThrottle()

	// Hammer the throttle from many goroutines — must not race or panic.
	var wg sync.WaitGroup
	const workers = 20
	const per = 10
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				_, _ = th.ShouldSuppress("concurrent", time.Now())
			}
		}()
	}
	wg.Wait()

	// Final call should reflect all events in the window (well past threshold).
	_, seen := th.ShouldSuppress("concurrent", time.Now())
	if seen != workers*per+1 {
		t.Errorf("concurrent throttle seen=%d, want %d", seen, workers*per+1)
	}
}

func TestThrottle_ThresholdZero(t *testing.T) {
	// With threshold=0, no failures should be suppressed — every one broadcasts.
	origW, origT := hookFailureWindow, hookFailureThreshold
	defer func() { hookFailureWindow, hookFailureThreshold = origW, origT }()
	hookFailureWindow = 5 * time.Minute
	hookFailureThreshold = 0

	th := newHookFailureThrottle()
	now := time.Now()
	for i := 1; i <= 3; i++ {
		suppress, _ := th.ShouldSuppress("z", now.Add(time.Duration(i)*time.Second))
		if suppress {
			t.Errorf("threshold=0 must never suppress, but failure #%d was suppressed", i)
		}
	}
}
