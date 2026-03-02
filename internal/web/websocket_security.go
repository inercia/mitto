package web

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketSecurityConfig holds security configuration for WebSocket connections.
type WebSocketSecurityConfig struct {
	// AllowedOrigins is a list of allowed origins for WebSocket connections.
	// If empty, only same-origin requests are allowed.
	// Use "*" to allow all origins (not recommended for production).
	AllowedOrigins []string

	// MaxMessageSize is the maximum size of a WebSocket message in bytes.
	// Default: 64KB
	MaxMessageSize int64

	// MaxConnectionsPerIP is the maximum number of concurrent WebSocket connections per IP.
	// Default: 10
	MaxConnectionsPerIP int

	// PongWait is the time to wait for a pong response.
	// Default: 60 seconds
	PongWait time.Duration

	// PingPeriod is the interval between ping messages.
	// Should be less than PongWait.
	// Default: 54 seconds (90% of PongWait)
	PingPeriod time.Duration

	// WriteWait is the time allowed to write a message.
	// Default: 10 seconds
	WriteWait time.Duration
}

// DefaultWebSocketSecurityConfig returns sensible defaults.
func DefaultWebSocketSecurityConfig() WebSocketSecurityConfig {
	return WebSocketSecurityConfig{
		AllowedOrigins:      nil,       // Same-origin only by default
		MaxMessageSize:      64 * 1024, // 64KB
		MaxConnectionsPerIP: 10,
		PongWait:            60 * time.Second,
		PingPeriod:          54 * time.Second,
		WriteWait:           10 * time.Second,
	}
}

// ConnectionTracker tracks WebSocket connections per IP.
type ConnectionTracker struct {
	mu          sync.RWMutex
	connections map[string]int
	maxPerIP    int
}

// NewConnectionTracker creates a new connection tracker.
func NewConnectionTracker(maxPerIP int) *ConnectionTracker {
	return &ConnectionTracker{
		connections: make(map[string]int),
		maxPerIP:    maxPerIP,
	}
}

// TryAdd attempts to add a connection for the given IP.
// Returns true if the connection is allowed, false if the limit is exceeded.
func (ct *ConnectionTracker) TryAdd(ip string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	current := ct.connections[ip]
	if current >= ct.maxPerIP {
		return false
	}
	ct.connections[ip] = current + 1
	return true
}

// Remove decrements the connection count for the given IP.
func (ct *ConnectionTracker) Remove(ip string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	current := ct.connections[ip]
	if current <= 1 {
		delete(ct.connections, ip)
	} else {
		ct.connections[ip] = current - 1
	}
}

// Count returns the current connection count for an IP.
func (ct *ConnectionTracker) Count(ip string) int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return ct.connections[ip]
}

// TotalConnections returns the total number of tracked connections.
func (ct *ConnectionTracker) TotalConnections() int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	total := 0
	for _, count := range ct.connections {
		total += count
	}
	return total
}

// OriginCheckLogger is a function that logs origin check details.
type OriginCheckLogger func(origin, host string, allowed bool, reason string)

// ExternalConnectionChecker is a function that checks if a request is from an authenticated external connection.
type ExternalConnectionChecker func(r *http.Request) bool

// WebSocket buffer sizes.
// External connections use larger buffers to reduce syscalls over higher-latency networks.
const (
	// Internal (localhost) connections: smaller buffers, no compression overhead
	wsBufferSizeInternal = 1024

	// External connections: larger buffers for better throughput over internet
	wsBufferSizeExternal = 4096
)

// createSecureUpgrader creates a WebSocket upgrader with all security options.
// enableCompression should be true for external connections (Tailscale, etc.) where
// bandwidth is limited and latency is high. For local connections, compression adds
// CPU overhead without network benefit.
func createSecureUpgrader(config WebSocketSecurityConfig, logger OriginCheckLogger, externalChecker ExternalConnectionChecker, enableCompression bool) websocket.Upgrader {
	bufferSize := wsBufferSizeInternal
	if enableCompression {
		bufferSize = wsBufferSizeExternal
	}

	return websocket.Upgrader{
		ReadBufferSize:  bufferSize,
		WriteBufferSize: bufferSize,
		CheckOrigin:     createOriginChecker(config.AllowedOrigins, logger, externalChecker),
		// EnableCompression enables per-message compression (RFC 7692).
		// Only enabled for external connections where bandwidth savings outweigh CPU cost.
		// For localhost connections, compression adds ~1-5% CPU overhead with no benefit.
		EnableCompression: enableCompression,
	}
}

