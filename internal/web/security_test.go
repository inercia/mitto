package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	config := DefaultSecurityConfig()

	handler := securityHeadersMiddleware(config)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	tests := []struct {
		header   string
		expected string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"X-XSS-Protection", "1; mode=block"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			got := w.Header().Get(tt.header)
			if got != tt.expected {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.expected)
			}
		})
	}

	// Note: CSP is now handled by cspNonceMiddleware, not securityHeadersMiddleware.
	// See TestCSPNonceMiddleware_* tests for CSP verification.

	// HSTS should NOT be set by default
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS should not be set by default")
	}
}

func TestSecurityHeadersMiddleware_WithHSTS(t *testing.T) {
	config := SecurityConfig{
		EnableHSTS: true,
		HSTSMaxAge: 3600,
	}

	handler := securityHeadersMiddleware(config)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts == "" {
		t.Error("HSTS header should be set when enabled")
	}
	if !strings.Contains(hsts, "max-age=3600") {
		t.Errorf("HSTS = %q, should contain max-age=3600", hsts)
	}
}

func TestRequestSizeLimitMiddleware(t *testing.T) {
	maxBytes := int64(100)

	handler := requestSizeLimitMiddleware(maxBytes)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read the body
		buf := make([]byte, 200)
		_, err := r.Body.Read(buf)
		if err != nil && err.Error() != "http: request body too large" {
			// Body was limited
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	// POST request with body larger than limit
	largeBody := strings.Repeat("x", 200)
	req := httptest.NewRequest("POST", "/", strings.NewReader(largeBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// The handler should have received a limited body
	// (actual behavior depends on how the handler reads the body)
}

func TestHideServerInfoMiddleware(t *testing.T) {
	handler := hideServerInfoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to set server headers
		w.Header().Set("Server", "MyServer/1.0")
		w.Header().Set("X-Powered-By", "Go")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Header().Get("Server") != "" {
		t.Error("Server header should be removed")
	}
	if w.Header().Get("X-Powered-By") != "" {
		t.Error("X-Powered-By header should be removed")
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{123, "123"},
		{31536000, "31536000"},
		{-5, "-5"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := itoa(tt.input)
			if got != tt.expected {
				t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestHideServerInfoResponseWriter_Write(t *testing.T) {
	handler := hideServerInfoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write without calling WriteHeader first
		w.Write([]byte("Hello, World!"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should have written the body
	if w.Body.String() != "Hello, World!" {
		t.Errorf("Body = %q, want %q", w.Body.String(), "Hello, World!")
	}

	// Should have set status to 200 OK
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRequestTimeoutMiddleware(t *testing.T) {
	handler := requestTimeoutMiddleware(DefaultRequestTimeout)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRequestTimeoutMiddleware_WebSocketExcluded(t *testing.T) {
	handler := requestTimeoutMiddleware(DefaultRequestTimeout)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// WebSocket upgrade request should bypass timeout
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}
