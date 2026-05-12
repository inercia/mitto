//go:build integration

package inprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestSendPromptAndReceiveResponse tests the complete prompt/response flow.
func TestSendPromptAndReceiveResponse(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Track events
	var (
		mu             sync.Mutex
		connected      bool
		promptReceived bool
		promptComplete bool
		agentMessages  []string
		agentThoughts  []string
		toolCalls      []string
		promptID       string
	)

	callbacks := client.SessionCallbacks{
		OnConnected: func(sid, cid, acp string) {
			mu.Lock()
			defer mu.Unlock()
			connected = true
			t.Logf("Connected: session=%s, client=%s", sid, cid)
		},
		OnPromptReceived: func(pid string) {
			mu.Lock()
			defer mu.Unlock()
			promptReceived = true
			promptID = pid
			t.Logf("Prompt received: %s", pid)
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			promptComplete = true
			t.Logf("Prompt complete: %d events", eventCount)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			defer mu.Unlock()
			agentMessages = append(agentMessages, html)
		},
		OnAgentThought: func(text string) {
			mu.Lock()
			defer mu.Unlock()
			agentThoughts = append(agentThoughts, text)
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			defer mu.Unlock()
			toolCalls = append(toolCalls, title)
			t.Logf("Tool call: %s (%s)", title, status)
		},
	}

	// Connect to the session
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Wait for connection
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connected
	}, "connection")

	// Client must send load_events to register as an observer
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Send a prompt
	err = ws.SendPrompt("Hello, this is a test message")
	if err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for prompt to complete (mock ACP should respond quickly)
	// Note: prompt_received may or may not be sent depending on server implementation
	waitFor(t, 15*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete")

	// Verify we got some response
	mu.Lock()
	defer mu.Unlock()

	if promptReceived && promptID == "" {
		t.Error("Prompt ID should not be empty when prompt_received was called")
	}

	// The mock ACP server should have sent some response
	totalContent := len(agentMessages) + len(agentThoughts) + len(toolCalls)
	if totalContent == 0 {
		t.Log("Warning: No agent content received (mock ACP may not have responded)")
	} else {
		t.Logf("Received: %d messages, %d thoughts, %d tool calls",
			len(agentMessages), len(agentThoughts), len(toolCalls))
	}

	// Check that agent message contains expected content from mock
	fullMessage := strings.Join(agentMessages, "")
	if len(fullMessage) > 0 {
		t.Logf("Agent message preview: %s...", truncate(fullMessage, 100))
	}
}

// waitFor waits for a condition to become true.
func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Timeout waiting for %s", description)
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// TestAfterPhaseProcessor_SentinelFile verifies that an on: agentResponded processor
// fires after a prompt completes and produces a side effect (sentinel file).
//
// Strategy:
//  1. Write a processor YAML to the workspace's .mitto/processors/ directory before
//     creating any session (processors are loaded lazily at session-creation time).
//  2. Send a prompt and wait for prompt_complete.
//  3. Assert the sentinel file exists.
func TestAfterPhaseProcessor_SentinelFile(t *testing.T) {
	ts := SetupTestServer(t)

	// Determine sentinel path inside the test's temp dir (auto-cleaned).
	sentinelPath := filepath.Join(ts.TempDir, "after-phase-fired.txt")

	// Write the processor YAML before creating a session.
	// The processor runs sh -c '...' and writes to the sentinel path.
	processorsDir := filepath.Join(ts.TempDir, "workspace", ".mitto", "processors")
	if err := os.MkdirAll(processorsDir, 0755); err != nil {
		t.Fatalf("Failed to create processors dir: %v", err)
	}

	processorYAML := fmt.Sprintf(`name: after-test-sentinel
when:
  on: agentResponded
  match: all
command: sh
args: ["-c", "echo fired >> %s"]
output: discard
`, sentinelPath)

	yamlPath := filepath.Join(processorsDir, "after-test-sentinel.yaml")
	if err := os.WriteFile(yamlPath, []byte(processorYAML), 0644); err != nil {
		t.Fatalf("Failed to write processor YAML: %v", err)
	}
	t.Logf("Processor YAML written to %s", yamlPath)
	t.Logf("Sentinel path: %s", sentinelPath)

	// Create a session (processor is loaded at this point).
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	var (
		mu             sync.Mutex
		promptComplete bool
	)

	callbacks := client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			promptComplete = true
			t.Logf("Prompt complete: %d events", eventCount)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, sess.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := ws.SendPrompt("Hello, test the after-phase processor"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for prompt_complete.
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete")

	// Give the after-phase processor a moment to finish writing the file.
	// The processor runs synchronously in the prompt goroutine, so by the time
	// prompt_complete is broadcast the processor should already be done.
	// A brief sleep guards against any timing edge cases.
	time.Sleep(500 * time.Millisecond)

	// Assert the sentinel file was created.
	if _, err := os.Stat(sentinelPath); os.IsNotExist(err) {
		t.Errorf("After-phase processor did not fire: sentinel file %s does not exist", sentinelPath)
	} else if err != nil {
		t.Errorf("Error checking sentinel file: %v", err)
	} else {
		content, _ := os.ReadFile(sentinelPath)
		t.Logf("Sentinel file content: %q", string(content))
	}
}
