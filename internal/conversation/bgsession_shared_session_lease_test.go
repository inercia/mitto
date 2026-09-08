package conversation

// bgsession_shared_session_lease_test.go proves the mitto-lrt.18 lease-bind
// wiring added to BackgroundSession.completeDeferredHandshake (the wrapper in
// bgsession_shared_session.go, distinct from sharedSessionHandshaker's own
// completeDeferredHandshake exercised via fakeHandshakeDeps in
// shared_session_handshaker_test.go): once the deferred handshake has
// resolved bs.acpID, a bound BackendLease must be Bind()-ed to the resulting
// agentbackend.SessionRef so later Detach/Reconnect target the real session.
//
// bs.sharedProcess is left nil in these tests, which makes the underlying
// sharedSessionHandshaker.completeDeferredHandshake a documented no-op (see
// its "d.hsGetSharedProcess() == nil || !pending" short-circuit) — exactly
// the idempotent re-bind case the doc comment on the wrapper describes ("a
// no-op re-bind when completeDeferredHandshake itself was a no-op because the
// handshake had already completed on an earlier call"). This isolates the
// NEW Bind-wiring logic from the handshake RPC machinery, which is already
// covered elsewhere.

import (
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

// recordingBackendLease is a spyBackendLease variant that records every Bind
// call's argument, used to assert the exact SessionRef passed.
type recordingBackendLease struct {
	spyBackendLease
	bindCalls []agentbackend.SessionRef
}

func (l *recordingBackendLease) Bind(ref agentbackend.SessionRef) {
	l.bindCalls = append(l.bindCalls, ref)
}

var _ BackendLease = (*recordingBackendLease)(nil)

// TestBackgroundSession_CompleteDeferredHandshake_BindsLeaseToResolvedACPID
// proves that once bs.acpID is resolved, completeDeferredHandshake binds the
// lease with ConversationID=bs.persistedID and
// ProviderSession=bs.acpID.
func TestBackgroundSession_CompleteDeferredHandshake_BindsLeaseToResolvedACPID(t *testing.T) {
	lease := &recordingBackendLease{}
	bs := &BackgroundSession{
		persistedID: "conv-1",
		acpID:       "acp-sess-1",
		lease:       lease,
	}

	if err := bs.completeDeferredHandshake(); err != nil {
		t.Fatalf("completeDeferredHandshake: %v", err)
	}

	if len(lease.bindCalls) != 1 {
		t.Fatalf("Bind() calls = %d, want 1", len(lease.bindCalls))
	}
	want := agentbackend.SessionRef{ConversationID: "conv-1", ProviderSession: "acp-sess-1"}
	if got := lease.bindCalls[0]; got != want {
		t.Errorf("Bind() arg = %+v, want %+v", got, want)
	}
}

// TestBackgroundSession_CompleteDeferredHandshake_EmptyACPID_DoesNotBind
// proves Bind is never called with an empty ProviderSession — if the
// handshake has not (yet) resolved bs.acpID, there is nothing to bind.
func TestBackgroundSession_CompleteDeferredHandshake_EmptyACPID_DoesNotBind(t *testing.T) {
	lease := &recordingBackendLease{}
	bs := &BackgroundSession{
		persistedID: "conv-1",
		acpID:       "",
		lease:       lease,
	}

	if err := bs.completeDeferredHandshake(); err != nil {
		t.Fatalf("completeDeferredHandshake: %v", err)
	}

	if len(lease.bindCalls) != 0 {
		t.Errorf("Bind() calls = %d, want 0 (acpID empty, nothing to bind)", len(lease.bindCalls))
	}
}

// TestBackgroundSession_CompleteDeferredHandshake_NilLease_NoPanic proves the
// nil-provider fallback stays byte-identical: with no BackendProvider
// injected (bs.lease == nil, the common case today), completeDeferredHandshake
// must not panic even once bs.acpID is resolved.
func TestBackgroundSession_CompleteDeferredHandshake_NilLease_NoPanic(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "conv-1",
		acpID:       "acp-sess-1",
		lease:       nil,
	}

	if err := bs.completeDeferredHandshake(); err != nil {
		t.Fatalf("completeDeferredHandshake: %v", err)
	}
}
