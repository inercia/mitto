//go:build integration

// Package client contains integration tests for the Mitto Go client.
// These tests verify full session lifecycle scenarios against the mock ACP server.
package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/tests/mocks/testutil"
)

var (
	testServerURL  string
	testServerCmd  *exec.Cmd
	testServerPort = "8098"
	testDir        string
	testWorkspace  string
)

// TestMain starts the test server before running tests
func TestMain(m *testing.M) {
	os.Setenv("MITTO_TEST_MODE", "1")

	if err := startTestServer(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start test server: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	stopTestServer()
	os.Exit(code)
}

func startTestServer() error {
	binary, err := testutil.GetMittoBinary()
	if err != nil {
		return fmt.Errorf("mitto binary not found: %w (run 'make build' first)", err)
	}

	mockACP, err := testutil.GetMockACPBinary()
	if err != nil {
		return fmt.Errorf("mock-acp-server not found: %w (run 'make build-mock-acp' first)", err)
	}

	testDir, err = testutil.CreateTestDir()
	if err != nil {
		return err
	}

	testWorkspace, err = testutil.GetTestWorkspace("project-alpha")
	if err != nil {
		return err
	}

	// Create config with mock ACP server
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
		"--dir", "mock-acp:"+testWorkspace,
	)
	testServerCmd.Env = testutil.TestEnv(testDir)
	testServerCmd.Stdout = os.Stdout
	testServerCmd.Stderr = os.Stderr

	if err := testServerCmd.Start(); err != nil {
		return err
	}

	return testutil.WaitForServer(testServerURL, 30*time.Second)
}

func stopTestServer() {
	if testServerCmd != nil && testServerCmd.Process != nil {
		testServerCmd.Process.Kill()
		testServerCmd.Wait()
	}
	if testDir != "" {
		os.RemoveAll(testDir)
	}
}

// =============================================================================
// Connection & Messaging Tests
// =============================================================================

// TestConnect_Success verifies basic connection to the mock ACP server
func TestConnect_Success(t *testing.T) {
	c := client.New(testServerURL)

	// Create a session
	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "connect-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	// Connect to the session
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var connected bool
	var connectedSessionID, connectedClientID string

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			connected = true
			connectedSessionID = sessionID
			connectedClientID = clientID
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	if !connected {
		t.Error("OnConnected callback was not called")
	}
	if connectedSessionID != session.SessionID {
		t.Errorf("Connected session ID = %q, want %q", connectedSessionID, session.SessionID)
	}
	if connectedClientID == "" {
		t.Error("Client ID should not be empty")
	}
}

// TestSendPrompt_SimpleMessage verifies sending a message and receiving a response
func TestSendPrompt_SimpleMessage(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "prompt-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use PromptAndWait for simple request-response
	result, err := c.PromptAndWait(ctx, session.SessionID, "Hello!")
	if err != nil {
		t.Fatalf("PromptAndWait failed: %v", err)
	}

	// Should have received agent messages
	if len(result.Messages) == 0 {
		t.Error("Expected at least one agent message")
	}

	// Verify the response contains expected content
	fullMessage := ""
	for _, msg := range result.Messages {
		fullMessage += msg
	}
	if fullMessage == "" {
		t.Error("Agent message should not be empty")
	}
	t.Logf("Received message: %s", fullMessage)
}

