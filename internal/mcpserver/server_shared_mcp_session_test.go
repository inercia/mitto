package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// Shared-process agents (e.g. OpenCode) multiplex several conversations over
// one MCP protocol session. A cached owner must not hijack calls whose
// self_id names a different registered conversation; the call fails closed.
func TestResolveSelfID_SharedMCPSessionDoesNotCrossConversations(t *testing.T) {
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
	ids := []string{session.GenerateSessionID(), session.GenerateSessionID()}
	for _, id := range ids {
		if err := store.Create(session.Metadata{SessionID: id, Name: id, ACPServer: "test", WorkingDir: t.TempDir()}); err != nil {
			t.Fatalf("store.Create: %v", err)
		}
		if err := srv.RegisterSession(id, nil, logger); err != nil {
			t.Fatalf("RegisterSession: %v", err)
		}
	}
	first, second := ids[0], ids[1]

	var capturedReq *mcp.CallToolRequest
	mcp.AddTool(srv.mcpServer, &mcp.Tool{Name: "test_capture_session"},
		func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			capturedReq = req
			return nil, struct{}{}, nil
		})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv.mcpServer
	}, mcpStreamableHTTPOptions()))
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "shared-session-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer cs.Close()
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "test_capture_session"}); err != nil {
		t.Fatalf("capture call: %v", err)
	}
	if capturedReq == nil || capturedReq.Session == nil {
		t.Fatal("no captured request session")
	}

	if got := srv.resolveSelfIDWithMCP(first, capturedReq); got != first {
		t.Fatalf("first conversation resolved to %q, want %q", got, first)
	}
	// The second conversation must never be routed to the first owner.
	if got := srv.resolveSelfIDWithMCP(second, capturedReq); got != "" {
		t.Fatalf("second conversation resolved to %q; want empty (fail closed, never %q)", got, first)
	}
	if got := srv.resolveSelfIDWithMCP(first, capturedReq); got != first {
		t.Fatalf("first conversation after mismatch resolved to %q, want %q", got, first)
	}
}
