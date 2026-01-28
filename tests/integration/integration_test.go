//go:build integration

// Package integration contains integration tests for Mitto.
// These tests require the mock ACP server and test environment to be set up.
//
// Run with: go test -tags=integration ./tests/integration/...
package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain sets up the test environment
func TestMain(m *testing.M) {
	// Ensure we're in test mode
	os.Setenv("MITTO_TEST_MODE", "1")

	// Run tests
	code := m.Run()

	os.Exit(code)
}

// getProjectRoot returns the project root directory
func getProjectRoot(t *testing.T) string {
	t.Helper()

	// Try to find the project root by looking for go.mod
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find project root (go.mod)")
		}
		dir = parent
	}
}

// getMittoBinary returns the path to the mitto binary
func getMittoBinary(t *testing.T) string {
	t.Helper()
	root := getProjectRoot(t)
	binary := filepath.Join(root, "mitto")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skip("mitto binary not found. Run 'make build' first.")
	}
	return binary
}

// getMockACPBinary returns the path to the mock ACP server binary
func getMockACPBinary(t *testing.T) string {
	t.Helper()
	root := getProjectRoot(t)
	binary := filepath.Join(root, "tests", "mocks", "acp-server", "mock-acp-server")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skip("mock-acp-server binary not found. Run 'make build-mock-acp' first.")
	}
	return binary
}

// getTestWorkspace returns the path to a test workspace
func getTestWorkspace(t *testing.T, name string) string {
	t.Helper()
	root := getProjectRoot(t)
	workspace := filepath.Join(root, "tests", "fixtures", "workspaces", name)
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		t.Fatalf("Test workspace not found: %s", workspace)
	}
	return workspace
}

// getTestConfig returns the path to a test configuration file
func getTestConfig(t *testing.T, name string) string {
	t.Helper()
	root := getProjectRoot(t)
	config := filepath.Join(root, "tests", "fixtures", "config", name)
	if _, err := os.Stat(config); os.IsNotExist(err) {
		t.Fatalf("Test config not found: %s", config)
	}
	return config
}

// createTestDir creates a temporary test directory
func createTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0755)
	return dir
}
