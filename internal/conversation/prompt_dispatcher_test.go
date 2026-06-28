package conversation

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// compile-time check.
var _ promptDeps = (*fakePromptDeps)(nil)

type fakePromptDeps struct {
	mu sync.Mutex

	resolver    PromptResolver
	workingDir  string
	agentImages bool
	hasStore    bool

	// per-ID path/error maps
	imagePaths map[string]string
	imageErrs  map[string]error
	filePaths  map[string]string
	fileErrs   map[string]error

	// recorders
	notifiedErrors []string
	logger         *slog.Logger
	sessionID      string

	// === New in 2.5-b ===
	workspaceUUID                  string
	availableACPServers            []processors.AvailableACPServer
	workspaceProcessorArgOverrides map[string]map[string]string
	sessionMeta            session.Metadata
	sessionMetaErr         error
	metaByID               map[string]session.Metadata
	childSessions          []session.Metadata
	childSessionsErr       error
	childPrompting         map[string]bool
	mcpToolNames           []string
	userData               *session.UserData
	userDataErr            error
	sessionCtx             context.Context
	hasProcessorMgr        bool
	applyResult            *processors.ProcessorResult
	applyErr               error
	persistActivationCalls int
	historyPrefix          string // prefix injected by pdBuildPromptWithHistory

	// === New in 2.5-c ===
	hasSharedProcess    bool
	handshakeErr        error
	handshakeCalls      int
	hasRecorder         bool
	recordedErrorEvents []string
	nextSeq             int64
	refreshSeqCalls     int
	promptingResetCalls int
	streamingChanges    []bool
	hasACPConn          bool
	acpNewSessionID     string
	acpNewSessionErr    error
	agentModels         *acp.UnstableSessionModelState
	resolvedPreferred   []string
	baselineModel       string
	overrideActive      bool
	setActiveModelCalls []string
	setActiveModelErr   error

	// === New in 2.5-d ===
	lastUsageSet          *acp.Usage
	accumulatedTokens     []int
	estimatedTokenCalls   []string // messages passed to pdEstimateTokensFromMessage
	lastAgentMessage      string   // returned by pdReadLastAgentMessage / pdReadLastAgentMessageFromStore
	markCompleteCount     int
	isClosed              bool
	flushMarkdownCount    int
	observerCount         int
	eventCount            int
	flushConfigCount      int
	processNextCalled     int
	processNextResult     bool // return value for pdProcessNextQueuedMessage
	retryTitleCalls       []string
	actionButtonsOn       bool
	immediateQueue        bool
	followUpCalls         [][]string // each element is [userMsg, agentMsg]
	afterProcessorCalls   int
	turnIdleCalls         int
	selfDestructRequested bool
	selfDestructCalls     int
	onCompleteCallOrder   []string // records "OnComplete" / "TurnIdle" / "SelfDestruct" in order

	// === New in 2.5-e ===
	acpDead        bool
	canRestart     bool
	restartInfo    string
	restartErr     error
	restartCalled  int
	reacquireCalls int

	// === mitto-pchx.3: per-conversation prompt-argument cache ===
	// promptParams is returned by pdResolvePromptParameters (nil ⇒ resolver returns nil).
	promptParams []config.PromptParameter
	// argCache is a real per-conversation cache backing pdCacheGetArg/pdCacheSetArg so
	// dispatcher tests can exercise the merge + write-back path end-to-end.
	argCache *promptArgCache
}

func newFakePromptDeps() *fakePromptDeps {
	return &fakePromptDeps{
		logger:         slog.Default(),
		sessionID:      "test-session",
		hasStore:       true,
		agentImages:    true,
		imagePaths:     make(map[string]string),
		imageErrs:      make(map[string]error),
		filePaths:      make(map[string]string),
		fileErrs:       make(map[string]error),
		metaByID:       make(map[string]session.Metadata),
		childPrompting: make(map[string]bool),
		sessionCtx:     context.Background(),
		argCache:       newPromptArgCache(),
	}
}

func (f *fakePromptDeps) pdPromptResolver() PromptResolver { return f.resolver }
func (f *fakePromptDeps) pdWorkingDir() string             { return f.workingDir }
func (f *fakePromptDeps) pdAgentSupportsImages() bool      { return f.agentImages }
func (f *fakePromptDeps) pdHasStore() bool                 { return f.hasStore }
func (f *fakePromptDeps) pdLogger() *slog.Logger           { return f.logger }
func (f *fakePromptDeps) pdSessionID() string              { return f.sessionID }

func (f *fakePromptDeps) pdGetImagePath(imageID string) (string, error) {
	if err := f.imageErrs[imageID]; err != nil {
		return "", err
	}
	return f.imagePaths[imageID], nil
}

func (f *fakePromptDeps) pdGetFilePath(fileID string) (string, error) {
	if err := f.fileErrs[fileID]; err != nil {
		return "", err
	}
	return f.filePaths[fileID], nil
}

func (f *fakePromptDeps) pdNotifyObservers(fn func(SessionObserver)) {
	fn(&pdRecorderObserver{deps: f})
}

// === New in 2.5-b ===

func (f *fakePromptDeps) pdWorkspaceUUID() string { return f.workspaceUUID }
func (f *fakePromptDeps) pdAvailableACPServers() []processors.AvailableACPServer {
	return f.availableACPServers
}
func (f *fakePromptDeps) pdGetSessionMetadata() (session.Metadata, error) {
	return f.sessionMeta, f.sessionMetaErr
}
func (f *fakePromptDeps) pdGetMetadataForID(id string) (session.Metadata, error) {
	m, ok := f.metaByID[id]
	if !ok {
		return session.Metadata{}, errors.New("not found")
	}
	return m, nil
}
func (f *fakePromptDeps) pdListChildSessions() ([]session.Metadata, error) {
	return f.childSessions, f.childSessionsErr
}
func (f *fakePromptDeps) pdIsChildPrompting(id string) bool { return f.childPrompting[id] }
func (f *fakePromptDeps) pdCachedMCPToolNames() []string    { return f.mcpToolNames }
func (f *fakePromptDeps) pdGetUserData() (*session.UserData, error) {
	return f.userData, f.userDataErr
}
func (f *fakePromptDeps) pdSessionCtx() context.Context { return f.sessionCtx }
func (f *fakePromptDeps) pdHasProcessorManager() bool   { return f.hasProcessorMgr }
func (f *fakePromptDeps) pdApplyProcessors(_ context.Context, _ *processors.ProcessorInput) (*processors.ProcessorResult, error) {
	return f.applyResult, f.applyErr
}
func (f *fakePromptDeps) pdPersistProcessorActivation() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistActivationCalls++
}
func (f *fakePromptDeps) pdBuildPromptWithHistory(msg string) string {
	return f.historyPrefix + msg
}
func (f *fakePromptDeps) pdWorkspaceProcessorArgOverrides() map[string]map[string]string {
	return f.workspaceProcessorArgOverrides
}

// === New in 2.5-c ===

func (f *fakePromptDeps) pdHasSharedProcess() bool { return f.hasSharedProcess }
func (f *fakePromptDeps) pdCompleteDeferredHandshake() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handshakeCalls++
	return f.handshakeErr
}
func (f *fakePromptDeps) pdHasRecorder() bool { return f.hasRecorder }
func (f *fakePromptDeps) pdGetNextSeq() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSeq++
	return f.nextSeq
}
func (f *fakePromptDeps) pdRefreshNextSeq() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshSeqCalls++
}
func (f *fakePromptDeps) pdRecordErrorEvent(_ int64, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedErrorEvents = append(f.recordedErrorEvents, msg)
	return nil
}
func (f *fakePromptDeps) pdResetPromptingStateForAbort() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptingResetCalls++
}
func (f *fakePromptDeps) pdNotifyStreamingStateChanged(active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamingChanges = append(f.streamingChanges, active)
}
func (f *fakePromptDeps) pdHasACPConn() bool { return f.hasACPConn }
func (f *fakePromptDeps) pdACPConnNewSession(_ context.Context, _ string) (string, error) {
	return f.acpNewSessionID, f.acpNewSessionErr
}
func (f *fakePromptDeps) pdGetAgentModels() *acp.UnstableSessionModelState { return f.agentModels }
func (f *fakePromptDeps) pdResolvePreferredModels(_ string) []string       { return f.resolvedPreferred }
func (f *fakePromptDeps) pdReadBaselineModel() string                      { return f.baselineModel }
func (f *fakePromptDeps) pdWriteOverrideActive(active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrideActive = active
}
func (f *fakePromptDeps) pdSetActiveModelOnly(_ context.Context, modelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setActiveModelCalls = append(f.setActiveModelCalls, modelID)
	return f.setActiveModelErr
}

