package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultAccessLogConfig(t *testing.T) {
	cfg := DefaultAccessLogConfig()

	if cfg.MaxSizeMB != 10 {
		t.Errorf("MaxSizeMB = %d, want 10", cfg.MaxSizeMB)
	}
	if cfg.MaxBackups != 1 {
		t.Errorf("MaxBackups = %d, want 1", cfg.MaxBackups)
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty", cfg.Path)
	}
}

func TestNewAccessLogger_Disabled(t *testing.T) {
	// Empty path should return nil (disabled)
	logger := NewAccessLogger(AccessLogConfig{Path: ""})
	if logger != nil {
		t.Error("Expected nil logger when path is empty")
	}
}

func TestNewAccessLogger_Enabled(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		MaxSizeMB:  5,
		MaxBackups: 2,
	})

	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	if logger.writer == nil {
		t.Error("Expected non-nil writer")
	}
}

func TestNewAccessLogger_DefaultValues(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	// Test with zero/negative values - should use defaults
	logger := NewAccessLogger(AccessLogConfig{
		Path:       logPath,
		MaxSizeMB:  0,  // Should default to 10
		MaxBackups: -1, // Should default to 1
	})

	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()
}

func TestEscapeQuotes(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{`hello "world"`, `hello \"world\"`},
		{`path\to\file`, `path\\to\\file`},
		{`"quoted"`, `\"quoted\"`},
		{`back\\slash`, `back\\\\slash`},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := escapeQuotes(tt.input)
			if got != tt.expected {
				t.Errorf("escapeQuotes(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestAccessLogger_Write(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	// Write a log entry
	entry := LogEntry{
		Timestamp:    time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		ClientIP:     "192.168.1.100",
		Method:       "POST",
		Path:         "/api/login",
		StatusCode:   200,
		BytesWritten: 42,
		Duration:     15 * time.Millisecond,
		UserAgent:    "Mozilla/5.0",
		EventType:    "login_success",
		Username:     "admin",
	}

	logger.Write(entry)
	logger.Close()

	// Read the log file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logLine := string(content)

	// Verify log line contains expected fields
	expectedFields := []string{
		"192.168.1.100",
		"POST /api/login",
		"200",
		"42",
		"15ms",
		"Mozilla/5.0",
		"login_success",
		"user=admin",
	}

	for _, field := range expectedFields {
		if !strings.Contains(logLine, field) {
			t.Errorf("Log line missing field %q: %s", field, logLine)
		}
	}
}

func TestAccessLogger_WriteWithError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	// Write a log entry with error
	entry := LogEntry{
		Timestamp:    time.Now(),
		ClientIP:     "10.0.0.1",
		Method:       "POST",
		Path:         "/api/login",
		StatusCode:   401,
		BytesWritten: 0,
		Duration:     5 * time.Millisecond,
		UserAgent:    "curl/7.68.0",
		EventType:    "login_failed",
		ErrorMessage: "Unauthorized",
		IsExternal:   true,
	}

	logger.Write(entry)
	logger.Close()

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logLine := string(content)

	if !strings.Contains(logLine, "login_failed") {
		t.Errorf("Log line missing event type: %s", logLine)
	}
	if !strings.Contains(logLine, `error="Unauthorized"`) {
		t.Errorf("Log line missing error message: %s", logLine)
	}
	if !strings.Contains(logLine, "external=true") {
		t.Errorf("Log line missing external flag: %s", logLine)
	}
}

func TestAccessLogger_WriteNil(t *testing.T) {
	// Writing to nil logger should not panic
	var logger *AccessLogger
	logger.Write(LogEntry{})
}

func TestAccessLogger_CloseNil(t *testing.T) {
	// Closing nil logger should not panic
	var logger *AccessLogger
	err := logger.Close()
	if err != nil {
		t.Errorf("Close() on nil logger returned error: %v", err)
	}
}

func TestAccessLogger_SetAuthManager(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	// Should not panic
	logger.SetAuthManager(nil)
}

func TestAccessLogger_SetAPIPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	logger.SetAPIPrefix("/mitto")

	if logger.apiPrefix != "/mitto" {
		t.Errorf("apiPrefix = %q, want %q", logger.apiPrefix, "/mitto")
	}
}

