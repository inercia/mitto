// Package middleware provides HTTP security/middleware functionality for Mitto.
//
// Auth tests use table-driven test patterns for comprehensive coverage.
// See TestAuthManager_ValidateCredentials, TestParseClientIP, etc. for examples.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

func TestAuthManager_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *config.WebAuth
		want   bool
	}{
		{
			name:   "nil config",
			config: nil,
			want:   false,
		},
		{
			name:   "nil simple",
			config: &config.WebAuth{Simple: nil},
			want:   false,
		},
		{
			name: "enabled",
			config: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := NewAuthManager(tt.config)
			if got := am.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthManager_ValidateCredentials(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "secret123",
		},
	})

	tests := []struct {
		name     string
		username string
		password string
		want     bool
	}{
		{"valid credentials", "admin", "secret123", true},
		{"wrong password", "admin", "wrongpass", false},
		{"wrong username", "wronguser", "secret123", false},
		{"both wrong", "wronguser", "wrongpass", false},
		{"empty credentials", "", "", false},
		{"empty username", "", "secret123", false},
		{"empty password", "admin", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := am.ValidateCredentials(tt.username, tt.password); got != tt.want {
				t.Errorf("ValidateCredentials(%q, %q) = %v, want %v",
					tt.username, tt.password, got, tt.want)
			}
		})
	}
}

func TestAuthManager_SessionLifecycle(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
	})

	// Create session
	session, err := am.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	if session.Username != "testuser" {
		t.Errorf("Username = %q, want %q", session.Username, "testuser")
	}

	if session.Token == "" {
		t.Error("Token is empty")
	}

	if len(session.Token) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("Token length = %d, want 64", len(session.Token))
	}

	// Validate session
	validSession, valid := am.ValidateSession(session.Token)
	if !valid {
		t.Error("ValidateSession returned false for valid session")
	}
	if validSession.Username != "testuser" {
		t.Errorf("ValidatedSession Username = %q, want %q", validSession.Username, "testuser")
	}

	// Invalidate session
	am.InvalidateSession(session.Token)

	// Validate again should fail
	_, valid = am.ValidateSession(session.Token)
	if valid {
		t.Error("ValidateSession returned true for invalidated session")
	}
}

func TestAuthManager_SessionExpiry(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
	})

	// Create session
	session, _ := am.CreateSession("testuser")

	// Manually expire the session
	am.mu.Lock()
	if s, ok := am.sessions[session.Token]; ok {
		s.ExpiresAt = time.Now().Add(-1 * time.Hour)
	}
	am.mu.Unlock()

	// Validate should fail
	_, valid := am.ValidateSession(session.Token)
	if valid {
		t.Error("ValidateSession returned true for expired session")
	}
}

// TestCookieSecure_ExternalListenerIgnoresLocalhostHost reproduces mitto-8c3u:
// trusted external-listener provenance must override the caller-controlled Host.
func TestCookieSecure_ExternalListenerIgnoresLocalhostHost(t *testing.T) {
	auth := &AuthManager{}
	session := &AuthSession{Token: "0123456789abcdef", ExpiresAt: time.Now().Add(time.Hour)}

	writers := map[string]func(http.ResponseWriter, *http.Request){
		"session set": func(w http.ResponseWriter, r *http.Request) {
			auth.SetSessionCookie(w, r, session)
		},
		"session clear": auth.ClearSessionCookie,
		"csrf": func(w http.ResponseWriter, r *http.Request) {
			(&CSRFManager{}).SetCSRFCookie(w, r, "csrf-token")
		},
	}

	requests := []struct {
		name       string
		host       string
		external   bool
		wantSecure bool
	}{
		{"external localhost", "localhost:8080", true, true},
		{"external IPv4 loopback", "127.0.0.1:8080", true, true},
		{"external IPv6 loopback", "[::1]:8080", true, true},
		{"internal localhost", "localhost:8080", false, false},
	}

	for _, request := range requests {
		for name, writeCookie := range writers {
			t.Run(request.name+"/"+name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				if request.external {
					req = makeExternalRequest(req)
				}
				req.Host = request.host
				recorder := httptest.NewRecorder()

				writeCookie(recorder, req)

				cookies := recorder.Result().Cookies()
				if len(cookies) != 1 {
					t.Fatalf("cookie count = %d, want 1", len(cookies))
				}
				if cookies[0].Secure != request.wantSecure {
					t.Errorf("Secure = %v, want %v for Host %q", cookies[0].Secure, request.wantSecure, request.host)
				}
			})
		}
	}
}

func TestAuthManager_HandleLogin(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "secret",
		},
	})
	defer am.Close()

	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
	}{
		{"valid login", "POST", `{"username":"admin","password":"secret"}`, http.StatusOK},
		{"invalid password", "POST", `{"username":"admin","password":"wrong"}`, http.StatusUnauthorized},
		{"wrong method", "GET", "", http.StatusMethodNotAllowed},
		{"empty body", "POST", "{}", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/login", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			am.HandleLogin(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body = %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestAuthManager_HandleLogin_SetsAuthIdentity verifies that the attempted username
// is written into the *AuthIdentity context holder on both success and failure paths,
// so access-log entries carry user= for auditing.
func TestAuthManager_HandleLogin_SetsAuthIdentity(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "secret",
		},
	})
	defer am.Close()

	tests := []struct {
		name     string
		body     string
		wantUser string
	}{
		{"failed login records username", `{"username":"attacker","password":"wrong"}`, "attacker"},
		{"successful login records username", `{"username":"admin","password":"secret"}`, "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			holder := &AuthIdentity{}
			req := httptest.NewRequest("POST", "/api/login", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyAuthIdentity, holder))
			w := httptest.NewRecorder()

			am.HandleLogin(w, req)

			if holder.User != tt.wantUser {
				t.Errorf("AuthIdentity.User = %q, want %q", holder.User, tt.wantUser)
			}
		})
	}
}

