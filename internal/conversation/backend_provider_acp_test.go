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

	unregistered []acp.SessionId
	generation   int
	restarted    []int
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
func (f *fakeBackendSharedProcess) Cancel(context.Context, acp.SessionId) error { return nil }
func (f *fakeBackendSharedProcess) SetSessionMode(context.Context, acp.SessionId, string) error {
	return nil
}
func (f *fakeBackendSharedProcess) SetSessionModel(context.Context, acp.SessionId, string) error {
	return nil
}
func (f *fakeBackendSharedProcess) Done() <-chan struct{} { return f.processDone }
func (f *fakeBackendSharedProcess) Prompt(context.Context, acp.SessionId, []acp.ContentBlock) (acp.PromptResponse, error) {
	return acp.PromptResponse{}, nil
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
	g.fakeBackendSharedProcess.mu.Lock()
	*g.calls++
	g.fakeBackendSharedProcess.mu.Unlock()
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
