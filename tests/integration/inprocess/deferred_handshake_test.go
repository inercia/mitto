//go:build integration

package inprocess

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/session"
)

// TestDeferredHandshakePermanentFailure verifies that when session/new always fails,
// the error is persisted as a reopen-visible event and is_prompting is cleared (mitto-8uz).
func TestDeferredHandshakePermanentFailure(t *testing.T) {
	// Inject failure for all session/new calls so handshake exhausts all retries.
	t.Setenv("MOCK_NEW_SESSION_FAIL_FIRST", "100")

	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "deferred-fail-permanent"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(func() { ts.Client.DeleteSession(sess.SessionID) })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	promptComplete := false
	var errors []string

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(_ int) {
			mu.Lock()
			promptComplete = true
			mu.Unlock()
		},
		OnError: func(msg string) {
			mu.Lock()
			errors = append(errors, msg)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := ws.SendPrompt("hello deferred-fail-permanent"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for prompting to finish: 3 retry attempts x (immediate error + backoff) ≈ 3–4 s.
	// Use 30s to accommodate slow CI environments.
	waitFor(t, 30*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete || len(errors) > 0
	}, "prompt complete or error received")

	// is_prompting must be false — no stuck spinner.
	bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
	if bs != nil && bs.IsPrompting() {
		t.Error("Expected is_prompting=false after handshake exhaustion")
	}

	// A persisted EventTypeError must exist so the failed turn is visible on reopen.
	time.Sleep(200 * time.Millisecond)
	events, err := ts.Store.ReadEvents(sess.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	hasError := false
	for _, e := range events {
		if e.Type == session.EventTypeError {
			hasError = true
			t.Logf("Found persisted error event seq=%d: %+v", e.Seq, e.Data)
			break
		}
	}
	if !hasError {
		types := make([]string, len(events))
		for i, e := range events {
			types[i] = string(e.Type)
		}
		t.Errorf("Expected persisted EventTypeError in session history; got event types: %v", types)
	}
}

// TestDeferredHandshakeRetrySucceeds verifies that when session/new fails once but
// succeeds on the 2nd attempt, the first prompt is answered normally with no error (mitto-8uz).
func TestDeferredHandshakeRetrySucceeds(t *testing.T) {
	// First session/new call fails; the retry (2nd call) succeeds.
	t.Setenv("MOCK_NEW_SESSION_FAIL_FIRST", "1")

	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "deferred-retry-ok"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(func() { ts.Client.DeleteSession(sess.SessionID) })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	promptComplete := false
	var agentMessages []string
	var errors []string

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			agentMessages = append(agentMessages, html)
			mu.Unlock()
		},
		OnPromptComplete: func(_ int) {
			mu.Lock()
			promptComplete = true
			mu.Unlock()
		},
		OnError: func(msg string) {
			mu.Lock()
			errors = append(errors, msg)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := ws.SendPrompt("hello deferred-retry-ok"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Allow 30s: 1s retry backoff + session/new + prompt processing.
	waitFor(t, 30*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete after retry")

	mu.Lock()
	errsCopy := append([]string{}, errors...)
	msgsCopy := append([]string{}, agentMessages...)
	mu.Unlock()

	if len(errsCopy) > 0 {
		t.Errorf("Expected no errors after successful retry, got: %v", errsCopy)
	}
	if len(msgsCopy) == 0 {
		t.Error("Expected agent message after successful retry, got none")
	}

	// No persisted error event should exist — the message self-healed.
	time.Sleep(200 * time.Millisecond)
	events, readErr := ts.Store.ReadEvents(sess.SessionID)
	if readErr != nil {
		t.Fatalf("ReadEvents failed: %v", readErr)
	}
	for _, e := range events {
		if e.Type == session.EventTypeError {
			t.Errorf("Unexpected persisted EventTypeError after successful retry: seq=%d data=%+v", e.Seq, e.Data)
		}
	}
}