func TestAuthManager_HandleLogin_RateLimiting(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "secret",
		},
	})
	defer am.Close()

	// Override rate limiter with shorter settings for testing
	am.rateLimiter.Close()
	am.rateLimiter = NewAuthRateLimiterWithConfig(3, time.Minute, 5*time.Minute)

	// First 3 failures should return 401
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()

		am.HandleLogin(w, req)

		// First 2 should be 401, 3rd triggers rate limit
		if i < 2 {
			if w.Code != http.StatusUnauthorized {
				t.Errorf("Attempt %d: status = %d, want %d", i+1, w.Code, http.StatusUnauthorized)
			}
		} else {
			if w.Code != http.StatusTooManyRequests {
				t.Errorf("Attempt %d: status = %d, want %d", i+1, w.Code, http.StatusTooManyRequests)
			}
		}
	}

	// Subsequent attempts should be rate limited immediately
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()

	am.HandleLogin(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("After lockout: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// Check Retry-After header
	retryAfter := w.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("Missing Retry-After header")
	}

	// Different IP should not be rate limited
	req2 := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "192.168.1.200:12345"
	w2 := httptest.NewRecorder()

	am.HandleLogin(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Different IP: status = %d, want %d", w2.Code, http.StatusOK)
	}
}

func TestAuthManager_IPAllowList(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "secret",
		},
		Allow: &config.AuthAllow{
			IPs: []string{
				"127.0.0.1",
				"::1",
				"192.168.1.0/24",
				"10.0.0.0/8",
				"2001:db8::/32",
			},
		},
	})

	tests := []struct {
		name    string
		ip      string
		allowed bool
	}{
		// Individual IPs
		{"localhost IPv4", "127.0.0.1", true},
		{"localhost IPv6", "::1", true},
		{"not in list", "8.8.8.8", false},

		// CIDR ranges
		{"in 192.168.1.0/24 range", "192.168.1.100", true},
		{"in 192.168.1.0/24 range start", "192.168.1.0", true},
		{"in 192.168.1.0/24 range end", "192.168.1.255", true},
		{"outside 192.168.1.0/24 range", "192.168.2.1", false},

		{"in 10.0.0.0/8 range", "10.255.255.255", true},
		{"outside 10.0.0.0/8 range", "11.0.0.1", false},

		// IPv6 CIDR
		{"in IPv6 CIDR", "2001:db8::1", true},
		{"outside IPv6 CIDR", "2001:db9::1", false},

		// With port numbers
		{"localhost with port", "127.0.0.1:8080", true},
		{"IPv6 with port", "[::1]:8080", true},
		{"not allowed with port", "8.8.8.8:443", false},

		// Edge cases
		{"empty string", "", false},
		{"invalid IP", "not-an-ip", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := am.IsIPAllowed(tt.ip); got != tt.allowed {
				t.Errorf("IsIPAllowed(%q) = %v, want %v", tt.ip, got, tt.allowed)
			}
		})
	}
}

func TestAuthManager_EmptyAllowList(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "secret",
		},
		Allow: &config.AuthAllow{
			IPs: []string{}, // Empty allow list
		},
	})

	// Nothing should be allowed
	if am.IsIPAllowed("127.0.0.1") {
		t.Error("IsIPAllowed should return false for empty allow list")
	}
}

func TestAuthManager_AllowListOnly(t *testing.T) {
	// Auth with only allow list (no simple auth) - edge case
	am := NewAuthManager(&config.WebAuth{
		Allow: &config.AuthAllow{
			IPs: []string{"127.0.0.1"},
		},
	})

	// IP should still be checked even without simple auth
	if !am.IsIPAllowed("127.0.0.1") {
		t.Error("IsIPAllowed should work even without simple auth configured")
	}

	// But auth is not enabled (no simple auth)
	if am.IsEnabled() {
		t.Error("IsEnabled should return false when simple auth is not configured")
	}
}

func TestParseClientIP(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{"plain IPv4", "192.168.1.1", "192.168.1.1"},
		{"IPv4 with port", "192.168.1.1:8080", "192.168.1.1"},
		{"plain IPv6", "::1", "::1"},
		{"IPv6 with port", "[::1]:8080", "::1"},
		{"full IPv6", "2001:db8::1", "2001:db8::1"},
		{"full IPv6 with port", "[2001:db8::1]:443", "2001:db8::1"},
		{"invalid", "not-an-ip", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseClientIP(tt.addr)
			if tt.want == "" {
				if got != nil {
					t.Errorf("parseClientIP(%q) = %v, want nil", tt.addr, got)
				}
			} else {
				if got == nil || got.String() != tt.want {
					t.Errorf("parseClientIP(%q) = %v, want %s", tt.addr, got, tt.want)
				}
			}
		})
	}
}

