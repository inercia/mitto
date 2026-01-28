// Package testutil provides shared test utilities for Mitto integration tests.
package testutil

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// FindProjectRoot finds the project root by looking for go.mod
func FindProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root (go.mod)")
		}
		dir = parent
	}
}

// GetMittoBinary returns the path to the mitto binary
func GetMittoBinary() (string, error) {
	root, err := FindProjectRoot()
	if err != nil {
		return "", err
	}
	binary := filepath.Join(root, "mitto")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		return "", fmt.Errorf("mitto binary not found at %s", binary)
	}
	return binary, nil
}

// GetMockACPBinary returns the path to the mock ACP server binary
func GetMockACPBinary() (string, error) {
	root, err := FindProjectRoot()
	if err != nil {
		return "", err
	}
	binary := filepath.Join(root, "tests", "mocks", "acp-server", "mock-acp-server")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		return "", fmt.Errorf("mock-acp-server binary not found at %s", binary)
	}
	return binary, nil
}

// GetTestWorkspace returns the path to a test workspace
func GetTestWorkspace(name string) (string, error) {
	root, err := FindProjectRoot()
	if err != nil {
		return "", err
	}
	workspace := filepath.Join(root, "tests", "fixtures", "workspaces", name)
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		return "", fmt.Errorf("test workspace not found at %s", workspace)
	}
	return workspace, nil
}

// GetTestConfig returns the path to a test configuration file
func GetTestConfig(name string) (string, error) {
	root, err := FindProjectRoot()
	if err != nil {
		return "", err
	}
	config := filepath.Join(root, "tests", "fixtures", "config", name)
	if _, err := os.Stat(config); os.IsNotExist(err) {
		return "", fmt.Errorf("test config not found at %s", config)
	}
	return config, nil
}

// CreateTestDir creates a temporary test directory with the required structure
func CreateTestDir() (string, error) {
	dir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0755); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// WaitForServer waits for an HTTP server to be ready
func WaitForServer(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for server at %s", url)
		default:
			resp, err := http.Get(url)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					return nil
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// TestEnv returns environment variables for test execution
func TestEnv(testDir string) []string {
	return append(os.Environ(),
		"MITTO_DIR="+testDir,
		"MITTO_TEST_MODE=1",
	)
}
