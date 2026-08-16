package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerReaperOwner(t *testing.T, srv *Server, owner string) {
	t.Helper()
	meta := session.Metadata{SessionID: owner, Name: owner, ACPServer: "test", WorkingDir: t.TempDir()}
	if err := srv.store.Create(meta); err != nil {
		t.Fatalf("store.Create(%q): %v", owner, err)
	}
	if err := srv.RegisterSession(owner, nil, slog.New(slog.NewTextHandler(os.Stderr, nil))); err != nil {
		t.Fatalf("RegisterSession(%q): %v", owner, err)
	}
}

func connectReaperProtocolSession(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv.mcpServer
	}, mcpStreamableHTTPOptions())
	srv.streamableHandler = handler
	ts := httptest.NewServer(handler)
	client := mcp.NewClient(&mcp.Implementation{Name: "mitto-owner-reaper-test", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("mcp client Connect: %v", err)
	}
	// The production logging middleware records the initialize response's new
	// session ID. This fixture serves the raw SDK handler, so mirror that touch.
	srv.reaperTouch(clientSession.ID())
	t.Cleanup(func() {
		_ = clientSession.Close()
		ts.Close()
	})
	return clientSession
}

func associateReaperOwner(t *testing.T, clientSession *mcp.ClientSession, owner string) {
	t.Helper()
	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "mitto_conversation_get_current",
		Arguments: map[string]any{"self_id": owner},
	})
	if err != nil {
		t.Fatalf("CallTool for owner %q: %v", owner, err)
	}
	if result.IsError {
		t.Fatalf("CallTool for owner %q returned tool error: %+v", owner, result.Content)
	}
}

func reaperLeaseState(srv *Server, sid string) (tracked bool, owners, inFlight int, retire bool) {
	srv.reaperMu.Lock()
	defer srv.reaperMu.Unlock()
	lease := srv.mcpSessionLeases[sid]
	if lease == nil {
		return false, 0, 0, false
	}
	return true, len(lease.owners), lease.inFlightPOSTs, lease.retireRequested
}

func TestReapOwnerlessMCPSessionWithOpenStream(t *testing.T) {
	srv := newReaperTestServer(t)
	var logs strings.Builder
	srv.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	registerReaperOwner(t, srv, "owner-one")
	clientSession := connectReaperProtocolSession(t, srv)
	associateReaperOwner(t, clientSession, "owner-one")
	sid := clientSession.ID()
	srv.reaperStreamOpened(sid)

	srv.UnregisterSession("owner-one")
	srv.reapIdleMCPSessions()

	if tracked, _, _, _ := reaperLeaseState(srv, sid); tracked {
		t.Fatal("ownerless MCP session with an open stream was not reaped")
	}
	if _, err := clientSession.ListTools(context.Background(), &mcp.ListToolsParams{}); err == nil {
		t.Fatal("expected SDK session to be deleted after final owner unregistered")
	}
	if text := logs.String(); !strings.Contains(text, "reason=owner_gone") || !strings.Contains(text, "open_streams=1") {
		t.Fatalf("missing owner-gone lifecycle evidence in logs:\n%s", text)
	}
}

func TestReapSharedMCPSessionAfterFinalOwner(t *testing.T) {
	srv := newReaperTestServer(t)
	registerReaperOwner(t, srv, "owner-one")
	registerReaperOwner(t, srv, "owner-two")
	clientSession := connectReaperProtocolSession(t, srv)
	associateReaperOwner(t, clientSession, "owner-one")
	associateReaperOwner(t, clientSession, "owner-two")
	sid := clientSession.ID()
	srv.reaperStreamOpened(sid)

	srv.UnregisterSession("owner-one")
	srv.reapIdleMCPSessions()
	if tracked, owners, _, retire := reaperLeaseState(srv, sid); !tracked || owners != 1 || retire {
		t.Fatalf("shared session changed after first owner left: tracked=%v owners=%d retire=%v", tracked, owners, retire)
	}
	if _, err := clientSession.ListTools(context.Background(), &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("shared SDK session was deleted while one owner remained: %v", err)
	}

	srv.UnregisterSession("owner-two")
	srv.reapIdleMCPSessions()
	if tracked, _, _, _ := reaperLeaseState(srv, sid); tracked {
		t.Fatal("shared session was not reaped after its final owner left")
	}
}