func TestGetClientIP(t *testing.T) {
	// getClientIP now only uses RemoteAddr — never trusts forwarded headers.
	// Forwarded headers are only trusted via getClientIPWithProxyCheck()
	// when the request comes from a configured trusted proxy.
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "RemoteAddr only",
			remoteAddr: "192.168.1.1:12345",
			headers:    nil,
			want:       "192.168.1.1:12345",
		},
		{
			name:       "ignores X-Forwarded-For",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50"},
			want:       "10.0.0.1:12345",
		},
		{
			name:       "ignores X-Forwarded-For multiple",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50, 70.41.3.18, 150.172.238.178"},
			want:       "10.0.0.1:12345",
		},
		{
			name:       "ignores X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Real-IP": "203.0.113.100"},
			want:       "10.0.0.1:12345",
		},
		{
			name:       "ignores all forwarded headers",
			remoteAddr: "10.0.0.1:12345",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.50",
				"X-Real-IP":       "203.0.113.100",
			},
			want: "10.0.0.1:12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got := getClientIP(req)
			if got != tt.want {
				t.Errorf("getClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsLoopbackIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		// IPv4 loopback
		{name: "127.0.0.1", ip: "127.0.0.1", want: true},
		{name: "127.0.0.1:8080", ip: "127.0.0.1:8080", want: true},
		{name: "127.0.1.1", ip: "127.0.1.1", want: true},
		{name: "127.255.255.255", ip: "127.255.255.255", want: true},

		// IPv6 loopback
		{name: "::1", ip: "::1", want: true},
		{name: "[::1]:8080", ip: "[::1]:8080", want: true},

		// Non-loopback
		{name: "192.168.1.1", ip: "192.168.1.1", want: false},
		{name: "192.168.1.1:8080", ip: "192.168.1.1:8080", want: false},
		{name: "10.0.0.1", ip: "10.0.0.1", want: false},
		{name: "0.0.0.0", ip: "0.0.0.0", want: false},
		{name: "8.8.8.8", ip: "8.8.8.8", want: false},
		{name: "::ffff:127.0.0.1", ip: "::ffff:127.0.0.1", want: true}, // IPv4-mapped IPv6 loopback

		// Invalid
		{name: "empty", ip: "", want: false},
		{name: "invalid", ip: "not-an-ip", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLoopbackIP(tt.ip)
			if got != tt.want {
				t.Errorf("isLoopbackIP(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestAuthManager_SessionLimit(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	defer am.Close()

	// Create more sessions than the limit
	username := "admin"
	var sessions []*AuthSession
	for i := 0; i < maxSessionsPerUser+5; i++ {
		session, err := am.CreateSession(username)
		if err != nil {
			t.Fatalf("CreateSession() error = %v", err)
		}
		sessions = append(sessions, session)
	}

	// Count valid sessions for this user
	validCount := 0
	for _, session := range sessions {
		if _, valid := am.ValidateSession(session.Token); valid {
			validCount++
		}
	}

	// Should have at most maxSessionsPerUser valid sessions
	if validCount > maxSessionsPerUser {
		t.Errorf("Valid sessions = %d, want <= %d", validCount, maxSessionsPerUser)
	}

	// The most recent sessions should be valid
	for i := len(sessions) - maxSessionsPerUser; i < len(sessions); i++ {
		if _, valid := am.ValidateSession(sessions[i].Token); !valid {
			t.Errorf("Recent session %d should be valid", i)
		}
	}
}

func TestAuthManager_Close(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})

	// Create a session
	_, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	// Close should not panic or hang
	done := make(chan struct{})
	go func() {
		am.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Close() timed out")
	}
}

func TestAuthManager_isPublicPath(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	am.SetAPIPrefix("/mitto")

	tests := []struct {
		name string
		path string
		want bool
	}{
		// Static public paths
		{"auth.html", "/auth.html", true},
		{"auth.js", "/auth.js", true},
		{"styles.css", "/styles.css", true},
		{"favicon.ico", "/favicon.ico", true},

		// API public paths (with prefix)
		{"login endpoint", "/mitto/api/login", true},
		{"csrf-token endpoint", "/mitto/api/csrf-token", true},
		{"supported-runners endpoint", "/mitto/api/supported-runners", true},
		{"Slack OAuth callback", "/mitto/api/slack/oauth/callback", true},

		// SDK asset tree (mitto-laa3): auth.js imports from ./sdk/index.js,
		// so the whole /sdk/ tree must be public or the login page's module
		// graph 302s to auth.html and fails to load.
		{"sdk index", "/sdk/index.js", true},
		{"sdk nested", "/sdk/core/config.js", true},
		{"sdk with prefix", "/mitto/sdk/index.js", true},

		// Non-public paths
		{"sessions endpoint", "/mitto/api/sessions", false},
		{"config endpoint", "/mitto/api/config", false},
		{"root", "/", false},
		{"index.html", "/index.html", false},
		{"app.js", "/app.js", false},
		{"not sdk", "/sdkfoo.js", false},

		// API paths without prefix (should not match)
		{"login without prefix", "/api/login", false},
		{"Slack OAuth callback without prefix", "/api/slack/oauth/callback", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := am.isPublicPath(tt.path); got != tt.want {
				t.Errorf("isPublicPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// sdkImportFromRe matches `import ... from "./x.js"` and `export ... from
// "./x.js"` (including multi-line `import {\n  a,\n} from "./x.js"` forms,
// since the character class excludes quotes and therefore cannot skip over
// an intervening bare/side-effect import's own quoted specifier).
var sdkImportFromRe = regexp.MustCompile(`(?:import|export)\s+[^'"]*?from\s+['"]([^'"]+)['"]`)

// sdkImportBareRe matches side-effect-only imports: `import "./x.js";`.
var sdkImportBareRe = regexp.MustCompile(`(?m)^\s*import\s+['"]([^'"]+)['"]`)

// relativeImportSpecifiers extracts the relative ("./x.js" / "../x.js")
// ES-module specifiers referenced by a JS file. Bare/package specifiers and
// absolute URLs are ignored. This is intentionally a simple regexp scan, not
// a real JS parser (mitto-laa3).
func relativeImportSpecifiers(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("failed to read %s: %v", file, err)
	}
	content := string(data)

	var specs []string
	seen := map[string]bool{}
	addIfRelative := func(spec string) {
		if (strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")) && !seen[spec] {
			seen[spec] = true
			specs = append(specs, spec)
		}
	}
	for _, m := range sdkImportFromRe.FindAllStringSubmatch(content, -1) {
		addIfRelative(m[1])
	}
	for _, m := range sdkImportBareRe.FindAllStringSubmatch(content, -1) {
		addIfRelative(m[1])
	}
	return specs
}

// TestAuthManager_isPublicPath_AuthJSModuleGraphIsPublic is a drift guard
// (mitto-laa3): it walks the entire relative ES-module import graph rooted
// at web/static/auth.js and asserts every reachable file is public. auth.js
// is a `type="module"` script loaded by the pre-auth login page; if any file
// in its import graph becomes non-public, the browser's module loader 302s
// to auth.html and the login form's submit listener is never attached — a
// silent, total remote-login outage (this is exactly how the /sdk/ tree
// broke login before this fix). The test does not special-case /sdk/ so it
// also catches the next refactor that pulls a new pre-auth file from
// elsewhere into the graph.
func TestAuthManager_isPublicPath_AuthJSModuleGraphIsPublic(t *testing.T) {
	staticRoot, err := filepath.Abs("../../../web/static")
	if err != nil {
		t.Fatalf("failed to resolve web/static root: %v", err)
	}
	entry, err := filepath.Abs(filepath.Join(staticRoot, "auth.js"))
	if err != nil {
		t.Fatalf("failed to resolve auth.js path: %v", err)
	}
	if _, statErr := os.Stat(entry); statErr != nil {
		t.Fatalf("expected %s to exist: %v", entry, statErr)
	}

	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})

	visited := map[string]bool{}
	queue := []string{entry}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true

		rel, relErr := filepath.Rel(staticRoot, current)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("resolved import %q escapes web/static root (rel=%q)", current, rel)
		}
		serverPath := "/" + filepath.ToSlash(rel)
		if !am.isPublicPath(serverPath) {
			t.Errorf("isPublicPath(%q) = false, want true (reachable from auth.js's module graph)", serverPath)
		}

		for _, spec := range relativeImportSpecifiers(t, current) {
			next := filepath.Clean(filepath.Join(filepath.Dir(current), spec))
			if !visited[next] {
				queue = append(queue, next)
			}
		}
	}

	// Sanity check: auth.js alone imports sdk/index.js, which fans out into
	// dozens of files. If we only ever visited 1-2 files, the import parser
	// is broken (or auth.js's imports were gutted) rather than the graph
	// being genuinely small.
	if len(visited) < 5 {
		t.Fatalf("expected auth.js's module graph to contain more than %d file(s) — import parsing may be broken", len(visited))
	}
}

func TestAuthManager_GetSessionFromRequest(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})

	// Create a valid session
	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	tests := []struct {
		name      string
		cookie    *http.Cookie
		wantValid bool
		wantUser  string
	}{
		{
			name:      "no cookie",
			cookie:    nil,
			wantValid: false,
		},
		{
			name: "valid session cookie",
			cookie: &http.Cookie{
				Name:  "mitto_session",
				Value: session.Token,
			},
			wantValid: true,
			wantUser:  "admin",
		},
		{
			name: "invalid token",
			cookie: &http.Cookie{
				Name:  "mitto_session",
				Value: "invalid-token-12345",
			},
			wantValid: false,
		},
		{
			name: "wrong cookie name",
			cookie: &http.Cookie{
				Name:  "other_cookie",
				Value: session.Token,
			},
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}

			gotSession, gotValid := am.GetSessionFromRequest(req)
			if gotValid != tt.wantValid {
				t.Errorf("GetSessionFromRequest() valid = %v, want %v", gotValid, tt.wantValid)
			}
			if gotValid && gotSession.Username != tt.wantUser {
				t.Errorf("GetSessionFromRequest() username = %q, want %q", gotSession.Username, tt.wantUser)
			}
		})
	}
}