// === mitto-pchx.3: prompt-arg cache ===

func (f *fakePromptDeps) pdResolvePromptParameters(_ string) []config.PromptParameter {
	return f.promptParams
}
func (f *fakePromptDeps) pdCacheGetArg(promptName, paramName string) (string, bool) {
	if f.argCache == nil {
		return "", false
	}
	return f.argCache.Get(promptName, paramName)
}
func (f *fakePromptDeps) pdCacheSetArg(promptName, paramName, value string, ttl time.Duration) {
	if f.argCache == nil {
		return
	}
	f.argCache.Set(promptName, paramName, value, ttl)
}

// === New in 2.5-d ===

func (f *fakePromptDeps) pdSetLastUsage(usage *acp.Usage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUsageSet = usage
}
func (f *fakePromptDeps) pdAccumulateTokenUsage(tokens int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accumulatedTokens = append(f.accumulatedTokens, tokens)
}
func (f *fakePromptDeps) pdEstimateTokensFromMessage(msg string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.estimatedTokenCalls = append(f.estimatedTokenCalls, msg)
	return len(msg) // simple word-count-ish fake
}
func (f *fakePromptDeps) pdReadLastAgentMessage() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastAgentMessage
}
func (f *fakePromptDeps) pdMarkPromptComplete() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCompleteCount++
}
func (f *fakePromptDeps) pdIsClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.isClosed
}
func (f *fakePromptDeps) pdFlushMarkdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushMarkdownCount++
}
func (f *fakePromptDeps) pdObserverCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.observerCount
}
func (f *fakePromptDeps) pdGetEventCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.eventCount
}
func (f *fakePromptDeps) pdFlushPendingConfig() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushConfigCount++
}
func (f *fakePromptDeps) pdProcessNextQueuedMessage() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.processNextCalled++
	return f.processNextResult
}
func (f *fakePromptDeps) pdRetryTitleGenerationIfNeeded(message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retryTitleCalls = append(f.retryTitleCalls, message)
}
func (f *fakePromptDeps) pdActionButtonsEnabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.actionButtonsOn
}
func (f *fakePromptDeps) pdReadLastAgentMessageFromStore() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastAgentMessage
}
func (f *fakePromptDeps) pdHasImmediateQueuedMessages() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.immediateQueue
}
func (f *fakePromptDeps) pdStartFollowUpAnalysis(userMessage, agentMessage string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.followUpCalls = append(f.followUpCalls, []string{userMessage, agentMessage})
}
func (f *fakePromptDeps) pdApplyAfterProcessors(_ context.Context, _, _, _ string, _, _ time.Time, _ acp.PromptResponse, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afterProcessorCalls++
}
func (f *fakePromptDeps) pdOnTurnIdle() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turnIdleCalls++
	f.onCompleteCallOrder = append(f.onCompleteCallOrder, "TurnIdle")
}
func (f *fakePromptDeps) pdIsSelfDestructRequested() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.selfDestructRequested
}
func (f *fakePromptDeps) pdTriggerSelfDestruct() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.selfDestructCalls++
	f.onCompleteCallOrder = append(f.onCompleteCallOrder, "SelfDestruct")
}

// === New in 2.5-e ===

func (f *fakePromptDeps) pdIsACPDead() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acpDead
}
func (f *fakePromptDeps) pdCanRestartACP() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canRestart
}
func (f *fakePromptDeps) pdGetRestartInfo() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restartInfo
}
func (f *fakePromptDeps) pdRestartACPProcess() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartCalled++
	return f.restartErr
}
func (f *fakePromptDeps) pdReacquirePromptingState() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reacquireCalls++
}

type pdRecorderObserver struct{ deps *fakePromptDeps }

func (r *pdRecorderObserver) OnError(msg string) {
	r.deps.mu.Lock()
	r.deps.notifiedErrors = append(r.deps.notifiedErrors, msg)
	r.deps.mu.Unlock()
}
func (r *pdRecorderObserver) OnAgentMessage(int64, string)                  {}
func (r *pdRecorderObserver) OnAgentThought(int64, string)                  {}
func (r *pdRecorderObserver) OnToolCall(int64, string, string, string)      {}
func (r *pdRecorderObserver) OnToolUpdate(int64, string, *string)           {}
func (r *pdRecorderObserver) OnPlan(int64, []PlanEntry)                     {}
func (r *pdRecorderObserver) OnFileWrite(int64, string, int)                {}
func (r *pdRecorderObserver) OnFileRead(int64, string, int)                 {}
func (r *pdRecorderObserver) OnContextUsageUpdate(int, int)                 {}
func (r *pdRecorderObserver) OnAvailableCommandsUpdated([]AvailableCommand) {}
func (r *pdRecorderObserver) OnQueueMessageSending(string)                  {}
func (r *pdRecorderObserver) OnQueueMessageSent(string)                     {}
func (r *pdRecorderObserver) OnQueueUpdated(int, string, string)            {}
func (r *pdRecorderObserver) OnQueueReordered([]session.QueuedMessage)      {}
func (r *pdRecorderObserver) OnPromptComplete(int)                          {}
func (r *pdRecorderObserver) OnActionButtons([]ActionButton)                {}
func (r *pdRecorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int) {
}
func (r *pdRecorderObserver) OnACPStopped(string)              {}
func (r *pdRecorderObserver) OnACPStarted()                    {}
func (r *pdRecorderObserver) OnUIPrompt(UIPromptRequest)       {}
func (r *pdRecorderObserver) OnUIPromptDismiss(string, string) {}
func (r *pdRecorderObserver) OnNotification(UINotifyRequest)   {}

// --- resolveAndSubstitute tests ---

