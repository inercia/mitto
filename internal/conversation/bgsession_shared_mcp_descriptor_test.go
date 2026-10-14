package conversation

import (
	"reflect"
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/session"
)

type callerSessionProcess struct {
	*fakeSharedProcess
	name, version string
}

func (p *callerSessionProcess) Capabilities() agentbackend.Capabilities {
	return NewProcessCapabilitiesWithAgentInfo(&acp.AgentCapabilities{}, p.name, p.version)
}

func newDescriptorFixture(t *testing.T, ids ...string) (*mcpserver.Server, []*BackgroundSession, *session.Store) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := mcpserver.NewServer(mcpserver.Config{Port: 0}, mcpserver.Dependencies{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop() })
	var out []*BackgroundSession
	for _, id := range ids {
		if err := store.Create(session.Metadata{SessionID: id, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
			t.Fatal(err)
		}
		out = append(out, &BackgroundSession{persistedID: id, store: store, globalMcpServer: srv})
	}
	return srv, out, store
}

var httpCaps = acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: true}}

func TestStartSessionMcpServer_SharedOpenCode2_IdenticalDescriptor(t *testing.T) {
	srv, bss, store := newDescriptorFixture(t, "20260101-000000-aaaaaaaa", "20260101-000000-bbbbbbbb")
	var got [][]acp.McpServer
	for _, bs := range bss {
		bs.sharedProcess = &callerSessionProcess{fakeSharedProcess: newFakeSharedProcess(), name: "OpenCode", version: "2.0.1"}
		got = append(got, bs.startSessionMcpServer(store, httpCaps))
	}
	if len(got[0]) != 1 || got[0][0].Http == nil {
		t.Fatalf("expected one http server, got %+v", got[0])
	}
	if !reflect.DeepEqual(got[0], got[1]) {
		t.Fatalf("descriptors differ across conversations:\n%+v\n%+v", got[0], got[1])
	}
	h := got[0][0].Http
	if h.Name != "mitto" || h.Url == "" || len(h.Headers) != 1 ||
		h.Headers[0].Name != mcpserver.SharedMCPHeaderName || h.Headers[0].Value != srv.SharedMCPSecret() {
		t.Fatalf("unexpected shared descriptor: %+v", h)
	}
}

func TestStartSessionMcpServer_NonSharedCases_KeepPerSessionBinding(t *testing.T) {
	cases := []struct {
		name          string
		proc          func() SharedProcess
		wantUnchanged bool
	}{
		{"direct", func() SharedProcess { return nil }, true},
		{"opencode1", func() SharedProcess {
			return &callerSessionProcess{fakeSharedProcess: newFakeSharedProcess(), name: "OpenCode", version: "1.9.0"}
		}, true},
		{"opencode-unknown", func() SharedProcess {
			return &callerSessionProcess{fakeSharedProcess: newFakeSharedProcess(), name: "OpenCode", version: "dev"}
		}, true},
		{"other-agent", func() SharedProcess {
			return &callerSessionProcess{fakeSharedProcess: newFakeSharedProcess(), name: "Other", version: "9.0.0"}
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, bss, store := newDescriptorFixture(t, "20260101-000000-cccccccc")
			bs := bss[0]
			if p := tc.proc(); p != nil {
				bs.sharedProcess = p
			}
			servers := bs.startSessionMcpServer(store, httpCaps)
			if len(servers) != 1 || servers[0].Http == nil {
				t.Fatalf("expected one http server, got %+v", servers)
			}
			hs := servers[0].Http.Headers
			if len(hs) != 1 || hs[0].Name != mcpserver.SessionBindingHeader {
				t.Fatalf("expected per-session binding header, got %+v", hs)
			}
		})
	}
}

func TestSetACPSessionID_BindsRebindsAndRejectsCollision(t *testing.T) {
	srv, bss, _ := newDescriptorFixture(t, "20260101-000000-dddddddd", "20260101-000000-eeeeeeee")
	a, b := bss[0], bss[1]

	a.setACPSessionID("ses_1")
	if a.acpID != "ses_1" {
		t.Fatalf("acpID not assigned: %q", a.acpID)
	}
	// Colliding id: bound elsewhere, so the server must refuse a second owner.
	if srv.BindACPSession(b.persistedID, "ses_1") {
		t.Fatal("collision must not bind")
	}
	// Collision quarantines the id: a further bind of it still fails.
	if srv.BindACPSession(b.persistedID, "ses_1") {
		t.Fatal("quarantined id must not bind")
	}
	// A distinct id binds fine and empty id unbinds the previous mapping.
	a.setACPSessionID("ses_3")
	a.setACPSessionID("")
	if a.acpID != "" {
		t.Fatalf("expected cleared acpID, got %q", a.acpID)
	}
	if !srv.BindACPSession(b.persistedID, "ses_3") {
		t.Fatal("empty id must release a's previous mapping")
	}
}

func TestHsSetACPID_DelegatesToBinding(t *testing.T) {
	srv, bss, _ := newDescriptorFixture(t, "20260101-000000-ffffffff", "20260101-000000-99999999")
	bss[0].hsSetACPID("ses_h")
	if srv.BindACPSession(bss[1].persistedID, "ses_h") {
		t.Fatal("hsSetACPID did not bind mapping")
	}
}
