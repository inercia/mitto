package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeneralRateLimiter_Allow(t *testing.T) {
	config := RateLimitConfig{
		RequestsPerSecond: 2,
		BurstSize:         3,
		CleanupInterval:   time.Hour, // Don't cleanup during test
		EntryTTL:          time.Hour,
	}
	rl := NewGeneralRateLimiter(config)
	defer rl.Close()

	ip := "192.168.1.1"

	// First 3 requests (burst) should be allowed
	for i := 0; i < 3; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}

	// 4th request should be denied (burst exhausted)
	if rl.Allow(ip) {
		t.Error("4th request should be denied (burst exhausted)")
	}
}

func TestGeneralRateLimiter_MultipleIPs(t *testing.T) {
	config := RateLimitConfig{
		RequestsPerSecond: 1,
		BurstSize:         2,
		CleanupInterval:   time.Hour,
		EntryTTL:          time.Hour,
	}
	rl := NewGeneralRateLimiter(config)
	defer rl.Close()

	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Exhaust burst for ip1
	rl.Allow(ip1)
	rl.Allow(ip1)

	// ip2 should still have its own burst
	if !rl.Allow(ip2) {
		t.Error("ip2 should be allowed (separate rate limit)")
	}
	if !rl.Allow(ip2) {
		t.Error("ip2 second request should be allowed")
	}

	// ip1 should be denied
	if rl.Allow(ip1) {
		t.Error("ip1 should be denied (burst exhausted)")
	}
}

func TestGeneralRateLimiter_Middleware(t *testing.T) {
	config := RateLimitConfig{
		RequestsPerSecond: 1,
		BurstSize:         2,
		CleanupInterval:   time.Hour,
		EntryTTL:          time.Hour,
	}
	rl := NewGeneralRateLimiter(config)
	defer rl.Close()

	// Create a simple handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with rate limiter middleware
	wrapped := rl.Middleware(handler)

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Request %d: got status %d, want %d", i+1, rec.Code, http.StatusOK)
		}
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("3rd request: got status %d, want %d", rec.Code, http.StatusTooManyRequests)
	}

	// Check Retry-After header
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Expected Retry-After header on rate limited response")
	}
}

func TestGeneralRateLimiter_Stats(t *testing.T) {
	config := DefaultRateLimitConfig()
	rl := NewGeneralRateLimiter(config)
	defer rl.Close()

	// Initially no entries
	if stats := rl.Stats(); stats != 0 {
		t.Errorf("Stats() = %d, want 0", stats)
	}

	// Add some IPs
	rl.Allow("192.168.1.1")
	rl.Allow("192.168.1.2")
	rl.Allow("192.168.1.3")

	if stats := rl.Stats(); stats != 3 {
		t.Errorf("Stats() = %d, want 3", stats)
	}
}

func TestDefaultRateLimitConfig(t *testing.T) {
	config := DefaultRateLimitConfig()

	if config.RequestsPerSecond <= 0 {
		t.Error("RequestsPerSecond should be positive")
	}
	if config.BurstSize <= 0 {
		t.Error("BurstSize should be positive")
	}
	if config.CleanupInterval <= 0 {
		t.Error("CleanupInterval should be positive")
	}
	if config.EntryTTL <= 0 {
		t.Error("EntryTTL should be positive")
	}
}

func TestGeneralRateLimiter_Cleanup(t *testing.T) {
	config := RateLimitConfig{
		RequestsPerSecond: 10,
		BurstSize:         10,
		CleanupInterval:   time.Hour, // Don't auto-cleanup
		EntryTTL:          10 * time.Millisecond,
	}
	rl := NewGeneralRateLimiter(config)
	defer rl.Close()

	// Add some entries
	rl.Allow("192.168.1.1")
	rl.Allow("192.168.1.2")

	// Verify entries exist
	if rl.Stats() != 2 {
		t.Errorf("Stats() = %d, want 2", rl.Stats())
	}

	// Wait for entries to expire
	time.Sleep(20 * time.Millisecond)

	// Manually trigger cleanup
	rl.cleanup()

	// Entries should be cleaned up
	if rl.Stats() != 0 {
		t.Errorf("Stats() = %d, want 0 after cleanup", rl.Stats())
	}
}
