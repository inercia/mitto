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

// TestLazyACPSessionCreation verifies acceptance criteria for the lazy ACP session
// creation feature (mitto-8yv.2):
//
//  1. Creating a conversation while a same-workspace agent is busy returns
//     immediately with a usable conversation — no 503 / session_creation_timeout.
//  2. The deferred session/new completes on the first prompt and the response is delivered.
//
// Strategy:
//   - Session A sends a slow prompt (3 s delay via fixture) to occupy the shared
//     mock-ACP process. The mock server is single-threaded, so any new RPC blocks.
//   - While A's prompt is in-flight, we call CreateSession for session B and measure
//     the wall-clock latency. With the old code this would block for ≥3 s (or 503);
//     with the lazy approach it should complete in well under 1 s.
//   - After B is created we send its first prompt and assert the response arrives,
//     proving the deferred session/new succeeded end-to-end.
func TestLazyACPSessionCreation(t *testing.T) {
	ts := SetupTestServer(t)

	// ── Phase 1: Create session A and start a slow prompt ────────────────────
	sessA, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "lazy-test-A"})
	if err != nil {
		t.Fatalf("CreateSession A failed: %v", err)
	}
	t.Cleanup(func() { ts.Client.DeleteSession(sessA.SessionID) })

	ctxA, cancelA := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelA()

	var muA sync.Mutex
	aPromptComplete := false
	aConnected := false

	wsA, err := ts.Client.Connect(ctxA, sessA.SessionID, client.SessionCallbacks{
		OnConnected: func(_, _, _ string) {
			muA.Lock()
			aConnected = true
			muA.Unlock()
		},
		OnPromptComplete: func(_ int) {
			muA.Lock()
			aPromptComplete = true
			muA.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Connect A failed: %v", err)
	}
	defer wsA.Close()

	waitFor(t, 5*time.Second, func() bool {
		muA.Lock()
		defer muA.Unlock()
		return aConnected
	}, "session A connected")

	if err := wsA.LoadEvents(10, 0, 0); err != nil {
		t.Fatalf("LoadEvents A: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // let observer register

	// Send the slow prompt (3 s delay in the mock server fixture).
	// The mock processes RPCs sequentially, so session/new for B will queue
	// behind this prompt when using the old eager approach.
	if err := wsA.SendPrompt("LAZY_SESSION_SLOW_PROMPT"); err != nil {
		t.Fatalf("SendPrompt A (slow): %v", err)
	}

	// Give the mock a moment to start processing the slow prompt.
	time.Sleep(200 * time.Millisecond)

	// ── Phase 2: Create session B while session A's prompt is in-flight ──────
	createStart := time.Now()
	sessB, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "lazy-test-B"})
	createDuration := time.Since(createStart)

	if err != nil {
		t.Fatalf("CreateSession B while A is busy: %v", err)
	}
	t.Cleanup(func() { ts.Client.DeleteSession(sessB.SessionID) })

	t.Logf("CreateSession B returned in %v", createDuration)

	// Accept criterion 1: create should not block on the busy agent.
	// With the old code this took ≥3 s (or returned 503); with lazy it's sub-second.
	if createDuration > 2*time.Second {
		t.Errorf("CreateSession B took %v — should be < 2 s with lazy session creation (old code would block ≥3 s)",
			createDuration)
	}

	// ── Phase 3: Send B's first prompt and verify deferred handshake succeeds ─
	ctxB, cancelB := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelB()

	var muB sync.Mutex
	bMessages := []string{}
	bComplete := false
	bErrors := []string{}
	bConnected := false

	wsB, err := ts.Client.Connect(ctxB, sessB.SessionID, client.SessionCallbacks{
		OnConnected: func(_, _, _ string) {
			muB.Lock()
			bConnected = true
			muB.Unlock()
		},
		OnAgentMessage: func(html string) {
			muB.Lock()
			bMessages = append(bMessages, html)
			muB.Unlock()
		},
		OnPromptComplete: func(_ int) {
			muB.Lock()
			bComplete = true
			muB.Unlock()
		},
		OnError: func(msg string) {
			muB.Lock()
			bErrors = append(bErrors, msg)
			muB.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Connect B failed: %v", err)
	}
	defer wsB.Close()

	waitFor(t, 5*time.Second, func() bool {
		muB.Lock()
		defer muB.Unlock()
		return bConnected
	}, "session B connected")

	if err := wsB.LoadEvents(10, 0, 0); err != nil {
		t.Fatalf("LoadEvents B: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Send B's first prompt — this triggers ensureSharedACPSession (session/new)
	// followed by the actual prompt. We allow enough time for A to finish first.
	if err := wsB.SendPrompt("LAZY_SESSION_FIRST_PROMPT"); err != nil {
		t.Fatalf("SendPrompt B: %v", err)
	}

	// Accept criterion 2: B's first prompt must succeed and deliver a response.
	// Allow 20 s total (A's slow prompt takes 3 s, then B's session/new + prompt).
	waitFor(t, 20*time.Second, func() bool {
		muB.Lock()
		defer muB.Unlock()
		return bComplete
	}, "session B prompt complete")

	muB.Lock()
	defer muB.Unlock()

	if len(bErrors) > 0 {
		t.Errorf("Session B received errors: %v", bErrors)
	}
	fullResp := strings.Join(bMessages, "")
	if !strings.Contains(fullResp, "deferred session succeeded") {
		t.Errorf("Session B did not receive expected response; got: %q", fullResp)
	}

	// Also wait for A's prompt to complete (clean teardown).
	waitFor(t, 10*time.Second, func() bool {
		muA.Lock()
		defer muA.Unlock()
		return aPromptComplete
	}, "session A prompt complete")
}
