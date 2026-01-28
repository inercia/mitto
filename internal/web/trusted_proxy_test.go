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
	checker := NewTrustedProxyChecker([]string{"10.0.0.0/8"})

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		wantIP     string
	}{
		{
			name:       "trusted proxy with XFF",
			remoteAddr: "10.1.2.3:8080",
			xff:        "203.0.113.50, 10.1.2.3",
			xri:        "",
			wantIP:     "203.0.113.50",
		},
		{
			name:       "trusted proxy with X-Real-IP",
			remoteAddr: "10.1.2.3:8080",
			xff:        "",
			xri:        "203.0.113.50",
			wantIP:     "203.0.113.50",
		},
		{
			name:       "untrusted proxy - ignore headers",
			remoteAddr: "192.168.1.1:8080",
			xff:        "203.0.113.50",
			xri:        "203.0.113.51",
			wantIP:     "192.168.1.1:8080",
		},
		{
			name:       "trusted proxy no headers",
			remoteAddr: "10.1.2.3:8080",
			xff:        "",
			xri:        "",
			wantIP:     "10.1.2.3:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
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
