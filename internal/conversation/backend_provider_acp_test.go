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

	unregistered []string
	generation   int
	restarted    []int

	// mitto-mx9.1: recorded/configurable Prompt/Cancel/SetSessionMode/
	// SetSessionModel calls, used by the SessionPromptOps seam tests below to
	// prove acpSessionPromptOps delegates to SharedProcess with the exact
	// session ID/content/value it was given, and propagates responses/errors
	// unchanged (modulo neutral translation).
	promptCalls   []fakeBackendPromptCall
	promptResp    agentbackend.PromptOutcome
	promptErr     error
	cancelCalls   []string
	cancelErr     error
	setModeCalls  []fakeBackendSetCall
	setModeErr    error
	setModelCalls []fakeBackendSetCall
	setModelErr   error
}

// fakeBackendPromptCall records one Prompt() invocation.
type fakeBackendPromptCall struct {
	sessionID string
	blocks    []agentbackend.ContentBlock
}

// fakeBackendSetCall records one SetSessionMode/SetSessionModel invocation.
type fakeBackendSetCall struct {
	sessionID string
	value     string
}

func newFakeBackendSharedProcess() *fakeBackendSharedProcess {
	return &fakeBackendSharedProcess{
		processDone: make(chan struct{}),
		caps:        &acp.AgentCapabilities{},
	}
}

func (f *fakeBackendSharedProcess) Capabilities() agentbackend.Capabilities {
	return NewProcessCapabilities(f.caps)
}
func (f *fakeBackendSharedProcess) ProcessDone() <-chan struct{} { return f.processDone }
func (f *fakeBackendSharedProcess) NewSession(context.Context, string, []agentbackend.MCPServerDescriptor) (*SessionHandle, error) {
	f.mu.Lock()
	f.newCalls++
	f.mu.Unlock()
	return f.newHandle, f.newErr
}
func (f *fakeBackendSharedProcess) LoadSession(context.Context, string, string, []agentbackend.MCPServerDescriptor) (*SessionHandle, error) {
	return f.loadHandle, f.loadErr
}
func (f *fakeBackendSharedProcess) ResumeSession(context.Context, string, string, []agentbackend.MCPServerDescriptor) (*SessionHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeHandle, f.resumeErr
}
func (f *fakeBackendSharedProcess) RegisterSession(string, *SessionCallbacks) {}
func (f *fakeBackendSharedProcess) UnregisterSession(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregistered = append(f.unregistered, id)
}
func (f *fakeBackendSharedProcess) Cancel(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCalls = append(f.cancelCalls, id)
	return f.cancelErr
}
func (f *fakeBackendSharedProcess) SetSessionMode(_ context.Context, id string, mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setModeCalls = append(f.setModeCalls, fakeBackendSetCall{sessionID: id, value: mode})
	return f.setModeErr
}
func (f *fakeBackendSharedProcess) SetSessionModel(_ context.Context, id string, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setModelCalls = append(f.setModelCalls, fakeBackendSetCall{sessionID: id, value: model})
	return f.setModelErr
}
func (f *fakeBackendSharedProcess) Done() <-chan struct{} { return f.processDone }
func (f *fakeBackendSharedProcess) Prompt(_ context.Context, id string, blocks []agentbackend.ContentBlock) (agentbackend.PromptOutcome, error) {
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

	if len(proc.unregistered) != 1 || proc.unregistered[0] != "sess-1" {
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

func (g *gatingSharedProcess) ResumeSession(ctx context.Context, id, cwd string, servers []agentbackend.MCPServerDescriptor) (*SessionHandle, error) {
	g.mu.Lock()
	*g.calls++
	g.mu.Unlock()
	<-g.gate
	return g.handle, nil
}

// TestACPCapabilities_Query proves the ACP capabilities adapter answers only
// what is directly knowable from AgentCapabilities/SessionHandle (reporting
// CapabilityUnknown rather than guessing for undecidable features), while
// FeatureFiles/FeaturePermissions are constant host facts and
// FeatureModelSelection/FeatureModeSelection require a non-empty catalog to
// report Supported (mitto-mx9.8: parity with internal/acpbackend's
// sessionCapabilities.Query).
func TestACPCapabilities_Query(t *testing.T) {
	caps := &acpCapabilities{
		processCaps: NewProcessCapabilities(&acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: true}}),
		handle:      &SessionHandle{Models: &SessionModelState{AvailableModels: []ModelInfo{{ModelId: "m1"}}}},
	}
	if got := caps.Query(agentbackend.FeatureImages); got != agentbackend.CapabilitySupported {
		t.Errorf("Query(FeatureImages) = %v, want Supported", got)
	}
	if got := caps.Query(agentbackend.FeatureFiles); got != agentbackend.CapabilitySupported {
		t.Errorf("Query(FeatureFiles) = %v, want Supported (constant host fact)", got)
	}
	if got := caps.Query(agentbackend.FeaturePermissions); got != agentbackend.CapabilitySupported {
		t.Errorf("Query(FeaturePermissions) = %v, want Supported (constant host fact)", got)
	}
	if got := caps.Query(agentbackend.FeatureModelSelection); got != agentbackend.CapabilitySupported {
		t.Errorf("Query(FeatureModelSelection) = %v, want Supported (non-empty catalog)", got)
	}
	if got := caps.Query(agentbackend.FeatureModeSelection); got != agentbackend.CapabilityUnsupported {
		t.Errorf("Query(FeatureModeSelection) = %v, want Unsupported (handle.Modes is nil)", got)
	}
	if got := caps.Query(agentbackend.FeatureTerminals); got != agentbackend.CapabilityUnknown {
		t.Errorf("Query(FeatureTerminals) = %v, want Unknown", got)
	}
}