func TestAuthManager_HandleLogout(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})

	// Create a session
	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	// Verify session is valid
	if _, valid := am.ValidateSession(session.Token); !valid {
		t.Fatal("Session should be valid before logout")
	}

	// Create logout request with session cookie
	req := httptest.NewRequest("POST", "/api/logout", nil)
	req.AddCookie(&http.Cookie{
		Name:  "mitto_session",
		Value: session.Token,
	})

	w := httptest.NewRecorder()
	am.HandleLogout(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("HandleLogout() status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify session is invalidated
	if _, valid := am.ValidateSession(session.Token); valid {
		t.Error("Session should be invalid after logout")
	}

	// Check that cookie is cleared (MaxAge = -1)
	cookies := w.Result().Cookies()
	var foundClearCookie bool
	for _, c := range cookies {
		if c.Name == "mitto_session" && c.MaxAge == -1 {
			foundClearCookie = true
			break
		}
	}
	if !foundClearCookie {
		t.Error("HandleLogout() should set cookie with MaxAge=-1 to clear it")
	}
}

func TestAuthManager_HandleLogout_WrongMethod(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})

	req := httptest.NewRequest("GET", "/api/logout", nil)
	w := httptest.NewRecorder()
	am.HandleLogout(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleLogout() with GET status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestAuthMiddleware_LocalhostBypass(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	am.SetAPIPrefix("/mitto")

	// Create a test handler that records if it was called
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := am.AuthMiddleware(testHandler)

	// Request from localhost (internal listener) should bypass auth
	req := httptest.NewRequest("GET", "/mitto/api/sessions", nil)
	req.RemoteAddr = "127.0.0.1:12345"

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("AuthMiddleware should allow localhost requests without auth")
	}
	if w.Code != http.StatusOK {
		t.Errorf("AuthMiddleware() status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAuthMiddleware_SpoofedForwardingHeadersDoNotGrantPrivileges(t *testing.T) {
	SetDefaultProxyChecker(NewTrustedProxyChecker([]string{"10.0.0.0/8"}))
	t.Cleanup(func() { SetDefaultProxyChecker(nil) })

	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "admin", Password: "password"},
		Allow:  &config.AuthAllow{IPs: []string{"127.0.0.1"}},
	})
	defer am.Close()
	am.SetAPIPrefix("/mitto")

	tests := []struct {
		name       string
		headers    map[string]string
		isExternal bool
	}{
		{
			name:       "Cf-Connecting-IP cannot spoof external allowlist",
			headers:    map[string]string{"Cf-Connecting-IP": "127.0.0.1"},
			isExternal: true,
		},
		{
			name:       "X-Real-IP cannot spoof external allowlist",
			headers:    map[string]string{"X-Real-IP": "127.0.0.1"},
			isExternal: true,
		},
		{
			name:    "leftmost X-Forwarded-For cannot spoof internal loopback",
			headers: map[string]string{"X-Forwarded-For": "127.0.0.1, 198.51.100.25, 10.0.0.2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerCalled := false
			handler := am.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest("GET", "/mitto/api/sessions", nil)
			req.RemoteAddr = "10.0.0.3:8080"
			for name, value := range tt.headers {
				req.Header.Set(name, value)
			}
			if tt.isExternal {
				req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if handlerCalled {
				t.Fatal("spoofed forwarding header granted access")
			}
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestAuthMiddleware_PublicPathBypass(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	am.SetAPIPrefix("/mitto")

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := am.AuthMiddleware(testHandler)

	// Request to public path from external IP should be allowed
	req := httptest.NewRequest("GET", "/auth.html", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("AuthMiddleware should allow public paths without auth")
	}
}

func TestAuthMiddleware_RequiresAuthForAPI(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	am.SetAPIPrefix("/mitto")

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := am.AuthMiddleware(testHandler)

	// Request to protected API from external IP without auth should be rejected
	req := httptest.NewRequest("GET", "/mitto/api/sessions", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if handlerCalled {
		t.Error("AuthMiddleware should NOT call handler for unauthenticated API requests")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("AuthMiddleware() status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_ValidSessionAllowed(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	am.SetAPIPrefix("/mitto")

	// Create a valid session
	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := am.AuthMiddleware(testHandler)

	// Request with valid session cookie from external IP should be allowed
	req := httptest.NewRequest("GET", "/mitto/api/sessions", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.AddCookie(&http.Cookie{
		Name:  "mitto_session",
		Value: session.Token,
	})

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("AuthMiddleware should allow requests with valid session")
	}
	if w.Code != http.StatusOK {
		t.Errorf("AuthMiddleware() status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAuthMiddleware_RedirectsPageRequests(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	am.SetAPIPrefix("/mitto")

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := am.AuthMiddleware(testHandler)

	// Request to non-API path from external IP without auth should redirect
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("AuthMiddleware() status = %d, want %d (redirect)", w.Code, http.StatusFound)
	}
	location := w.Header().Get("Location")
	if location != "/auth.html" {
		t.Errorf("AuthMiddleware() redirect location = %q, want %q", location, "/auth.html")
	}
}

func TestAuthMiddleware_DisabledPassesThrough(t *testing.T) {
	// Auth manager with no credentials (disabled)
	am := NewAuthManager(nil)

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := am.AuthMiddleware(testHandler)

	// Any request should pass through when auth is disabled
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.RemoteAddr = "192.168.1.100:12345"

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("AuthMiddleware should pass through when auth is disabled")
	}
}

func TestAuthManager_HasValidCredentials(t *testing.T) {
	tests := []struct {
		name   string
		config *config.WebAuth
		want   bool
	}{
		{
			name:   "nil config",
			config: nil,
			want:   false,
		},
		{
			name:   "nil simple",
			config: &config.WebAuth{Simple: nil},
			want:   false,
		},
		{
			name: "empty username",
			config: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "", Password: "pass"},
			},
			want: false,
		},
		{
			name: "empty password",
			config: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "user", Password: ""},
			},
			want: false,
		},
		{
			name: "valid credentials",
			config: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := NewAuthManager(tt.config)
			if got := am.HasValidCredentials(); got != tt.want {
				t.Errorf("HasValidCredentials() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthManager_CredentialError(t *testing.T) {
	tests := []struct {
		name      string
		config    *config.WebAuth
		wantError error
	}{
		{
			name:      "nil config",
			config:    nil,
			wantError: ErrNoCredentials,
		},
		{
			name:      "nil simple",
			config:    &config.WebAuth{Simple: nil},
			wantError: ErrNoCredentials,
		},
		{
			name: "empty username",
			config: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "", Password: "pass"},
			},
			wantError: ErrEmptyUsername,
		},
		{
			name: "empty password",
			config: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "user", Password: ""},
			},
			wantError: ErrEmptyPassword,
		},
		{
			name: "valid credentials",
			config: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
			},
			wantError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := NewAuthManager(tt.config)
			got := am.CredentialError()
			if got != tt.wantError {
				t.Errorf("CredentialError() = %v, want %v", got, tt.wantError)
			}
		})
	}
}

func TestAuthManager_UpdateConfig(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "user",
			Password: "pass",
		},
	})
	defer am.Close()

	// Update with new config
	newConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "newuser",
			Password: "newpass",
		},
		Allow: &config.AuthAllow{
			IPs: []string{"192.168.1.0/24", "10.0.0.1"},
		},
	}
	am.UpdateConfig(newConfig)

	// Verify the config was updated
	if !am.IsEnabled() {
		t.Error("Auth should still be enabled after update")
	}
}

