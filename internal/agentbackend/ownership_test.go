package agentbackend

import (
	"context"
	"errors"
	"testing"
)

func TestRejectIfHostOwned_NilOwnerIsLocal(t *testing.T) {
	if err := RejectIfHostOwned(nil, SessionRef{ConversationID: "c"}, FeatureFiles); err != nil {
		t.Fatalf("RejectIfHostOwned(nil owner) = %v, want nil (assume local)", err)
	}
}

func TestRejectIfHostOwned_HostOwnedRejectsWithUnsupported(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	sess, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	got := RejectIfHostOwned(h, sess.Ref(), FeatureTerminals)
	var unsupported *UnsupportedError
	if !errors.As(got, &unsupported) || unsupported.Feature != FeatureTerminals {
		t.Fatalf("RejectIfHostOwned(host-owned) = %v, want *UnsupportedError{Feature: terminals}", got)
	}
}

// localOwner is a minimal ResourceOwner reporting OwnershipLocal, used to
// prove RejectIfHostOwned lets local-owned sessions through unchanged.
type localOwner struct{}

func (localOwner) Ownership(ref SessionRef) ResourceOwnership { return OwnershipLocal }

func TestRejectIfHostOwned_LocalOwnedPasses(t *testing.T) {
	if err := RejectIfHostOwned(localOwner{}, SessionRef{ConversationID: "c"}, FeatureFiles); err != nil {
		t.Fatalf("RejectIfHostOwned(local-owned) = %v, want nil", err)
	}
}

func TestResourceOwnership_String(t *testing.T) {
	if got := OwnershipLocal.String(); got != "local" {
		t.Fatalf("OwnershipLocal.String() = %q, want %q", got, "local")
	}
	if got := OwnershipHost.String(); got != "host" {
		t.Fatalf("OwnershipHost.String() = %q, want %q", got, "host")
	}
}
