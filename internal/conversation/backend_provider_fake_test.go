package conversation

// backend_provider_fake_test.go proves the BackendProvider/BackendLease seam
// (backend_provider.go) works for a non-ACP backend with NO process, PID,
// runner, or restart method — built on agentbackend.NewFakeHost — satisfying
// the mitto-lrt.7 acceptance criterion: "Fake remote-backend integration
// proves create/attach/detach/resume without a process, PID, runner or
// restart method. Concurrent detach of one conversation does not affect
// siblings or external-owned sessions. Reconnect is bounded and
// single-flight."

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/agentbackend"
)

// fakeRemoteBackendProvider adapts an agentbackend.SessionOps host (the
// in-memory fake) to BackendProvider. Unlike acpBackendProvider it never
// touches a process, PID, or runner — proving the ownership model needs none.
type fakeRemoteBackendProvider struct {
	ops agentbackend.SessionOps
}

func (p *fakeRemoteBackendProvider) AcquireSession(ctx context.Context, req AcquireRequest) (BackendLease, error) {
	var (
		sess agentbackend.Session
		err  error
	)
	switch req.Intent {
	case IntentNew:
		sess, err = p.ops.NewSession(ctx, req.Agent.Provider)
	case IntentLoad:
		sess, err = p.ops.LoadSession(ctx, req.Session)
	case IntentResume:
		sess, err = p.ops.ResumeSession(ctx, req.Session)
	default:
		return nil, fmt.Errorf("fakeRemoteBackendProvider: unknown intent %d", req.Intent)
	}
	if err != nil {
		return nil, err
	}
	return &fakeRemoteLease{ops: p.ops, session: sess}, nil
}

// fakeRemoteLease implements BackendLease with no process/PID/runner at all.
type fakeRemoteLease struct {
	ops agentbackend.SessionOps

	mu       sync.Mutex
	session  agentbackend.Session
	detached bool

	reconnectMu       sync.Mutex
	reconnectInFlight bool
	reconnectDone     chan struct{}
	reconnectErr      error
}

func (l *fakeRemoteLease) Ref() agentbackend.SessionRef {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.session.Ref()
}

func (l *fakeRemoteLease) State() agentbackend.LifecycleState {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.detached {
		return agentbackend.LifecycleDisconnected
	}
	return agentbackend.LifecycleConnected
}

func (l *fakeRemoteLease) Capabilities() agentbackend.Capabilities {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.session.Capabilities()
}

// Detach marks this lease disconnected. There is no process/PID/OS resource
// to release — proving the ownership model works without one — and it never
// touches the host's other sessions (siblings, external-owned or otherwise).
func (l *fakeRemoteLease) Detach() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.detached = true
}

// Terminate is unsupported for the fake remote backend: it has no local
// process/restart concept, so it must fail closed with a typed
// *agentbackend.UnsupportedError rather than faking success.
func (l *fakeRemoteLease) Terminate(ctx context.Context) error {
	return &agentbackend.UnsupportedError{Feature: agentbackend.Feature("terminate")}
}

