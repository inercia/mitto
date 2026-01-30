package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
}

func TestCSRFManager_ValidateToken(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	// Generate a valid token
	token, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	// Should validate successfully
	if !cm.ValidateToken(token) {
		t.Error("ValidateToken returned false for valid token")
	}

	// Should fail for invalid token
	if cm.ValidateToken("invalid-token") {
		t.Error("ValidateToken returned true for invalid token")
	}

	// Should fail for empty token
	if cm.ValidateToken("") {
		t.Error("ValidateToken returned true for empty token")
	}
}

func TestCSRFManager_ValidateTokenConstantTime(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	token, _ := cm.GenerateToken()

	if !cm.ValidateTokenConstantTime(token) {
		t.Error("ValidateTokenConstantTime returned false for valid token")
	}

	if cm.ValidateTokenConstantTime("wrong-token") {
		t.Error("ValidateTokenConstantTime returned true for invalid token")
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

func TestCSRFMiddleware_SafeMethods(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Safe methods should pass without CSRF token
	safeMethods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, method := range safeMethods {
		req := httptest.NewRequest(method, "/api/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s request without token: status = %d, want %d", method, w.Code, http.StatusOK)
		}
	}
}

func TestCSRFMiddleware_StateChangingMethods(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// State-changing methods should require CSRF token
	stateChangingMethods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range stateChangingMethods {
		// Without token - should fail
		req := httptest.NewRequest(method, "/api/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s without token: status = %d, want %d", method, w.Code, http.StatusForbidden)
		}
	}
}

func TestCSRFMiddleware_ValidToken(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Generate a valid token
	token, _ := cm.GenerateToken()

	// POST with valid token in header should succeed
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("POST with valid token: status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCSRFMiddleware_InvalidToken(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// POST with invalid token should fail
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, "invalid-token-12345")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("POST with invalid token: status = %d, want %d", w.Code, http.StatusForbidden)
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

	// Login is exempt - should pass without token (with API prefix)
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/login", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("POST /mitto/api/login without token: status = %d, want %d", w.Code, http.StatusOK)
	}

	// Login without prefix should NOT be exempt when API prefix is set
	req = httptest.NewRequest(http.MethodPost, "/api/login", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("POST /api/login without token (missing prefix): status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCSRFMiddleware_WebSocketUpgrade(t *testing.T) {
	cm := NewCSRFManager()
	defer cm.Close()

	handler := cm.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// WebSocket upgrade requests should be exempt
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("WebSocket upgrade: status = %d, want %d", w.Code, http.StatusOK)
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

	// With matching header and cookie should succeed
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set(csrfTokenHeader, token)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Double-submit with matching tokens: status = %d, want %d", w.Code, http.StatusOK)
	}

	// With mismatching cookie should fail
	req2 := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req2.Header.Set(csrfTokenHeader, token)
	req2.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "different-token"})
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("Double-submit with mismatching tokens: status = %d, want %d", w2.Code, http.StatusForbidden)
	}
}

