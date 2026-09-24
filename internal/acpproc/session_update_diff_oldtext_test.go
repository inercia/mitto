package acpproc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/conversation"
)

// TestSessionUpdate_ToolCallDiffOldTextObject_mitto5jnw reproduces mitto-5jnw:
// a `session/update` notification whose `tool_call` diff content carries
// `oldText` as a JSON object (instead of the SDK's strictly-typed
// `ToolCallContentDiff.OldText *string`) is rejected wholesale by the
// acp-go-sdk connection's `handleInbound` with a `-32602 Invalid params`
// decode error, logged as "failed to handle notification", and the
// registered session callback (MultiplexClient -> BackgroundSession) never
// observes the tool_call at all.
//
// This test drives a real acp.Connection (the same wiring
// SharedACPProcess.startProcessLocked uses: MultiplexClient +
// JSONLineFilterReader wrapping a raw pipe standing in for the agent's
// stdout) with a hand-crafted raw JSON line, since the malformed shape
// cannot be produced by marshaling the SDK's own (string-typed) structs.
//
// Expected/fixed behavior: the notification is not dropped and the
// registered OnSessionUpdate callback observes the tool_call diff exactly
// once, with no "failed to handle notification" error logged. Today this
// fails: the callback is never invoked and the error is logged instead.
func TestSessionUpdate_ToolCallDiffOldTextObject_mitto5jnw(t *testing.T) {
	const sessionID acp.SessionId = "test-session"

	agentToClientR, agentToClientW := io.Pipe()
	clientToAgentR, clientToAgentW := io.Pipe()
	t.Cleanup(func() {
		_ = agentToClientR.Close()
		_ = agentToClientW.Close()
		_ = clientToAgentR.Close()
		_ = clientToAgentW.Close()
	})

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	mux := NewMultiplexClient()
	// Wrap the agent's stdout with JSONLineFilterReader, exactly as
	// SharedACPProcess.startProcessLocked does in production — this is
	// where mitto-5jnw's fix (sanitizeToolCallDiffOldText) intercepts the
	// malformed payload before it ever reaches the SDK's decoder.
	filteredStdout := mittoAcp.NewJSONLineFilterReader(agentToClientR, logger)
	conn := acp.NewClientSideConnection(mux, clientToAgentW, filteredStdout)
	conn.SetLogger(logger)

	var mu sync.Mutex
	var gotUpdate acp.SessionNotification
	var received bool
	mux.RegisterSession(sessionID, &conversation.SessionCallbacks{
		OnSessionUpdate: func(_ context.Context, n acp.SessionNotification) error {
			mu.Lock()
			gotUpdate = n
			received = true
			mu.Unlock()
			return nil
		},
	})

	// Raw session/update notification whose diff content's `oldText` is a
	// JSON object rather than a string. This mirrors the real-world payload
	// observed from an Auggie-routed Claude-on-Vertex tool call (bead
	// mitto-5jnw investigation notes): toolCallId prefix "toolu_vrtx_...".
	rawNotification := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call","toolCallId":"toolu_vrtx_test","title":"Edit file","status":"in_progress","content":[{"type":"diff","path":"/tmp/foo.go","newText":"new contents","oldText":{"text":"old contents","language":"go"}}]}}}` + "\n"

	if _, err := agentToClientW.Write([]byte(rawNotification)); err != nil {
		t.Fatalf("write notification: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := received
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if !received {
		t.Fatalf("session/update with object-shaped oldText was dropped instead of "+
			"reaching the session callback (mitto-5jnw); connection log:\n%s", logBuf.String())
	}
	if gotUpdate.Update.ToolCall == nil {
		t.Fatalf("expected a ToolCall session update, got: %+v", gotUpdate.Update)
	}
	if len(gotUpdate.Update.ToolCall.Content) != 1 || gotUpdate.Update.ToolCall.Content[0].Diff == nil {
		t.Fatalf("expected exactly one diff content block, got: %+v", gotUpdate.Update.ToolCall.Content)
	}
	if strings.Contains(logBuf.String(), "failed to handle notification") {
		t.Fatalf("unexpected notification-handling error logged: %s", logBuf.String())
	}
}
