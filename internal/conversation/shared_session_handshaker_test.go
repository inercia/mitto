package conversation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/session"
)

// compile-time check.
var _ handshakeDeps = (*fakeHandshakeDeps)(nil)

// fakeSharedProcess implements SharedProcess for testing.
type fakeSharedProcess struct {
	mu sync.Mutex

	caps               *acp.AgentCapabilities
	processDone        chan struct{}
	newSessionHandle   *SessionHandle
	newSessionErr      error
	newSessionCalls    []string // recorded workingDirs
	loadSessionHandle  *SessionHandle
	loadSessionErr     error
	loadSessionCalls   []string // recorded acp_session_ids
	registeredSessions []acp.SessionId

	// mitto-1ut: budget observability. recommendedLoadTimeout is returned by
	// RecommendedLoadTimeout; the *Deadline fields capture the ctx deadline (if
	// any) observed on the respective RPC so tests can assert the resume path
	// caps both under ONE shared budget instead of stacking two.
	recommendedLoadTimeout time.Duration
	loadCtxDeadline        time.Time
	loadCtxHasDeadline     bool
	newCtxDeadline         time.Time
	newCtxHasDeadline      bool

	// loadBlocksUntilCtxDone, when true, makes LoadSession block until its ctx is
	// cancelled/expires and then return context.DeadlineExceeded — simulating a
	// probe against a genuinely COLD process that hits its cap rather than
	// returning a fast -32602. Used to exercise the mitto-1ut starvation fix where
	// a timed-out probe must NOT truncate the session/new fallback's budget.
	loadBlocksUntilCtxDone bool
}

func newFakeSharedProcess() *fakeSharedProcess {
	return &fakeSharedProcess{
		processDone:      make(chan struct{}),
		caps:             &acp.AgentCapabilities{},
		newSessionHandle: &SessionHandle{SessionID: "acp-sess-1"},
	}
}

func (f *fakeSharedProcess) Capabilities() *acp.AgentCapabilities { return f.caps }
func (f *fakeSharedProcess) ProcessDone() <-chan struct{}         { return f.processDone }
func (f *fakeSharedProcess) NewSession(ctx context.Context, cwd string, _ []acp.McpServer) (*SessionHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newSessionCalls = append(f.newSessionCalls, cwd)
	f.newCtxDeadline, f.newCtxHasDeadline = ctx.Deadline()
	return f.newSessionHandle, f.newSessionErr
}
func (f *fakeSharedProcess) LoadSession(ctx context.Context, acpSessionID, _ string, _ []acp.McpServer) (*SessionHandle, error) {
	f.mu.Lock()
	f.loadSessionCalls = append(f.loadSessionCalls, acpSessionID)
	f.loadCtxDeadline, f.loadCtxHasDeadline = ctx.Deadline()
	blockUntilDone := f.loadBlocksUntilCtxDone
	f.mu.Unlock()
	if blockUntilDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadSessionErr != nil || f.loadSessionHandle != nil {
		return f.loadSessionHandle, f.loadSessionErr
	}
	return nil, errors.New("load not supported")
}
func (f *fakeSharedProcess) ResumeSession(_ context.Context, _, _ string, _ []acp.McpServer) (*SessionHandle, error) {
	return nil, errors.New("resume not supported")
}
func (f *fakeSharedProcess) RegisterSession(id acp.SessionId, _ *SessionCallbacks) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registeredSessions = append(f.registeredSessions, id)
}
func (f *fakeSharedProcess) UnregisterSession(_ acp.SessionId)               {}
func (f *fakeSharedProcess) Cancel(_ context.Context, _ acp.SessionId) error { return nil }
func (f *fakeSharedProcess) Done() <-chan struct{}                           { return f.processDone }
func (f *fakeSharedProcess) Prompt(_ context.Context, _ acp.SessionId, _ []acp.ContentBlock) (acp.PromptResponse, error) {
	return acp.PromptResponse{}, nil
}
func (f *fakeSharedProcess) SetSessionMode(_ context.Context, _ acp.SessionId, _ string) error {
	return nil
}
func (f *fakeSharedProcess) SetSessionModel(_ context.Context, _ acp.SessionId, _ string) error {
	return nil
}
func (f *fakeSharedProcess) Restart() error { return nil }
func (f *fakeSharedProcess) RecommendedLoadTimeout(_ bool) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recommendedLoadTimeout
}
func (f *fakeSharedProcess) MCPInitDone() bool                                                   { return true }
func (f *fakeSharedProcess) WaitForMCPInit(_ context.Context) bool                               { return true }
func (f *fakeSharedProcess) SetPromptFunc(_ func(context.Context, string, string, string) error) {}
func (f *fakeSharedProcess) PromptProcessorAsync(_ context.Context, _, _, _ string) error {
	return nil
}

