package web

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"
)

// SecurityConfig holds security-related configuration.
type SecurityConfig struct {
	// EnableHSTS enables HTTP Strict Transport Security header.
	// Only enable this if you're serving over HTTPS.
	EnableHSTS bool
	// HSTSMaxAge is the max-age value for HSTS in seconds (default: 1 year).
	HSTSMaxAge int
}

// DefaultSecurityConfig returns the default security configuration.
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		EnableHSTS: false,    // Disabled by default, enable when behind HTTPS
		HSTSMaxAge: 31536000, // 1 year
	}
}

// securityHeadersMiddleware adds security headers to all responses.
func securityHeadersMiddleware(config SecurityConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Prevent MIME type sniffing
			w.Header().Set("X-Content-Type-Options", "nosniff")

			// Prevent clickjacking
			w.Header().Set("X-Frame-Options", "DENY")

			// XSS protection (legacy but still useful for older browsers)
			w.Header().Set("X-XSS-Protection", "1; mode=block")

			// Referrer policy - don't leak referrer to other origins
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			// Permissions policy - disable unnecessary browser features
			w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

			// Content Security Policy
			// Allow scripts from self and specific CDNs used by the frontend
			csp := "default-src 'self'; " +
				"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://cdn.skypack.dev; " +
				"style-src 'self' 'unsafe-inline'; " +
				"img-src 'self' data:; " +
				"font-src 'self'; " +
				"connect-src 'self' ws: wss:; " +
				"frame-ancestors 'none'; " +
				"base-uri 'self'; " +
				"form-action 'self'"
			w.Header().Set("Content-Security-Policy", csp)

			// HSTS - only if enabled (should only be used with HTTPS)
			if config.EnableHSTS {
				hstsValue := "max-age=31536000; includeSubDomains"
				if config.HSTSMaxAge > 0 {
					hstsValue = "max-age=" + itoa(config.HSTSMaxAge) + "; includeSubDomains"
				}
				w.Header().Set("Strict-Transport-Security", hstsValue)
			}

			// Cross-Origin isolation headers for additional protection
			// COOP: Prevents other origins from opening this page in a popup
			w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
			// CORP: Prevents other origins from embedding this page's resources
			w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

			// Remove server identification headers
			w.Header().Del("Server")
			w.Header().Del("X-Powered-By")

			next.ServeHTTP(w, r)
		})
	}
}

// itoa converts an integer to a string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// requestSizeLimitMiddleware limits the size of request bodies.
func requestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only limit POST, PUT, PATCH requests
			if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// hideServerInfoMiddleware removes or obscures server identification from responses.
// This is applied as a wrapper to ensure headers are removed even if set by other handlers.
type hideServerInfoResponseWriter struct {
	http.ResponseWriter
	headerWritten bool
}

func (w *hideServerInfoResponseWriter) WriteHeader(statusCode int) {
	if !w.headerWritten {
		// Remove server identification headers before writing
		w.Header().Del("Server")
		w.Header().Del("X-Powered-By")
		w.headerWritten = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *hideServerInfoResponseWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Hijack implements http.Hijacker to support WebSocket upgrades.
// The gorilla/websocket upgrader requires the ResponseWriter to implement Hijacker.
func (w *hideServerInfoResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

func hideServerInfoMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &hideServerInfoResponseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
	})
}

// DefaultRequestTimeout is the default timeout for HTTP requests.
const DefaultRequestTimeout = 30 * time.Second

// requestTimeoutMiddleware adds a timeout to HTTP requests.
// WebSocket upgrade requests are excluded from the timeout.
func requestTimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip timeout for WebSocket upgrade requests
			if r.Header.Get("Upgrade") == "websocket" {
				next.ServeHTTP(w, r)
				return
			}

			// Apply timeout using http.TimeoutHandler
			http.TimeoutHandler(next, timeout, "Request timeout").ServeHTTP(w, r)
		})
	}
}
