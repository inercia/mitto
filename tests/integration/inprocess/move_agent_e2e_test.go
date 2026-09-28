//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// setupTwoAgentServer creates a test server with TWO ACP server entries
// ("mock-a" and "mock-b", both pointing at the same mock ACP binary) and a
// workspace for each, both bound to the SAME working directory — the shape
// MoveSessionToAgent needs to consider "mock-b" a valid move target for a
// conversation created against "mock-a" (moveAgentPreflight requires a
// workspace matching (meta.WorkingDir, targetAgent), see
// SessionManager.GetWorkspaceByDirAndACP). It also wires
// MOCK_RPC_ORDER_FILE so tests can inspect the literal prompt text
// physically delivered to whichever mock ACP process handled it
// (tests/mocks/acp-server's recordRPCOrder), which is how the carried-over
// history + handoff preamble (mitto-f7yo.5) and the refreshed ACP session
// (mitto-f7yo.1) are verified end-to-end without any new mock-server
// capability — see readRPCOrder/promptLineFor (prompt_test.go,
// deferred_config_test.go), reused here.
func setupTwoAgentServer(t *testing.T) (*TestServer, string) {
	t.Helper()
	orderFile := filepath.Join(t.TempDir(), "rpc-order.log")
	t.Setenv("MOCK_RPC_ORDER_FILE", orderFile)

	ts := SetupTestServer(t, func(c *web.Config) {
		dir := c.DefaultWorkingDir
		cmd := c.ACPCommand
		c.MittoConfig.ACPServers = []config.ACPServer{
			{Name: "mock-a", Command: cmd},
			{Name: "mock-b", Command: cmd},
		}
		c.Workspaces = []config.WorkspaceSettings{
			{ACPServer: "mock-a", WorkingDir: dir, IsDefault: true},
			{ACPServer: "mock-b", WorkingDir: dir},
		}
		c.ACPServer = "mock-a"
	})
	return ts, orderFile
}

// connectAndWaitComplete connects a WebSocket observer for sessionID, sends
// message, and blocks until the turn completes (OnPromptComplete) or
// timeout. Mirrors the Connect/LoadEvents/SendPrompt/waitFor pattern used
// throughout prompt_test.go.
func connectAndWaitComplete(t *testing.T, ts *TestServer, sessionID, message string) {
	t.Helper()
	var (
		mu       sync.Mutex
		complete bool
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sessionID, api.SessionCallbacks{
		OnPromptComplete: func(_ int) { mu.Lock(); complete = true; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	if err := ws.SendPrompt(message); err != nil {
		t.Fatalf("SendPrompt(%q): %v", message, err)
	}
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return complete
	}, "prompt complete for "+sessionID)
}

// promptDeliveries reads the raw RPC-order file and reconstructs the full,
// possibly multi-line, text of every "prompt" RPC recorded by the mock ACP
// server. readRPCOrder/promptLineFor (deferred_config_test.go/prompt_test.go)
// split the file on "\n" and match whole lines starting with "prompt\t" —
// correct for the single-line prompts those tests send, but the
// mitto-f7yo.5 handoff preamble + injected history this test's second prompt
// carries contains embedded newlines, so the literal detail written by
// recordRPCOrder ("prompt\t<message>\n") spans several physical lines and a
// naive line split fatally truncates it at the first "\n". recordRPCOrder
// always records a "prompt" RPC immediately followed by a "prompt_model" RPC
// (handlePrompt calls both back to back, see tests/mocks/acp-server/handler.go),
// so that pairing is used as an unambiguous entry boundary instead.
func promptDeliveries(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read rpc order file: %v", err)
	}
	raw := string(data)
	var starts []int
	if strings.HasPrefix(raw, "prompt\t") {
		starts = append(starts, 0)
	}
	for i := 0; i < len(raw); {
		idx := strings.Index(raw[i:], "\nprompt\t")
		if idx == -1 {
			break
		}
		starts = append(starts, i+idx+1)
		i = i + idx + 1
	}
	var entries []string
	for _, s := range starts {
		rest := raw[s+len("prompt\t"):]
		if end := strings.Index(rest, "\nprompt_model\t"); end != -1 {
			entries = append(entries, rest[:end])
		} else {
			entries = append(entries, strings.TrimRight(rest, "\n"))
		}
	}
	return entries
}

