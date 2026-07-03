//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// sessionListIsStreaming fetches GET /api/sessions and returns the is_streaming
// flag the server reports for the given session id. It fails the test on any
// transport/decode error, or if the session is not present in the response.
func sessionListIsStreaming(t *testing.T, ts *TestServer, sessionID string) bool {
	t.Helper()
	resp, err := ts.HTTPServer.Client().Get(ts.HTTPServer.URL + "/mitto/api/sessions")
	if err != nil {
		t.Fatalf("GET /api/sessions failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/sessions: unexpected status %d", resp.StatusCode)
	}
	var sessions []struct {
		SessionID   string `json:"session_id"`
		IsStreaming bool   `json:"is_streaming"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatalf("GET /api/sessions: decode failed: %v", err)
	}
	for _, s := range sessions {
		if s.SessionID == sessionID {
			return s.IsStreaming
		}
	}
	t.Fatalf("session %s not found in /api/sessions response", sessionID)
	return false
}

// TestSessionList_IsStreamingSurvivesReload is the regression test for mitto-wktp.
//
// The sidebar pulsing ring is driven by per-session isStreaming. On a full page
// reload the frontend rebuilds its list from GET /api/sessions, so that endpoint
// must report streaming state — otherwise the ring disappears for sessions that
// are still actively prompting until the next live session_streaming event fires.
//
// This test drives a real, slow streaming prompt through the mock ACP and asserts
// that GET /api/sessions reports is_streaming=true for the duration of the prompt
// (the value a reloading client would re-hydrate from), then clears to false once
// the prompt completes.
func TestSessionList_IsStreamingSurvivesReload(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	var promptComplete int32

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			atomic.AddInt32(&promptComplete, 1)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Register as an observer so the BackgroundSession streams to this client.
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Sanity: a freshly created, idle session must not report streaming.
	if sessionListIsStreaming(t, ts, sess.SessionID) {
		t.Fatalf("is_streaming should be false before any prompt is sent")
	}

	// Trigger the slow-response fixture (8 chunks x 500ms ≈ several seconds of
	// streaming), giving a wide, race-free window to observe the list API state.
	if err := ws.SendPrompt("Simulate a slow response"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Core regression assertion: while the agent is streaming, the session list
	// API reports is_streaming=true. This is the state a reloading client
	// re-hydrates so the sidebar pulsing ring survives Cmd-R (mitto-wktp).
	waitFor(t, 10*time.Second, func() bool {
		return sessionListIsStreaming(t, ts, sess.SessionID)
	}, "is_streaming=true on GET /api/sessions during prompt")

	// Once the prompt finishes, the tracked streaming state must clear.
	waitFor(t, 30*time.Second, func() bool {
		return atomic.LoadInt32(&promptComplete) > 0
	}, "prompt completion")

	waitFor(t, 5*time.Second, func() bool {
		return !sessionListIsStreaming(t, ts, sess.SessionID)
	}, "is_streaming=false on GET /api/sessions after completion")
}
