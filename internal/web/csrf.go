package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/logging"
)

const (
	// csrfTokenLength is the length of the CSRF token in bytes (32 bytes = 256 bits)
	csrfTokenLength = 32

	// csrfTokenHeader is the HTTP header name for CSRF tokens
	csrfTokenHeader = "X-CSRF-Token"

	// csrfCookieName is the name of the cookie that holds the CSRF token
	csrfCookieName = "mitto_csrf"

	// csrfTokenDuration is how long a CSRF token is valid
	csrfTokenDuration = 24 * time.Hour

	// csrfCleanupInterval is how often to clean up expired tokens
	csrfCleanupInterval = 5 * time.Minute
)

// CSRFToken represents a CSRF token with its expiration.
type CSRFToken struct {
	Token     string
	ExpiresAt time.Time
}

// CSRFManager manages CSRF tokens.
type CSRFManager struct {
	tokens      map[string]*CSRFToken // token -> CSRFToken
	mu          sync.RWMutex
	stopCleanup chan struct{}
	cleanupDone chan struct{}
	apiPrefix   string // API prefix for checking exempt paths
}

// NewCSRFManager creates a new CSRF manager.
func NewCSRFManager() *CSRFManager {
	cm := &CSRFManager{
		tokens:      make(map[string]*CSRFToken),
		stopCleanup: make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}
	go cm.cleanupLoop()
	return cm
}

// SetAPIPrefix sets the API prefix for checking exempt paths.
func (c *CSRFManager) SetAPIPrefix(prefix string) {
	c.apiPrefix = prefix
}

// Close stops the cleanup goroutine.
func (c *CSRFManager) Close() {
	close(c.stopCleanup)
	<-c.cleanupDone
}

// cleanupLoop periodically removes expired tokens.
func (c *CSRFManager) cleanupLoop() {
	defer close(c.cleanupDone)
	ticker := time.NewTicker(csrfCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanupExpiredTokens()
		case <-c.stopCleanup:
			return
		}
	}
}

// cleanupExpiredTokens removes all expired CSRF tokens.
func (c *CSRFManager) cleanupExpiredTokens() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for token, csrfToken := range c.tokens {
		if now.After(csrfToken.ExpiresAt) {
			delete(c.tokens, token)
		}
	}
}

// GenerateToken creates a new CSRF token.
func (c *CSRFManager) GenerateToken() (string, error) {
	bytes := make([]byte, csrfTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.tokens[token] = &CSRFToken{
		Token:     token,
		ExpiresAt: time.Now().Add(csrfTokenDuration),
	}

	return token, nil
}

// ValidateToken checks if a CSRF token is valid.
func (c *CSRFManager) ValidateToken(token string) bool {
	if token == "" {
		return false
	}

	c.mu.RLock()
	csrfToken, exists := c.tokens[token]
	c.mu.RUnlock()

	if !exists {
		return false
	}

	if time.Now().After(csrfToken.ExpiresAt) {
		// Token expired, remove it
		c.mu.Lock()
		delete(c.tokens, token)
		c.mu.Unlock()
		return false
	}

	return true
}

// ValidateTokenConstantTime validates token with constant-time comparison.
// This prevents timing attacks on token guessing.
func (c *CSRFManager) ValidateTokenConstantTime(token string) bool {
	if token == "" {
		return false
	}

	c.mu.RLock()
	csrfToken, exists := c.tokens[token]
	c.mu.RUnlock()

	if !exists {
		return false
	}

	if time.Now().After(csrfToken.ExpiresAt) {
		c.mu.Lock()
		delete(c.tokens, token)
		c.mu.Unlock()
		return false
	}

	// Constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(token), []byte(csrfToken.Token)) == 1
}

// SetCSRFCookie sets the CSRF token cookie on the response.
func (c *CSRFManager) SetCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // JavaScript needs to read this
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(csrfTokenDuration.Seconds()),
	})
}

// GetTokenFromRequest gets the CSRF token from request header or cookie.
func (c *CSRFManager) GetTokenFromRequest(r *http.Request) string {
	// First try header (preferred for AJAX requests)
	token := r.Header.Get(csrfTokenHeader)
	if token != "" {
		return token
	}

	// Fall back to cookie for the double-submit pattern
	cookie, err := r.Cookie(csrfCookieName)
	if err == nil {
		return cookie.Value
	}

	return ""
}

// HandleCSRFToken handles GET /api/csrf-token to get a new CSRF token.
func (c *CSRFManager) HandleCSRFToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token, err := c.GenerateToken()
	if err != nil {
		logging.Web().Error("Failed to generate CSRF token", "error", err)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	// Set cookie so subsequent requests can use it
	c.SetCSRFCookie(w, token)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

// isStateChangingMethod returns true for HTTP methods that change state.
func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// csrfExemptAPIPaths are API paths that don't require CSRF protection.
// Note: login is exempt because it has its own rate limiting and uses
// credentials for authentication. WebSocket upgrades are also exempt.
var csrfExemptAPIPaths = map[string]bool{
	"/api/login": true, // Login has rate limiting, no session yet
}

// isCSRFExemptPath checks if a path is exempt from CSRF protection.
// It checks both static paths and API paths (with the configured prefix).
func (c *CSRFManager) isCSRFExemptPath(path string) bool {
	// Check API paths with prefix
	for apiPath := range csrfExemptAPIPaths {
		if path == c.apiPrefix+apiPath {
			return true
		}
	}
	return false
}

// CSRFMiddleware returns a middleware that validates CSRF tokens on state-changing requests.
func (c *CSRFManager) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip CSRF check for safe methods (GET, HEAD, OPTIONS)
		if !isStateChangingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		// Skip CSRF check for exempt paths
		if c.isCSRFExemptPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Skip CSRF check for WebSocket upgrade requests
		if r.Header.Get("Upgrade") == "websocket" {
			next.ServeHTTP(w, r)
			return
		}

		// Get CSRF token from header
		headerToken := r.Header.Get(csrfTokenHeader)

		// Double-submit cookie pattern: also check cookie for comparison
		cookieToken := ""
		if cookie, err := r.Cookie(csrfCookieName); err == nil {
			cookieToken = cookie.Value
		}

		// Validate that header token exists and matches cookie (double-submit pattern)
		if headerToken == "" {
			logging.Web().Warn("CSRF token missing",
				"method", r.Method,
				"path", r.URL.Path,
				"client_ip", getClientIPWithProxyCheck(r))
			http.Error(w, "CSRF token required", http.StatusForbidden)
			return
		}

		// Validate token exists in our token store
		if !c.ValidateTokenConstantTime(headerToken) {
			logging.Web().Warn("CSRF token invalid",
				"method", r.Method,
				"path", r.URL.Path,
				"client_ip", getClientIPWithProxyCheck(r))
			http.Error(w, "Invalid CSRF token", http.StatusForbidden)
			return
		}

		// Double-submit pattern: verify header token matches cookie token
		if cookieToken != "" && subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) != 1 {
			logging.Web().Warn("CSRF token mismatch",
				"method", r.Method,
				"path", r.URL.Path,
				"client_ip", getClientIPWithProxyCheck(r))
			http.Error(w, "CSRF token mismatch", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

