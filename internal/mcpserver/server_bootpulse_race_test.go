package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestResolveSelfIDWithMCP_BootPulseRace_mitto_8r1 is the reproduction test
// for mitto-8r1: mitto_ui_* tools (and mitto_conversation_get_current) fail
// with "session not found" when a run_on_start boot pulse fires against a
// just-resumed session whose per-session MCP correlation is not yet ready.
//
// Root cause (see mitto-8r1 Investigation comment, tier: Reasoning):
// resolveSelfIDWithMCP's Phase 1 (MCP-session cache, lookupMCPSession) is
// cold on the FIRST call for a protocol session — exactly the boot-pulse
// case, since the pulse's own tool call is the first thing the resumed
// session does. Phase 2 (WaitForPendingRequest) waits up to
// pendingRequestTimeout (5s) for the ACP layer to OBSERVE a ToolCall event
// and register the requestID==sessionID pending-request mapping
// (cbRegisterPendingMCPRequest). For legacy-correlation agents (e.g. Auggie,
// which never advertises McpCapabilities.Http and so never receives the
// mitto-apvg HTTP binding), that registration is the ONLY correlation
// signal. Under post-restart cold-start load the ACP-observed registration
// can land AFTER the 5s deadline (the timing inversion documented on
// mitto-220), so the resolve call — despite the session being genuinely
// registered (getSession != nil) — times out and returns "", surfacing as
// "session not found: the self_id '...' could not be resolved" in
// handleUINotify / handleUIOptions / handleUITextbox / handleUIForm /
// handleGetCurrentSession.
//
// This test calls resolveSelfIDWithMCP directly (in-process) against a REAL
// *mcp.ServerSession captured via a probe tool call over a real Streamable
// HTTP connection — mirroring
// TestParallelSelfIDResolution_ThunderingHerdOnSingleMCPSession_mitto_b9q's
// proven pattern for obtaining a genuine req.Session. NO pending request is
// registered manually before the resolve call: this test asserts what
// RegisterSession itself must guarantee at registration time, so it
// exercises the exact boot-pulse scenario — resolving self_id immediately
// after the session is registered/resumed, before any tool call has ever
// been observed by the ACP layer.
//
// Fix (see mitto-8r1 Fix comment, tier: Coding): RegisterSession now
// eagerly seeds a pending-request correlation entry
// (RegisterPendingRequest(sessionID, sessionID)) at registration time —
// covering both a fresh session start and the idempotent restart/resume
// branch — so the mapping a legacy (non-HTTP-MCP) agent's FIRST mitto_*
// tool call needs already exists before that call is ever made, instead of
// depending on the ACP layer observing the tool call in real time and
// racing pendingRequestTimeout.
//
// An earlier version of this test modeled the race by registering the
// pending request from a goroutine that deliberately fired AFTER
// pendingRequestTimeout, then asserted resolution should still succeed.
// That is not fixable without weakening the security invariant documented
// on resolveSelfIDWithMCP (a caller-supplied registered conversation ID is
// never accepted as authentication on its own) — WaitForPendingRequest
// cannot find an entry that does not exist yet when it gives up. This
// version instead pins the real fix's invariant: eager registration at
// RegisterSession time, exercised by simply NOT pre-registering anything
// and resolving right away.
//
// Pre-fix this test FAILS: RegisterSession does not seed anything, so a
// resolve attempted immediately after registration (before any tool call
// has been observed by the ACP layer) exhausts the full
// pendingRequestTimeout and returns "" — reproducing "session not found:
// the self_id could not be resolved". Post-fix it resolves near-instantly
// because RegisterSession already seeded the mapping.
func TestResolveSelfIDWithMCP_BootPulseRace_mitto_8r1(t *testing.T) {
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

	// Probe tool: captures a real *mcp.CallToolRequest (and its req.Session)
	// for a fresh MCP protocol session, WITHOUT touching resolveSelfIDWithMCP
	// or warming any Phase-1 cache — exactly the state of a just-resumed
	// session's first tool call after a restart.
	var capturedReq *mcp.CallToolRequest
	mcp.AddTool(srv.mcpServer, &mcp.Tool{Name: "test_capture_session_8r1"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			capturedReq = req
			return nil, struct{}{}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv.mcpServer
	}, mcpStreamableHTTPOptions())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "mitto-8r1-repro-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer clientSession.Close()

	if _, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "test_capture_session_8r1"}); err != nil {
		t.Fatalf("capture probe call: %v", err)
	}
	if capturedReq == nil || capturedReq.Session == nil {
		t.Fatal("failed to capture a real *mcp.CallToolRequest with a non-nil Session")
	}

	// No manual RegisterPendingRequest call here: RegisterSession above is
	// solely responsible for seeding the mapping. This models the
	// boot-pulse scenario where the FIRST resolve call happens before the
	// ACP layer has had any chance to observe a tool call.
	start := time.Now()
	resolved := srv.resolveSelfIDWithMCP(targetID, capturedReq)
	elapsed := time.Since(start)

	if resolved != targetID {
		t.Errorf("mitto-8r1 regression: resolveSelfIDWithMCP(%q) = %q after %v (pendingRequestTimeout=%v); "+
			"a genuinely registered session (getSession != nil) failed to resolve because RegisterSession did "+
			"not seed a pending-request correlation entry at registration time — reproduces the boot-pulse "+
			"\"session not found: the self_id could not be resolved\" error surfaced by "+
			"handleUINotify/handleUIOptions/handleUITextbox/handleUIForm/handleGetCurrentSession. "+
			"See mitto-8r1 Investigation comment.",
			targetID, resolved, elapsed, pendingRequestTimeout)
	}

	const maxAcceptableLatency = 1 * time.Second
	if elapsed >= maxAcceptableLatency {
		t.Errorf("mitto-8r1 regression: resolveSelfIDWithMCP(%q) took %v (>= %v); expected near-instant "+
			"resolution via the pending-request entry RegisterSession seeds at registration time, not a slow "+
			"WaitForPendingRequest poll racing the agent's first tool call.",
			targetID, elapsed, maxAcceptableLatency)
	}
}

