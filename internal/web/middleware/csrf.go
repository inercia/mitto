package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
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

	// tokenAuthChecker, when set, lets CSRFMiddleware exempt requests carrying a
	// valid shared-token bearer credential (mitto-7gta.26) from the double-submit
	// cookie check. CSRF wraps OUTSIDE auth in the middleware chain (server.go),
	// so it cannot read an auth decision from the request context — this checker
	// is the seam that lets it validate the token independently. It MUST validate
	// the token, not merely detect the Authorization header: exempting on header
	// presence alone would be a CSRF bypass for cookie-authenticated browsers.
	tokenAuthChecker func(*http.Request) bool
}

// NewCSRFManager creates a new CSRF manager.
func NewCSRFManager() *CSRFManager {
	return &CSRFManager{}
}

// SetAPIPrefix sets the API prefix for checking exempt paths.
func (c *CSRFManager) SetAPIPrefix(prefix string) {
	c.apiPrefix = prefix
}

// SetTokenAuthChecker installs a validator that CSRFMiddleware consults to
// exempt requests authenticated via a shared bearer token. Pass nil to disable
// the exemption (the default; existing double-submit-cookie behaviour only).
func (c *CSRFManager) SetTokenAuthChecker(checker func(*http.Request) bool) {
	c.tokenAuthChecker = checker
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

// normalizeIPForFingerprint coarsens ip to its containing network prefix
// (/24 for IPv4, /64 for IPv6) so that benign IP drift within the same
// network (mobile carrier NAT, Wi-Fi/cellular handoff) does not change the
// fingerprint. Falls back to returning ip unchanged if it is empty or
// cannot be parsed.
func normalizeIPForFingerprint(ip string) string {
	if ip == "" {
		return ip
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if ip4 := parsed.To4(); ip4 != nil {
		return ip4.Mask(net.CIDRMask(24, 32)).String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}

// embedFingerprint appends a non-reversible client fingerprint to a bare CSRF token.
//
// Format: {64-char-hex-token}.{16-char-hex-sha256-prefix(token+network-prefix+user-agent)}
//
// The fingerprint lets the server detect split-IP login patterns (the token was
// issued to one client and then the POST arrives from a different one) without
// storing any server-side state. The IP is coarsened to its /24 (IPv4) or /64
// (IPv6) network prefix so that benign IP drift within the same network doesn't
// trip the check, and the User-Agent is folded in so a genuinely different
// client still trips it. Because the fingerprint is derived from the random
// token as well, an attacker cannot forge a valid fingerprint for a new
// network/UA without knowing the original token value.
func embedFingerprint(token, ip, userAgent string) string {
	if ip == "" {
		return token
	}
	h := sha256.New()
	h.Write([]byte(token))
	h.Write([]byte(normalizeIPForFingerprint(ip)))
	h.Write([]byte(userAgent))
	return token + csrfIPHashSep + hex.EncodeToString(h.Sum(nil)[:csrfIPHashLen])
}

// VerifyIPFromToken returns true when the client fingerprint embedded in tokenValue
// matches ip and userAgent (after coarsening ip to its network prefix).
//
// Returns true (no anomaly) when tokenValue has no embedded fingerprint — this handles
// tokens that were issued before this feature was deployed (graceful degradation).
func VerifyIPFromToken(tokenValue, ip, userAgent string) bool {
	idx := strings.LastIndex(tokenValue, csrfIPHashSep)
	if idx < 0 {
		return true // Old format — no fingerprint to check
	}
	base := tokenValue[:idx]
	if len(base) != csrfTokenLength*2 { // each byte → 2 hex chars
		return true // Unexpected format — skip check to avoid false positives
	}
	storedHash := tokenValue[idx+1:]
	expected := embedFingerprint(base, ip, userAgent)
	expectedHash := expected[idx+1:]
	// Constant-time compare to prevent timing-based fingerprint enumeration
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(expectedHash)) == 1
}

// SetCSRFCookie sets the CSRF token cookie on the response.
// The request is used to determine if we're on localhost (to set Secure flag appropriately).
func (c *CSRFManager) SetCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	secure := shouldUseSecureCookie(r)

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

	// Embed the issuing IP + User-Agent as a fingerprint so that HandleLogin can
	// detect split-IP anomalies (auth page loaded from one client, POST from
	// another). The double-submit cookie pattern (cookie == header) is unchanged
	// because both the cookie and the JavaScript-read header value carry the
	// same full string (token + "." + fingerprint-hash).
	ip := GetClientIPWithProxyCheck(r)
	tokenWithIP := embedFingerprint(token, ip, r.Header.Get("User-Agent"))

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
	"/api/login":                 true, // Login has rate limiting, no session yet
	"/api/webauthn/login/begin":  true, // Pre-auth (mitto-4mz.4); the WebAuthn assertion itself is CSRF-resistant
	"/api/webauthn/login/finish": true, // See /api/webauthn/login/begin — same pre-auth rationale
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

		// Skip CSRF check for requests carrying a valid shared bearer token
		// (mitto-7gta.26). Programmatic clients (SDK, CLI) authenticate via
		// Authorization: Bearer <token> and never hold the CSRF cookie/header
		// pair a browser session would. The checker VALIDATES the token, not
		// merely detects the header, so a garbage/invalid header still falls
		// through to the normal double-submit cookie enforcement below.
		if c.tokenAuthChecker != nil && c.tokenAuthChecker(r) {
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
				"client_ip", GetClientIPWithProxyCheck(r))
			writeErrorJSON(w, http.StatusForbidden, "", "CSRF token required")
			return
		}

		// Constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) != 1 {
			logging.Web().Warn("CSRF token mismatch",
				"method", r.Method,
				"path", r.URL.Path,
				"client_ip", GetClientIPWithProxyCheck(r))
			writeErrorJSON(w, http.StatusForbidden, "", "CSRF token mismatch")
			return
		}

		next.ServeHTTP(w, r)
	})
}