// TestACPProcessCapabilities_Query pins the ACP→neutral translation added by
// mitto-mx9.1.3 for the process-level Feature constants (FeatureMCPHttp,
// FeatureSessionResume, FeatureSessionLoad), plus the pre-existing
// FeatureImages and the nil-caps / unknown-feature fallbacks. This is the
// adapter SharedProcess.Capabilities() now returns instead of the raw
// *acp.AgentCapabilities struct.
func TestACPProcessCapabilities_Query(t *testing.T) {
	t.Run("nil caps reports Unknown for every feature", func(t *testing.T) {
		caps := NewProcessCapabilities(nil)
		for _, f := range []agentbackend.Feature{
			agentbackend.FeatureImages,
			agentbackend.FeatureMCPHttp,
			agentbackend.FeatureSessionResume,
			agentbackend.FeatureSessionLoad,
		} {
			if got := caps.Query(f); got != agentbackend.CapabilityUnknown {
				t.Errorf("Query(%v) with nil caps = %v, want Unknown", f, got)
			}
		}
	})

	t.Run("supported/unsupported per feature", func(t *testing.T) {
		caps := NewProcessCapabilities(&acp.AgentCapabilities{
			PromptCapabilities: acp.PromptCapabilities{Image: true},
			McpCapabilities:    acp.McpCapabilities{Http: true},
			SessionCapabilities: acp.SessionCapabilities{
				Resume: &acp.SessionResumeCapabilities{},
			},
			LoadSession: true,
		})
		cases := []struct {
			feature agentbackend.Feature
			want    agentbackend.CapabilityState
		}{
			{agentbackend.FeatureImages, agentbackend.CapabilitySupported},
			{agentbackend.FeatureMCPHttp, agentbackend.CapabilitySupported},
			{agentbackend.FeatureSessionResume, agentbackend.CapabilitySupported},
			{agentbackend.FeatureSessionLoad, agentbackend.CapabilitySupported},
			{agentbackend.FeatureTerminals, agentbackend.CapabilityUnknown},
		}
		for _, tc := range cases {
			if got := caps.Query(tc.feature); got != tc.want {
				t.Errorf("Query(%v) = %v, want %v", tc.feature, got, tc.want)
			}
		}

		unsupported := NewProcessCapabilities(&acp.AgentCapabilities{})
		for _, f := range []agentbackend.Feature{
			agentbackend.FeatureImages,
			agentbackend.FeatureMCPHttp,
			agentbackend.FeatureSessionResume,
			agentbackend.FeatureSessionLoad,
		} {
			if got := unsupported.Query(f); got != agentbackend.CapabilityUnsupported {
				t.Errorf("Query(%v) on zero-value caps = %v, want Unsupported", f, got)
			}
		}
	})
}