// TestRegisterSession_StickyCorrelationSurvivesExpiry_mittoBux is the
// reproduction/regression test for mitto-bux: the mitto-8r1 fix seeds a
// transient pending-request correlation entry at RegisterSession time, but
// that entry expires after pendingRequestExpiry (30s). A legacy
// (non-HTTP-MCP) agent whose first mitto_* tool call arrives LATER than that
// — a quiet supervisor loop, or a run_on_start boot pulse delayed under
// cold-start load — found the seeded entry already swept by
// cleanupExpiredPendingRequestsLocked, so resolution fell all the way back
// to Phase 2's real-time ACP-observed registration, which could itself lose
// the race under load (reproducing "session not found: the self_id could
// not be resolved" even though the session is genuinely registered).
//
// This test does not sleep pendingRequestExpiry (30s) in real time; it
// simulates the post-expiry state directly by deleting the transient entry
// via the same unexported field cleanupExpiredPendingRequestsLocked would
// have emptied, then asserts WaitForPendingRequest still resolves via the
// sticky, non-expiring mirror RegisterSession also seeds (registerStickyPendingRequest).
// Finally it asserts UnregisterSession clears the sticky entry too, so a
// genuinely-unregistered session cannot be resolved via the fallback.
func TestRegisterSession_StickyCorrelationSurvivesExpiry_mittoBux(t *testing.T) {
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

	// Simulate the transient entry's expiry (what cleanupExpiredPendingRequestsLocked
	// would have done 30s later) without a real 30s sleep.
	srv.pendingRequestsMu.Lock()
	delete(srv.pendingRequests, targetID)
	srv.pendingRequestsMu.Unlock()

	start := time.Now()
	resolved := srv.WaitForPendingRequest(targetID)
	elapsed := time.Since(start)

	if resolved != targetID {
		t.Errorf("mitto-bux regression: WaitForPendingRequest(%q) = %q after the transient entry expired; "+
			"expected the sticky fallback seeded by RegisterSession to still resolve it", targetID, resolved)
	}
	const maxAcceptableLatency = 1 * time.Second
	if elapsed >= maxAcceptableLatency {
		t.Errorf("mitto-bux regression: WaitForPendingRequest(%q) took %v (>= %v) after expiry; expected "+
			"near-instant resolution via the sticky fallback, not a slow poll to pendingRequestTimeout",
			targetID, elapsed, maxAcceptableLatency)
	}

	// UnregisterSession must clear the sticky entry too.
	srv.UnregisterSession(targetID)
	srv.stickyPendingRequestsMu.RLock()
	_, stillSticky := srv.stickyPendingRequests[targetID]
	srv.stickyPendingRequestsMu.RUnlock()
	if stillSticky {
		t.Errorf("mitto-bux: sticky pending-request entry for %q survived UnregisterSession", targetID)
	}
}

