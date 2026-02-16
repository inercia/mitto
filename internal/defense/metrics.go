package defense

import (
	"sync"
	"time"
)

// RequestInfo contains information about a single request for analysis.
type RequestInfo struct {
	Path       string
	Method     string
	StatusCode int
	UserAgent  string
	Timestamp  time.Time
}

// IPStats tracks statistics for a single IP address.
type IPStats struct {
	FirstSeen       time.Time
	LastSeen        time.Time
	TotalRequests   int
	ErrorRequests   int           // 4xx/5xx responses
	SuspiciousPaths int           // hits to suspicious paths
	RecentRequests  []RequestInfo // ring buffer for rate limiting
}

// IPMetrics tracks request metrics per IP address.
type IPMetrics struct {
	mu        sync.RWMutex
	requests  map[string]*IPStats
	window    time.Duration
	maxRecent int // max recent requests to keep per IP
}

// NewIPMetrics creates a new metrics tracker.
func NewIPMetrics(window time.Duration) *IPMetrics {
	return &IPMetrics{
		requests:  make(map[string]*IPStats),
		window:    window,
		maxRecent: 200, // Keep last 200 requests for rate limiting
	}
}

// Record adds a request to the metrics.
func (m *IPMetrics) Record(ip string, req *RequestInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stats, exists := m.requests[ip]
	if !exists {
		stats = &IPStats{
			FirstSeen:      req.Timestamp,
			RecentRequests: make([]RequestInfo, 0, m.maxRecent),
		}
		m.requests[ip] = stats
	}

	stats.LastSeen = req.Timestamp
	stats.TotalRequests++

	// Track error responses (4xx and 5xx)
	if req.StatusCode >= 400 {
		stats.ErrorRequests++
	}

	// Track suspicious paths
	if IsSuspiciousPath(req.Path) {
		stats.SuspiciousPaths++
	}

	// Add to recent requests (ring buffer behavior)
	if len(stats.RecentRequests) >= m.maxRecent {
		// Remove oldest
		stats.RecentRequests = stats.RecentRequests[1:]
	}
	stats.RecentRequests = append(stats.RecentRequests, *req)
}

// GetStats returns a copy of stats for an IP.
func (m *IPMetrics) GetStats(ip string) *IPStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats, exists := m.requests[ip]
	if !exists {
		return nil
	}

	// Return a copy
	copy := *stats
	copy.RecentRequests = make([]RequestInfo, len(stats.RecentRequests))
	_ = copy // Golang requires this
	for i, req := range stats.RecentRequests {
		copy.RecentRequests[i] = req
	}
	return &copy
}

// CountRecentRequests counts requests within the time window.
func (m *IPMetrics) CountRecentRequests(ip string, since time.Time) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats, exists := m.requests[ip]
	if !exists {
		return 0
	}

	count := 0
	for _, req := range stats.RecentRequests {
		if req.Timestamp.After(since) || req.Timestamp.Equal(since) {
			count++
		}
	}
	return count
}

// ShouldBlock analyzes metrics and returns (shouldBlock, reason).
func (m *IPMetrics) ShouldBlock(ip string, config Config) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats, exists := m.requests[ip]
	if !exists {
		return false, ""
	}

	// Check rate limit
	windowStart := time.Now().Add(-config.RateWindow)
	recentCount := 0
	for _, req := range stats.RecentRequests {
		if req.Timestamp.After(windowStart) {
			recentCount++
		}
	}
	if recentCount > config.RateLimit {
		return true, "rate_limit_exceeded"
	}

	// Check error rate (only if we have enough requests)
	if stats.TotalRequests >= config.MinRequestsForAnalysis {
		errorRate := float64(stats.ErrorRequests) / float64(stats.TotalRequests)
		if errorRate >= config.ErrorRateThreshold {
			return true, "high_error_rate"
		}
	}

	// Check suspicious path hits
	if stats.SuspiciousPaths >= config.SuspiciousPathThreshold {
		return true, "suspicious_paths"
	}

	return false, ""
}

// Cleanup removes stale entries older than maxAge.
func (m *IPMetrics) Cleanup(maxAge time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for ip, stats := range m.requests {
		if stats.LastSeen.Before(cutoff) {
			delete(m.requests, ip)
			removed++
		}
	}
	return removed
}
