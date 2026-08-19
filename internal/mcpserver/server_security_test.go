package mcpserver

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
