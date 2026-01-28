package web

import (
	"sync"
	"testing"
	"time"
)

func TestAuthRateLimiter_BasicBlocking(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(3, time.Minute, 5*time.Minute)
	defer rl.Close()

	ip := "192.168.1.1"

	// First two failures should not block
	for i := 0; i < 2; i++ {
		blocked, _ := rl.RecordFailure(ip)
		if blocked {
			t.Errorf("Should not be blocked after %d failures", i+1)
		}
	}

	// Third failure should trigger block
	blocked, duration := rl.RecordFailure(ip)
	if !blocked {
		t.Error("Should be blocked after 3 failures")
	}
	if duration < 4*time.Minute || duration > 6*time.Minute {
		t.Errorf("Lockout duration = %v, want ~5 minutes", duration)
	}

	// Subsequent checks should show blocked
	isBlocked, remaining := rl.IsBlocked(ip)
	if !isBlocked {
		t.Error("IsBlocked should return true")
	}
	if remaining < 4*time.Minute {
		t.Errorf("Remaining time = %v, want ~5 minutes", remaining)
	}
}

func TestAuthRateLimiter_DifferentIPs(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(3, time.Minute, 5*time.Minute)
	defer rl.Close()

	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Block ip1
	for i := 0; i < 3; i++ {
		rl.RecordFailure(ip1)
	}

	// ip1 should be blocked
	blocked, _ := rl.IsBlocked(ip1)
	if !blocked {
		t.Error("ip1 should be blocked")
	}

	// ip2 should not be blocked
	blocked, _ = rl.IsBlocked(ip2)
	if blocked {
		t.Error("ip2 should not be blocked")
	}

	// ip2 can still have failures
	rl.RecordFailure(ip2)
	remaining := rl.RemainingAttempts(ip2)
	if remaining != 2 {
		t.Errorf("ip2 remaining attempts = %d, want 2", remaining)
	}
}

func TestAuthRateLimiter_SuccessClearsRecord(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(3, time.Minute, 5*time.Minute)
	defer rl.Close()

	ip := "192.168.1.1"

	// Record 2 failures
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	remaining := rl.RemainingAttempts(ip)
	if remaining != 1 {
		t.Errorf("Remaining attempts = %d, want 1", remaining)
	}

	// Successful login clears the record
	rl.RecordSuccess(ip)

	remaining = rl.RemainingAttempts(ip)
	if remaining != 3 {
		t.Errorf("After success, remaining attempts = %d, want 3", remaining)
	}
}

func TestAuthRateLimiter_WindowExpiry(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(3, 100*time.Millisecond, 5*time.Minute)
	defer rl.Close()

	ip := "192.168.1.1"

	// Record 2 failures
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Failures should have expired, so we should have full attempts again
	remaining := rl.RemainingAttempts(ip)
	if remaining != 3 {
		t.Errorf("After window expiry, remaining = %d, want 3", remaining)
	}

	// New failure should not cause immediate block
	blocked, _ := rl.RecordFailure(ip)
	if blocked {
		t.Error("Should not be blocked after window expiry")
	}
}

func TestAuthRateLimiter_LockoutExpiry(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(3, time.Minute, 100*time.Millisecond)
	defer rl.Close()

	ip := "192.168.1.1"

	// Trigger lockout
	for i := 0; i < 3; i++ {
		rl.RecordFailure(ip)
	}

	blocked, _ := rl.IsBlocked(ip)
	if !blocked {
		t.Error("Should be blocked immediately after 3 failures")
	}

	// Wait for lockout to expire
	time.Sleep(150 * time.Millisecond)

	blocked, _ = rl.IsBlocked(ip)
	if blocked {
		t.Error("Should not be blocked after lockout expiry")
	}

	// Should be able to try again
	remaining := rl.RemainingAttempts(ip)
	if remaining != 3 {
		t.Errorf("After lockout expiry, remaining = %d, want 3", remaining)
	}
}

func TestAuthRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(100, time.Minute, 5*time.Minute)
	defer rl.Close()

	var wg sync.WaitGroup
	numGoroutines := 50
	failuresPerGoroutine := 10

	// Concurrent failures from different IPs
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ip := "192.168.1." + string(rune('0'+id%10))
			for j := 0; j < failuresPerGoroutine; j++ {
				rl.RecordFailure(ip)
				rl.IsBlocked(ip)
				rl.RemainingAttempts(ip)
			}
		}(i)
	}

	wg.Wait()

	// Should not panic or deadlock
	total, blocked := rl.Stats()
	t.Logf("After concurrent access: total=%d, blocked=%d", total, blocked)
}

func TestAuthRateLimiter_ConcurrentSameIP(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(10, time.Minute, 5*time.Minute)
	defer rl.Close()

	ip := "192.168.1.1"
	var wg sync.WaitGroup
	numGoroutines := 20

	// Concurrent failures from same IP
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.RecordFailure(ip)
		}()
	}

	wg.Wait()

	// Should be blocked (20 failures > 10 max)
	blocked, _ := rl.IsBlocked(ip)
	if !blocked {
		t.Error("Should be blocked after 20 concurrent failures")
	}
}

func TestAuthRateLimiter_RemainingAttempts(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(5, time.Minute, 5*time.Minute)
	defer rl.Close()

	ip := "192.168.1.1"

	// Initially should have max attempts
	if remaining := rl.RemainingAttempts(ip); remaining != 5 {
		t.Errorf("Initial remaining = %d, want 5", remaining)
	}

	// After each failure, remaining should decrease
	for i := 0; i < 4; i++ {
		rl.RecordFailure(ip)
		expected := 5 - (i + 1)
		if remaining := rl.RemainingAttempts(ip); remaining != expected {
			t.Errorf("After %d failures, remaining = %d, want %d", i+1, remaining, expected)
		}
	}

	// After blocking, remaining should be -1
	rl.RecordFailure(ip)
	if remaining := rl.RemainingAttempts(ip); remaining != -1 {
		t.Errorf("When blocked, remaining = %d, want -1", remaining)
	}
}

func TestAuthRateLimiter_Stats(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(2, time.Minute, 5*time.Minute)
	defer rl.Close()

	// Initially empty
	total, blocked := rl.Stats()
	if total != 0 || blocked != 0 {
		t.Errorf("Initial stats: total=%d, blocked=%d, want 0, 0", total, blocked)
	}

	// Add some failures
	rl.RecordFailure("192.168.1.1")
	rl.RecordFailure("192.168.1.2")

	total, blocked = rl.Stats()
	if total != 2 || blocked != 0 {
		t.Errorf("After 2 failures: total=%d, blocked=%d, want 2, 0", total, blocked)
	}

	// Block one IP
	rl.RecordFailure("192.168.1.1")

	total, blocked = rl.Stats()
	if total != 2 || blocked != 1 {
		t.Errorf("After blocking: total=%d, blocked=%d, want 2, 1", total, blocked)
	}
}

func TestAuthRateLimiter_ExtendLockoutOnContinuedAttempts(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(2, time.Minute, 100*time.Millisecond)
	defer rl.Close()

	ip := "192.168.1.1"

	// Trigger lockout
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	blocked, _ := rl.IsBlocked(ip)
	if !blocked {
		t.Error("Should be blocked")
	}

	// Additional failure while locked should not reset the lockout timer
	// (prevents attackers from extending lockout indefinitely)
	nowBlocked, _ := rl.RecordFailure(ip)
	if !nowBlocked {
		t.Error("Should still be blocked")
	}

	// Wait for lockout to expire
	time.Sleep(150 * time.Millisecond)

	blocked, _ = rl.IsBlocked(ip)
	if blocked {
		t.Error("Should not be blocked after lockout expiry")
	}
}

func TestAuthRateLimiter_CustomTimeFunc(t *testing.T) {
	rl := NewAuthRateLimiterWithConfig(3, time.Minute, 5*time.Minute)
	defer rl.Close()

	// Inject custom time function for testing
	currentTime := time.Now()
	rl.nowFunc = func() time.Time { return currentTime }

	ip := "192.168.1.1"

	// Record failures
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	blocked, _ := rl.IsBlocked(ip)
	if !blocked {
		t.Error("Should be blocked")
	}

	// Advance time past lockout
	currentTime = currentTime.Add(6 * time.Minute)

	blocked, _ = rl.IsBlocked(ip)
	if blocked {
		t.Error("Should not be blocked after time advance")
	}
}