// promptDeliveryContaining returns the first entry from promptDeliveries
// containing needle, or "" if none matches.
func promptDeliveryContaining(entries []string, needle string) string {
	for _, e := range entries {
		if strings.Contains(e, needle) {
			return e
		}
	}
	return ""
}

// sessionChangeAgentEvents returns every "session_change" (kind "agent")
// event recorded for sessionID, in order.
func sessionChangeAgentEvents(t *testing.T, ts *TestServer, sessionID string) []session.SessionChangeData {
	t.Helper()
	events, err := ts.Store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents(%s): %v", sessionID, err)
	}
	var out []session.SessionChangeData
	for _, ev := range events {
		if ev.Type != session.EventTypeSessionChange {
			continue
		}
		data, err := session.DecodeEventData(ev)
		if err != nil {
			continue
		}
		scd, ok := data.(session.SessionChangeData)
		if !ok || scd.Kind != "agent" {
			continue
		}
		out = append(out, scd)
	}
	return out
}

// TestMoveAgent_E2E drives the full mitto-f7yo agent-migration flow against
// two mock ACP processes ("mock-a", "mock-b") in one workspace folder:
//
//  1. Create a conversation on mock-a, send a prompt, wait for completion.
//  2. Preflight (mitto-f7yo.2) reports mock-b as an available candidate.
//  3. POST .../move-agent while a turn is streaming is rejected 409/busy
//     (mitto-f7yo.1's moveAgentPreflight busy check).
//  4. POST .../move-agent to mock-b succeeds once idle: metadata.acp_server
//     becomes mock-b, acp_session_id is refreshed (differs from the
//     mock-a-era value), and a session_change(kind=agent) event records
//     value=mock-b/previous_value=mock-a (mitto-f7yo.1).
//  5. The first prompt physically delivered to mock-b carries BOTH the
//     mitto-f7yo.5 handoff preamble (naming mock-a) AND the earlier turn's
//     text as injected history — verified via the mock ACP's RPC-order log,
//     the only place the literal wire text is observable.
func TestMoveAgent_E2E(t *testing.T) {
	ts, orderFile := setupTwoAgentServer(t)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{ACPServer: "mock-a"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	if sess.ACPServer != "mock-a" {
		t.Fatalf("new session ACPServer = %q, want mock-a", sess.ACPServer)
	}

	// --- Step 1: first turn on mock-a --------------------------------------
	const firstMarker = "FIRST-ON-A-MARKER-9f3c"
	connectAndWaitComplete(t, ts, sess.SessionID, "please respond to "+firstMarker)

	metaBeforeMove, err := ts.Store.GetMetadata(sess.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata before move: %v", err)
	}
	if metaBeforeMove.ACPServer != "mock-a" {
		t.Fatalf("metadata acp_server before move = %q, want mock-a", metaBeforeMove.ACPServer)
	}
	acpSessionIDBeforeMove := metaBeforeMove.ACPSessionID
	if acpSessionIDBeforeMove == "" {
		t.Fatal("expected a non-empty acp_session_id after the first prompt on mock-a")
	}

	// --- Step 2: preflight reports mock-b as an available candidate -------
	preflight, err := ts.Client.MoveAgentPreflight(sess.SessionID)
	if err != nil {
		t.Fatalf("MoveAgentPreflight: %v", err)
	}
	if preflight.CurrentAgent != "mock-a" {
		t.Errorf("preflight current_agent = %q, want mock-a", preflight.CurrentAgent)
	}
	if preflight.Busy {
		t.Errorf("preflight busy = true while idle")
	}
	foundB := false
	for _, cand := range preflight.Candidates {
		if cand.Name == "mock-b" {
			foundB = true
			if !cand.Available {
				t.Errorf("candidate mock-b.available = false, want true")
			}
		}
	}
	if !foundB {
		t.Fatalf("preflight candidates %+v do not include mock-b", preflight.Candidates)
	}

	// --- Step 3: move-agent while streaming is rejected (busy/409) --------
	var (
		mu        sync.Mutex
		slowStart bool
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	slowWS, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnPromptReceived: func(_ string) { mu.Lock(); slowStart = true; mu.Unlock() },
	})
	if err != nil {
		cancel()
		t.Fatalf("Connect (slow): %v", err)
	}
	if err := slowWS.LoadEvents(50, 0, 0); err != nil {
		cancel()
		t.Fatalf("LoadEvents (slow): %v", err)
	}
	// Matches tests/fixtures/responses/slow-response.json's trigger pattern
	// "(?i)slow response": an 8-chunk, 500ms-per-chunk streamed reply, ample
	// window to observe BackgroundSession.IsPrompting()==true deterministically.
	if err := slowWS.SendPrompt("Simulate a slow response please"); err != nil {
		cancel()
		t.Fatalf("SendPrompt (slow): %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
		return bs != nil && bs.IsPrompting()
	}, "session to start streaming the slow response")

	busyPreflight, err := ts.Client.MoveAgentPreflight(sess.SessionID)
	if err != nil {
		t.Fatalf("MoveAgentPreflight while busy: %v", err)
	}
	if !busyPreflight.Busy || busyPreflight.BusyReason != "turn_streaming" {
		t.Errorf("preflight while streaming = {busy:%v reason:%q}, want {busy:true reason:turn_streaming}",
			busyPreflight.Busy, busyPreflight.BusyReason)
	}

	if _, err := ts.Client.MoveAgent(sess.SessionID, "mock-b", false); err == nil {
		t.Error("MoveAgent while a turn is streaming: expected an error, got nil")
	} else if apiErr, ok := err.(*api.APIError); !ok || apiErr.Status != http.StatusConflict {
		t.Errorf("MoveAgent while streaming: got %v, want *api.APIError{Status: 409}", err)
	}

	waitFor(t, 15*time.Second, func() bool {
		bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
		return bs != nil && !bs.IsPrompting()
	}, "slow response to finish streaming")
	slowWS.Close()
	cancel()
	_ = slowStart // observed via waitFor above; kept for readability at the call site

	// --- Step 4: move-agent succeeds once idle -----------------------------
	result, err := ts.Client.MoveAgent(sess.SessionID, "mock-b", false)
	if err != nil {
		t.Fatalf("MoveAgent: %v", err)
	}
	if result.PreviousAgent != "mock-a" {
		t.Errorf("MoveAgentResult.PreviousAgent = %q, want mock-a", result.PreviousAgent)
	}
	if len(result.Moved) != 1 || result.Moved[0] != sess.SessionID {
		t.Errorf("MoveAgentResult.Moved = %v, want [%s]", result.Moved, sess.SessionID)
	}
	if result.ResumeError != "" {
		t.Errorf("MoveAgentResult.ResumeError = %q, want empty", result.ResumeError)
	}

	var metaAfterMove session.Metadata
	waitFor(t, 15*time.Second, func() bool {
		m, err := ts.Store.GetMetadata(sess.SessionID)
		if err != nil {
			return false
		}
		metaAfterMove = m
		return m.ACPServer == "mock-b" && m.ACPSessionID != "" && m.ACPSessionID != acpSessionIDBeforeMove
	}, "metadata to reflect the move to mock-b with a refreshed acp_session_id")

	if metaAfterMove.ACPServer != "mock-b" {
		t.Fatalf("metadata acp_server after move = %q, want mock-b", metaAfterMove.ACPServer)
	}
	if metaAfterMove.ACPSessionID == acpSessionIDBeforeMove {
		t.Fatalf("acp_session_id after move (%q) did not change from before the move", metaAfterMove.ACPSessionID)
	}

	changeEvents := sessionChangeAgentEvents(t, ts, sess.SessionID)
	if len(changeEvents) == 0 {
		t.Fatal("expected at least one session_change(kind=agent) event after the move")
	}
	last := changeEvents[len(changeEvents)-1]
	if last.Value != "mock-b" || last.PreviousValue != "mock-a" {
		t.Errorf("session_change(kind=agent) = {value:%q previous_value:%q}, want {value:mock-b previous_value:mock-a}",
			last.Value, last.PreviousValue)
	}

	// --- Step 5: first prompt on mock-b carries history + handoff preamble -
	const secondMarker = "SECOND-ON-B-MARKER-7a1d"
	connectAndWaitComplete(t, ts, sess.SessionID, "please respond to "+secondMarker)

	deliveries := promptDeliveries(t, orderFile)
	sentToB := promptDeliveryContaining(deliveries, secondMarker)
	if sentToB == "" {
		t.Fatalf("no prompt delivery found containing %q; deliveries:\n%s", secondMarker, strings.Join(deliveries, "\n---\n"))
	}
	if !strings.Contains(sentToB, `previously handled by agent "mock-a"`) {
		t.Errorf("first prompt on mock-b missing the mitto-f7yo.5 handoff preamble naming mock-a; got:\n%s", sentToB)
	}
	if !strings.Contains(sentToB, firstMarker) {
		t.Errorf("first prompt on mock-b missing the carried-over history from the mock-a turn (marker %q); got:\n%s",
			firstMarker, sentToB)
	}
	if !strings.Contains(sentToB, secondMarker) {
		t.Errorf("first prompt on mock-b missing its own new message (marker %q); got:\n%s", secondMarker, sentToB)
	}
}

