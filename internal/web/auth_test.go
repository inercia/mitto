package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			name:       "X-Forwarded-For single",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50"},
			want:       "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For multiple",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50, 70.41.3.18, 150.172.238.178"},
			want:       "203.0.113.50",
		},
		{
			name:       "X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Real-IP": "203.0.113.100"},
			want:       "203.0.113.100",
		},
		{
			name:       "X-Forwarded-For takes precedence",
			remoteAddr: "10.0.0.1:12345",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.50",
				"X-Real-IP":       "203.0.113.100",
			},
			want: "203.0.113.50",
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

		// Non-public paths
		{"sessions endpoint", "/mitto/api/sessions", false},
		{"config endpoint", "/mitto/api/config", false},
		{"root", "/", false},
		{"index.html", "/index.html", false},
		{"app.js", "/app.js", false},

		// API paths without prefix (should not match)
		{"login without prefix", "/api/login", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := am.isPublicPath(tt.path); got != tt.want {
				t.Errorf("isPublicPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
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