func TestAuthManager_UpdateConfig_RevokesSessions(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	oldConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{Username: "olduser", Password: "oldpass"},
	}
	am := NewAuthManager(oldConfig)
	session, err := am.CreateSession("olduser")
	if err != nil {
		am.Close()
		t.Fatalf("CreateSession() error = %v", err)
	}

	// Regression test for mitto-fz2w: credential rotation must revoke the
	// session immediately and atomically clear its persisted representation.
	newConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{Username: "newuser", Password: "newpass"},
	}
	am.UpdateConfig(newConfig)
	if _, valid := am.ValidateSession(session.Token); valid {
		t.Error("session remained valid after credential rotation")
	}
	am.Close()

	restarted := NewAuthManager(newConfig)
	defer restarted.Close()
	if _, valid := restarted.ValidateSession(session.Token); valid {
		t.Error("revoked session was restored after restart")
	}
}

func TestAuthManager_UpdateConfig_SessionRevocationSemantics(t *testing.T) {
	baseConfig := func() *config.WebAuth {
		return &config.WebAuth{
			Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
			Cloudflare: &config.CloudflareAuth{
				TeamDomain: "old.cloudflareaccess.com",
				Audience:   "old-audience",
			},
			Allow:       &config.AuthAllow{IPs: []string{"192.0.2.1"}},
			SharedToken: "old-token",
		}
	}

	tests := []struct {
		name        string
		update      func(*config.WebAuth) *config.WebAuth
		wantRevoked bool
	}{
		{"username changed", func(c *config.WebAuth) *config.WebAuth { c.Simple.Username = "newuser"; return c }, true},
		{"password changed", func(c *config.WebAuth) *config.WebAuth { c.Simple.Password = "newpass"; return c }, true},
		{"identity provider changed", func(c *config.WebAuth) *config.WebAuth {
			c.Cloudflare.TeamDomain = "new.cloudflareaccess.com"
			return c
		}, true},
		{"identity provider audience changed", func(c *config.WebAuth) *config.WebAuth { c.Cloudflare.Audience = "new-audience"; return c }, true},
		{"identity provider CA changed", func(c *config.WebAuth) *config.WebAuth { c.Cloudflare.CACertFile = "new-ca.pem"; return c }, true},
		{"shared token changed", func(c *config.WebAuth) *config.WebAuth { c.SharedToken = "new-token"; return c }, true},
		{"authentication disabled", func(*config.WebAuth) *config.WebAuth { return nil }, true},
		{"allowlist changed", func(c *config.WebAuth) *config.WebAuth { c.Allow.IPs = []string{"198.51.100.0/24"}; return c }, true},
		{"unchanged auth", func(c *config.WebAuth) *config.WebAuth { return c }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(appdir.MittoDirEnv, t.TempDir())
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)

			am := NewAuthManager(baseConfig())
			defer am.Close()
			session, err := am.CreateSession("user")
			if err != nil {
				t.Fatalf("CreateSession() error = %v", err)
			}

			am.UpdateConfig(tt.update(baseConfig()))
			_, valid := am.ValidateSession(session.Token)
			if valid == tt.wantRevoked {
				t.Fatalf("ValidateSession() valid = %v, want %v", valid, !tt.wantRevoked)
			}
		})
	}
}

func TestAuthManager_SetSharedToken_RevokesSessions(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	am := NewAuthManager(&config.WebAuth{
		Simple:      &config.SimpleAuth{Username: "user", Password: "pass"},
		SharedToken: "old-token",
	})
	defer am.Close()
	session, err := am.CreateSession("user")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	am.SetSharedToken("new-token")
	if _, valid := am.ValidateSession(session.Token); valid {
		t.Error("session remained valid after shared token rotation")
	}
}

func TestAuthManager_UpdateConfig_Nil(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "user",
			Password: "pass",
		},
	})
	defer am.Close()

	// Update with nil config
	am.UpdateConfig(nil)

	// Auth should be disabled
	if am.IsEnabled() {
		t.Error("Auth should be disabled after nil config update")
	}
}

