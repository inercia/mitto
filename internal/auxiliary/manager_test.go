package auxiliary

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// getMockACPPath returns the path to the mock ACP server binary.
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

func TestNewManager(t *testing.T) {
	manager := NewManager("echo test", nil)
	if manager == nil {
		t.Fatal("NewManager returned nil")
	}

	if manager.command != "echo test" {
		t.Errorf("command = %q, want %q", manager.command, "echo test")
	}

	if manager.started {
		t.Error("Manager should not be started initially")
	}
}

func TestManager_IsStarted_Initially(t *testing.T) {
	manager := NewManager("echo test", nil)

	if manager.IsStarted() {
		t.Error("IsStarted should return false initially")
	}
}

func TestManager_Close_NotStarted(t *testing.T) {
	manager := NewManager("echo test", nil)

	// Close without starting should not error
	err := manager.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestManager_Prompt_EmptyCommand(t *testing.T) {
	manager := NewManager("", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := manager.Prompt(ctx, "Hello")
	if err == nil {
		t.Error("Prompt should fail with empty command")
	}
}

func TestManager_Prompt_InvalidCommand(t *testing.T) {
	manager := NewManager("/nonexistent/command", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := manager.Prompt(ctx, "Hello")
	if err == nil {
		t.Error("Prompt should fail with invalid command")
	}
}

func TestManager_WithMockACP(t *testing.T) {
	mockPath := getMockACPPath(t)
	manager := NewManager(mockPath, nil)
	defer manager.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// First prompt should start the manager
	if manager.IsStarted() {
		t.Error("Manager should not be started before first prompt")
	}

	response, err := manager.Prompt(ctx, "Hello, test!")
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Manager should now be started
	if !manager.IsStarted() {
		t.Error("Manager should be started after first prompt")
	}

	// Response should contain something
	if response == "" {
		t.Log("Warning: Empty response from mock ACP server")
	}
}

func TestManager_MultiplePrompts(t *testing.T) {
	mockPath := getMockACPPath(t)
	manager := NewManager(mockPath, nil)
	defer manager.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Send multiple prompts
	for i := 0; i < 3; i++ {
		_, err := manager.Prompt(ctx, "Hello")
		if err != nil {
			t.Fatalf("Prompt %d failed: %v", i, err)
		}
	}

	// Manager should still be running
	if !manager.IsStarted() {
		t.Error("Manager should still be started after multiple prompts")
	}
}

func TestManager_CloseAfterPrompt(t *testing.T) {
	mockPath := getMockACPPath(t)
	manager := NewManager(mockPath, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := manager.Prompt(ctx, "Hello")
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Close should work
	err = manager.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Manager should no longer be started
	if manager.IsStarted() {
		t.Error("Manager should not be started after Close")
	}
}