// fakeHandshakeDeps is a test double for handshakeDeps.
type fakeHandshakeDeps struct {
	mu sync.Mutex

	// state knobs
	sessionID         string
	logger            *slog.Logger
	sessionCtx        context.Context
	creationCtx       context.Context
	sharedProcess     SharedProcess
	acpClient         *WebClient
	agentImages       bool
	acpID             string
	pending           bool
	pendingDir        string
	pendingMcpSrv     []acp.McpServer
	pendingModes      *acp.SessionModeState
	pendingModels     *SessionModelState
	pendingModelCfgId acp.SessionConfigId
	appliedModelCfgId acp.SessionConfigId
	resumeMethod      string

	// mutexes for pending/handshake
	pendingMu   sync.Mutex
	handshakeMu sync.Mutex

	// recorders
	persistedACPID  int
	clearedACPID    int
	notifiedEvents  []string
	appliedModes    []*acp.SessionModeState
	appliedModels   []*SessionModelState
	startMcpCalls   int
	stopMcpCalls    int
	processDonesSet int
	niledCreation   int
}

func newFakeHandshakeDeps() *fakeHandshakeDeps {
	return &fakeHandshakeDeps{
		sessionID:  "sess-hs",
		logger:     slog.Default(),
		sessionCtx: context.Background(),
		acpClient:  &WebClient{},
	}
}

func (f *fakeHandshakeDeps) hsSessionID() string            { return f.sessionID }
func (f *fakeHandshakeDeps) hsLogger() *slog.Logger         { return f.logger }
func (f *fakeHandshakeDeps) hsSessionCtx() context.Context  { return f.sessionCtx }
func (f *fakeHandshakeDeps) hsCreationCtx() context.Context { return f.creationCtx }
func (f *fakeHandshakeDeps) hsNilCreationCtx() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creationCtx = nil
	f.niledCreation++
}
func (f *fakeHandshakeDeps) hsBuildWebClientConfig() WebClientConfig {
	return WebClientConfig{SeqProvider: &fakeSeqProvider{}}
}

func (f *fakeHandshakeDeps) hsGetSharedProcess() SharedProcess  { return f.sharedProcess }
func (f *fakeHandshakeDeps) hsSetSharedProcess(p SharedProcess) { f.sharedProcess = p }

func (f *fakeHandshakeDeps) hsSetACPClient(c *WebClient) { f.acpClient = c }
func (f *fakeHandshakeDeps) hsGetACPClient() *WebClient  { return f.acpClient }

func (f *fakeHandshakeDeps) hsSetAgentSupportsImages(v bool) { f.agentImages = v }

func (f *fakeHandshakeDeps) hsGetACPID() string   { return f.acpID }
func (f *fakeHandshakeDeps) hsSetACPID(id string) { f.acpID = id }

func (f *fakeHandshakeDeps) hsPendingSharedLock()   { f.pendingMu.Lock() }
func (f *fakeHandshakeDeps) hsPendingSharedUnlock() { f.pendingMu.Unlock() }

