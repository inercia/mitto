package mcpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bindingHeaderTransport struct {
	value string
}

func (t bindingHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set(SessionBindingHeader, t.value)
	return http.DefaultTransport.RoundTrip(clone)
}

func connectBoundProtocolSession(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "mitto-binding-test", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: bindingHeaderTransport{value: token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect bound MCP session: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func sessionBindingToken(t *testing.T, srv *Server, sessionID string) string {
	t.Helper()
	srv.sessionsMu.RLock()
	defer srv.sessionsMu.RUnlock()
	reg := srv.sessions[sessionID]
	if reg == nil || reg.bindingToken == "" {
		t.Fatalf("session %q has no binding token", sessionID)
	}
	return reg.bindingToken
}

func currentSessionID(t *testing.T, clientSession *mcp.ClientSession) string {
	t.Helper()
	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "mitto_conversation_get_current", Arguments: map[string]any{"self_id": "init"},
	})
	if err != nil || result.IsError {
		t.Fatalf("bound get_current: err=%v result=%+v", err, result)
	}
	var got CurrentSessionOutput
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("encode current session output: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode current session output: %v", err)
	}
	return got.SessionID
}

// Regression test for mitto-o779: a correlated MCP caller cannot switch its
// identity by supplying another registered conversation ID.
func TestMCPCallerCannotImpersonateRegisteredSessionID(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	for _, id := range []string{"attacker-session", "victim-session"} {
		if err := store.Create(session.Metadata{SessionID: id, Name: id, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
			t.Fatalf("store.Create(%q): %v", id, err)
		}
		if err := srv.RegisterSession(id, nil, logger); err != nil {
			t.Fatalf("RegisterSession(%q): %v", id, err)
		}
	}

	clientSession := connectReaperProtocolSession(t, srv)
	srv.RegisterPendingRequest("attacker-session", "attacker-session")
	first, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "mitto_conversation_get_current", Arguments: map[string]any{"self_id": "attacker-session"},
	})
	if err != nil || first.IsError {
		t.Fatalf("establish attacker correlation: err=%v result=%+v", err, first)
	}

	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "mitto_conversation_get_current", Arguments: map[string]any{"self_id": "victim-session"},
	})
	if err != nil || result.IsError {
		t.Fatalf("repeat get_current: err=%v result=%+v", err, result)
	}
	var got CurrentSessionOutput
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("decode get_current output: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode get_current output: %v", err)
	}
	if got.SessionID != "attacker-session" {
		t.Fatalf("caller switched identity to %q; want attacker-session", got.SessionID)
	}
}

// Regression test for mitto-o779: startup must reject a remotely reachable MCP listener.
func TestNewServerRejectsRemoteBindHost(t *testing.T) {
	if _, err := NewServer(Config{Host: "0.0.0.0", Port: 0}, Dependencies{}); err == nil {
		t.Fatal("NewServer accepted remote MCP bind host 0.0.0.0")
	}
}

func TestBoundMCPTransportsResolveConcurrentInitWithoutFIFO(t *testing.T) {
	srv := newReaperTestServer(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	for _, id := range []string{"parent-session", "child-session"} {
		registerReaperOwner(t, srv, id)
		if err := srv.RegisterSession(id, nil, logger); err != nil {
			t.Fatalf("RegisterSession(%q): %v", id, err)
		}
	}
	if srv.RegisterPendingRequest("init", "parent-session") {
		t.Fatal("failed HTTP-layer init left an ambiguous pending entry")
	}
	if !srv.RegisterPendingRequest("parent-session", "parent-session") {
		t.Fatal("exact legacy correlation was unexpectedly rejected")
	}

	rawHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv.mcpServer
	}, mcpStreamableHTTPOptions())
	srv.streamableHandler = rawHandler
	ts := httptest.NewServer(srv.mcpRequestLoggingMiddleware(rawHandler))
	defer ts.Close()

	parent := connectBoundProtocolSession(t, ts.URL, sessionBindingToken(t, srv, "parent-session"))
	child := connectBoundProtocolSession(t, ts.URL, sessionBindingToken(t, srv, "child-session"))

	var wg sync.WaitGroup
	got := make([]string, 2)
	for i, clientSession := range []*mcp.ClientSession{child, parent} {
		wg.Add(1)
		go func(i int, clientSession *mcp.ClientSession) {
			defer wg.Done()
			got[i] = currentSessionID(t, clientSession)
		}(i, clientSession)
	}
	wg.Wait()
	if got[0] != "child-session" || got[1] != "parent-session" {
		t.Fatalf("bound concurrent init cross-wired: child=%q parent=%q", got[0], got[1])
	}
	if owner := srv.WaitForPendingRequest("parent-session"); owner != "parent-session" {
		t.Fatalf("bound calls consumed unrelated legacy correlation: got %q", owner)
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(mcpSessionIDHeader, parent.ID())
	req.Header.Set(SessionBindingHeader, sessionBindingToken(t, srv, "child-session"))
	rec := httptest.NewRecorder()
	srv.mcpRequestLoggingMiddleware(rawHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflicting transport rebind status=%d, want %d", rec.Code, http.StatusConflict)
	}
}
