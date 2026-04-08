package web

import (
	"net/http/httptest"
	"testing"
)

func TestTrustedProxyChecker_IsTrusted(t *testing.T) {
	checker := NewTrustedProxyChecker([]string{
		"127.0.0.1",
		"10.0.0.0/8",
		"192.168.1.0/24",
	})

	tests := []struct {
		name    string
		ip      string
		trusted bool
	}{
		{"localhost trusted", "127.0.0.1", true},
		{"localhost with port", "127.0.0.1:8080", true},
		{"10.x.x.x trusted", "10.1.2.3", true},
		{"192.168.1.x trusted", "192.168.1.50", true},
		{"192.168.2.x not trusted", "192.168.2.50", false},
		{"public IP not trusted", "8.8.8.8", false},
		{"empty string", "", false},
		{"invalid IP", "not-an-ip", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checker.IsTrusted(tt.ip)
			if got != tt.trusted {
				t.Errorf("IsTrusted(%q) = %v, want %v", tt.ip, got, tt.trusted)
			}
		})
	}
}

func TestTrustedProxyChecker_GetClientIP_NoProxies(t *testing.T) {
	// No trusted proxies configured
	checker := NewTrustedProxyChecker(nil)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.50:12345"
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 192.168.1.1")
	req.Header.Set("X-Real-IP", "10.0.0.2")

	// Should ignore headers and return RemoteAddr
	got := checker.GetClientIP(req)
	if got != "203.0.113.50:12345" {
		t.Errorf("GetClientIP() = %q, want %q", got, "203.0.113.50:12345")
	}
}

func TestTrustedProxyChecker_GetClientIP_TrustedProxy(t *testing.T) {
	checker := NewTrustedProxyChecker([]string{"10.0.0.0/8", "127.0.0.1", "::1"})

	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		wantIP     string
	}{
		{
			name:       "Cf-Connecting-IP takes priority",
			remoteAddr: "10.1.2.3:8080",
			headers: map[string]string{
				"Cf-Connecting-IP": "203.0.113.99",
				"X-Forwarded-For":  "203.0.113.50",
				"X-Real-IP":        "203.0.113.51",
			},
			wantIP: "203.0.113.99",
		},
		{
			name:       "X-Real-IP when no Cf-Connecting-IP",
			remoteAddr: "10.1.2.3:8080",
			headers: map[string]string{
				"X-Real-IP":       "203.0.113.50",
				"X-Forwarded-For": "203.0.113.51, 10.1.2.3",
			},
			wantIP: "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For as fallback",
			remoteAddr: "10.1.2.3:8080",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50, 10.1.2.3"},
			wantIP:     "203.0.113.50",
		},
		{
			name:       "untrusted proxy - ignore all headers",
			remoteAddr: "192.168.1.1:8080",
			headers: map[string]string{
				"Cf-Connecting-IP": "203.0.113.99",
				"X-Forwarded-For":  "203.0.113.50",
				"X-Real-IP":        "203.0.113.51",
			},
			wantIP: "192.168.1.1:8080",
		},
		{
			name:       "trusted proxy no headers - direct IP",
			remoteAddr: "10.1.2.3:8080",
			headers:    nil,
			wantIP:     "10.1.2.3:8080",
		},
		{
			name:       "IPv6 loopback trusted (cloudflared)",
			remoteAddr: "[::1]:54321",
			headers:    map[string]string{"Cf-Connecting-IP": "207.188.191.36"},
			wantIP:     "207.188.191.36",
		},
		{
			name:       "IPv4 loopback trusted",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"Cf-Connecting-IP": "207.188.191.36"},
			wantIP:     "207.188.191.36",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got := checker.GetClientIP(req)
			if got != tt.wantIP {
				t.Errorf("GetClientIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestTrustedProxyChecker_HasTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		proxies []string
		want    bool
	}{
		{"nil proxies", nil, false},
		{"empty proxies", []string{}, false},
		{"with proxies", []string{"127.0.0.1"}, true},
		{"with CIDR", []string{"10.0.0.0/8"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewTrustedProxyChecker(tt.proxies)
			if got := checker.HasTrustedProxies(); got != tt.want {
				t.Errorf("HasTrustedProxies() = %v, want %v", got, tt.want)
			}
		})
	}
}