func (f *fakeHandshakeDeps) hsIsPendingShared() bool                         { return f.pending }
func (f *fakeHandshakeDeps) hsSetPendingShared(v bool)                       { f.pending = v }
func (f *fakeHandshakeDeps) hsGetPendingSharedWorkingDir() string            { return f.pendingDir }
func (f *fakeHandshakeDeps) hsSetPendingSharedWorkingDir(dir string)         { f.pendingDir = dir }
func (f *fakeHandshakeDeps) hsGetPendingSharedMcpServers() []acp.McpServer   { return f.pendingMcpSrv }
func (f *fakeHandshakeDeps) hsSetPendingSharedMcpServers(s []acp.McpServer)  { f.pendingMcpSrv = s }
func (f *fakeHandshakeDeps) hsGetPendingSharedModes() *acp.SessionModeState  { return f.pendingModes }
func (f *fakeHandshakeDeps) hsSetPendingSharedModes(m *acp.SessionModeState) { f.pendingModes = m }
func (f *fakeHandshakeDeps) hsGetPendingSharedModels() *SessionModelState {
	return f.pendingModels
}
func (f *fakeHandshakeDeps) hsSetPendingSharedModels(m *SessionModelState) {
	f.pendingModels = m
}
func (f *fakeHandshakeDeps) hsGetPendingSharedModelConfigId() acp.SessionConfigId {
	return f.pendingModelCfgId
}
func (f *fakeHandshakeDeps) hsSetPendingSharedModelConfigId(id acp.SessionConfigId) {
	f.pendingModelCfgId = id
}

func (f *fakeHandshakeDeps) hsHandshakeLock()   { f.handshakeMu.Lock() }
func (f *fakeHandshakeDeps) hsHandshakeUnlock() { f.handshakeMu.Unlock() }

func (f *fakeHandshakeDeps) hsInitACPProcessDone(_ <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.processDonesSet++
}
func (f *fakeHandshakeDeps) hsSetResumeMethod(method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeMethod = method
}
func (f *fakeHandshakeDeps) hsGetResumeMethod() string { return f.resumeMethod }

func (f *fakeHandshakeDeps) hsStartMcpServer(_ acp.AgentCapabilities) []acp.McpServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startMcpCalls++
	return nil
}
func (f *fakeHandshakeDeps) hsStopMcpServer() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopMcpCalls++
}
func (f *fakeHandshakeDeps) hsApplySessionModes(m *acp.SessionModeState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliedModes = append(f.appliedModes, m)
}
func (f *fakeHandshakeDeps) hsApplyAgentModels(m *SessionModelState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliedModels = append(f.appliedModels, m)
}
func (f *fakeHandshakeDeps) hsApplyAgentModelConfigId(id acp.SessionConfigId) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliedModelCfgId = id
}
func (f *fakeHandshakeDeps) hsLogAgentModels(_ *SessionModelState) {}
func (f *fakeHandshakeDeps) hsPersistACPSessionID() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistedACPID++
}
func (f *fakeHandshakeDeps) hsClearPersistedACPSessionID() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearedACPID++
}
func (f *fakeHandshakeDeps) hsNotifyObservers(fn func(SessionObserver)) {
	fn(&handshakeRecorderObserver{deps: f})
}

// mitto-3mv WI-2: cold-start trace stubs — no-op in tests.
func (f *fakeHandshakeDeps) hsColdPhase(_ string, _ ...any)       {}
func (f *fakeHandshakeDeps) hsMarkMcpInitStart()                  {}
func (f *fakeHandshakeDeps) hsMarkMcpInitEnd()                    {}
func (f *fakeHandshakeDeps) hsFinishColdTrace(_ string, _ ...any) {}
func (f *fakeHandshakeDeps) hsColdTraceCtx(base context.Context) context.Context {
	return base
}

// fakeSeqProvider satisfies SeqProvider for WebClientConfig.
type fakeSeqProvider struct{}

func (f *fakeSeqProvider) GetNextSeq() int64 { return 0 }

// handshakeRecorderObserver records observer events.
type handshakeRecorderObserver struct{ deps *fakeHandshakeDeps }

