// tools_wait_client_timeout_test.go: regression test for mitto-m2lk — a
// client-side HTTP transport timeout on a blocking mitto_conversation_wait
// tools/call POST, indistinguishable from the client's perspective from the
// "fetch failed" error reported by undici-based agents (auggie) in
// production.
//
// ROOT CAUSE (see the mitto-m2lk Investigation comment for the full
// analysis): mcpStreamableHTTPOptions() sets JSONResponse:true (mitto-6hr),
// so a blocking tool handler's response resolves inline on the POST — the
// POST connection carries zero bytes, not even headers, until the handler
// returns.
//
// CORRECTED DIAGNOSIS (this phase): the reproduce phase's original test
// assumed the progress heartbeat (startProgressHeartbeat) could be made to
// keep such a POST alive. That is actually impossible without forking the
// vendored go-sdk: streamableServerConn.Write (mcp/streamable.go) forcibly
// reroutes any non-response message — including progress notifications — to
// the standalone SSE stream whenever jsonResponse is set, and Go's net/http
// does not send response headers until the handler's first Write, which in
// JSON-response mode is the final, complete JSON body. No heartbeat cadence
// can change that. So the fix (see defaultMaxSingleWaitBlock,
// Server.maxSingleWaitBlock, tools_wait.go) instead caps how long a single
// physical HTTP call is allowed to block: handleConversationWait and
// handleBeadsIssuesReachedState now always return within that cap —
// (TimedOut:true, not an error) — regardless of timeout_seconds or the
// mode's own (600s / 4h) default, so the POST always completes comfortably
// inside any realistic client transport budget. A caller that still wants to
// keep waiting just calls the tool again, mirroring the resumable pattern
// already documented for mitto_children_tasks_wait.
//
// This test exercises the REAL mitto_conversation_wait tool (registered via
// NewServer, exactly as in production) over Mitto's REAL Streamable HTTP
// configuration (mcpStreamableHTTPOptions(), JSONResponse:true), against a
// target session that never stops "prompting" (so agent_responded's
// condition is never met) with a caller-requested timeout far longer than a
// short client HTTP timeout. Server.maxSingleWaitBlock is overridden to a
// small value purely so the test runs fast; production uses
// defaultMaxSingleWaitBlock (4 minutes).
package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

func TestConversationWait_ClientSideTimeoutOnBlockingToolCall(t *testing.T) {
	targetID := session.GenerateSessionID()
	// Target never finishes responding, so WaitForResponseComplete always
	// runs out its full budget — the scenario that used to block for the
	// caller's entire (possibly hours-long) requested timeout.
	mockBS := newMockBackgroundSessionForWait(true)
	srv, callerID := setupServerForWait(t, targetID, mockBS)

	// singleCallCap stands in for defaultMaxSingleWaitBlock (4 min in
	// production) — shrunk here so the test completes in well under a
	// second. clientTimeout sits strictly between the cap and the caller's
	// requested timeout: long enough that the capped call always beats it,
	// short enough that the old (uncapped) behavior would have tripped it.
	const singleCallCap = 60 * time.Millisecond
	const clientTimeout = 500 * time.Millisecond
	const requestedTimeoutSeconds = 3600 // 1h — the caller's real ask
	srv.maxSingleWaitBlock = singleCallCap

	// Real production Streamable HTTP configuration (server.go startSSE),
	// including JSONResponse:true (mitto-6hr), serving the REAL mcpServer
	// with mitto_conversation_wait registered exactly as NewServer wires it.
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv.mcpServer
	}, mcpStreamableHTTPOptions())

	ts := httptest.NewServer(handler)
	defer ts.Close()

	transport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL,
		// A fixed-budget HTTP client, standing in for undici's headersTimeout
		// on the production agent side. Unlike a context deadline, this fires
		// regardless of any data received on other connections.
		HTTPClient:           &http.Client{Timeout: clientTimeout},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mitto-m2lk-repro-client", Version: "1.0.0"}, nil)

	ctx := context.Background()
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client Connect (initialize) failed: %v", err)
	}
	defer sess.Close()

	start := time.Now()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "mitto_conversation_wait",
		Arguments: map[string]any{
			"self_id":         callerID,
			"conversation_id": targetID,
			"what":            "agent_responded",
			"timeout_seconds": requestedTimeoutSeconds,
		},
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("mitto-m2lk regression: tools/call failed after %v (client timeout=%v, single-call cap=%v, requested timeout=%ds): %v\n"+
			"The internal single-call cap should make this call return cleanly well within the client's "+
			"transport timeout even though the caller asked to wait far longer. See mitto-m2lk Investigation "+
			"comment and defaultMaxSingleWaitBlock (tools_wait.go).",
			elapsed, clientTimeout, singleCallCap, requestedTimeoutSeconds, err)
	}

	// The call must return promptly (bounded by the cap, not the client
	// timeout or the caller's requested hour) — otherwise this test would
	// pass vacuously merely because clientTimeout happened to be generous.
	if elapsed >= clientTimeout {
		t.Errorf("tools/call took %v, expected well under the client timeout (%v) thanks to the %v internal cap",
			elapsed, clientTimeout, singleCallCap)
	}

	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	var out ConversationWaitOutput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal ConversationWaitOutput: %v", err)
	}

	// The fix must not silently fabricate success: a call that returned
	// early purely because of the internal cap must say so via TimedOut,
	// not Success-without-TimedOut (which would mean the agent falsely
	// appears to have responded).
	if !out.TimedOut {
		t.Errorf("expected timed_out=true (capped early return), got %+v", out)
	}
	if !out.StillPrompting {
		t.Errorf("expected still_prompting=true (target never stops prompting), got %+v", out)
	}
}
