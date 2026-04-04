package web

import (
	"net/http/httptest"
	"testing"
)

func TestOriginChecker_SameOrigin(t *testing.T) {
	checker := createOriginChecker(nil, nil, nil) // nil = same-origin only

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
	checker := createOriginChecker(allowedOrigins, nil, nil)

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
	checker := createOriginChecker([]string{"*"}, nil, nil)

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


