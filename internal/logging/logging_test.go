package logging

import (
	"bytes"
	"log/slog"
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
