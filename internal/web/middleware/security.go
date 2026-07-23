package middleware

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// StatusClientClosedRequest mirrors nginx's non-standard 499 status: the
// client closed the connection before the server produced a response. Used
// by RequestTimeoutMiddleware to distinguish a client cancellation from a
// genuine server-side timeout (which remains 503). See mitto-axsg.
const StatusClientClosedRequest = 499

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

// SecurityHeadersMiddleware adds security headers to all responses.
func SecurityHeadersMiddleware(config SecurityConfig) func(http.Handler) http.Handler {
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

// RequestSizeLimitMiddleware limits the size of request bodies.
func RequestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only limit POST, PUT, PATCH requests
			if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
				// Multipart uploads (images, files, save-file) enforce their own
				// per-endpoint MaxBytesReader; the generic cap would silently
				// clamp legitimate uploads well below what handlers advertise.
				ct := r.Header.Get("Content-Type")
				if !strings.HasPrefix(strings.ToLower(ct), "multipart/") {
					r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
				}
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

// HideServerInfoMiddleware removes or obscures server identification from responses.
func HideServerInfoMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &hideServerInfoResponseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
	})
}

// DefaultRequestTimeout is the default timeout for HTTP requests. Sized to
// sit above auxBackedRequestTimeout so bd/aux-backed handlers can emit a
// clear, retryable 503 before http.TimeoutHandler forces an opaque one.
const DefaultRequestTimeout = 60 * time.Second

// RequestTimeoutMiddleware adds a timeout to HTTP requests.
// WebSocket upgrade requests are excluded from the timeout.
// Callers may also pass exemptPaths (exact-match on r.URL.Path) to opt
// specific handlers out of both the timeout and the TimeoutHandler panic
// recovery. Intended for handlers that run long subprocesses with their own
// timeout budget, e.g. bd migrate — where the outer 60s cap would otherwise
// preempt the handler's minutes-long budget with an opaque "Request timeout".
// This middleware includes panic recovery to handle the known issue where
// http.TimeoutHandler can cause nil pointer dereferences when the underlying
// handler writes to the ResponseWriter after a timeout has occurred.
//
// It also rewrites the bodyless 503 that http.TimeoutHandler emits when the
// request context is canceled by the client (see mitto-axsg): Go's stdlib
// TimeoutHandler conflates context.Canceled (client disconnect) and
// context.DeadlineExceeded (server-side timeout), writing 503 for both — with
// no body on the Canceled path. That produces "503 body_bytes=0" entries in
// access.log for what were legitimate client-initiated cancellations. Here,
// on client cancellation, the 503 is rewritten to 499 (nginx's
// StatusClientClosedRequest) so real server timeouts remain distinguishable
// from client aborts in the access log.
func RequestTimeoutMiddleware(timeout time.Duration, exemptPaths ...string) func(http.Handler) http.Handler {
	// Build the exempt set once at middleware construction, not per-request.
	exempt := make(map[string]struct{}, len(exemptPaths))
	for _, p := range exemptPaths {
		exempt[p] = struct{}{}
	}
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

			// Skip timeout and the TimeoutHandler panic-recovery wrapper for
			// exempted paths; those handlers own their timeout budget.
			if _, ok := exempt[r.URL.Path]; ok {
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

			// Wrap the outer ResponseWriter so a 503 emitted by
			// http.TimeoutHandler on client cancellation gets rewritten to 499
			// (see mitto-axsg). A real deadline-exceeded timeout is left as
			// 503 because on that path the request context error is
			// context.DeadlineExceeded, not context.Canceled.
			cw := &clientCancelResponseWriter{ResponseWriter: w, req: r}
			timeoutHandler.ServeHTTP(cw, r)
		})
	}
}

// clientCancelResponseWriter intercepts WriteHeader to distinguish a 503
// written by http.TimeoutHandler on client cancellation (context.Canceled)
// from a 503 written on genuine server-side timeout (context.DeadlineExceeded).
// The former is rewritten to StatusClientClosedRequest so access.log records
// the client abort separately from real server-side unavailability.
type clientCancelResponseWriter struct {
	http.ResponseWriter
	req         *http.Request
	wroteHeader bool
}

func (w *clientCancelResponseWriter) WriteHeader(statusCode int) {
	if !w.wroteHeader && statusCode == http.StatusServiceUnavailable {
		if err := w.req.Context().Err(); errors.Is(err, context.Canceled) {
			statusCode = StatusClientClosedRequest
		}
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *clientCancelResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Hijack, Flush, Unwrap forwarders keep the wrapper transparent to WebSocket
// upgrades, streaming responses, and downstream middleware chains.
func (w *clientCancelResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *clientCancelResponseWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func (w *clientCancelResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
