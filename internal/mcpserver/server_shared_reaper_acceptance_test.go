package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An ownerless shared OpenCode protocol session (shared-secret header, no
// protocol-owner association) must not be idle-reaped beneath an in-flight
// blocking mitto_ui_options POST, must drain afterwards, and must be retired
// once genuinely idle. A fresh session is then accepted on the same endpoint.
// Limitation: this does not exercise OpenCode's own client retry logic; it only
// shows the server accepts a new MCP session after retirement.
func TestSharedMCPSession_ReaperRespectsInFlightPOSTThenRetires(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}

	var clockMu sync.Mutex
	clock := time.Now()
	srv.reaperNow = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	srv.reaperTimeout = 30 * time.Minute

	conv := "20261014-000000-cccccccc"
	p := newBlockingPrompter("C")
	if err := store.Create(session.Metadata{
		SessionID: conv, Name: conv, ACPServer: "opencode", WorkingDir: t.TempDir(),
		AdvancedSettings: map[string]bool{session.FlagCanPromptUser: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.RegisterSession(conv, p, slog.Default()); err != nil {
		t.Fatal(err)
	}
	if !srv.BindACPSession(conv, "ses_C") {
		t.Fatal("BindACPSession failed")
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv.mcpServer }, mcpStreamableHTTPOptions())
	srv.reaperMu.Lock()
	srv.streamableHandler = handler
	srv.reaperMu.Unlock()
	ts := httptest.NewServer(srv.mcpRequestLoggingMiddleware(handler))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connect := func() *mcp.ClientSession {
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   ts.URL,
			HTTPClient: &http.Client{Transport: secretRoundTripper{secret: srv.SharedMCPSecret()}},
		}, nil)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		return cs
	}
	cs := connect()
	defer cs.Close()
	sid := cs.ID()

	done := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Meta: mcp.Meta{OpenCodeCallerMetaKey: "ses_C"},
			Name: "mitto_ui_options",
			Arguments: map[string]any{
				"self_id": "init", "question": "q",
				"options": []map[string]any{{"label": "ok"}},
			},
		})
		if err == nil && res.IsError {
			err = context.Canceled
		}
		done <- err
	}()

	select {
	case <-p.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("prompt never reached prompter")
	}

	if tracked, owners, inFlight, _ := reaperLeaseState(srv, sid); !tracked || owners != 0 || inFlight < 1 {
		t.Fatalf("before reap: tracked=%v owners=%d inFlight=%d", tracked, owners, inFlight)
	}
	advance(mcpIdleSessionTimeout + 31*time.Minute)
	srv.reapIdleMCPSessions()
	if tracked, owners, inFlight, retire := reaperLeaseState(srv, sid); !tracked || owners != 0 || inFlight < 1 || retire {
		t.Fatalf("in-flight session was reaped: tracked=%v owners=%d inFlight=%d retire=%v", tracked, owners, inFlight, retire)
	}
	select {
	case err := <-done:
		t.Fatalf("blocked call torn down early: %v", err)
	default:
	}

	close(p.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("call failed after release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("call did not finish")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, inFlight, _ := reaperLeaseState(srv, sid)
		if inFlight == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inFlightPOSTs did not drain: %d", inFlight)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if owner := srv.lookupMCPSession(sid); owner != "" {
		t.Fatalf("shared protocol session acquired owner %q", owner)
	}

	// The reaper needs no open GET stream; close the client's so only idleness remains.
	cs.Close()
	advance(mcpIdleSessionTimeout + 31*time.Minute)
	srv.reapIdleMCPSessions()
	if tracked, _, _, _ := reaperLeaseState(srv, sid); tracked {
		t.Fatal("idle ownerless shared session was not retired")
	}

	cs2 := connect()
	defer cs2.Close()
	if cs2.ID() == sid {
		t.Fatal("expected a fresh MCP session id")
	}
	if _, err := cs2.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("fresh session rejected: %v", err)
	}
}