func TestPromptDispatcher_ResolveAndSubstitute_NoResolverError(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = nil

	_, _, _, err := p.resolveAndSubstitute(d, "", PromptMeta{PromptName: "my-prompt"})
	if err == nil {
		t.Fatal("expected error when no resolver configured")
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestPromptDispatcher_ResolveAndSubstitute_ResolverError(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) {
		return "", errors.New("lookup failed")
	}

	_, _, _, err := p.resolveAndSubstitute(d, "", PromptMeta{PromptName: "bad-prompt"})
	if err == nil {
		t.Fatal("expected error from resolver failure")
	}
}

func TestPromptDispatcher_ResolveAndSubstitute_ResolverSuccess(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) {
		return "Hello, World!", nil
	}

	msg, argCount, _, err := p.resolveAndSubstitute(d, "", PromptMeta{PromptName: "greet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "Hello, World!" {
		t.Fatalf("expected resolved message, got %q", msg)
	}
	if argCount != 0 {
		t.Fatalf("expected argCount=0, got %d", argCount)
	}
}

func TestPromptDispatcher_ResolveAndSubstitute_NoPromptName_PassthroughMessage(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	msg, argCount, _, err := p.resolveAndSubstitute(d, "direct message", PromptMeta{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "direct message" {
		t.Fatalf("expected unchanged message, got %q", msg)
	}
	if argCount != 0 {
		t.Fatalf("expected argCount=0, got %d", argCount)
	}
}

func TestPromptDispatcher_ResolveAndSubstitute_ArgSubstitution(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	args := map[string]string{"NAME": "Alice", "CITY": "Paris"}
	msg, argCount, updatedMeta, err := p.resolveAndSubstitute(d,
		"Hello ${NAME}, welcome to ${CITY}!", PromptMeta{Arguments: args})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "Hello Alice, welcome to Paris!" {
		t.Fatalf("expected substituted message, got %q", msg)
	}
	if argCount != 2 {
		t.Fatalf("expected argCount=2, got %d", argCount)
	}
	if updatedMeta.Meta == nil {
		t.Fatal("expected meta.Meta populated")
	}
	if _, ok := updatedMeta.Meta["argument_names"]; !ok {
		t.Fatal("expected argument_names in meta.Meta")
	}
	if _, ok := updatedMeta.Meta["arguments"]; !ok {
		t.Fatal("expected arguments in meta.Meta")
	}
}

func TestPromptDispatcher_ResolveAndSubstitute_NoArgs_MetaUntouched(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	original := PromptMeta{SenderID: "user-1"}
	_, argCount, updatedMeta, err := p.resolveAndSubstitute(d, "plain text", original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if argCount != 0 {
		t.Fatalf("expected argCount=0, got %d", argCount)
	}
	if updatedMeta.Meta != nil {
		t.Fatalf("expected meta.Meta nil when no args, got %v", updatedMeta.Meta)
	}
}

// --- resolveAndSubstitute template-render tests (mitto-m7sb.5) ---

// TestResolveAndSubstitute_Template_FastPath verifies that a body without {{ is
// returned unchanged and that no template work is done (fast path).
func TestResolveAndSubstitute_Template_FastPath(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	// Body contains ${VAR} and @mitto: tokens but NO {{ — must pass through unchanged.
	body := "plain ${VAR} @mitto:session_id text"
	msg, _, _, err := p.resolveAndSubstitute(d, body, PromptMeta{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != body {
		t.Fatalf("expected body unchanged, got %q", msg)
	}
}

// TestResolveAndSubstitute_Template_SessionID verifies that a template body
// referencing {{ .Session.ID }} renders to the value from the fake deps.
func TestResolveAndSubstitute_Template_SessionID(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.sessionID = "my-sess-42"

	msg, _, _, err := p.resolveAndSubstitute(d, "id={{ .Session.ID }}", PromptMeta{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "id=my-sess-42" {
		t.Fatalf("expected rendered message, got %q", msg)
	}
}

// TestResolveAndSubstitute_Template_RenderBeforeArgSubstitution verifies that
// template rendering runs BEFORE ${VAR} substitution: the template may emit
// ${SUFFIX} tokens that SubstituteArguments then resolves.
func TestResolveAndSubstitute_Template_RenderBeforeArgSubstitution(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.sessionID = "sess-X"

	// Template outputs "sess-X-${SUFFIX}"; SubstituteArguments then resolves ${SUFFIX}.
	body := "{{ .Session.ID }}-${SUFFIX}"
	args := map[string]string{"SUFFIX": "end"}
	msg, _, _, err := p.resolveAndSubstitute(d, body, PromptMeta{Arguments: args})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "sess-X-end" {
		t.Fatalf("expected render-then-subst result, got %q", msg)
	}
}

// TestResolveAndSubstitute_Template_FailClosed verifies that an invalid template
// body returned by a named prompt resolver returns a non-nil error (fail-closed).
func TestResolveAndSubstitute_Template_FailClosed(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	// Resolver returns an invalid template body (missing {{ end }}).
	d.resolver = func(name, _ string) (string, error) {
		return "{{ if .Broken }}", nil
	}

	msg, _, _, err := p.resolveAndSubstitute(d, "", PromptMeta{PromptName: "x"})
	if err == nil {
		t.Fatal("expected non-nil error for invalid named-prompt template body")
	}
	if msg != "" {
		t.Fatalf("expected empty message on error, got %q", msg)
	}
}

// TestResolveAndSubstitute_FreeText_InvalidTemplate_FailOpen verifies that a
// free-text body from DIRECT HUMAN INPUT (empty SenderID) containing unbalanced
// template syntax is delivered raw (fail-open) — so pasted text containing {{ is
// delivered literally (mitto-gnxe).
func TestResolveAndSubstitute_FreeText_InvalidTemplate_FailOpen(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	// Unbalanced {{ if }} with no matching {{ end }} — reproduces mitto-gnxe.
	body := "{{ if .Broken }}"
	msg, _, _, err := p.resolveAndSubstitute(d, body, PromptMeta{})
	if err != nil {
		t.Fatalf("expected nil error for free-text with invalid template syntax, got: %v", err)
	}
	if msg != body {
		t.Fatalf("expected raw body byte-for-byte, got %q", msg)
	}
}

// TestResolveAndSubstitute_AutomatedDispatch_InvalidTemplate_FailClosed verifies
// that a free-text body with unbalanced template syntax dispatched via an automated
// path (queue / periodic-runner) fails CLOSED — it returns a non-nil error instead
// of silently delivering the raw, unrenderable body to a child (mitto-e7u).
func TestResolveAndSubstitute_AutomatedDispatch_InvalidTemplate_FailClosed(t *testing.T) {
	p := promptDispatcher{}
	body := "{{ if .Broken }}" // unbalanced action -> "unexpected EOF"

	for _, senderID := range []string{senderIDQueue, senderIDPeriodic} {
		t.Run(senderID, func(t *testing.T) {
			d := newFakePromptDeps()
			msg, _, _, err := p.resolveAndSubstitute(d, body, PromptMeta{SenderID: senderID})
			if err == nil {
				t.Fatalf("expected non-nil error for automated dispatch (sender=%q) with invalid template, got msg=%q", senderID, msg)
			}
			if msg != "" {
				t.Fatalf("expected empty message on fail-closed, got %q", msg)
			}
		})
	}
}

// --- buildAttachmentBlocks tests ---

func TestPromptDispatcher_BuildAttachmentBlocks_NoStore(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasStore = false

	blocks, imageRefs, fileRefs := p.buildAttachmentBlocks(d, []string{"img.png"}, []string{"file.txt"})
	if len(blocks) != 0 || len(imageRefs) != 0 || len(fileRefs) != 0 {
		t.Fatal("expected empty results when no store")
	}
}

func TestPromptDispatcher_BuildAttachmentBlocks_NoImageSupport_StillNotifies(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentImages = false
	// No image paths → no blocks, but notification should fire.

	p.buildAttachmentBlocks(d, []string{"img.png"}, nil)

	if len(d.notifiedErrors) != 1 {
		t.Fatalf("expected 1 OnError notification, got %d", len(d.notifiedErrors))
	}
}

func TestPromptDispatcher_BuildAttachmentBlocks_ImageGetPathError_Continue(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.imageErrs["img.png"] = errors.New("not found")

	blocks, imageRefs, _ := p.buildAttachmentBlocks(d, []string{"img.png"}, nil)
	if len(blocks) != 0 || len(imageRefs) != 0 {
		t.Fatal("expected skip (continue) on GetImagePath error")
	}
}

func TestPromptDispatcher_BuildAttachmentBlocks_ImageHappyPath(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	// Create a real PNG file (minimal 8-byte signature) so ImageAttachmentFromFile succeeds.
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	// Write a minimal valid PNG (1x1 white pixel).
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR length + type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // width=1, height=1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // bit depth=8, color type=2
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT length + type
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, // IDAT data
		0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC, // IDAT CRC
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, // IEND length + type
		0x44, 0xAE, 0x42, 0x60, 0x82, // IEND data+CRC
	}
	if err := os.WriteFile(imgPath, pngData, 0644); err != nil {
		t.Fatalf("failed to write test PNG: %v", err)
	}

	d.imagePaths["test.png"] = imgPath

	blocks, imageRefs, _ := p.buildAttachmentBlocks(d, []string{"test.png"}, nil)
	if len(imageRefs) != 1 || imageRefs[0].ID != "test.png" {
		t.Fatalf("expected 1 imageRef, got %v", imageRefs)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(blocks))
	}
}

func TestPromptDispatcher_BuildAttachmentBlocks_FileGetPathError_Continue(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.fileErrs["doc.txt"] = errors.New("not found")

	blocks, _, fileRefs := p.buildAttachmentBlocks(d, nil, []string{"doc.txt"})
	if len(blocks) != 0 || len(fileRefs) != 0 {
		t.Fatal("expected skip (continue) on GetFilePath error")
	}
}

func TestPromptDispatcher_BuildAttachmentBlocks_TextFile(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	tmpDir := t.TempDir()
	txtPath := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtPath, []byte("hello world"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	d.filePaths["readme.txt"] = txtPath

	blocks, _, fileRefs := p.buildAttachmentBlocks(d, nil, []string{"readme.txt"})
	if len(fileRefs) != 1 || fileRefs[0].ID != "readme.txt" {
		t.Fatalf("expected 1 fileRef, got %v", fileRefs)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(blocks))
	}
	if fileRefs[0].Category != session.FileCategoryText {
		t.Fatalf("expected text category, got %v", fileRefs[0].Category)
	}
}

func TestPromptDispatcher_BuildAttachmentBlocks_BinaryFile(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "data.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02}, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	d.filePaths["data.bin"] = binPath

	blocks, _, fileRefs := p.buildAttachmentBlocks(d, nil, []string{"data.bin"})
	if len(fileRefs) != 1 || fileRefs[0].ID != "data.bin" {
		t.Fatalf("expected 1 fileRef, got %v", fileRefs)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(blocks))
	}
	if fileRefs[0].Category == session.FileCategoryText {
		t.Fatalf("expected non-text category for .bin, got %v", fileRefs[0].Category)
	}
}

// --- buildProcessorInput tests ---

func TestPromptDispatcher_BuildProcessorInput_NoStore_MinimalInput(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasStore = false
	d.sessionID = "sess-1"
	d.workingDir = "" // no workingDir → no RC loading

	input := p.buildProcessorInput(d, "hello", false, PromptMeta{SenderID: "user"})

	if input.Message != "hello" {
		t.Fatalf("expected message='hello', got %q", input.Message)
	}
	if input.SessionID != "sess-1" {
		t.Fatalf("expected SessionID='sess-1', got %q", input.SessionID)
	}
	if input.IsFirstMessage {
		t.Fatal("expected IsFirstMessage=false")
	}
	if input.IsPeriodic {
		t.Fatal("expected IsPeriodic=false for non-periodic sender")
	}
	// Store-dependent fields must be empty
	if input.SessionName != "" || input.ParentSessionID != "" || input.UserDataJSON != "" {
		t.Fatalf("expected empty store-dependent fields, got %+v", input)
	}
}

func TestPromptDispatcher_BuildProcessorInput_WithMetadata(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.sessionID = "sess-2"
	d.sessionMeta = session.Metadata{
		Name:            "My Session",
		ACPServer:       "auggie",
		ParentSessionID: "parent-1",
		BeadsIssue:      "mitto-123",
	}
	d.metaByID["parent-1"] = session.Metadata{Name: "Parent Session"}
	d.childSessions = []session.Metadata{
		{SessionID: "child-1", Name: "Child A", ACPServer: "auggie"},
	}
	d.childPrompting["child-1"] = true
	d.mcpToolNames = []string{"tool_a", "tool_b"}

	input := p.buildProcessorInput(d, "test", true, PromptMeta{SenderID: "periodic-runner"})

	if input.SessionName != "My Session" {
		t.Fatalf("expected SessionName='My Session', got %q", input.SessionName)
	}
	if input.ParentSessionID != "parent-1" {
		t.Fatalf("expected ParentSessionID set, got %q", input.ParentSessionID)
	}
	if input.ParentSessionName != "Parent Session" {
		t.Fatalf("expected ParentSessionName='Parent Session', got %q", input.ParentSessionName)
	}
	if len(input.ChildSessions) != 1 || !input.ChildSessions[0].IsPrompting {
		t.Fatalf("expected 1 prompting child, got %+v", input.ChildSessions)
	}
	if len(input.MCPToolNames) != 2 {
		t.Fatalf("expected 2 MCP tool names, got %v", input.MCPToolNames)
	}
	if !input.IsPeriodic {
		t.Fatal("expected IsPeriodic=true for periodic-runner sender")
	}
	if input.BeadsIssue != "mitto-123" {
		t.Fatalf("expected BeadsIssue='mitto-123', got %q", input.BeadsIssue)
	}
}

func TestPromptDispatcher_BuildProcessorInput_IsPeriodicForced(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasStore = false

	meta := PromptMeta{IsPeriodicForced: true}
	input := p.buildProcessorInput(d, "msg", false, meta)
	if !input.IsPeriodicForced {
		t.Fatal("expected IsPeriodicForced=true")
	}
}

func TestPromptDispatcher_BuildProcessorInput_Arguments(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasStore = false

	args := map[string]string{"BRANCH": "main", "ISSUE": "mitto-1"}
	meta := PromptMeta{Arguments: args}
	input := p.buildProcessorInput(d, "msg", false, meta)
	if input.Arguments == nil {
		t.Fatal("expected Arguments populated from meta.Arguments")
	}
	if input.Arguments["BRANCH"] != "main" || input.Arguments["ISSUE"] != "mitto-1" {
		t.Fatalf("unexpected Arguments: %#v", input.Arguments)
	}
}

func TestPromptDispatcher_BuildProcessorInput_UserDataJSON(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.userData = &session.UserData{
		Attributes: []session.UserDataAttribute{
			{Name: "env", Value: "prod"},
			{Name: "JIRA Ticket", Value: "PROJ-99"},
		},
	}

	input := p.buildProcessorInput(d, "msg", false, PromptMeta{})
	if input.UserDataJSON == "" {
		t.Fatal("expected UserDataJSON populated from user data attributes")
	}
	// UserData map must mirror Attributes.
	if input.UserData == nil {
		t.Fatal("expected UserData map populated from user data attributes")
	}
	if input.UserData["env"] != "prod" {
		t.Errorf(`UserData["env"] = %q, want "prod"`, input.UserData["env"])
	}
	if input.UserData["JIRA Ticket"] != "PROJ-99" {
		t.Errorf(`UserData["JIRA Ticket"] = %q, want "PROJ-99"`, input.UserData["JIRA Ticket"])
	}
}

// --- applyProcessorsAndBuildBlocks tests ---

func TestPromptDispatcher_ApplyProcessorsAndBuildBlocks_NoProcessor_TextOnly(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = false

	blocks := p.applyProcessorsAndBuildBlocks(d, &processors.ProcessorInput{}, "hello", nil, false)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 block (text only), got %d", len(blocks))
	}
	if blocks[0].Text == nil || blocks[0].Text.Text != "hello" {
		t.Fatalf("expected text block 'hello', got %+v", blocks[0])
	}
}

func TestPromptDispatcher_ApplyProcessorsAndBuildBlocks_ProcessorError_OriginalMessagePreserved(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = true
	d.applyErr = errors.New("proc fail")

	blocks := p.applyProcessorsAndBuildBlocks(d, &processors.ProcessorInput{}, "original", nil, false)

	// On error, original message is preserved (not empty).
	if len(blocks) != 1 || blocks[0].Text == nil {
		t.Fatalf("expected 1 text block on error, got %+v", blocks)
	}
	// The text will be original (SubstituteVariables on "original" with empty input returns "original").
	if blocks[0].Text.Text != "original" {
		t.Fatalf("expected 'original', got %q", blocks[0].Text.Text)
	}
	// No persist call on error.
	if d.persistActivationCalls != 0 {
		t.Fatalf("expected 0 persist calls on error, got %d", d.persistActivationCalls)
	}
}

func TestPromptDispatcher_ApplyProcessorsAndBuildBlocks_ProcessorSuccess_PersistsCalled(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = true
	modifiedMsg := "modified by proc"
	d.applyResult = &processors.ProcessorResult{Message: modifiedMsg}

	input := &processors.ProcessorInput{Message: "original"}
	blocks := p.applyProcessorsAndBuildBlocks(d, input, "original", nil, false)

	if d.persistActivationCalls != 1 {
		t.Fatalf("expected 1 persist call on success, got %d", d.persistActivationCalls)
	}
	if len(blocks) != 1 || blocks[0].Text == nil || blocks[0].Text.Text != modifiedMsg {
		t.Fatalf("expected modified message in block, got %+v", blocks)
	}
}

func TestPromptDispatcher_ApplyProcessorsAndBuildBlocks_ShouldInjectHistory(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = false
	d.historyPrefix = "[HISTORY] "

	input := &processors.ProcessorInput{}
	blocks := p.applyProcessorsAndBuildBlocks(d, input, "msg", nil, true)

	if len(blocks) != 1 || blocks[0].Text == nil {
		t.Fatalf("expected 1 text block, got %+v", blocks)
	}
	if blocks[0].Text.Text != "[HISTORY] msg" {
		t.Fatalf("expected history prefix, got %q", blocks[0].Text.Text)
	}
}

func TestPromptDispatcher_ApplyProcessorsAndBuildBlocks_BlockOrdering(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = false

	// Provide an existing content block (e.g. an uploaded image).
	uploadBlock := makeTextBlock("uploaded-image-placeholder")
	input := &processors.ProcessorInput{}
	blocks := p.applyProcessorsAndBuildBlocks(d, input, "text", []acp.ContentBlock{uploadBlock}, false)

	// Order: [upload] [text]
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Text == nil || blocks[0].Text.Text != "uploaded-image-placeholder" {
		t.Fatalf("expected upload block first, got %+v", blocks[0])
	}
	if blocks[1].Text == nil || blocks[1].Text.Text != "text" {
		t.Fatalf("expected text block last, got %+v", blocks[1])
	}
}

// makeTextBlock creates a simple text content block for testing.
func makeTextBlock(text string) acp.ContentBlock {
	return acp.TextBlock(text)
}

// --- completeHandshakeOrAbort tests ---

func TestPromptDispatcher_CompleteHandshakeOrAbort_NoSharedProcess_ReturnsTrue(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasSharedProcess = false

	ok := p.completeHandshakeOrAbort(d)
	if !ok {
		t.Fatal("expected true when no shared process")
	}
	if d.handshakeCalls != 0 {
		t.Fatalf("expected no handshake calls, got %d", d.handshakeCalls)
	}
}

func TestPromptDispatcher_CompleteHandshakeOrAbort_Success_ReturnsTrue(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasSharedProcess = true
	d.handshakeErr = nil // success immediately

	ok := p.completeHandshakeOrAbort(d)
	if !ok {
		t.Fatal("expected true on successful handshake")
	}
	if d.handshakeCalls != 1 {
		t.Fatalf("expected 1 handshake call, got %d", d.handshakeCalls)
	}
}

func TestPromptDispatcher_CompleteHandshakeOrAbort_PermanentError_ReturnsFalseAndResetsState(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasSharedProcess = true
	d.handshakeErr = errors.New("connection refused") // non-transient

	ok := p.completeHandshakeOrAbort(d)
	if ok {
		t.Fatal("expected false on permanent handshake error")
	}
	// Error notification must fire
	if len(d.notifiedErrors) != 1 {
		t.Fatalf("expected 1 observer error notification, got %d", len(d.notifiedErrors))
	}
	// Prompting state must be reset
	if d.promptingResetCalls != 1 {
		t.Fatalf("expected 1 prompting reset, got %d", d.promptingResetCalls)
	}
	// Streaming state must be set to false
	if len(d.streamingChanges) != 1 || d.streamingChanges[0] != false {
		t.Fatalf("expected streaming=false notification, got %v", d.streamingChanges)
	}
}

func TestPromptDispatcher_CompleteHandshakeOrAbort_PermanentError_RecordsEventWhenRecorderPresent(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasSharedProcess = true
	d.handshakeErr = errors.New("permanent failure")
	d.hasRecorder = true

	p.completeHandshakeOrAbort(d)

	if len(d.recordedErrorEvents) != 1 {
		t.Fatalf("expected 1 recorded error event, got %d", len(d.recordedErrorEvents))
	}
	if d.refreshSeqCalls != 1 {
		t.Fatalf("expected 1 refreshNextSeq call, got %d", d.refreshSeqCalls)
	}
}

func TestPromptDispatcher_CompleteHandshakeOrAbort_TransientThenSuccess_Retries(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasSharedProcess = true
	// First call transient, second call succeeds.
	callCount := 0
	originalErr := d.handshakeErr
	_ = originalErr
	d.handshakeErr = errors.New("deadline exceeded") // transient keyword
	// Override via a custom fake that succeeds on attempt 2
	// We simulate by making handshakeErr nil after 1 call
	type countedDeps struct {
		*fakePromptDeps
		target int
	}
	cd := &countedDeps{fakePromptDeps: d, target: 1}
	// Use a wrapper that fails once then succeeds
	wrapper := &transientFakePromptDeps{fakePromptDeps: d, failTimes: 1}
	ok := p.completeHandshakeOrAbort(wrapper)
	if !ok {
		t.Fatal("expected true after transient retry succeeded")
	}
	if wrapper.handshakeCalls < 2 {
		t.Fatalf("expected at least 2 handshake calls for retry, got %d", wrapper.handshakeCalls)
	}
	_ = callCount
	_ = cd
}

// transientFakePromptDeps fails the first N handshake calls with a transient error.
type transientFakePromptDeps struct {
	*fakePromptDeps
	failTimes int
	successes int
}

func (t *transientFakePromptDeps) pdCompleteDeferredHandshake() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handshakeCalls++
	if t.handshakeCalls <= t.failTimes {
		return errors.New("timeout connecting")
	}
	t.successes++
	return nil
}

// --- createFreshContextSession tests ---

func TestPromptDispatcher_CreateFreshContextSession_FreshContextFalse_ReturnsEmpty(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = true

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: false})
	if id != "" {
		t.Fatalf("expected empty id when FreshContext=false, got %q", id)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_NoACPConn_ReturnsEmpty(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = false

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true})
	if id != "" {
		t.Fatalf("expected empty id when no ACP conn, got %q", id)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_Success_ReturnsID(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = true
	d.acpNewSessionID = "fresh-session-123"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true})
	if id != "fresh-session-123" {
		t.Fatalf("expected 'fresh-session-123', got %q", id)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_ACPError_ReturnsEmpty(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = true
	d.acpNewSessionErr = errors.New("new session failed")

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true})
	if id != "" {
		t.Fatalf("expected empty id on error, got %q", id)
	}
}

// --- applyModelPreference tests ---

func TestPromptDispatcher_ApplyModelPreference_NoAgentModels_NoOp(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = nil
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	p.applyModelPreference(d, PromptMeta{})

	if len(d.setActiveModelCalls) != 0 {
		t.Fatalf("expected no setActiveModel call when agentModels=nil, got %v", d.setActiveModelCalls)
	}
	if !strings.Contains(buf.String(), "decision=skip_no_agent_models") {
		t.Fatalf("expected decision=skip_no_agent_models in log, got: %s", buf.String())
	}
}

func TestPromptDispatcher_ApplyModelPreference_NoPreference_DesiredIsBaseline_NoSwitch(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &acp.UnstableSessionModelState{CurrentModelId: "m-1"}
	d.baselineModel = "m-1" // same as current
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	p.applyModelPreference(d, PromptMeta{}) // no preferred models

	if len(d.setActiveModelCalls) != 0 {
		t.Fatalf("expected no model switch when desired==current, got %v", d.setActiveModelCalls)
	}
	if d.overrideActive {
		t.Fatal("expected overrideActive=false when no preference and using baseline")
	}
	if !strings.Contains(buf.String(), "decision=skip_no_preference") {
		t.Fatalf("expected decision=skip_no_preference in log, got: %s", buf.String())
	}
}

func TestPromptDispatcher_ApplyModelPreference_MatchingPreference_SetsModelAndOverride(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &acp.UnstableSessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []acp.UnstableModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	d.baselineModel = "m-1"
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Prefer "m-2" (matched by name "Model 2" with "contains" mode)
	p.applyModelPreference(d, PromptMeta{PreferredModels: []string{"Model 2"}})

	if len(d.setActiveModelCalls) != 1 || d.setActiveModelCalls[0] != "m-2" {
		t.Fatalf("expected setActiveModelOnly('m-2'), got %v", d.setActiveModelCalls)
	}
	if !d.overrideActive {
		t.Fatal("expected overrideActive=true when preferred differs from baseline")
	}
	if !strings.Contains(buf.String(), "decision=switching") {
		t.Fatalf("expected decision=switching in log, got: %s", buf.String())
	}
}

func TestPromptDispatcher_ApplyModelPreference_PreferenceAlreadyActive_NoSwitch(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &acp.UnstableSessionModelState{
		CurrentModelId: "m-2",
		AvailableModels: []acp.UnstableModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	d.baselineModel = "m-1"
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Prefer "m-2" which is already active.
	p.applyModelPreference(d, PromptMeta{PreferredModels: []string{"Model 2"}})

	if len(d.setActiveModelCalls) != 0 {
		t.Fatalf("expected no RPC when preferred model already active, got %v", d.setActiveModelCalls)
	}
	// But override is still true because desired != baseline
	if !d.overrideActive {
		t.Fatal("expected overrideActive=true because desired differs from baseline")
	}
	if !strings.Contains(buf.String(), "decision=skip_already_satisfied") {
		t.Fatalf("expected decision=skip_already_satisfied in log, got: %s", buf.String())
	}
}

func TestPromptDispatcher_ApplyModelPreference_NoMatch_UsesBaseline_ClearsOverride(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &acp.UnstableSessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []acp.UnstableModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
		},
	}
	d.baselineModel = "m-1"
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Preference pattern doesn't match anything → desired stays at baseline.
	p.applyModelPreference(d, PromptMeta{PreferredModels: []string{"nonexistent-model"}})

	if len(d.setActiveModelCalls) != 0 {
		t.Fatalf("expected no model switch on no-match, got %v", d.setActiveModelCalls)
	}
	if d.overrideActive {
		t.Fatal("expected overrideActive=false when no match and desired==baseline")
	}
	if !strings.Contains(buf.String(), "decision=skip_no_match") {
		t.Fatalf("expected decision=skip_no_match in log, got: %s", buf.String())
	}
}

