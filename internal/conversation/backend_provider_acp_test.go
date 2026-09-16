package conversation

// backend_provider_acp_test.go exercises acpBackendProvider/acpLease against
// dedicated fakes (kept local to this file rather than reusing the broader
// fakeSharedProcess/fakeProcessManager test doubles so their configurable
// ResumeSession/UnregisterSession/Restart recording cannot affect unrelated
// tests) — proving the ACP-backed seam delegates to ProcessManager/
// SharedProcess identically to today's direct callers (mitto-lrt.7).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// fakeBackendSharedProcess is a dedicated, fully-controllable SharedProcess
// fake for the tests in this file.
type fakeBackendSharedProcess struct {
	mu sync.Mutex

	caps        *acp.AgentCapabilities
	processDone chan struct{}

	newHandle, loadHandle, resumeHandle *SessionHandle
	newErr, loadErr, resumeErr          error
	newCalls                            int

	unregistered []acp.SessionId
	generation   int
	restarted    []int

	// mitto-mx9.1: recorded/configurable Prompt/Cancel/SetSessionMode/
	// SetSessionModel calls, used by the SessionPromptOps seam tests below to
	// prove acpSessionPromptOps delegates to SharedProcess with the exact
	// session ID/content/value it was given, and propagates responses/errors
	// unchanged (modulo neutral translation).
	promptCalls   []fakeBackendPromptCall
	promptResp    acp.PromptResponse
	promptErr     error
	cancelCalls   []acp.SessionId
	cancelErr     error
	setModeCalls  []fakeBackendSetCall
	setModeErr    error
	setModelCalls []fakeBackendSetCall
	setModelErr   error
}

// fakeBackendPromptCall records one Prompt() invocation.
type fakeBackendPromptCall struct {
	sessionID acp.SessionId
	blocks    []acp.ContentBlock
}

// fakeBackendSetCall records one SetSessionMode/SetSessionModel invocation.
type fakeBackendSetCall struct {
	sessionID acp.SessionId
	value     string
}

func newFakeBackendSharedProcess() *fakeBackendSharedProcess {
	return &fakeBackendSharedProcess{
		processDone: make(chan struct{}),
		caps:        &acp.AgentCapabilities{},
	}
}

func (f *fakeBackendSharedProcess) Capabilities() *acp.AgentCapabilities { return f.caps }
func (f *fakeBackendSharedProcess) ProcessDone() <-chan struct{}         { return f.processDone }
func (f *fakeBackendSharedProcess) NewSession(context.Context, string, []acp.McpServer) (*SessionHandle, error) {
	f.mu.Lock()
	f.newCalls++
	f.mu.Unlock()
	return f.newHandle, f.newErr
}
func (f *fakeBackendSharedProcess) LoadSession(context.Context, string, string, []acp.McpServer) (*SessionHandle, error) {
	return f.loadHandle, f.loadErr
}
func (f *fakeBackendSharedProcess) ResumeSession(context.Context, string, string, []acp.McpServer) (*SessionHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeHandle, f.resumeErr
}
func (f *fakeBackendSharedProcess) RegisterSession(acp.SessionId, *SessionCallbacks) {}
func (f *fakeBackendSharedProcess) UnregisterSession(id acp.SessionId) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregistered = append(f.unregistered, id)
}
func (f *fakeBackendSharedProcess) Cancel(_ context.Context, id acp.SessionId) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCalls = append(f.cancelCalls, id)
	return f.cancelErr
}
func (f *fakeBackendSharedProcess) SetSessionMode(_ context.Context, id acp.SessionId, mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setModeCalls = append(f.setModeCalls, fakeBackendSetCall{sessionID: id, value: mode})
	return f.setModeErr
}
func (f *fakeBackendSharedProcess) SetSessionModel(_ context.Context, id acp.SessionId, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setModelCalls = append(f.setModelCalls, fakeBackendSetCall{sessionID: id, value: model})
	return f.setModelErr
}
func (f *fakeBackendSharedProcess) Done() <-chan struct{} { return f.processDone }
func (f *fakeBackendSharedProcess) Prompt(_ context.Context, id acp.SessionId, blocks []acp.ContentBlock) (acp.PromptResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptCalls = append(f.promptCalls, fakeBackendPromptCall{sessionID: id, blocks: blocks})
	return f.promptResp, f.promptErr
}
func (f *fakeBackendSharedProcess) Generation() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generation
}
func (f *fakeBackendSharedProcess) Restart(observedGen int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = append(f.restarted, observedGen)
	f.generation++
	return nil
}
func (f *fakeBackendSharedProcess) RecommendedLoadTimeout(bool) time.Duration { return 0 }
func (f *fakeBackendSharedProcess) MCPInitDone() bool                         { return true }
func (f *fakeBackendSharedProcess) WaitForMCPInit(context.Context) bool       { return true }