func (r *handshakeRecorderObserver) record(s string) {
	r.deps.mu.Lock()
	r.deps.notifiedEvents = append(r.deps.notifiedEvents, s)
	r.deps.mu.Unlock()
}
func (r *handshakeRecorderObserver) OnACPStarted()                                 { r.record("acp_started") }
func (r *handshakeRecorderObserver) OnACPStopped(string)                           {}
func (r *handshakeRecorderObserver) OnAgentMessage(int64, string)                  {}
func (r *handshakeRecorderObserver) OnAgentThought(int64, string)                  {}
func (r *handshakeRecorderObserver) OnToolCall(int64, string, string, string)      {}
func (r *handshakeRecorderObserver) OnToolUpdate(int64, string, *string)           {}
func (r *handshakeRecorderObserver) OnPlan(int64, []PlanEntry)                     {}
func (r *handshakeRecorderObserver) OnFileWrite(int64, string, int)                {}
func (r *handshakeRecorderObserver) OnFileRead(int64, string, int)                 {}
func (r *handshakeRecorderObserver) OnContextUsageUpdate(int, int)                 {}
func (r *handshakeRecorderObserver) OnAvailableCommandsUpdated([]AvailableCommand) {}
func (r *handshakeRecorderObserver) OnQueueMessageSending(string)                  {}
func (r *handshakeRecorderObserver) OnQueueMessageSent(string)                     {}
func (r *handshakeRecorderObserver) OnQueueUpdated(int, string, string)            {}
func (r *handshakeRecorderObserver) OnQueueReordered([]session.QueuedMessage)      {}
func (r *handshakeRecorderObserver) OnError(string)                                {}
func (r *handshakeRecorderObserver) OnPromptComplete(int)                          {}
func (r *handshakeRecorderObserver) OnActionButtons([]ActionButton)                {}
func (r *handshakeRecorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int) {
}
func (r *handshakeRecorderObserver) OnUIPrompt(UIPromptRequest)       {}
func (r *handshakeRecorderObserver) OnUIPromptDismiss(string, string) {}
func (r *handshakeRecorderObserver) OnNotification(UINotifyRequest)   {}

// --- Tests ---

func TestHandshaker_CreationRPCCtx_NoDeadline_NoPropagatedDeadline(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	// No deadline on sessionCtx → per-attempt timeout now lives in SharedACPProcess.NewSession
	// (mitto-4no7), so creationRPCCtx should return a plain cancellable context with no deadline.
	ctx, cancel := c.creationRPCCtx(d)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("expected no deadline on creation RPC context (per-attempt timeout lives in NewSession)")
	}
}

func TestHandshaker_CreationRPCCtx_WithDeadline_HonoursIt(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	deadline := time.Now().Add(5 * time.Second)
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), deadline)
	defer deadlineCancel()
	d.creationCtx = deadlineCtx

	ctx, cancel := c.creationRPCCtx(d)
	defer cancel()
	got, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be preserved")
	}
	if !got.Equal(deadline) {
		t.Fatalf("expected deadline=%v, got=%v", deadline, got)
	}
}

