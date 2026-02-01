//go:build integration

// Package client contains integration tests for multi-client scenarios.
// These tests verify that multiple clients connected to the same session
// receive events correctly and can interact without interference.
package client

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// =============================================================================
// Multi-Client Tests
// =============================================================================

// TestMultiClient_BothReceiveEvents verifies that two clients connected to the
// same session both receive all events when one sends a prompt.
func TestMultiClient_BothReceiveEvents(t *testing.T) {
	c := client.New(testServerURL)

	// Create a session
	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "multiclient-both-receive",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Track events for both clients
	var mu sync.Mutex
	client1Events := make([]string, 0)
	client2Events := make([]string, 0)
	client1Done := make(chan struct{})
	client2Done := make(chan struct{})
	client1Connected := make(chan struct{})
	client2Connected := make(chan struct{})

	// Connect client 1
	sess1, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client1Events = append(client1Events, "message")
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			client1Events = append(client1Events, fmt.Sprintf("tool:%s", id))
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			client1Events = append(client1Events, "complete")
			mu.Unlock()
			close(client1Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	// Connect client 2
	sess2, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Connected)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client2Events = append(client2Events, "message")
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			client2Events = append(client2Events, fmt.Sprintf("tool:%s", id))
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			client2Events = append(client2Events, "complete")
			mu.Unlock()
			close(client2Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	// Wait for both clients to connect
	select {
	case <-client1Connected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 1 to connect")
	}
	select {
	case <-client2Connected:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 2 to connect")
	}

	// Small delay to ensure both are fully registered
	time.Sleep(100 * time.Millisecond)

	// Client 1 sends a prompt
	if err := sess1.SendPrompt("Hello!"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for both clients to receive completion
	select {
	case <-client1Done:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 1 completion")
	}
	select {
	case <-client2Done:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 2 completion")
	}

	mu.Lock()
	defer mu.Unlock()

	// Both clients should have received events
	if len(client1Events) == 0 {
		t.Error("Client 1 received no events")
	}
	if len(client2Events) == 0 {
		t.Error("Client 2 received no events")
	}

	// Both should have received the completion event
	if client1Events[len(client1Events)-1] != "complete" {
		t.Errorf("Client 1 last event should be 'complete', got %q", client1Events[len(client1Events)-1])
	}
	if client2Events[len(client2Events)-1] != "complete" {
		t.Errorf("Client 2 last event should be 'complete', got %q", client2Events[len(client2Events)-1])
	}

	t.Logf("Client 1 events: %v", client1Events)
	t.Logf("Client 2 events: %v", client2Events)
}

// TestMultiClient_PromptFromOneReceiveOnAnother verifies that when client A
// sends a prompt, client B receives the user_prompt event.
func TestMultiClient_PromptFromOneReceiveOnAnother(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "multiclient-prompt-broadcast",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var mu sync.Mutex
	var client2ReceivedPrompt bool
	var client2PromptMessage string
	var client1ClientID string
	client1Connected := make(chan struct{})
	client2Connected := make(chan struct{})
	client1Done := make(chan struct{})
	client2Done := make(chan struct{})

	// Connect client 1
	sess1, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			mu.Lock()
			client1ClientID = clientID
			mu.Unlock()
			close(client1Connected)
		},
		OnPromptComplete: func(eventCount int) {
			close(client1Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	// Connect client 2
	sess2, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Connected)
		},
		OnUserPrompt: func(senderID, promptID, message string) {
			mu.Lock()
			client2ReceivedPrompt = true
			client2PromptMessage = message
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client2Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	// Wait for both to connect
	<-client1Connected
	<-client2Connected
	time.Sleep(100 * time.Millisecond)

	// Client 1 sends a prompt
	testMessage := "Hello from client 1!"
	if err := sess1.SendPrompt(testMessage); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for completion
	<-client1Done
	<-client2Done

	mu.Lock()
	defer mu.Unlock()

	// Client 2 should have received the user_prompt event
	if !client2ReceivedPrompt {
		t.Error("Client 2 did not receive user_prompt event")
	}
	if client2PromptMessage != testMessage {
		t.Errorf("Client 2 received message %q, want %q", client2PromptMessage, testMessage)
	}

	t.Logf("Client 1 ID: %s", client1ClientID)
	t.Logf("Client 2 received prompt: %v, message: %q", client2ReceivedPrompt, client2PromptMessage)
}

// TestMultiClient_DisconnectOneOtherContinues verifies that when one client
// disconnects, the other client continues to receive events normally.
func TestMultiClient_DisconnectOneOtherContinues(t *testing.T) {
	c := client.New(testServerURL)

	session, err := c.CreateSession(client.CreateSessionRequest{
		Name:       "multiclient-disconnect",
		WorkingDir: testWorkspace,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer c.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var mu sync.Mutex
	client2Events := make([]string, 0)
	client1Connected := make(chan struct{})
	client2Connected := make(chan struct{})
	client2Done := make(chan struct{})

	// Connect client 1
	sess1, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}

	// Connect client 2
	sess2, err := c.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Connected)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client2Events = append(client2Events, "message")
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			client2Events = append(client2Events, "complete")
			mu.Unlock()
			close(client2Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	// Wait for both to connect
	<-client1Connected
	<-client2Connected
	time.Sleep(100 * time.Millisecond)

	// Disconnect client 1
	sess1.Close()
	time.Sleep(100 * time.Millisecond)

	// Client 2 sends a prompt (should still work)
	if err := sess2.SendPrompt("Hello after disconnect!"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for client 2 to receive completion
	select {
	case <-client2Done:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 2 completion")
	}

	mu.Lock()
	defer mu.Unlock()

	// Client 2 should have received events
	if len(client2Events) == 0 {
		t.Error("Client 2 received no events after client 1 disconnected")
	}
	if client2Events[len(client2Events)-1] != "complete" {
		t.Errorf("Client 2 last event should be 'complete', got %q", client2Events[len(client2Events)-1])
	}

	t.Logf("Client 2 events after client 1 disconnect: %v", client2Events)
}