// fakeBackendProcessManager returns a preset SharedProcess/error, ignoring
// its other ProcessManager methods (no-ops).
type fakeBackendProcessManager struct {
	process SharedProcess
	err     error
}

func (f *fakeBackendProcessManager) GetOrCreateProcess(*config.WorkspaceSettings, string, string, map[string]string, *runner.Runner, bool) (SharedProcess, error) {
	return f.process, f.err
}
func (f *fakeBackendProcessManager) EnsurePrewarmed(string, *slog.Logger) {}
func (f *fakeBackendProcessManager) ClearGCSuspended(string)              {}
func (f *fakeBackendProcessManager) IsGCSuspended(string) bool            { return false }
func (f *fakeBackendProcessManager) StopGC()                              {}
func (f *fakeBackendProcessManager) Close()                               {}
func (f *fakeBackendProcessManager) ProcessCount() int                    { return 1 }
func (f *fakeBackendProcessManager) ColdProcessCount() int                { return 0 }
func (f *fakeBackendProcessManager) PinWorkspace(string, string, time.Duration, int) bool {
	return true
}
func (f *fakeBackendProcessManager) HasLiveProcess(string) bool { return true }

// TestACPBackendProvider_AcquireSession_IntentsDelegateToProcess proves each
// Intent issues exactly the RPC today's direct callers issue (NewSession/
// LoadSession/ResumeSession), and the returned lease exposes the resulting
// process+handle unchanged via the ACP-only escape hatches.
func TestACPBackendProvider_AcquireSession_IntentsDelegateToProcess(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
		handle *SessionHandle
	}{
		{"new", IntentNew, &SessionHandle{SessionID: "sess-new"}},
		{"load", IntentLoad, &SessionHandle{SessionID: "sess-load"}},
		{"resume", IntentResume, &SessionHandle{SessionID: "sess-resume"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newFakeBackendSharedProcess()
			switch tt.intent {
			case IntentNew:
				proc.newHandle = tt.handle
			case IntentLoad:
				proc.loadHandle = tt.handle
			case IntentResume:
				proc.resumeHandle = tt.handle
			}
			provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})

			lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Intent: tt.intent})
			if err != nil {
				t.Fatalf("AcquireSession: unexpected error: %v", err)
			}
			gotProc, ok := lease.LocalProcess()
			if !ok || gotProc != proc {
				t.Fatalf("LocalProcess() = (%v, %v), want (%v, true)", gotProc, ok, proc)
			}
			gotHandle, ok := lease.SessionHandle()
			if !ok || gotHandle != tt.handle {
				t.Fatalf("SessionHandle() = (%v, %v), want (%v, true)", gotHandle, ok, tt.handle)
			}
			if got := string(lease.Ref().ProviderSession); got != tt.handle.SessionID {
				t.Errorf("Ref().ProviderSession = %q, want %q", got, tt.handle.SessionID)
			}
		})
	}
}

// TestACPBackendProvider_AcquireSession_NoProcessManager_ReturnsErrNotConnected
// proves a provider wrapping a nil ProcessManager fails closed rather than
// panicking or faking success.
func TestACPBackendProvider_AcquireSession_NoProcessManager_ReturnsErrNotConnected(t *testing.T) {
	provider := NewACPBackendProvider(nil)
	if _, err := provider.AcquireSession(context.Background(), AcquireRequest{}); !errors.Is(err, agentbackend.ErrNotConnected) {
		t.Fatalf("AcquireSession error = %v, want ErrNotConnected", err)
	}
}