func TestReapOwnerlessMCPSessionWaitsForInflightPOST(t *testing.T) {
	srv := newReaperTestServer(t)
	registerReaperOwner(t, srv, "owner-one")
	clientSession := connectReaperProtocolSession(t, srv)
	associateReaperOwner(t, clientSession, "owner-one")
	sid := clientSession.ID()
	postLease := srv.reaperPOSTStarted(sid)
	srv.UnregisterSession("owner-one")

	srv.reapIdleMCPSessions()
	if tracked, _, inFlight, retire := reaperLeaseState(srv, sid); !tracked || inFlight != 1 || !retire {
		t.Fatalf("in-flight session state = tracked:%v inFlight:%d retire:%v", tracked, inFlight, retire)
	}
	srv.reaperPOSTFinished(postLease)
	srv.reapIdleMCPSessions()
	if tracked, _, _, _ := reaperLeaseState(srv, sid); tracked {
		t.Fatal("ownerless session was not reaped after its POST drained")
	}
}

func TestPOSTCancelsPendingOwnerRetirement(t *testing.T) {
	srv := newReaperTestServer(t)
	registerReaperOwner(t, srv, "owner-one")
	clientSession := connectReaperProtocolSession(t, srv)
	associateReaperOwner(t, clientSession, "owner-one")
	sid := clientSession.ID()
	srv.reaperStreamOpened(sid)
	srv.UnregisterSession("owner-one")

	postLease := srv.reaperPOSTStarted(sid)
	srv.reaperPOSTFinished(postLease)
	srv.reapIdleMCPSessions()
	if tracked, owners, inFlight, retire := reaperLeaseState(srv, sid); !tracked || owners != 0 || inFlight != 0 || retire {
		t.Fatalf("new POST did not cancel retirement: tracked=%v owners=%d inFlight=%d retire=%v", tracked, owners, inFlight, retire)
	}
}

func TestNewOwnerCancelsPendingOwnerRetirement(t *testing.T) {
	srv := newReaperTestServer(t)
	registerReaperOwner(t, srv, "owner-one")
	clientSession := connectReaperProtocolSession(t, srv)
	associateReaperOwner(t, clientSession, "owner-one")
	sid := clientSession.ID()
	srv.UnregisterSession("owner-one")
	registerReaperOwner(t, srv, "owner-two")
	associateReaperOwner(t, clientSession, "owner-two")

	srv.reapIdleMCPSessions()
	if tracked, owners, _, retire := reaperLeaseState(srv, sid); !tracked || owners != 1 || retire {
		t.Fatalf("new owner did not cancel retirement: tracked=%v owners=%d retire=%v", tracked, owners, retire)
	}
}

func TestOwnerLifecycleCleanupIsIdempotent(t *testing.T) {
	srv := newReaperTestServer(t)
	registerReaperOwner(t, srv, "owner-one")
	clientSession := connectReaperProtocolSession(t, srv)
	associateReaperOwner(t, clientSession, "owner-one")
	sid := clientSession.ID()
	srv.UnregisterSession("owner-one")
	srv.UnregisterSession("owner-one")
	srv.reapIdleMCPSessions()
	srv.reapIdleMCPSessions()
	if tracked, _, _, _ := reaperLeaseState(srv, sid); tracked {
		t.Fatal("idempotent cleanup left the lease tracked")
	}
}

func TestOwnerLifecycleRecentActivityIsNotIdleReaped(t *testing.T) {
	srv := newReaperTestServer(t)
	clock := time.Now()
	srv.reaperNow = func() time.Time { return clock }
	srv.reaperTimeout = 30 * time.Minute
	srv.reaperTouch("recent-session")
	srv.reapIdleMCPSessions()
	if tracked, _, _, _ := reaperLeaseState(srv, "recent-session"); !tracked {
		t.Fatal("recent unknown session was incorrectly idle-reaped")
	}
}
