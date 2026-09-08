package conversation

// bgsession_acp_process_test.go proves the mitto-lrt.18 Detach routing in
// killACPProcess's shared-mode branch: teardown must prefer a bound
// BackendLease's Detach() over unregistering from SharedProcess directly, and
// must fall back to the direct call byte-identically when no BackendProvider
// was injected (bs.lease == nil).

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// spyBackendLease is a minimal BackendLease test double that records Detach
// calls. killACPProcess only ever calls Detach on the lease, so every other
// method is a documented no-op / zero value.
type spyBackendLease struct {
	detachCalls int
}

func (l *spyBackendLease) Ref() agentbackend.SessionRef            { return agentbackend.SessionRef{} }
func (l *spyBackendLease) State() agentbackend.LifecycleState      { return agentbackend.LifecycleConnected }
func (l *spyBackendLease) Capabilities() agentbackend.Capabilities { return nil }
func (l *spyBackendLease) Detach()                                 { l.detachCalls++ }
func (l *spyBackendLease) Bind(agentbackend.SessionRef)            {}
func (l *spyBackendLease) Reconnect(context.Context) error         { return nil }
func (l *spyBackendLease) Terminate(context.Context) error         { return nil }
func (l *spyBackendLease) LocalProcess() (SharedProcess, bool)     { return nil, false }
func (l *spyBackendLease) SessionHandle() (*SessionHandle, bool)   { return nil, false }

var _ BackendLease = (*spyBackendLease)(nil)

// TestBackgroundSession_KillACPProcess_SharedMode_PrefersLeaseDetach proves
// that when a BackendLease is present, shared-mode teardown routes through
// lease.Detach() instead of calling sharedProcess.UnregisterSession directly.
func TestBackgroundSession_KillACPProcess_SharedMode_PrefersLeaseDetach(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	lease := &spyBackendLease{}
	bs := &BackgroundSession{
		sharedProcess: proc,
		lease:         lease,
		acpID:         "acp-sess-1",
	}

	bs.killACPProcess()

	if lease.detachCalls != 1 {
		t.Errorf("lease.Detach() calls = %d, want 1", lease.detachCalls)
	}
	if len(proc.unregistered) != 0 {
		t.Errorf("sharedProcess.UnregisterSession calls = %v, want none (must route through the lease, not the direct call)", proc.unregistered)
	}
}

// TestBackgroundSession_KillACPProcess_SharedMode_NilLease_FallsBackToDirectUnregister
// proves the nil-provider fallback stays byte-identical: when no
// BackendProvider was injected (bs.lease == nil), teardown still unregisters
// the session directly from the SharedProcess exactly as before mitto-lrt.18.
func TestBackgroundSession_KillACPProcess_SharedMode_NilLease_FallsBackToDirectUnregister(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	bs := &BackgroundSession{
		sharedProcess: proc,
		lease:         nil,
		acpID:         "acp-sess-1",
	}

	bs.killACPProcess()

	if len(proc.unregistered) != 1 || proc.unregistered[0] != acp.SessionId("acp-sess-1") {
		t.Fatalf("unregistered = %v, want exactly [acp-sess-1]", proc.unregistered)
	}
}

// TestBackgroundSession_KillACPProcess_SharedMode_BoundLeaseUnbound_DetachIsSafe
// proves a bound-but-empty acpID case never reaches the direct fallback: even
// if bs.acpID happens to be empty while bs.lease is non-nil, teardown must
// still prefer lease.Detach() (a documented safe no-op on an unbound lease
// per acpLease.Detach) rather than skip cleanup entirely.
func TestBackgroundSession_KillACPProcess_SharedMode_BoundLeaseUnbound_DetachIsSafe(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	lease := &spyBackendLease{}
	bs := &BackgroundSession{
		sharedProcess: proc,
		lease:         lease,
		acpID:         "",
	}

	bs.killACPProcess()

	if lease.detachCalls != 1 {
		t.Errorf("lease.Detach() calls = %d, want 1", lease.detachCalls)
	}
	if len(proc.unregistered) != 0 {
		t.Errorf("sharedProcess.UnregisterSession calls = %v, want none", proc.unregistered)
	}
}
