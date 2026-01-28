//go:build integration

// Package api contains HTTP API integration tests for Mitto.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	testServerURL  string
	testServerCmd  *exec.Cmd
	testServerPort = "8099"
)

// TestMain starts the test server before running tests
func TestMain(m *testing.M) {
	// Set up test environment
	os.Setenv("MITTO_TEST_MODE", "1")

	// Start test server
	if err := startTestServer(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start test server: %v\n", err)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Stop test server
	stopTestServer()

	os.Exit(code)
}

func startTestServer() error {
	root := findProjectRoot()
	if root == "" {
		return fmt.Errorf("could not find project root")
	}

	binary := filepath.Join(root, "mitto")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		return fmt.Errorf("mitto binary not found. Run 'make build' first")
	}

	mockACP := filepath.Join(root, "tests", "mocks", "acp-server", "mock-acp-server")
	if _, err := os.Stat(mockACP); os.IsNotExist(err) {
		return fmt.Errorf("mock-acp-server not found. Run 'make build-mock-acp' first")
	}

	testDir, err := os.MkdirTemp("", "mitto-api-test-*")
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Join(testDir, "sessions"), 0755)

	workspace := filepath.Join(root, "tests", "fixtures", "workspaces", "project-alpha")

	// Create a config file with the mock ACP server
	configContent := fmt.Sprintf(`acp:
  - mock-acp:
      command: %s
web:
  host: 127.0.0.1
  port: %s
`, mockACP, testServerPort)
	configPath := filepath.Join(testDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	testServerURL = fmt.Sprintf("http://127.0.0.1:%s", testServerPort)

	testServerCmd = exec.Command(binary, "web",
		"--config", configPath,
		"--port", testServerPort,
		"--dir", "mock-acp:"+workspace,
	)
	testServerCmd.Env = append(os.Environ(),
		"MITTO_DIR="+testDir,
		"MITTO_TEST_MODE=1",
	)
	testServerCmd.Stdout = os.Stdout
	testServerCmd.Stderr = os.Stderr

	if err := testServerCmd.Start(); err != nil {
		return err
	}

	// Wait for server to be ready
	return waitForServer(testServerURL, 30*time.Second)
}

func stopTestServer() {
	if testServerCmd != nil && testServerCmd.Process != nil {
		testServerCmd.Process.Kill()
		testServerCmd.Wait()
	}
}

func waitForServer(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for server at %s", url)
		default:
			resp, err := http.Get(url + "/")
			if err == nil {
				resp.Body.Close()
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func findProjectRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// TestHealthEndpoint tests the health/root endpoint
func TestHealthEndpoint(t *testing.T) {
	resp, err := http.Get(testServerURL + "/")
	if err != nil {
		t.Fatalf("Failed to get /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

// TestSessionsAPI tests the sessions API endpoints
func TestSessionsAPI(t *testing.T) {
	// GET /api/sessions
	resp, err := http.Get(testServerURL + "/api/sessions")
	if err != nil {
		t.Fatalf("Failed to get sessions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, body)
	}

	var sessions []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatalf("Failed to decode sessions: %v", err)
	}

	// Sessions should be an array (may be empty)
	t.Logf("Found %d sessions", len(sessions))
}

// TestWorkspacesAPI tests the workspaces API endpoints
func TestWorkspacesAPI(t *testing.T) {
	// GET /api/workspaces
	resp, err := http.Get(testServerURL + "/api/workspaces")
	if err != nil {
		t.Fatalf("Failed to get workspaces: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, body)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("Failed to decode workspaces: %v", err)
	}

	// Should have workspaces and acp_servers
	if _, ok := data["workspaces"]; !ok {
		t.Error("Response missing 'workspaces' field")
	}
	if _, ok := data["acp_servers"]; !ok {
		t.Error("Response missing 'acp_servers' field")
	}
}

// TestCreateSession tests session creation
func TestCreateSession(t *testing.T) {
	// POST /api/sessions
	body := strings.NewReader(`{"name": "API Test Session"}`)
	resp, err := http.Post(testServerURL+"/api/sessions", "application/json", body)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200/201, got %d: %s", resp.StatusCode, respBody)
	}

	var session map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatalf("Failed to decode session: %v", err)
	}

	if _, ok := session["session_id"]; !ok {
		t.Error("Response missing 'session_id' field")
	}
}