func TestAccessLogger_IsSecurityRelevantPath(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	logger.SetAPIPrefix("/mitto")

	tests := []struct {
		path     string
		expected bool
	}{
		{"/api/login", true},
		{"/api/logout", true},
		{"/auth.html", true},
		{"/mitto/api/login", true},
		{"/mitto/api/logout", true},
		{"/api/sessions", false},
		{"/index.html", false},
		{"/static/app.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := logger.isSecurityRelevantPath(tt.path)
			if got != tt.expected {
				t.Errorf("isSecurityRelevantPath(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestAccessLogger_DetermineEventType(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	tests := []struct {
		name       string
		path       string
		method     string
		statusCode int
		isExternal bool
		expected   string
	}{
		{"login success", "/api/login", "POST", 200, false, "login_success"},
		{"login failed", "/api/login", "POST", 401, false, "login_failed"},
		{"login rate limited", "/api/login", "POST", 429, false, "rate_limited"},
		{"login error", "/api/login", "POST", 500, false, "login_error"},
		{"logout", "/api/logout", "POST", 200, false, "logout"},
		{"external unauthorized", "/api/sessions", "GET", 401, true, "unauthorized"},
		{"external forbidden", "/api/sessions", "GET", 403, true, "forbidden"},
		{"external rate limited", "/api/sessions", "GET", 429, true, "rate_limited"},
		{"external access", "/api/sessions", "GET", 200, true, "external_access"},
		{"internal unauthorized", "/api/sessions", "GET", 401, false, "unauthorized"},
		{"internal rate limited", "/api/sessions", "GET", 429, false, "rate_limited"},
		{"internal success - not logged", "/api/sessions", "GET", 200, false, ""},
		{"static asset external - logged", "/static/app.js", "GET", 200, true, "external_access"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			got := logger.determineEventType(req, tt.statusCode, tt.isExternal)
			if got != tt.expected {
				t.Errorf("determineEventType() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestAccessLogResponseWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	wrapped := &accessLogResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	// Test WriteHeader
	wrapped.WriteHeader(http.StatusNotFound)
	if wrapped.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want %d", wrapped.statusCode, http.StatusNotFound)
	}
	if !wrapped.wroteHeader {
		t.Error("wroteHeader should be true after WriteHeader")
	}

	// Test Write
	n, err := wrapped.Write([]byte("test body"))
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}
	if n != 9 {
		t.Errorf("Write() = %d, want 9", n)
	}
	if wrapped.bytesWritten != 9 {
		t.Errorf("bytesWritten = %d, want 9", wrapped.bytesWritten)
	}
}

func TestAccessLogResponseWriter_ImplicitOK(t *testing.T) {
	rec := httptest.NewRecorder()
	wrapped := &accessLogResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	// Write without calling WriteHeader first
	wrapped.Write([]byte("test"))

	if !wrapped.wroteHeader {
		t.Error("wroteHeader should be true after Write")
	}
	if wrapped.statusCode != http.StatusOK {
		t.Errorf("statusCode = %d, want %d", wrapped.statusCode, http.StatusOK)
	}
}

func TestAccessLogger_MiddlewareNil(t *testing.T) {
	// Nil logger should pass through
	var logger *AccessLogger
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := logger.Middleware(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccessLogger_MiddlewareLogsSecurityEvents(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	// Handler that returns 401 Unauthorized
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	wrapped := logger.Middleware(handler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Close to flush
	logger.Close()

	// Read log file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logLine := string(content)
	if !strings.Contains(logLine, "unauthorized") {
		t.Errorf("Log should contain 'unauthorized' event: %s", logLine)
	}
}

func TestAccessLogger_MiddlewareSkipsNonSecurityEvents(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	logger := NewAccessLogger(AccessLogConfig{Path: logPath})
	if logger == nil {
		t.Fatal("Expected non-nil logger")
	}
	defer logger.Close()

	// Handler that returns 200 OK for internal request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := logger.Middleware(handler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Close to flush
	logger.Close()

	// Read log file - should be empty for non-security events
	content, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if len(content) > 0 {
		t.Errorf("Log should be empty for non-security events, got: %s", string(content))
	}
}
