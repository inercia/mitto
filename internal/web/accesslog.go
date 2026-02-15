// Package web provides the HTTP server for Mitto.
package web

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// AccessLogConfig holds configuration for access logging.
type AccessLogConfig struct {
	// Path is the file path for the access log.
	// Empty string disables access logging.
	Path string

	// MaxSizeMB is the maximum size of the log file in megabytes before rotation.
	// Default: 10MB
	MaxSizeMB int

	// MaxBackups is the maximum number of old log files to retain.
	// Default: 1
	MaxBackups int
}

// DefaultAccessLogConfig returns the default access log configuration.
func DefaultAccessLogConfig() AccessLogConfig {
	return AccessLogConfig{
		MaxSizeMB:  10,
		MaxBackups: 1,
	}
}

// AccessLogger handles security-focused access logging to a file with rotation.
// It logs security-relevant events such as authentication attempts, unauthorized
// access, and rate limiting triggers.
type AccessLogger struct {
	writer    io.WriteCloser
	mu        sync.Mutex
	authMgr   *AuthManager // Reference to auth manager for enriched logging
	apiPrefix string       // API prefix for detecting security-relevant paths
}

// NewAccessLogger creates a new access logger that writes to the specified file.
// If path is empty, returns nil (access logging disabled).
func NewAccessLogger(config AccessLogConfig) *AccessLogger {
	if config.Path == "" {
		return nil
	}

	// Apply defaults
	maxSize := config.MaxSizeMB
	if maxSize <= 0 {
		maxSize = 10
	}
	maxBackups := config.MaxBackups
	if maxBackups < 0 {
		maxBackups = 1
	}

	// Create lumberjack logger for rotation
	writer := &lumberjack.Logger{
		Filename:   config.Path,
		MaxSize:    maxSize,    // megabytes
		MaxBackups: maxBackups, // number of backups
		MaxAge:     0,          // don't delete old files based on age
		Compress:   false,      // don't compress backups
	}

	return &AccessLogger{
		writer: writer,
	}
}

// SetAuthManager sets the auth manager reference for enriched logging.
func (a *AccessLogger) SetAuthManager(authMgr *AuthManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authMgr = authMgr
}

// SetAPIPrefix sets the API prefix for detecting security-relevant paths.
func (a *AccessLogger) SetAPIPrefix(prefix string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apiPrefix = prefix
}

// Close closes the access logger.
func (a *AccessLogger) Close() error {
	if a == nil || a.writer == nil {
		return nil
	}
	return a.writer.Close()
}

// LogEntry represents a single access log entry.
type LogEntry struct {
	Timestamp    time.Time
	ClientIP     string
	Method       string
	Path         string
	StatusCode   int
	BytesWritten int64
	Duration     time.Duration
	UserAgent    string

	// Security context
	EventType    string // login_success, login_failed, unauthorized, rate_limited, external_access, etc.
	Username     string // For auth events
	ErrorMessage string // For failed events
	IsExternal   bool   // Whether request came from external listener
}

// Write writes a log entry to the access log file.
// Format: Combined Log Format with security extensions
// timestamp ip method path status bytes duration "user-agent" event_type [username] [error]
func (a *AccessLogger) Write(entry LogEntry) {
	if a == nil || a.writer == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Format timestamp in ISO 8601 format for clarity
	ts := entry.Timestamp.Format(time.RFC3339)

	// Build log line - Apache Combined-like format with security extensions
	// Format: timestamp client_ip "method path" status bytes duration_ms "user-agent" event [user] [error]
	line := fmt.Sprintf("%s %s \"%s %s\" %d %d %dms \"%s\" %s",
		ts,
		entry.ClientIP,
		entry.Method,
		entry.Path,
		entry.StatusCode,
		entry.BytesWritten,
		entry.Duration.Milliseconds(),
		escapeQuotes(entry.UserAgent),
		entry.EventType,
	)

	// Add optional security context
	if entry.Username != "" {
		line += fmt.Sprintf(" user=%s", entry.Username)
	}
	if entry.ErrorMessage != "" {
		line += fmt.Sprintf(" error=\"%s\"", escapeQuotes(entry.ErrorMessage))
	}
	if entry.IsExternal {
		line += " external=true"
	}

	line += "\n"

	// Write to log file (lumberjack handles rotation)
	_, _ = a.writer.Write([]byte(line))
}

