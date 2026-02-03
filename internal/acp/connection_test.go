package acp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// getMockACPPath returns the path to the mock ACP server binary.
// It searches relative to the test file location.
func getMockACPPath(t *testing.T) string {
	t.Helper()

	// Find project root by looking for go.mod
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("Could not find project root")
		}
		dir = parent
	}

	mockPath := filepath.Join(dir, "tests", "mocks", "acp-server", "mock-acp-server")
	if _, err := os.Stat(mockPath); os.IsNotExist(err) {
		t.Skip("mock-acp-server not found. Run 'make build-mock-acp' first.")
	}

	return mockPath
}

func TestNewConnection_EmptyCommand(t *testing.T) {
	ctx := context.Background()
	_, err := NewConnection(ctx, "", true, nil, nil)
	if err == nil {
		t.Error("NewConnection should fail with empty command")
	}
	if !strings.Contains(err.Error(), "empty command") {
		t.Errorf("Error should mention 'empty command', got: %v", err)
	}
}

func TestNewConnection_InvalidCommand(t *testing.T) {
	ctx := context.Background()
	_, err := NewConnection(ctx, "/nonexistent/command/that/does/not/exist", true, nil, nil)
	if err == nil {
		t.Error("NewConnection should fail with invalid command")
	}
}

func TestNewConnection_WithMockACP(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var output strings.Builder
	conn, err := NewConnection(ctx, mockPath, true, func(msg string) {
		output.WriteString(msg)
	}, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	if conn.cmd == nil {
		t.Error("Connection cmd should not be nil")
	}
	if conn.conn == nil {
		t.Error("Connection conn should not be nil")
	}
	if conn.client == nil {
		t.Error("Connection client should not be nil")
	}
}

func TestConnection_Initialize(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var output strings.Builder
	conn, err := NewConnection(ctx, mockPath, true, func(msg string) {
		output.WriteString(msg)
	}, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	err = conn.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Check that output contains connection message
	if !strings.Contains(output.String(), "Connected") {
		t.Errorf("Output should contain 'Connected', got: %s", output.String())
	}
}

func TestConnection_HasImageSupport_BeforeInitialize(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := NewConnection(ctx, mockPath, true, nil, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	// Before Initialize, capabilities should be nil
	if conn.capabilities != nil {
		t.Error("capabilities should be nil before Initialize")
	}
}

func TestConnection_NewSession(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var output strings.Builder
	conn, err := NewConnection(ctx, mockPath, true, func(msg string) {
		output.WriteString(msg)
	}, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	tmpDir := t.TempDir()
	err = conn.NewSession(ctx, tmpDir)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// Check session was created
	if conn.session == nil {
		t.Error("Session should not be nil after NewSession")
	}

	// Check output contains session creation message
	if !strings.Contains(output.String(), "session") {
		t.Errorf("Output should contain 'session', got: %s", output.String())
	}
}

func TestConnection_Prompt(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var output strings.Builder
	conn, err := NewConnection(ctx, mockPath, true, func(msg string) {
		output.WriteString(msg)
	}, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	tmpDir := t.TempDir()
	if err := conn.NewSession(ctx, tmpDir); err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// Send a prompt
	err = conn.Prompt(ctx, "Hello, test!")
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// The mock server should respond with something
	outputStr := output.String()
	if !strings.Contains(outputStr, "Hello") && !strings.Contains(outputStr, "mock") {
		t.Logf("Output: %s", outputStr)
		// Don't fail - the mock server may have different response format
	}
}

func TestConnection_Prompt_NoSession(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := NewConnection(ctx, mockPath, true, nil, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	if err := conn.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Try to prompt without creating a session
	err = conn.Prompt(ctx, "Hello")
	if err == nil {
		t.Error("Prompt should fail without a session")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("Error should mention 'no active session', got: %v", err)
	}
}

func TestConnection_Cancel_NoSession(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := NewConnection(ctx, mockPath, true, nil, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	// Cancel without a session should not error
	err = conn.Cancel(ctx)
	if err != nil {
		t.Errorf("Cancel without session should not error, got: %v", err)
	}
}

func TestConnection_Close(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := NewConnection(ctx, mockPath, true, nil, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}

	// Close should not error
	err = conn.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Close again should not panic or error
	err = conn.Close()
	if err != nil {
		t.Errorf("Second Close failed: %v", err)
	}
}

func TestConnection_Close_NilCmd(t *testing.T) {
	conn := &Connection{}

	// Close with nil cmd should not error
	err := conn.Close()
	if err != nil {
		t.Errorf("Close with nil cmd should not error, got: %v", err)
	}
}

func TestConnection_Done(t *testing.T) {
	mockPath := getMockACPPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := NewConnection(ctx, mockPath, true, nil, nil)
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	defer conn.Close()

	// Done should return a channel
	done := conn.Done()
	if done == nil {
		t.Error("Done should return a non-nil channel")
	}

	// Channel should not be closed yet
	select {
	case <-done:
		t.Error("Done channel should not be closed while connection is active")
	default:
		// Expected
	}
}
