package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// CORSConfig holds configuration for cross-origin resource sharing (CORS) on
// REST endpoints. It reuses the same origin allowlist as the WebSocket origin
// check (WebSecurity.AllowedOrigins) so operators configure cross-origin
// browser access — REST and WebSocket — in exactly one place.
type CORSConfig struct {
	// AllowedOrigins is a list of allowed origins for cross-origin browser
	// requests. If empty, CORS is disabled entirely: no CORS headers are
	// emitted and behaviour is identical to before this middleware existed
	// (same-origin browsers work; non-browser clients are unaffected, since
	// CORS is a browser-only mechanism). Use "*" to allow all origins; this
	// is safe here because Access-Control-Allow-Credentials is never
	// emitted — see CORSMiddleware.
	AllowedOrigins []string
}

// DefaultCORSConfig returns the default CORS configuration: no allowed
// origins configured, i.e. CORS is disabled (zero behaviour change).
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{}
}

// Preflight response constants.
const (
	// corsAllowedMethods lists the methods advertised on preflight responses.
	corsAllowedMethods = "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS"

	// corsAllowedHeaders lists the request headers a cross-origin client may
	// send. Authorization covers the shared bearer token (mitto-7gta.26);
	// X-CSRF-Token is listed for completeness even though a cross-origin
	// cookie-authenticated request is never allowed (see CORSMiddleware).
	corsAllowedHeaders = "Authorization, Content-Type, X-CSRF-Token"

	// corsMaxAgeSeconds is how long a browser may cache a preflight result.
	corsMaxAgeSeconds = "600"
)

// CORSMiddleware returns a middleware that emits CORS headers for
// cross-origin browser clients, gated by the same allowlist used for
// WebSocket origins (WebSecurity.AllowedOrigins).
//
// SECURITY: Access-Control-Allow-Credentials is NEVER emitted. Cross-origin
// browser access is bearer-token-only (mitto-7gta.26); the ambient session
// cookie can never ride a cross-origin request that the browser is willing
// to expose to script, so this middleware cannot be used to bypass CSRF
// protection. Same-origin cookie-authenticated requests are completely
// unaffected (no Origin header is required for them to keep working, exactly
// as before this middleware existed).
//
// A disallowed Origin is never rejected outright — the Access-Control-*
// headers are simply omitted (mirroring the WebSocket origin-check posture
// in websocket_security.go) so the browser blocks script from reading the
// response while the request itself still completes normally for
// non-browser callers that happen to set an Origin header. An absent Origin
// header is not treated as an authentication signal; such requests pass
// through untouched.
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
	allowedSet, allowAll := buildOriginAllowlist(cfg.AllowedOrigins)
	enabled := allowAll || len(allowedSet) > 0

	return func(next http.Handler) http.Handler {
		if !enabled {
			// No allowlist configured: zero behaviour change, don't even
			// inspect the Origin header.
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Not a CORS request (non-browser client, or same-origin
				// navigation). Pass through untouched.
				next.ServeHTTP(w, r)
				return
			}

			// Vary: Origin must be set whenever the response depends on the
			// Origin header — allowed or not — so shared/browser caches
			// never serve an allowed-origin response to a disallowed origin
			// or vice versa.
			w.Header().Add("Vary", "Origin")

			if !corsOriginAllowed(origin, allowedSet, allowAll) {
				if isPreflight(r) {
					// Terminate here (do not forward to CSRF/Auth/mux): a
					// disallowed preflight gets a bare 204 with no CORS
					// headers so the browser fails the check.
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			if isPreflight(r) {
				// Preflights carry no credentials, cookies, or CSRF token,
				// so they must be answered here — before CSRF/Auth — rather
				// than forwarded down the chain.
				w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
				w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
				w.Header().Set("Access-Control-Max-Age", corsMaxAgeSeconds)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// Expose Retry-After so SDK clients can read rate-limit/lockout
			// signals on cross-origin responses (browsers hide response
			// headers from script unless explicitly exposed).
			w.Header().Set("Access-Control-Expose-Headers", "Retry-After")

			next.ServeHTTP(w, r)
		})
	}
}

// isPreflight reports whether r is a CORS preflight request: an OPTIONS
// request carrying Access-Control-Request-Method. A bare OPTIONS request
// without that header is not a preflight and is passed through unchanged.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// corsOriginAllowed reports whether origin is permitted by the allowlist
// built by buildOriginAllowlist, matching both the full origin string and
// the bare host — the same two-tier match createOriginChecker uses for
// WebSocket origins.
func corsOriginAllowed(origin string, allowedSet map[string]bool, allowAll bool) bool {
	if allowAll {
		return true
	}
	if allowedSet[strings.ToLower(origin)] {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return allowedSet[strings.ToLower(originURL.Host)]
}