// --- accumulateTokenUsage tests ---

func TestPromptDispatcher_AccumulateTokenUsage_UsagePresent_SetsAndAccumulates(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = true
	usage := &acp.Usage{TotalTokens: 42}
	resp := acp.PromptResponse{Usage: usage}

	p.accumulateTokenUsage(d, resp, "hello")

	if d.lastUsageSet != usage {
		t.Fatal("expected pdSetLastUsage to be called with the usage")
	}
	if len(d.accumulatedTokens) != 1 || d.accumulatedTokens[0] != 42 {
		t.Fatalf("expected AccumulateTokenUsage(42), got %v", d.accumulatedTokens)
	}
}

func TestPromptDispatcher_AccumulateTokenUsage_UsageNil_EstimatesFromMessage(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = true
	d.lastAgentMessage = "agent reply" // returned by pdReadLastAgentMessage
	resp := acp.PromptResponse{}       // Usage == nil

	p.accumulateTokenUsage(d, resp, "user msg")

	// pdEstimateTokensFromMessage called twice: once for message, once for agent reply
	if len(d.estimatedTokenCalls) < 2 {
		t.Fatalf("expected 2 estimate calls, got %d", len(d.estimatedTokenCalls))
	}
	// Must still accumulate (len("user msg") + len("agent reply") > 0)
	if len(d.accumulatedTokens) == 0 {
		t.Fatal("expected AccumulateTokenUsage to be called when estimated > 0")
	}
}