// TestACPLease_Detach_UnregistersSessionWithoutKillingProcess proves Detach
// only unregisters this session from the multiplex layer — it must never
// kill the shared OS process, which other sessions may still own.
func TestACPLease_Detach_UnregistersSessionWithoutKillingProcess(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	proc.newHandle = &SessionHandle{SessionID: "sess-1"}
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}

	lease.Detach()

	if len(proc.unregistered) != 1 || proc.unregistered[0] != acp.SessionId("sess-1") {
		t.Fatalf("unregistered = %v, want [sess-1]", proc.unregistered)
	}
	select {
	case <-proc.ProcessDone():
		t.Fatal("Detach must not close ProcessDone (must not kill the shared process)")
	default:
	}
	if got := lease.State(); got != agentbackend.LifecycleDisconnected {
		t.Errorf("State() after Detach = %v, want LifecycleDisconnected", got)
	}
}

// TestACPLease_Terminate_RestartsWithCurrentGeneration proves Terminate maps
// to the existing generation-fenced Restart.
func TestACPLease_Terminate_RestartsWithCurrentGeneration(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	proc.newHandle = &SessionHandle{SessionID: "sess-1"}
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}

	if err := lease.Terminate(context.Background()); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if len(proc.restarted) != 1 || proc.restarted[0] != 0 {
		t.Fatalf("restarted = %v, want [0]", proc.restarted)
	}
}

// TestACPLease_Reconnect_SingleFlight_CoalescesConcurrentCallers proves
// concurrent Reconnect callers on the same lease coalesce into ONE
// ResumeSession RPC rather than duplicating work or replaying a possibly
// already-accepted prompt.
func TestACPLease_Reconnect_SingleFlight_CoalescesConcurrentCallers(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	proc.newHandle = &SessionHandle{SessionID: "sess-1"}
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}

	al, ok := lease.(*acpLease)
	if !ok {
		t.Fatalf("lease is %T, want *acpLease", lease)
	}

	// Gate ResumeSession so all goroutines are guaranteed to overlap.
	gate := make(chan struct{})
	var calls int
	origHandle := &SessionHandle{SessionID: "sess-1-resumed"}
	al.process = &gatingSharedProcess{fakeBackendSharedProcess: proc, gate: gate, calls: &calls, handle: origHandle}

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
	// Let every goroutine reach the gate before releasing it.
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
	if got, _ := lease.SessionHandle(); got != origHandle {
		t.Errorf("SessionHandle() after Reconnect = %v, want %v", got, origHandle)
	}
}

// gatingSharedProcess wraps fakeBackendSharedProcess, blocking ResumeSession
// on gate and counting calls, to deterministically force concurrent
// Reconnect callers to overlap.
type gatingSharedProcess struct {
	*fakeBackendSharedProcess
	gate   chan struct{}
	calls  *int
	handle *SessionHandle
}

func (g *gatingSharedProcess) ResumeSession(ctx context.Context, id, cwd string, servers []acp.McpServer) (*SessionHandle, error) {
	g.mu.Lock()
	*g.calls++
	g.mu.Unlock()
	<-g.gate
	return g.handle, nil
}

// TestACPCapabilities_Query proves the ACP capabilities adapter answers only
// what is directly knowable from AgentCapabilities/SessionHandle, reporting
// CapabilityUnknown rather than guessing otherwise.
func TestACPCapabilities_Query(t *testing.T) {
	caps := &acpCapabilities{
		agentCaps: &acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: true}},
		handle:    &SessionHandle{Models: &SessionModelState{}},
	}
	if got := caps.Query(agentbackend.FeatureImages); got != agentbackend.CapabilitySupported {
		t.Errorf("Query(FeatureImages) = %v, want Supported", got)
	}
	if got := caps.Query(agentbackend.FeatureModelSelection); got != agentbackend.CapabilitySupported {
		t.Errorf("Query(FeatureModelSelection) = %v, want Supported", got)
	}
	if got := caps.Query(agentbackend.FeatureModeSelection); got != agentbackend.CapabilityUnknown {
		t.Errorf("Query(FeatureModeSelection) = %v, want Unknown (handle.Modes is nil)", got)
	}
	if got := caps.Query(agentbackend.FeatureTerminals); got != agentbackend.CapabilityUnknown {
		t.Errorf("Query(FeatureTerminals) = %v, want Unknown", got)
	}
}