func TestAuthMiddleware_SensitivePathsRequireAuth(t *testing.T) {
	// Acceptance criterion: sensitive/write/delete API endpoints must return 401
	// when accessed without a session cookie from a non-loopback external IP.
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "admin",
			Password: "password",
		},
	})
	am.SetAPIPrefix("")

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := am.AuthMiddleware(testHandler)

	paths := []struct {
		method string
		path   string
	}{
		{"GET", "/api/config"},
		{"GET", "/api/workspaces"},
		{"GET", "/api/advanced-flags"},
		{"GET", "/api/workspace-prompts"},
		{"POST", "/api/sessions"},
		{"DELETE", "/api/sessions/20260621-000238-bdefea3e"},
	}

	for _, tc := range paths {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			// Non-loopback RemoteAddr + external listener flag
			req.RemoteAddr = "203.0.113.1:54321"
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))

			w := httptest.NewRecorder()
			middleware.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s: status = %d, want %d (Unauthorized)",
					tc.method, tc.path, w.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestAuthManager_CleanupExpiredSessions(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "user",
			Password: "pass",
		},
	})
	defer am.Close()

	// Create a session
	session, err := am.CreateSession("user")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Verify session exists
	_, valid := am.ValidateSession(session.Token)
	if !valid {
		t.Error("Session should be valid after creation")
	}

	// Manually trigger cleanup (sessions should not be cleaned up yet)
	am.cleanupExpiredSessions()

	// Session should still exist (not expired)
	_, valid = am.ValidateSession(session.Token)
	if !valid {
		t.Error("Session should still be valid after cleanup")
	}
}

// --- Shared bearer token tests (mitto-7gta.26) ---

func TestAuthManager_HasSharedToken(t *testing.T) {
	tests := []struct {
		name   string
		config *config.WebAuth
		want   bool
	}{
		{"nil config", nil, false},
		{"no token configured", &config.WebAuth{Simple: &config.SimpleAuth{Username: "u", Password: "p"}}, false},
		{"empty token string", &config.WebAuth{SharedToken: ""}, false},
		{"token configured", &config.WebAuth{SharedToken: "s3cr3t"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := NewAuthManager(tt.config)
			defer am.Close()
			if got := am.HasSharedToken(); got != tt.want {
				t.Errorf("HasSharedToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthManager_ValidateSharedToken(t *testing.T) {
	tests := []struct {
		name   string
		config *config.WebAuth
		tok    string
		want   bool
	}{
		{"valid token", &config.WebAuth{SharedToken: "s3cr3t"}, "s3cr3t", true},
		{"wrong token", &config.WebAuth{SharedToken: "s3cr3t"}, "wrong", false},
		{"empty tok against configured token", &config.WebAuth{SharedToken: "s3cr3t"}, "", false},
		{"no token configured", &config.WebAuth{}, "s3cr3t", false},
		// An empty configured token must never match an empty presented token.
		{"empty configured token, empty presented", &config.WebAuth{SharedToken: ""}, "", false},
		{"nil config", nil, "anything", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := NewAuthManager(tt.config)
			defer am.Close()
			if got := am.ValidateSharedToken(tt.tok); got != tt.want {
				t.Errorf("ValidateSharedToken(%q) = %v, want %v", tt.tok, got, tt.want)
			}
		})
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid bearer token", "Bearer abc123", "abc123"},
		{"lowercase scheme", "bearer abc123", "abc123"},
		{"mixed case scheme", "BeArEr abc123", "abc123"},
		{"no header", "", ""},
		{"wrong scheme", "Basic abc123", ""},
		{"bearer with no token", "Bearer ", ""},
		{"bearer with only whitespace token", "Bearer    ", ""},
		{"extra whitespace around token", "Bearer  abc123  ", "abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if got := extractBearerToken(req); got != tt.want {
				t.Errorf("extractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestAuthManager_ValidateBearerRequest(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{SharedToken: "s3cr3t"})
	defer am.Close()

	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"valid token", "Bearer s3cr3t", true},
		{"invalid token", "Bearer wrong", false},
		{"no header", "", false},
		{"wrong scheme", "Basic s3cr3t", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/test", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if got := am.ValidateBearerRequest(req); got != tt.want {
				t.Errorf("ValidateBearerRequest() = %v, want %v", got, tt.want)
			}
		})
	}

	// No token configured at all: ValidateBearerRequest must return false even
	// with a well-formed header, and must not panic on a nil-Simple config.
	amNoToken := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "u", Password: "p"}})
	defer amNoToken.Close()
	req := httptest.NewRequest("POST", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	if amNoToken.ValidateBearerRequest(req) {
		t.Error("ValidateBearerRequest() = true when no shared token is configured, want false")
	}
}

// TestAuthMiddleware_SharedToken_ValidGrantsAccess verifies that a valid bearer
// token is accepted by AuthMiddleware even without a session cookie, sets the
// "token:shared" identity, and requires no CSRF/session state.
func TestAuthMiddleware_SharedToken_ValidGrantsAccess(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
		SharedToken: "s3cr3t-token",
	})
	defer am.Close()
	am.SetAPIPrefix("")

	var gotUser any
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Context().Value(ContextKeyAuthUser)
		w.WriteHeader(http.StatusOK)
	})
	middleware := am.AuthMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.RemoteAddr = "203.0.113.1:54321"
	req.Header.Set("Authorization", "Bearer s3cr3t-token")
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if gotUser != "token:shared" {
		t.Errorf("ContextKeyAuthUser = %v, want %q", gotUser, "token:shared")
	}
}

