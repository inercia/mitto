package web

import (
	"sync"
	"time"
)

const (
	// Default rate limiting settings - tuned for security against brute force attacks
	defaultMaxFailures     = 5                // Max failures before lockout
	defaultFailureWindow   = 5 * time.Minute  // Window to count failures
	defaultLockoutDuration = 15 * time.Minute // How long to lock out after max failures

	// Cleanup interval for expired entries
	cleanupInterval = 1 * time.Minute
)

// failureRecord tracks authentication failures for a single IP.
type failureRecord struct {
	failures    []time.Time // Timestamps of recent failures
	lockedUntil time.Time   // If set, IP is locked until this time
}

// AuthRateLimiter tracks failed authentication attempts and enforces rate limits.
// It is safe for concurrent use.
type AuthRateLimiter struct {
	mu              sync.RWMutex
	records         map[string]*failureRecord
	maxFailures     int
	failureWindow   time.Duration
	lockoutDuration time.Duration

	// For testing: allows injecting a custom time source
	nowFunc func() time.Time

	// Cleanup goroutine control
	stopCleanup chan struct{}
	cleanupDone chan struct{}
}

// NewAuthRateLimiter creates a new rate limiter with default settings.
func NewAuthRateLimiter() *AuthRateLimiter {
	rl := &AuthRateLimiter{
		records:         make(map[string]*failureRecord),
		maxFailures:     defaultMaxFailures,
		failureWindow:   defaultFailureWindow,
		lockoutDuration: defaultLockoutDuration,
		nowFunc:         time.Now,
		stopCleanup:     make(chan struct{}),
		cleanupDone:     make(chan struct{}),
	}

	// Start background cleanup goroutine
	go rl.cleanupLoop()

	return rl
}

// NewAuthRateLimiterWithConfig creates a rate limiter with custom settings.
func NewAuthRateLimiterWithConfig(maxFailures int, failureWindow, lockoutDuration time.Duration) *AuthRateLimiter {
	rl := &AuthRateLimiter{
		records:         make(map[string]*failureRecord),
		maxFailures:     maxFailures,
		failureWindow:   failureWindow,
		lockoutDuration: lockoutDuration,
		nowFunc:         time.Now,
		stopCleanup:     make(chan struct{}),
		cleanupDone:     make(chan struct{}),
	}

	go rl.cleanupLoop()

	return rl
}

// Close stops the cleanup goroutine and releases resources.
func (rl *AuthRateLimiter) Close() {
	close(rl.stopCleanup)
	<-rl.cleanupDone
}

// IsBlocked checks if an IP is currently blocked due to too many failures.
// Returns true if blocked, along with the remaining lockout duration.
func (rl *AuthRateLimiter) IsBlocked(ip string) (blocked bool, remaining time.Duration) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	record, exists := rl.records[ip]
	if !exists {
		return false, 0
	}

	now := rl.nowFunc()

	// Check if currently locked out
	if !record.lockedUntil.IsZero() && now.Before(record.lockedUntil) {
		return true, record.lockedUntil.Sub(now)
	}

	return false, 0
}

// RecordFailure records a failed authentication attempt for an IP.
// Returns true if the IP is now blocked, along with the lockout duration.
func (rl *AuthRateLimiter) RecordFailure(ip string) (nowBlocked bool, lockoutDuration time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc()

	record, exists := rl.records[ip]
	if !exists {
		record = &failureRecord{
			failures: make([]time.Time, 0, rl.maxFailures),
		}
		rl.records[ip] = record
	}

	// If already locked, extend the lockout (prevents timing attacks)
	if !record.lockedUntil.IsZero() && now.Before(record.lockedUntil) {
		// Already locked, don't reveal timing information
		return true, record.lockedUntil.Sub(now)
	}

	// Clear expired lockout
	if !record.lockedUntil.IsZero() && now.After(record.lockedUntil) {
		record.lockedUntil = time.Time{}
		record.failures = record.failures[:0]
	}

	// Remove failures outside the window
	cutoff := now.Add(-rl.failureWindow)
	validFailures := record.failures[:0]
	for _, t := range record.failures {
		if t.After(cutoff) {
			validFailures = append(validFailures, t)
		}
	}
	record.failures = validFailures

	// Add the new failure
	record.failures = append(record.failures, now)

	// Check if we've hit the limit
	if len(record.failures) >= rl.maxFailures {
		record.lockedUntil = now.Add(rl.lockoutDuration)
		return true, rl.lockoutDuration
	}

	return false, 0
}

// RecordSuccess clears the failure record for an IP after successful auth.
func (rl *AuthRateLimiter) RecordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	delete(rl.records, ip)
}

// RemainingAttempts returns the number of attempts remaining before lockout.
// Returns -1 if the IP is currently locked out.
func (rl *AuthRateLimiter) RemainingAttempts(ip string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	record, exists := rl.records[ip]
	if !exists {
		return rl.maxFailures
	}

	now := rl.nowFunc()

	// Check if currently locked out
	if !record.lockedUntil.IsZero() && now.Before(record.lockedUntil) {
		return -1
	}

	// If lockout has expired, the record is effectively reset
	if !record.lockedUntil.IsZero() && now.After(record.lockedUntil) {
		return rl.maxFailures
	}

	// Count valid failures within the window
	cutoff := now.Add(-rl.failureWindow)
	validCount := 0
	for _, t := range record.failures {
		if t.After(cutoff) {
			validCount++
		}
	}

	remaining := rl.maxFailures - validCount
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

// cleanupLoop periodically removes expired records to prevent memory leaks.
func (rl *AuthRateLimiter) cleanupLoop() {
	defer close(rl.cleanupDone)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCleanup:
			return
		case <-ticker.C:
			rl.cleanup()
		}
	}
}

// cleanup removes expired records.
func (rl *AuthRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc()
	cutoff := now.Add(-rl.failureWindow)

	for ip, record := range rl.records {
		// Remove if lockout has expired and no recent failures
		if !record.lockedUntil.IsZero() && now.After(record.lockedUntil) {
			delete(rl.records, ip)
			continue
		}

		// Remove if no failures within the window and not locked
		if record.lockedUntil.IsZero() {
			hasRecentFailures := false
			for _, t := range record.failures {
				if t.After(cutoff) {
					hasRecentFailures = true
					break
				}
			}
			if !hasRecentFailures {
				delete(rl.records, ip)
			}
		}
	}
}

// Stats returns current statistics for monitoring/debugging.
func (rl *AuthRateLimiter) Stats() (totalRecords, blockedIPs int) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	now := rl.nowFunc()
	totalRecords = len(rl.records)

	for _, record := range rl.records {
		if !record.lockedUntil.IsZero() && now.Before(record.lockedUntil) {
			blockedIPs++
		}
	}

	return totalRecords, blockedIPs
}
