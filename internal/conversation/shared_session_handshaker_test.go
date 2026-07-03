package conversation

import (
	"context"
	"errors"
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
	registeredSessions []acp.SessionId
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
func (f *fakeSharedProcess) NewSession(_ context.Context, cwd string, _ []acp.McpServer) (*SessionHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newSessionCalls = append(f.newSessionCalls, cwd)
	return f.newSessionHandle, f.newSessionErr
}
func (f *fakeSharedProcess) LoadSession(_ context.Context, _, _ string, _ []acp.McpServer) (*SessionHandle, error) {
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
func (f *fakeSharedProcess) Restart() error                                                      { return nil }
func (f *fakeSharedProcess) SetPromptFunc(_ func(context.Context, string, string, string) error) {}
func (f *fakeSharedProcess) PromptProcessorAsync(_ context.Context, _, _, _ string) error {
	return nil
}

// fakeHandshakeDeps is a test double for handshakeDeps.
type fakeHandshakeDeps struct {
	mu sync.Mutex

	// state knobs
	sessionID     string
	logger        *slog.Logger
	sessionCtx    context.Context
	creationCtx   context.Context
	sharedProcess SharedProcess
	acpClient     *WebClient
	agentImages   bool
	acpID         string
	pending       bool
	pendingDir    string
	pendingMcpSrv []acp.McpServer
	pendingModes  *acp.SessionModeState
	pendingModels *acp.UnstableSessionModelState
	resumeMethod  string

	// mutexes for pending/handshake
	pendingMu   sync.Mutex
	handshakeMu sync.Mutex

	// recorders
	persistedACPID  int
	notifiedEvents  []string
	appliedModes    []*acp.SessionModeState
	appliedModels   []*acp.UnstableSessionModelState
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
func (f *fakeHandshakeDeps) hsGetPendingSharedModels() *acp.UnstableSessionModelState {
	return f.pendingModels
}
func (f *fakeHandshakeDeps) hsSetPendingSharedModels(m *acp.UnstableSessionModelState) {
	f.pendingModels = m
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
func (f *fakeHandshakeDeps) hsApplyAgentModels(m *acp.UnstableSessionModelState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliedModels = append(f.appliedModels, m)
}
func (f *fakeHandshakeDeps) hsLogAgentModels(_ *acp.UnstableSessionModelState) {}
func (f *fakeHandshakeDeps) hsPersistACPSessionID() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistedACPID++
}
func (f *fakeHandshakeDeps) hsNotifyObservers(fn func(SessionObserver)) {
	fn(&handshakeRecorderObserver{deps: f})
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
	d.pendingModels = &acp.UnstableSessionModelState{CurrentModelId: "m-1"}

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