func TestHandshaker_EnsureSharedACPSession_AlreadyDone(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pending = false // already done
	d.sharedProcess = newFakeSharedProcess()

	err := c.ensureSharedACPSession(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No NewSession call expected.
	fp := d.sharedProcess.(*fakeSharedProcess)
	if len(fp.newSessionCalls) != 0 {
		t.Fatalf("expected no NewSession call, got %v", fp.newSessionCalls)
	}
}

func TestHandshaker_EnsureSharedACPSession_PendingTrue_Success(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pending = true
	d.pendingDir = "my/working/dir"
	fp := newFakeSharedProcess()
	d.sharedProcess = fp

	err := c.ensureSharedACPSession(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fp.newSessionCalls) != 1 || fp.newSessionCalls[0] != "my/working/dir" {
		t.Fatalf("expected NewSession called with dir, got %v", fp.newSessionCalls)
	}
	if d.acpID != "acp-sess-1" {
		t.Fatalf("expected acpID set to 'acp-sess-1', got %q", d.acpID)
	}
	if d.pending {
		t.Fatal("expected pendingShared cleared after successful handshake")
	}
	if len(fp.registeredSessions) != 1 {
		t.Fatalf("expected 1 RegisterSession call, got %d", len(fp.registeredSessions))
	}
}

func TestHandshaker_EnsureSharedACPSession_RPCError_LeavesPending(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pending = true
	fp := newFakeSharedProcess()
	fp.newSessionErr = errors.New("rpc fail")
	d.sharedProcess = fp

	err := c.ensureSharedACPSession(d)
	if err == nil {
		t.Fatal("expected error on NewSession failure")
	}
	if !d.pending {
		t.Fatal("expected pendingShared to remain true after RPC error (retryable)")
	}
}

func TestHandshaker_ApplyPendingSharedModes_NilModes(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pendingModes = nil
	d.pendingModels = nil

	c.applyPendingSharedModes(d)

	if len(d.appliedModes) != 0 || len(d.appliedModels) != 0 {
		t.Fatal("expected no mode/model application when both pending are nil")
	}
}

func TestHandshaker_ApplyPendingSharedModes_Applies(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pendingModes = &acp.SessionModeState{CurrentModeId: "code"}
	d.pendingModels = &SessionModelState{CurrentModelId: "m-1"}

	c.applyPendingSharedModes(d)

	if len(d.appliedModes) != 1 || d.appliedModes[0].CurrentModeId != "code" {
		t.Fatalf("expected mode 'code' applied, got %v", d.appliedModes)
	}
	if len(d.appliedModels) != 1 {
		t.Fatalf("expected models applied, got %v", d.appliedModels)
	}
	// Verify stash was cleared.
	if d.pendingModes != nil || d.pendingModels != nil {
		t.Fatal("expected pending modes/models cleared after apply")
	}
}

func TestHandshaker_CompleteDeferredHandshake_NotPending_NoOp(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.sharedProcess = newFakeSharedProcess()
	d.pending = false

	err := c.completeDeferredHandshake(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no observer notifications, got %v", d.notifiedEvents)
	}
}

func TestHandshaker_CompleteDeferredHandshake_NilSharedProcess_NoOp(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.sharedProcess = nil
	d.pending = true

	err := c.completeDeferredHandshake(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.notifiedEvents) != 0 {
		t.Fatal("expected no notifications when sharedProcess is nil")
	}
}

func TestHandshaker_CompleteDeferredHandshake_Success(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pending = true
	d.pendingDir = "cwd"
	fp := newFakeSharedProcess()
	d.sharedProcess = fp

	err := c.completeDeferredHandshake(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.persistedACPID != 1 {
		t.Fatalf("expected 1 persist call, got %d", d.persistedACPID)
	}
	if len(d.notifiedEvents) != 1 || d.notifiedEvents[0] != "acp_started" {
		t.Fatalf("expected acp_started notification, got %v", d.notifiedEvents)
	}
}

func TestHandshaker_CompleteDeferredHandshake_RPCError_Propagates(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pending = true
	fp := newFakeSharedProcess()
	fp.newSessionErr = errors.New("fail")
	d.sharedProcess = fp

	err := c.completeDeferredHandshake(d)
	if err == nil {
		t.Fatal("expected error when NewSession fails")
	}
	if d.persistedACPID != 0 {
		t.Fatal("expected no persist call on error")
	}
	if len(d.notifiedEvents) != 0 {
		t.Fatal("expected no observer notification on error")
	}
}

func TestHandshaker_Prewarm_NilProcess_NoOp(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.sharedProcess = nil

	c.prewarmACPSession(d) // must not panic

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notifications with nil sharedProcess, got %v", d.notifiedEvents)
	}
}

func TestHandshaker_Prewarm_RPCError_LogsWarning(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	d.pending = true
	fp := newFakeSharedProcess()
	fp.newSessionErr = errors.New("prewarm fail")
	d.sharedProcess = fp

	c.prewarmACPSession(d) // must not panic or propagate error

	// No notification since the RPC failed.
	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notifications, got %v", d.notifiedEvents)
	}
}

func TestHandshaker_PrepareSharedACPSession_SetsFields(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()

	err := c.prepareSharedACPSession(d, fp, "my/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.sharedProcess != fp {
		t.Fatal("expected sharedProcess set")
	}
	if !d.pending {
		t.Fatal("expected pendingShared=true after prepare")
	}
	if d.pendingDir != "my/dir" {
		t.Fatalf("expected pendingDir='my/dir', got %q", d.pendingDir)
	}
	if d.acpClient == nil {
		t.Fatal("expected acpClient created")
	}
	if d.processDonesSet != 1 {
		t.Fatalf("expected 1 process done init, got %d", d.processDonesSet)
	}
	if d.niledCreation != 1 {
		t.Fatalf("expected creationCtx nilled, got %d", d.niledCreation)
	}
}

