package web

// middleware_wiring.go contains Server methods that bridge internal/web and
// internal/web/middleware. These two methods must live in package web because
// they reference Server fields (s.wsSecurityConfig, s.defense, s.accessLogger).
// All pure middleware logic lives in the middleware sub-package.

import (
	"bufio"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/defense"
	"github.com/inercia/mitto/internal/web/middleware"
)

// getSecureUpgraderForRequest returns a WebSocket upgrader with security checks,
// with compression enabled only for external connections.
//
// Compression trade-offs:
//   - External (Tailscale, etc.): High latency, limited bandwidth → compression beneficial
//   - Local (macOS app, localhost): Zero latency, unlimited bandwidth → compression overhead not worth it
func (s *Server) getSecureUpgraderForRequest(r *http.Request) websocket.Upgrader {
	var logger middleware.OriginCheckLogger
	if s.logger != nil {
		logger = func(origin, host string, allowed bool, reason string) {
			s.logger.Debug("WS: Origin check",
				"origin", origin,
				"host", host,
				"allowed", allowed,
				"reason", reason)
		}
	}

	// Enable compression only for external connections where bandwidth savings matter.
	// Local connections skip compression to avoid unnecessary CPU overhead.
	enableCompression := middleware.IsExternalConnection(r)

	if enableCompression && s.logger != nil {
		s.logger.Debug("WS: Enabling compression for external connection",
			"client_ip", middleware.GetClientIPWithProxyCheck(r))
	}

	// Allow authenticated external connections (e.g., Tailscale funnel)
	// These have already been authenticated by the auth middleware
	return middleware.CreateSecureUpgrader(s.wsSecurityConfig, logger, middleware.IsExternalConnection, enableCompression)
}

// defenseRecordingMiddleware records requests for analysis by the scanner defense system.
// Only records requests from external connections (not localhost).
// For already-blocked IPs, silently drops the connection without sending any HTTP response.
// This is especially important for Tailscale Funnel where the FilteredListener cannot see
// real client IPs (traffic arrives from Tailscale relay servers via utun interface).
func (s *Server) defenseRecordingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.defense == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Only apply to external connections
		isExternal, _ := r.Context().Value(middleware.ContextKeyExternalConnection).(bool)
		if !isExternal {
			next.ServeHTTP(w, r)
			return
		}

		ip := middleware.GetClientIPWithProxyCheck(r)

		// For already-blocked IPs: silently drop the connection.
		// Don't send any response — not even a 403 — to give the scanner
		// zero information about the server's existence.
		if s.defense.IsBlocked(ip) {
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					conn.Close()

					if s.accessLogger != nil {
						s.accessLogger.Write(LogEntry{
							Timestamp:    time.Now(),
							ClientIP:     ip,
							Method:       r.Method,
							Path:         r.URL.Path,
							StatusCode:   0,
							EventType:    "blocked_http",
							ErrorMessage: s.defense.GetBlockReason(ip),
							IsExternal:   true,
						})
					}
					return
				}
			}
			// Fallback if hijack not available: close without body
			w.WriteHeader(http.StatusForbidden)
			return
		}

		// Wrap response writer to capture status code
		wrapped := &defenseStatusRecorder{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(wrapped, r)

		// Record request for analysis (async to not block response)
		go s.defense.RecordRequest(ip, &defense.RequestInfo{
			Path:       r.URL.Path,
			Method:     r.Method,
			StatusCode: wrapped.statusCode,
			UserAgent:  r.UserAgent(),
			Timestamp:  time.Now(),
		})
	})
}

// defenseStatusRecorder wraps http.ResponseWriter to capture the status code.
type defenseStatusRecorder struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

func (r *defenseStatusRecorder) WriteHeader(code int) {
	if !r.headerWritten {
		r.statusCode = code
		r.headerWritten = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *defenseStatusRecorder) Write(b []byte) (int, error) {
	if !r.headerWritten {
		r.statusCode = http.StatusOK
		r.headerWritten = true
	}
	return r.ResponseWriter.Write(b)
}

// Hijack implements http.Hijacker for WebSocket support.
func (r *defenseStatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Flush implements http.Flusher for streaming support.
func (r *defenseStatusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter.
func (r *defenseStatusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
