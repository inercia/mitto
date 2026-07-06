//go:build integration

package inprocess

// Integration tests for the MCP-init extended-budget policy (mitto-8ul.1).
//
// The mock ACP server accepts two env vars:
//   - MOCK_MCP_INIT_DELAY_MS: delay session/new by this many ms while emitting
//     an MCP-init progress line on stderr. Mitto's stderr monitor uses that
//     progress line to fire the onMCPInitProgress callback and widen its RPC
//     deadline for the cold session/new call.
//   - MOCK_MCP_INIT_TIMEOUT_MS: after this many ms, emit an MCP-init-timeout
//     line on stderr and fail the pending session/new with a JSON-RPC error.
//     Mitto's stderr monitor should abort the pending RPC promptly and surface
//     an actionable permanent error rather than "context deadline exceeded".

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
)

// TestMCPInitTimeout_FailsFastOnStderrSignal verifies that a mock agent whose
// stderr reports "MCP initialization timed out" causes Mitto to abort the pending
// session/new with an actionable permanent error, without waiting the full RPC
// deadline. mitto-8ul.1.
func TestMCPInitTimeout_FailsFastOnStderrSignal(t *testing.T) {
	// Mock agent will emit the MCP-init progress line, wait 500ms, then emit
	// the MCP-init-timeout line and fail session/new.
	t.Setenv("MOCK_MCP_INIT_TIMEOUT_MS", "500")

	ts := SetupTestServer(t, func(c *web.Config) {
		if c.MittoConfig == nil {
			c.MittoConfig = &config.Config{}
		}
		// Extended budget of 10s so we would otherwise wait a long time — the test
		// asserts we bail well before that on the stderr signal.
		c.MittoConfig.Session = &config.SessionConfig{McpInitTimeout: "10s"}
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "mcp-init-timeout"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var mu sync.Mutex
	var errors []string
	promptComplete := false

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

	start := time.Now()
	if err := ws.SendPrompt("hello mcp-init-timeout"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// The RPC should fail promptly on the stderr signal (well before the 10s
	// extended budget elapses). Give the server up to 20s to record the failure.
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete || len(errors) > 0
	}, "prompt complete or error received")

	elapsed := time.Since(start)
	if elapsed > 8*time.Second {
		t.Errorf("Expected fail-fast on MCP-init-timeout stderr signal; took %v", elapsed)
	}

	// is_prompting must be false — no stuck spinner.
	bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
	if bs != nil && bs.IsPrompting() {
		t.Error("Expected is_prompting=false after MCP-init timeout")
	}

	// A persisted EventTypeError must exist so the failed turn is reopen-visible.
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
		t.Error("Expected persisted EventTypeError after MCP-init-timeout abort")
	}
}

// TestMCPInitDelay_ExtendedBudgetAllowsSuccess verifies that a cold session/new
// which takes longer than the normal 25s budget (simulated at ~500ms so the test
// is fast) still succeeds when the MCP-init extended budget is configured
// generously. mitto-8ul.1.
func TestMCPInitDelay_ExtendedBudgetAllowsSuccess(t *testing.T) {
	// Mock agent delays session/new by 500ms while emitting the MCP-init progress line.
	t.Setenv("MOCK_MCP_INIT_DELAY_MS", "500")

	ts := SetupTestServer(t, func(c *web.Config) {
		if c.MittoConfig == nil {
			c.MittoConfig = &config.Config{}
		}
		// Any non-empty enabled value; even the default 240s is fine since the mock
		// only delays 500ms.
		c.MittoConfig.Session = &config.SessionConfig{McpInitTimeout: "4m"}
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "mcp-init-delay-ok"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	if err := ws.SendPrompt("hello mcp-init-delay-ok"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete despite MCP-init delay")

	mu.Lock()
	errsCopy := append([]string{}, errors...)
	mu.Unlock()
	if len(errsCopy) > 0 {
		t.Errorf("Expected no errors with extended MCP-init budget, got: %v", errsCopy)
	}
}
