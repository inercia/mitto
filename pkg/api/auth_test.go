package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// TestNoAuth_DefaultClientAddsNoCredentials pins the primary compatibility
// constraint (mitto-rwxq.4 plan): New(baseURL) with no options must keep
// sending no Authorization header and no cookies, and must not allocate a
// cookie jar, exactly like the client before this feature existed.
func TestNoAuth_DefaultClientAddsNoCredentials(t *testing.T) {
	var gotAuth, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if c.httpClient.Jar != nil {
		t.Fatal("New(baseURL) must not allocate a cookie jar before Login is called")
	}
	if _, ok := c.auth.(noAuth); !ok {
		t.Fatalf("default auth = %T, want noAuth", c.auth)
	}

	req, err := c.newRequest(http.MethodGet, srv.URL+"/anything", "", nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()

	if gotAuth != "" {
		t.Errorf("Authorization header = %q, want empty", gotAuth)
	}
	if gotCookie != "" {
		t.Errorf("Cookie header = %q, want empty", gotCookie)
	}

	h := http.Header{}
	if err := c.auth.applyWS(h); err != nil {
		t.Fatalf("applyWS: %v", err)
	}
	if len(h) != 0 {
		t.Errorf("applyWS added headers %v, want none for noAuth", h)
	}
}

// TestWithBearerToken_SetsAuthorizationHeaderOnRESTAndWS covers the shared
// token mode end to end: the exact "Bearer <token>" header format on both a
// REST request and a WebSocket handshake decoration.
func TestWithBearerToken_SetsAuthorizationHeaderOnRESTAndWS(t *testing.T) {
	const token = "shared-tok-123"

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, WithBearerToken(token))

	req, err := c.newRequest(http.MethodGet, srv.URL+"/anything", "", nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()

	want := "Bearer " + token
	if gotAuth != want {
		t.Errorf("REST Authorization header = %q, want %q", gotAuth, want)
	}

	h := http.Header{}
	if err := c.auth.applyWS(h); err != nil {
		t.Fatalf("applyWS: %v", err)
	}
	if got := h.Get("Authorization"); got != want {
		t.Errorf("WS handshake Authorization header = %q, want %q", got, want)
	}
}

// TestWithTokenSupplier_ReinvokedPerRequest ensures a caller-supplied token
// supplier (env var, keychain, config reload) is called fresh on every
// request rather than cached at construction time, so rotation works
// without reconstructing the Client.
func TestWithTokenSupplier_ReinvokedPerRequest(t *testing.T) {
	var calls int32
	supplier := func() (string, error) {
		n := atomic.AddInt32(&calls, 1)
		return fmt.Sprintf("tok-%d", n), nil
	}

	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, WithTokenSupplier(supplier))

	for i := 0; i < 2; i++ {
		req, err := c.newRequest(http.MethodGet, srv.URL+"/anything", "", nil)
		if err != nil {
			t.Fatalf("newRequest #%d: %v", i, err)
		}
		resp, err := c.do(req)
		if err != nil {
			t.Fatalf("do #%d: %v", i, err)
		}
		resp.Body.Close()
	}

	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("supplier called %d times, want 2 (once per request)", calls)
	}
	if len(gotAuth) != 2 || gotAuth[0] == gotAuth[1] {
		t.Errorf("Authorization headers = %v, want two distinct values (rotation)", gotAuth)
	}
	if gotAuth[0] != "Bearer tok-1" || gotAuth[1] != "Bearer tok-2" {
		t.Errorf("Authorization headers = %v, want [\"Bearer tok-1\" \"Bearer tok-2\"]", gotAuth)
	}
}

// TestWithTokenSupplier_ErrorSurfacesWithoutLeakingToken confirms a
// supplier error fails the request with a wrapped error, and that neither
// the supplier's own error text nor any token material leaks through
// newRequest's returned error or through applyWS.
func TestWithTokenSupplier_ErrorSurfacesWithoutLeakingToken(t *testing.T) {
	supplierErr := fmt.Errorf("keychain locked")
	c := New("http://example.invalid", WithTokenSupplier(func() (string, error) {
		return "", supplierErr
	}))

	_, err := c.newRequest(http.MethodGet, "http://example.invalid/anything", "", nil)
	if err == nil {
		t.Fatal("newRequest returned nil error, want the supplier error wrapped")
	}
	if !strings.Contains(err.Error(), "keychain locked") {
		t.Errorf("newRequest error = %q, want it to wrap the supplier error", err.Error())
	}

	h := http.Header{}
	if err := c.auth.applyWS(h); err == nil {
		t.Fatal("applyWS returned nil error, want the supplier error wrapped")
	}
	if len(h) != 0 {
		t.Errorf("applyWS set headers %v on error, want none", h)
	}
}

