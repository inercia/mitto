package acpproc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/conversation"
)

// These fixtures are derived from the OpenCode v2.0.23 child-update wire
// contract in packages/cli/src/acp/child.ts; they are not captured live traces.

func TestDecodeOpenCodeChildUpdate_StatusesAndUpdates(t *testing.T) {
	for _, status := range []string{"created", "running", "completed", "failed", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			params := fmt.Sprintf(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":%q}`, status)
			got, err := decodeOpenCodeChildUpdate(json.RawMessage(params))
			if err != nil {
				t.Fatalf("decode status: %v", err)
			}
			if got.RootSessionID != "root" || got.ChildSessionID != "child" || got.ParentSessionID != "root" || got.Depth != 1 || got.Status != status {
				t.Fatalf("decoded event mismatch: %#v", got)
			}
		})
	}

	t.Run("optional error", func(t *testing.T) {
		got, err := decodeOpenCodeChildUpdate(json.RawMessage(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"failed","error":{"type":"failed","message":"worker failed"}}`))
		if err != nil || got.Error == nil || got.Error.Message != "worker failed" {
			t.Fatalf("decoded status error = %#v, %v", got.Error, err)
		}
	})

	for name, update := range map[string]string{
		"text": `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}`,
		"tool": `{"sessionUpdate":"tool_call","title":"Run","toolCallId":"child:tool-1","kind":"execute","status":"pending"}`,
	} {
		t.Run(name, func(t *testing.T) {
			params := fmt.Sprintf(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","title":"worker","update":%s}`, update)
			got, err := decodeOpenCodeChildUpdate(json.RawMessage(params))
			if err != nil || got.Update == nil || got.Type != "update" || got.Title == nil || *got.Title != "worker" {
				t.Fatalf("decoded update = %#v, %v", got, err)
			}
		})
	}

	t.Run("nested child", func(t *testing.T) {
		got, err := decodeOpenCodeChildUpdate(json.RawMessage(`{"rootSessionId":"root","childSessionId":"grandchild","parentSessionId":"child","depth":2,"type":"status","status":"created"}`))
		if err != nil || got.Depth != 2 || got.ParentSessionID != "child" {
			t.Fatalf("decoded nested event = %#v, %v", got, err)
		}
	})
}

func TestDecodeOpenCodeChildUpdate_RejectsMalformedAndContradictoryEvents(t *testing.T) {
	valid := `"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created"`
	cases := map[string]string{
		"not object":                    `[]`,
		"missing root":                  `{"childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created"}`,
		"empty id":                      `{"rootSessionId":"","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created"}`,
		"oversized id":                  fmt.Sprintf(`{"rootSessionId":%q,"childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created"}`, strings.Repeat("r", maxChildSessionIDBytes+1)),
		"child is root":                 `{"rootSessionId":"root","childSessionId":"root","parentSessionId":"root","depth":1,"type":"status","status":"created"}`,
		"child is parent":               `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"child","depth":1,"type":"status","status":"created"}`,
		"depth zero":                    `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":0,"type":"status","status":"created"}`,
		"depth too high":                `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":33,"type":"status","status":"created"}`,
		"fractional depth":              `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1.5,"type":"status","status":"created"}`,
		"string depth":                  `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":"1","type":"status","status":"created"}`,
		"depth one has non-root parent": `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"other","depth":1,"type":"status","status":"created"}`,
		"nested depth has root parent":  `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":2,"type":"status","status":"created"}`,
		"unknown event type":            `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"other","status":"created"}`,
		"unknown status":                `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"paused"}`,
		"status includes update":        `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created","update":{}}`,
		"null title":                    `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"title":null,"type":"status","status":"created"}`,
		"unknown update discriminator":  `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","update":{"sessionUpdate":"future_update"}}`,
		"null update":                   `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","update":null}`,
		"update includes status":        `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","status":"created","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}`,
		"metadata id mismatch":          `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"},"_meta":{"opencode/child-session":{"id":"other","parentID":"root","depth":1}}}}`,
		"metadata depth mismatch":       `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"},"_meta":{"opencode/child-session":{"id":"child","parentID":"root","depth":2}}}}`,
		"metadata title mismatch":       `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"title":"worker","type":"update","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"},"_meta":{"opencode/child-session":{"id":"child","parentID":"root","depth":1,"title":"other"}}}}`,
		"duplicate envelope key":        `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","type":"status","status":"created"}`,
		"invalid error shape":           `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"failed","error":{"type":7,"message":"secret-sentinel"}}`,
	}
	_ = valid // Keep the canonical envelope fragment visible alongside cases.
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeOpenCodeChildUpdate(json.RawMessage(params))
			if err == nil {
				t.Fatal("expected invalid child update to be rejected")
			}
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatalf("validation error leaked payload: %v", err)
			}
		})
	}
	if _, err := decodeOpenCodeChildUpdate(json.RawMessage("{" + valid + "}")); err != nil {
		t.Fatalf("canonical envelope rejected: %v", err)
	}
}

