package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateCSPNonce(t *testing.T) {
	// Generate multiple nonces and verify they're unique and properly formatted
	nonces := make(map[string]bool)
	for i := 0; i < 100; i++ {
		nonce, err := generateCSPNonce()
		if err != nil {
			t.Fatalf("generateCSPNonce() error = %v", err)
		}

		// Nonce should be base64 encoded (24 chars for 16 bytes)
		if len(nonce) != 24 {
			t.Errorf("generateCSPNonce() length = %d, want 24", len(nonce))
		}

		// Nonce should be unique
		if nonces[nonce] {
			t.Errorf("generateCSPNonce() generated duplicate nonce: %s", nonce)
		}
		nonces[nonce] = true
	}
}

func TestCSPNonceMiddleware_HTMLResponse(t *testing.T) {
	// Create a handler that returns HTML with nonce placeholders
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <script nonce="{{CSP_NONCE}}" src="test.js"></script>
</head>
<body>Hello</body>
</html>`))
	})

	// Wrap with CSP nonce middleware
	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{config: DefaultSecurityConfig()})(handler)

	// Make a request
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Check response
	body := rec.Body.String()

	// Nonce placeholder should be replaced
	if strings.Contains(body, "{{CSP_NONCE}}") {
		t.Error("Response still contains nonce placeholder")
	}

	// Should contain a nonce attribute
	if !strings.Contains(body, `nonce="`) {
		t.Error("Response does not contain nonce attribute")
	}

	// CSP header should be set with nonce
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Content-Security-Policy header not set")
	}
	if !strings.Contains(csp, "'nonce-") {
		t.Errorf("CSP header does not contain nonce: %s", csp)
	}
	// script-src should not contain 'unsafe-inline' (style-src still does, which is OK)
	if strings.Contains(csp, "script-src") && strings.Contains(csp, "'unsafe-inline'") {
		// Extract script-src directive to check
		parts := strings.Split(csp, ";")
		for _, part := range parts {
			if strings.Contains(part, "script-src") && strings.Contains(part, "'unsafe-inline'") {
				t.Errorf("script-src contains 'unsafe-inline': %s", part)
			}
		}
	}
}

func TestCSPNonceMiddleware_NonHTMLResponse(t *testing.T) {
	// Create a handler that returns JSON
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "ok"}`))
	})

	// Wrap with CSP nonce middleware
	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{config: DefaultSecurityConfig()})(handler)

	// Make a request
	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Check response body is unchanged
	body := rec.Body.String()
	if body != `{"status": "ok"}` {
		t.Errorf("Response body modified: %s", body)
	}

	// CSP header should be set without nonce
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Content-Security-Policy header not set")
	}
	if strings.Contains(csp, "'nonce-") {
		t.Errorf("CSP header contains nonce for non-HTML response: %s", csp)
	}
	// script-src should not contain 'unsafe-inline'
	parts := strings.Split(csp, ";")
	for _, part := range parts {
		if strings.Contains(part, "script-src") && strings.Contains(part, "'unsafe-inline'") {
			t.Errorf("script-src contains 'unsafe-inline': %s", part)
		}
	}
}

func TestCSPNonceMiddleware_MultipleNoncePlaceholders(t *testing.T) {
	// Create a handler that returns HTML with multiple nonce placeholders
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<script nonce="{{CSP_NONCE}}" src="a.js"></script>
<script nonce="{{CSP_NONCE}}" src="b.js"></script>
<script nonce="{{CSP_NONCE}}" src="c.js"></script>`))
	})

	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{config: DefaultSecurityConfig()})(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	body := rec.Body.String()

	// All placeholders should be replaced with the same nonce
	if strings.Contains(body, "{{CSP_NONCE}}") {
		t.Error("Response still contains nonce placeholder")
	}

	// Count nonce occurrences - should be 3
	count := strings.Count(body, `nonce="`)
	if count != 3 {
		t.Errorf("Expected 3 nonce attributes, got %d", count)
	}
}