func TestPromptDispatcher_AccumulateTokenUsage_NoProcessorManager_SkipsAccumulate(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = false
	usage := &acp.Usage{TotalTokens: 10}
	resp := acp.PromptResponse{Usage: usage}

	p.accumulateTokenUsage(d, resp, "msg")

	// setLastUsage still called
	if d.lastUsageSet == nil {
		t.Fatal("expected pdSetLastUsage even when no processor manager")
	}
	// accumulate NOT called
	if len(d.accumulatedTokens) != 0 {
		t.Fatalf("expected no accumulate without processor manager, got %v", d.accumulatedTokens)
	}
}

func TestPromptDispatcher_AccumulateTokenUsage_EstimatedIsZero_NoAccumulate(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = true
	d.lastAgentMessage = ""      // empty → estimate=0
	resp := acp.PromptResponse{} // Usage nil

	p.accumulateTokenUsage(d, resp, "") // message also empty → estimate=0

	if len(d.accumulatedTokens) != 0 {
		t.Fatalf("expected no accumulate when estimated==0, got %v", d.accumulatedTokens)
	}
}

// --- markPromptCompleteAndFlush tests ---

func TestPromptDispatcher_MarkPromptCompleteAndFlush_NotClosed_ReturnsFalse(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.isClosed = false

	closed := p.markPromptCompleteAndFlush(d)
	if closed {
		t.Fatal("expected false when session is not closed")
	}
	if d.markCompleteCount != 1 {
		t.Fatalf("expected pdMarkPromptComplete called once, got %d", d.markCompleteCount)
	}
	if d.flushMarkdownCount != 1 {
		t.Fatalf("expected pdFlushMarkdown called once, got %d", d.flushMarkdownCount)
	}
	// Streaming state change: false (prompt completed)
	if len(d.streamingChanges) != 1 || d.streamingChanges[0] != false {
		t.Fatalf("expected streaming=false change, got %v", d.streamingChanges)
	}
}

