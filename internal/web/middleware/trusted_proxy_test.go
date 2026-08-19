package middleware

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
	tests := []struct {
		name           string
		remoteAddr     string
		headers        map[string]string
		trustedHeaders []string
		wantIP         string
	}{
		{
			name:           "explicit Cf-Connecting-IP",
			remoteAddr:     "10.1.2.3:8080",
			headers:        map[string]string{"Cf-Connecting-IP": "203.0.113.99"},
			trustedHeaders: []string{TrustedProxyHeaderCFConnectingIP},
			wantIP:         "203.0.113.99",
		},
		{
			name:           "explicit X-Real-IP",
			remoteAddr:     "10.1.2.3:8080",
			headers:        map[string]string{"X-Real-IP": "203.0.113.50"},
			trustedHeaders: []string{TrustedProxyHeaderXRealIP},
			wantIP:         "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For default",
			remoteAddr: "10.1.2.3:8080",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50, 10.1.2.3"},
			wantIP:     "203.0.113.50",
		},
		{
			name:       "single-IP headers ignored by default",
			remoteAddr: "10.1.2.3:8080",
			headers: map[string]string{
				"Cf-Connecting-IP": "127.0.0.1",
				"X-Real-IP":        "127.0.0.1",
				"X-Forwarded-For":  "203.0.113.50",
			},
			wantIP: "203.0.113.50",
		},
		{
			name:       "untrusted proxy ignores all headers",
			remoteAddr: "192.168.1.1:8080",
			headers: map[string]string{
				"Cf-Connecting-IP": "203.0.113.99",
				"X-Forwarded-For":  "203.0.113.50",
				"X-Real-IP":        "203.0.113.51",
			},
			trustedHeaders: []string{
				TrustedProxyHeaderCFConnectingIP,
				TrustedProxyHeaderXRealIP,
				TrustedProxyHeaderXForwardedFor,
			},
			wantIP: "192.168.1.1:8080",
		},
		{
			name:       "trusted proxy no headers uses direct IP",
			remoteAddr: "10.1.2.3:8080",
			wantIP:     "10.1.2.3:8080",
		},
		{
			name:           "explicit Cloudflare header from IPv6 loopback proxy",
			remoteAddr:     "[::1]:54321",
			headers:        map[string]string{"Cf-Connecting-IP": "207.188.191.36"},
			trustedHeaders: []string{TrustedProxyHeaderCFConnectingIP},
			wantIP:         "207.188.191.36",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewTrustedProxyChecker(
				[]string{"10.0.0.0/8", "127.0.0.1", "::1"},
				tt.trustedHeaders...,
			)
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			if got := checker.GetClientIP(req); got != tt.wantIP {
				t.Errorf("GetClientIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestTrustedProxyChecker_GetClientIP_RejectsAmbiguousSingleIPHeaders(t *testing.T) {
	headers := []struct {
		name string
		mode string
	}{
		{"Cf-Connecting-IP", TrustedProxyHeaderCFConnectingIP},
		{"X-Real-IP", TrustedProxyHeaderXRealIP},
	}
	values := []struct {
		name   string
		values []string
	}{
		{"malformed", []string{"not-an-ip"}},
		{"comma-separated", []string{"203.0.113.10, 198.51.100.20"}},
		{"duplicated", []string{"203.0.113.10", "198.51.100.20"}},
	}

	for _, header := range headers {
		for _, value := range values {
			t.Run(header.name+"/"+value.name, func(t *testing.T) {
				checker := NewTrustedProxyChecker([]string{"10.0.0.0/8"}, header.mode)
				req := httptest.NewRequest("GET", "/", nil)
				req.RemoteAddr = "10.1.2.3:8080"
				for _, item := range value.values {
					req.Header.Add(header.name, item)
				}

				if got, want := checker.GetClientIP(req), req.RemoteAddr; got != want {
					t.Fatalf("GetClientIP() = %q, want direct peer %q", got, want)
				}
			})
		}
	}
}

func TestTrustedProxyChecker_GetClientIP_XForwardedForStopsAtFirstUntrustedHop(t *testing.T) {
	checker := NewTrustedProxyChecker([]string{"10.0.0.0/8"})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.3:8080"
	// mitto-nxhx: the leftmost value is attacker-controlled; walk from the
	// trusted proxy on the right and stop at the first untrusted hop.
	req.Header.Set("X-Forwarded-For", "127.0.0.1, 198.51.100.25, 10.0.0.2")

	if got, want := checker.GetClientIP(req), "198.51.100.25"; got != want {
		t.Fatalf("GetClientIP() = %q, want first untrusted hop %q", got, want)
	}
}

func TestTrustedProxyChecker_GetClientIP_XForwardedForValidation(t *testing.T) {
	checker := NewTrustedProxyChecker([]string{"10.0.0.0/8"})

	t.Run("multiple field lines preserve right-to-left order", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.3:8080"
		req.Header.Add("X-Forwarded-For", "203.0.113.1")
		req.Header.Add("X-Forwarded-For", "198.51.100.2, 10.0.0.5")

		if got, want := checker.GetClientIP(req), "198.51.100.2"; got != want {
			t.Fatalf("GetClientIP() = %q, want first untrusted hop %q", got, want)
		}
	})

	t.Run("malformed hop fails closed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.3:8080"
		req.Header.Set("X-Forwarded-For", "203.0.113.1, not-an-ip, 10.0.0.2")

		if got, want := checker.GetClientIP(req), req.RemoteAddr; got != want {
			t.Fatalf("GetClientIP() = %q, want direct peer %q", got, want)
		}
	})
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