func TestAdaptOpenCodeChildUpdate_PreservesParamsAndHandlesMethodBoundary(t *testing.T) {
	params := ` { "rootSessionId":"root", "childSessionId":"child", "parentSessionId":"root", "depth":1, "type":"status", "status":"created", "opaqueNumber":9007199254740993, "nested":{"method":"opencode/session/child_update"} } `
	line := []byte(`{ "jsonrpc" : "2.0", "method" : "opencode\/session\/child_update", "params" :` + params + ` }`)
	want := bytes.Replace(line, []byte(`"opencode\/session\/child_update"`), []byte(`"_mitto/opencode/session/child_update"`), 1)
	got, discard := adaptOpenCodeChildUpdate(line)
	if discard || !bytes.Equal(got, want) {
		t.Fatalf("adapted notification mismatch\n got: %s\nwant: %s\ndiscard=%v", got, want, discard)
	}
	if !bytes.Contains(got, []byte(params)) {
		t.Fatalf("params bytes changed during aliasing: %s", got)
	}

	for _, id := range []string{`null`, `0`, `"request-1"`} {
		request := []byte(`{"jsonrpc":"2.0","method":"opencode/session/child_update","id":` + id + `,"params":` + params + `}`)
		adapted, discard := adaptOpenCodeChildUpdate(request)
		if discard || !bytes.Equal(adapted, request) {
			t.Errorf("request id %s was adapted or discarded: %s", id, adapted)
		}
	}

	for _, tc := range []struct {
		name    string
		line    string
		want    string
		discard bool
	}{
		{name: "raw alias notification", line: `{"jsonrpc":"2.0","method":"_mitto/opencode/session/child_update","params":{}}`, discard: true},
		{name: "raw alias request", line: `{"jsonrpc":"2.0","method":"_mitto/opencode/session/child_update","id":null,"params":{}}`, want: reservedChildUpdateMethod},
		{name: "unrelated extension", line: `{"jsonrpc":"2.0","method":"_other/child_update","params":{}}`, want: "_other/child_update"},
		{name: "malformed lifecycle notification", line: `{"jsonrpc":"2.0","method":"opencode/session/child_update","params":{"rootSessionId":"root"}}`, discard: true},
		{name: "ambiguous lifecycle notification", line: `{"jsonrpc":"2.0","method":"opencode/session/child_update","method":"opencode/session/child_update","params":{}}`, discard: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapted, discard := adaptOpenCodeChildUpdate([]byte(tc.line))
			if discard != tc.discard {
				t.Fatalf("discard = %v, want %v; output %s", discard, tc.discard, adapted)
			}
			if !discard {
				var envelope struct {
					Method string `json:"method"`
				}
				if err := json.Unmarshal(adapted, &envelope); err != nil || envelope.Method != tc.want {
					t.Fatalf("method = %q, err=%v; want %q", envelope.Method, err, tc.want)
				}
			}
		})
	}
}

func TestSharedACPChildUpdateTransform_LogsSanitizedRejections(t *testing.T) {
	var logs bytes.Buffer
	transform := sharedACPChildUpdateTransform(slog.New(slog.NewTextHandler(&logs, nil)))
	payloadSentinel := "payload-secret-sentinel"
	titleSentinel := "title-secret-sentinel"
	errorSentinel := "error-secret-sentinel"
	line := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"opencode/session/child_update","params":{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","title":%q,"error":{"type":"failed","message":%q},"update":{"sessionUpdate":"bad"},"private":%q}}`, titleSentinel, errorSentinel, payloadSentinel))
	if _, discard := transform(line); !discard {
		t.Fatal("malformed lifecycle notification was not discarded")
	}
	text := logs.String()
	if !strings.Contains(text, "Discarded OpenCode child lifecycle notification") || !strings.Contains(text, "invalid_lifecycle_envelope") {
		t.Fatalf("missing bounded rejection diagnostic: %s", text)
	}
	for _, secret := range []string{payloadSentinel, titleSentinel, errorSentinel} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, text)
		}
	}
}

