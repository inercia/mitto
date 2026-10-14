package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

func newSharedIdentityServer(t *testing.T, ids ...string) *Server {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := store.Create(session.Metadata{SessionID: id, Name: id, ACPServer: "opencode", WorkingDir: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		if err := srv.RegisterSession(id, nil, slog.Default()); err != nil {
			t.Fatal(err)
		}
	}
	return srv
}

func sharedReq(srv *Server, acpID string) *mcp.CallToolRequest {
	meta := mcp.Meta{}
	if acpID != "" {
		meta[OpenCodeCallerMetaKey] = acpID
	}
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Meta: meta},
		Extra:  &mcp.RequestExtra{Header: http.Header{http.CanonicalHeaderKey(SharedMCPHeaderName): {srv.SharedMCPSecret()}}},
	}
}

func TestSharedMiddleware_SecretValidation(t *testing.T) {
	srv := newSharedIdentityServer(t)
	called := false
	var shared bool
	h := srv.mcpRequestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		shared = isSharedTransportContext(r.Context())
	}))
	do := func(hdr map[string]string) int {
		called, shared = false, false
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := do(map[string]string{SharedMCPHeaderName: "wrong"}); c != http.StatusUnauthorized || called {
		t.Fatalf("wrong secret: code=%d called=%v", c, called)
	}
	if c := do(map[string]string{SharedMCPHeaderName: ""}); c != http.StatusUnauthorized || called {
		t.Fatalf("empty secret: code=%d called=%v", c, called)
	}
	if c := do(map[string]string{SharedMCPHeaderName: srv.SharedMCPSecret(), SessionBindingHeader: "x"}); c != http.StatusUnauthorized || called {
		t.Fatalf("both headers: code=%d called=%v", c, called)
	}
	if c := do(map[string]string{SharedMCPHeaderName: srv.SharedMCPSecret()}); c != http.StatusOK || !called || !shared {
		t.Fatalf("valid secret: code=%d called=%v shared=%v", c, called, shared)
	}
	if c := do(nil); !called || c != http.StatusOK || shared {
		t.Fatalf("no shared header must be non-shared: code=%d called=%v shared=%v", c, called, shared)
	}
}

func TestSharedResolve_PerCallMetaAndFailClosed(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a", "conv-b")
	if !srv.BindACPSession("conv-a", "ses_a") || !srv.BindACPSession("conv-b", "ses_b") {
		t.Fatal("bind failed")
	}
	if got := srv.resolveSelfIDWithMCP("init", sharedReq(srv, "ses_a")); got != "conv-a" {
		t.Fatalf("a: %q", got)
	}
	if got := srv.resolveSelfIDWithMCP("self", sharedReq(srv, "ses_b")); got != "conv-b" {
		t.Fatalf("b: %q", got)
	}
	if got := srv.resolveSelfIDWithMCP("unregistered-x", sharedReq(srv, "ses_a")); got != "conv-a" {
		t.Fatalf("unregistered self_id: %q", got)
	}
	if got := srv.resolveSelfIDWithMCP("conv-b", sharedReq(srv, "ses_a")); got != "" {
		t.Fatalf("forged registered self_id accepted: %q", got)
	}
	if got := srv.resolveSelfIDWithMCP("conv-a", sharedReq(srv, "")); got != "" {
		t.Fatalf("missing meta must fail closed: %q", got)
	}
	if got := srv.resolveSelfIDWithMCP("conv-a", sharedReq(srv, "ses_unknown")); got != "" {
		t.Fatalf("unmapped meta must fail closed: %q", got)
	}
	// Even with a seeded legacy correlation, shared requests never use it.
	srv.RegisterPendingRequest("conv-a", "conv-a")
	if got := srv.resolveSelfIDWithMCP("init", sharedReq(srv, "")); got != "" {
		t.Fatalf("legacy fallback used on shared transport: %q", got)
	}
}

func TestSharedResolve_NoOwnerAssociationOrCache(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a")
	srv.BindACPSession("conv-a", "ses_a")
	if got := srv.resolveSelfIDWithMCP("init", sharedReq(srv, "ses_a")); got != "conv-a" {
		t.Fatalf("resolve: %q", got)
	}
	srv.mcpSessionMapMu.RLock()
	n := len(srv.mcpSessionMap)
	srv.mcpSessionMapMu.RUnlock()
	if n != 0 {
		t.Fatalf("shared resolution cached %d protocol sessions", n)
	}
}