// TestMCPServersFromACP_TranslatesStdioAndHTTPSkipsUnrecognized pins the
// mitto-mx9.1.2 ACP→neutral MCP server translator: Stdio and HTTP entries
// round-trip their fields (including nested Env/Headers), order is
// preserved, and an entry with neither Http nor Stdio set (e.g. a Sse-only
// union member, which this codebase never constructs) is silently skipped
// rather than producing a zero-value descriptor.
func TestMCPServersFromACP_TranslatesStdioAndHTTPSkipsUnrecognized(t *testing.T) {
	in := []acp.McpServer{
		{Stdio: &acp.McpServerStdio{
			Name:    "mitto",
			Command: "/opt/mitto/bin/mitto",
			Args:    []string{"mcp", "--proxy-to", "http://127.0.0.1:5757/mcp"},
			Env:     []acp.EnvVariable{{Name: "FOO", Value: "bar"}},
		}},
		{Sse: &acp.McpServerSseInline{Url: "http://example.invalid/sse"}}, // unrecognized union member
		{Http: &acp.McpServerHttpInline{
			Type:    "http",
			Name:    "mitto",
			Url:     "http://127.0.0.1:5757/mcp",
			Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer tok"}},
		}},
	}

	out := MCPServersFromACP(in)

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (Sse-only entry must be skipped)", len(out))
	}

	stdio := out[0]
	if stdio.Stdio == nil || stdio.HTTP != nil {
		t.Fatalf("out[0] = %+v, want Stdio set and HTTP nil", stdio)
	}
	if stdio.Stdio.Name != "mitto" || stdio.Stdio.Command != "/opt/mitto/bin/mitto" {
		t.Errorf("out[0].Stdio = %+v, want Name=mitto Command=/opt/mitto/bin/mitto", stdio.Stdio)
	}
	wantArgs := []string{"mcp", "--proxy-to", "http://127.0.0.1:5757/mcp"}
	if len(stdio.Stdio.Args) != len(wantArgs) {
		t.Fatalf("out[0].Stdio.Args = %v, want %v", stdio.Stdio.Args, wantArgs)
	}
	for i, a := range wantArgs {
		if stdio.Stdio.Args[i] != a {
			t.Errorf("out[0].Stdio.Args[%d] = %q, want %q", i, stdio.Stdio.Args[i], a)
		}
	}
	if len(stdio.Stdio.Env) != 1 || stdio.Stdio.Env[0].Name != "FOO" || stdio.Stdio.Env[0].Value != "bar" {
		t.Errorf("out[0].Stdio.Env = %+v, want [{FOO bar}]", stdio.Stdio.Env)
	}

	httpEntry := out[1]
	if httpEntry.HTTP == nil || httpEntry.Stdio != nil {
		t.Fatalf("out[1] = %+v, want HTTP set and Stdio nil", httpEntry)
	}
	if httpEntry.HTTP.Name != "mitto" || httpEntry.HTTP.URL != "http://127.0.0.1:5757/mcp" {
		t.Errorf("out[1].HTTP = %+v, want Name=mitto URL=http://127.0.0.1:5757/mcp", httpEntry.HTTP)
	}
	if len(httpEntry.HTTP.Headers) != 1 || httpEntry.HTTP.Headers[0].Name != "Authorization" || httpEntry.HTTP.Headers[0].Value != "Bearer tok" {
		t.Errorf("out[1].HTTP.Headers = %+v, want [{Authorization Bearer tok}]", httpEntry.HTTP.Headers)
	}
}

