package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/logging"
)

const (
	// csrfTokenLength is the length of the CSRF token in bytes (32 bytes = 256 bits)
	csrfTokenLength = 32

	// csrfTokenHeader is the HTTP header name for CSRF tokens
	csrfTokenHeader = "X-CSRF-Token"

	// csrfCookieName is the name of the cookie that holds the CSRF token
	csrfCookieName = "mitto_csrf"

	// csrfTokenDuration is how long a CSRF token cookie is valid (7 days)
	csrfTokenDuration = 7 * 24 * 60 * 60 // seconds

	// csrfIPHashSep separates the random token from its embedded IP fingerprint.
	// Using "." keeps the cookie value URL-safe; it is not a valid hex character,
	// so it cannot appear in the base token portion (which is pure hex).
	csrfIPHashSep = "."

	// csrfIPHashLen is the number of bytes taken from the SHA-256 digest for the IP fingerprint.
	// 8 bytes (16 hex chars) provides ample collision resistance for anomaly detection.
	csrfIPHashLen = 8
)

// CSRFManager manages CSRF protection using the double-submit cookie pattern.
// This is a stateless approach where the server doesn't need to store tokens.
// Security is provided by requiring the header token to match the cookie token,
// which an attacker cannot do due to same-origin policy restrictions on cookies.
type CSRFManager struct {
	apiPrefix string // API prefix for checking exempt paths
}

// NewCSRFManager creates a new CSRF manager.
func NewCSRFManager() *CSRFManager {
	return &CSRFManager{}
}

// SetAPIPrefix sets the API prefix for checking exempt paths.
func (c *CSRFManager) SetAPIPrefix(prefix string) {
	c.apiPrefix = prefix
}

// Close is a no-op for the stateless CSRF manager.
func (c *CSRFManager) Close() {
	// No cleanup needed - stateless design
}

// GenerateToken creates a new random CSRF token (64 hex chars, no IP suffix).
func (c *CSRFManager) GenerateToken() (string, error) {
	bytes := make([]byte, csrfTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// embedIPInToken appends a non-reversible IP fingerprint to a bare CSRF token.
//
// Format: {64-char-hex-token}.{16-char-hex-sha256-prefix(token+ip)}
//
// The fingerprint lets the server detect split-IP login patterns (the token was
// issued to one IP and then the POST arrives from a different IP) without storing
// any server-side state.  Because the fingerprint is derived from both the random
// token and the IP, an attacker cannot forge a valid fingerprint for a new IP
// without knowing the original token value.
func embedIPInToken(token, ip string) string {
	if ip == "" {
		return token
	}
	h := sha256.New()
	h.Write([]byte(token))
	h.Write([]byte(ip))
	return token + csrfIPHashSep + hex.EncodeToString(h.Sum(nil)[:csrfIPHashLen])
}

// VerifyIPFromToken returns true when the IP fingerprint embedded in tokenValue matches ip.
//
// Returns true (no anomaly) when tokenValue has no embedded fingerprint — this handles
// tokens that were issued before this feature was deployed (graceful degradation).
func VerifyIPFromToken(tokenValue, ip string) bool {
	idx := strings.LastIndex(tokenValue, csrfIPHashSep)
	if idx < 0 {
		return true // Old format — no IP fingerprint to check
	}
	base := tokenValue[:idx]
	if len(base) != csrfTokenLength*2 { // each byte → 2 hex chars
		return true // Unexpected format — skip check to avoid false positives
	}
	storedHash := tokenValue[idx+1:]
	expected := embedIPInToken(base, ip)
	expectedHash := expected[idx+1:]
	// Constant-time compare to prevent timing-based IP enumeration
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(expectedHash)) == 1
}

// SetCSRFCookie sets the CSRF token cookie on the response.
// The request is used to determine if we're on localhost (to set Secure flag appropriately).
func (c *CSRFManager) SetCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	// Determine if we should set Secure flag.
	// WKWebView (macOS app) doesn't send Secure cookies over http://localhost,
	// so we need to set Secure=false for localhost connections.
	secure := !isLocalhostRequest(r)

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // JavaScript needs to read this
		Secure:   secure,
		SameSite: http.SameSiteLaxMode, // Lax mode for better Safari/iOS compatibility
		MaxAge:   csrfTokenDuration,
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

	// Embed the issuing IP as a fingerprint so that HandleLogin can detect
	// split-IP anomalies (auth page loaded from one IP, POST from another).
	// The double-submit cookie pattern (cookie == header) is unchanged because
	// both the cookie and the JavaScript-read header value carry the same full
	// string (token + "." + ip-hash).
	ip := getClientIPWithProxyCheck(r)
	tokenWithIP := embedIPInToken(token, ip)

	c.SetCSRFCookie(w, r, tokenWithIP)
	writeJSONOK(w, map[string]string{"token": tokenWithIP})
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

	// Check path prefixes for dynamic paths (callback tokens)
	callbackPrefix := c.apiPrefix + "/api/callback/"
	return strings.HasPrefix(path, callbackPrefix)
}

// CSRFMiddleware returns a middleware that validates CSRF tokens on state-changing requests.
// Uses the double-submit cookie pattern: the header token must match the cookie token.
// This is stateless and doesn't require server-side token storage.
//
// CSRF protection is only enforced for external connections (those coming through
// the external listener). Internal/localhost connections skip CSRF checks since
// an attacker would need to be on the same machine to exploit them.
func (c *CSRFManager) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip CSRF check for internal (localhost) connections.
		// CSRF attacks require a victim's browser to make requests to our server,
		// which is only a concern for externally-accessible endpoints.
		if !IsExternalConnection(r) {
			next.ServeHTTP(w, r)
			return
		}

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

		// Get CSRF token from cookie
		cookieToken := ""
		if cookie, err := r.Cookie(csrfCookieName); err == nil {
			cookieToken = cookie.Value
		}

		// Double-submit cookie pattern: both must exist and match
		// An attacker cannot read the cookie value due to same-origin policy,
		// so they cannot set the correct header value.
		if headerToken == "" || cookieToken == "" {
			logging.Web().Warn("CSRF token missing",
				"method", r.Method,
				"path", r.URL.Path,
				"has_header", headerToken != "",
				"has_cookie", cookieToken != "",
				"client_ip", getClientIPWithProxyCheck(r))
			http.Error(w, "CSRF token required", http.StatusForbidden)
			return
		}

		// Constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) != 1 {
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