// TestAuthMiddleware_SharedToken_InvalidFallsThroughToUnauthorized verifies an
// invalid bearer token does not grant access and degrades to the existing
// session/Cloudflare checks, which reject the unauthenticated request.
func TestAuthMiddleware_SharedToken_InvalidFallsThroughToUnauthorized(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
		SharedToken: "s3cr3t-token",
	})
	defer am.Close()
	am.SetAPIPrefix("")

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	middleware := am.AuthMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.RemoteAddr = "203.0.113.1:54321"
	req.Header.Set("Authorization", "Bearer wrong-token")
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if handlerCalled {
		t.Error("AuthMiddleware should NOT call handler for an invalid bearer token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestAuthMiddleware_SharedToken_AbsentHeaderUnchangedBehaviour verifies that
// when no Authorization header is present at all, behaviour is identical to
// before the shared-token feature existed (zero behaviour change).
func TestAuthMiddleware_SharedToken_AbsentHeaderUnchangedBehaviour(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
		SharedToken: "s3cr3t-token",
	})
	defer am.Close()
	am.SetAPIPrefix("")

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := am.AuthMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.RemoteAddr = "203.0.113.1:54321"
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestAuthMiddleware_SharedToken_NotConfiguredIgnoresHeader verifies that when
// no shared token is configured at all, a bearer header is simply ignored
// (the token-only-config-must-not-enable-auth requirement).
func TestAuthMiddleware_SharedToken_NotConfiguredIgnoresHeader(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "admin", Password: "password"},
		// SharedToken intentionally left empty.
	})
	defer am.Close()
	am.SetAPIPrefix("")

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	middleware := am.AuthMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.RemoteAddr = "203.0.113.1:54321"
	req.Header.Set("Authorization", "Bearer whatever")
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if handlerCalled {
		t.Error("AuthMiddleware should NOT call handler when no shared token is configured")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestAuthMiddleware_SharedToken_TokenOnlyDoesNotEnableAuth verifies that a
// configured shared token, on its own (no Simple/Cloudflare), does not flip
// IsEnabled() -- a token alone must not arm the external listener.
func TestAuthMiddleware_SharedToken_TokenOnlyDoesNotEnableAuth(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{SharedToken: "s3cr3t-token"})
	defer am.Close()

	if am.IsEnabled() {
		t.Error("IsEnabled() = true with only a shared token configured, want false")
	}
}

// TestAuthMiddleware_SharedToken_RateLimiting verifies repeated invalid bearer
// tokens trip the SAME per-IP rate limiter used by password login, and that a
// valid token is rejected once the lockout engages (shared lockout, not a
// separate counter that could be used to sidestep password lockout).
func TestAuthMiddleware_SharedToken_RateLimiting(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
		SharedToken: "s3cr3t-token",
	})
	defer am.Close()
	am.SetAPIPrefix("")

	// Shorter settings for a fast, deterministic test.
	am.rateLimiter.Close()
	am.rateLimiter = NewAuthRateLimiterWithConfig(3, time.Minute, 5*time.Minute)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := am.AuthMiddleware(testHandler)

	const ip = "203.0.113.50:12345"

	// Unlike HandleLogin (which checks RecordFailure's return value and
	// returns 429 on the very request that reaches maxFailures), the
	// AuthMiddleware bearer-token branch gates on IsBlocked() BEFORE
	// validating and only calls RecordFailure() afterwards without
	// inspecting its return. So the 3 failing attempts that reach
	// maxFailures=3 each fall through to the downstream 401, and the
	// lockout set by the 3rd failure is only observed starting with the
	// NEXT (4th) request. This is a real, intentional-looking asymmetry
	// with the login path (recorded here rather than "fixed" in the Test
	// phase); see the accompanying bd comment.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/sessions", nil)
		req.RemoteAddr = ip
		req.Header.Set("Authorization", "Bearer wrong-token")
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("attempt %d: status = %d, want %d", i+1, w.Code, http.StatusUnauthorized)
		}
	}

	// The 4th request observes the lockout set by the 3rd failure.
	reqBlocked := httptest.NewRequest("GET", "/api/sessions", nil)
	reqBlocked.RemoteAddr = ip
	reqBlocked.Header.Set("Authorization", "Bearer wrong-token")
	reqBlocked = reqBlocked.WithContext(context.WithValue(reqBlocked.Context(), ContextKeyExternalConnection, true))
	wBlocked := httptest.NewRecorder()
	middleware.ServeHTTP(wBlocked, reqBlocked)

	if wBlocked.Code != http.StatusTooManyRequests {
		t.Errorf("4th attempt: status = %d, want %d", wBlocked.Code, http.StatusTooManyRequests)
	}
	if wBlocked.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header on rate-limited response")
	}

	// Now even a VALID token from the same IP is rejected while locked out.
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.RemoteAddr = ip
	req.Header.Set("Authorization", "Bearer s3cr3t-token")
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("valid token during lockout: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// A different IP is unaffected and the valid token still works.
	req2 := httptest.NewRequest("GET", "/api/sessions", nil)
	req2.RemoteAddr = "203.0.113.99:12345"
	req2.Header.Set("Authorization", "Bearer s3cr3t-token")
	req2 = req2.WithContext(context.WithValue(req2.Context(), ContextKeyExternalConnection, true))
	w2 := httptest.NewRecorder()
	middleware.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("different IP with valid token: status = %d, want %d", w2.Code, http.StatusOK)
	}
}

// TestAuthMiddleware_SharedToken_NeverLogsTokenValue captures slog output
// during valid and invalid bearer-token requests and asserts the raw token
// value never appears in any log line, only client_ip/path/decision.
func TestAuthMiddleware_SharedToken_NeverLogsTokenValue(t *testing.T) {
	const secretToken = "super-secret-do-not-log-xyz789"

	am := NewAuthManager(&config.WebAuth{
		Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
		SharedToken: secretToken,
	})
	defer am.Close()
	am.SetAPIPrefix("")

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := am.AuthMiddleware(testHandler)

	// logging.Auth() resolves to slog.Default() whenever logging.Initialize has
	// not set a global logger (the case in this test binary), so redirecting
	// the process-wide default captures everything AuthMiddleware logs.
	var buf strings.Builder
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prevDefault)

	for _, hdr := range []string{"Bearer " + secretToken, "Bearer wrong-" + secretToken} {
		req := httptest.NewRequest("GET", "/api/sessions", nil)
		req.RemoteAddr = "203.0.113.1:54321"
		req.Header.Set("Authorization", hdr)
		req = req.WithContext(context.WithValue(req.Context(), ContextKeyExternalConnection, true))
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, req)
	}

	if strings.Contains(buf.String(), secretToken) {
		t.Errorf("log output leaked the shared token value:\n%s", buf.String())
	}
}

func TestAuthManager_ShouldWarnSplitIP(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
	})
	defer am.Close()

	const key = "192.168.1.0|some-ua"

	if !am.shouldWarnSplitIP(key) {
		t.Error("first call for a key should warn")
	}
	if am.shouldWarnSplitIP(key) {
		t.Error("second call within the dedup window should not warn again")
	}

	// A different key should warn independently.
	if !am.shouldWarnSplitIP("10.0.0.0|other-ua") {
		t.Error("a different key should warn")
	}

	// Simulate the dedup window having elapsed by backdating the seen time.
	am.splitIPWarnMu.Lock()
	am.splitIPWarnSeen[key] = time.Now().Add(-splitIPWarnWindow - time.Second)
	am.splitIPWarnMu.Unlock()

	if !am.shouldWarnSplitIP(key) {
		t.Error("call after the dedup window elapsed should warn again")
	}
}