// Reconnect coalesces concurrent callers onto a single ResumeSession RPC
// (single-flight), mirroring acpLease.Reconnect.
func (l *fakeRemoteLease) Reconnect(ctx context.Context) error {
	l.reconnectMu.Lock()
	if l.reconnectInFlight {
		done := l.reconnectDone
		l.reconnectMu.Unlock()
		select {
		case <-done:
			l.reconnectMu.Lock()
			err := l.reconnectErr
			l.reconnectMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	l.reconnectInFlight = true
	done := make(chan struct{})
	l.reconnectDone = done
	ref := l.Ref()
	l.reconnectMu.Unlock()

	sess, err := l.ops.ResumeSession(ctx, ref)

	l.reconnectMu.Lock()
	l.reconnectErr = err
	l.reconnectInFlight = false
	close(done)
	l.reconnectMu.Unlock()

	if err == nil {
		l.mu.Lock()
		l.session = sess
		l.detached = false
		l.mu.Unlock()
	}
	return err
}

// LocalProcess always returns (nil, false): the fake remote backend has no
// process at all.
func (l *fakeRemoteLease) LocalProcess() (SharedProcess, bool) { return nil, false }

// SessionHandle always returns (nil, false): the fake remote backend has no
// ACP session handle at all.
func (l *fakeRemoteLease) SessionHandle() (*SessionHandle, bool) { return nil, false }

var (
	_ BackendProvider = (*fakeRemoteBackendProvider)(nil)
	_ BackendLease    = (*fakeRemoteLease)(nil)
)

// TestFakeRemoteBackendProvider_CreateAttachDetachResume_NoProcess proves
// create/attach(load)/detach/resume all work on a backend with no process,
// PID, runner, or restart method.
func TestFakeRemoteBackendProvider_CreateAttachDetachResume_NoProcess(t *testing.T) {
	host := agentbackend.NewFakeHost("fake-provider")
	provider := &fakeRemoteBackendProvider{ops: host}
	agent := agentbackend.AgentRef{Backend: "fake", Provider: "fake-provider"}

	// Create.
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession(New): %v", err)
	}
	if _, ok := lease.LocalProcess(); ok {
		t.Error("LocalProcess() ok = true, want false (no process on the fake backend)")
	}
	if _, ok := lease.SessionHandle(); ok {
		t.Error("SessionHandle() ok = true, want false (no ACP session handle on the fake backend)")
	}
	ref := lease.Ref()
	if ref.ConversationID == "" || ref.ProviderSession == "" {
		t.Fatalf("Ref() = %+v, want populated identity", ref)
	}

	// Attach (a second client loads the same session by ref).
	attached, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Session: ref, Intent: IntentLoad})
	if err != nil {
		t.Fatalf("AcquireSession(Load): %v", err)
	}
	if attached.Ref() != ref {
		t.Fatalf("attached.Ref() = %+v, want %+v", attached.Ref(), ref)
	}

	// Detach one of the two owners.
	lease.Detach()
	if got := lease.State(); got != agentbackend.LifecycleDisconnected {
		t.Errorf("lease.State() after Detach = %v, want Disconnected", got)
	}

	// Resume still works (the fake host never deletes a session on detach).
	resumed, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Session: ref, Intent: IntentResume})
	if err != nil {
		t.Fatalf("AcquireSession(Resume): %v", err)
	}
	if resumed.Ref() != ref {
		t.Fatalf("resumed.Ref() = %+v, want %+v", resumed.Ref(), ref)
	}
}

// TestFakeRemoteBackendProvider_ConcurrentDetach_DoesNotAffectSiblings proves
// concurrently detaching one conversation's lease never disturbs a sibling
// lease sharing the same host, nor a third, external-owned session this test
// never acquires a lease for at all.
func TestFakeRemoteBackendProvider_ConcurrentDetach_DoesNotAffectSiblings(t *testing.T) {
	host := agentbackend.NewFakeHost("fake-provider")
	provider := &fakeRemoteBackendProvider{ops: host}
	agent := agentbackend.AgentRef{Backend: "fake", Provider: "fake-provider"}

	mine, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession(mine): %v", err)
	}
	sibling, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession(sibling): %v", err)
	}
	external, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession(external): %v", err) // never touched again below
	}
	externalRef := external.Ref()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var siblingErrs int64

	// Continuously exercise the sibling lease while `mine` is detached
	// repeatedly and concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if sibling.State() != agentbackend.LifecycleConnected {
				atomic.AddInt64(&siblingErrs, 1)
			}
			if sibling.Capabilities() == nil {
				atomic.AddInt64(&siblingErrs, 1)
			}
		}
	}()

	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			mine.Detach()
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	if siblingErrs != 0 {
		t.Errorf("sibling observed %d inconsistencies during concurrent detach of `mine`", siblingErrs)
	}
	if got := mine.State(); got != agentbackend.LifecycleDisconnected {
		t.Errorf("mine.State() = %v, want Disconnected", got)
	}
	if got := sibling.State(); got != agentbackend.LifecycleConnected {
		t.Errorf("sibling.State() = %v, want Connected (untouched)", got)
	}

	// The external-owned session was never detached and remains loadable.
	if _, err := host.LoadSession(context.Background(), externalRef); err != nil {
		t.Errorf("external session became unloadable after sibling's detach churn: %v", err)
	}
}

// gatingSessionOps wraps a real agentbackend.SessionOps, blocking
// ResumeSession on gate and counting calls, so concurrent Reconnect callers
// can be forced to overlap deterministically regardless of scheduling.
type gatingSessionOps struct {
	agentbackend.SessionOps
	gate  chan struct{}
	calls *int64
}

func (g *gatingSessionOps) ResumeSession(ctx context.Context, ref agentbackend.SessionRef) (agentbackend.Session, error) {
	atomic.AddInt64(g.calls, 1)
	<-g.gate
	return g.SessionOps.ResumeSession(ctx, ref)
}