// createOriginChecker returns a function that validates WebSocket origins.
func createOriginChecker(allowedOrigins []string, logger OriginCheckLogger, externalChecker ExternalConnectionChecker) func(*http.Request) bool {
	// Build a set of allowed origins for fast lookup
	allowedSet := make(map[string]bool)
	allowAll := false
	for _, origin := range allowedOrigins {
		if origin == "*" {
			allowAll = true
			break
		}
		allowedSet[strings.ToLower(origin)] = true
	}

	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		host := r.Host

		logResult := func(allowed bool, reason string) bool {
			if logger != nil {
				logger(origin, host, allowed, reason)
			}
			return allowed
		}

		// No origin header - likely a non-browser client (curl, etc.)
		// Allow these as they can't perform CSWSH attacks
		if origin == "" {
			return logResult(true, "no origin header (non-browser client)")
		}

		// Allow all origins if configured (not recommended)
		if allowAll {
			return logResult(true, "allow all origins configured")
		}

		// Allow authenticated external connections (e.g., Tailscale funnel)
		// These have already been authenticated by the auth middleware
		if externalChecker != nil && externalChecker(r) {
			return logResult(true, "authenticated external connection")
		}

		// Parse the origin URL
		originURL, err := url.Parse(origin)
		if err != nil {
			return logResult(false, "failed to parse origin URL")
		}

		// Check against explicit allowlist
		if len(allowedSet) > 0 {
			if allowedSet[strings.ToLower(origin)] {
				return logResult(true, "origin in allowlist")
			}
			if allowedSet[strings.ToLower(originURL.Host)] {
				return logResult(true, "origin host in allowlist")
			}
			return logResult(false, "origin not in allowlist")
		}

		// Default: same-origin check
		result := isSameOrigin(r, originURL)
		if result {
			return logResult(true, "same-origin check passed")
		}
		return logResult(false, "same-origin check failed")
	}
}

// isSameOrigin checks if the origin matches the request host.
// This implements a strict same-origin check where both host and port must match.
func isSameOrigin(r *http.Request, originURL *url.URL) bool {
	// Get the request host (may or may not include port)
	requestHost := r.Host

	// Parse request host and port
	requestHostname, requestPort, err := net.SplitHostPort(requestHost)
	if err != nil {
		// No port in request host
		requestHostname = requestHost
		requestPort = ""
	}

	// Parse origin host and port
	originHostname, originPort, err := net.SplitHostPort(originURL.Host)
	if err != nil {
		// No port in origin host
		originHostname = originURL.Host
		originPort = ""
	}

	// Hostnames must match (case-insensitive)
	if !strings.EqualFold(requestHostname, originHostname) {
		return false
	}

	// Normalize ports: if not specified, use default for scheme
	if originPort == "" {
		switch originURL.Scheme {
		case "https", "wss":
			originPort = "443"
		case "http", "ws":
			originPort = "80"
		}
	}

	// If request port is empty, we can't strictly compare
	// In this case, allow if hostnames match (common behind reverse proxies)
	if requestPort == "" {
		return true
	}

	// Both ports specified - must match
	return requestPort == originPort
}

// configureWebSocketConn applies security settings to a WebSocket connection.
func configureWebSocketConn(conn *websocket.Conn, config WebSocketSecurityConfig) {
	// Set maximum message size
	conn.SetReadLimit(config.MaxMessageSize)

	// Set initial read deadline
	conn.SetReadDeadline(time.Now().Add(config.PongWait))

	// Set pong handler to extend read deadline
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(config.PongWait))
		return nil
	})
}

// getSecureUpgraderForRequest returns a WebSocket upgrader with security checks,
// with compression enabled only for external connections.
//
// Compression trade-offs:
//   - External (Tailscale, etc.): High latency, limited bandwidth → compression beneficial
//   - Local (macOS app, localhost): Zero latency, unlimited bandwidth → compression overhead not worth it
func (s *Server) getSecureUpgraderForRequest(r *http.Request) websocket.Upgrader {
	var logger OriginCheckLogger
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
	enableCompression := IsExternalConnection(r)

	if enableCompression && s.logger != nil {
		s.logger.Debug("WS: Enabling compression for external connection",
			"client_ip", getClientIPWithProxyCheck(r))
	}

	// Allow authenticated external connections (e.g., Tailscale funnel)
	// These have already been authenticated by the auth middleware
	return createSecureUpgrader(s.wsSecurityConfig, logger, IsExternalConnection, enableCompression)
}
