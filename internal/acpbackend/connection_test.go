package acpbackend

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
)

func TestConnection_Lifecycle(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	if got := c.State(); got != agentbackend.LifecycleDisconnected {
		t.Fatalf("initial State = %v, want Disconnected", got)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := c.State(); got != agentbackend.LifecycleConnected {
		t.Fatalf("State after Connect = %v, want Connected", got)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := c.State(); got != agentbackend.LifecycleStopped {
		t.Fatalf("State after Close = %v, want Stopped", got)
	}
	// Close must be idempotent.
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestConnection_Providers(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	providers, err := c.Providers(context.Background())
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(providers) != 1 || providers[0] != "acp" {
		t.Fatalf("Providers = %+v, want [\"acp\"]", providers)
	}
}

func TestConnection_NewSession_SynthesizesDistinctConversationID(t *testing.T) {
	fp := newFakeSharedProcess()
	fp.newSessionHandle = &conversation.SessionHandle{SessionID: "upstream-1"}
	c := NewConnection(fp, "acp", "/work", nil)

	sess, err := c.NewSession(context.Background(), "acp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ref := sess.Ref()
	if ref.ConversationID == "" {
		t.Fatal("expected a synthesized, non-empty ConversationID")
	}
	if string(ref.ProviderSession) != "upstream-1" {
		t.Errorf("ProviderSession = %q, want upstream-1", ref.ProviderSession)
	}
	if ref.ConversationID == string(ref.ProviderSession) {
		t.Errorf("ConversationID must be distinct from ProviderSession (ADR §4), got both %q", ref.ConversationID)
	}

	sess2, err := c.NewSession(context.Background(), "acp")
	if err != nil {
		t.Fatalf("second NewSession: %v", err)
	}
	if sess2.Ref().ConversationID == ref.ConversationID {
		t.Error("expected distinct ConversationIDs across separate NewSession calls")
	}

	// RegisterSession must have been called on the underlying process.
	if _, ok := fp.registered[acp.SessionId("upstream-1")]; !ok {
		t.Error("expected the session's callbacks to be registered on the SharedProcess")
	}
}

func TestConnection_NewSession_UnknownProvider(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	if _, err := c.NewSession(context.Background(), "other-provider"); !errors.Is(err, agentbackend.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for a mismatched provider, got %v", err)
	}
}

func TestConnection_NewSession_PropagatesError(t *testing.T) {
	fp := newFakeSharedProcess()
	fp.newSessionErr = errors.New("boom")
	c := NewConnection(fp, "acp", "/work", nil)
	if _, err := c.NewSession(context.Background(), "acp"); err == nil {
		t.Fatal("expected an error to propagate from the underlying SharedProcess")
	}
}

func TestConnection_LoadSession_RequiresProviderSessionID(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	_, err := c.LoadSession(context.Background(), agentbackend.SessionRef{})
	if !errors.Is(err, agentbackend.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for an empty ProviderSession, got %v", err)
	}
}

func TestConnection_LoadSession_ProviderMismatch(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{Provider: "other", ProviderSession: "upstream-1"}
	if _, err := c.LoadSession(context.Background(), ref); !errors.Is(err, agentbackend.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for a mismatched provider, got %v", err)
	}
}

func TestConnection_LoadSession_PreservesCallerConversationID(t *testing.T) {
	fp := newFakeSharedProcess()
	fp.loadSessionHandle = &conversation.SessionHandle{SessionID: "upstream-1"}
	c := NewConnection(fp, "acp", "/work", nil)

	ref := agentbackend.SessionRef{ConversationID: "conv-existing", Provider: "acp", ProviderSession: "upstream-1"}
	sess, err := c.LoadSession(context.Background(), ref)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.Ref().ConversationID != "conv-existing" {
		t.Errorf("ConversationID = %q, want the caller-supplied conv-existing", sess.Ref().ConversationID)
	}
}

func TestConnection_ResumeSession_PropagatesError(t *testing.T) {
	fp := newFakeSharedProcess()
	fp.resumeSessionErr = errors.New("resume failed")
	c := NewConnection(fp, "acp", "/work", nil)
	ref := agentbackend.SessionRef{Provider: "acp", ProviderSession: "upstream-1"}
	if _, err := c.ResumeSession(context.Background(), ref); err == nil {
		t.Fatal("expected an error to propagate from the underlying SharedProcess")
	}
}
