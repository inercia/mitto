package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// isolateAppdir points appdir at a throwaway temp directory for the duration of
// a test so that AuthManager session persistence never touches the real user
// data file (honours the "no live settings mutation" constraint of mitto-aha).
func isolateAppdir(t *testing.T) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
}

// trust_boundary_test.go holds isolated, table-driven CHARACTERIZATION tests for
// the localhost-vs-external trust boundary (bead mitto-aha). They document the
// CURRENT behaviour of the auth + CSRF + CORS middleware chain — including the
// known server-side gaps on the internal listener — without asserting any new
// hardening. When the hardening in mitto-aha lands, the cases marked
// "documents gap" below are the ones expected to change.
//
// No live settings are mutated: every case constructs its own AuthManager /
// CSRFManager and drives them through httptest requests.

// newTrustBoundaryChain wires the middleware exactly as production does: CSRF on
// the OUTSIDE, AuthMiddleware on the INSIDE. The returned handler records whether
// the innermost handler was reached.
func newTrustBoundaryChain(t *testing.T, am *AuthManager, cm *CSRFManager, reached *bool) http.Handler {
	t.Helper()
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
	return cm.CSRFMiddleware(am.AuthMiddleware(final))
}

// TestTrustBoundary_InternalListenerSkipsAuthAndCSRF documents that requests on
// the internal (loopback, non-external) listener bypass BOTH authentication and
// CSRF regardless of hostile Origin/Host headers or state-changing methods.
//
// These pass-throughs are the confirmed server-side gaps recorded in mitto-aha:
// the internal listener performs no Host allowlist, no Origin check, and no CSRF
// enforcement. Browser-based exploitability is separately mitigated (SameSite
// cookies, no Access-Control-Allow-Credentials, Local Network Access), but the
// server itself does not enforce these boundaries here.
func TestTrustBoundary_InternalListenerSkipsAuthAndCSRF(t *testing.T) {
	tests := []struct {
		name   string
		method string
		host   string
		origin string
	}{
		{name: "benign GET", method: http.MethodGet},
		{name: "state-changing POST without CSRF token", method: http.MethodPost},
		{name: "hostile Origin ignored (documents gap)", method: http.MethodPost, origin: "https://evil.example"},
		{name: "hostile Host ignored, no DNS-rebinding allowlist (documents gap)", method: http.MethodGet, host: "attacker.example"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateAppdir(t)
			am := NewAuthManager(&config.WebAuth{
				Simple: &config.SimpleAuth{Username: "admin", Password: "password"},
			})
			defer am.Close()
			am.SetAPIPrefix("")
			cm := NewCSRFManager()
			defer cm.Close()

			reached := false
			handler := newTrustBoundaryChain(t, am, cm, &reached)

			req := httptest.NewRequest(tt.method, "/api/sessions", nil)
			req.RemoteAddr = "127.0.0.1:12345" // internal listener client
			if tt.host != "" {
				req.Host = tt.host
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			// NOT marked external: this is the internal listener.

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if !reached {
				t.Fatalf("internal request was blocked (code=%d); current behaviour bypasses auth+CSRF", w.Code)
			}
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}
		})
	}
}

// TestTrustBoundary_ExternalListenerEnforcesAuthAndCSRF documents that the
// external listener enforces authentication and, for cookie-authenticated
// state-changing requests, CSRF — while shared-token bearer clients are
// CSRF-exempt by design (a browser can never attach the Authorization header
// ambiently, so there is no CSRF vector to protect against). A hostile Origin
// on a cookie request is still blocked, but by the CSRF double-submit check —
// NOT by any server-side Origin allowlist (there is none for REST).
func TestTrustBoundary_ExternalListenerEnforcesAuthAndCSRF(t *testing.T) {
	const sharedToken = "s3cr3t-token"

	tests := []struct {
		name        string
		method      string
		origin      string
		withSession bool
		withCSRF    bool
		bearer      string
		wantReached bool
		wantCode    int
	}{
		{name: "no credentials rejected", method: http.MethodGet, wantReached: false, wantCode: http.StatusUnauthorized},
		{name: "cookie POST without CSRF token blocked", method: http.MethodPost, withSession: true, wantReached: false, wantCode: http.StatusForbidden},
		{name: "cookie POST hostile Origin still blocked by CSRF", method: http.MethodPost, withSession: true, origin: "https://evil.example", wantReached: false, wantCode: http.StatusForbidden},
		{name: "cookie POST with valid double-submit CSRF allowed", method: http.MethodPost, withSession: true, withCSRF: true, wantReached: true, wantCode: http.StatusOK},
		{name: "bearer POST without CSRF token allowed (exempt by design)", method: http.MethodPost, bearer: sharedToken, wantReached: true, wantCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateAppdir(t)
			am := NewAuthManager(&config.WebAuth{
				Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
				SharedToken: sharedToken,
			})
			defer am.Close()
			am.SetAPIPrefix("")
			cm := NewCSRFManager()
			defer cm.Close()
			// Wire the bearer-token CSRF exemption exactly as the server does.
			cm.SetTokenAuthChecker(am.ValidateBearerRequest)

			reached := false
			handler := newTrustBoundaryChain(t, am, cm, &reached)

			req := httptest.NewRequest(tt.method, "/api/sessions", nil)
			req.RemoteAddr = "203.0.113.1:54321" // public client
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.withSession {
				session, err := am.CreateSession("admin")
				if err != nil {
					t.Fatalf("CreateSession() error = %v", err)
				}
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
			}
			if tt.withCSRF {
				const csrfVal = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
				req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrfVal})
				req.Header.Set(csrfTokenHeader, csrfVal)
			}
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if reached != tt.wantReached {
				t.Errorf("handler reached = %v, want %v (code=%d)", reached, tt.wantReached, w.Code)
			}
			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// TestTrustBoundary_CORSWildcardNeverGrantsCredentials documents that a wildcard
// CORS allowlist ("*") echoes Access-Control-Allow-Origin: * but NEVER emits
// Access-Control-Allow-Credentials, so an ambient session cookie can never ride
// a cross-origin request in a way that script can read — the wildcard is
// therefore safe here (mitto-7gta.27 invariant, re-verified for mitto-aha).
func TestTrustBoundary_CORSWildcardNeverGrantsCredentials(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"*"}}
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	h := CORSMiddleware(cfg)(next)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("wrapped handler was not reached for a wildcard-allowed request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty (never emitted)", got)
	}
}