// TestCSPNonceMiddleware_APIPrefixInjection verifies that {{API_PREFIX}} is replaced.
func TestCSPNonceMiddleware_APIPrefixInjection(t *testing.T) {
	tests := []struct {
		name       string
		apiPrefix  string
		wantPrefix string
	}{
		{
			name:       "default prefix /mitto",
			apiPrefix:  "/mitto",
			wantPrefix: "/mitto",
		},
		{
			name:       "custom prefix /custom",
			apiPrefix:  "/custom",
			wantPrefix: "/custom",
		},
		{
			name:       "empty prefix",
			apiPrefix:  "",
			wantPrefix: "",
		},
		{
			name:       "prefix with trailing slash",
			apiPrefix:  "/api/",
			wantPrefix: "/api/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(`<script>window.mittoApiPrefix = "{{API_PREFIX}}";</script>`))
			})

			opts := cspNonceMiddlewareOptions{
				config:    DefaultSecurityConfig(),
				apiPrefix: tt.apiPrefix,
			}
			wrapped := cspNonceMiddlewareWithOptions(opts)(handler)

			req := httptest.NewRequest("GET", "/", nil)
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			body := rec.Body.String()

			// Placeholder should be replaced
			if strings.Contains(body, "{{API_PREFIX}}") {
				t.Error("Response still contains API_PREFIX placeholder")
			}

			// Should contain the expected prefix value
			expected := `window.mittoApiPrefix = "` + tt.wantPrefix + `"`
			if !strings.Contains(body, expected) {
				t.Errorf("body = %q, want to contain %q", body, expected)
			}
		})
	}
}

// TestCSPNonceMiddleware_BothPlaceholders verifies CSP_NONCE, API_PREFIX, and IS_EXTERNAL are replaced.
func TestCSPNonceMiddleware_BothPlaceholders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <script nonce="{{CSP_NONCE}}">window.mittoApiPrefix = "{{API_PREFIX}}"; window.mittoIsExternal = {{IS_EXTERNAL}};</script>
    <script nonce="{{CSP_NONCE}}" src="./app.js"></script>
</head>
<body>Test</body>
</html>`))
	})

	opts := cspNonceMiddlewareOptions{
		config:    DefaultSecurityConfig(),
		apiPrefix: "/mitto",
	}
	wrapped := cspNonceMiddlewareWithOptions(opts)(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	body := rec.Body.String()

	// No placeholders should remain
	if strings.Contains(body, "{{CSP_NONCE}}") {
		t.Error("Response still contains CSP_NONCE placeholder")
	}
	if strings.Contains(body, "{{API_PREFIX}}") {
		t.Error("Response still contains API_PREFIX placeholder")
	}
	if strings.Contains(body, "{{IS_EXTERNAL}}") {
		t.Error("Response still contains IS_EXTERNAL placeholder")
	}

	// Should contain the API prefix
	if !strings.Contains(body, `window.mittoApiPrefix = "/mitto"`) {
		t.Errorf("body does not contain expected API prefix assignment")
	}

	// Should contain isExternal = false (not an external connection by default)
	if !strings.Contains(body, `window.mittoIsExternal = false`) {
		t.Errorf("body does not contain expected isExternal = false assignment")
	}

	// Should contain nonce attributes
	if !strings.Contains(body, `nonce="`) {
		t.Error("body does not contain nonce attributes")
	}

	// CSP header should be set
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Content-Security-Policy header not set")
	}
}

