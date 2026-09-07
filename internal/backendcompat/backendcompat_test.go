package backendcompat

import (
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestBackendConnectionFromACPServer_OK(t *testing.T) {
	srv := config.ACPServer{
		Name:    "Auggie",
		Command: "auggie acp",
		Cwd:     "/work",
		Env:     map[string]string{"FOO": "bar"},
	}
	conn, err := BackendConnectionFromACPServer(srv)
	if err != nil {
		t.Fatalf("BackendConnectionFromACPServer() error = %v", err)
	}
	if conn.Backend != BackendIDForACP {
		t.Fatalf("Backend = %q, want %q", conn.Backend, BackendIDForACP)
	}
	if conn.Protocol != agentbackend.ProtocolACP {
		t.Fatalf("Protocol = %q, want %q", conn.Protocol, agentbackend.ProtocolACP)
	}
	if conn.Command != srv.Command || conn.Cwd != srv.Cwd {
		t.Fatalf("Command/Cwd not preserved byte-for-byte: got Command=%q Cwd=%q", conn.Command, conn.Cwd)
	}
	if conn.Env["FOO"] != "bar" {
		t.Fatalf("Env not preserved: got %+v", conn.Env)
	}
	if conn.CredentialRef != "" || conn.Endpoint != "" {
		t.Fatalf("local ACP connection must not populate remote fields: Endpoint=%q CredentialRef=%q", conn.Endpoint, conn.CredentialRef)
	}
	if err := conn.Validate(); err != nil {
		t.Fatalf("derived connection failed Validate(): %v", err)
	}
}

func TestBackendConnectionFromACPServer_EmptyNameRejected(t *testing.T) {
	_, err := BackendConnectionFromACPServer(config.ACPServer{Command: "auggie"})
	if err == nil {
		t.Fatal("BackendConnectionFromACPServer() error = nil, want error for empty Name")
	}
}

func TestBackendConnectionFromACPServer_EmptyCommandRejected(t *testing.T) {
	_, err := BackendConnectionFromACPServer(config.ACPServer{Name: "Auggie"})
	if err == nil {
		t.Fatal("BackendConnectionFromACPServer() error = nil, want error for empty Command")
	}
}

func TestAgentRefFromACPServerName_OK(t *testing.T) {
	ref, err := AgentRefFromACPServerName("Auggie")
	if err != nil {
		t.Fatalf("AgentRefFromACPServerName() error = %v", err)
	}
	if ref.Backend != BackendIDForACP || ref.Provider != "Auggie" {
		t.Fatalf("unexpected AgentRef: %+v", ref)
	}
}

func TestAgentRefFromACPServerName_EmptyRejected(t *testing.T) {
	if _, err := AgentRefFromACPServerName(""); err == nil {
		t.Fatal("AgentRefFromACPServerName(\"\") error = nil, want error")
	}
}

func TestSessionRefFromMetadata_DoesNotCollapseIdentitySpaces(t *testing.T) {
	meta := session.Metadata{
		SessionID:    "mitto-conv-123",
		ACPServer:    "Auggie",
		ACPSessionID: "upstream-acp-session-456",
	}
	ref := SessionRefFromMetadata(meta)
	if ref.ConversationID != meta.SessionID {
		t.Fatalf("ConversationID = %q, want %q (meta.SessionID)", ref.ConversationID, meta.SessionID)
	}
	if string(ref.Provider) != meta.ACPServer {
		t.Fatalf("Provider = %q, want %q (meta.ACPServer)", ref.Provider, meta.ACPServer)
	}
	if string(ref.ProviderSession) != meta.ACPSessionID {
		t.Fatalf("ProviderSession = %q, want %q (meta.ACPSessionID)", ref.ProviderSession, meta.ACPSessionID)
	}
	// The two identifier spaces must never collapse into one.
	if ref.ConversationID == string(ref.ProviderSession) {
		t.Fatalf("ConversationID and ProviderSession collapsed to the same value %q", ref.ConversationID)
	}
}

func TestSessionRefFromMetadata_EmptyACPSessionIDPreserved(t *testing.T) {
	// A session that has never resumed (or predates ACP resumption) has no
	// ACPSessionID yet; the neutral projection must reflect that as empty,
	// not synthesize one.
	meta := session.Metadata{SessionID: "mitto-conv-789", ACPServer: "Auggie"}
	ref := SessionRefFromMetadata(meta)
	if ref.ProviderSession != "" {
		t.Fatalf("ProviderSession = %q, want empty when meta.ACPSessionID is empty", ref.ProviderSession)
	}
}
