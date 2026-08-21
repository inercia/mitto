package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// makeExternalRequest marks a request as coming from the external listener.
// This is needed because CSRF protection only applies to external connections.
func makeExternalRequest(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), ContextKeyExternalConnection, true)
	return r.WithContext(ctx)
}

func TestCSRFManager_GenerateToken(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	token, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	if token == "" {
		t.Error("GenerateToken returned empty token")
	}

	// Token should be 64 hex characters (32 bytes)
	if len(token) != 64 {
		t.Errorf("Token length = %d, want 64", len(token))
	}

	// Each generated token should be unique
	token2, _ := cm.GenerateToken()
	if token == token2 {
		t.Error("GenerateToken returned duplicate tokens")
	}
}

func TestCSRFManager_HandleCSRFToken(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/csrf-token", nil)
	w := httptest.NewRecorder()

	cm.HandleCSRFToken(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HandleCSRFToken status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Should have set the CSRF cookie
	cookies := resp.Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == csrfCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Error("CSRF cookie not set")
	}

	// Should return method not allowed for non-GET
	reqPost := httptest.NewRequest(http.MethodPost, "/api/csrf-token", nil)
	wPost := httptest.NewRecorder()
	cm.HandleCSRFToken(wPost, reqPost)
	if wPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleCSRFToken POST status = %d, want %d", wPost.Code, http.StatusMethodNotAllowed)
	}
}

func TestCSRFMiddleware_InternalConnectionsBypass(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Internal connections (not marked as external) should bypass CSRF entirely
	stateChangingMethods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range stateChangingMethods {
		req := httptest.NewRequest(method, "/api/test", nil)
		// NOT marked as external - should pass without CSRF token
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Internal %s without token: status = %d, want %d", method, w.Code, http.StatusOK)
		}
	}
}

func TestCSRFMiddleware_SafeMethods(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Safe methods should pass without CSRF token even for external connections
	safeMethods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, method := range safeMethods {
		req := makeExternalRequest(httptest.NewRequest(method, "/api/test", nil))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("External %s request without token: status = %d, want %d", method, w.Code, http.StatusOK)
		}
	}
}

func TestCSRFMiddleware_StateChangingMethods(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// State-changing methods on external connections should require CSRF token
	stateChangingMethods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range stateChangingMethods {
		// External connection without token - should fail
		req := makeExternalRequest(httptest.NewRequest(method, "/api/test", nil))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("External %s without token: status = %d, want %d", method, w.Code, http.StatusForbidden)
		}
	}
}

func TestCSRFMiddleware_ValidToken(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Generate a token
	token, _ := cm.GenerateToken()

	// External POST with matching header and cookie should succeed (double-submit pattern)
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, token)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("External POST with matching header and cookie: status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCSRFMiddleware_MissingCookie(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// External POST with header but no cookie should fail
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, "some-token")
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("External POST with header but no cookie: status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCSRFMiddleware_MissingHeader(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// External POST with cookie but no header should fail
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "some-token"})
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("External POST with cookie but no header: status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCSRFMiddleware_ExemptPaths(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	// Set API prefix (as used in production)
	cm.SetAPIPrefix("/mitto")

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Login is exempt - should pass without token even for external connections
	req := makeExternalRequest(httptest.NewRequest(http.MethodPost, "/mitto/api/login", nil))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("POST /mitto/api/login without token: status = %d, want %d", w.Code, http.StatusOK)
	}

	// External request to login without prefix should NOT be exempt when API prefix is set
	req = makeExternalRequest(httptest.NewRequest(http.MethodPost, "/api/login", nil))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("External POST /api/login without token (missing prefix): status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCSRFMiddleware_WebSocketUpgrade(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// WebSocket upgrade requests should be exempt even for external connections
	req := makeExternalRequest(httptest.NewRequest(http.MethodGet, "/ws", nil))
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("External WebSocket upgrade: status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCSRFMiddleware_DoubleSubmitPattern(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Generate a valid token
	token, _ := cm.GenerateToken()

	// External request with matching header and cookie should succeed
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, token)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("External double-submit with matching tokens: status = %d, want %d", w.Code, http.StatusOK)
	}

	// External request with mismatching cookie should fail
	req2 := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req2.Header.Set(csrfTokenHeader, token)
	req2.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "different-token"})
	req2 = makeExternalRequest(req2)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("External double-submit with mismatching tokens: status = %d, want %d", w2.Code, http.StatusForbidden)
	}
}

