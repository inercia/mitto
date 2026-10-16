package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type diagnosticUIPrompter struct {
	mu       sync.Mutex
	prompts  int
	notifies int
}

func (p *diagnosticUIPrompter) UIPrompt(_ context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	p.mu.Lock()
	p.prompts++
	p.mu.Unlock()
	return UIPromptResponse{RequestID: req.RequestID, OptionID: "0"}, nil
}

func (*diagnosticUIPrompter) DismissPrompt(string) {}

func (p *diagnosticUIPrompter) UINotify(UINotifyRequest) error {
	p.mu.Lock()
	p.notifies++
	p.mu.Unlock()
	return nil
}

type spoofDiagnosticHeaderTransport struct {
	secret string
}

func (rt spoofDiagnosticHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set(SharedMCPHeaderName, rt.secret)
	clone.Header[mcpDiagnosticRequestIDHeader] = []string{"spoofed", "also-spoofed"}
	return http.DefaultTransport.RoundTrip(clone)
}

func TestMCPDiagnosticRequestIDMiddlewareOverwritesSpoofAndPreservesBody(t *testing.T) {
	srv := newSharedIdentityServer(t)
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{}}`
	var gotID string
	var gotBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get(mcpDiagnosticRequestIDHeader)
		gotBodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read downstream body: %v", err)
		}
		gotBody = string(gotBodyBytes)
		w.WriteHeader(http.StatusNoContent)
	})
	h := srv.mcpRequestLoggingMiddleware(next)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	requestIDHeader := http.CanonicalHeaderKey(mcpDiagnosticRequestIDHeader)
	req.Header[requestIDHeader] = []string{"client-value", "second-value"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if !isMCPDiagnosticRequestID(gotID) || gotID == "client-value" {
		t.Fatalf("downstream request ID = %q, want server-generated 8-char lowercase hex", gotID)
	}
	if gotBody != body {
		t.Fatalf("downstream body = %q, want original %q", gotBody, body)
	}
	if got := req.Header.Values(requestIDHeader); len(got) != 2 || got[0] != "client-value" {
		t.Fatalf("middleware mutated original request headers: %v", got)
	}
}

func TestSharedCallerDiagnosticAttrsAreBoundedAndNilSafe(t *testing.T) {
	valid := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "mitto_ui_options"},
		Extra:  &mcp.RequestExtra{Header: http.Header{http.CanonicalHeaderKey(mcpDiagnosticRequestIDHeader): {"ab12cd34"}}},
	}
	attrs := sharedCallerDiagnosticAttrs(valid, false, "absent", "caller_meta_absent")
	if got := diagnosticAttrMap(attrs); got["mcp_req_id"] != "ab12cd34" || got["tool_name"] != "mitto_ui_options" {
		t.Fatalf("valid diagnostic attrs = %#v", got)
	}

	for _, value := range []string{"ABCDEF12", "1234567", "123456789", "1234567g", ""} {
		if isMCPDiagnosticRequestID(value) {
			t.Errorf("isMCPDiagnosticRequestID(%q) = true, want false", value)
		}
	}
	for _, value := range []string{"", strings.Repeat("a", 129), "tool name", "tool/with/slash", "tool\nname"} {
		if boundedMCPDiagnosticToolName(value) != "" {
			t.Errorf("boundedMCPDiagnosticToolName(%q) = %q, want omitted", value, boundedMCPDiagnosticToolName(value))
		}
	}
	if got := boundedMCPDiagnosticToolName("Tool_name-1.v2"); got != "Tool_name-1.v2" {
		t.Fatalf("bounded valid tool name = %q", got)
	}
	if got := sharedCallerDiagnosticAttrs(nil, false, "absent", "caller_meta_absent"); len(got) != 6 {
		t.Fatalf("nil request attrs = %#v, want only fixed metadata fields", got)
	}
	if got := sharedCallerDiagnosticAttrs(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: strings.Repeat("x", 129)}}, true, "string", "caller_unresolved"); len(got) != 6 {
		t.Fatalf("invalid tool name attrs = %#v, want no tool_name", got)
	}
}

func TestResolveSharedCaller_LogsBoundedRejectionReasons(t *testing.T) {
	buf := &synchronizedBuffer{}
	srv := newSharedIdentityServer(t, "conv-a", "conv-b")
	srv.logger = slog.New(slog.NewJSONHandler(buf, nil))
	if !srv.BindACPSession("conv-a", "ses-a") {
		t.Fatal("bind conv-a")
	}

	tests := []struct {
		name       string
		meta       mcp.Meta
		selfID     string
		wantState  string
		wantReason string
	}{
		{name: "absent", meta: mcp.Meta{}, selfID: "conv-a", wantState: "absent", wantReason: "caller_meta_absent"},
		{name: "wrong type", meta: mcp.Meta{OpenCodeCallerMetaKey: nil}, selfID: "conv-a", wantState: "wrong_type", wantReason: "caller_meta_wrong_type"},
		{name: "empty", meta: mcp.Meta{OpenCodeCallerMetaKey: ""}, selfID: "conv-a", wantState: "empty_string", wantReason: "caller_meta_empty"},
		{name: "unresolved", meta: mcp.Meta{OpenCodeCallerMetaKey: "raw-metadata-sentinel"}, selfID: "conv-a", wantState: "string", wantReason: "caller_unresolved"},
		{name: "self id mismatch", meta: mcp.Meta{OpenCodeCallerMetaKey: "ses-a"}, selfID: "conv-b", wantState: "string", wantReason: "self_id_mismatch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{Name: "mitto_ui_options", Meta: tc.meta},
				Extra:  &mcp.RequestExtra{Header: http.Header{http.CanonicalHeaderKey(mcpDiagnosticRequestIDHeader): {"deadbeef"}}},
			}
			if got := srv.resolveSharedCaller(req, tc.selfID); got != "" {
				t.Fatalf("resolveSharedCaller() = %q, want fail closed", got)
			}
			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			var record map[string]any
			for i := len(lines) - 1; i >= 0; i-- {
				var candidate map[string]any
				if err := json.Unmarshal([]byte(lines[i]), &candidate); err == nil && candidate["reason"] == tc.wantReason {
					record = candidate
					break
				}
			}
			if record == nil {
				t.Fatalf("no log with reason %q in %s", tc.wantReason, buf.String())
			}
			if record["caller_meta_state"] != tc.wantState || record["tool_name"] != "mitto_ui_options" || record["mcp_req_id"] != "deadbeef" {
				t.Fatalf("diagnostic fields = %#v", record)
			}
		})
	}

	if strings.Contains(buf.String(), "raw-metadata-sentinel") || strings.Contains(buf.String(), "conv-b") || strings.Contains(buf.String(), "ses-a") {
		t.Fatalf("rejection logs contain raw caller/self identity: %s", buf.String())
	}
}

func TestSharedDiagnosticsCorrelateConcurrentSDKCallsPerRequest(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	logs := &synchronizedBuffer{}
	srv.logger = slog.New(slog.NewJSONHandler(logs, nil))
	prompter := &diagnosticUIPrompter{}
	convID := "20261016-000000-cccccccc"
	if err := store.Create(session.Metadata{
		SessionID:        convID,
		Name:             convID,
		ACPServer:        "opencode",
		WorkingDir:       t.TempDir(),
		AdvancedSettings: map[string]bool{session.FlagCanPromptUser: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.RegisterSession(convID, prompter, slog.Default()); err != nil {
		t.Fatal(err)
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv.mcpServer }, mcpStreamableHTTPOptions())
	ts := httptest.NewServer(srv.mcpRequestLoggingMiddleware(handler))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "diagnostics-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{Transport: spoofDiagnosticHeaderTransport{secret: srv.SharedMCPSecret()}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	type callResult struct {
		tool   string
		result *mcp.CallToolResult
		err    error
	}
	results := make(chan callResult, 2)
	calls := []struct {
		name string
		meta mcp.Meta
		args map[string]any
	}{
		{name: "mitto_ui_options", meta: mcp.Meta{}, args: map[string]any{
			"self_id": "init", "question": "diagnostic prompt sentinel", "options": []map[string]any{{"label": "continue"}},
		}},
		{name: "mitto_ui_notify", meta: mcp.Meta{OpenCodeCallerMetaKey: "raw-metadata-sentinel"}, args: map[string]any{
			"self_id": "init", "title": "diagnostic title sentinel", "message": "diagnostic message sentinel",
		}},
	}
	for _, call := range calls {
		call := call
		go func() {
			res, callErr := cs.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Meta: call.meta, Arguments: call.args})
			results <- callResult{tool: call.name, result: res, err: callErr}
		}()
	}
	for range calls {
		got := <-results
		if got.err == nil && (got.result == nil || !got.result.IsError) {
			t.Errorf("%s unexpectedly succeeded: result=%#v err=%v", got.tool, got.result, got.err)
		}
	}

	if owner := srv.lookupMCPSession(cs.ID()); owner != "" {
		t.Fatalf("shared protocol session acquired owner %q", owner)
	}
	prompter.mu.Lock()
	promptCount, notifyCount := prompter.prompts, prompter.notifies
	prompter.mu.Unlock()
	if promptCount != 0 || notifyCount != 0 {
		t.Fatalf("rejected calls caused side effects: prompts=%d notifications=%d", promptCount, notifyCount)
	}

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err == nil {
			records = append(records, record)
		}
	}
	warnings := make([]map[string]any, 0, 2)
	for _, record := range records {
		if record["msg"] == "Shared MCP caller identity unavailable" {
			warnings = append(warnings, record)
		}
	}
	if len(warnings) != 2 {
		t.Fatalf("got %d shared caller warnings, want 2; logs: %s", len(warnings), logs.String())
	}

	want := map[string]struct {
		state  string
		reason string
	}{
		"mitto_ui_options": {state: "absent", reason: "caller_meta_absent"},
		"mitto_ui_notify":  {state: "string", reason: "caller_unresolved"},
	}
	seen := make(map[string]bool)
	for _, warning := range warnings {
		tool, _ := warning["tool_name"].(string)
		wantFields, ok := want[tool]
		if !ok {
			t.Fatalf("unexpected tool in warning: %#v", warning)
		}
		if seen[tool] {
			t.Fatalf("duplicate warning for %s: %#v", tool, warning)
		}
		seen[tool] = true
		if warning["caller_meta_state"] != wantFields.state || warning["reason"] != wantFields.reason {
			t.Errorf("warning for %s has wrong classification: %#v", tool, warning)
		}
		requestID, _ := warning["mcp_req_id"].(string)
		if !isMCPDiagnosticRequestID(requestID) || requestID == "spoofed" {
			t.Errorf("warning has invalid/missing server request ID: %#v", warning)
		}
		received, completed := false, false
		for _, record := range records {
			if record["mcp_req_id"] != requestID || record["rpc_method"] != "tools/call" {
				continue
			}
			switch record["msg"] {
			case "MCP request received":
				received = true
			case "MCP request completed":
				completed = true
			}
		}
		if !received || !completed {
			t.Errorf("warning ID %q did not match its own tools/call received/completed records (received=%v completed=%v)", requestID, received, completed)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("saw warnings for %d tools, want %d", len(seen), len(want))
	}
	for _, sentinel := range []string{srv.SharedMCPSecret(), "raw-metadata-sentinel", "diagnostic prompt sentinel", "diagnostic title sentinel", "diagnostic message sentinel"} {
		if strings.Contains(logs.String(), sentinel) {
			t.Errorf("logs contain sensitive sentinel %q", sentinel)
		}
	}
}

func diagnosticAttrMap(attrs []any) map[string]any {
	got := make(map[string]any, len(attrs)/2)
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if ok {
			got[key] = attrs[i+1]
		}
	}
	return got
}