func TestMultiplexClient_HandleChildUpdate(t *testing.T) {
	const params = `{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"running"}`
	mc := NewMultiplexClient()
	called := false
	mc.RegisterSession("root", &conversation.SessionCallbacks{
		OnChildSessionUpdate: func(_ context.Context, update conversation.ChildSessionUpdate) error {
			called = update.ChildSessionID == "child" && update.Status == "running"
			mc.UnregisterSession(update.RootSessionID) // proves callback executes outside mux lock
			return nil
		},
	})
	if _, err := mc.HandleExtensionMethod(context.Background(), mittoChildUpdateMethod, json.RawMessage(params)); err != nil {
		t.Fatalf("handle child update: %v", err)
	}
	if !called {
		t.Fatal("root callback did not receive typed child update")
	}
	if _, err := mc.HandleExtensionMethod(context.Background(), mittoChildUpdateMethod, json.RawMessage(params)); err != nil {
		t.Fatalf("unknown root should be ignored: %v", err)
	}
	if _, err := mc.HandleExtensionMethod(context.Background(), "_other/method", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown extension should return method-not-found")
	}
	_, err := mc.HandleExtensionMethod(context.Background(), mittoChildUpdateMethod, json.RawMessage(`{"message":"payload-sentinel"}`))
	if err == nil || strings.Contains(err.Error(), "payload-sentinel") {
		t.Fatalf("malformed payload error leaked input: %v", err)
	}
}

