package conversation

// acpProcessController owns the ACP process restart policy: sliding-window rate
// limiting, lifetime cap, and the permanent-failure circuit breaker. It is a
// self-contained collaborator of BackgroundSession (held by composition) and is
// unit-testable in isolation — callers pass logger/sessionID for telemetry.

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// RestartStats contains statistics about ACP process restarts.
type RestartStats struct {
	TotalRestarts   int                   // Total number of restarts in session lifetime
	RecentRestarts  int                   // Number of restarts in the current window
	ReasonCounts    map[RestartReason]int // Count of restarts by reason
	LastRestartTime time.Time             // Timestamp of most recent restart
	LastReason      RestartReason         // Reason for most recent restart
}

type acpProcessController struct {
	mu                sync.Mutex
	restartCount      int
	restartTimes      []time.Time
	restartReasons    []RestartReason
	permanentlyFailed bool
}

// canRestart checks if we can restart the ACP process based on rate limiting.
// Returns true if restart is allowed, false if we've exceeded the limit.
// This method is thread-safe.
func (c *acpProcessController) canRestart(logger *slog.Logger, sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Circuit breaker: a permanent error (or lifetime cap) has already tripped this flag.
	// Once set, no further restart attempts are made — the sliding window is irrelevant.
	if c.permanentlyFailed {
		if logger != nil {
			logger.Debug("canRestartACP: permanently failed, circuit breaker open",
				"session_id", sessionID,
				"total_restarts", c.restartCount)
		}
		return false
	}

	// Lifetime cap: even for transient errors, don't restart more than MaxACPTotalRestarts
	// times in total. This prevents infinite retry cycles where the sliding window keeps
	// resetting every ACPRestartWindow (e.g. dead pipe, repeatedly failing cold-start).
	if c.restartCount >= MaxACPTotalRestarts {
		c.permanentlyFailed = true
		if logger != nil {
			logger.Warn("canRestartACP: lifetime restart cap reached, circuit breaker opened",
				"session_id", sessionID,
				"total_restarts", c.restartCount,
				"max_total_restarts", MaxACPTotalRestarts)
		}
		return false
	}

	now := time.Now()
	cutoff := now.Add(-ACPRestartWindow)

	// Filter out old restart times and corresponding reasons (keep indices in sync)
	var recentRestarts []time.Time
	var recentReasons []RestartReason
	for i, t := range c.restartTimes {
		if t.After(cutoff) {
			recentRestarts = append(recentRestarts, t)
			// Keep reasons in sync with times
			if i < len(c.restartReasons) {
				recentReasons = append(recentReasons, c.restartReasons[i])
			}
		}
	}
	c.restartTimes = recentRestarts
	c.restartReasons = recentReasons

	return len(recentRestarts) < MaxACPRestarts
}

// recordRestart records a restart attempt for rate limiting and telemetry.
// This method is thread-safe.
func (c *acpProcessController) recordRestart(reason RestartReason, logger *slog.Logger, sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.restartCount++
	now := time.Now()
	c.restartTimes = append(c.restartTimes, now)
	c.restartReasons = append(c.restartReasons, reason)

	// Log restart reason for telemetry
	if logger != nil {
		logger.Info("Recording ACP restart",
			"session_id", sessionID,
			"restart_count", c.restartCount,
			"reason", string(reason),
			"timestamp", now.Format(time.RFC3339))
	}
}

// getRestartInfo returns a human-readable restart attempt indicator like "(attempt 2 of 3)".
// This is shown to the user so they understand the system is in a retry loop and won't retry forever.
// This method is thread-safe.
func (c *acpProcessController) getRestartInfo() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-ACPRestartWindow)
	count := 0
	for _, t := range c.restartTimes {
		if t.After(cutoff) {
			count++
		}
	}
	// count is the number of recent restarts already done; the next one will be count+1
	return fmt.Sprintf("(attempt %d of %d)", count+1, MaxACPRestarts)
}

// stats returns statistics about ACP process restarts for telemetry.
// This method is thread-safe.
func (c *acpProcessController) stats() RestartStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := RestartStats{
		TotalRestarts: c.restartCount,
		ReasonCounts:  make(map[RestartReason]int),
	}

	// Count recent restarts and reasons
	now := time.Now()
	cutoff := now.Add(-ACPRestartWindow)
	for i, t := range c.restartTimes {
		if t.After(cutoff) {
			s.RecentRestarts++
		}
		// Count all reasons (not just recent)
		if i < len(c.restartReasons) {
			s.ReasonCounts[c.restartReasons[i]]++
		}
	}

	// Get last restart info
	if len(c.restartTimes) > 0 {
		s.LastRestartTime = c.restartTimes[len(c.restartTimes)-1]
		if len(c.restartReasons) > 0 {
			s.LastReason = c.restartReasons[len(c.restartReasons)-1]
		}
	}

	return s
}

// recentRestartCount returns the number of restarts recorded in restartTimes (raw slice length).
// This method is thread-safe.
func (c *acpProcessController) recentRestartCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.restartTimes)
}

// totalRestarts returns the total number of restarts recorded across the session lifetime.
// This method is thread-safe.
func (c *acpProcessController) totalRestarts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.restartCount
}

// markPermanentlyFailed trips the circuit breaker, preventing any future restart attempts.
// This method is thread-safe.
func (c *acpProcessController) markPermanentlyFailed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permanentlyFailed = true
}