func TestPromptDispatcher_MarkPromptCompleteAndFlush_IsClosed_ReturnsTrue(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.isClosed = true

	closed := p.markPromptCompleteAndFlush(d)
	if !closed {
		t.Fatal("expected true when session is closed")
	}
	// flush must NOT have been called after early return
	if d.flushMarkdownCount != 0 {
		t.Fatalf("expected no flush when closed, got %d", d.flushMarkdownCount)
	}
}

// --- handlePromptSuccess tests ---

func TestPromptDispatcher_HandlePromptSuccess_NotDispatched_SessionIdle(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.processNextResult = false // no queued message dispatched

	sessionIdle := p.handlePromptSuccess(d, 3, 2, acp.PromptResponse{}, "msg", PromptMeta{}, time.Now(), time.Now())

	if !sessionIdle {
		t.Fatal("expected sessionIdle=true when no queued message dispatched")
	}
	if d.flushConfigCount != 1 {
		t.Fatalf("expected 1 flushPendingConfig call, got %d", d.flushConfigCount)
	}
	if d.processNextCalled != 1 {
		t.Fatalf("expected 1 processNextQueuedMessage call, got %d", d.processNextCalled)
	}
}

func TestPromptDispatcher_HandlePromptSuccess_Dispatched_NotSessionIdle(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.processNextResult = true // queued message dispatched

	sessionIdle := p.handlePromptSuccess(d, 1, 1, acp.PromptResponse{}, "msg", PromptMeta{}, time.Now(), time.Now())

	if sessionIdle {
		t.Fatal("expected sessionIdle=false when queued message was dispatched")
	}
}

func TestPromptDispatcher_HandlePromptSuccess_EndTurn_ActionButtons_FollowUp(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.actionButtonsOn = true
	d.lastAgentMessage = "agent response here"
	d.immediateQueue = false
	resp := acp.PromptResponse{StopReason: acp.StopReasonEndTurn}

	p.handlePromptSuccess(d, 1, 1, resp, "user prompt", PromptMeta{}, time.Now(), time.Now())

	if len(d.followUpCalls) != 1 {
		t.Fatalf("expected 1 follow-up call, got %d", len(d.followUpCalls))
	}
	if d.followUpCalls[0][0] != "user prompt" || d.followUpCalls[0][1] != "agent response here" {
		t.Fatalf("unexpected follow-up args: %v", d.followUpCalls[0])
	}
}

