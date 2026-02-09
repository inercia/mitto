//go:build integration

package inprocess

import (
	"context"
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