// TestFakeRemoteBackendProvider_Reconnect_SingleFlight proves concurrent
// Reconnect callers on the same lease coalesce into exactly ONE ResumeSession
// call, so a lost connection is never resumed twice.
func TestFakeRemoteBackendProvider_Reconnect_SingleFlight(t *testing.T) {
	host := agentbackend.NewFakeHost("fake-provider")
	provider := &fakeRemoteBackendProvider{ops: host}
	agent := agentbackend.AgentRef{Backend: "fake", Provider: "fake-provider"}

	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	fl, ok := lease.(*fakeRemoteLease)
	if !ok {
		t.Fatalf("lease is %T, want *fakeRemoteLease", lease)
	}

	gate := make(chan struct{})
	var calls int64
	fl.ops = &gatingSessionOps{SessionOps: fl.ops, gate: gate, calls: &calls}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = lease.Reconnect(context.Background())
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Errorf("Reconnect[%d] error = %v, want nil", i, e)
		}
	}
	if calls != 1 {
		t.Fatalf("ResumeSession calls = %d, want exactly 1 (single-flight)", calls)
	}
}

// TestFakeRemoteBackendProvider_AcquireSession_MissingSession_NoFallbackToNewSession
// proves the mitto-lrt.7 acceptance criterion "missing-session ... cases
// surface actionable states instead of duplicating work" on the non-process
// fake backend: Load/Resume against a session ref that was never created
// returns ErrSessionNotFound (classifiable to an actionable state) and never
// silently creates a replacement session under that ref.
func TestFakeRemoteBackendProvider_AcquireSession_MissingSession_NoFallbackToNewSession(t *testing.T) {
	host := agentbackend.NewFakeHost("fake-provider")
	provider := &fakeRemoteBackendProvider{ops: host}
	agent := agentbackend.AgentRef{Backend: "fake", Provider: "fake-provider"}
	unknownRef := agentbackend.SessionRef{ConversationID: "conv-never-created", Provider: "fake-provider", ProviderSession: "sess-never-created"}

	tests := []struct {
		name   string
		intent Intent
	}{
		{"load", IntentLoad},
		{"resume", IntentResume},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Session: unknownRef, Intent: tt.intent})
			if lease != nil {
				t.Fatalf("AcquireSession lease = %v, want nil for a missing session", lease)
			}
			if !errors.Is(err, agentbackend.ErrSessionNotFound) {
				t.Fatalf("AcquireSession error = %v, want ErrSessionNotFound", err)
			}
			if state, _ := ClassifyAcquireError(err); state != agentbackend.LifecycleDisconnected {
				t.Errorf("ClassifyAcquireError state = %v, want LifecycleDisconnected (actionable missing-session state)", state)
			}
			// No fallback: the ref must still be unloadable afterward — no
			// replacement session was silently created under it.
			if _, err := host.LoadSession(context.Background(), unknownRef); !errors.Is(err, agentbackend.ErrSessionNotFound) {
				t.Errorf("LoadSession after failed acquire = %v, want ErrSessionNotFound (no silent creation)", err)
			}
		})
	}
}

// TestFakeRemoteBackendProvider_Reconnect_WaiterContextCancelled_DoesNotStartSecondAttempt
// proves the mitto-lrt.7 acceptance criterion "uncertain-delivery cases
// surface actionable states instead of duplicating work" on the non-process
// fake backend: a caller whose context is cancelled while WAITING on another
// caller's in-flight Reconnect gets back an actionable ctx error but never
// triggers a second, duplicate ResumeSession call.
func TestFakeRemoteBackendProvider_Reconnect_WaiterContextCancelled_DoesNotStartSecondAttempt(t *testing.T) {
	host := agentbackend.NewFakeHost("fake-provider")
	provider := &fakeRemoteBackendProvider{ops: host}
	agent := agentbackend.AgentRef{Backend: "fake", Provider: "fake-provider"}

	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Agent: agent, Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	fl, ok := lease.(*fakeRemoteLease)
	if !ok {
		t.Fatalf("lease is %T, want *fakeRemoteLease", lease)
	}

	gate := make(chan struct{})
	var calls int64
	fl.ops = &gatingSessionOps{SessionOps: fl.ops, gate: gate, calls: &calls}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- lease.Reconnect(context.Background())
	}()
	time.Sleep(20 * time.Millisecond) // ensure the first caller is in-flight

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if waiterErr := lease.Reconnect(ctx); !errors.Is(waiterErr, context.DeadlineExceeded) {
		t.Fatalf("waiter Reconnect error = %v, want context.DeadlineExceeded", waiterErr)
	}

	close(gate)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Reconnect error = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("ResumeSession calls = %d, want exactly 1 (a timed-out waiter must not start a second attempt)", calls)
	}
}