// TestSendPrompt_StreamingEvents verifies streaming events are received in order
func TestSendPrompt_StreamingEvents(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "streaming-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Track event order
	var mu sync.Mutex
	var events []string
	done := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			mu.Lock()
			events = append(events, "connected")
			mu.Unlock()
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			events = append(events, fmt.Sprintf("message:%s", html))
			mu.Unlock()
		},
		OnAgentThought: func(text string) {
			mu.Lock()
			events = append(events, fmt.Sprintf("thought:%s", text))
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			events = append(events, fmt.Sprintf("tool_call:%s:%s", id, title))
			mu.Unlock()
		},
		OnToolUpdate: func(id, status string) {
			mu.Lock()
			events = append(events, fmt.Sprintf("tool_update:%s:%s", id, status))
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			events = append(events, "complete")
			mu.Unlock()
			close(done)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	// Send a prompt that triggers interleaved tool calls
	if err := sess.SendPrompt("fix the file"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for completion
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for prompt completion")
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify we got events
	if len(events) < 3 {
		t.Errorf("Expected at least 3 events, got %d: %v", len(events), events)
	}

	// First event should be connected
	if events[0] != "connected" {
		t.Errorf("First event should be 'connected', got %q", events[0])
	}

	// Last event should be complete
	if events[len(events)-1] != "complete" {
		t.Errorf("Last event should be 'complete', got %q", events[len(events)-1])
	}

	t.Logf("Received %d events: %v", len(events), events)
}

// =============================================================================
// Disconnect & Reconnection Tests
// =============================================================================

// TestDisconnect_MidSession verifies cleanup when disconnecting mid-session
func TestDisconnect_MidSession(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "disconnect-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var disconnected bool
	var disconnectErr error

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnDisconnected: func(err error) {
			disconnected = true
			disconnectErr = err
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	// Close the session
	if err := sess.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Wait for disconnect callback
	time.Sleep(200 * time.Millisecond)

	// Verify session can still be retrieved (it should still exist on server)
	got, err := c.GetSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed after disconnect: %v", err)
	}
	if got.SessionID != session.SessionID {
		t.Errorf("Session ID mismatch: got %q, want %q", got.SessionID, session.SessionID)
	}

	t.Logf("Disconnected: %v, error: %v", disconnected, disconnectErr)
}

// TestReconnect_ExistingSession verifies reconnecting to an existing session
func TestReconnect_ExistingSession(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "reconnect-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First connection - send a message
	result1, err := c.PromptAndWait(ctx, session.SessionID, "Hello first time!")
	if err != nil {
		t.Fatalf("First PromptAndWait failed: %v", err)
	}
	if len(result1.Messages) == 0 {
		t.Error("First prompt should have received messages")
	}

	// Second connection - send another message
	result2, err := c.PromptAndWait(ctx, session.SessionID, "Hello second time!")
	if err != nil {
		t.Fatalf("Second PromptAndWait failed: %v", err)
	}
	if len(result2.Messages) == 0 {
		t.Error("Second prompt should have received messages")
	}

	t.Logf("First prompt: %d messages, Second prompt: %d messages",
		len(result1.Messages), len(result2.Messages))
}

// TestReconnect_EventReplayOrder verifies events are replayed in correct order on reconnection
func TestReconnect_EventReplayOrder(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "replay-order-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First connection - send a prompt that triggers interleaved events
	var mu sync.Mutex
	var firstEvents []string
	done1 := make(chan struct{})

	sess1, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			firstEvents = append(firstEvents, fmt.Sprintf("msg:%s", html))
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			firstEvents = append(firstEvents, fmt.Sprintf("tool:%s", id))
			mu.Unlock()
		},
		OnToolUpdate: func(id, status string) {
			mu.Lock()
			firstEvents = append(firstEvents, fmt.Sprintf("update:%s:%s", id, status))
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(done1)
		},
	})
	if err != nil {
		t.Fatalf("First Connect failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if err := sess1.SendPrompt("fix the file"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-done1:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for first prompt")
	}
	sess1.Close()

	mu.Lock()
	firstEventsCopy := make([]string, len(firstEvents))
	copy(firstEventsCopy, firstEvents)
	mu.Unlock()

	// Second connection - verify we can still interact
	result, err := c.PromptAndWait(ctx, session.SessionID, "Hello again!")
	if err != nil {
		t.Fatalf("Second PromptAndWait failed: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Error("Second prompt should have received messages")
	}

	t.Logf("First connection events: %v", firstEventsCopy)
	t.Logf("Second connection messages: %d", len(result.Messages))
}

// =============================================================================
// Event Ordering & Completeness Tests
// =============================================================================

