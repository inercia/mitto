package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newCORSTestHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// TestCORSMiddleware_DisabledByDefault confirms an empty allowlist is a
// complete no-op: no CORS headers are ever emitted, even with an Origin
// header present, and the request still reaches the wrapped handler.
func TestCORSMiddleware_DisabledByDefault(t *testing.T) {
	h := CORSMiddleware(DefaultCORSConfig())(newCORSTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://trusted.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want empty", got)
	}
}

// TestCORSMiddleware_NoOriginHeader confirms non-browser / same-origin
// requests (no Origin header) pass through untouched even when an allowlist
// is configured.
func TestCORSMiddleware_NoOriginHeader(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://trusted.com"}}
	h := CORSMiddleware(cfg)(newCORSTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want empty for request with no Origin header", got)
	}
}

// TestCORSMiddleware_AllowedOrigin_SimpleRequest confirms an allowed origin
// on a non-preflight request gets Access-Control-Allow-Origin, Vary: Origin,
// and Expose-Headers, reaches the wrapped handler, and never gets
// Access-Control-Allow-Credentials (mitto-7gta.27 security invariant).
func TestCORSMiddleware_AllowedOrigin_SimpleRequest(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://trusted.com"}}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := CORSMiddleware(cfg)(next)

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://trusted.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("wrapped handler was not called for an allowed simple request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://trusted.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://trusted.com")
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Retry-After" {
		t.Errorf("Access-Control-Expose-Headers = %q, want %q", got, "Retry-After")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty (never emitted)", got)
	}
}

// TestCORSMiddleware_DisallowedOrigin_SimpleRequest confirms a disallowed
// origin is never rejected outright: no Access-Control-Allow-Origin header
// is set, but the request still reaches the wrapped handler.
func TestCORSMiddleware_DisallowedOrigin_SimpleRequest(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://trusted.com"}}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := CORSMiddleware(cfg)(next)

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("wrapped handler was not called for a disallowed simple request (must not be rejected)")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for disallowed origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q even for a disallowed origin", got, "Origin")
	}
}

// TestCORSMiddleware_Preflight_Allowed confirms an allowed preflight
// terminates in this middleware with a 204 and full CORS headers, never
// reaching the wrapped handler (so it runs before CSRF/Auth in the chain).
func TestCORSMiddleware_Preflight_Allowed(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://trusted.com"}}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	h := CORSMiddleware(cfg)(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/x", nil)
	req.Header.Set("Origin", "https://trusted.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("wrapped handler was called for a preflight request; it must terminate in CORSMiddleware")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://trusted.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://trusted.com")
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Access-Control-Allow-Methods missing on allowed preflight")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("Access-Control-Allow-Headers missing on allowed preflight")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Error("Access-Control-Max-Age missing on allowed preflight")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty (never emitted)", got)
	}
}

// TestCORSMiddleware_Preflight_Disallowed confirms a disallowed preflight
// also terminates here (204, no CORS headers) rather than falling through to
// CSRF/Auth, so the browser fails the preflight cleanly.
func TestCORSMiddleware_Preflight_Disallowed(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://trusted.com"}}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	h := CORSMiddleware(cfg)(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/x", nil)
	req.Header.Set("Origin", "https://evil.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("wrapped handler was called for a disallowed preflight; it must terminate in CORSMiddleware")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for disallowed preflight", got)
	}
}

// TestCORSMiddleware_BareOptions_NotPreflight confirms a bare OPTIONS
// request without Access-Control-Request-Method is NOT treated as a
// preflight and falls through to the wrapped handler like any other method.
func TestCORSMiddleware_BareOptions_NotPreflight(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://trusted.com"}}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := CORSMiddleware(cfg)(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/x", nil)
	req.Header.Set("Origin", "https://trusted.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("wrapped handler was not called for a bare OPTIONS request (not a preflight)")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestCORSMiddleware_AllowAllWildcard confirms "*" allows any origin, still
// without ever emitting Access-Control-Allow-Credentials.
func TestCORSMiddleware_AllowAllWildcard(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"*"}}
	h := CORSMiddleware(cfg)(newCORSTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty even with wildcard", got)
	}
}

// TestCORSMiddleware_AllowedOrigin_HostOnlyMatch confirms the host-only
// matching tier (matching just origin URL's host against the allowlist)
// mirrors createOriginChecker's WebSocket behaviour.
func TestCORSMiddleware_AllowedOrigin_HostOnlyMatch(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"trusted.com"}}
	h := CORSMiddleware(cfg)(newCORSTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://trusted.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://trusted.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q (host-only allowlist entry should match)", got, "https://trusted.com")
	}
}