// TestACPBackendProvider_AcquireSession_MissingSession_NoFallbackToNewSession
// proves the mitto-lrt.7 acceptance criterion "missing-session ... cases
// surface actionable states instead of duplicating work": when
// LoadSession/ResumeSession fails (e.g. the upstream session is gone), the
// error is surfaced unchanged (classifiable to an actionable state) and the
// provider never silently falls back to creating a replacement session.
func TestACPBackendProvider_AcquireSession_MissingSession_NoFallbackToNewSession(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
	}{
		{"load", IntentLoad},
		{"resume", IntentResume},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newFakeBackendSharedProcess()
			wantErr := fmt.Errorf("upstream session gone: %w", agentbackend.ErrSessionNotFound)
			switch tt.intent {
			case IntentLoad:
				proc.loadErr = wantErr
			case IntentResume:
				proc.resumeErr = wantErr
			}
			provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})

			lease, err := provider.AcquireSession(context.Background(), AcquireRequest{
				Intent:  tt.intent,
				Session: agentbackend.SessionRef{ProviderSession: "missing-session"},
			})
			if lease != nil {
				t.Fatalf("AcquireSession lease = %v, want nil on a missing-session error", lease)
			}
			if !errors.Is(err, agentbackend.ErrSessionNotFound) {
				t.Fatalf("AcquireSession error = %v, want to wrap ErrSessionNotFound", err)
			}
			if proc.newCalls != 0 {
				t.Errorf("NewSession calls = %d, want 0 (must not silently create a replacement session)", proc.newCalls)
			}
			if state, _ := ClassifyAcquireError(err); state != agentbackend.LifecycleDisconnected {
				t.Errorf("ClassifyAcquireError state = %v, want LifecycleDisconnected (actionable missing-session state)", state)
			}
		})
	}
}

// TestACPBackendProvider_AcquireSession_DeferSession_SkipsRPCReturnsProcessOnlyLease
// proves the mitto-lrt.16 AcquireRequest.DeferSession contract: the provider
// still gets/creates the shared process, but skips the NewSession/
// LoadSession/ResumeSession RPC entirely, returning a lease whose
// LocalProcess() exposes the acquired process and whose SessionHandle() is
// (nil, false) until a caller performs its own deferred handshake.
func TestACPBackendProvider_AcquireSession_DeferSession_SkipsRPCReturnsProcessOnlyLease(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
	}{
		{"new", IntentNew},
		{"load", IntentLoad},
		{"resume", IntentResume},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newFakeBackendSharedProcess()
			// Pre-set every handle/err so a wrongly-issued RPC would be
			// immediately observable via newCalls or a returned handle.
			proc.newHandle = &SessionHandle{SessionID: "should-not-be-returned"}
			proc.loadHandle = &SessionHandle{SessionID: "should-not-be-returned"}
			proc.resumeHandle = &SessionHandle{SessionID: "should-not-be-returned"}
			provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})

			lease, err := provider.AcquireSession(context.Background(), AcquireRequest{
				Intent:       tt.intent,
				Session:      agentbackend.SessionRef{ProviderSession: "existing-session"},
				DeferSession: true,
			})
			if err != nil {
				t.Fatalf("AcquireSession: unexpected error: %v", err)
			}
			if proc.newCalls != 0 {
				t.Errorf("NewSession calls = %d, want 0 (DeferSession must skip the RPC)", proc.newCalls)
			}
			gotProc, ok := lease.LocalProcess()
			if !ok || gotProc != proc {
				t.Fatalf("LocalProcess() = (%v, %v), want (%v, true)", gotProc, ok, proc)
			}
			if handle, ok := lease.SessionHandle(); ok || handle != nil {
				t.Errorf("SessionHandle() = (%v, %v), want (nil, false) — RPC must not have run", handle, ok)
			}
		})
	}
}

// TestACPLease_Detach_DeferSessionNeverBound_NoOpsSafely proves that
// detaching a DeferSession lease that was never bound to a real ACP session
// ID (sessionID still "") is a safe no-op — it must not call
// UnregisterSession with a bogus empty ID, and State() still reports
// Disconnected afterwards.
func TestACPLease_Detach_DeferSessionNeverBound_NoOpsSafely(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{
		Intent:       IntentNew,
		DeferSession: true,
	})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}

	lease.Detach()

	if len(proc.unregistered) != 0 {
		t.Fatalf("unregistered = %v, want none (unbound lease must not unregister a bogus empty session ID)", proc.unregistered)
	}
	if got := lease.State(); got != agentbackend.LifecycleDisconnected {
		t.Errorf("State() after Detach = %v, want LifecycleDisconnected", got)
	}
}

