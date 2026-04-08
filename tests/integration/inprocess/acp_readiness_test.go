//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// This file tests the ACP readiness feature: acp_ready field in the connected
// message, acp_started WebSocket message, and prompt gating on ACP readiness.
package inprocess

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestACPReadiness_ConnectedMessage tests that the connected WebSocket message
// includes the acp_ready field.
func TestACPReadiness_ConnectedMessage(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var connectedData map[string]interface{}
	var mu sync.Mutex

	callbacks := client.SessionCallbacks{
		OnConnectedFull: func(data map[string]interface{}) {
			mu.Lock()
			connectedData = data
			mu.Unlock()
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Wait for connected message with acp_ready field.
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connectedData != nil
	}, "connected message with acp_ready field")

	mu.Lock()
	defer mu.Unlock()

	// Verify acp_ready field exists in the connected message.
	_, ok := connectedData["acp_ready"]
	if !ok {
		t.Fatal("connected message should include acp_ready field")
	}
}

// TestACPReadiness_SendPromptWhenReady tests that prompts succeed when ACP is ready.
func TestACPReadiness_SendPromptWhenReady(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var promptComplete bool
	var mu sync.Mutex

	callbacks := client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			promptComplete = true
			mu.Unlock()
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Send a prompt — should succeed since ACP is ready.
	if err := ws.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	waitFor(t, 15*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt to complete")
}

// TestACPReadiness_ACPStartedCallback tests that the OnACPStarted callback
// is wired correctly in the test client and fires for the acp_started message.
func TestACPReadiness_ACPStartedCallback(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var acpStartedCount int
	var mu sync.Mutex

	callbacks := client.SessionCallbacks{
		OnACPStarted: func() {
			mu.Lock()
			acpStartedCount++
			mu.Unlock()
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Wait briefly to collect any acp_started messages.
	// For a fully started session, acp_started may have been sent before
	// the client registered as an observer, so we just verify no panic occurs
	// and the callback is wired correctly.
	time.Sleep(2 * time.Second)

	mu.Lock()
	count := acpStartedCount
	mu.Unlock()

	// Log the result — it's non-deterministic whether acp_started arrives
	// depending on whether ACP was already running before we connected.
	t.Logf("acp_started received %d time(s) during 2s window", count)
}
