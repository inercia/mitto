package client_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// testServerURL returns the URL of a running test server.
// In CI/integration tests, this would be set up by the test harness.
func testServerURL(t *testing.T) string {
	if url := os.Getenv("MITTO_TEST_SERVER_URL"); url != "" {
		return url
	}
	t.Skip("MITTO_TEST_SERVER_URL not set; skipping integration test")
	return ""
}

func TestClient_ListSessions(t *testing.T) {
	serverURL := testServerURL(t)
	c := client.New(serverURL)

	sessions, err := c.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}

	t.Logf("Found %d sessions", len(sessions))
}

func TestClient_CreateAndDeleteSession(t *testing.T) {
	serverURL := testServerURL(t)
	c := client.New(serverURL)

	// Get current working directory for the session
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get cwd: %v", err)
	}

	// Create a session
	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "test-session",
		WorkingDir: cwd,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	t.Logf("Created session: %s", session.SessionID)

	// Get the session
	got, err := c.GetSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.SessionID != session.SessionID {
		t.Errorf("GetSession returned wrong session: got %s, want %s", got.SessionID, session.SessionID)
	}

	// Delete the session
	if err := c.DeleteSession(session.SessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	t.Log("Session deleted successfully")
}

func TestSession_SendPrompt(t *testing.T) {
	serverURL := testServerURL(t)
	c := client.New(serverURL)

	// Get workspace directory from environment or use fixture
	workingDir := os.Getenv("MITTO_TEST_WORKSPACE")
	if workingDir == "" {
		// Find the project root and use a fixture workspace
		root := findProjectRoot(t)
		workingDir = filepath.Join(root, "tests", "fixtures", "workspaces", "project-alpha")
	}

	// Create a session
	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "prompt-test",
		WorkingDir: workingDir,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	// Track events
	var mu sync.Mutex
	var connected bool
	var messages []string
	var promptComplete bool

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to the session
	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			mu.Lock()
			connected = true
			mu.Unlock()
			t.Logf("Connected: session=%s client=%s acp=%s", sessionID, clientID, acpServer)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			messages = append(messages, html)
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			promptComplete = true
			mu.Unlock()
			t.Logf("Prompt complete: %d events", eventCount)
			cancel() // Stop waiting
		},
		OnError: func(message string) {
			t.Errorf("Error: %s", message)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	// Wait for connection
	time.Sleep(100 * time.Millisecond)

	// Send a prompt
	if err := sess.SendPrompt("Hello, world!"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for completion or timeout
	<-ctx.Done()

	mu.Lock()
	defer mu.Unlock()

	if !connected {
		t.Error("Never received connected event")
	}
	if !promptComplete {
		t.Error("Never received prompt_complete event")
	}
	if len(messages) == 0 {
		t.Error("Never received any agent messages")
	}
}

// findProjectRoot finds the project root by looking for go.mod
func findProjectRoot(t *testing.T) string {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to find project root: %v", err)
	}
	return string(out[:len(out)-1]) // trim newline
}