// TestMCPServersFromACP_EmptyInputReturnsNonNilEmptySlice guards against
// regressing to a nil result: ACP validates that McpServers is a non-nil
// (possibly empty) slice, and MCPServersToACP's output feeds directly into
// that RPC field, so the intermediate neutral slice must also never be nil.
func TestMCPServersFromACP_EmptyInputReturnsNonNilEmptySlice(t *testing.T) {
	out := MCPServersFromACP(nil)
	if out == nil {
		t.Fatal("MCPServersFromACP(nil) = nil, want non-nil empty slice")
	}
	if len(out) != 0 {
		t.Errorf("len(out) = %d, want 0", len(out))
	}
}

// TestMCPServersToACP_RoundTripsStdioAndHTTP is the reverse of
// TestMCPServersFromACP_TranslatesStdioAndHTTPSkipsUnrecognized: rebuilding
// the ACP wire shape from neutral descriptors must reproduce the original
// fields exactly (this is the direction SharedProcess implementations use
// right before issuing the session/new RPC).
func TestMCPServersToACP_RoundTripsStdioAndHTTP(t *testing.T) {
	in := []agentbackend.MCPServerDescriptor{
		{HTTP: &agentbackend.MCPServerHTTP{
			Name:    "mitto",
			URL:     "http://127.0.0.1:5757/mcp",
			Headers: []agentbackend.HTTPHeader{{Name: "Authorization", Value: "Bearer tok"}},
		}},
		{Stdio: &agentbackend.MCPServerStdio{
			Name:    "mitto",
			Command: "/opt/mitto/bin/mitto",
			Args:    []string{"mcp", "--proxy-to", "http://127.0.0.1:5757/mcp"},
			Env:     []agentbackend.EnvVar{{Name: "FOO", Value: "bar"}},
		}},
	}

	out := MCPServersToACP(in)

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}

	httpEntry := out[0]
	if httpEntry.Http == nil || httpEntry.Stdio != nil {
		t.Fatalf("out[0] = %+v, want Http set and Stdio nil", httpEntry)
	}
	if httpEntry.Http.Type != "http" {
		t.Errorf("out[0].Http.Type = %q, want %q (ACP requires the discriminant)", httpEntry.Http.Type, "http")
	}
	if httpEntry.Http.Name != "mitto" || httpEntry.Http.Url != "http://127.0.0.1:5757/mcp" {
		t.Errorf("out[0].Http = %+v, want Name=mitto Url=http://127.0.0.1:5757/mcp", httpEntry.Http)
	}
	if len(httpEntry.Http.Headers) != 1 || httpEntry.Http.Headers[0].Name != "Authorization" || httpEntry.Http.Headers[0].Value != "Bearer tok" {
		t.Errorf("out[0].Http.Headers = %+v, want [{Authorization Bearer tok}]", httpEntry.Http.Headers)
	}

	stdio := out[1]
	if stdio.Stdio == nil || stdio.Http != nil {
		t.Fatalf("out[1] = %+v, want Stdio set and Http nil", stdio)
	}
	if stdio.Stdio.Name != "mitto" || stdio.Stdio.Command != "/opt/mitto/bin/mitto" {
		t.Errorf("out[1].Stdio = %+v, want Name=mitto Command=/opt/mitto/bin/mitto", stdio.Stdio)
	}
	if len(stdio.Stdio.Env) != 1 || stdio.Stdio.Env[0].Name != "FOO" || stdio.Stdio.Env[0].Value != "bar" {
		t.Errorf("out[1].Stdio.Env = %+v, want [{FOO bar}]", stdio.Stdio.Env)
	}
}