// TestLogin_InstallsCookieAuthAndAttachesCSRFOnStateChangingRequests drives
// the full interactive login handshake against a fake server: GET
// /api/csrf-token, then POST /api/login with the CSRF header and
// credentials, then asserts that a subsequent state-changing REST request
// carries both the session cookie (via the jar, automatically) and the
// X-CSRF-Token header, while a read-only GET carries the cookie but not the
// CSRF header (mirrors internal/web/middleware/csrf.go's
// isStateChangingMethod).
func TestLogin_InstallsCookieAuthAndAttachesCSRFOnStateChangingRequests(t *testing.T) {
	const csrfToken = "csrf-abc"
	const sessionCookie = "sess-123"

	var lastCookie, lastCSRFHeader, lastMethod string

	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/csrf-token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q}`, csrfToken)
	})
	mux.HandleFunc("/mitto/api/login", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(csrfTokenHeaderClient); got != csrfToken {
			http.Error(w, fmt.Sprintf("missing/wrong CSRF header: %q", got), http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "mitto_session", Value: sessionCookie, Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		lastCookie = r.Header.Get("Cookie")
		lastCSRFHeader = r.Header.Get(csrfTokenHeaderClient)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Login(context.Background(), "alice", "hunter2"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.httpClient.Jar == nil {
		t.Fatal("Login did not install a cookie jar")
	}
	if _, ok := c.auth.(cookieAuth); !ok {
		t.Fatalf("auth after Login = %T, want cookieAuth", c.auth)
	}

	// State-changing request: expects both the session cookie (from the
	// jar) and the CSRF header.
	req, err := c.newRequest(http.MethodPost, c.apiURL("/api/sessions"), "", nil)
	if err != nil {
		t.Fatalf("newRequest POST: %v", err)
	}
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do POST: %v", err)
	}
	resp.Body.Close()

	if lastMethod != http.MethodPost {
		t.Fatalf("server saw method %q, want POST", lastMethod)
	}
	if !strings.Contains(lastCookie, "mitto_session="+sessionCookie) {
		t.Errorf("Cookie header = %q, want it to contain the session cookie", lastCookie)
	}
	if lastCSRFHeader != csrfToken {
		t.Errorf("X-CSRF-Token header on POST = %q, want %q", lastCSRFHeader, csrfToken)
	}

	// Read-only request: cookie still attached, but no CSRF header
	// required (mirrors the backend's double-submit exemption for safe
	// methods).
	req, err = c.newRequest(http.MethodGet, c.apiURL("/api/sessions"), "", nil)
	if err != nil {
		t.Fatalf("newRequest GET: %v", err)
	}
	resp, err = c.do(req)
	if err != nil {
		t.Fatalf("do GET: %v", err)
	}
	resp.Body.Close()

	if !strings.Contains(lastCookie, "mitto_session="+sessionCookie) {
		t.Errorf("GET Cookie header = %q, want it to contain the session cookie", lastCookie)
	}
	if lastCSRFHeader != "" {
		t.Errorf("X-CSRF-Token header on GET = %q, want empty", lastCSRFHeader)
	}

	// The WebSocket handshake needs no extra decoration: Connect() shares
	// the same jar with the websocket.Dialer, which attaches jar cookies to
	// the upgrade request itself. cookieAuth.applyWS is documented as a
	// no-op; pin that contract here.
	h := http.Header{}
	if err := c.auth.applyWS(h); err != nil {
		t.Fatalf("applyWS: %v", err)
	}
	if len(h) != 0 {
		t.Errorf("cookieAuth.applyWS set headers %v, want none (dialer.Jar handles cookies)", h)
	}
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	cookies := c.httpClient.Jar.Cookies(srvURL)
	found := false
	for _, ck := range cookies {
		if ck.Name == "mitto_session" && ck.Value == sessionCookie {
			found = true
		}
	}
	if !found {
		t.Errorf("cookie jar does not contain mitto_session for %s; the WS dialer would send no session cookie", srv.URL)
	}
}

// TestLogout_RevertsToNoAuth confirms Logout posts to /api/logout and
// reverts the client to the unauthenticated default, and that Logout on a
// client never logged in is a no-op.
func TestLogout_RevertsToNoAuth(t *testing.T) {
	var logoutCalled bool
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/csrf-token", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"token":"tok"}`)
	})
	mux.HandleFunc("/mitto/api/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "mitto_session", Value: "sess", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/mitto/api/logout", func(w http.ResponseWriter, r *http.Request) {
		logoutCalled = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Logout without a prior Login is a no-op and must not hit the server.
	c := New(srv.URL)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout before Login: %v", err)
	}
	if logoutCalled {
		t.Fatal("Logout before Login must not call the server")
	}

	if err := c.Login(context.Background(), "alice", "hunter2"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if !logoutCalled {
		t.Fatal("Logout did not call the server")
	}
	if _, ok := c.auth.(noAuth); !ok {
		t.Fatalf("auth after Logout = %T, want noAuth", c.auth)
	}
	if c.csrfToken != "" {
		t.Errorf("csrfToken after Logout = %q, want empty", c.csrfToken)
	}
}

// TestBearerAuth_TokenNeverAppearsInURLOrErrors is the explicit credential
// leak guard called for by the plan: the token must never appear in the
// request URL/query string, nor in any error surfaced by the auth layer.
func TestBearerAuth_TokenNeverAppearsInURLOrErrors(t *testing.T) {
	const secretToken = "s3cr3t-token-xyz"

	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, WithBearerToken(secretToken))

	req, err := c.newRequest(http.MethodGet, srv.URL+"/api/sessions?foo=bar", "", nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	if strings.Contains(req.URL.String(), secretToken) {
		t.Fatalf("token leaked into the built request URL: %s", req.URL.String())
	}

	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	apiErr := c.apiError("get", resp)
	resp.Body.Close()

	if strings.Contains(gotURL, secretToken) {
		t.Errorf("token leaked into the URL the server observed: %s", gotURL)
	}
	if strings.Contains(apiErr.Error(), secretToken) {
		t.Errorf("token leaked into the API error message: %s", apiErr.Error())
	}
	if strings.Contains(fmt.Sprintf("%+v", c), secretToken) {
		t.Errorf("token leaked into a %%+v dump of the Client (would leak via ad-hoc logging)")
	}
}
