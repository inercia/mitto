package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithSession(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := slog.New(handler)

	logger := WithSession(base, "test-session-123")
	logger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, "session_id=test-session-123") {
		t.Errorf("Expected session_id in output, got: %s", output)
	}
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected message in output, got: %s", output)
	}
}

func TestWithSession_NilLogger(t *testing.T) {
	logger := WithSession(nil, "test-session")
	if logger != nil {
		t.Error("WithSession(nil, ...) should return nil")
	}
}

func TestWithSessionContext(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := slog.New(handler)

	logger := WithSessionContext(base, "session-456", "/home/user/project", "auggie")
	logger.Info("context test")

	output := buf.String()
	if !strings.Contains(output, "session_id=session-456") {
		t.Errorf("Expected session_id in output, got: %s", output)
	}
	if !strings.Contains(output, "working_dir=/home/user/project") {
		t.Errorf("Expected working_dir in output, got: %s", output)
	}
	if !strings.Contains(output, "acp_server=auggie") {
		t.Errorf("Expected acp_server in output, got: %s", output)
	}
}

func TestWithSessionContext_NilLogger(t *testing.T) {
	logger := WithSessionContext(nil, "session", "/dir", "server")
	if logger != nil {
		t.Error("WithSessionContext(nil, ...) should return nil")
	}
}

func TestWithClient(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := slog.New(handler)

	logger := WithClient(base, "client-abc", "session-xyz")
	logger.Info("client test")

	output := buf.String()
	if !strings.Contains(output, "client_id=client-abc") {
		t.Errorf("Expected client_id in output, got: %s", output)
	}
	if !strings.Contains(output, "session_id=session-xyz") {
		t.Errorf("Expected session_id in output, got: %s", output)
	}
}

func TestWithClient_NilLogger(t *testing.T) {
	logger := WithClient(nil, "client", "session")
	if logger != nil {
		t.Error("WithClient(nil, ...) should return nil")
	}
}

func TestWithSession_MultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := slog.New(handler)

	logger := WithSession(base, "persistent-session")

	// Log multiple messages - all should have session_id
	logger.Info("first message")
	logger.Debug("second message")
	logger.Warn("third message")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) != 3 {
		t.Errorf("Expected 3 log lines, got %d", len(lines))
	}

	for i, line := range lines {
		if !strings.Contains(line, "session_id=persistent-session") {
			t.Errorf("Line %d missing session_id: %s", i+1, line)
		}
	}
}

func TestWithSessionContext_AdditionalAttributes(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	base := slog.New(handler)

	logger := WithSessionContext(base, "session-1", "/tmp", "test-server")

	// Add additional attributes on top of context
	logger.Info("with extra", "extra_key", "extra_value")

	output := buf.String()
	if !strings.Contains(output, "session_id=session-1") {
		t.Errorf("Expected session_id in output, got: %s", output)
	}
	if !strings.Contains(output, "extra_key=extra_value") {
		t.Errorf("Expected extra_key in output, got: %s", output)
	}
}

// resetGlobalState resets global logging state for testing.
// This should be called at the start of tests that modify global state.
func resetGlobalState() {
	globalMu.Lock()
	globalLogger = nil
	globalMu.Unlock()

	logFileMu.Lock()
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
	logFileMu.Unlock()

	componentsMu.Lock()
	allowedComponents = nil
	componentsMu.Unlock()
}

func TestInitialize_BasicConfig(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	err := Initialize(Config{Level: "debug"})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	logger := Get()
	if logger == nil {
		t.Fatal("Get returned nil logger")
	}
}

func TestInitialize_WithLogFile(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	err := Initialize(Config{
		Level:   "info",
		LogFile: logPath,
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	defer Close()

	// Log something
	logger := Get()
	logger.Info("test log message")

	// Verify file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("Log file was not created")
	}

	// Close to flush
	if err := Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Read and verify content
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(content), "test log message") {
		t.Errorf("Log file should contain 'test log message', got: %s", content)
	}
}

func TestInitialize_InvalidLogFilePath(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	err := Initialize(Config{
		Level:   "info",
		LogFile: "/nonexistent/directory/that/does/not/exist/log.txt",
	})
	if err == nil {
		t.Error("Initialize should fail with invalid log file path")
	}
}

func TestInitialize_JSONFormat(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.json.log")

	err := Initialize(Config{
		Level:   "info",
		LogFile: logPath,
		JSON:    true,
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	defer Close()

	logger := Get()
	logger.Info("json test", "key", "value")

	Close()

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	// JSON format should contain JSON structures
	if !strings.Contains(string(content), `"msg"`) {
		t.Errorf("JSON log should contain 'msg' field, got: %s", content)
	}
}

func TestGet_BeforeInitialize(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger := Get()
	if logger == nil {
		t.Error("Get should return non-nil logger even before Initialize")
	}
}

func TestClose_NotInitialized(t *testing.T) {
	resetGlobalState()

	// Close without Initialize should not error
	err := Close()
	if err != nil {
		t.Errorf("Close without Initialize should not error, got: %v", err)
	}
}

func TestClose_Multiple(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	err := Initialize(Config{LogFile: logPath})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// First close
	if err := Close(); err != nil {
		t.Errorf("First Close failed: %v", err)
	}

	// Second close should not error
	if err := Close(); err != nil {
		t.Errorf("Second Close should not error, got: %v", err)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},        // default
		{"invalid", slog.LevelInfo}, // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLevel(tt.input)
			if got != tt.expected {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestWithComponent(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	Initialize(Config{Level: "debug"})

	logger := WithComponent("test-component")
	if logger == nil {
		t.Fatal("WithComponent returned nil")
	}
}

func TestComponentFiltering(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Initialize with component filtering
	err := Initialize(Config{
		Level:      "debug",
		LogFile:    logPath,
		Components: []string{"allowed"},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Log from allowed component
	allowedLogger := WithComponent("allowed")
	allowedLogger.Info("allowed message")

	// Log from filtered component
	filteredLogger := WithComponent("filtered")
	filteredLogger.Info("filtered message")

	Close()

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "allowed message") {
		t.Error("Log should contain message from allowed component")
	}
	if strings.Contains(contentStr, "filtered message") {
		t.Error("Log should NOT contain message from filtered component")
	}
}

func TestComponentShortcuts(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	Initialize(Config{Level: "debug"})

	// Test all component shortcut functions
	shortcuts := []struct {
		name   string
		logger *slog.Logger
	}{
		{"web", Web()},
		{"auth", Auth()},
		{"hook", Hook()},
		{"websocket", WebSocket()},
		{"session", Session()},
		{"shutdown", Shutdown()},
	}

	for _, s := range shortcuts {
		t.Run(s.name, func(t *testing.T) {
			if s.logger == nil {
				t.Errorf("%s() returned nil", s.name)
			}
		})
	}
}
