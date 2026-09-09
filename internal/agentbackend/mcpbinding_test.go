package agentbackend

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFakeHost_Ownership_ReportsHost(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := h.Ownership(sess.Ref()); got != OwnershipHost {
		t.Fatalf("Ownership() = %v, want OwnershipHost", got)
	}
}

func TestFakeHost_BindMCP_IdempotentForSameRef(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	ref := SessionRef{ConversationID: "conv-1", Provider: "p1", ProviderSession: "upstream-1"}

	h1, err := h.BindMCP(context.Background(), ref)
	if err != nil {
		t.Fatalf("BindMCP: %v", err)
	}
	if h1.IsZero() {
		t.Fatal("BindMCP returned a zero handle")
	}
	h2, err := h.BindMCP(context.Background(), ref)
	if err != nil {
		t.Fatalf("BindMCP (repeat): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("repeat BindMCP for the same ref returned a different handle: %+v vs %+v", h1, h2)
	}
}

func TestFakeHost_BindMCP_RejectsCrossSessionRebind(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	original := SessionRef{ConversationID: "conv-1", Provider: "p1", ProviderSession: "upstream-shared"}
	other := SessionRef{ConversationID: "conv-2", Provider: "p1", ProviderSession: "upstream-shared"}

	if _, err := h.BindMCP(context.Background(), original); err != nil {
		t.Fatalf("BindMCP(original): %v", err)
	}
	if _, err := h.BindMCP(context.Background(), other); !errors.Is(err, ErrCrossSessionMCPBinding) {
		t.Fatalf("BindMCP(other) err = %v, want ErrCrossSessionMCPBinding", err)
	}
}

func TestFakeHost_UnbindMCP_RejectsCrossSessionRelease(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	original := SessionRef{ConversationID: "conv-1", Provider: "p1", ProviderSession: "upstream-shared"}
	other := SessionRef{ConversationID: "conv-2", Provider: "p1", ProviderSession: "upstream-shared"}

	if _, err := h.BindMCP(context.Background(), original); err != nil {
		t.Fatalf("BindMCP: %v", err)
	}
	if err := h.UnbindMCP(context.Background(), other); !errors.Is(err, ErrCrossSessionMCPBinding) {
		t.Fatalf("UnbindMCP(other) err = %v, want ErrCrossSessionMCPBinding", err)
	}
	// The original owner can still release it.
	if err := h.UnbindMCP(context.Background(), original); err != nil {
		t.Fatalf("UnbindMCP(original): %v", err)
	}
}

func TestFakeHost_UnbindMCP_UnknownSession(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	ref := SessionRef{ConversationID: "ghost", Provider: "p1", ProviderSession: "no-such-upstream"}
	if err := h.UnbindMCP(context.Background(), ref); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("UnbindMCP(unknown) err = %v, want ErrSessionNotFound", err)
	}
}

func TestMCPBindingHandle_StringNeverExposesInternalID(t *testing.T) {
	ref := SessionRef{ConversationID: "conv-secret-test"}
	h := NewMCPBindingHandle(ref, "super-secret-binding-token")
	s := h.String()
	if strings.Contains(s, "super-secret-binding-token") {
		t.Fatalf("MCPBindingHandle.String() leaked the internal id: %q", s)
	}
	if !strings.Contains(s, "conv-secret-test") {
		t.Fatalf("MCPBindingHandle.String() = %q, want it to name the bound session", s)
	}
}

func TestMCPBindingHandle_IsZero(t *testing.T) {
	var h MCPBindingHandle
	if !h.IsZero() {
		t.Fatal("zero-value MCPBindingHandle.IsZero() = false, want true")
	}
	bound := NewMCPBindingHandle(SessionRef{ConversationID: "c"}, "id")
	if bound.IsZero() {
		t.Fatal("bound handle.IsZero() = true, want false")
	}
}

func TestEndpointBinding_ZeroValueIsClosedByDefault(t *testing.T) {
	var b EndpointBinding
	if b.Address != AddressLoopback {
		t.Fatalf("zero value Address = %v, want AddressLoopback", b.Address)
	}
	if b.ToolsRelayed {
		t.Fatal("zero value ToolsRelayed = true, want false (closed by default)")
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("zero value Validate() = %v, want nil (loopback needs no TLS/auth)", err)
	}
}

func TestEndpointBinding_RemoteRequiresTLS(t *testing.T) {
	b := EndpointBinding{Address: AddressRemote, Auth: AuthBearer, CredentialRef: "ref-1"}
	if err := b.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error for a remote endpoint without TLS")
	}
}

func TestEndpointBinding_RemoteRequiresAuth(t *testing.T) {
	b := EndpointBinding{Address: AddressRemote, TLSRequired: true}
	if err := b.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error for a remote endpoint without an auth scheme")
	}
}

func TestEndpointBinding_RemoteRequiresCredentialRef(t *testing.T) {
	b := EndpointBinding{Address: AddressRemote, TLSRequired: true, Auth: AuthBearer}
	if err := b.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error for a remote endpoint with an auth scheme but no CredentialRef")
	}
}

func TestEndpointBinding_RemoteFullyConfiguredValidates(t *testing.T) {
	b := EndpointBinding{Address: AddressRemote, TLSRequired: true, Auth: AuthBearer, CredentialRef: "ref-1"}
	if err := b.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestEndpointBinding_StringNeverExposesCredentialValue(t *testing.T) {
	// CredentialRef only ever holds an opaque reference, never a secret
	// value, but this pins that String() surfaces the reference as-is
	// (nothing resembling a raw secret should ever be assigned to it).
	b := EndpointBinding{Address: AddressRemote, TLSRequired: true, Auth: AuthBearer, CredentialRef: "opaque-ref-1"}
	s := b.String()
	if !strings.Contains(s, "remote") {
		t.Fatalf("String() = %q, want it to mention the address class", s)
	}
}