// TestModeStateFromACP_NilAndPopulated pins the mitto-mx9.1.2
// SessionHandle.Modes translation: a nil ACP mode state translates to nil
// (no session modes advertised), and a populated one carries over the
// current mode id plus every available mode, handling both a present and an
// absent (nil) per-mode Description pointer.
func TestModeStateFromACP_NilAndPopulated(t *testing.T) {
	if got := ModeStateFromACP(nil); got != nil {
		t.Fatalf("ModeStateFromACP(nil) = %+v, want nil", got)
	}

	desc := "Focused on planning, not editing"
	in := &acp.SessionModeState{
		CurrentModeId: acp.SessionModeId("plan"),
		AvailableModes: []acp.SessionMode{
			{Id: acp.SessionModeId("plan"), Name: "Plan", Description: &desc},
			{Id: acp.SessionModeId("code"), Name: "Code", Description: nil},
		},
	}

	got := ModeStateFromACP(in)
	if got == nil {
		t.Fatal("ModeStateFromACP(populated) = nil, want non-nil")
	}
	if got.CurrentModeID != "plan" {
		t.Errorf("CurrentModeID = %q, want %q", got.CurrentModeID, "plan")
	}
	if len(got.Available) != 2 {
		t.Fatalf("len(Available) = %d, want 2", len(got.Available))
	}
	if got.Available[0].ID != "plan" || got.Available[0].Name != "Plan" || got.Available[0].Description != desc {
		t.Errorf("Available[0] = %+v, want {ID:plan Name:Plan Description:%q}", got.Available[0], desc)
	}
	if got.Available[1].ID != "code" || got.Available[1].Name != "Code" || got.Available[1].Description != "" {
		t.Errorf("Available[1] = %+v, want {ID:code Name:Code Description:\"\"} (nil Description pointer -> empty string)", got.Available[1])
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

	if len(proc.unregistered) != 1 || proc.unregistered[0] != "bound-sess-1" {
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
	if len(proc.cancelCalls) != 1 || proc.cancelCalls[0] != "bound-sess-1" {
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
	proc.promptResp = agentbackend.PromptOutcome{StopReason: agentbackend.StopReasonEndTurn}
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
	if call.sessionID != "sess-xyz" {
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
	if len(proc.cancelCalls) != 1 || proc.cancelCalls[0] != "sess-cancel-1" {
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

// TestAcpLeaseStopReasonFromNeutral_AllCases pins the reverse (neutral->ACP)
// mapping used by promptOutcomeToACPResponse to feed the main prompt-loop's
// existing ACP-typed downstream pipeline (mitto-mx9.1.1). Per
// acpLeaseStopReasonFromNeutral's doc, StopReasonMaxTokens always
// reconstructs as acp.StopReasonMaxTokens specifically (the
// acp.StopReasonMaxTurnRequests collapse on the way in is one-directional),
// and StopReasonError has no ACP counterpart so it reconstructs as "".
func TestAcpLeaseStopReasonFromNeutral_AllCases(t *testing.T) {
	tests := []struct {
		in   agentbackend.StopReason
		want acp.StopReason
	}{
		{agentbackend.StopReasonEndTurn, acp.StopReasonEndTurn},
		{agentbackend.StopReasonCancelled, acp.StopReasonCancelled},
		{agentbackend.StopReasonMaxTokens, acp.StopReasonMaxTokens},
		{agentbackend.StopReasonRefusal, acp.StopReasonRefusal},
		{agentbackend.StopReasonError, acp.StopReason("")},
		{agentbackend.StopReason("something-unknown"), acp.StopReason("")},
	}
	for _, tt := range tests {
		if got := acpLeaseStopReasonFromNeutral(tt.in); got != tt.want {
			t.Errorf("acpLeaseStopReasonFromNeutral(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestAcpLeaseUsageFromNeutral_NilAndPopulated covers both branches of the
// neutral->ACP usage reconstruction: nil (no usage reported) must stay nil
// rather than becoming a zero-valued *acp.Usage, and a populated PromptUsage
// must translate its three fields exactly (mitto-mx9.1.1 token-accounting
// parity for the fields the downstream pipeline actually reads).
func TestAcpLeaseUsageFromNeutral_NilAndPopulated(t *testing.T) {
	if got := acpLeaseUsageFromNeutral(nil); got != nil {
		t.Fatalf("acpLeaseUsageFromNeutral(nil) = %+v, want nil", got)
	}

	got := acpLeaseUsageFromNeutral(&agentbackend.PromptUsage{
		InputTokens:  111,
		OutputTokens: 222,
		TotalTokens:  333,
	})
	if got == nil {
		t.Fatal("acpLeaseUsageFromNeutral(populated) = nil, want non-nil")
	}
	if got.InputTokens != 111 || got.OutputTokens != 222 || got.TotalTokens != 333 {
		t.Errorf("acpLeaseUsageFromNeutral(populated) = %+v, want {111, 222, 333}", got)
	}
}

// TestPromptOutcomeToACPResponse_TokenAccountingParity is the mitto-mx9.1.1
// acceptance-criteria test for the neutral->ACP direction: "agentbackend.
// PromptOutcome carries token usage; production token-accounting parity
// verified against the ACP baseline." promptOutcomeToACPResponse is the
// real translation the main prompt-loop (bgsession_prompt.go) applies to
// SharedProcess.Prompt's neutral outcome before feeding the existing
// ACP-typed accumulateTokenUsage/handlePromptSuccess pipeline; this proves a
// populated PromptUsage (as internal/acpbackend.ToNeutralPromptOutcome would
// have produced from an ACP baseline — see
// TestToNeutralPromptOutcome_UsagePassthrough for that ACP->neutral half of
// the round trip) reconstructs with exact Input/Output/TotalTokens parity.
func TestPromptOutcomeToACPResponse_TokenAccountingParity(t *testing.T) {
	neutral := agentbackend.PromptOutcome{
		StopReason: agentbackend.StopReasonEndTurn,
		Usage: &agentbackend.PromptUsage{
			InputTokens:  1000,
			OutputTokens: 250,
			TotalTokens:  1250,
		},
	}

	got := promptOutcomeToACPResponse(neutral)

	if got.StopReason != acp.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want %q", got.StopReason, acp.StopReasonEndTurn)
	}
	if got.Usage == nil {
		t.Fatal("Usage = nil, want non-nil (neutral outcome reported usage)")
	}
	if got.Usage.InputTokens != 1000 || got.Usage.OutputTokens != 250 || got.Usage.TotalTokens != 1250 {
		t.Errorf("Usage = %+v, want {1000, 250, 1250} (exact parity)", got.Usage)
	}
}

// TestPromptOutcomeToACPResponse_NoUsageReported proves the "no usage
// reported" case (Usage == nil, e.g. an agent that doesn't report token
// usage for a turn) reconstructs as nil rather than a zero-valued
// *acp.Usage — distinguishing "unknown" from "zero" end-to-end, per
// PromptUsage's doc.
func TestPromptOutcomeToACPResponse_NoUsageReported(t *testing.T) {
	neutral := agentbackend.PromptOutcome{StopReason: agentbackend.StopReasonCancelled}

	got := promptOutcomeToACPResponse(neutral)

	if got.Usage != nil {
		t.Errorf("Usage = %+v, want nil (neutral outcome reported no usage)", got.Usage)
	}
	if got.StopReason != acp.StopReasonCancelled {
		t.Errorf("StopReason = %q, want %q", got.StopReason, acp.StopReasonCancelled)
	}
}