// TestCSPNonceMiddleware_ExternalConnection verifies IS_EXTERNAL is set to true for external connections.
func TestCSPNonceMiddleware_ExternalConnection(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <script nonce="{{CSP_NONCE}}">window.mittoIsExternal = {{IS_EXTERNAL}};</script>
</head>
<body>Test</body>
</html>`))
	})

	opts := cspNonceMiddlewareOptions{
		config:    DefaultSecurityConfig(),
		apiPrefix: "/mitto",
	}
	wrapped := cspNonceMiddlewareWithOptions(opts)(handler)

	// Create a request with external connection context
	req := httptest.NewRequest("GET", "/", nil)
	ctx := context.WithValue(req.Context(), ContextKeyExternalConnection, true)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Should contain isExternal = true
	if !strings.Contains(body, `window.mittoIsExternal = true`) {
		t.Errorf("body does not contain expected isExternal = true assignment, got: %s", body)
	}

	// Placeholder should not remain
	if strings.Contains(body, "{{IS_EXTERNAL}}") {
		t.Error("Response still contains IS_EXTERNAL placeholder")
	}
}

// TestCSPNonceMiddleware_NonHTMLDoesNotReplacePrefix verifies non-HTML responses are unchanged.
func TestCSPNonceMiddleware_NonHTMLDoesNotReplacePrefix(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		// This shouldn't happen in practice, but test the behavior
		w.Write([]byte(`const prefix = "{{API_PREFIX}}";`))
	})

	opts := cspNonceMiddlewareOptions{
		config:    DefaultSecurityConfig(),
		apiPrefix: "/mitto",
	}
	wrapped := cspNonceMiddlewareWithOptions(opts)(handler)

	req := httptest.NewRequest("GET", "/app.js", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Non-HTML responses should NOT have placeholders replaced
	// (they're served as-is, which is correct since JS files shouldn't have placeholders)
	if !strings.Contains(body, "{{API_PREFIX}}") {
		t.Error("Non-HTML response should not have placeholders replaced")
	}
}

// TestCSPNonceMiddleware_ContentLengthUpdated verifies Content-Length is updated after replacement.
func TestCSPNonceMiddleware_ContentLengthUpdated(t *testing.T) {
	originalContent := `<script nonce="{{CSP_NONCE}}">x</script>`

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", itoa(len(originalContent)))
		w.Write([]byte(originalContent))
	})

	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{config: DefaultSecurityConfig()})(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	body := rec.Body.String()
	contentLength := rec.Header().Get("Content-Length")

	// Content-Length should match actual body length
	if contentLength != "" {
		expectedLen := itoa(len(body))
		if contentLength != expectedLen {
			t.Errorf("Content-Length = %s, want %s (body len = %d)", contentLength, expectedLen, len(body))
		}
	}
}

func TestCSPNonceMiddleware_WriteHeaderHTML(t *testing.T) {
	// Create a handler that explicitly calls WriteHeader for HTML
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body>Test</body></html>`))
	})

	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{config: DefaultSecurityConfig()})(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Should have CSP header
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Expected Content-Security-Policy header")
	}
}

func TestCSPNonceMiddleware_WriteHeaderNonHTML(t *testing.T) {
	// Create a handler that explicitly calls WriteHeader for non-HTML
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{config: DefaultSecurityConfig()})(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestCSPNonceMiddleware_ExternalImagesDisabled(t *testing.T) {
	// Create a handler that returns HTML
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><body>Test</body></html>`))
	})

	// Test with external images disabled (default)
	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{
		config:              DefaultSecurityConfig(),
		allowExternalImages: false,
	})(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header not set")
	}

	// Should contain img-src without https:
	if !strings.Contains(csp, "img-src 'self' data: blob:") {
		t.Errorf("CSP should contain 'img-src 'self' data: blob:', got: %s", csp)
	}
	// Should NOT contain https: in img-src
	if strings.Contains(csp, "img-src 'self' data: blob: https:") {
		t.Errorf("CSP should NOT contain https: in img-src when external images disabled, got: %s", csp)
	}
}

func TestCSPNonceMiddleware_ExternalImagesEnabled(t *testing.T) {
	// Create a handler that returns HTML
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><body>Test</body></html>`))
	})

	// Test with external images enabled
	wrapped := cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{
		config:              DefaultSecurityConfig(),
		allowExternalImages: true,
	})(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header not set")
	}

	// Should contain img-src with https:
	if !strings.Contains(csp, "img-src 'self' data: blob: https:") {
		t.Errorf("CSP should contain 'img-src 'self' data: blob: https:', got: %s", csp)
	}
}
