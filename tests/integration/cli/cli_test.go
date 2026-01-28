//go:build integration

// Package cli contains CLI integration tests for Mitto.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCLIHelp tests the help command
func TestCLIHelp(t *testing.T) {
	binary := getMittoBinary(t)

	cmd := exec.Command(binary, "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mitto --help failed: %v\nOutput: %s", err, output)
	}

	// Verify help output contains expected content
	outputStr := string(output)
	expectedStrings := []string{"mitto", "cli", "web", "Usage"}
	for _, expected := range expectedStrings {
		if !strings.Contains(strings.ToLower(outputStr), strings.ToLower(expected)) {
			t.Errorf("Help output missing expected string: %s", expected)
		}
	}
}

// TestCLIVersion tests the version command
func TestCLIVersion(t *testing.T) {
	binary := getMittoBinary(t)

	cmd := exec.Command(binary, "version")
	output, err := cmd.CombinedOutput()

	// Version command might not exist, which is okay
	if err != nil {
		t.Skipf("version command not available: %v", err)
	}

	if len(output) == 0 {
		t.Error("Version output is empty")
	}
}

// TestCLIWithMockACP tests CLI with the mock ACP server
func TestCLIWithMockACP(t *testing.T) {
	binary := getMittoBinary(t)
	mockACP := getMockACPBinary(t)
	testDir := createTestDir(t)
	projectRoot := getProjectRoot(t)
	workspace := filepath.Join(projectRoot, "tests", "fixtures", "workspaces", "project-alpha")

	// Set up environment
	env := append(os.Environ(),
		"MITTO_DIR="+testDir,
		"MITTO_TEST_MODE=1",
	)

	// Create a test config that uses the mock ACP server with absolute path
	configContent := fmt.Sprintf(`acp:
  - mock-acp:
      command: %s
`, mockACP)
	configPath := filepath.Join(testDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Run mitto cli with --once flag
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "cli",
		"--config", configPath,
		"--dir", workspace,
		"--once", "Hello",
		"--auto-approve",
	)
	cmd.Env = env
	cmd.Dir = projectRoot // Run from project root

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		t.Logf("stdout: %s", stdout.String())
		t.Logf("stderr: %s", stderr.String())
		t.Fatalf("mitto cli failed: %v", err)
	}

	// Verify we got some output
	output := stdout.String()
	if len(output) == 0 {
		t.Error("Expected some output from the agent")
	}
}

// TestCLIOnceMode tests the --once flag behavior
func TestCLIOnceMode(t *testing.T) {
	binary := getMittoBinary(t)
	mockACP := getMockACPBinary(t)
	testDir := createTestDir(t)
	projectRoot := getProjectRoot(t)
	workspace := filepath.Join(projectRoot, "tests", "fixtures", "workspaces", "project-alpha")

	// Create config with absolute path
	configContent := fmt.Sprintf(`acp:
  - mock-acp:
      command: %s
`, mockACP)
	configPath := filepath.Join(testDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	env := append(os.Environ(),
		"MITTO_DIR="+testDir,
		"MITTO_TEST_MODE=1",
	)

	// Test that --once exits after response
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, binary, "cli",
		"--config", configPath,
		"--dir", workspace,
		"--once", "What is 2+2?",
		"--auto-approve",
	)
	cmd.Env = env
	cmd.Dir = projectRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		t.Logf("stdout: %s", stdout.String())
		t.Logf("stderr: %s", stderr.String())
		t.Fatalf("mitto cli --once failed: %v", err)
	}

	// Should complete within reasonable time (not hang)
	if duration > 25*time.Second {
		t.Errorf("--once mode took too long: %v", duration)
	}
}

// Helper functions (duplicated from parent package for build tag isolation)
func getMittoBinary(t *testing.T) string {
	t.Helper()
	root := getProjectRoot(t)
	binary := filepath.Join(root, "mitto")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skip("mitto binary not found. Run 'make build' first.")
	}
	return binary
}

func getMockACPBinary(t *testing.T) string {
	t.Helper()
	root := getProjectRoot(t)
	binary := filepath.Join(root, "tests", "mocks", "acp-server", "mock-acp-server")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skip("mock-acp-server binary not found. Run 'make build-mock-acp' first.")
	}
	return binary
}

func getProjectRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find project root")
		}
		dir = parent
	}
}

func createTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0755)
	return dir
}