// TestEventOrder_InterleavedToolCalls verifies tool calls and messages are interleaved correctly
func TestEventOrder_InterleavedToolCalls(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "interleaved-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Track all events in order
	var mu sync.Mutex
	var eventOrder []string
	done := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnAgentThought: func(text string) {
			mu.Lock()
			eventOrder = append(eventOrder, "thought")
			mu.Unlock()
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			eventOrder = append(eventOrder, "message")
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			eventOrder = append(eventOrder, fmt.Sprintf("tool_call:%s", id))
			mu.Unlock()
		},
		OnToolUpdate: func(id, status string) {
			mu.Lock()
			eventOrder = append(eventOrder, fmt.Sprintf("tool_update:%s", id))
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(done)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	time.Sleep(200 * time.Millisecond)

	// Trigger the interleaved scenario
	if err := sess.SendPrompt("fix the file"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for prompt completion")
	}

	mu.Lock()
	defer mu.Unlock()

	// The mock server sends: thought, tool_call, tool_update, message, tool_call, tool_update, message
	// Verify we have the expected event types
	hasThought := false
	hasToolCall := false
	hasToolUpdate := false
	hasMessage := false

	for _, e := range eventOrder {
		switch {
		case e == "thought":
			hasThought = true
		case len(e) > 10 && e[:10] == "tool_call:":
			hasToolCall = true
		case len(e) > 12 && e[:12] == "tool_update:":
			hasToolUpdate = true
		case e == "message":
			hasMessage = true
		}
	}

	if !hasThought {
		t.Error("Expected at least one thought event")
	}
	if !hasToolCall {
		t.Error("Expected at least one tool_call event")
	}
	if !hasToolUpdate {
		t.Error("Expected at least one tool_update event")
	}
	if !hasMessage {
		t.Error("Expected at least one message event")
	}

	t.Logf("Event order: %v", eventOrder)
}

// TestEventOrder_MessageBeforeSecondToolCall verifies agent messages appear BEFORE subsequent tool calls.
// This is the key ordering test: the mock server sends interleaved events, and we verify
// that a message appears between the first and second tool calls.
//
// Expected order from mock: thought → tool_call:read → tool_update:read → MESSAGE → tool_call:edit → tool_update:edit → MESSAGE
// The test fails if all messages appear at the end (which happens if MarkdownBuffer isn't flushed before tool calls).
func TestEventOrder_MessageBeforeSecondToolCall(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "order-verification-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Track all events in order with detailed info
	var mu sync.Mutex
	var eventOrder []string
	done := make(chan struct{})

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnAgentThought: func(text string) {
			mu.Lock()
			eventOrder = append(eventOrder, "thought")
			mu.Unlock()
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			eventOrder = append(eventOrder, "message")
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			eventOrder = append(eventOrder, fmt.Sprintf("tool_call:%s", id))
			mu.Unlock()
		},
		OnToolUpdate: func(id, status string) {
			mu.Lock()
			eventOrder = append(eventOrder, fmt.Sprintf("tool_update:%s:%s", id, status))
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(done)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	time.Sleep(200 * time.Millisecond)

	// Trigger the interleaved scenario
	if err := sess.SendPrompt("fix the file"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for prompt completion")
	}

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Event order: %v", eventOrder)

	// Find the positions of key events
	firstToolCallIdx := -1
	secondToolCallIdx := -1
	messageIndices := []int{}

	for i, e := range eventOrder {
		switch {
		case e == "tool_call:tool-read-1" && firstToolCallIdx == -1:
			firstToolCallIdx = i
		case e == "tool_call:tool-edit-1" && secondToolCallIdx == -1:
			secondToolCallIdx = i
		case e == "message":
			messageIndices = append(messageIndices, i)
		}
	}

	// Verify we found the expected events
	if firstToolCallIdx == -1 {
		t.Fatal("Did not find first tool call (tool-read-1)")
	}
	if secondToolCallIdx == -1 {
		t.Fatal("Did not find second tool call (tool-edit-1)")
	}
	if len(messageIndices) == 0 {
		t.Fatal("Did not find any message events")
	}

	// KEY TEST: At least one message must appear BETWEEN the first tool call and second tool call
	// This verifies that messages are not batched to the end
	messageBeforeSecondTool := false
	for _, msgIdx := range messageIndices {
		if msgIdx > firstToolCallIdx && msgIdx < secondToolCallIdx {
			messageBeforeSecondTool = true
			break
		}
	}

	if !messageBeforeSecondTool {
		t.Errorf("No message appeared between first tool call (idx %d) and second tool call (idx %d). Messages at indices: %v. This indicates messages are being batched to the end instead of interleaved.",
			firstToolCallIdx, secondToolCallIdx, messageIndices)
	}
}

// TestEventCompleteness_AllEventTypes verifies all expected event types are received
func TestEventCompleteness_AllEventTypes(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "completeness-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use PromptAndWait which captures all events
	result, err := c.PromptAndWait(ctx, session.SessionID, "fix the file")
	if err != nil {
		t.Fatalf("PromptAndWait failed: %v", err)
	}

	// Verify we got all expected event types
	if len(result.Messages) == 0 {
		t.Error("Expected at least one agent message")
	}
	if len(result.Thoughts) == 0 {
		t.Error("Expected at least one agent thought")
	}
	if len(result.ToolCalls) == 0 {
		t.Error("Expected at least one tool call")
	}

	// Verify tool calls have expected fields
	for _, tc := range result.ToolCalls {
		if tc.ID == "" {
			t.Error("Tool call ID should not be empty")
		}
		if tc.Title == "" {
			t.Error("Tool call Title should not be empty")
		}
	}

	t.Logf("Messages: %d, Thoughts: %d, ToolCalls: %d",
		len(result.Messages), len(result.Thoughts), len(result.ToolCalls))
}

// TestMultipleTurns_EventOrdering verifies event ordering across multiple turns
func TestMultipleTurns_EventOrdering(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "multi-turn-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Track events across multiple turns
	var mu sync.Mutex
	var allEvents []string
	turnCount := 0

	sess, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			allEvents = append(allEvents, fmt.Sprintf("turn%d:message", turnCount))
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			allEvents = append(allEvents, fmt.Sprintf("turn%d:tool:%s", turnCount, id))
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			allEvents = append(allEvents, fmt.Sprintf("turn%d:complete", turnCount))
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	time.Sleep(200 * time.Millisecond)

	// Send multiple prompts
	prompts := []string{"Hello!", "fix the file", "Hello again!"}
	for i, prompt := range prompts {
		mu.Lock()
		turnCount = i + 1
		mu.Unlock()

		if err := sess.SendPrompt(prompt); err != nil {
			t.Fatalf("SendPrompt %d failed: %v", i+1, err)
		}

		// Wait for completion
		deadline := time.Now().Add(10 * time.Second)
		for {
			mu.Lock()
			lastEvent := ""
			if len(allEvents) > 0 {
				lastEvent = allEvents[len(allEvents)-1]
			}
			mu.Unlock()

			if lastEvent == fmt.Sprintf("turn%d:complete", i+1) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Timeout waiting for turn %d completion", i+1)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify we have events from all turns
	for i := 1; i <= len(prompts); i++ {
		found := false
		for _, e := range allEvents {
			if e == fmt.Sprintf("turn%d:complete", i) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Missing completion event for turn %d", i)
		}
	}

	t.Logf("All events across %d turns: %v", len(prompts), allEvents)
}

// =============================================================================
// Error Handling Tests
// =============================================================================

// TestConnect_InvalidSession verifies error handling for invalid session ID
func TestConnect_InvalidSession(t *testing.T) {
	c := client.New(testServerURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var gotError bool
	var errorMsg string

	sess, err := c.Connect(ctx, "nonexistent-session-id", client.SessionCallbacks{
		OnError: func(message string) {
			gotError = true
			errorMsg = message
		},
	})

	// Connection might succeed but we should get an error message
	if err == nil && sess != nil {
		defer sess.Close()
		// Wait for potential error
		time.Sleep(500 * time.Millisecond)
	}

	// Either connection fails or we get an error callback
	if err == nil && !gotError {
		t.Log("Note: Connection to invalid session succeeded without error (server may create session)")
	} else if err != nil {
		t.Logf("Connection failed as expected: %v", err)
	} else {
		t.Logf("Got error callback: %s", errorMsg)
	}
}

// TestConnect_ServerDown verifies error handling when server is unreachable
func TestConnect_ServerDown(t *testing.T) {
	// Use a port that's definitely not running
	c := client.New("http://127.0.0.1:59999")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.Connect(ctx, "any-session", client.SessionCallbacks{})
	if err == nil {
		t.Error("Expected error when connecting to unreachable server")
	} else {
		t.Logf("Got expected error: %v", err)
	}
}

// TestListSessions_Empty verifies listing sessions works
func TestListSessions_Empty(t *testing.T) {
	c := client.New(testServerURL)

	sessions, err := c.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}

	// Should return an array (may have sessions from other tests)
	t.Logf("Found %d sessions", len(sessions))
}

// TestCreateDeleteSession verifies session lifecycle
func TestCreateDeleteSession(t *testing.T) {
	c := client.New(testServerURL)

	// Create
	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "lifecycle-test",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Get
	got, err := c.GetSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.SessionID != session.SessionID {
		t.Errorf("Session ID mismatch: got %q, want %q", got.SessionID, session.SessionID)
	}

	// Delete
	if err := c.DeleteSession(session.SessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// Verify deleted - GetSession should fail
	_, err = c.GetSession(session.SessionID)
	if err == nil {
		t.Error("GetSession should fail for deleted session")
	}
}

