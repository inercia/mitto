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

	// promptCalls records every Prompt() invocation (session ID + content blocks
	// handed to the transport). Used by mitto-ip1 to pin the exact payload
	// BackgroundSession.FlushContext sends to the agent.
	promptCalls []fakeSharedProcessPromptCall

	// promptBlock, when non-nil, makes Prompt() block until the channel is
	// closed before returning — used by mitto-p10q's dispatch-precedence test
	// to hold a loop dispatch claim open across a checkSession call so a
	// second, lower-precedence trigger observes it still in flight.
	promptBlock chan struct{}
}

// fakeSharedProcessPromptCall records a single Prompt() call on fakeSharedProcess.
type fakeSharedProcessPromptCall struct {
	sessionID acp.SessionId
	blocks    []acp.ContentBlock
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
func (f *fakeSharedProcess) Prompt(_ context.Context, sessionID acp.SessionId, blocks []acp.ContentBlock) (acp.PromptResponse, error) {
	f.mu.Lock()
	f.promptCalls = append(f.promptCalls, fakeSharedProcessPromptCall{sessionID: sessionID, blocks: blocks})
	block := f.promptBlock
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return acp.PromptResponse{}, nil
}
func (f *fakeSharedProcess) SetSessionMode(_ context.Context, _ acp.SessionId, _ string) error {
	return nil
}
func (f *fakeSharedProcess) SetSessionModel(_ context.Context, _ acp.SessionId, _ string) error {
	return nil
}
func (f *fakeSharedProcess) Generation() int     { return 0 }
func (f *fakeSharedProcess) Restart(_ int) error { return nil }
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
	persistedACPID   int
	clearedACPID     int
	notifiedEvents   []string
	appliedModes     []*acp.SessionModeState
	appliedModels    []*SessionModelState
	synthesizedCalls int // mitto-886: hsApplySynthesizedModelsIfEmpty invocations
	startMcpCalls    int
	stopMcpCalls     int
	processDonesSet  int
	niledCreation    int

	// === New in mitto-s9g2: ACP context virginity tracking ===
	markFreshCalls   int
	markUnknownCalls int
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

func (f *fakeHandshakeDeps) hsMarkContextFresh()   { f.markFreshCalls++ }
func (f *fakeHandshakeDeps) hsMarkContextUnknown() { f.markUnknownCalls++ }

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
func (f *fakeHandshakeDeps) hsApplySynthesizedModelsIfEmpty() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.synthesizedCalls++
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
func (r *handshakeRecorderObserver) OnAgentMessage(int64, string, string)          {}
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
func (r *handshakeRecorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int, map[string]string) {
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

// TestHandshaker_ResumeSharedACPSession_DeletionCancellationSkipsNewSession
// reproduces mitto-f9mt: deleting a conversation while its LoadSession probe is
// blocked must cancel the resume without falling through to session/new.
func TestHandshaker_ResumeSharedACPSession_DeletionCancellationSkipsNewSession(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	resumeCtx, cancelResume := context.WithCancel(context.Background())
	d.creationCtx = resumeCtx
	fp := newFakeSharedProcess()
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	fp.loadBlocksUntilCtxDone = true

	done := make(chan error, 1)
	go func() {
		done <- c.resumeSharedACPSession(d, fp, "cwd", "persisted-acp-id")
	}()

	deadline := time.Now().Add(time.Second)
	for {
		fp.mu.Lock()
		loadStarted := len(fp.loadSessionCalls) == 1
		fp.mu.Unlock()
		if loadStarted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for LoadSession to block")
		}
		time.Sleep(time.Millisecond)
	}

	// Simulate deletion invalidating the in-flight pending resume.
	cancelResume()
	err := <-done

	fp.mu.Lock()
	newCalls := len(fp.newSessionCalls)
	fp.mu.Unlock()
	if newCalls != 0 {
		t.Fatalf("NewSession calls after deletion cancellation = %d, want 0", newCalls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("resume error = %v, want context.Canceled", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.clearedACPID != 0 {
		t.Fatalf("persisted ACP session ID clear calls = %d, want 0", d.clearedACPID)
	}
	if d.stopMcpCalls != 1 || d.acpClient != nil || d.sharedProcess != nil {
		t.Fatalf("canceled handshake cleanup = (stopMCP=%d, client=%p, shared=%p), want (1, nil, nil)",
			d.stopMcpCalls, d.acpClient, d.sharedProcess)
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

// TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_FreshBoundedDeadline
// encodes the post-mitto-l9as invariant on the timed-out-probe path.
//
// mitto-1ut original criterion (still upheld): on a cold shared process
// (RecommendedLoadTimeout > 0) whose load PROBE fails by DEADLINE — NOT a
// stale id returning a fast -32602 — the session/new FALLBACK must NOT inherit
// the TRUNCATED remainder of the original shared handshake deadline. Otherwise
// NewSession gets only (budget - ~probe) < one MCPInitTimeout attempt, attempt 1
// is truncated, attempt 2 is aborted with "context cancelled before attempt 2",
// and the cold/contended case is guaranteed to fail (the starvation wedge).
//
// mitto-l9as addition: releasing the cap ENTIRELY — as the original mitto-1ut
// exception did — lets the fallback ctx run unbounded from the resume path's
// perspective, and on outcome=shared_resume_failed the operator sees the
// STACKED probe(25s) + fullNewBudget(~175s) ≈ 200s wedge (evidence:
// 2026-07-23T08:55:24 total_ms=199032). The fix restores a bounded ceiling
// while preserving the starvation criterion: impose a FRESH cold budget
// deadline from the current instant (RecommendedLoadTimeout again). NewSession
// still gets a full MCPInitTimeout attempt, and the combined wall-clock burn
// is capped at probe + oneColdBudget instead of probe + unbounded.
//
// Contrast: with ColdBudget_DoesNotStack the probe fails FAST (-32602),
// probeTimedOut=false, and the ORIGINAL shared cap IS retained to prevent
// stacking on the stale-id path.
func TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_FreshBoundedDeadline(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	// Small cold budget so the capped probe expires quickly in the test; the probe
	// blocks until its ctx deadline and returns context.DeadlineExceeded (a TIMEOUT,
	// not a -32602), which must set probeTimedOut and trigger the fresh-ceiling
	// branch of the fallback deadline logic.
	const coldBudget = 150 * time.Millisecond
	fp.recommendedLoadTimeout = coldBudget
	fp.loadBlocksUntilCtxDone = true

	beforeFallback := time.Now()
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

	// mitto-l9as: the fallback MUST carry a resume-imposed deadline (bounded
	// ceiling) — releasing the cap unconditionally is the exact behaviour that
	// caused the ~200s shared_resume_failed wedge.
	if !fp.newCtxHasDeadline {
		t.Fatal("mitto-l9as: expected a resume-imposed deadline on NewSession after a TIMED-OUT probe (fresh cold-budget ceiling), but got none")
	}
	// mitto-1ut starvation criterion: the deadline must be FRESH — at least
	// one full RecommendedLoadTimeout from the fallback's start — not the
	// truncated remainder of the original handshakeDeadline (which would leave
	// only ~zero after the probe drained its capped deadline).
	minFreshDeadline := beforeFallback.Add(coldBudget)
	if fp.newCtxDeadline.Before(minFreshDeadline) {
		t.Fatalf("mitto-1ut: expected fresh cold-budget ceiling on NewSession fallback "+
			"(>= now+%v = %v), got %v — this would truncate attempt 1 and starve "+
			"the cold/contended NewSession",
			coldBudget, minFreshDeadline, fp.newCtxDeadline)
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

// TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_NewSessionCombinedCap
// is the mitto-l9as reproduction for the STACKED-timeout wedge:
//
// On a genuinely cold shared process, a stale acp_session_id makes the
// LoadSession probe block until it hits staleLoadProbeTimeout (~25s,
// probeTimedOut=true). The mitto-1ut starvation exception then releases
// the shared handshake cap so the fallback NewSession derives its own
// coldMCPBudget (up to MCPInitTimeout ≈ 240s). If NewSession also fails
// (outcome=shared_resume_failed — 1× in the mitto-l9as 24h window), the
// user-visible wall-clock burn is loadProbe + fullNewBudget ≈ 200s.
//
// Field evidence (mitto-l9as investigation, log line at 2026-07-23T08:55:24):
//
//	phase timeline: sem_acquired@0ms -> mcp_init_wait_begin@0ms
//	  -> session_load_failed@25001ms -> session_new_failed@199032ms
//	  outcome=shared_resume_failed total_ms=199032
//
// This asserts a COMBINED user-visible latency ceiling on the resume path
// even on the shared_resume_failed sub-path. A reasonable ceiling is
// ~staleLoadProbeTimeout + one MCPInitTimeout — but the total_ms=199032
// evidence shows both being burned in full. The bug is that today's
// starvation exception (shared_session_handshaker.go:499) releases the cap
// UNCONDITIONALLY on probeTimedOut, without leaving any bounded ceiling on
// the fallback when it too is destined to fail — so the operator sees the
// stacked ~200s wedge instead of a bounded fail.
//
// This test FAILS on the current code (no combined cap after a timed-out
// probe) and will PASS once the fix caps the combined budget or fast-fails
// the NewSession fallback on a demonstrably-wedged cold process.
func TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_NewSessionCombinedCap(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	// Cold budget: mimics MCPInitTimeout=240s. Real production value.
	const coldBudget = 240 * time.Second
	fp.recommendedLoadTimeout = coldBudget
	// The probe will time out (silent-stall cold process, mid-MCP-init).
	fp.loadBlocksUntilCtxDone = true
	// The NewSession fallback will also fail — outcome=shared_resume_failed.
	// The reproduction fires irrespective of the error kind; use a plain
	// non-context error to isolate the budget/cap arithmetic from the caller's
	// error-classification path.
	fp.newSessionErr = errors.New("simulated cold NewSession failure")

	// Use a shorter staleLoadProbeTimeout via a real, but bounded, probe.
	// The fake blocks until ctx.Done(), so the load probe drains its capped
	// deadline (staleLoadProbeTimeout in production). We can't drop the
	// constant here, but the observable that matters is the CTX DEADLINE the
	// fallback NewSession receives — the arithmetic proof that the two
	// budgets do not stack.

	// Reproduction is arithmetic: we assert on the NewSession ctx deadline
	// (or lack thereof) rather than actually waiting 200s.
	_ = c.resumeSharedACPSession(d, fp, "cwd", "stale-acp-id")

	// The probe must have run and been capped (sanity check preconditions).
	if len(fp.loadSessionCalls) != 1 {
		t.Fatalf("expected exactly 1 LoadSession probe, got %d", len(fp.loadSessionCalls))
	}
	if !fp.loadCtxHasDeadline {
		t.Fatal("expected LoadSession probe to carry a capped deadline (preconditions)")
	}
	// The fallback NewSession must have been invoked.
	if len(fp.newSessionCalls) != 1 {
		t.Fatalf("expected exactly 1 NewSession fallback after probe timeout, got %d", len(fp.newSessionCalls))
	}

	// mitto-l9as: the COMBINED user-visible latency on shared_resume_failed
	// must be bounded by a single cold budget, not probeTimeout + coldBudget.
	// The observable proxy: the NewSession ctx deadline (if any) must not
	// exceed a single cold budget measured from the start of the WHOLE
	// resume attempt. Today, mitto-1ut's starvation exception releases the
	// shared cap on probeTimedOut, so NewSession derives its OWN coldBudget
	// on top of the ~probe burn — which is exactly the wedge.
	//
	// After the fix the deadline should be present AND ceiling-bounded, i.e.
	// the fallback NewSession must NOT be handed an unbounded (or MCPInitTimeout-
	// fresh) budget on top of a proven-wedged probe.
	if !fp.newCtxHasDeadline {
		t.Errorf("mitto-l9as: NewSession fallback received NO ctx deadline "+
			"after a TIMED-OUT cold probe — the mitto-1ut starvation exception "+
			"releases the shared cap unconditionally, so NewSession derives its "+
			"OWN coldMCPBudget (up to MCPInitTimeout ≈ %v) on top of the ~%v probe "+
			"burn. On outcome=shared_resume_failed the operator sees the STACKED "+
			"~%v wedge (evidence: mitto-l9as 2026-07-23T08:55:24 total_ms=199032). "+
			"The fix must impose a bounded ceiling on the fallback budget when the "+
			"probe timed out — either a combined cap (probe + new ≤ one cold budget) "+
			"or a fast-fail of the fallback on a demonstrably-wedged cold process.",
			coldBudget, staleLoadProbeTimeout, staleLoadProbeTimeout+coldBudget)
	}
}

// TestHandshaker_ResumeSharedACPSession_CombinedWallClock_BoundedBySingleColdBudget
// is the mitto-s1rt.1 reproduction.
//
// mitto-s1rt.1 investigation: the 265s field outage (probe 25001ms +
// session/new 240000ms = 265009ms total) is not an unbounded stack — it is
// exactly staleLoadProbeTimeout + one fresh MCPInitTimeout, because the
// mitto-1ut/mitto-l9as starvation exception (shared_session_handshaker.go,
// "fallbackDeadline = time.Now().Add(rec)") grants the session/new fallback a
// FRESH full cold budget measured from AFTER the probe already burned its own
// staleLoadProbeTimeout. TestHandshaker_ResumeSharedACPSession_ColdProbeTimeout_
// NewSessionCombinedCap only asserts the fallback ctx HAS a deadline — it does
// not bound the deadline's VALUE relative to the start of the whole resume
// attempt, so today's "probe + fresh full budget" arithmetic passes it.
//
// This test measures the fallback deadline from the START of the WHOLE resume
// attempt (matching how an operator experiences total_ms in cold_start_summary)
// and asserts it does not exceed roughly ONE cold budget. On HEAD the fallback
// gets probe-time + a full fresh cold budget on top, i.e. close to 2x the cold
// budget — this test FAILS on HEAD and must PASS once the fix bounds the
// combined budget (e.g. reduces the fallback's budget by however much the
// probe already spent, or fails the handshake fast instead of paying a second
// full budget).
func TestHandshaker_ResumeSharedACPSession_CombinedWallClock_BoundedBySingleColdBudget(t *testing.T) {
	c := sharedSessionHandshaker{}
	d := newFakeHandshakeDeps()
	fp := newFakeSharedProcess()
	fp.caps = &acp.AgentCapabilities{LoadSession: true}
	// Small cold budget so the capped probe (and any fresh fallback budget)
	// resolve quickly in the test; staleLoadProbeTimeout (25s) is much larger
	// than this, so the probe's effective deadline is capped by coldBudget
	// itself (min(staleLoadProbeTimeout, remaining) == coldBudget here) —
	// mirroring the production shape where the probe is the binding constraint
	// against a large remaining handshake budget.
	const coldBudget = 200 * time.Millisecond
	fp.recommendedLoadTimeout = coldBudget
	// The probe blocks until its ctx deadline and returns DeadlineExceeded —
	// a genuinely cold/wedged process, not a fast -32602 stale-id response.
	fp.loadBlocksUntilCtxDone = true
	fp.newSessionErr = errors.New("simulated cold NewSession failure")

	start := time.Now()
	_ = c.resumeSharedACPSession(d, fp, "cwd", "stale-acp-id")

	if len(fp.loadSessionCalls) != 1 {
		t.Fatalf("expected exactly 1 LoadSession probe, got %d", len(fp.loadSessionCalls))
	}
	if len(fp.newSessionCalls) != 1 {
		t.Fatalf("expected exactly 1 NewSession fallback after probe timeout, got %d", len(fp.newSessionCalls))
	}
	if !fp.newCtxHasDeadline {
		t.Fatal("expected NewSession fallback to carry a bounded deadline after a timed-out probe")
	}

	// mitto-s1rt.1: combined wall-clock burn (probe + fallback), measured from
	// the START of the whole resume attempt, must not exceed roughly ONE cold
	// budget plus a small scheduling margin. On HEAD it is close to 2x
	// coldBudget (probe drains coldBudget, then the fallback gets a FRESH
	// coldBudget from that later instant) — reproducing the field evidence
	// where probe(25001ms) + new(240000ms) = 265009ms, i.e. ~(25s + 240s),
	// not bounded by a single ~240s ceiling.
	combined := fp.newCtxDeadline.Sub(start)
	maxAllowed := coldBudget + coldBudget/2 // generous margin, still << 2x
	if combined > maxAllowed {
		t.Fatalf("mitto-s1rt.1: combined resume wall-clock budget %v exceeds single-cold-budget "+
			"ceiling %v (coldBudget=%v) — the session/new fallback is granted a FRESH full cold "+
			"budget on top of the probe's own %v burn instead of sharing one budget. Field evidence: "+
			"session_load_failed@25001ms -> session_new_failed@265009ms (staleLoadProbeTimeout=%v + "+
			"MCPInitTimeout=240s = 265s doomed-agent outage).",
			combined, maxAllowed, coldBudget, coldBudget, staleLoadProbeTimeout)
	}
}