// TestACPLease_Bind_AttachesSessionIDEnablingDetach proves the mitto-lrt.18
// Bind seam: a DeferSession lease starts with sessionID empty (see
// TestACPLease_Detach_DeferSessionNeverBound_NoOpsSafely, where Detach is a
// safe no-op on such a lease). Once the caller completes its own deferred
// handshake and calls Bind with the resulting identity, Ref() reflects it and
// a subsequent Detach() must actually unregister that real session — proving
// Bind is not itself a no-op and genuinely changes Detach's target.
func TestACPLease_Bind_AttachesSessionIDEnablingDetach(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{
		Intent:       IntentNew,
		DeferSession: true,
	})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}

	ref := agentbackend.SessionRef{ConversationID: "conv-1", ProviderSession: "bound-sess-1"}
	lease.Bind(ref)

	if got := lease.Ref(); got != ref {
		t.Errorf("Ref() after Bind = %+v, want %+v", got, ref)
	}

	lease.Detach()

	if len(proc.unregistered) != 1 || proc.unregistered[0] != acp.SessionId("bound-sess-1") {
		t.Fatalf("unregistered = %v, want exactly [bound-sess-1] (Bind must attach the real session ID so Detach targets it, not no-op)", proc.unregistered)
	}
}

// TestACPLease_Reconnect_WaiterContextCancelled_DoesNotStartSecondAttempt
// proves the mitto-lrt.7 acceptance criterion "uncertain-delivery cases
// surface actionable states instead of duplicating work": a caller whose
// context is cancelled while WAITING on another caller's in-flight Reconnect
// gets back an actionable ctx error (it cannot know whether the shared
// attempt will ultimately succeed) but must never trigger a second,
// duplicate ResumeSession call — the in-flight attempt remains the single
// source of truth.
func TestACPLease_Reconnect_WaiterContextCancelled_DoesNotStartSecondAttempt(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	proc.newHandle = &SessionHandle{SessionID: "sess-1"}
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	al := lease.(*acpLease)

	gate := make(chan struct{})
	var calls int
	al.process = &gatingSharedProcess{fakeBackendSharedProcess: proc, gate: gate, calls: &calls, handle: &SessionHandle{SessionID: "sess-1-resumed"}}

	// First caller starts the in-flight attempt and blocks on gate.
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- lease.Reconnect(context.Background())
	}()
	time.Sleep(20 * time.Millisecond) // ensure the first caller is in-flight

	// A waiter whose context expires before the in-flight attempt completes
	// must surface that uncertainty to ITS caller without starting a second
	// ResumeSession call.
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

// TestACPLease_SessionOps_DeferSessionUnbound_ReturnsFalse proves the
// mitto-mx9.1 contract: a DeferSession lease that has not yet been Bind()-ed
// (sessionID still "") reports ok=false from SessionOps, so hot-path callers
// keep using their pre-existing LocalProcess()-based path instead of calling
// through a not-yet-established session.
func TestACPLease_SessionOps_DeferSessionUnbound_ReturnsFalse(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{
		Intent:       IntentNew,
		DeferSession: true,
	})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}

	ops, ref, ok := lease.SessionOps()
	if ok || ops != nil || ref != (agentbackend.SessionRef{}) {
		t.Fatalf("SessionOps() = (%v, %+v, %v), want (nil, {}, false) before Bind", ops, ref, ok)
	}
}

// TestACPLease_SessionOps_BoundAfterDefer_ReturnsWorkingOps proves that once
// a DeferSession lease is Bind()-ed to a real session identity, SessionOps
// flips to ok=true and returns ops that route to the right session ID.
func TestACPLease_SessionOps_BoundAfterDefer_ReturnsWorkingOps(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{
		Intent:       IntentNew,
		DeferSession: true,
	})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	if _, _, ok := lease.SessionOps(); ok {
		t.Fatalf("SessionOps() ok = true before Bind, want false")
	}

	ref := agentbackend.SessionRef{ConversationID: "conv-1", ProviderSession: "bound-sess-1"}
	lease.Bind(ref)

	ops, gotRef, ok := lease.SessionOps()
	if !ok || ops == nil {
		t.Fatalf("SessionOps() after Bind = (%v, _, %v), want (non-nil, true)", ops, ok)
	}
	if gotRef != ref {
		t.Errorf("SessionOps() ref = %+v, want %+v", gotRef, ref)
	}
	if err := ops.Cancel(context.Background(), gotRef); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(proc.cancelCalls) != 1 || proc.cancelCalls[0] != acp.SessionId("bound-sess-1") {
		t.Fatalf("cancelCalls = %v, want exactly [bound-sess-1]", proc.cancelCalls)
	}
}

