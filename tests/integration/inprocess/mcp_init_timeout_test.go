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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
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

// TestMCPInitTimeout_AutoResume_NoArchive verifies mitto-54k.6: a background
// auto-resume that fails with an MCP-init timeout must NOT increment the hard
// ACPStartFailureCount counter and must NOT auto-archive the stored session.
// The transient cold-start timeout should be treated as retryable so that a
// later resume attempt (once the shared ACP process warms) can succeed.
//
// Strategy:
//   - Seed a session record directly into the store (no ACP spawn yet), so the
//     failure counter starts at 0.
//   - Set MOCK_MCP_INIT_TIMEOUT_MS so the mock agent aborts session/new with the
//     MCP-init-timeout stderr signal.
//   - Call ResumeSession and assert it returns an MCP-init-timeout error.
//   - Assert the persisted metadata still has ACPStartFailureCount == 0 and
//     Archived == false with no ArchiveReasonACPFailures.
func TestMCPInitTimeout_AutoResume_NoArchive(t *testing.T) {
	// Force the mock agent to time out on MCP-init for every session/new.
	t.Setenv("MOCK_MCP_INIT_TIMEOUT_MS", "300")

	ts := SetupTestServer(t, func(c *web.Config) {
		if c.MittoConfig == nil {
			c.MittoConfig = &config.Config{}
		}
		// Extended budget so the stderr signal (not the deadline) is what aborts.
		c.MittoConfig.Session = &config.SessionConfig{McpInitTimeout: "10s"}
	})

	// Seed a session record directly into the store. This bypasses any ACP
	// spawn during CreateSession and starts the failure counter at 0.
	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	sessionID := uuid.NewString()
	meta := session.Metadata{
		SessionID:  sessionID,
		Name:       "mcp-init-timeout-autoresume",
		ACPServer:  "mock-acp",
		WorkingDir: workspaceDir,
		Status:     session.SessionStatusActive,
	}
	if err := ts.Store.Create(meta); err != nil {
		t.Fatalf("Store.Create failed: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sessionID) })

	// Sanity: fresh session must have a zero failure counter and not be archived.
	pre, err := ts.Store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata (pre) failed: %v", err)
	}
	if pre.ACPStartFailureCount != 0 {
		t.Fatalf("Pre: ACPStartFailureCount = %d, want 0", pre.ACPStartFailureCount)
	}
	if pre.Archived {
		t.Fatalf("Pre: session already archived")
	}

	// Drive the resume. The MCP-init timeout must be returned as an error.
	sm := ts.Server.GetSessionManager()
	bs, err := sm.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
	if err == nil {
		if bs != nil {
			bs.Close("test_cleanup")
		}
		t.Fatalf("ResumeSession unexpectedly succeeded under MCP-init timeout")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "mcp initialization timed out") {
		t.Fatalf("ResumeSession error = %q, want it to contain 'mcp initialization timed out'", err.Error())
	}

	// Post-condition: the failure counter must NOT have been incremented and
	// the session must NOT have been auto-archived.
	post, err := ts.Store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata (post) failed: %v", err)
	}
	if post.ACPStartFailureCount != 0 {
		t.Errorf("Post: ACPStartFailureCount = %d, want 0 (MCP-init timeout must not count as hard failure)",
			post.ACPStartFailureCount)
	}
	if post.Archived {
		t.Errorf("Post: session was auto-archived (reason=%q); MCP-init timeout must not trigger archive",
			post.ArchiveReason)
	}
	if post.ArchiveReason == session.ArchiveReasonACPFailures {
		t.Errorf("Post: ArchiveReason = %q, want empty (no ACP-failures archive)", post.ArchiveReason)
	}

	// Confirm the session was not registered as a running BackgroundSession.
	if got := sm.GetSession(sessionID); got != nil {
		t.Errorf("Session unexpectedly registered as running after failed resume")
		got.Close("test_cleanup")
	}
}