func TestHandshaker_ResumeSharedACPSession_CreatesNew_WhenNoID(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()

	err := c.resumeSharedACPSession(d, fp, "cwd", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fp.newSessionCalls) != 1 {
		t.Fatalf("expected 1 NewSession call, got %v", fp.newSessionCalls)
	}
	if d.acpID != "acp-sess-1" {
		t.Fatalf("expected acpID='acp-sess-1', got %q", d.acpID)
	}
	if len(d.notifiedEvents) != 1 || d.notifiedEvents[0] != "acp_started" {
		t.Fatalf("expected acp_started, got %v", d.notifiedEvents)
	}
	if d.resumeMethod != "new" {
		t.Fatalf("expected resumeMethod='new', got %q", d.resumeMethod)
	}
}

func TestHandshaker_ResumeSharedACPSession_RPCError_Cleans(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	fp.newSessionErr = errors.New("fail")

	err := c.resumeSharedACPSession(d, fp, "cwd", "")
	if err == nil {
		t.Fatal("expected error on NewSession failure")
	}
	if d.sharedProcess != nil {
		t.Fatal("expected sharedProcess nilled on failure")
	}
	if d.acpClient != nil {
		t.Fatal("expected acpClient nilled on failure")
	}
	if d.stopMcpCalls != 1 {
		t.Fatalf("expected stopMcpServer called on failure, got %d", d.stopMcpCalls)
	}
}

