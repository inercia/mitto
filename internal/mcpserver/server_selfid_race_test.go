package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestParallelSelfIDResolution_ThunderingHerdOnSingleMCPSession_mitto_b9q is
// the reproduction test for mitto-b9q: parallel mitto_* tool calls (e.g.
// several mitto_conversation_new spawns) race on self_id resolution when
// they all arrive over ONE shared MCP protocol session — exactly how a
// single Auggie conversation (no HTTP MCP capability, see mitto-b9q
// Investigation) fires several tool calls in parallel.
//
// Root cause (see mitto-b9q Investigation comment, tier: Reasoning):
// resolveSelfIDWithMCP (server.go) has NO single-flight collapsing of
// concurrent resolves sharing one MCP protocol session. Phase 1 (MCP-session
// cache) is warmed only by the FIRST successful resolve; Phase 2
// (WaitForPendingRequest) is a FIFO queue keyed by the session ID. When the
// ACP layer has only registered ONE pending request within the window (the
// lagging/undercounted registration behavior observed live), exactly one of
// N concurrent callers consumes it and resolves instantly; the remaining
// N-1 have already missed the (not-yet-warmed) Phase-1 cache and block the
// full pendingRequestTimeout (5s) in WaitForPendingRequest before failing —
// reproducing the reported "9x Pending request not found within timeout"
// symptom.
//
// This test calls resolveSelfIDWithMCP directly (in-process, no HTTP
// round-trip) against a REAL *mcp.ServerSession captured via a probe tool
// call over a real Streamable HTTP connection. Going through the real
// transport once to obtain req.Session, then firing the concurrent burst
// in-process, reproduces the true race deterministically: routing all N
// concurrent calls through real HTTP round-trips (attempted first) let
// JSON-RPC serialization naturally stagger the calls enough to warm the
// Phase-1 cache between them, masking the bug.
//
// This test currently FAILS (pre-fix): it asserts the acceptance criteria
// from mitto-b9q — firing N>=4 concurrent resolves sharing one MCP protocol
// session succeeds for all callers without timing out — which the current
// code does not satisfy. It must start passing once the reproduce/fix phase
// adds single-flight collapsing keyed by the MCP protocol session ID.
func TestParallelSelfIDResolution_ThunderingHerdOnSingleMCPSession_mitto_b9q(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	targetID := session.GenerateSessionID()
	if err := store.Create(session.Metadata{SessionID: targetID, Name: "target", ACPServer: "test", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(targetID, nil, logger); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	// Probe tool: captures the real *mcp.CallToolRequest (and its
	// req.Session) for a single MCP protocol session, WITHOUT touching
	// resolveSelfIDWithMCP or warming any cache.
	var capturedReq *mcp.CallToolRequest
	mcp.AddTool(srv.mcpServer, &mcp.Tool{Name: "test_capture_session"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			capturedReq = req
			return nil, struct{}{}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv.mcpServer
	}, mcpStreamableHTTPOptions())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "mitto-b9q-repro-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer clientSession.Close()

	if _, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "test_capture_session"}); err != nil {
		t.Fatalf("capture probe call: %v", err)
	}
	if capturedReq == nil || capturedReq.Session == nil {
		t.Fatal("failed to capture a real *mcp.CallToolRequest with a non-nil Session")
	}

	// Mirror the ACP layer observing exactly ONE ToolCall event within the
	// window before the concurrent burst arrives — the lagging/undercounted
	// registration behavior described in the mitto-b9q investigation. All
	// callers below share this SAME MCP protocol session (single
	// req.Session.ID()), exactly like one Auggie conversation firing several
	// mitto_* tool calls in parallel.
	if !srv.RegisterPendingRequest(targetID, targetID) {
		t.Fatal("RegisterPendingRequest unexpectedly rejected")
	}

	const concurrency = 4
	type callResult struct {
		sessionID string
		elapsed   time.Duration
	}
	results := make([]callResult, concurrency)

	var gate sync.WaitGroup
	gate.Add(1)
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(i int) {
			defer wg.Done()
			gate.Wait() // release all goroutines together

			callStart := time.Now()
			resolved := srv.resolveSelfIDWithMCP(targetID, capturedReq)
			results[i] = callResult{sessionID: resolved, elapsed: time.Since(callStart)}
		}(i)
	}
	gate.Done()

	wg.Wait()

	var failures int
	var maxElapsed time.Duration
	for i, r := range results {
		if r.sessionID != targetID {
			failures++
			t.Logf("caller %d: FAILED after %v (resolved=%q, want %q)", i, r.elapsed, r.sessionID, targetID)
		} else {
			t.Logf("caller %d: resolved %q in %v", i, r.sessionID, r.elapsed)
		}
		if r.elapsed > maxElapsed {
			maxElapsed = r.elapsed
		}
	}

	if failures > 0 {
		t.Errorf("mitto-b9q regression: %d/%d concurrent resolveSelfIDWithMCP calls sharing a single MCP "+
			"protocol session failed to resolve self_id=%q (thundering-herd race: only one pending-request "+
			"registration is consumed, the remaining callers miss the not-yet-warmed Phase-1 cache and time "+
			"out in WaitForPendingRequest). Max observed latency: %v (pendingRequestTimeout=%v). "+
			"See mitto-b9q Investigation comment.",
			failures, concurrency, targetID, maxElapsed, pendingRequestTimeout)
	}
	if maxElapsed >= pendingRequestTimeout {
		t.Errorf("mitto-b9q regression: slowest concurrent resolve took %v (>= pendingRequestTimeout=%v) — "+
			"reproduces the reported 5s stall", maxElapsed, pendingRequestTimeout)
	}
}