func TestPromptDispatcher_HandlePromptSuccess_EndTurn_ImmediateQueue_SkipsFollowUp(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.actionButtonsOn = true
	d.lastAgentMessage = "response"
	d.immediateQueue = true // should skip analysis
	resp := acp.PromptResponse{StopReason: acp.StopReasonEndTurn}

	p.handlePromptSuccess(d, 1, 1, resp, "msg", PromptMeta{}, time.Now(), time.Now())

	if len(d.followUpCalls) != 0 {
		t.Fatalf("expected no follow-up when immediate queue, got %d", len(d.followUpCalls))
	}
}

func TestPromptDispatcher_HandlePromptSuccess_AfterProcessors_Called(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasProcessorMgr = true

	p.handlePromptSuccess(d, 0, 0, acp.PromptResponse{}, "msg", PromptMeta{}, time.Now(), time.Now())

	if d.afterProcessorCalls != 1 {
		t.Fatalf("expected 1 applyAfterProcessors call, got %d", d.afterProcessorCalls)
	}
}

// --- finalizeTurn tests ---

func TestPromptDispatcher_FinalizeTurn_OnComplete_Called(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	var completedErr error
	completed := false
	meta := PromptMeta{
		OnComplete: func(err error) {
			completed = true
			completedErr = err
		},
	}
	sentinel := errors.New("some error")
	p.finalizeTurn(d, sentinel, meta, false)

	if !completed {
		t.Fatal("expected OnComplete to be called")
	}
	if completedErr != sentinel {
		t.Fatalf("expected OnComplete(sentinel), got %v", completedErr)
	}
}

func TestPromptDispatcher_FinalizeTurn_SessionIdle_TurnIdleCalled(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	p.finalizeTurn(d, nil, PromptMeta{}, true /* sessionIdle */)

	if d.turnIdleCalls != 1 {
		t.Fatalf("expected 1 onTurnIdle call, got %d", d.turnIdleCalls)
	}
}

func TestPromptDispatcher_FinalizeTurn_NotIdle_TurnIdleNotCalled(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	p.finalizeTurn(d, nil, PromptMeta{}, false /* not idle */)

	if d.turnIdleCalls != 0 {
		t.Fatalf("expected no onTurnIdle call when not idle, got %d", d.turnIdleCalls)
	}
}

func TestPromptDispatcher_FinalizeTurn_SelfDestruct_Triggered(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.selfDestructRequested = true

	p.finalizeTurn(d, nil, PromptMeta{}, false)

	if d.selfDestructCalls != 1 {
		t.Fatalf("expected 1 self-destruct call, got %d", d.selfDestructCalls)
	}
}

func TestPromptDispatcher_FinalizeTurn_NoSelfDestruct_NotTriggered(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.selfDestructRequested = false

	p.finalizeTurn(d, nil, PromptMeta{}, false)

	if d.selfDestructCalls != 0 {
		t.Fatalf("expected no self-destruct, got %d", d.selfDestructCalls)
	}
}

func TestPromptDispatcher_FinalizeTurn_OnCompleteBeforeTurnIdle(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	callOrder := []string{}
	meta := PromptMeta{
		OnComplete: func(error) {
			callOrder = append(callOrder, "OnComplete")
		},
	}
	// Override pdOnTurnIdle to capture order.
	trackingDeps := &orderTrackingDeps{fakePromptDeps: d, order: &callOrder}

	p.finalizeTurn(trackingDeps, nil, meta, true)

	if len(callOrder) < 2 || callOrder[0] != "OnComplete" || callOrder[1] != "TurnIdle" {
		t.Fatalf("expected OnComplete before TurnIdle, got %v", callOrder)
	}
}

// orderTrackingDeps wraps fakePromptDeps to record call order in finalizeTurn.
type orderTrackingDeps struct {
	*fakePromptDeps
	order *[]string
}

func (o *orderTrackingDeps) pdOnTurnIdle() {
	*o.order = append(*o.order, "TurnIdle")
}

// --- handlePromptError tests ---

// helper: make a sentinel error that is neither rate-limit nor context-too-large.
func transientErr() error { return errors.New("generic transient failure") }

func TestPromptDispatcher_HandlePromptError_WatchdogFired_RecoverableMessage_NoRetry(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = false // irrelevant when watchdog fires

	autoRetried := false
	retry := p.handlePromptError(d, transientErr(), &autoRetried, 1, true /* watchdogFired */)

	if retry {
		t.Fatal("expected retry=false for watchdog-fired path")
	}
	if len(d.notifiedErrors) != 1 {
		t.Fatalf("expected 1 error notification, got %d", len(d.notifiedErrors))
	}
	if d.restartCalled != 0 {
		t.Fatal("expected no restart attempt for watchdog-fired path")
	}
	if d.processNextCalled != 0 {
		t.Fatal("expected no queue advance for watchdog-fired path")
	}
}

func TestPromptDispatcher_HandlePromptError_ACPDead_AlreadyAutoRetried_NoRetry(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = true

	autoRetried := true
	retry := p.handlePromptError(d, transientErr(), &autoRetried, 2, false)

	if retry {
		t.Fatal("expected retry=false when already auto-retried")
	}
	if len(d.notifiedErrors) != 1 {
		t.Fatalf("expected 1 error notification, got %d", len(d.notifiedErrors))
	}
	if d.restartCalled != 0 {
		t.Fatal("expected no restart when already auto-retried")
	}
}

func TestPromptDispatcher_HandlePromptError_ACPDead_CanRestart_Success_ReturnsRetryTrue(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = true
	d.canRestart = true
	d.restartErr = nil // restart succeeds

	autoRetried := false
	retry := p.handlePromptError(d, transientErr(), &autoRetried, 0, false)

	if !retry {
		t.Fatal("expected retry=true after successful restart")
	}
	if !autoRetried {
		t.Fatal("expected *autoRetried set to true after successful restart")
	}
	if d.restartCalled != 1 {
		t.Fatalf("expected 1 restart call, got %d", d.restartCalled)
	}
	if d.reacquireCalls != 1 {
		t.Fatalf("expected 1 pdReacquirePromptingState call, got %d", d.reacquireCalls)
	}
	// streaming state must be set to true (retry is about to fire)
	if len(d.streamingChanges) == 0 || d.streamingChanges[len(d.streamingChanges)-1] != true {
		t.Fatalf("expected streamingChanged(true) notification, got %v", d.streamingChanges)
	}
	// "Retrying your message automatically..." notification must be present
	found := false
	for _, msg := range d.notifiedErrors {
		if len(msg) > 0 && msg != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected at least one observer notification on restart success")
	}
}

func TestPromptDispatcher_HandlePromptError_ACPDead_CanRestart_Fails_NoRetry(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = true
	d.canRestart = true
	d.restartErr = errors.New("restart failed permanently")

	autoRetried := false
	retry := p.handlePromptError(d, transientErr(), &autoRetried, 0, false)

	if retry {
		t.Fatal("expected retry=false when restart fails")
	}
	if autoRetried {
		t.Fatal("expected *autoRetried NOT set when restart fails")
	}
	if d.reacquireCalls != 0 {
		t.Fatal("expected no pdReacquirePromptingState when restart fails")
	}
	// Must notify a failure message
	if len(d.notifiedErrors) < 2 {
		t.Fatalf("expected ≥2 error notifications (restart attempt + failure), got %d", len(d.notifiedErrors))
	}
}

func TestPromptDispatcher_HandlePromptError_ACPDead_NoRestart_KeepsCrashingMessage(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = true
	d.canRestart = false // restart limit exceeded

	autoRetried := false
	retry := p.handlePromptError(d, transientErr(), &autoRetried, 0, false)

	if retry {
		t.Fatal("expected retry=false when restart not available")
	}
	if len(d.notifiedErrors) != 1 {
		t.Fatalf("expected 1 error notification, got %d", len(d.notifiedErrors))
	}
	// Must be the "keeps crashing" message
	if !containsSubstring(d.notifiedErrors[0], "keeps crashing") {
		t.Fatalf("expected 'keeps crashing' message, got %q", d.notifiedErrors[0])
	}
}