// TestResolveSelfIDWithMCP_StickyFallbackAfterExpiry_mittoBux is the
// end-to-end companion to TestRegisterSession_StickyCorrelationSurvivesExpiry_mittoBux.
// That test exercises WaitForPendingRequest directly; this one drives the
// mitto-bux acceptance criteria's actual entry point — resolveSelfIDWithMCP
// against a REAL *mcp.ServerSession from a genuine Streamable HTTP round-trip
// (the same pattern TestResolveSelfIDWithMCP_BootPulseRace_mitto_8r1 uses) —
// so Phase 1 (lookupMCPSession, cold on a session's first call) is exercised
// too, not just Phase 2's WaitForPendingRequest.
//
// It reproduces the exact "quiet spawn-only loop" scenario from the mitto-bux
// description: a legacy (non-HTTP-MCP) agent whose first mitto_* tool call
// arrives AFTER the mitto-8r1 transient entry's pendingRequestExpiry window
// has elapsed (simulated by deleting the transient entry post-registration,
// as above — no real 30s sleep). Pre-fix (sticky map absent or not
// consulted), this reproduces "session not found: the self_id could not be
// resolved" despite the session being genuinely registered. Post-fix it
// resolves near-instantly via the sticky mirror.
func TestResolveSelfIDWithMCP_StickyFallbackAfterExpiry_mittoBux(t *testing.T) {
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

	// Simulate the mitto-8r1 transient entry's expiry (what
	// cleanupExpiredPendingRequestsLocked would have done 30s later) BEFORE
	// the session's first tool call is ever observed — modeling a quiet
	// spawn-only loop pass that arrives late.
	srv.pendingRequestsMu.Lock()
	delete(srv.pendingRequests, targetID)
	srv.pendingRequestsMu.Unlock()

	// Probe tool: captures a real *mcp.CallToolRequest (and its req.Session)
	// for a fresh MCP protocol session, exactly as in the mitto-8r1 test —
	// this is the session's first-ever tool call, arriving after expiry.
	var capturedReq *mcp.CallToolRequest
	mcp.AddTool(srv.mcpServer, &mcp.Tool{Name: "test_capture_session_bux"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			capturedReq = req
			return nil, struct{}{}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv.mcpServer
	}, mcpStreamableHTTPOptions())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "mitto-bux-repro-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer clientSession.Close()

	if _, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "test_capture_session_bux"}); err != nil {
		t.Fatalf("capture probe call: %v", err)
	}
	if capturedReq == nil || capturedReq.Session == nil {
		t.Fatal("failed to capture a real *mcp.CallToolRequest with a non-nil Session")
	}

	start := time.Now()
	resolved := srv.resolveSelfIDWithMCP(targetID, capturedReq)
	elapsed := time.Since(start)

	if resolved != targetID {
		t.Errorf("mitto-bux regression: resolveSelfIDWithMCP(%q) = %q after the mitto-8r1 transient entry "+
			"expired; expected the sticky fallback seeded by RegisterSession to still resolve it on the "+
			"session's first-ever tool call, reproducing the acceptance criteria: a self_id-correlated tool "+
			"call succeeds on the first attempt even after a cold-start restart", targetID, resolved)
	}
	const maxAcceptableLatency = 1 * time.Second
	if elapsed >= maxAcceptableLatency {
		t.Errorf("mitto-bux regression: resolveSelfIDWithMCP(%q) took %v (>= %v) after expiry; expected "+
			"near-instant resolution via the sticky fallback, not a slow Phase 2 poll to pendingRequestTimeout",
			targetID, elapsed, maxAcceptableLatency)
	}
}
