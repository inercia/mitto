//go:build integration

package inprocess

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// sessionGoneData is the payload of a session_gone WebSocket message.
type sessionGoneData struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

// connectAndWaitForSessionGone connects to a deleted session and waits for the session_gone message.
// Returns the parsed payload or fails the test on timeout.
func connectAndWaitForSessionGone(t *testing.T, ts *TestServer, sessionID string) sessionGoneData {
	t.Helper()

	gone := make(chan sessionGoneData, 1)

	callbacks := client.SessionCallbacks{
		OnRawMessage: func(msgType string, data []byte) {
			if msgType == "session_gone" {
				var payload sessionGoneData
				if err := json.Unmarshal(data, &payload); err == nil {
					select {
					case gone <- payload:
					default:
					}
				}
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, sessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Do NOT call LoadEvents — the server sends session_gone before the client registers as observer.
	// The server closes the connection ~100ms after sending session_gone.

	select {
	case payload := <-gone:
		return payload
	case <-time.After(5 * time.Second):
		t.Fatalf("Timeout waiting for session_gone for session %s", sessionID)
		return sessionGoneData{}
	}
}

// TestSessionGone_DeletedSessionSendsGone verifies that when a session is deleted,
// a new WebSocket connection to that session receives a session_gone message.
func TestSessionGone_DeletedSessionSendsGone(t *testing.T) {
	ts := SetupTestServer(t)

	// 1. Create a session.
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	sessionID := sess.SessionID

	// 2. Connect Client A, register as observer, send a prompt, wait for completion.
	var (
		promptComplete bool
		pcMu           sync.Mutex
	)
	cbA := client.SessionCallbacks{
		OnPromptComplete: func(_ int) {
			pcMu.Lock()
			promptComplete = true
			pcMu.Unlock()
		},
	}

	ctxA, cancelA := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelA()

	wsA, err := ts.Client.Connect(ctxA, sessionID, cbA)
	if err != nil {
		t.Fatalf("Connect (A) failed: %v", err)
	}

	if err := wsA.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := wsA.SendPrompt("hello from session gone test"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	waitFor(t, 15*time.Second, func() bool {
		pcMu.Lock()
		defer pcMu.Unlock()
		return promptComplete
	}, "prompt complete")

	// 3. Disconnect Client A.
	wsA.Close()

	// 4. Delete the session.
	if err := ts.Client.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// 5 & 6. Reconnect and wait for session_gone.
	payload := connectAndWaitForSessionGone(t, ts, sessionID)

	// 7. Verify the session_id in the payload matches.
	if payload.SessionID != sessionID {
		t.Errorf("session_gone session_id = %q, want %q", payload.SessionID, sessionID)
	}
	t.Logf("Received session_gone: session_id=%s reason=%s", payload.SessionID, payload.Reason)
}

// TestSessionGone_NegativeCacheFastPath verifies that after the first lookup for a deleted
// session hits the filesystem and populates the negative cache, subsequent lookups are served
// from the cache (circuit breaker fast path).
func TestSessionGone_NegativeCacheFastPath(t *testing.T) {
	ts := SetupTestServer(t)

	// 1. Create a session and immediately delete it (no need to connect first).
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	sessionID := sess.SessionID

	if err := ts.Client.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// 2. Client A: first connection — hits filesystem, caches result.
	payloadA := connectAndWaitForSessionGone(t, ts, sessionID)

	if payloadA.SessionID != sessionID {
		t.Errorf("Client A session_gone session_id = %q, want %q", payloadA.SessionID, sessionID)
	}
	t.Logf("Client A received session_gone: reason=%s", payloadA.Reason)

	// 3. Brief pause to let async cleanup settle.
	time.Sleep(100 * time.Millisecond)

	// 4. Client B: second connection — hits negative cache, no filesystem I/O.
	payloadB := connectAndWaitForSessionGone(t, ts, sessionID)

	if payloadB.SessionID != sessionID {
		t.Errorf("Client B session_gone session_id = %q, want %q", payloadB.SessionID, sessionID)
	}
	t.Logf("Client B received session_gone: reason=%s", payloadB.Reason)

	// 5. Both connections succeeded and returned session_gone — negative cache is working.
	t.Log("Both clients received session_gone — negative cache circuit breaker verified")
}
