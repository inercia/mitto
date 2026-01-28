package web

import (
	"net/http/httptest"
	"testing"
)

func TestOriginChecker_SameOrigin(t *testing.T) {
	checker := createOriginChecker(nil) // nil = same-origin only

	tests := []struct {
		name      string
		host      string
		origin    string
		wantAllow bool
	}{
		{
			name:      "same origin http",
			host:      "localhost:8080",
			origin:    "http://localhost:8080",
			wantAllow: true,
		},
		{
			name:      "same origin https",
			host:      "example.com",
			origin:    "https://example.com",
			wantAllow: true,
		},
		{
			name:      "different origin",
			host:      "localhost:8080",
			origin:    "http://evil.com",
			wantAllow: false,
		},
		{
			name:      "no origin header (non-browser)",
			host:      "localhost:8080",
			origin:    "",
			wantAllow: true,
		},
		{
			name:      "different port",
			host:      "localhost:8080",
			origin:    "http://localhost:9090",
			wantAllow: false,
		},
		{
			name:      "subdomain attack",
			host:      "example.com",
			origin:    "http://evil.example.com",
			wantAllow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			got := checker(req)
			if got != tt.wantAllow {
				t.Errorf("checker() = %v, want %v", got, tt.wantAllow)
			}
		})
	}
}

func TestOriginChecker_AllowList(t *testing.T) {
	allowedOrigins := []string{
		"https://trusted.com",
		"https://also-trusted.com:8443",
	}
	checker := createOriginChecker(allowedOrigins)

	tests := []struct {
		name      string
		origin    string
		wantAllow bool
	}{
		{
			name:      "allowed origin",
			origin:    "https://trusted.com",
			wantAllow: true,
		},
		{
			name:      "allowed origin with port",
			origin:    "https://also-trusted.com:8443",
			wantAllow: true,
		},
		{
			name:      "not in allowlist",
			origin:    "https://evil.com",
			wantAllow: false,
		},
		{
			name:      "no origin (non-browser)",
			origin:    "",
			wantAllow: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			req.Host = "myserver.com"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			got := checker(req)
			if got != tt.wantAllow {
				t.Errorf("checker() = %v, want %v", got, tt.wantAllow)
			}
		})
	}
}

func TestOriginChecker_AllowAll(t *testing.T) {
	checker := createOriginChecker([]string{"*"})

	tests := []struct {
		name   string
		origin string
	}{
		{"any origin", "https://any-site.com"},
		{"localhost", "http://localhost:3000"},
		{"no origin", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			req.Host = "myserver.com"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			if !checker(req) {
				t.Errorf("checker() = false, want true for origin %q", tt.origin)
			}
		})
	}
}

func TestConnectionTracker_Basic(t *testing.T) {
	tracker := NewConnectionTracker(3) // Max 3 connections per IP

	ip := "192.168.1.100"

	// First 3 connections should succeed
	for i := 0; i < 3; i++ {
		if !tracker.TryAdd(ip) {
			t.Errorf("TryAdd() failed for connection %d, expected success", i+1)
		}
	}

	// 4th connection should fail
	if tracker.TryAdd(ip) {
		t.Error("TryAdd() succeeded for 4th connection, expected failure")
	}

	// Count should be 3
	if count := tracker.Count(ip); count != 3 {
		t.Errorf("Count() = %d, want 3", count)
	}

	// Remove one connection
	tracker.Remove(ip)

	// Now we should be able to add one more
	if !tracker.TryAdd(ip) {
		t.Error("TryAdd() failed after Remove(), expected success")
	}
}

func TestConnectionTracker_MultipleIPs(t *testing.T) {
	tracker := NewConnectionTracker(2)

	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Add 2 connections for each IP
	tracker.TryAdd(ip1)
	tracker.TryAdd(ip1)
	tracker.TryAdd(ip2)
	tracker.TryAdd(ip2)

	// Both should be at limit
	if tracker.TryAdd(ip1) {
		t.Error("ip1 should be at limit")
	}
	if tracker.TryAdd(ip2) {
		t.Error("ip2 should be at limit")
	}

	// Total connections should be 4
	if total := tracker.TotalConnections(); total != 4 {
		t.Errorf("TotalConnections() = %d, want 4", total)
	}
}

func TestConnectionTracker_RemoveToZero(t *testing.T) {
	tracker := NewConnectionTracker(5)

	ip := "10.0.0.1"

	tracker.TryAdd(ip)
	tracker.Remove(ip)

	// Count should be 0 (entry removed)
	if count := tracker.Count(ip); count != 0 {
		t.Errorf("Count() = %d after remove to zero, want 0", count)
	}

	// Should be able to add again
	if !tracker.TryAdd(ip) {
		t.Error("TryAdd() failed after removing all connections")
	}
}