func TestBindACPSession_Semantics(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a", "conv-b")
	if !srv.BindACPSession("conv-a", "ses_1") {
		t.Fatal("bind")
	}
	if srv.BindACPSession("conv-b", "ses_1") {
		t.Fatal("collision accepted")
	}
	if srv.lookupACPSession("ses_1") != "" {
		t.Fatal("collided id must fail closed")
	}
	if !srv.BindACPSession("conv-a", "ses_2") {
		t.Fatal("rebind")
	}
	if srv.lookupACPSession("ses_1") != "" || srv.lookupACPSession("ses_2") != "conv-a" {
		t.Fatal("rebind did not drop previous id")
	}
	srv.UnregisterSession("conv-a")
	if srv.lookupACPSession("ses_2") != "" {
		t.Fatal("unregister kept mapping")
	}
}

func TestSharedWorkspaceUINotify_UsesAttestedIdentity(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a", "conv-b")
	srv.BindACPSession("conv-a", "ses_a")
	_, _, err := srv.handleWorkspaceUINotify(context.Background(), sharedReq(srv, ""), WorkspaceUINotifyInput{SelfID: "init", WorkspaceUUID: "w", Title: "t"})
	if err == nil {
		t.Fatal("missing metadata must fail closed")
	}
	_, _, err = srv.handleWorkspaceUINotify(context.Background(), sharedReq(srv, "ses_a"), WorkspaceUINotifyInput{SelfID: "conv-b", WorkspaceUUID: "w", Title: "t"})
	if err == nil {
		t.Fatal("forged self_id must be rejected")
	}
}

func TestBindACPSession_CollisionQuarantine(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a", "conv-b", "conv-c")
	srv.BindACPSession("conv-c", "ses_ok")
	srv.BindACPSession("conv-a", "ses_dup")
	if srv.BindACPSession("conv-b", "ses_dup") {
		t.Fatal("collision accepted")
	}
	if got := srv.lookupACPSession("ses_dup"); got != "" {
		t.Fatalf("ambiguous id resolved to %q", got)
	}
	if srv.lookupACPSession("ses_ok") != "conv-c" {
		t.Fatal("unrelated mapping lost")
	}
	// Re-binding by either claimant, or a third conversation, stays rejected.
	for _, c := range []string{"conv-a", "conv-b", "conv-c"} {
		if srv.BindACPSession(c, "ses_dup") {
			t.Fatalf("%s bound quarantined id", c)
		}
	}
	if srv.lookupACPSession("ses_dup") != "" {
		t.Fatal("attribution restored")
	}
	// Quarantine clears only when all claimants (a, b, c) have left.
	srv.UnbindACPSession("conv-a")
	srv.UnbindACPSession("conv-b")
	if srv.lookupACPSession("ses_dup") != "" {
		t.Fatal("quarantine cleared early")
	}
	srv.UnbindACPSession("conv-c")
	if !srv.BindACPSession("conv-a", "ses_dup") || srv.lookupACPSession("ses_dup") != "conv-a" {
		t.Fatal("clean id not bindable after claimants left")
	}
}

func TestBindACPSession_CollisionMovesToDistinctID(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a", "conv-b")
	srv.BindACPSession("conv-a", "ses_dup")
	srv.BindACPSession("conv-b", "ses_dup")
	if !srv.BindACPSession("conv-a", "ses_a2") {
		t.Fatal("distinct id rebind")
	}
	srv.UnregisterSession("conv-b")
	if srv.lookupACPSession("ses_dup") != "" {
		t.Fatal("stale identity for ses_dup")
	}
	if !srv.BindACPSession("conv-b", "ses_dup") || srv.lookupACPSession("ses_dup") != "conv-b" {
		t.Fatal("quarantine not cleared after claimants left")
	}
}

func TestSharedResolve_CollisionFailsClosed(t *testing.T) {
	srv := newSharedIdentityServer(t, "conv-a", "conv-b", "conv-c")
	srv.BindACPSession("conv-c", "ses_c")
	srv.BindACPSession("conv-a", "ses_dup")
	if srv.BindACPSession("conv-b", "ses_dup") {
		t.Fatal("collision must be rejected")
	}
	for _, self := range []string{"init", "conv-a", "conv-b"} {
		if got := srv.resolveSelfIDWithMCP(self, sharedReq(srv, "ses_dup")); got != "" {
			t.Fatalf("self=%s resolved %q on colliding id", self, got)
		}
	}
	if got := srv.resolveSelfIDWithMCP("init", sharedReq(srv, "ses_c")); got != "conv-c" {
		t.Fatalf("unrelated mapping broken: %q", got)
	}
}