// TestACPLease_SessionOps_NonDeferred_ImmediatelyBoundToHandleSessionID
// proves a non-deferred lease (the common AcquireSession path) exposes
// working SessionOps immediately, using the ACP session ID returned by
// NewSession/LoadSession/ResumeSession.
func TestACPLease_SessionOps_NonDeferred_ImmediatelyBoundToHandleSessionID(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	proc.newHandle = &SessionHandle{SessionID: "sess-new-1"}
	provider := NewACPBackendProvider(&fakeBackendProcessManager{process: proc})
	lease, err := provider.AcquireSession(context.Background(), AcquireRequest{Intent: IntentNew})
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}

	ops, ref, ok := lease.SessionOps()
	if !ok || ops == nil {
		t.Fatalf("SessionOps() = (%v, _, %v), want (non-nil, true)", ops, ok)
	}
	if string(ref.ProviderSession) != "sess-new-1" {
		t.Errorf("SessionOps() ref.ProviderSession = %q, want %q", ref.ProviderSession, "sess-new-1")
	}
}

// TestAcpSessionPromptOps_Prompt_TranslatesContentSessionIDAndStopReason
// proves Prompt: (a) translates the caller's neutral content blocks into ACP
// blocks, (b) calls SharedProcess.Prompt with the SessionRef.ProviderSession
// the caller passed in (not any lease-internal field), and (c) translates
// the ACP StopReason back into its neutral counterpart.
func TestAcpSessionPromptOps_Prompt_TranslatesContentSessionIDAndStopReason(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	proc.promptResp = acp.PromptResponse{StopReason: acp.StopReasonEndTurn}
	ops := &acpSessionPromptOps{process: proc}
	ref := agentbackend.SessionRef{ProviderSession: "sess-xyz"}

	outcome, err := ops.Prompt(context.Background(), ref, []agentbackend.ContentBlock{
		{Text: &agentbackend.TextBlock{Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if outcome.StopReason != agentbackend.StopReasonEndTurn {
		t.Errorf("StopReason = %v, want EndTurn", outcome.StopReason)
	}
	if len(proc.promptCalls) != 1 {
		t.Fatalf("promptCalls = %d, want 1", len(proc.promptCalls))
	}
	call := proc.promptCalls[0]
	if call.sessionID != acp.SessionId("sess-xyz") {
		t.Errorf("Prompt sessionID = %q, want %q", call.sessionID, "sess-xyz")
	}
	if len(call.blocks) != 1 || call.blocks[0].Text == nil || call.blocks[0].Text.Text != "hello" {
		t.Errorf("Prompt blocks = %+v, want one text block %q", call.blocks, "hello")
	}
}

// TestAcpSessionPromptOps_Prompt_ErrorTranslation proves Prompt maps a
// cancelled context and a JSON-RPC "method not found" error into the
// agentbackend sentinels, and passes through any other error unchanged.
func TestAcpSessionPromptOps_Prompt_ErrorTranslation(t *testing.T) {
	ref := agentbackend.SessionRef{ProviderSession: "sess-1"}

	t.Run("cancelled", func(t *testing.T) {
		proc := newFakeBackendSharedProcess()
		proc.promptErr = context.Canceled
		ops := &acpSessionPromptOps{process: proc}
		_, err := ops.Prompt(context.Background(), ref, nil)
		if !errors.Is(err, agentbackend.ErrCancelled) {
			t.Fatalf("Prompt error = %v, want ErrCancelled", err)
		}
	})

	t.Run("method not found", func(t *testing.T) {
		proc := newFakeBackendSharedProcess()
		proc.promptErr = &acp.RequestError{Code: acpLeaseJSONRPCMethodNotFound}
		ops := &acpSessionPromptOps{process: proc}
		_, err := ops.Prompt(context.Background(), ref, nil)
		var unsupported *agentbackend.UnsupportedError
		if !errors.As(err, &unsupported) {
			t.Fatalf("Prompt error = %v, want *agentbackend.UnsupportedError", err)
		}
	})

	t.Run("other error passes through unchanged", func(t *testing.T) {
		proc := newFakeBackendSharedProcess()
		wantErr := fmt.Errorf("transport exploded")
		proc.promptErr = wantErr
		ops := &acpSessionPromptOps{process: proc}
		_, err := ops.Prompt(context.Background(), ref, nil)
		if !errors.Is(err, wantErr) {
			t.Fatalf("Prompt error = %v, want to wrap %v unchanged", err, wantErr)
		}
	})
}

// TestAcpSessionPromptOps_Cancel_DelegatesWithSessionIDAndTranslatesError
// proves Cancel forwards the caller-supplied session ID and translates
// errors identically to Prompt.
func TestAcpSessionPromptOps_Cancel_DelegatesWithSessionIDAndTranslatesError(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	ops := &acpSessionPromptOps{process: proc}
	ref := agentbackend.SessionRef{ProviderSession: "sess-cancel-1"}

	if err := ops.Cancel(context.Background(), ref); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(proc.cancelCalls) != 1 || proc.cancelCalls[0] != acp.SessionId("sess-cancel-1") {
		t.Fatalf("cancelCalls = %v, want exactly [sess-cancel-1]", proc.cancelCalls)
	}

	proc.cancelErr = context.Canceled
	if err := ops.Cancel(context.Background(), ref); !errors.Is(err, agentbackend.ErrCancelled) {
		t.Fatalf("Cancel error = %v, want ErrCancelled", err)
	}
}

// TestAcpSessionPromptOps_SetModel_DelegatesAndTagsUnsupportedWithFeature
// proves SetModel forwards session ID + model ID unchanged, and a "method
// not found" error is tagged with agentbackend.FeatureModelSelection so
// callers can distinguish it from a mode-selection failure.
func TestAcpSessionPromptOps_SetModel_DelegatesAndTagsUnsupportedWithFeature(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	ops := &acpSessionPromptOps{process: proc}
	ref := agentbackend.SessionRef{ProviderSession: "sess-model-1"}

	if err := ops.SetModel(context.Background(), ref, "gpt-5"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if len(proc.setModelCalls) != 1 || proc.setModelCalls[0] != (fakeBackendSetCall{sessionID: "sess-model-1", value: "gpt-5"}) {
		t.Fatalf("setModelCalls = %+v, want exactly [{sess-model-1 gpt-5}]", proc.setModelCalls)
	}

	proc.setModelErr = &acp.RequestError{Code: acpLeaseJSONRPCMethodNotFound}
	err := ops.SetModel(context.Background(), ref, "gpt-5")
	var unsupported *agentbackend.UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Feature != agentbackend.FeatureModelSelection {
		t.Fatalf("SetModel error = %v, want *UnsupportedError{Feature: FeatureModelSelection}", err)
	}
}

// TestAcpSessionPromptOps_SetMode_DelegatesAndTagsUnsupportedWithFeature
// mirrors the SetModel test above for SetMode/FeatureModeSelection.
func TestAcpSessionPromptOps_SetMode_DelegatesAndTagsUnsupportedWithFeature(t *testing.T) {
	proc := newFakeBackendSharedProcess()
	ops := &acpSessionPromptOps{process: proc}
	ref := agentbackend.SessionRef{ProviderSession: "sess-mode-1"}

	if err := ops.SetMode(context.Background(), ref, "plan"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if len(proc.setModeCalls) != 1 || proc.setModeCalls[0] != (fakeBackendSetCall{sessionID: "sess-mode-1", value: "plan"}) {
		t.Fatalf("setModeCalls = %+v, want exactly [{sess-mode-1 plan}]", proc.setModeCalls)
	}

	proc.setModeErr = &acp.RequestError{Code: acpLeaseJSONRPCMethodNotFound}
	err := ops.SetMode(context.Background(), ref, "plan")
	var unsupported *agentbackend.UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Feature != agentbackend.FeatureModeSelection {
		t.Fatalf("SetMode error = %v, want *UnsupportedError{Feature: FeatureModeSelection}", err)
	}
}

// TestAcpLeaseContentBlocksToACP_TranslatesEachKind proves the neutral->ACP
// content translator used by the Prompt hot path handles every neutral
// content kind SessionPromptOps.Prompt can receive.
func TestAcpLeaseContentBlocksToACP_TranslatesEachKind(t *testing.T) {
	blocks := acpLeaseContentBlocksToACP([]agentbackend.ContentBlock{
		{Text: &agentbackend.TextBlock{Text: "hi"}},
		{Image: &agentbackend.ImageBlock{Data: "b64", MimeType: "image/png"}},
		{File: &agentbackend.FileBlock{Path: "/tmp/x.txt"}},
	})
	if len(blocks) != 3 {
		t.Fatalf("len(blocks) = %d, want 3", len(blocks))
	}
	if blocks[0].Text == nil || blocks[0].Text.Text != "hi" {
		t.Errorf("blocks[0] = %+v, want text block %q", blocks[0], "hi")
	}
	if blocks[1].Image == nil || blocks[1].Image.Data != "b64" || blocks[1].Image.MimeType != "image/png" {
		t.Errorf("blocks[1] = %+v, want image block {b64, image/png}", blocks[1])
	}
	if blocks[2].ResourceLink == nil || blocks[2].ResourceLink.Uri != "file:///tmp/x.txt" {
		t.Errorf("blocks[2] = %+v, want resource_link with file:// URI", blocks[2])
	}
}

// TestAcpLeaseContentBlocksToNeutral_TranslatesEachKind_SkipsUnsupported
// proves the ACP->neutral content translator used by flushContextInPlace
// round-trips Text/Image/ResourceLink (with and without a MIME type) and
// silently skips content kinds without a neutral analogue (e.g. Audio),
// mirroring internal/acpbackend's ToNeutralContentBlocks.
func TestAcpLeaseContentBlocksToNeutral_TranslatesEachKind_SkipsUnsupported(t *testing.T) {
	mime := "text/plain"
	blocks := acpLeaseContentBlocksToNeutral([]acp.ContentBlock{
		acp.TextBlock("hi"),
		acp.ImageBlock("b64", "image/png"),
		{ResourceLink: &acp.ContentBlockResourceLink{Uri: "file:///tmp/x.txt", MimeType: &mime}},
		{ResourceLink: &acp.ContentBlockResourceLink{Uri: "file:///tmp/y.txt"}}, // no MimeType
		acp.AudioBlock("b64", "audio/mp3"),                                      // no neutral analogue: must be skipped
	})
	if len(blocks) != 4 {
		t.Fatalf("len(blocks) = %d, want 4 (Audio has no neutral analogue and must be skipped)", len(blocks))
	}
	if blocks[0].Text == nil || blocks[0].Text.Text != "hi" {
		t.Errorf("blocks[0] = %+v, want text block %q", blocks[0], "hi")
	}
	if blocks[1].Image == nil || blocks[1].Image.Data != "b64" || blocks[1].Image.MimeType != "image/png" {
		t.Errorf("blocks[1] = %+v, want image block {b64, image/png}", blocks[1])
	}
	if blocks[2].File == nil || blocks[2].File.Path != "file:///tmp/x.txt" || blocks[2].File.MimeType != "text/plain" {
		t.Errorf("blocks[2] = %+v, want file block {file:///tmp/x.txt, text/plain}", blocks[2])
	}
	if blocks[3].File == nil || blocks[3].File.Path != "file:///tmp/y.txt" || blocks[3].File.MimeType != "" {
		t.Errorf("blocks[3] = %+v, want file block {file:///tmp/y.txt, \"\"} (missing MimeType -> empty string)", blocks[3])
	}
}

// TestAcpLeaseStopReasonToNeutral_AllCases pins the full ACP->neutral stop
// reason mapping, including the fallback for any unrecognized value.
func TestAcpLeaseStopReasonToNeutral_AllCases(t *testing.T) {
	tests := []struct {
		in   acp.StopReason
		want agentbackend.StopReason
	}{
		{acp.StopReasonEndTurn, agentbackend.StopReasonEndTurn},
		{acp.StopReasonCancelled, agentbackend.StopReasonCancelled},
		{acp.StopReasonMaxTokens, agentbackend.StopReasonMaxTokens},
		{acp.StopReasonMaxTurnRequests, agentbackend.StopReasonMaxTokens},
		{acp.StopReasonRefusal, agentbackend.StopReasonRefusal},
		{acp.StopReason("something-unknown"), agentbackend.StopReasonError},
	}
	for _, tt := range tests {
		if got := acpLeaseStopReasonToNeutral(tt.in); got != tt.want {
			t.Errorf("acpLeaseStopReasonToNeutral(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