// TestIsSessionNotFoundErr verifies the JSON-RPC -32602 classifier used to
// drive the LoadSession → NewSession fallback (mitto-z70).
func TestIsSessionNotFoundErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("boom"), false},
		{"wrong_code", &acp.RequestError{Code: -32603, Message: "Internal error"}, false},
		{"invalid_params", &acp.RequestError{Code: -32602, Message: "Invalid params"}, true},
		{"wrapped_invalid_params", fmt.Errorf("failed to load session: %w", &acp.RequestError{Code: -32602, Message: "session not found"}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSessionNotFoundErr(tc.err); got != tc.want {
				t.Fatalf("isSessionNotFoundErr(%v)=%v want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestHandshaker_ResumeSharedACPSession_LoadNotFound_FallsBackToNewSessionOnce
// is the mitto-z70 regression: when LoadSession returns JSON-RPC -32602
// ("session not found"), the handshaker must fall back to EXACTLY ONE
// NewSession call — not retry LoadSession — and complete the handshake with
// resumeMethod="new".
func TestHandshaker_ResumeSharedACPSession_LoadNotFound_FallsBackToNewSessionOnce(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	// Enable LoadSession capability so the handshaker attempts it, and disable
	// Resume so we exercise the load-path branch specifically.
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	fp.loadSessionErr = &acp.RequestError{Code: -32602, Message: "session not found"}

	err := c.resumeSharedACPSession(d, fp, "cwd", "stale-acp-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(fp.loadSessionCalls); got != 1 {
		t.Fatalf("expected exactly 1 LoadSession call, got %d (%v)", got, fp.loadSessionCalls)
	}
	if fp.loadSessionCalls[0] != "stale-acp-id" {
		t.Fatalf("expected LoadSession called with stale id, got %q", fp.loadSessionCalls[0])
	}
	if got := len(fp.newSessionCalls); got != 1 {
		t.Fatalf("expected exactly 1 NewSession fallback, got %d (%v)", got, fp.newSessionCalls)
	}
	if d.resumeMethod != "new" {
		t.Fatalf("expected resumeMethod=%q, got %q", "new", d.resumeMethod)
	}
	if d.acpID != "acp-sess-1" {
		t.Fatalf("expected acpID from NewSession, got %q", d.acpID)
	}
	// The stale persisted acp_session_id must be cleared on load failure so the
	// next cold start does not re-probe a known-bad id (doomed session/load).
	if d.clearedACPID != 1 {
		t.Fatalf("expected 1 clear-persisted call on load failure, got %d", d.clearedACPID)
	}
}

// TestHandshaker_ResumeSharedACPSession_ColdBudget_DoesNotStack is the mitto-1ut
// regression: on a cold shared process (RecommendedLoadTimeout > 0), a stale-load
// probe followed by the session/new fallback must share ONE wall-clock budget
// rather than stacking two. It asserts (a) the load probe is capped at ≤
// staleLoadProbeTimeout so a doomed load fails fast, and (b) the fallback
// NewSession's context deadline never exceeds the single handshake budget
// (now + RecommendedLoadTimeout) — i.e. the old 240s + 240s = 480s stacking is gone.
func TestHandshaker_ResumeSharedACPSession_ColdBudget_DoesNotStack(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	fp.loadSessionErr = &acp.RequestError{Code: -32602, Message: "session not found"}
	// Cold process: mimics MCPInitTimeout=240s.
	const coldBudget = 240 * time.Second
	fp.recommendedLoadTimeout = coldBudget

	start := time.Now()
	if err := c.resumeSharedACPSession(d, fp, "cwd", "stale-acp-id"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Load probe must be capped at the short stale-probe timeout, NOT the full
	// cold budget — otherwise a stale id burns ~240s before falling back.
	if !fp.loadCtxHasDeadline {
		t.Fatal("expected LoadSession to receive a deadline (capped probe)")
	}
	loadBudget := fp.loadCtxDeadline.Sub(start)
	if loadBudget > staleLoadProbeTimeout+time.Second {
		t.Fatalf("load probe budget %v exceeds staleLoadProbeTimeout %v (stale probe not capped)", loadBudget, staleLoadProbeTimeout)
	}

	// Fallback NewSession must be bounded by the SINGLE shared handshake budget
	// (start + coldBudget), never start + loadBudget + coldBudget.
	if !fp.newCtxHasDeadline {
		t.Fatal("expected NewSession to receive a shared-budget deadline in the resume path")
	}
	newBudget := fp.newCtxDeadline.Sub(start)
	// Generous upper bound: the shared budget plus a small scheduling slack.
	if newBudget > coldBudget+time.Second {
		t.Fatalf("NewSession budget %v exceeds shared handshake budget %v — budgets are stacking (mitto-1ut regression)", newBudget, coldBudget)
	}
	// And it must be meaningfully large (not accidentally capped to the probe).
	if newBudget < coldBudget-staleLoadProbeTimeout-time.Second {
		t.Fatalf("NewSession budget %v unexpectedly small vs shared budget %v", newBudget, coldBudget)
	}
}

// TestHandshaker_ResumeSharedACPSession_WarmBudget_ProbeCappedNoNewDeadline
// verifies the warm path (RecommendedLoadTimeout == 0): the stale-load probe is
// still capped at staleLoadProbeTimeout so a stale id fails fast, but the
// session/new fallback derives its OWN budget (no shared handshake deadline is
// imposed by the resume path), matching pre-mitto-1ut warm behaviour.
func TestHandshaker_ResumeSharedACPSession_WarmBudget_ProbeCappedNoNewDeadline(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	fp.loadSessionErr = &acp.RequestError{Code: -32602, Message: "session not found"}
	fp.recommendedLoadTimeout = 0 // warm

	start := time.Now()
	if err := c.resumeSharedACPSession(d, fp, "cwd", "stale-acp-id"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !fp.loadCtxHasDeadline {
		t.Fatal("expected LoadSession to receive a capped-probe deadline even when warm")
	}
	loadBudget := fp.loadCtxDeadline.Sub(start)
	if loadBudget > staleLoadProbeTimeout+time.Second {
		t.Fatalf("warm load probe budget %v exceeds staleLoadProbeTimeout %v", loadBudget, staleLoadProbeTimeout)
	}
	// Warm: resume path imposes no shared deadline on the NewSession fallback.
	if fp.newCtxHasDeadline {
		t.Fatalf("expected no resume-imposed deadline on NewSession when warm, got deadline in %v", fp.newCtxDeadline.Sub(start))
	}
}

// TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_NoNewDeadline is the
// mitto-1ut STARVATION fix: on a cold shared process (RecommendedLoadTimeout > 0)
// whose load PROBE fails by DEADLINE (the process is genuinely cold / mid-MCP-init,
// NOT a stale id returning a fast -32602), the session/new FALLBACK must NOT inherit
// the truncated remainder of the shared handshake deadline. Otherwise NewSession gets
// only (budget - ~probe) < one MCPInitTimeout attempt, attempt 1 is truncated and
// attempt 2 is aborted with "context cancelled before attempt 2" — guaranteeing
// failure on exactly the cold/contended case. The fix releases the shared cap when the
// probe timed out, letting NewSession derive its own bounded budget (no resume-imposed
// deadline). Contrast with ColdBudget_DoesNotStack, where the probe fails FAST (-32602)
// and the shared cap IS retained to prevent stacking.
func TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_NoNewDeadline(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	// Small cold budget so the capped probe expires quickly in the test; the probe
	// blocks until its ctx deadline and returns context.DeadlineExceeded (a TIMEOUT,
	// not a -32602), which must set probeTimedOut and release the shared cap.
	fp.recommendedLoadTimeout = 150 * time.Millisecond
	fp.loadBlocksUntilCtxDone = true

	if err := c.resumeSharedACPSession(d, fp, "cwd", "stale-acp-id"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The probe must have run (and timed out).
	if len(fp.loadSessionCalls) != 1 {
		t.Fatalf("expected exactly 1 LoadSession probe, got %d", len(fp.loadSessionCalls))
	}
	if !fp.loadCtxHasDeadline {
		t.Fatal("expected LoadSession probe to receive a capped deadline")
	}
	// The fallback NewSession must have been called...
	if len(fp.newSessionCalls) != 1 {
		t.Fatalf("expected exactly 1 NewSession fallback, got %d", len(fp.newSessionCalls))
	}
	// ...and it must NOT carry a resume-imposed (truncated) deadline: the timed-out
	// probe proves the process is cold, so NewSession derives its OWN budget instead
	// of the shared handshakeDeadline remainder (mitto-1ut starvation fix).
	if fp.newCtxHasDeadline {
		t.Fatalf("expected NO resume-imposed deadline on NewSession after a TIMED-OUT probe (mitto-1ut starvation fix), but got one")
	}
}

// TestHandshaker_StaleLoadProbeTimeout_CeilingCap is the mitto-54k.9 regression:
// even after mitto-1ut capped the doomed LoadSession probe, field evidence
// (2026-07-14 logs) shows the cap at 45s stacks with the mitto-1ut starvation
// exception's fresh NewSession budget (up to MCPInitTimeout=240s) for a
// worst-case wall-clock wedge of ~285s (rpc_ms=45001 probe + rpc_ms=240001
// session/new on cgw-managed-tools).
//
// The MCP-init abort signal from agent stderr empirically fires between 11-25s
// when it fires at all; when it doesn't (silent-stall cold processes), only
// the cap catches the probe. A 45s cap is therefore ~2x the observed abort
// window and admits ~20s of avoidable stacking. Tightening the ceiling to
// ≤25s aligns with observed abort behaviour and reduces the worst-case wedge
// from ~285s to ~265s without regressing legit warm loads (which resolve in
// well under 25s).
//
// This test fails on the current 45s constant and passes once the cap is
// tightened to ≤25s. Do NOT relax this ceiling without re-checking field logs
// for the actual MCP-init abort-signal window.
func TestHandshaker_StaleLoadProbeTimeout_CeilingCap(t *testing.T) {
	const ceiling = 25 * time.Second
	if staleLoadProbeTimeout > ceiling {
		t.Fatalf("staleLoadProbeTimeout=%v exceeds mitto-54k.9 ceiling of %v — "+
			"a doomed cold load probe stacks with the session/new fallback's fresh "+
			"MCPInitTimeout budget (mitto-1ut starvation exception releases the shared "+
			"cap on probe DeadlineExceeded), producing a ~%v worst-case wedge. "+
			"Tighten the constant to align with the observed 11-25s MCP-init abort-signal "+
			"window.",
			staleLoadProbeTimeout, ceiling, staleLoadProbeTimeout+240*time.Second)
	}
}