// --- Shared bearer token CSRF exemption tests (mitto-7gta.26) ---

func TestCSRFMiddleware_TokenAuthChecker_ValidTokenBypassesCSRF(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()
	cm.SetTokenAuthChecker(func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer valid-token"
	})

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// A state-changing external request with a valid bearer token and NO
	// CSRF cookie/header pair must still succeed.
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("valid bearer token: status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCSRFMiddleware_TokenAuthChecker_InvalidTokenStillEnforcesCSRF(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()
	cm.SetTokenAuthChecker(func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer valid-token"
	})

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// An invalid/garbage bearer token must NOT bypass CSRF -- this is the
	// critical "validate, don't just detect the header" property. Without a
	// matching double-submit cookie/header pair, the request must be rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("invalid bearer token: status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCSRFMiddleware_TokenAuthChecker_NilCheckerPreservesExistingBehaviour(t *testing.T) {
	// No SetTokenAuthChecker call at all -- the default nil checker must not
	// change any existing double-submit-cookie behaviour.
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer anything")
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("nil tokenAuthChecker with bearer header only: status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCSRFMiddleware_TokenAuthChecker_AbsentHeaderFallsThroughToCookieCheck(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()
	cm.SetTokenAuthChecker(func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer valid-token"
	})

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No Authorization header at all: checker returns false, falls through to
	// the normal double-submit cookie pattern, which succeeds when it matches.
	token, _ := cm.GenerateToken()
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, token)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req = makeExternalRequest(req)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("absent bearer header with matching CSRF cookie/header: status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCSRFManager_SetTokenAuthChecker(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	if cm.tokenAuthChecker != nil {
		t.Error("tokenAuthChecker should be nil by default")
	}

	called := false
	cm.SetTokenAuthChecker(func(r *http.Request) bool {
		called = true
		return true
	})
	if cm.tokenAuthChecker == nil {
		t.Fatal("tokenAuthChecker should be set after SetTokenAuthChecker")
	}
	cm.tokenAuthChecker(httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Error("installed checker was not invoked")
	}

	// Passing nil disables the exemption again.
	cm.SetTokenAuthChecker(nil)
	if cm.tokenAuthChecker != nil {
		t.Error("tokenAuthChecker should be nil after SetTokenAuthChecker(nil)")
	}
}

func TestCSRFManager_Close(t *testing.T) {
	cm := NewCSRFManager()

	// Close should not panic
	cm.Close()

	// Close again should not panic (idempotent)
	cm.Close()
}

func TestCSRFManager_GetTokenFromRequest(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	// Test with header
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, "header-token")

	token := cm.GetTokenFromRequest(req)
	if token != "header-token" {
		t.Errorf("GetTokenFromRequest = %q, want %q", token, "header-token")
	}

	// Test with cookie (no header)
	req2 := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req2.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "cookie-token"})

	token2 := cm.GetTokenFromRequest(req2)
	if token2 != "cookie-token" {
		t.Errorf("GetTokenFromRequest = %q, want %q", token2, "cookie-token")
	}

	// Test with neither header nor cookie
	req3 := httptest.NewRequest(http.MethodPost, "/api/test", nil)

	token3 := cm.GetTokenFromRequest(req3)
	if token3 != "" {
		t.Errorf("GetTokenFromRequest = %q, want empty", token3)
	}

	// Test header takes precedence over cookie
	req4 := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req4.Header.Set(csrfTokenHeader, "header-token")
	req4.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "cookie-token"})

	token4 := cm.GetTokenFromRequest(req4)
	if token4 != "header-token" {
		t.Errorf("GetTokenFromRequest = %q, want %q (header takes precedence)", token4, "header-token")
	}
}

func TestCSRFManager_SetAPIPrefix(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	cm.SetAPIPrefix("/api/v1")

	if cm.apiPrefix != "/api/v1" {
		t.Errorf("apiPrefix = %q, want %q", cm.apiPrefix, "/api/v1")
	}
}

