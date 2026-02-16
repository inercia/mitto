package defense

import (
	"testing"
	"time"
)

func TestIPMetrics_Record(t *testing.T) {
	m := NewIPMetrics(time.Minute)

	req := &RequestInfo{
		Path:       "/api/test",
		Method:     "GET",
		StatusCode: 200,
		Timestamp:  time.Now(),
	}

	m.Record("192.168.1.1", req)

	stats := m.GetStats("192.168.1.1")
	if stats == nil {
		t.Fatal("Expected stats for recorded IP")
	}

	if stats.TotalRequests != 1 {
		t.Errorf("Expected 1 request, got %d", stats.TotalRequests)
	}

	if stats.ErrorRequests != 0 {
		t.Errorf("Expected 0 errors, got %d", stats.ErrorRequests)
	}
}

func TestIPMetrics_ErrorTracking(t *testing.T) {
	m := NewIPMetrics(time.Minute)

	// Record some errors
	for i := 0; i < 5; i++ {
		m.Record("192.168.1.1", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 404,
			Timestamp:  time.Now(),
		})
	}

	stats := m.GetStats("192.168.1.1")
	if stats.ErrorRequests != 5 {
		t.Errorf("Expected 5 errors, got %d", stats.ErrorRequests)
	}
}

func TestIPMetrics_SuspiciousPathTracking(t *testing.T) {
	m := NewIPMetrics(time.Minute)

	// Record suspicious path hits
	m.Record("192.168.1.1", &RequestInfo{
		Path:       "/.env",
		Method:     "GET",
		StatusCode: 404,
		Timestamp:  time.Now(),
	})

	stats := m.GetStats("192.168.1.1")
	if stats.SuspiciousPaths != 1 {
		t.Errorf("Expected 1 suspicious path hit, got %d", stats.SuspiciousPaths)
	}
}

func TestIPMetrics_ShouldBlock_RateLimit(t *testing.T) {
	m := NewIPMetrics(time.Minute)
	config := Config{
		RateLimit:  5,
		RateWindow: time.Minute,
	}

	// Record requests up to limit
	for i := 0; i < 6; i++ {
		m.Record("192.168.1.1", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	shouldBlock, reason := m.ShouldBlock("192.168.1.1", config)
	if !shouldBlock {
		t.Error("Expected IP to be blocked for rate limit")
	}
	if reason != "rate_limit_exceeded" {
		t.Errorf("Expected reason 'rate_limit_exceeded', got %q", reason)
	}
}

func TestIPMetrics_ShouldBlock_ErrorRate(t *testing.T) {
	m := NewIPMetrics(time.Minute)
	config := Config{
		ErrorRateThreshold:     0.9,
		MinRequestsForAnalysis: 10,
		RateLimit:              1000, // High limit to not trigger rate limiting
		RateWindow:             time.Minute,
	}

	// Record 10 requests with 90% errors
	for i := 0; i < 10; i++ {
		status := 404
		if i == 0 {
			status = 200 // 1 success
		}
		m.Record("192.168.1.1", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: status,
			Timestamp:  time.Now(),
		})
	}

	shouldBlock, reason := m.ShouldBlock("192.168.1.1", config)
	if !shouldBlock {
		t.Error("Expected IP to be blocked for high error rate")
	}
	if reason != "high_error_rate" {
		t.Errorf("Expected reason 'high_error_rate', got %q", reason)
	}
}

func TestIPMetrics_ShouldBlock_SuspiciousPaths(t *testing.T) {
	m := NewIPMetrics(time.Minute)
	config := Config{
		SuspiciousPathThreshold: 5,
		RateLimit:               1000,
		RateWindow:              time.Minute,
		ErrorRateThreshold:      0.99, // High threshold so error rate doesn't trigger first
		MinRequestsForAnalysis:  100,  // High minimum so error rate analysis doesn't apply
	}

	// Record 5 suspicious path hits with 200 status (so error rate doesn't trigger)
	for i := 0; i < 5; i++ {
		m.Record("192.168.1.1", &RequestInfo{
			Path:       "/.env",
			Method:     "GET",
			StatusCode: 200, // Use 200 to avoid error rate blocking
			Timestamp:  time.Now(),
		})
	}

	shouldBlock, reason := m.ShouldBlock("192.168.1.1", config)
	if !shouldBlock {
		t.Error("Expected IP to be blocked for suspicious paths")
	}
	if reason != "suspicious_paths" {
		t.Errorf("Expected reason 'suspicious_paths', got %q", reason)
	}
}

func TestIPMetrics_Cleanup(t *testing.T) {
	m := NewIPMetrics(time.Minute)

	// Record request for an IP
	m.Record("192.168.1.1", &RequestInfo{
		Path:       "/api/test",
		Method:     "GET",
		StatusCode: 200,
		Timestamp:  time.Now().Add(-2 * time.Hour), // Old timestamp
	})

	// Force lastSeen to be old
	m.mu.Lock()
	stats := m.requests["192.168.1.1"]
	stats.LastSeen = time.Now().Add(-2 * time.Hour)
	m.mu.Unlock()

	removed := m.Cleanup(time.Hour)
	if removed != 1 {
		t.Errorf("Expected 1 removed, got %d", removed)
	}

	if m.GetStats("192.168.1.1") != nil {
		t.Error("Expected stats to be removed after cleanup")
	}
}