// escapeQuotes escapes quotes in a string for log safety.
func escapeQuotes(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			result = append(result, '\\', '"')
		case '\\':
			result = append(result, '\\', '\\')
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}

// isSecurityRelevantPath returns true if the path is security-relevant and should be logged.
func (a *AccessLogger) isSecurityRelevantPath(path string) bool {
	// Always log auth-related endpoints
	securityPaths := []string{
		"/api/login",
		"/api/logout",
		"/api/auth/",
		"/auth.html",
	}

	// Check with and without API prefix
	for _, sp := range securityPaths {
		if path == sp || path == a.apiPrefix+sp {
			return true
		}
	}

	return false
}

// accessLogResponseWriter wraps http.ResponseWriter to capture status code and bytes written.
type accessLogResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	wroteHeader  bool
}

func (w *accessLogResponseWriter) WriteHeader(statusCode int) {
	if !w.wroteHeader {
		w.statusCode = statusCode
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *accessLogResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	return n, err
}

// Hijack implements http.Hijacker for WebSocket support.
func (w *accessLogResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// Flush implements http.Flusher to support streaming responses.
func (w *accessLogResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for interface detection.
// This is required for proper compatibility with http.TimeoutHandler and other middleware.
func (w *accessLogResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Middleware returns an HTTP middleware that logs security-relevant access events.
// Only external connections and security-relevant paths are logged.
func (a *AccessLogger) Middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		clientIP := getClientIPWithProxyCheck(r)
		isExternal := IsExternalConnection(r)

		// Wrap response writer to capture status code
		wrapped := &accessLogResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Serve the request
		next.ServeHTTP(wrapped, r)

		// Determine if this is a security-relevant event
		eventType := a.determineEventType(r, wrapped.statusCode, isExternal)
		if eventType == "" {
			return // Not a security-relevant event, skip logging
		}

		// Extract username from session if available
		username := ""
		if a.authMgr != nil {
			if session, valid := a.authMgr.GetSessionFromRequest(r); valid {
				username = session.Username
			}
		}

		// Determine error message for failed events
		errorMsg := ""
		if wrapped.statusCode >= 400 {
			errorMsg = http.StatusText(wrapped.statusCode)
		}

		// Log the entry
		a.Write(LogEntry{
			Timestamp:    start,
			ClientIP:     clientIP,
			Method:       r.Method,
			Path:         r.URL.Path,
			StatusCode:   wrapped.statusCode,
			BytesWritten: wrapped.bytesWritten,
			Duration:     time.Since(start),
			UserAgent:    r.UserAgent(),
			EventType:    eventType,
			Username:     username,
			ErrorMessage: errorMsg,
			IsExternal:   isExternal,
		})
	})
}

// determineEventType determines the type of security event based on request and response.
func (a *AccessLogger) determineEventType(r *http.Request, statusCode int, isExternal bool) string {
	path := r.URL.Path

	// Login endpoint - always log
	if path == "/api/login" || path == a.apiPrefix+"/api/login" {
		if r.Method == http.MethodPost {
			switch statusCode {
			case http.StatusOK:
				return "login_success"
			case http.StatusTooManyRequests:
				return "rate_limited"
			case http.StatusUnauthorized:
				return "login_failed"
			default:
				return "login_error"
			}
		}
	}

	// Logout endpoint
	if path == "/api/logout" || path == a.apiPrefix+"/api/logout" {
		if r.Method == http.MethodPost && statusCode == http.StatusOK {
			return "logout"
		}
	}

	// External connections - log all requests
	if isExternal {
		switch statusCode {
		case http.StatusUnauthorized:
			return "unauthorized"
		case http.StatusForbidden:
			return "forbidden"
		case http.StatusTooManyRequests:
			return "rate_limited"
		default:
			// Log all successful external access (including static assets)
			if statusCode >= 200 && statusCode < 300 {
				return "external_access"
			}
		}
	}

	// Any unauthorized or forbidden response
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return "unauthorized"
	}

	// Rate limiting trigger (429)
	if statusCode == http.StatusTooManyRequests {
		return "rate_limited"
	}

	return "" // Not a security-relevant event
}
