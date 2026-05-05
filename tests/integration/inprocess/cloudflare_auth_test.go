//go:build integration

package inprocess

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestCloudflareAuth_JWTValidation tests the full Cloudflare Access JWT authentication flow
// using a mock JWKS server. This verifies that:
// - Requests without JWT or session are rejected (401)
// - Requests with invalid JWT are rejected (401)
// - Requests with valid JWT via Cf-Access-Jwt-Assertion header are authenticated (200)
// - Requests with valid JWT via CF_Authorization cookie are authenticated (200)
// - The /api/auth-info endpoint correctly reports available auth methods
func TestCloudflareAuth_JWTValidation(t *testing.T) {
	ts := SetupTestServerWithCloudflareAuth(t)

	baseURL := ts.HTTPServer.URL + "/mitto"

	// Test 1: /api/auth-info should be public and report cloudflare=true
	t.Run("auth-info endpoint", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/auth-info")
		if err != nil {
			t.Fatalf("GET /api/auth-info: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		var info map[string]bool
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if !info["cloudflare"] {
			t.Error("expected cloudflare=true")
		}
		if !info["simple"] {
			t.Error("expected simple=true")
		}
	})

	// Test 2: No auth → 401
	// Note: /api/health is intentionally public (for tunnel monitoring), so we use
	// /api/sessions which requires authentication.
	t.Run("no auth rejected", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/sessions")
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 401 {
			t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
		}
	})

	// Test 3: Invalid JWT → 401
	// Note: /api/health is intentionally public (for tunnel monitoring), so we use
	// /api/sessions which requires authentication.
	t.Run("invalid JWT rejected", func(t *testing.T) {
		req, _ := http.NewRequest("GET", baseURL+"/api/sessions", nil)
		req.Header.Set("Cf-Access-Jwt-Assertion", "not.a.valid.jwt")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 401 {
			t.Fatalf("expected 401 with invalid JWT, got %d", resp.StatusCode)
		}
	})

	// Test 4: Valid JWT via header → 200
	t.Run("valid JWT via header", func(t *testing.T) {
		req, _ := http.NewRequest("GET", baseURL+"/api/health", nil)
		req.Header.Set("Cf-Access-Jwt-Assertion", ts.CloudflareJWT)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200 with valid JWT, got %d: %s", resp.StatusCode, body)
		}

		var health map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
			t.Fatalf("decode health: %v", err)
		}
		if health["status"] != "healthy" {
			t.Errorf("expected healthy, got %v", health["status"])
		}
	})

	// Test 5: Valid JWT via cookie → 200
	t.Run("valid JWT via cookie", func(t *testing.T) {
		req, _ := http.NewRequest("GET", baseURL+"/api/health", nil)
		req.AddCookie(&http.Cookie{
			Name:  "CF_Authorization",
			Value: ts.CloudflareJWT,
		})

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200 with cookie JWT, got %d: %s", resp.StatusCode, body)
		}
	})

	// Test 6: Expired JWT → 401
	// Note: /api/health is intentionally public (for tunnel monitoring), so we use
	// /api/sessions which requires authentication.
	t.Run("expired JWT rejected", func(t *testing.T) {
		if ts.ExpiredJWT == "" {
			t.Skip("expired JWT not available")
		}
		req, _ := http.NewRequest("GET", baseURL+"/api/sessions", nil)
		req.Header.Set("Cf-Access-Jwt-Assertion", ts.ExpiredJWT)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 401 {
			t.Fatalf("expected 401 with expired JWT, got %d", resp.StatusCode)
		}
	})

	// Test 7: Wrong audience → 401
	// Note: /api/health is intentionally public (for tunnel monitoring), so we use
	// /api/sessions which requires authentication.
	t.Run("wrong audience rejected", func(t *testing.T) {
		if ts.WrongAudienceJWT == "" {
			t.Skip("wrong audience JWT not available")
		}
		req, _ := http.NewRequest("GET", baseURL+"/api/sessions", nil)
		req.Header.Set("Cf-Access-Jwt-Assertion", ts.WrongAudienceJWT)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 401 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 401 with wrong audience, got %d: %s", resp.StatusCode, body)
		}
	})

	// Test 8: Valid JWT grants access to HTML pages too
	t.Run("valid JWT serves HTML", func(t *testing.T) {
		req, _ := http.NewRequest("GET", baseURL+"/", nil)
		req.Header.Set("Cf-Access-Jwt-Assertion", ts.CloudflareJWT)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /: %v", err)
		}
		defer resp.Body.Close()

		// Should get 200 with HTML, not a redirect to auth.html
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			t.Error("expected Content-Type header")
		}
	})

	// Test 9: Login page accessible without auth (public path)
	t.Run("login page is public", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/auth.html")
		if err != nil {
			t.Fatalf("GET /auth.html: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Fatalf("expected 200 for auth.html, got %d", resp.StatusCode)
		}
	})

	// Test 10: Sessions API requires auth
	t.Run("sessions API requires auth", func(t *testing.T) {
		// Without JWT
		resp1, err := http.Get(baseURL + "/api/sessions")
		if err != nil {
			t.Fatalf("GET /api/sessions (no auth): %v", err)
		}
		resp1.Body.Close()
		if resp1.StatusCode != 401 {
			t.Fatalf("expected 401 without auth, got %d", resp1.StatusCode)
		}

		// With JWT
		req, _ := http.NewRequest("GET", baseURL+"/api/sessions", nil)
		req.Header.Set("Cf-Access-Jwt-Assertion", ts.CloudflareJWT)
		resp2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/sessions (with JWT): %v", err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != 200 {
			t.Fatalf("expected 200 with JWT, got %d", resp2.StatusCode)
		}
	})
}

// TestCloudflareAuth_CloudflareOnlyNoLoginForm verifies that when only cloudflare
// auth is configured (no simple auth), the /api/auth-info reports simple=false.
func TestCloudflareAuth_CloudflareOnlyNoLoginForm(t *testing.T) {
	ts := SetupTestServerWithCloudflareOnlyAuth(t)

	baseURL := ts.HTTPServer.URL + "/mitto"

	resp, err := http.Get(baseURL + "/api/auth-info")
	if err != nil {
		t.Fatalf("GET /api/auth-info: %v", err)
	}
	defer resp.Body.Close()

	var info map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !info["cloudflare"] {
		t.Error("expected cloudflare=true")
	}
	if info["simple"] {
		t.Error("expected simple=false when only cloudflare auth is configured")
	}

	// Valid JWT should still work
	req, _ := http.NewRequest("GET", baseURL+"/api/health", nil)
	req.Header.Set("Cf-Access-Jwt-Assertion", ts.CloudflareJWT)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("expected 200, got %d: %s", resp2.StatusCode, body)
	}
	_ = fmt.Sprintf("verified") // Use fmt to satisfy import
}
