package web

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