// TestAuthManager_HandleLogin_SplitIP_RaisesAnomalyFlag verifies that a CSRF
// token IP fingerprint mismatch during login writes SplitIP=true into the
// mutable *AuthAnomaly context holder for BOTH mobile and desktop user agents.
// This is the audit-trail promotion path used by AccessLogger to suffix
// EventType with "+split_ip"; the flag must be raised regardless of the
// mitto.log dedup window and regardless of UA class.
func TestAuthManager_HandleLogin_SplitIP_RaisesAnomalyFlag(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "admin", Password: "secret"},
	})
	defer am.Close()

	// Build a CSRF cookie whose fingerprint was embedded for a DIFFERENT
	// network prefix than the login request's IP, so VerifyIPFromToken
	// will report a mismatch.
	const issuedIP = "10.20.30.40"
	baseToken := strings.Repeat("a", csrfTokenLength*2) // 64 hex chars
	cases := []struct {
		name string
		ua   string
	}{
		{"mobile UA (iPhone Safari)", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7) Safari/604.1"},
		{"desktop UA (Chrome on macOS)", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120.0.0.0 Safari/537.36"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cookieValue := embedFingerprint(baseToken, issuedIP, tc.ua)

			req := httptest.NewRequest("POST", "/api/login",
				strings.NewReader(`{"username":"admin","password":"wrong"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", tc.ua)
			// Login POST arrives from a different network prefix (/24) than
			// the one baked into the CSRF cookie.
			req.RemoteAddr = "192.168.1.100:12345"
			req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookieValue})

			anomaly := &AuthAnomaly{}
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyAuthAnomaly, anomaly))

			w := httptest.NewRecorder()
			am.HandleLogin(w, req)

			if !anomaly.SplitIP {
				t.Errorf("AuthAnomaly.SplitIP = false, want true (UA class must not gate the audit flag)")
			}
		})
	}
}

// TestAuthManager_HandleLogin_SplitIP_MatchingFingerprintDoesNotFlag verifies
// the negative path: when the CSRF cookie's embedded fingerprint matches the
// request IP+UA (the normal case), no anomaly is recorded.
func TestAuthManager_HandleLogin_SplitIP_MatchingFingerprintDoesNotFlag(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "admin", Password: "secret"},
	})
	defer am.Close()

	const clientIP = "192.168.1.100"
	const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120.0.0.0 Safari/537.36"
	baseToken := strings.Repeat("b", csrfTokenLength*2)
	cookieValue := embedFingerprint(baseToken, clientIP, ua)

	req := httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"username":"admin","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = clientIP + ":12345"
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookieValue})

	anomaly := &AuthAnomaly{}
	req = req.WithContext(context.WithValue(req.Context(), ContextKeyAuthAnomaly, anomaly))

	w := httptest.NewRecorder()
	am.HandleLogin(w, req)

	if anomaly.SplitIP {
		t.Error("AuthAnomaly.SplitIP = true on matching fingerprint, want false")
	}
}

func TestAuthManager_PruneSplitIPWarnSeen(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "user", Password: "pass"},
	})
	defer am.Close()

	am.splitIPWarnMu.Lock()
	am.splitIPWarnSeen["stale"] = time.Now().Add(-splitIPWarnWindow - time.Second)
	am.splitIPWarnSeen["fresh"] = time.Now()
	am.splitIPWarnMu.Unlock()

	am.pruneSplitIPWarnSeen()

	am.splitIPWarnMu.Lock()
	_, staleExists := am.splitIPWarnSeen["stale"]
	_, freshExists := am.splitIPWarnSeen["fresh"]
	am.splitIPWarnMu.Unlock()

	if staleExists {
		t.Error("stale entry should have been pruned")
	}
	if !freshExists {
		t.Error("fresh entry should not have been pruned")
	}
}

// --- SetSharedToken (mitto-pscc.9 rotation) -------------------------------

// TestAuthManager_SetSharedToken_SwapsAcceptedCredential verifies that
// rotating the shared token via SetSharedToken atomically flips which token
// value ValidateSharedToken accepts: the old value must be rejected and the
// new value accepted immediately after the call returns.
func TestAuthManager_SetSharedToken_SwapsAcceptedCredential(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{SharedToken: "old-token"})
	defer am.Close()

	if !am.ValidateSharedToken("old-token") {
		t.Fatal("old-token should be accepted before rotation")
	}

	am.SetSharedToken("new-token")

	if am.ValidateSharedToken("old-token") {
		t.Error("old-token should be rejected after rotation")
	}
	if !am.ValidateSharedToken("new-token") {
		t.Error("new-token should be accepted after rotation")
	}
}

// TestAuthManager_SetSharedToken_NilConfig verifies SetSharedToken does not
// panic when the AuthManager was constructed with a nil config (auth
// disabled) — it lazily allocates a config so a shared token can still be
// installed dynamically (not exercised by any current caller, but rotate's
// implementation must not assume a.config is always non-nil).
func TestAuthManager_SetSharedToken_NilConfig(t *testing.T) {
	am := NewAuthManager(nil)
	defer am.Close()

	am.SetSharedToken("tok")

	if !am.HasSharedToken() {
		t.Fatal("HasSharedToken() = false after SetSharedToken on a nil-config manager")
	}
	if !am.ValidateSharedToken("tok") {
		t.Error("ValidateSharedToken(tok) = false after SetSharedToken on a nil-config manager")
	}
}

// TestAuthManager_SharedToken_ConcurrentRotateAndValidate_Race exercises
// SetSharedToken concurrently with HasSharedToken/ValidateSharedToken (the
// hot read path AuthMiddleware calls on every request) to catch data races
// under `go test -race` (mitto-pscc.9: HasSharedToken/ValidateSharedToken
// moved from unlocked reads to a.mu.RLock specifically to make this safe).
func TestAuthManager_SharedToken_ConcurrentRotateAndValidate_Race(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{SharedToken: "initial-token"})
	defer am.Close()

	const iterations = 200
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			am.SetSharedToken("token-" + string(rune('a'+i%26)))
		}
	}()

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = am.HasSharedToken()
				_ = am.ValidateSharedToken("token-" + string(rune('a'+i%26)))
			}
		}()
	}

	wg.Wait()
}