// TestMoveAgent_IncludeChildren_E2E verifies that MoveAgentOptions.IncludeChildren
// (mitto-f7yo.1) rebinds a non-archived, non-busy child conversation bound to
// the same previous agent onto the new agent alongside its parent, recording
// the same session_change(kind=agent) event for the child.
func TestMoveAgent_IncludeChildren_E2E(t *testing.T) {
	ts, _ := setupTwoAgentServer(t)

	parent, err := ts.Client.CreateSession(api.CreateSessionRequest{ACPServer: "mock-a", Name: "parent"})
	if err != nil {
		t.Fatalf("CreateSession(parent): %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(parent.SessionID) })

	child, err := ts.Client.CreateSession(api.CreateSessionRequest{ACPServer: "mock-a", Name: "child"})
	if err != nil {
		t.Fatalf("CreateSession(child): %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(child.SessionID) })

	// Establish the parent/child relationship directly in the store — this
	// bead's scope is MoveSessionToAgent's own IncludeChildren traversal
	// (store.FindAllChildrenRecursive), not conversation auto-parenting.
	if err := ts.Store.UpdateMetadata(child.SessionID, func(m *session.Metadata) {
		m.ParentSessionID = parent.SessionID
	}); err != nil {
		t.Fatalf("UpdateMetadata(child, set ParentSessionID): %v", err)
	}

	result, err := ts.Client.MoveAgent(parent.SessionID, "mock-b", true)
	if err != nil {
		t.Fatalf("MoveAgent(includeChildren=true): %v", err)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("MoveAgentResult.Skipped = %+v, want none", result.Skipped)
	}

	moved := map[string]bool{}
	for _, id := range result.Moved {
		moved[id] = true
	}
	if !moved[parent.SessionID] || !moved[child.SessionID] {
		t.Fatalf("MoveAgentResult.Moved = %v, want both %s and %s", result.Moved, parent.SessionID, child.SessionID)
	}

	var childMeta session.Metadata
	waitFor(t, 15*time.Second, func() bool {
		m, err := ts.Store.GetMetadata(child.SessionID)
		if err != nil {
			return false
		}
		childMeta = m
		return m.ACPServer == "mock-b"
	}, "child metadata to reflect the move to mock-b")

	if childMeta.ACPServer != "mock-b" {
		t.Fatalf("child metadata acp_server = %q, want mock-b", childMeta.ACPServer)
	}

	childChangeEvents := sessionChangeAgentEvents(t, ts, child.SessionID)
	if len(childChangeEvents) == 0 {
		t.Fatal("expected a session_change(kind=agent) event for the moved child")
	}
	lastChild := childChangeEvents[len(childChangeEvents)-1]
	if lastChild.Value != "mock-b" || lastChild.PreviousValue != "mock-a" {
		t.Errorf("child session_change(kind=agent) = {value:%q previous_value:%q}, want {value:mock-b previous_value:mock-a}",
			lastChild.Value, lastChild.PreviousValue)
	}
}

// TestMoveAgent_LoopUnchangedAndFiresOnNewAgent_E2E verifies that moving a
// loop conversation to a different agent leaves its loop.json configuration
// untouched (mitto-f7yo.7 acceptance criterion) and that a subsequent
// RunLoopNow correctly delivers on the NEW agent (mock-b).
func TestMoveAgent_LoopUnchangedAndFiresOnNewAgent_E2E(t *testing.T) {
	ts, orderFile := setupTwoAgentServer(t)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{ACPServer: "mock-a", Name: "loop-move"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	const loopMarker = "LOOP-PING-MARKER-3e2b"
	// Far-future daily schedule: only an explicit RunLoopNow should ever
	// deliver a run during this test's lifetime (mirrors the pattern used by
	// loop_runonstart_e2e_test.go/loop_oncompletion_e2e_test.go).
	cfgBefore, err := ts.Client.SetLoop(sess.SessionID, api.SetLoopRequest{
		Prompt:    loopMarker,
		Triggers:  []string{"schedule"},
		Frequency: api.LoopFrequency{Value: 1, Unit: "days", At: "09:00"},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("SetLoop: %v", err)
	}

	if _, err := ts.Client.MoveAgent(sess.SessionID, "mock-b", false); err != nil {
		t.Fatalf("MoveAgent: %v", err)
	}
	waitFor(t, 15*time.Second, func() bool {
		m, err := ts.Store.GetMetadata(sess.SessionID)
		return err == nil && m.ACPServer == "mock-b"
	}, "metadata to reflect the move to mock-b")

	cfgAfter, err := ts.Client.GetLoop(sess.SessionID)
	if err != nil {
		t.Fatalf("GetLoop after move: %v", err)
	}
	if cfgAfter.Prompt != cfgBefore.Prompt || cfgAfter.Trigger != cfgBefore.Trigger ||
		cfgAfter.Frequency != cfgBefore.Frequency || cfgAfter.Enabled != cfgBefore.Enabled {
		t.Fatalf("loop config changed by the move: before=%+v after=%+v", cfgBefore, cfgAfter)
	}

	// resetTimer=true: this loop's own manual RunLoopNow call must advance
	// IterationCount (RecordSent) so waitLoopIterationCount below can observe
	// the delivery — resetTimer=false ("manual run with keep schedule")
	// intentionally never calls RecordSent (see loop_runner.go's deliverPrompt
	// OnComplete) and would hang here forever.
	if err := ts.Client.RunLoopNow(sess.SessionID, true); err != nil {
		t.Fatalf("RunLoopNow: %v", err)
	}
	waitLoopIterationCount(t, ts, sess.SessionID, 1)
	waitLoopSessionIdle(t, ts, sess.SessionID)

	// The loop's first delivery after the move is also this conversation's
	// first prompt since the handoff, so it carries the same mitto-f7yo.5
	// preamble/history injection as any other post-move prompt — use the
	// multi-line-aware helper (see promptDeliveries) rather than the
	// single-line readRPCOrder/promptLineFor pair.
	deliveries := promptDeliveries(t, orderFile)
	sentToB := promptDeliveryContaining(deliveries, loopMarker)
	if sentToB == "" {
		t.Fatalf("no prompt delivery found containing loop marker %q after RunLoopNow; deliveries:\n%s",
			loopMarker, strings.Join(deliveries, "\n---\n"))
	}
}