func TestPromptDispatcher_HandlePromptError_Transient_AdvancesQueue(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = false

	autoRetried := false
	retry := p.handlePromptError(d, transientErr(), &autoRetried, 0, false)

	if retry {
		t.Fatal("expected retry=false for transient error")
	}
	// queue must be advanced for plain transient errors
	if d.processNextCalled != 1 {
		t.Fatalf("expected 1 processNextQueuedMessage call, got %d", d.processNextCalled)
	}
	if d.flushConfigCount != 1 {
		t.Fatalf("expected 1 flushPendingConfig call, got %d", d.flushConfigCount)
	}
}

func TestPromptDispatcher_HandlePromptError_RateLimitError_QueueNotAdvanced(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = false

	// rateLimitErr: use a string that triggers isRateLimitError
	rlErr := &fakeRateLimitError{}
	autoRetried := false
	p.handlePromptError(d, rlErr, &autoRetried, 0, false)

	if d.processNextCalled != 0 {
		t.Fatalf("expected no queue advance for rate-limit error, got %d", d.processNextCalled)
	}
}

func TestPromptDispatcher_HandlePromptError_ContextTooLargeError_QueueNotAdvanced(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = false

	ctxErr := &fakeContextTooLargeError{}
	autoRetried := false
	p.handlePromptError(d, ctxErr, &autoRetried, 0, false)

	if d.processNextCalled != 0 {
		t.Fatalf("expected no queue advance for context-too-large error, got %d", d.processNextCalled)
	}
}

// containsSubstring is a simple helper to avoid importing strings in test.
func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// fakeRateLimitError mimics the shape isRateLimitError checks.
// The actual check is done by the free function isRateLimitError(err) in the package.
// We need an error that satisfies that function's predicate.
type fakeRateLimitError struct{}

func (e *fakeRateLimitError) Error() string { return "rate_limit_error: too many requests" }

// fakeContextTooLargeError mimics the shape isContextTooLargeError checks.
type fakeContextTooLargeError struct{}

func (e *fakeContextTooLargeError) Error() string { return "context_length_exceeded: 413" }


// --- mitto-pchx.3: prompt-arg cache merge + write-back tests ---

// boolPtr is a tiny helper for *bool fields.
func boolPtr(b bool) *bool { return &b }

// TestResolveAndSubstitute_Cache_WriteBackAndAutoFill verifies that a cacheable
// arg supplied on a first dispatch is written to the cache, and that a second
// dispatch with the arg absent auto-fills it from the cache and substitutes it
// into the body. It also confirms the auto-filled arg appears in argument_names.
func TestResolveAndSubstitute_Cache_WriteBackAndAutoFill(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) { return "Hi ${NAME}", nil }
	d.promptParams = []config.PromptParameter{
		{Name: "NAME", Type: "string", Cache: &config.PromptParameterCache{Destination: "memory"}},
	}

	// First call: arg supplied → substituted into body AND written to cache.
	msg1, argCount1, meta1, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet", Arguments: map[string]string{"NAME": "Alice"}})
	if err != nil {
		t.Fatalf("first call unexpected error: %v", err)
	}
	if msg1 != "Hi Alice" {
		t.Fatalf("first call: expected substituted message, got %q", msg1)
	}
	if argCount1 != 1 {
		t.Fatalf("first call: expected argCount=1, got %d", argCount1)
	}
	if v, ok := d.argCache.Get("greet", "NAME"); !ok || v != "Alice" {
		t.Fatalf("expected cache populated with NAME=Alice after first call, got (%q, %v)", v, ok)
	}
	// Sanity: argument_names lists NAME on the supplied-arg path.
	if names, ok := meta1.Meta["argument_names"].([]string); !ok || len(names) != 1 || names[0] != "NAME" {
		t.Fatalf("first call: expected argument_names=[NAME], got %v", meta1.Meta["argument_names"])
	}

	// Second call: arg absent → auto-filled from cache + substituted.
	msg2, argCount2, meta2, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet"})
	if err != nil {
		t.Fatalf("second call unexpected error: %v", err)
	}
	if msg2 != "Hi Alice" {
		t.Fatalf("second call: expected auto-filled message %q, got %q", "Hi Alice", msg2)
	}
	if argCount2 != 1 {
		t.Fatalf("second call: expected argCount=1 from auto-fill, got %d", argCount2)
	}
	if names, ok := meta2.Meta["argument_names"].([]string); !ok || len(names) != 1 || names[0] != "NAME" {
		t.Fatalf("second call: expected argument_names=[NAME] from auto-fill, got %v", meta2.Meta["argument_names"])
	}
}

// TestResolveAndSubstitute_Cache_ExpiredNotAutoFilled verifies that an entry
// past its TTL is NOT auto-filled and the body keeps its ${NAME:-default} default.
func TestResolveAndSubstitute_Cache_ExpiredNotAutoFilled(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) { return "Hi ${NAME:-stranger}", nil }
	d.promptParams = []config.PromptParameter{
		{Name: "NAME", Type: "string", Cache: &config.PromptParameterCache{Destination: "memory", TTL: "20ms"}},
	}

	// Populate cache via a first supplied-arg call.
	if _, _, _, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet", Arguments: map[string]string{"NAME": "Alice"}}); err != nil {
		t.Fatalf("seed call unexpected error: %v", err)
	}
	if v, ok := d.argCache.Get("greet", "NAME"); !ok || v != "Alice" {
		t.Fatalf("seed: expected cache populated, got (%q, %v)", v, ok)
	}

	// Wait past TTL.
	time.Sleep(40 * time.Millisecond)

	// Second call with no args: cache expired → arg not filled, no substitution runs.
	msg, argCount, _, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "Hi ${NAME:-stranger}" {
		t.Fatalf("expected raw body kept (no substitution), got %q", msg)
	}
	if argCount != 0 {
		t.Fatalf("expected argCount=0 when cache expired, got %d", argCount)
	}
}

// TestResolveAndSubstitute_Cache_NonCacheableNotStored verifies that a parameter
// without a Cache config is never written to the cache, even when supplied.
func TestResolveAndSubstitute_Cache_NonCacheableNotStored(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) { return "Hi ${NAME}", nil }
	d.promptParams = []config.PromptParameter{
		{Name: "NAME", Type: "string"}, // Cache == nil
	}

	if _, _, _, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet", Arguments: map[string]string{"NAME": "Alice"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := d.argCache.Get("greet", "NAME"); ok {
		t.Fatal("expected non-cacheable arg NOT written to cache")
	}
}

// TestResolveAndSubstitute_Cache_NilResolverSafe verifies that with a nil
// parameters resolver (or unknown prompt) the dispatcher still works and
// nothing is cached.
func TestResolveAndSubstitute_Cache_NilResolverSafe(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) { return "Hi ${NAME}", nil }
	d.promptParams = nil // resolver returns nil — simulates unknown/unparameterised prompt

	msg, argCount, _, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet", Arguments: map[string]string{"NAME": "Alice"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "Hi Alice" {
		t.Fatalf("expected substituted message, got %q", msg)
	}
	if argCount != 1 {
		t.Fatalf("expected argCount=1, got %d", argCount)
	}
	if _, ok := d.argCache.Get("greet", "NAME"); ok {
		t.Fatal("expected no cache write when resolver returns nil params")
	}
}

// TestResolveAndSubstitute_Cache_RequiredPtrNotInterferingWithCache ensures that
// the Required field (an unrelated *bool) does not affect cache merge/write-back.
func TestResolveAndSubstitute_Cache_RequiredPtrNotInterferingWithCache(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) { return "Hi ${NAME}", nil }
	d.promptParams = []config.PromptParameter{
		{Name: "NAME", Type: "string", Required: boolPtr(true), Cache: &config.PromptParameterCache{Destination: "memory"}},
	}

	if _, _, _, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet", Arguments: map[string]string{"NAME": "Alice"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := d.argCache.Get("greet", "NAME"); !ok || v != "Alice" {
		t.Fatalf("expected cache populated, got (%q, %v)", v, ok)
	}
}
