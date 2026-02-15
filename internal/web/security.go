package web

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
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

			// Note: Content-Security-Policy is set by cspNonceMiddleware for HTML responses
			// to enable nonce-based script loading instead of 'unsafe-inline'.
			// For non-HTML responses, cspNonceMiddleware sets a stricter CSP without inline scripts.

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

// Flush implements http.Flusher to support streaming responses.
func (w *hideServerInfoResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for interface detection.
// This is required for proper compatibility with http.TimeoutHandler and other middleware.
func (w *hideServerInfoResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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
// This middleware includes panic recovery to handle the known issue where
// http.TimeoutHandler can cause nil pointer dereferences when the underlying
// handler writes to the ResponseWriter after a timeout has occurred.
func requestTimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// Create the timeout handler once during middleware setup, not per-request.
		// This avoids potential race conditions and is more efficient.
		timeoutHandler := http.TimeoutHandler(next, timeout, "Request timeout")

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip timeout for WebSocket upgrade requests
			if r.Header.Get("Upgrade") == "websocket" {
				next.ServeHTTP(w, r)
				return
			}

			// Recover from panics that can occur in http.TimeoutHandler.
			// This is a known issue where writing to the ResponseWriter after
			// a timeout can cause a nil pointer dereference in the standard library.
			defer func() {
				if rec := recover(); rec != nil {
					// Check if this is the known TimeoutHandler nil pointer issue
					// by looking at the panic value AND the stack trace to confirm
					// it originated from http.TimeoutHandler.
					if err, ok := rec.(error); ok {
						if err.Error() == "runtime error: invalid memory address or nil pointer dereference" {
							// Get the stack trace to verify this is from TimeoutHandler
							buf := make([]byte, 4096)
							n := runtime.Stack(buf, false)
							stackTrace := string(buf[:n])

							// Check if the panic originated from http.TimeoutHandler
							if strings.Contains(stackTrace, "net/http.(*timeoutWriter)") ||
								strings.Contains(stackTrace, "net/http.TimeoutHandler") {
								// This is the known TimeoutHandler issue - log it and continue.
								// The client already received a timeout response.
								slog.Debug("Recovered from TimeoutHandler nil pointer panic",
									"method", r.Method,
									"path", r.URL.Path,
									"remote_addr", r.RemoteAddr,
								)
								return
							}
						}
					}
					// Log and re-panic for unexpected errors
					slog.Error("Unexpected panic in request handler",
						"panic", rec,
						"method", r.Method,
						"path", r.URL.Path,
						"remote_addr", r.RemoteAddr,
					)
					panic(rec)
				}
			}()

			// Apply timeout
			timeoutHandler.ServeHTTP(w, r)
		})
	}
}