func TestSharedACPChildUpdates_AreOrderedWithRootUpdates(t *testing.T) {
	rootEntered := make(chan struct{}, 1)
	releaseRoot := make(chan struct{})
	events := make(chan string, 16)
	process := &SharedACPProcess{}
	var replayLogs bytes.Buffer
	process.logger = slog.New(slog.NewTextHandler(&replayLogs, nil))
	mux := NewMultiplexClient()
	mux.RegisterSession("root", &conversation.SessionCallbacks{
		OnSessionUpdate: func(_ context.Context, params acp.SessionNotification) error {
			seq := int(params.Meta["seq"].(float64))
			if seq == 1 {
				rootEntered <- struct{}{}
				<-releaseRoot
			}
			events <- fmt.Sprintf("root-%d", seq)
			return nil
		},
		OnChildSessionUpdate: func(_ context.Context, update conversation.ChildSessionUpdate) error {
			if update.Type == "update" {
				if update.Update == nil {
					return fmt.Errorf("child update missing decoded payload")
				}
				payload, err := json.Marshal(update.Update)
				if err != nil || !bytes.Contains(payload, []byte("projected child text")) {
					return fmt.Errorf("child update payload was not preserved: %v", err)
				}
				events <- "child-update"
				return nil
			}
			events <- "child-" + update.Status
			return nil
		},
	})

	agentToClientR, agentToClientW := io.Pipe()
	clientToAgentR, clientToAgentW := io.Pipe()
	conn := acp.NewClientSideConnection(mux, clientToAgentW,
		mittoAcp.NewJSONLineFilterReaderWithTransform(agentToClientR, nil, process.filterLoadReplayNotification, sharedACPChildUpdateTransform(nil)),
		acp.WithMaxQueuedNotifications(sharedACPNotificationQueueCapacity))
	t.Cleanup(func() {
		_ = agentToClientR.Close()
		_ = agentToClientW.Close()
		_ = clientToAgentR.Close()
		_ = clientToAgentW.Close()
	})
	responses := make(chan map[string]json.RawMessage, 8)
	go func() {
		scanner := bufio.NewScanner(clientToAgentR)
		for scanner.Scan() {
			var response map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &response) == nil {
				responses <- response
			}
		}
	}()

	write := func(line string) {
		t.Helper()
		if _, err := io.WriteString(agentToClientW, line+"\n"); err != nil {
			t.Fatalf("write notification: %v", err)
		}
	}
	seenResponses := make(map[string]map[string]json.RawMessage)
	waitResponse := func(id string) map[string]json.RawMessage {
		t.Helper()
		if response := seenResponses[id]; response != nil {
			return response
		}
		for {
			select {
			case response := <-responses:
				var responseID string
				_ = json.Unmarshal(response["id"], &responseID)
				seenResponses[responseID] = response
				if responseID == id {
					return response
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for response %q; observed responses %v", id, seenResponses)
				return nil
			}
		}
	}
	process.beginLoadReplaySuppression("root")
	write(childNotificationLine(`{"rootSessionId":"root","childSessionId":"replayed","parentSessionId":"root","depth":1,"type":"status","status":"created"}`))
	write(childNotificationLine(`{"rootSessionId":"root","childSessionId":"replayed","parentSessionId":"root","depth":1,"type":"update","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"replayed text"}}}`))
	write(`{"jsonrpc":"2.0","method":"_other/replay_barrier","id":"replay-barrier","params":{}}`)
	if response := waitResponse("replay-barrier"); len(response["error"]) == 0 {
		t.Fatalf("replay barrier request unexpectedly succeeded: %s", response)
	}
	process.loadReplayMu.RLock()
	state := process.loadReplaySuppressions["root"]
	gotSuppressed := state.suppressed.Load()
	process.loadReplayMu.RUnlock()
	if gotSuppressed != 2 {
		t.Fatalf("reader did not suppress both status/update replay before queueing: count=%d", gotSuppressed)
	}
	process.endLoadReplaySuppression("root")
	if !strings.Contains(replayLogs.String(), `suppressed_notifications=2`) {
		t.Fatalf("replay suppression summary missing expected count: %s", replayLogs.String())
	}

	write(rootSessionUpdateLine(1))
	select {
	case <-rootEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first root callback did not start")
	}
	write(`{"jsonrpc":"2.0","method":"opencode/session/child_update","params":{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"unknown"}}`)
	write(`{"jsonrpc":"2.0","method":"_mitto/opencode/session/child_update","params":{"rootSessionId":"root","childSessionId":"private-child","parentSessionId":"root","depth":1,"type":"status","status":"created"}}`)
	write(childNotificationLine(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created"}`))
	write(childNotificationLine(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"running"}`))
	write(childNotificationLine(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"projected child text"},"_meta":{"opencode/child-session":{"id":"child","parentID":"root","depth":1}}}}`))
	write(childNotificationLine(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"completed"}`))
	write(childNotificationLine(`{"rootSessionId":"unknown-root","childSessionId":"other-child","parentSessionId":"unknown-root","depth":1,"type":"status","status":"running"}`))
	write(rootSessionUpdateLine(2))
	close(releaseRoot)

	var got []string
	want := []string{"root-1", "child-created", "child-running", "child-update", "child-completed", "root-2"}
	for len(got) < len(want) {
		select {
		case event := <-events:
			got = append(got, event)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for ordered callbacks; received %v", got)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("callback order = %v, want %v", got, want)
	}
	// Both the original request and a request attempting the private alias must
	// receive method-not-found responses without invoking the lifecycle callback.
	write(`{"jsonrpc":"2.0","method":"opencode/session/child_update","id":"wire-request","params":{}}`)
	write(`{"jsonrpc":"2.0","method":"_mitto/opencode/session/child_update","id":"private-request","params":{}}`)
	for _, id := range []string{"wire-request", "private-request"} {
		response := waitResponse(id)
		if len(response["error"]) == 0 {
			t.Errorf("request %q did not receive method-not-found error: %s", id, response["error"])
		}
	}
	select {
	case event := <-events:
		t.Fatalf("request caused unexpected lifecycle callback %q", event)
	default:
	}
	select {
	case <-conn.Done():
		t.Fatal("connection closed while receiving child notifications")
	default:
	}
}

func rootSessionUpdateLine(seq int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"root","_meta":{"seq":%d},"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"root"}}}}`, seq)
}

func childNotificationLine(params string) string {
	return `{"jsonrpc":"2.0","method":"opencode/session/child_update","params":` + params + `}`
}

func TestLoadReplayFilter_SuppressesOnlyChildNotificationsForLoadingRoot(t *testing.T) {
	p := &SharedACPProcess{}
	p.beginLoadReplaySuppression("root")
	defer p.endLoadReplaySuppression("root")

	cases := []struct {
		name string
		line string
		want bool
	}{
		{name: "status notification", line: childNotificationLine(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created"}`), want: true},
		{name: "update notification", line: childNotificationLine(`{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"update","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}`), want: true},
		{name: "other root", line: childNotificationLine(`{"rootSessionId":"other","childSessionId":"child","parentSessionId":"other","depth":1,"type":"status","status":"created"}`), want: false},
		{name: "request with null id", line: `{"jsonrpc":"2.0","method":"opencode/session/child_update","id":null,"params":{"rootSessionId":"root"}}`, want: false},
		{name: "unrelated extension", line: `{"jsonrpc":"2.0","method":"_other/child_update","params":{"rootSessionId":"root"}}`, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.filterLoadReplayNotification([]byte(tc.line)); got != tc.want {
				t.Fatalf("filter result = %v, want %v", got, tc.want)
			}
		})
	}
	p.loadReplayMu.RLock()
	got := p.loadReplaySuppressions["root"].suppressed.Load()
	p.loadReplayMu.RUnlock()
	if got != 2 {
		t.Fatalf("suppressed count = %d, want 2", got)
	}
}

func TestAdaptOpenCodeChildUpdate_DirectAliasRequestNeverReachesHandler(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","method":"_mitto/opencode/session/child_update","id":1,"params":{"rootSessionId":"root","childSessionId":"child","parentSessionId":"root","depth":1,"type":"status","status":"created"}}`)
	got, discard := adaptOpenCodeChildUpdate(line)
	if discard {
		t.Fatal("request should be preserved as an unhandled method")
	}
	var envelope struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(got, &envelope); err != nil || envelope.Method != reservedChildUpdateMethod {
		t.Fatalf("reserved request method = %q, err=%v", envelope.Method, err)
	}
}