func TestCSRFManager_CloseIsNoOp(t *testing.T) {
	cm := NewCSRFManager()

	// Close should not panic and should be idempotent
	cm.Close()
	cm.Close()
	cm.Close()

	// Manager should still work after close (stateless)
	token, err := cm.GenerateToken()
	if err != nil {
		t.Errorf("GenerateToken after Close failed: %v", err)
	}
	if token == "" {
		t.Error("GenerateToken after Close returned empty token")
	}
}

func TestNormalizeIPForFingerprint(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want string
	}{
		{"IPv4 masks to /24", "192.168.1.42", "192.168.1.0"},
		{"IPv4 masks to /24 (different host)", "192.168.1.200", "192.168.1.0"},
		{"IPv6 masks to /64", "2001:db8:1234:5678:aaaa:bbbb:cccc:dddd", "2001:db8:1234:5678::"},
		{"empty returns unchanged", "", ""},
		{"unparseable returns unchanged", "not-an-ip", "not-an-ip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeIPForFingerprint(tt.ip)
			if got != tt.want {
				t.Errorf("normalizeIPForFingerprint(%q) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

func TestVerifyIPFromToken_SameNetworkSameUA(t *testing.T) {
	const ua = "Mozilla/5.0 (iPhone)"

	// Use a base of csrfTokenLength*2 hex chars to exercise the real
	// comparison path (shorter bases hit the graceful-degradation guard).
	realBase := make([]byte, csrfTokenLength*2)
	for i := range realBase {
		realBase[i] = '0'
	}
	token := embedFingerprint(string(realBase), "192.168.1.10", ua)

	// Different IP in the same /24, same UA -> no anomaly.
	if !VerifyIPFromToken(token, "192.168.1.250", ua) {
		t.Error("expected true for different IP in same /24 with same UA")
	}
}

func TestVerifyIPFromToken_DifferentNetwork(t *testing.T) {
	const ua = "Mozilla/5.0 (iPhone)"

	// Use a base of csrfTokenLength*2 hex chars to exercise the real
	// comparison path (shorter bases hit the graceful-degradation guard).
	realBase := make([]byte, csrfTokenLength*2)
	for i := range realBase {
		realBase[i] = '0'
	}
	token := embedFingerprint(string(realBase), "192.168.1.10", ua)

	// Different /24 network, same UA -> anomaly (fingerprint mismatch).
	if VerifyIPFromToken(token, "192.168.2.10", ua) {
		t.Error("expected false for different /24 network with same UA")
	}
}

func TestVerifyIPFromToken_DifferentUserAgent(t *testing.T) {
	realBase := make([]byte, csrfTokenLength*2)
	for i := range realBase {
		realBase[i] = '0'
	}
	token := embedFingerprint(string(realBase), "192.168.1.10", "UA-A")

	if VerifyIPFromToken(token, "192.168.1.10", "UA-B") {
		t.Error("expected false for same IP with different User-Agent")
	}
}

func TestVerifyIPFromToken_GracefulDegradation(t *testing.T) {
	// Old-format token with no embedded fingerprint.
	if !VerifyIPFromToken("plain-token-no-suffix", "1.2.3.4", "UA") {
		t.Error("expected true (no anomaly) for token without embedded fingerprint")
	}

	// Token with a suffix but an unexpected base length.
	if !VerifyIPFromToken("shortbase.deadbeefdeadbeef", "1.2.3.4", "UA") {
		t.Error("expected true (no anomaly) for token with unexpected base length")
	}
}

func TestVerifyIPFromToken_RoundTrip(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	token, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	const ua = "curl/8.0"
	tokenWithFingerprint := embedFingerprint(token, "10.0.0.5", ua)

	// Same network prefix (/24), same UA -> verified.
	if !VerifyIPFromToken(tokenWithFingerprint, "10.0.0.99", ua) {
		t.Error("expected true for round-trip within same /24 and same UA")
	}

	// Different network prefix -> mismatch.
	if VerifyIPFromToken(tokenWithFingerprint, "10.0.1.5", ua) {
		t.Error("expected false for round-trip with different /24")
	}

	// Different UA -> mismatch.
	if VerifyIPFromToken(tokenWithFingerprint, "10.0.0.5", "different-ua") {
		t.Error("expected false for round-trip with different User-Agent")
	}
}
