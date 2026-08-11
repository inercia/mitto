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

	mittoAcp "github.com/inercia/mitto/internal/acp"
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
	sessionMeta                    session.Metadata
	sessionMetaErr                 error
	metaByID                       map[string]session.Metadata
	childSessions                  []session.Metadata
	childSessionsErr               error
	workspacePeers                 []session.Metadata
	workspacePeersErr              error
	childPrompting                 map[string]bool
	childQueueLength               map[string]int
	mcpToolNames                   []string
	promptsSnapshotFn              func() *config.PromptsSnapshot
	userData                       *session.UserData
	userDataErr                    error
	sessionCtx                     context.Context
	hasProcessorMgr                bool
	applyResult                    *processors.ProcessorResult
	applyErr                       error
	persistActivationCalls         int
	historyPrefix                  string // prefix injected by pdBuildPromptWithHistory

	// === New in 2.5-c ===
	hasSharedProcess bool
	// sharedProcessHistory backs pdSharedProcessHistory; zero value
	// (mittoAcp.ProcessHistoryUnknown) matches the "no shared process" default.
	sharedProcessHistory mittoAcp.ProcessHistory
	handshakeErr         error
	handshakeCalls       int
	// handshakeBlock, when non-nil, is closed by the test to release a blocked
	// pdCompleteDeferredHandshake. Used to simulate a wedged handshake for the
	// completeHandshakeOrAbort watchdog test (mitto-f51).
	handshakeBlock chan struct{}
	// handshakeDeadline is returned by pdRecommendedHandshakeDeadline (mitto-f51).
	handshakeDeadline   time.Duration
	hasRecorder         bool
	recordedErrorEvents []string
	nextSeq             int64
	refreshSeqCalls     int
	promptingResetCalls int
	streamingChanges    []bool
	hasACPConn          bool
	acpNewSessionID     string
	acpNewSessionErr    error
	// acpNewSessionCalls counts pdACPConnNewSession invocations (mitto-2efc:
	// asserts the new-session fallback is/isn't reached after an in-place
	// context-flush failure).
	acpNewSessionCalls     int
	agentModels            *SessionModelState
	resolvedModelTags      []string
	modelTagsByName        map[string][]string // when set, pdResolveModelTags keys off the model name
	resolvedPreferred      []config.PromptPreferredModel
	modelProfiles          []config.ModelProfile
	baselineModel          string
	overrideActive         bool
	setActiveModelCalls    []string
	setActiveModelErr      error
	setActiveModelGate     chan struct{} // if non-nil, block in pdSetActiveModelOnly until closed (simulates a slow/cold set_model)
	recordedSessionChanges []session.SessionChangeData
	// recordedSessionChangeSeqs (mitto-c36) mirrors recordedSessionChanges 1:1
	// and captures the seq passed via pdRecordSessionChangeWithSeq (or 0 when
	// the seq-less pdRecordSessionChange was used).
	recordedSessionChangeSeqs []int64

	// === New in 2.5-d ===
	lastUsageSet               *acp.Usage
	cumulativeUsageSet         []*acp.Usage
	accumulatedTokens          []int
	estimatedTokenCalls        []string // messages passed to pdEstimateTokensFromMessage
	lastAgentMessage           string   // returned by pdReadLastAgentMessage / pdReadLastAgentMessageFromStore
	markCompleteCount          int
	dismissActiveUIPromptCalls int
	isClosed                   bool
	flushMarkdownCount         int
	observerCount              int
	eventCount                 int
	flushConfigCount           int
	processNextCalled          int
	processNextResult          bool // return value for pdProcessNextQueuedMessage
	retryTitleCalls            []string
	actionButtonsOn            bool
	immediateQueue             bool
	followUpCalls              [][]string // each element is [userMsg, agentMsg]
	afterProcessorCalls        int
	turnIdleCalls              int
	selfDestructRequested      bool
	selfDestructCalls          int
	onCompleteCallOrder        []string // records "OnComplete" / "TurnIdle" / "SelfDestruct" in order

	// === New in 2.5-e ===
	acpDead        bool
	canRestart     bool
	restartInfo    string
	restartErr     error
	restartCalled  int
	reacquireCalls int

	// === New in mitto-2tm: in-place context flush ===
	contextFlushCommand    string
	flushContextInPlaceErr error
	flushContextCalled     bool

	// === mitto-pchx.3: per-conversation prompt-argument cache ===
	// promptParams is returned by pdResolvePromptParameters (nil ⇒ resolver returns nil).
	promptParams []config.PromptParameter
	// argCache is a real per-conversation cache backing pdCacheGetArg/pdCacheSetArg so
	// dispatcher tests can exercise the merge + write-back path end-to-end.
	argCache *promptArgCache

	// === New in mitto-s9g2: skip redundant FreshContext clear on a virgin session ===
	// contextIsEmpty backs pdContextIsEmpty. Defaults to false so all pre-existing
	// FreshContext dispatcher tests keep exercising the flush/new-session paths
	// unchanged; virginity-skip tests set this to true explicitly.
	contextIsEmpty bool
}

func newFakePromptDeps() *fakePromptDeps {
	return &fakePromptDeps{
		logger:           slog.Default(),
		sessionID:        "test-session",
		hasStore:         true,
		agentImages:      true,
		imagePaths:       make(map[string]string),
		imageErrs:        make(map[string]error),
		filePaths:        make(map[string]string),
		fileErrs:         make(map[string]error),
		metaByID:         make(map[string]session.Metadata),
		childPrompting:   make(map[string]bool),
		childQueueLength: make(map[string]int),
		sessionCtx:       context.Background(),
		argCache:         newPromptArgCache(),
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
func (f *fakePromptDeps) pdListWorkspacePeers() ([]session.Metadata, error) {
	return f.workspacePeers, f.workspacePeersErr
}
func (f *fakePromptDeps) pdIsChildPrompting(id string) bool { return f.childPrompting[id] }
func (f *fakePromptDeps) pdChildQueueLength(id string) int  { return f.childQueueLength[id] }
func (f *fakePromptDeps) pdCachedMCPToolNames() []string    { return f.mcpToolNames }
func (f *fakePromptDeps) pdPromptsSnapshot() func() *config.PromptsSnapshot {
	return f.promptsSnapshotFn
}
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
func (f *fakePromptDeps) pdSharedProcessHistory() mittoAcp.ProcessHistory {
	return f.sharedProcessHistory
}
func (f *fakePromptDeps) pdCompleteDeferredHandshake() error {
	f.mu.Lock()
	f.handshakeCalls++
	block := f.handshakeBlock
	err := f.handshakeErr
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return err
}
func (f *fakePromptDeps) pdRecommendedHandshakeDeadline() time.Duration {
	return f.handshakeDeadline
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
	f.mu.Lock()
	f.acpNewSessionCalls++
	f.mu.Unlock()
	return f.acpNewSessionID, f.acpNewSessionErr
}
func (f *fakePromptDeps) pdGetAgentModels() *SessionModelState { return f.agentModels }
func (f *fakePromptDeps) pdResolveModelTags(name string) []string {
	if f.modelTagsByName != nil {
		return f.modelTagsByName[name]
	}
	return f.resolvedModelTags
}
func (f *fakePromptDeps) pdResolvePreferredModels(_ string) []config.PromptPreferredModel {
	return f.resolvedPreferred
}
func (f *fakePromptDeps) pdModelProfiles() []config.ModelProfile { return f.modelProfiles }
func (f *fakePromptDeps) pdReadBaselineModel() string            { return f.baselineModel }
func (f *fakePromptDeps) pdWriteOverrideActive(active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrideActive = active
}
func (f *fakePromptDeps) pdSetActiveModelOnly(ctx context.Context, modelID string) error {
	f.mu.Lock()
	f.setActiveModelCalls = append(f.setActiveModelCalls, modelID)
	gate := f.setActiveModelGate
	err := f.setActiveModelErr
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}
func (f *fakePromptDeps) pdRecordSessionChange(kind, value, previousValue string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedSessionChanges = append(f.recordedSessionChanges, session.SessionChangeData{
		Kind: kind, Value: value, PreviousValue: previousValue,
	})
	f.recordedSessionChangeSeqs = append(f.recordedSessionChangeSeqs, 0)
}

// pdRecordSessionChangeWithSeq (mitto-c36): captures the caller-reserved seq
// alongside the change so tests can assert the pill seq. Kind/value/previous are
// appended to recordedSessionChanges (same slice as the seq-less variant) so
// existing assertions on pill content continue to work.
func (f *fakePromptDeps) pdRecordSessionChangeWithSeq(seq int64, kind, value, previousValue string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedSessionChanges = append(f.recordedSessionChanges, session.SessionChangeData{
		Kind: kind, Value: value, PreviousValue: previousValue,
	})
	f.recordedSessionChangeSeqs = append(f.recordedSessionChangeSeqs, seq)
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
func (f *fakePromptDeps) pdAccumulateCumulativeUsage(usage *acp.Usage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cumulativeUsageSet = append(f.cumulativeUsageSet, usage)
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
func (f *fakePromptDeps) pdDismissActiveUIPrompt() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissActiveUIPromptCalls++
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

// === New in mitto-2tm ===

func (f *fakePromptDeps) pdContextFlushCommand() string { return f.contextFlushCommand }
func (f *fakePromptDeps) pdContextIsEmpty() bool        { return f.contextIsEmpty }
func (f *fakePromptDeps) pdFlushContextInPlace(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushContextCalled = true
	return f.flushContextInPlaceErr
}

// mitto-3mv WI-2: cold-start trace stub — no-op in tests.
func (f *fakePromptDeps) pdColdPhase(_ string, _ ...any) {}

type pdRecorderObserver struct{ deps *fakePromptDeps }

func (r *pdRecorderObserver) OnError(msg string) {
	r.deps.mu.Lock()
	r.deps.notifiedErrors = append(r.deps.notifiedErrors, msg)
	r.deps.mu.Unlock()
}
func (r *pdRecorderObserver) OnAgentMessage(int64, string, string)          {}
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
func (r *pdRecorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int, map[string]string) {
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

// TestResolveAndSubstitute_PromptNameAlwaysResolvedWhenSet verifies the mitto-kt6
// resolver-layer defense: whenever meta.PromptName is set, the named prompt is
// resolved and its body replaces any incoming free-text message — even a
// non-empty placeholder like "__placeholder__". This closes the class for any
// entry point (e.g. a queued row) that might carry both fields.
func TestResolveAndSubstitute_PromptNameAlwaysResolvedWhenSet(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) {
		if name != "X" {
			t.Fatalf("unexpected prompt name: %q", name)
		}
		return "resolved body of X", nil
	}

	msg, _, _, err := p.resolveAndSubstitute(d, "__placeholder__", PromptMeta{PromptName: "X"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "resolved body of X" {
		t.Fatalf("expected resolved body to replace placeholder, got %q", msg)
	}
	if strings.Contains(msg, "__placeholder__") {
		t.Fatalf("placeholder leaked into resolved message: %q", msg)
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
		"Hello {{ .Args.NAME }}, welcome to {{ .Args.CITY }}!", PromptMeta{Arguments: args})
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

// TestResolveAndSubstitute_Template_ArgsAvailableAtRender verifies that
// .Args values are available during template rendering via {{ .Args.SUFFIX }}.
func TestResolveAndSubstitute_Template_ArgsAvailableAtRender(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.sessionID = "sess-X"

	// Template uses .Session.ID and .Args.SUFFIX directly in one render pass.
	body := "{{ .Session.ID }}-{{ .Args.SUFFIX }}"
	args := map[string]string{"SUFFIX": "end"}
	msg, _, _, err := p.resolveAndSubstitute(d, body, PromptMeta{Arguments: args})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "sess-X-end" {
		t.Fatalf("expected rendered result, got %q", msg)
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

// TestResolveAndSubstitute_Template_PromptText_Wired verifies that the
// PromptText template function is wired to the dispatcher's PromptResolver at
// render time (mitto-85y.3): a body invoking `{{ PromptText .Args.Prompt }}`
// with Arguments["Prompt"]="known" resolves the named prompt body via the
// same resolver the dispatcher uses for named-prompt resolution, and inlines
// it verbatim in the outer render.
func TestResolveAndSubstitute_Template_PromptText_Wired(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.workingDir = "/tmp/ws"
	// Resolver used both for named-prompt resolution AND for PromptText.
	d.resolver = func(name, workingDir string) (string, error) {
		if name == "known" {
			return "resolved-body", nil
		}
		return "", errors.New("prompt not found: " + name)
	}

	body := `pre {{ PromptText .Args.Prompt }} post`
	meta := PromptMeta{Arguments: map[string]string{"Prompt": "known"}}
	msg, _, _, err := p.resolveAndSubstitute(d, body, meta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "pre resolved-body post" {
		t.Fatalf("expected inlined body, got %q", msg)
	}
}

// TestResolveAndSubstitute_Template_PromptText_UnknownFailsClosed verifies
// that PromptText fails-closed when the referenced prompt does not exist,
// on the NAMED-PROMPT path: the resolver error propagates as a template
// render error and the outer send is aborted (mitto-85y.3). Free-text
// bodies fail-open on template errors (mitto-gnxe), so this path is
// exercised via PromptMeta.PromptName.
func TestResolveAndSubstitute_Template_PromptText_UnknownFailsClosed(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) {
		if name == "outer" {
			return `{{ PromptText "missing" }}`, nil
		}
		return "", errors.New("prompt not found: " + name)
	}

	msg, _, _, err := p.resolveAndSubstitute(d, "", PromptMeta{PromptName: "outer"})
	if err == nil {
		t.Fatalf("expected non-nil error for unknown PromptText target, got msg=%q", msg)
	}
	if msg != "" {
		t.Fatalf("expected empty message on fail-closed, got %q", msg)
	}
}

// TestResolveAndSubstitute_AutomatedDispatch_InvalidTemplate_FailClosed verifies
// that a free-text body with unbalanced template syntax dispatched via an automated
// path (agent-originated queue dispatch / loop-runner) fails CLOSED — it returns a
// non-nil error instead of silently delivering the raw, unrenderable body to a
// child (mitto-e7u).
func TestResolveAndSubstitute_AutomatedDispatch_InvalidTemplate_FailClosed(t *testing.T) {
	p := promptDispatcher{}
	body := "{{ if .Broken }}" // unbalanced action -> "unexpected EOF"

	cases := []struct {
		name string
		meta PromptMeta
	}{
		{name: senderIDQueue, meta: PromptMeta{SenderID: senderIDQueue, QueueOrigin: session.QueueOriginAgent}},
		{name: senderIDLoop, meta: PromptMeta{SenderID: senderIDLoop}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakePromptDeps()
			msg, _, _, err := p.resolveAndSubstitute(d, body, tc.meta)
			if err == nil {
				t.Fatalf("expected non-nil error for automated dispatch (sender=%q) with invalid template, got msg=%q", tc.name, msg)
			}
			if msg != "" {
				t.Fatalf("expected empty message on fail-closed, got %q", msg)
			}
		})
	}
}

// TestResolveAndSubstitute_QueueUserOrigin_InvalidTemplate_FailOpen verifies that a
// queue dispatch ORIGINATING FROM A HUMAN (QueueOrigin == user, or empty for
// backward compatibility) fails OPEN on an invalid template body — the raw text is
// delivered verbatim instead of being dropped (mitto-nvb). Only agent-originated
// queue dispatches (cross-session/MCP) fail closed.
func TestResolveAndSubstitute_QueueUserOrigin_InvalidTemplate_FailOpen(t *testing.T) {
	p := promptDispatcher{}
	body := "{{ if .Broken }}" // unbalanced action -> "unexpected EOF"

	cases := []struct {
		name        string
		queueOrigin string
	}{
		{name: "explicit_user_origin", queueOrigin: session.QueueOriginUser},
		{name: "empty_origin_backward_compat", queueOrigin: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakePromptDeps()
			meta := PromptMeta{SenderID: senderIDQueue, QueueOrigin: tc.queueOrigin}
			msg, _, _, err := p.resolveAndSubstitute(d, body, meta)
			if err != nil {
				t.Fatalf("expected nil error for user-origin queue dispatch with invalid template, got: %v", err)
			}
			if msg != body {
				t.Fatalf("expected raw body byte-for-byte, got %q", msg)
			}
		})
	}
}

// TestResolveAndSubstitute_AgentQueueOrigin_ExecuteError_FailOpen verifies that
// an agent-origin queue dispatch (SenderID=queue, QueueOrigin=agent, no
// PromptName — i.e. mitto_conversation_send_prompt called with raw 'prompt:'
// free text) whose body PARSES cleanly but references a missing field/method
// at exec time (e.g. literal "{{ .DefaultText }}" quoted from a fragment
// docstring) is delivered raw with a warn log — NOT fail-closed and silently
// dropped after the queue-dispatcher's bounded retry. Reproduces mitto-z6f:
// 3 driver-composed messages were lost because "prompt template \"prompt\":
// render error: template: prompt:31:121: can't evaluate field DefaultText in
// type *cel.PromptEnabledContext" was treated as fail-closed. Parse errors on
// the same origin remain fail-closed (see the AutomatedDispatch test above)
// so the mitto-e7u guarantee for structurally broken templates is unchanged.
func TestResolveAndSubstitute_AgentQueueOrigin_ExecuteError_FailOpen(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	// Parses as a valid pipeline but "DefaultText" is not a field of
	// *cel.PromptEnabledContext, so Execute reports:
	//   can't evaluate field DefaultText in type *cel.PromptEnabledContext
	body := "please see fragment docs: {{ .DefaultText }} — end"
	meta := PromptMeta{SenderID: senderIDQueue, QueueOrigin: session.QueueOriginAgent}
	msg, _, _, err := p.resolveAndSubstitute(d, body, meta)
	if err != nil {
		t.Fatalf("expected nil error (fail-open) for agent-origin queue dispatch with execute-time template error, got: %v", err)
	}
	if msg != body {
		t.Fatalf("expected raw body byte-for-byte, got %q", msg)
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
	if input.IsLoop {
		t.Fatal("expected IsLoop=false for non-loop sender")
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

	input := p.buildProcessorInput(d, "test", true, PromptMeta{SenderID: "loop-runner"})

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
	if !input.IsLoop {
		t.Fatal("expected IsLoop=true for loop-runner sender")
	}
	if input.BeadsIssue != "mitto-123" {
		t.Fatalf("expected BeadsIssue='mitto-123', got %q", input.BeadsIssue)
	}
}

// TestPromptDispatcher_BuildProcessorInput_ChildQueuedCount verifies that
// pdChildQueueLength results propagate to ProcessorInput.ChildSessions[i].QueuedCount
// so cleanup/orchestrator prompts can identify children with pending queued
// work without a per-child mitto_conversation_get fan-out (mitto-p9r).
func TestPromptDispatcher_BuildProcessorInput_ChildQueuedCount(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.sessionID = "sess-qc"
	d.sessionMeta = session.Metadata{Name: "Parent", ACPServer: "auggie"}
	d.childSessions = []session.Metadata{
		{SessionID: "child-a", Name: "Child A", ACPServer: "auggie"},
		{SessionID: "child-b", Name: "Child B", ACPServer: "auggie"},
		{SessionID: "child-c", Name: "Child C", ACPServer: "auggie"},
	}
	d.childQueueLength["child-a"] = 3
	d.childQueueLength["child-b"] = 0
	// child-c: absent from map → default 0 (documents fail-open semantics)

	input := p.buildProcessorInput(d, "test", false, PromptMeta{})

	if len(input.ChildSessions) != 3 {
		t.Fatalf("expected 3 children, got %d", len(input.ChildSessions))
	}
	byID := map[string]int{}
	for _, c := range input.ChildSessions {
		byID[c.ID] = c.QueuedCount
	}
	if got := byID["child-a"]; got != 3 {
		t.Fatalf("child-a QueuedCount: expected 3, got %d", got)
	}
	if got := byID["child-b"]; got != 0 {
		t.Fatalf("child-b QueuedCount: expected 0, got %d", got)
	}
	if got := byID["child-c"]; got != 0 {
		t.Fatalf("child-c QueuedCount (unset → default): expected 0, got %d", got)
	}
}

// TestPromptDispatcher_BuildProcessorInput_WorkspacePeers verifies that
// pdListWorkspacePeers results propagate to ProcessorInput.WorkspacePeers
// with the correct field mapping and IsPrompting resolved via
// pdIsChildPrompting (which is a generic session-ID prompting check reused
// for peers; see mitto-4d6 implementation). The store-error path must fail
// open (leave WorkspacePeers empty).
func TestPromptDispatcher_BuildProcessorInput_WorkspacePeers(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.sessionID = "self"
	d.sessionMeta = session.Metadata{Name: "Self", ACPServer: "auggie"}
	d.workspacePeers = []session.Metadata{
		{
			SessionID:       "peer-1",
			Name:            "Peer A",
			ACPServer:       "auggie",
			ParentSessionID: "self",
			ChildOrigin:     session.ChildOriginAuto,
			BeadsIssue:      "mitto-1",
		},
		{
			SessionID: "peer-2",
			Name:      "Peer B",
			ACPServer: "auggie",
		},
	}
	// Reuse the generic prompting map — pdIsChildPrompting is the same
	// resolver used for both children and peers.
	d.childPrompting["peer-1"] = true
	d.childPrompting["peer-2"] = false

	input := p.buildProcessorInput(d, "msg", false, PromptMeta{})

	if len(input.WorkspacePeers) != 2 {
		t.Fatalf("expected 2 workspace peers, got %d: %+v", len(input.WorkspacePeers), input.WorkspacePeers)
	}
	got := map[string]processors.PeerSession{}
	for _, p := range input.WorkspacePeers {
		got[p.ID] = p
	}
	p1, ok := got["peer-1"]
	if !ok {
		t.Fatalf("missing peer-1: %+v", input.WorkspacePeers)
	}
	if p1.Name != "Peer A" || p1.ACPServer != "auggie" || p1.ParentID != "self" ||
		p1.ChildOrigin != string(session.ChildOriginAuto) || !p1.IsPrompting || p1.BeadsIssue != "mitto-1" {
		t.Errorf("peer-1 field mismatch: %+v", p1)
	}
	p2, ok := got["peer-2"]
	if !ok {
		t.Fatalf("missing peer-2: %+v", input.WorkspacePeers)
	}
	if p2.Name != "Peer B" || p2.ACPServer != "auggie" || p2.ParentID != "" ||
		p2.ChildOrigin != "" || p2.IsPrompting || p2.BeadsIssue != "" {
		t.Errorf("peer-2 field mismatch: %+v", p2)
	}
}

// TestPromptDispatcher_BuildProcessorInput_WorkspacePeersError verifies that
// a store error from pdListWorkspacePeers is swallowed (fail-open) so
// menu-time gating and processor invocation are never broken by a peer
// lookup failure — matches the sibling ChildSessions error handling.
func TestPromptDispatcher_BuildProcessorInput_WorkspacePeersError(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.sessionID = "self"
	d.workspacePeersErr = errors.New("simulated store failure")

	input := p.buildProcessorInput(d, "msg", false, PromptMeta{})
	if len(input.WorkspacePeers) != 0 {
		t.Fatalf("expected empty WorkspacePeers on error, got %+v", input.WorkspacePeers)
	}
}

// TestPromptDispatcher_BuildProcessorInput_WorkspacePeersNoStore verifies
// that a missing store leaves WorkspacePeers empty (peers block gated on
// pdHasStore, same as ChildSessions).
func TestPromptDispatcher_BuildProcessorInput_WorkspacePeersNoStore(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasStore = false
	d.workspacePeers = []session.Metadata{{SessionID: "peer-1", Name: "Peer A"}}

	input := p.buildProcessorInput(d, "msg", false, PromptMeta{})
	if len(input.WorkspacePeers) != 0 {
		t.Fatalf("expected empty WorkspacePeers when no store, got %+v", input.WorkspacePeers)
	}
}

func TestPromptDispatcher_BuildProcessorInput_IsLoopForced(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasStore = false

	meta := PromptMeta{IsLoopForced: true}
	input := p.buildProcessorInput(d, "msg", false, meta)
	if !input.IsLoopForced {
		t.Fatal("expected IsLoopForced=true")
	}
}

// TestPromptDispatcher_BuildProcessorInput_IsLoopRunOnStart verifies that the
// boot-pulse flag (mitto-ystk) propagates from PromptMeta into ProcessorInput,
// which is what wires the signal into the @mitto:loop_run_on_start placeholder
// and the CEL Session.IsLoopRunOnStart variable.
func TestPromptDispatcher_BuildProcessorInput_IsLoopRunOnStart(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasStore = false

	meta := PromptMeta{IsLoopRunOnStart: true}
	input := p.buildProcessorInput(d, "msg", false, meta)
	if !input.IsLoopRunOnStart {
		t.Fatal("expected IsLoopRunOnStart=true")
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

// TestPromptDispatcher_BuildProcessorInput_ModelTagsUseIntendedModel verifies that
// buildProcessorInput renders ModelName/ModelTags against the model the dispatch's
// preferredModels resolves to, not the model left active by the previous turn.
// applyModelPreference runs later in the pipeline, so without this a tier-declaring
// prompt (e.g. the beads-issues phase prompts' tier-check) always observed the stale
// tier and reported a spurious tier-degraded run.
func TestPromptDispatcher_BuildProcessorInput_ModelTagsUseIntendedModel(t *testing.T) {
	p := promptDispatcher{}
	newDeps := func() *fakePromptDeps {
		d := newFakePromptDeps()
		d.agentModels = &SessionModelState{
			CurrentModelId: "m-opus",
			AvailableModels: []ModelInfo{
				{ModelId: "m-opus", Name: "Claude Opus"},
				{ModelId: "m-sonnet", Name: "Claude Sonnet"},
			},
		}
		d.modelProfiles = []config.ModelProfile{
			{Name: "Coding", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Sonnet"}, Tags: []string{"Coding"}},
			{Name: "Reasoning", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}, Tags: []string{"Reasoning"}},
		}
		d.modelTagsByName = map[string][]string{
			"Claude Opus":   {"Reasoning"},
			"Claude Sonnet": {"Coding"},
		}
		return d
	}

	// Explicit meta.PreferredModels → intended model wins over the active one.
	d := newDeps()
	meta := PromptMeta{PreferredModels: []config.PromptPreferredModel{{ModelTag: "Coding"}}}
	input := p.buildProcessorInput(d, "msg", false, meta)
	if input.ModelName != "Claude Sonnet" {
		t.Errorf("ModelName = %q, want %q", input.ModelName, "Claude Sonnet")
	}
	if len(input.ModelTags) != 1 || input.ModelTags[0] != "Coding" {
		t.Errorf("ModelTags = %v, want [Coding]", input.ModelTags)
	}

	// Preference declared by the named prompt (resolved via pdResolvePreferredModels).
	d = newDeps()
	d.resolvedPreferred = []config.PromptPreferredModel{{ModelTag: "Coding"}}
	input = p.buildProcessorInput(d, "msg", false, PromptMeta{PromptName: "Feature — test phase"})
	if input.ModelName != "Claude Sonnet" {
		t.Errorf("named-prompt ModelName = %q, want %q", input.ModelName, "Claude Sonnet")
	}

	// No preference → falls back to the active model.
	d = newDeps()
	input = p.buildProcessorInput(d, "msg", false, PromptMeta{})
	if input.ModelName != "Claude Opus" {
		t.Errorf("no-preference ModelName = %q, want %q", input.ModelName, "Claude Opus")
	}

	// Preference that resolves to nothing → falls back to the active model.
	d = newDeps()
	input = p.buildProcessorInput(d, "msg", false, PromptMeta{
		PreferredModels: []config.PromptPreferredModel{{ModelTag: "Nonexistent"}},
	})
	if input.ModelName != "Claude Opus" {
		t.Errorf("unresolvable-preference ModelName = %q, want %q", input.ModelName, "Claude Opus")
	}
}

// TestPromptDispatcher_BuildProcessorInput_TriggerOnTasksChanges verifies that
// buildProcessorInput threads meta.Trigger.OnTasks.Changes (populated by the
// onTasks loop runner) into ProcessorInput.TriggerOnTasksChanges by reference,
// and leaves the field nil for non-onTasks dispatches (mitto-xkn).
func TestPromptDispatcher_BuildProcessorInput_TriggerOnTasksChanges(t *testing.T) {
	p := promptDispatcher{}

	// (1) meta.Trigger == nil → input.TriggerOnTasksChanges must be nil.
	d1 := newFakePromptDeps()
	d1.hasStore = false
	input := p.buildProcessorInput(d1, "msg", false, PromptMeta{})
	if input.TriggerOnTasksChanges != nil {
		t.Errorf("expected TriggerOnTasksChanges=nil when meta.Trigger is nil, got %#v", input.TriggerOnTasksChanges)
	}

	// (2) meta.Trigger set but OnTasks nil → still nil (defensive guard).
	d2 := newFakePromptDeps()
	d2.hasStore = false
	input = p.buildProcessorInput(d2, "msg", false, PromptMeta{Trigger: &PromptTriggerContext{}})
	if input.TriggerOnTasksChanges != nil {
		t.Errorf("expected TriggerOnTasksChanges=nil when meta.Trigger.OnTasks is nil, got %#v", input.TriggerOnTasksChanges)
	}

	// (3) meta.Trigger.OnTasks.Changes set → input.TriggerOnTasksChanges must
	// alias the same *config.TasksDelta (no defensive copy in the hot path).
	delta := &config.TasksDelta{
		Added:   []map[string]any{{"id": "mitto-a", "status": "open"}},
		Updated: []map[string]any{{"id": "mitto-u", "status": "in_progress"}},
		Touched: []map[string]any{{"id": "mitto-a"}, {"id": "mitto-u"}},
	}
	d3 := newFakePromptDeps()
	d3.hasStore = false
	meta := PromptMeta{
		SenderID: "loop-runner",
		Trigger: &PromptTriggerContext{
			OnTasks: &PromptOnTasksContext{Changes: delta},
		},
	}
	input = p.buildProcessorInput(d3, "msg", false, meta)
	if input.TriggerOnTasksChanges == nil {
		t.Fatal("expected TriggerOnTasksChanges non-nil when meta.Trigger.OnTasks.Changes is set")
	}
	if input.TriggerOnTasksChanges != delta {
		t.Errorf("expected TriggerOnTasksChanges to alias meta.Trigger.OnTasks.Changes (pointer equality), got different pointer")
	}
	if len(input.TriggerOnTasksChanges.Added) != 1 || input.TriggerOnTasksChanges.Added[0]["id"] != "mitto-a" {
		t.Errorf("Added: got %#v, want single mitto-a entry", input.TriggerOnTasksChanges.Added)
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

// TestPromptDispatcher_CompleteHandshakeOrAbort_WatchdogFiresOnWedgedHandshake
// verifies mitto-f51: a pdCompleteDeferredHandshake that hangs past the derived
// deadline takes the abort branch (friendly "still starting up" message, prompting
// state reset, streaming state notified) instead of blocking forever.
func TestPromptDispatcher_CompleteHandshakeOrAbort_WatchdogFiresOnWedgedHandshake(t *testing.T) {
	// Shrink the margin so the test isn't blocked for 30s. Restore after.
	origMargin := handshakeWatchdogMargin
	handshakeWatchdogMargin = 10 * time.Millisecond
	defer func() { handshakeWatchdogMargin = origMargin }()

	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasSharedProcess = true
	d.hasRecorder = true
	// Force pdCompleteDeferredHandshake to block indefinitely.
	d.handshakeBlock = make(chan struct{})
	defer close(d.handshakeBlock) // release the orphaned goroutine at test end
	// Tight deadline so the test is fast (base + shrunken margin ~= 20ms).
	d.handshakeDeadline = 10 * time.Millisecond

	done := make(chan bool, 1)
	go func() { done <- p.completeHandshakeOrAbort(d) }()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("expected completeHandshakeOrAbort to return false when watchdog fires")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completeHandshakeOrAbort did not return within 5s despite watchdog")
	}

	// Watchdog trips must not spawn retries — the orphaned goroutine still holds
	// the shared process and another attempt would just re-hang (mitto-f51).
	d.mu.Lock()
	calls := d.handshakeCalls
	d.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected exactly 1 handshake call (no retry on watchdog trip), got %d", calls)
	}

	// The friendly "still starting up" message must be surfaced to observers.
	if len(d.notifiedErrors) != 1 {
		t.Fatalf("expected 1 observer error notification, got %d", len(d.notifiedErrors))
	}
	if !strings.Contains(d.notifiedErrors[0], "still starting up") {
		t.Fatalf("expected 'still starting up' message, got %q", d.notifiedErrors[0])
	}
	// And a recorded error event (recorder is present in this test).
	if len(d.recordedErrorEvents) != 1 {
		t.Fatalf("expected 1 recorded error event, got %d", len(d.recordedErrorEvents))
	}
	// Prompting state must be reset so the user can re-send.
	if d.promptingResetCalls != 1 {
		t.Fatalf("expected 1 prompting reset, got %d", d.promptingResetCalls)
	}
	// Streaming state must be flipped to false.
	if len(d.streamingChanges) != 1 || d.streamingChanges[0] {
		t.Fatalf("expected streaming=false notification, got %v", d.streamingChanges)
	}
}

// TestPromptDispatcher_CompleteHandshakeOrAbort_FallbackDeadlineUsedWhenDepsReportsZero
// verifies runHandshakeWithWatchdog uses handshakeWatchdogFallback when
// pdRecommendedHandshakeDeadline returns 0 — the watchdog is always armed so a
// hung handshake is always recoverable (mitto-f51).
func TestPromptDispatcher_CompleteHandshakeOrAbort_FallbackDeadlineUsedWhenDepsReportsZero(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasSharedProcess = true
	d.handshakeDeadline = 0 // deps has no recommendation
	// Immediate success: the dispatcher must still complete quickly even with
	// the (long) fallback armed.
	d.handshakeErr = nil

	start := time.Now()
	ok := p.completeHandshakeOrAbort(d)
	if !ok {
		t.Fatalf("expected true on immediate success, got false; errors=%v", d.notifiedErrors)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("dispatcher took too long (%v) on immediate success — watchdog blocking?", elapsed)
	}
}

// TestRunHandshakeWithWatchdog_ZeroDeadlineFallsThrough verifies that a
// non-positive deadline bypasses the watchdog goroutine entirely — used as a
// safety valve for tests / callers that explicitly opt out.
func TestRunHandshakeWithWatchdog_ZeroDeadlineFallsThrough(t *testing.T) {
	d := newFakePromptDeps()
	d.handshakeErr = errors.New("boom")

	err := runHandshakeWithWatchdog(d, 0)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected 'boom' error passed through, got %v", err)
	}
	if d.handshakeCalls != 1 {
		t.Fatalf("expected exactly 1 handshake call, got %d", d.handshakeCalls)
	}
}

// --- createFreshContextSession tests ---

func TestPromptDispatcher_CreateFreshContextSession_FreshContextFalse_ReturnsEmpty(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = true

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: false}, 0)
	if id != "" {
		t.Fatalf("expected empty id when FreshContext=false, got %q", id)
	}
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no session change when FreshContext=false, got %v", d.recordedSessionChanges)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_NoACPConn_ReturnsEmpty(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = false

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)
	if id != "" {
		t.Fatalf("expected empty id when no ACP conn, got %q", id)
	}
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no session change when no ACP conn, got %v", d.recordedSessionChanges)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_Success_ReturnsID(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = true
	d.acpNewSessionID = "fresh-session-123"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)
	if id != "fresh-session-123" {
		t.Fatalf("expected 'fresh-session-123', got %q", id)
	}
	// mitto-so19: successful NewSession must record a context_cleared pill.
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 context_cleared pill on NewSession success, got %d: %v", len(d.recordedSessionChanges), d.recordedSessionChanges)
	}
	sc := d.recordedSessionChanges[0]
	if sc.Kind != "context_cleared" || sc.Value != "new_session" || sc.PreviousValue != "" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_ACPError_ReturnsEmpty(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = true
	d.acpNewSessionErr = errors.New("new session failed")

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)
	if id != "" {
		t.Fatalf("expected empty id on error, got %q", id)
	}
	// mitto-so19: no pill should be recorded when NewSession fails.
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no session change on NewSession error, got %v", d.recordedSessionChanges)
	}
}

// --- createFreshContextSession in-place flush tests (mitto-2tm) ---

func TestPromptDispatcher_CreateFreshContextSession_PrefersInPlaceFlush_WhenCmdConfigured(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextFlushCommand = "/clear"
	// hasACPConn=false intentionally: in-place path must work without it.
	d.hasACPConn = false
	d.acpNewSessionID = "should-not-be-used"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "" {
		t.Fatalf("expected empty id (in-place path), got %q", id)
	}
	if !d.flushContextCalled {
		t.Fatal("expected pdFlushContextInPlace to be called")
	}
	// NewSession must NOT have been called.
	// (acpNewSessionCalled would increment nextSeq; verify it wasn't via the fake)
	// We check by asserting flushContextCalled AND that acpNewSessionID is unused:
	// if NewSession had been called and succeeded the return would be non-empty.
	// mitto-so19: successful in-place flush must record a context_cleared pill.
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 context_cleared pill on flush success, got %d: %v", len(d.recordedSessionChanges), d.recordedSessionChanges)
	}
	sc := d.recordedSessionChanges[0]
	if sc.Kind != "context_cleared" || sc.Value != "flush" || sc.PreviousValue != "" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_FlushErrorDoesNotAbort(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextFlushCommand = "/clear"
	d.flushContextInPlaceErr = errors.New("flush failed")
	// hasACPConn defaults to false — the new-session fallback (mitto-2efc) is
	// unreachable here, so the observable behavior (return "") is unchanged
	// from before the fix. See TestPromptDispatcher_CreateFreshContextSession_FlushError_FallsBackToNewSession
	// for the case where the fallback IS available.

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	// Must still return "" (continue on existing session) even on flush error,
	// since no ACP connection is available to create a fresh session.
	if id != "" {
		t.Fatalf("expected empty id even on flush error, got %q", id)
	}
	if !d.flushContextCalled {
		t.Fatal("expected pdFlushContextInPlace to be called")
	}
	// mitto-so19: no pill should be recorded when the flush command failed.
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no session change on flush error, got %v", d.recordedSessionChanges)
	}
}

// TestPromptDispatcher_CreateFreshContextSession_FlushError_FallsBackToNewSession
// reproduces the fix for mitto-2efc defect 3: when the in-place context flush
// fails AND a direct ACP connection is available, createFreshContextSession
// must fall through to the new-ACP-session fallback instead of unconditionally
// returning "" and leaving the (possibly wedged) session in place.
func TestPromptDispatcher_CreateFreshContextSession_FlushError_FallsBackToNewSession(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextFlushCommand = "/clear"
	d.flushContextInPlaceErr = errors.New("flush failed")
	d.hasACPConn = true
	d.acpNewSessionID = "fresh-after-flush-fail"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "fresh-after-flush-fail" {
		t.Fatalf("expected new-session fallback id, got %q", id)
	}
	if !d.flushContextCalled {
		t.Fatal("expected pdFlushContextInPlace to be called")
	}
	if d.acpNewSessionCalls != 1 {
		t.Fatalf("acpNewSessionCalls = %d, want 1 (fallback must be invoked)", d.acpNewSessionCalls)
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 context_cleared pill on fallback success, got %d: %v", len(d.recordedSessionChanges), d.recordedSessionChanges)
	}
	if sc := d.recordedSessionChanges[0]; sc.Kind != "context_cleared" || sc.Value != "new_session" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

// TestPromptDispatcher_CreateFreshContextSession_FlushSuccess_NoNewSessionFallback
// asserts the inverse of the above: a successful in-place flush must NOT reach
// the new-session fallback, even when an ACP connection is available.
func TestPromptDispatcher_CreateFreshContextSession_FlushSuccess_NoNewSessionFallback(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextFlushCommand = "/clear"
	d.hasACPConn = true
	d.acpNewSessionID = "should-not-be-used"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "" {
		t.Fatalf("expected empty id (in-place path succeeded), got %q", id)
	}
	if !d.flushContextCalled {
		t.Fatal("expected pdFlushContextInPlace to be called")
	}
	if d.acpNewSessionCalls != 0 {
		t.Fatalf("acpNewSessionCalls = %d, want 0 (fallback must NOT be invoked on flush success)", d.acpNewSessionCalls)
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 context_cleared pill on flush success, got %d: %v", len(d.recordedSessionChanges), d.recordedSessionChanges)
	}
	if sc := d.recordedSessionChanges[0]; sc.Kind != "context_cleared" || sc.Value != "flush" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_FallsBackToNewSession_WhenNoCmd(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextFlushCommand = "" // no flush command → NewSession fallback
	d.hasACPConn = true
	d.acpNewSessionID = "new-sess-42"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "new-sess-42" {
		t.Fatalf("expected fallback NewSession id, got %q", id)
	}
	if d.flushContextCalled {
		t.Fatal("expected pdFlushContextInPlace NOT to be called when no flush command")
	}
	// mitto-so19: NewSession fallback success must record a context_cleared pill.
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 context_cleared pill on NewSession fallback, got %d: %v", len(d.recordedSessionChanges), d.recordedSessionChanges)
	}
	sc := d.recordedSessionChanges[0]
	if sc.Kind != "context_cleared" || sc.Value != "new_session" || sc.PreviousValue != "" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

// mitto-c36: when PromptWithMeta reserves a pillSeq upstream (before the
// user-prompt seq), createFreshContextSession must forward it to the seq-aware
// recorder so the "context_cleared" pill orders BEFORE the user prompt.
func TestPromptDispatcher_CreateFreshContextSession_UsesReservedPillSeq_Flush(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextFlushCommand = "/clear"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 42)

	if id != "" {
		t.Fatalf("expected empty id (in-place path), got %q", id)
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 pill, got %d", len(d.recordedSessionChanges))
	}
	if got := d.recordedSessionChangeSeqs[0]; got != 42 {
		t.Fatalf("expected pill seq=42 (reserved upstream), got %d", got)
	}
	if sc := d.recordedSessionChanges[0]; sc.Kind != "context_cleared" || sc.Value != "flush" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

func TestPromptDispatcher_CreateFreshContextSession_UsesReservedPillSeq_NewSession(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.hasACPConn = true
	d.acpNewSessionID = "fresh-99"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 99)

	if id != "fresh-99" {
		t.Fatalf("expected 'fresh-99', got %q", id)
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 pill, got %d", len(d.recordedSessionChanges))
	}
	if got := d.recordedSessionChangeSeqs[0]; got != 99 {
		t.Fatalf("expected pill seq=99 (reserved upstream), got %d", got)
	}
	if sc := d.recordedSessionChanges[0]; sc.Kind != "context_cleared" || sc.Value != "new_session" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

// mitto-c36: when pillSeq==0 (test callers not exercising seq ordering), the
// seq-less pdRecordSessionChange path is used and seq is captured as 0 in the fake.
func TestPromptDispatcher_CreateFreshContextSession_ZeroPillSeq_FallsBackToSeqless(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextFlushCommand = "/clear"

	_ = p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 pill, got %d", len(d.recordedSessionChanges))
	}
	if got := d.recordedSessionChangeSeqs[0]; got != 0 {
		t.Fatalf("expected pill seq=0 (seq-less fallback), got %d", got)
	}
}

// --- createFreshContextSession virginity-skip tests (mitto-s9g2) ---

// TestPromptDispatcher_CreateFreshContextSession_VirginSession_SkipsInPlaceFlush
// asserts that a provably-empty session (pdContextIsEmpty()==true) skips the
// in-place flush RPC entirely, records no pill, and returns "" (continue on
// the existing, already-empty session).
func TestPromptDispatcher_CreateFreshContextSession_VirginSession_SkipsInPlaceFlush(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextIsEmpty = true
	d.contextFlushCommand = "/clear" // would normally be preferred
	d.hasACPConn = true
	d.acpNewSessionID = "should-not-be-used"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "" {
		t.Fatalf("expected empty id (skip path), got %q", id)
	}
	if d.flushContextCalled {
		t.Fatal("expected pdFlushContextInPlace NOT to be called on a virgin session")
	}
	if d.acpNewSessionCalls != 0 {
		t.Fatalf("acpNewSessionCalls = %d, want 0 (new-session fallback must not be reached)", d.acpNewSessionCalls)
	}
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no context_cleared pill on skip, got %v", d.recordedSessionChanges)
	}
}

// TestPromptDispatcher_CreateFreshContextSession_VirginSession_SkipsNewSession
// exercises the skip on the no-flush-command / new-session-fallback path:
// a virgin session must never pay for a new ACP session either.
func TestPromptDispatcher_CreateFreshContextSession_VirginSession_SkipsNewSession(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextIsEmpty = true
	d.contextFlushCommand = "" // no flush command configured
	d.hasACPConn = true
	d.acpNewSessionID = "should-not-be-used"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "" {
		t.Fatalf("expected empty id (skip path), got %q", id)
	}
	if d.acpNewSessionCalls != 0 {
		t.Fatalf("acpNewSessionCalls = %d, want 0 (new-session fallback must not be reached)", d.acpNewSessionCalls)
	}
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no context_cleared pill on skip, got %v", d.recordedSessionChanges)
	}
}

// TestPromptDispatcher_CreateFreshContextSession_NonVirginSession_FlushesAsToday
// pins the tri-state intent: a session with >=1 dispatched turn (not empty)
// must flush exactly as before mitto-s9g2 — same observable behavior as the
// pre-existing PrefersInPlaceFlush test, but asserted explicitly against the
// new contextIsEmpty=false knob so a future regression flipping the default
// is caught here too.
func TestPromptDispatcher_CreateFreshContextSession_NonVirginSession_FlushesAsToday(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextIsEmpty = false // explicit: session has dispatched turns
	d.contextFlushCommand = "/clear"
	d.hasACPConn = false

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "" {
		t.Fatalf("expected empty id (in-place path), got %q", id)
	}
	if !d.flushContextCalled {
		t.Fatal("expected pdFlushContextInPlace to be called for a non-virgin session")
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 context_cleared pill on flush success, got %d: %v", len(d.recordedSessionChanges), d.recordedSessionChanges)
	}
	if sc := d.recordedSessionChanges[0]; sc.Kind != "context_cleared" || sc.Value != "flush" {
		t.Fatalf("unexpected pill: %+v", sc)
	}
}

// TestPromptDispatcher_CreateFreshContextSession_ResumedUnknownVirginity_FlushesAsToday
// pins the safety contract for resumed/loaded sessions: pdContextIsEmpty()
// reports false (fail safe) for the unknown-virginity state, so a resumed
// session's FreshContext iteration flushes exactly like a non-virgin one —
// asserted separately from the non-virgin case to document that both "known
// not-empty" and "unknown virginity" collapse to the same observable
// behavior via the same false return.
func TestPromptDispatcher_CreateFreshContextSession_ResumedUnknownVirginity_FlushesAsToday(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.contextIsEmpty = false // resumed/loaded sessions report false (unknown != empty)
	d.hasACPConn = true
	d.contextFlushCommand = "" // exercise the new-session fallback leg too
	d.acpNewSessionID = "resumed-session-fresh"

	id := p.createFreshContextSession(d, PromptMeta{FreshContext: true}, 0)

	if id != "resumed-session-fresh" {
		t.Fatalf("expected new-session fallback id, got %q", id)
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 context_cleared pill, got %d: %v", len(d.recordedSessionChanges), d.recordedSessionChanges)
	}
	if sc := d.recordedSessionChanges[0]; sc.Kind != "context_cleared" || sc.Value != "new_session" {
		t.Fatalf("unexpected pill: %+v", sc)
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

// TestPromptDispatcher_ApplyModelPreference_PreferenceButNoAgentModels_ObservableFailure
// reproduces mitto-ishl. When a prompt DECLARES preferredModels but agentModels
// is nil (e.g. every Auggie session today, which never advertises a model
// catalog via ACP ConfigOptions), the failure must be observable in the log
// stream at WARN level with a distinct decision code so tier-splitting
// failures are never silent.
//
// Pre-fix behavior (buggy): the check at prompt_dispatcher.go:854 short-circuits
// at DEBUG level with decision=skip_no_agent_models, regardless of whether a
// preference was declared. There is no way to tell from the log whether we
// silently dropped a real tier switch or whether the prompt genuinely had no
// preference; both look identical.
//
// Post-fix contract: when preferredModels is non-empty AND agentModels is nil,
// applyModelPreference must emit a WARN-level log with a distinct decision code
// (skip_agent_advertises_no_models) that includes the declared preference so an
// operator can grep for silently-dropped tier switches.
func TestPromptDispatcher_ApplyModelPreference_PreferenceButNoAgentModels_ObservableFailure(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = nil // simulate an Auggie-shaped agent that never advertises models
	var buf bytes.Buffer
	// Capture WARN+ only so a DEBUG-level short-circuit fails the assertion:
	// the whole point of the fix is that this specific combination is loud,
	// not hidden in a firehose of debug lines that ship disabled in production.
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	p.applyModelPreference(d, PromptMeta{
		PromptName:      "Bug fix investigate phase",
		PreferredModels: []config.PromptPreferredModel{{ModelTag: "Reasoning"}},
	})

	if len(d.setActiveModelCalls) != 0 {
		t.Fatalf("expected no setActiveModel call when agentModels=nil, got %v", d.setActiveModelCalls)
	}
	if d.overrideActive {
		t.Fatal("expected overrideActive=false when the switch cannot be applied")
	}
	logOut := buf.String()
	if !strings.Contains(logOut, "level=WARN") {
		t.Fatalf("expected a WARN-level log when preferredModels is declared but agentModels is nil, got: %q", logOut)
	}
	if !strings.Contains(logOut, "decision=skip_agent_advertises_no_models") {
		t.Fatalf("expected decision=skip_agent_advertises_no_models in log to distinguish this failure from a no-preference no-op, got: %q", logOut)
	}
	if !strings.Contains(logOut, "Reasoning") {
		t.Fatalf("expected the declared preference (Reasoning) to appear in the log so operators can grep for dropped tier switches, got: %q", logOut)
	}
	if !strings.Contains(logOut, "Bug fix investigate phase") {
		t.Fatalf("expected the prompt_name to appear in the log for auditability, got: %q", logOut)
	}
}

// TestPromptDispatcher_ApplyModelPreference_NoAgentModels_SynthesizesPillFromProfiles
// verifies the mitto-ishl fix: when agentModels is nil (e.g. Auggie sessions,
// which never advertise a model catalog via ACP ConfigOptions) but the prompt
// declares a preferredModels tag AND the configured model profiles carry a
// matching entry, applyModelPreference records a model_override session_change
// pill so the ⚡ timeline pill fires without requiring a set_model RPC.
func TestPromptDispatcher_ApplyModelPreference_NoAgentModels_SynthesizesPillFromProfiles(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = nil  // Auggie-shaped agent: no advertised catalog
	d.baselineModel = "" // no advertised current model either
	// A single profile carrying the "Reasoning" tag that matches its own display name.
	d.modelProfiles = []config.ModelProfile{
		{
			Name:     "Claude Opus",
			Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"},
			Tags:     []string{"Reasoning"},
		},
	}
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	p.applyModelPreference(d, PromptMeta{
		PromptName:      "Bug fix investigate phase",
		PreferredModels: []config.PromptPreferredModel{{ModelTag: "Reasoning"}},
	})

	if len(d.setActiveModelCalls) != 0 {
		t.Fatalf("expected no setActiveModel call when agentModels=nil (agent has no models to switch), got %v", d.setActiveModelCalls)
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 model_override pill from synthesized profile, got %d (log=%q)", len(d.recordedSessionChanges), buf.String())
	}
	sc := d.recordedSessionChanges[0]
	if sc.Kind != ConfigOptionCategoryModelOverride {
		t.Fatalf("expected kind=%q, got %q", ConfigOptionCategoryModelOverride, sc.Kind)
	}
	if sc.Value != "Claude Opus" {
		t.Fatalf("expected pill value=%q (profile Name), got %q", "Claude Opus", sc.Value)
	}
	if !d.overrideActive {
		t.Fatal("expected overrideActive=true after synthesizing the pill from profiles")
	}
	if !strings.Contains(buf.String(), "decision=synth_profile_pill") {
		t.Fatalf("expected decision=synth_profile_pill in log, got: %q", buf.String())
	}
}

// TestPromptDispatcher_ApplyModelPreference_SyntheticCatalog_NeverReverifiesModel
// reproduces mitto-fvt: after the shared ACP process restarts and the agent
// advertises an empty model catalog (availableModels: []), DeriveAgentModels
// falls back to SynthesizeModelStateFromProfiles (model_state.go), whose
// AvailableModels entries use ModelId == profile.Name — Mitto-internal display
// labels (e.g. "Opus 5") that were never validated against the live agent or
// backend. Per the mitto-fvt investigation, a session/set_model RPC issued
// with one of these synthetic ids can appear to succeed locally (the agent
// just stores the opaque string), so bs.agentModels.CurrentModelId and the
// persisted baselineModel both end up recording the fake id — indistinguishable
// from a genuinely agent-confirmed model. Every subsequent chat-stream request
// naming that id then 404s ("The selected model is not available for this
// session"), yet applyModelPreference sees current==baseline and short-circuits
// at decision=skip_no_preference forever, so the wedge never self-heals.
//
// Pre-fix (buggy) behavior, asserted here: zero pdSetActiveModelOnly calls —
// applyModelPreference trusts the synthetic current==baseline agreement as if
// it were a real negotiated state and never attempts to re-establish a valid
// model.
//
// Post-fix contract (mitto-fvt): when every entry in the active catalog carries
// the SynthesizeModelStateFromProfiles signature (ModelId == Name), the catalog
// must not be treated as agent-confirmed truth merely because current==baseline;
// applyModelPreference must attempt to (re)establish a real model via
// pdSetActiveModelOnly so a poisoned session can recover instead of looping
// 404s indefinitely.
func TestPromptDispatcher_ApplyModelPreference_SyntheticCatalog_NeverReverifiesModel(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()

	profiles := []config.ModelProfile{
		{Name: "Opus 5", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}},
	}
	// Build the catalog exactly as DeriveAgentModels' branch 3 would after an
	// ACP restart wipes the agent's real catalog (mitto-fvt evidence:
	// availableModels: [] from 10:11:45 onward, config_option_count=0).
	synth := SynthesizeModelStateFromProfiles(profiles)
	if synth == nil {
		t.Fatal("expected SynthesizeModelStateFromProfiles to produce a non-nil catalog")
	}
	// Simulate the post-wedge state observed in the bead: CurrentModelId has
	// been recorded as the synthetic profile-name id, and the persisted
	// baseline carried the same value across the restart (metadata.json
	// baseline_model: "Opus 5").
	synth.CurrentModelId = "Opus 5"
	d.agentModels = synth
	d.baselineModel = "Opus 5"
	d.modelProfiles = profiles

	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Ordinary scheduled loop prompt: no per-prompt preferredModels declared,
	// mirroring the on-call loop conversations' periodic re-runs.
	p.applyModelPreference(d, PromptMeta{})

	if len(d.setActiveModelCalls) == 0 {
		t.Fatalf(
			"mitto-fvt reproduction: applyModelPreference silently trusted a "+
				"SYNTHETIC catalog's current==baseline agreement and never "+
				"attempted to re-establish a real model — this is the infinite "+
				"404 wedge (\"selected model is not available for this session\"). "+
				"log: %s", buf.String())
	}
}

func TestPromptDispatcher_ApplyModelPreference_NoPreference_DesiredIsBaseline_NoSwitch(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &SessionModelState{CurrentModelId: "m-1"}
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
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no model_override pill with no preference, got %v", d.recordedSessionChanges)
	}
}

func TestPromptDispatcher_ApplyModelPreference_MatchingPreference_SetsModelAndOverride(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &SessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []ModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	d.baselineModel = "m-1"
	d.modelProfiles = []config.ModelProfile{
		{Name: "Pref2", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Model 2"}},
	}
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Prefer "m-2" via the "Pref2" profile (matched by name "Model 2" with "contains" mode)
	p.applyModelPreference(d, PromptMeta{PreferredModels: []config.PromptPreferredModel{{ModelName: "Pref2"}}})

	if len(d.setActiveModelCalls) != 1 || d.setActiveModelCalls[0] != "m-2" {
		t.Fatalf("expected setActiveModelOnly('m-2'), got %v", d.setActiveModelCalls)
	}
	if !d.overrideActive {
		t.Fatal("expected overrideActive=true when preferred differs from baseline")
	}
	if !strings.Contains(buf.String(), "decision=switching") {
		t.Fatalf("expected decision=switching in log, got: %s", buf.String())
	}
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 model_override pill, got %d", len(d.recordedSessionChanges))
	}
	if sc := d.recordedSessionChanges[0]; sc.Kind != ConfigOptionCategoryModelOverride ||
		sc.Value != "Model 2" || sc.PreviousValue != "Model 1" {
		t.Fatalf("unexpected model_override pill: %+v", sc)
	}
}

func TestPromptDispatcher_ApplyModelPreference_PreferenceAlreadyActive_NoSwitch(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &SessionModelState{
		CurrentModelId: "m-2",
		AvailableModels: []ModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	d.baselineModel = "m-1"
	d.modelProfiles = []config.ModelProfile{
		{Name: "Pref2", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Model 2"}},
	}
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Prefer "m-2" via the "Pref2" profile, which is already active.
	p.applyModelPreference(d, PromptMeta{PreferredModels: []config.PromptPreferredModel{{ModelName: "Pref2"}}})

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
	// Pill is still emitted: the prompt runs on a non-baseline model even though
	// no RPC switch was needed.
	if len(d.recordedSessionChanges) != 1 {
		t.Fatalf("expected 1 model_override pill when override active, got %d", len(d.recordedSessionChanges))
	}
	if sc := d.recordedSessionChanges[0]; sc.Value != "Model 2" || sc.PreviousValue != "Model 1" {
		t.Fatalf("unexpected model_override pill: %+v", sc)
	}
}

func TestPromptDispatcher_ApplyModelPreference_NoMatch_UsesBaseline_ClearsOverride(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &SessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []ModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
		},
	}
	d.baselineModel = "m-1"
	d.modelProfiles = []config.ModelProfile{
		{Name: "Missing", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "nonexistent-model"}},
	}
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Preference profile's criteria doesn't match anything → desired stays at baseline.
	p.applyModelPreference(d, PromptMeta{PreferredModels: []config.PromptPreferredModel{{ModelName: "Missing"}}})

	if len(d.setActiveModelCalls) != 0 {
		t.Fatalf("expected no model switch on no-match, got %v", d.setActiveModelCalls)
	}
	if d.overrideActive {
		t.Fatal("expected overrideActive=false when no match and desired==baseline")
	}
	if !strings.Contains(buf.String(), "decision=skip_no_match") {
		t.Fatalf("expected decision=skip_no_match in log, got: %s", buf.String())
	}
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no model_override pill when not overriding, got %v", d.recordedSessionChanges)
	}
}

func TestPromptDispatcher_ApplyModelPreference_SwitchFails_NoPill(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &SessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []ModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	d.baselineModel = "m-1"
	d.setActiveModelErr = errors.New("boom")
	d.modelProfiles = []config.ModelProfile{
		{Name: "Pref2", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Model 2"}},
	}
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	p.applyModelPreference(d, PromptMeta{PreferredModels: []config.PromptPreferredModel{{ModelName: "Pref2"}}})

	if len(d.setActiveModelCalls) != 1 {
		t.Fatalf("expected setActiveModelOnly to be attempted, got %v", d.setActiveModelCalls)
	}
	if len(d.recordedSessionChanges) != 0 {
		t.Fatalf("expected no model_override pill when switch RPC failed, got %v", d.recordedSessionChanges)
	}
}

func TestPromptDispatcher_ApplyModelPreference_ColdSlowSwitch_DoesNotBlockPrompt(t *testing.T) {
	// Shrink the synchronous grace so the test is fast.
	origGrace := modelSwitchSyncGrace
	modelSwitchSyncGrace = 30 * time.Millisecond
	defer func() { modelSwitchSyncGrace = origGrace }()

	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &SessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []ModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	d.baselineModel = "m-1"
	d.modelProfiles = []config.ModelProfile{
		{Name: "Pref2", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Model 2"}},
	}
	gate := make(chan struct{})
	d.setActiveModelGate = gate
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	start := time.Now()
	p.applyModelPreference(d, PromptMeta{PreferredModels: []config.PromptPreferredModel{{ModelName: "Pref2"}}})
	elapsed := time.Since(start)

	// The interactive prompt must NOT block on the slow set_model.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("applyModelPreference blocked on slow set_model (%s); expected to return near the grace window", elapsed)
	}
	if !strings.Contains(buf.String(), "Deferring model switch to background") {
		t.Fatalf("expected deferral log, got: %s", buf.String())
	}

	// The background switch was attempted (poll to avoid scheduling flakiness).
	deadline := time.Now().Add(2 * time.Second)
	for {
		d.mu.Lock()
		calls := len(d.setActiveModelCalls)
		d.mu.Unlock()
		if calls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected the background switch to be attempted once")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The override pill/flag are NOT applied yet (switch still in flight -> next turn).
	d.mu.Lock()
	pills := len(d.recordedSessionChanges)
	d.mu.Unlock()
	if pills != 0 {
		t.Fatalf("expected no override pill until the deferred switch lands, got %d", pills)
	}

	// Release the switch; it should now complete and apply the override.
	close(gate)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		pills = len(d.recordedSessionChanges)
		override := d.overrideActive
		d.mu.Unlock()
		if pills == 1 && override {
			return // success: switch landed, override applied for the next turn
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("deferred model switch did not apply the override after the switch completed")
}

// TestPromptDispatcher_TierCheck_RendersOptimisticWhenSwitchDeferred pins the
// fix for mitto-y78i: resolveAndSubstitute now attempts a templated dispatch's
// model-switch preference (via applyModelPreference) BEFORE rendering, and
// marks meta.modelPreferenceResolved so buildProcessorInput trusts the actual
// post-attempt models.CurrentModelId instead of intendedModelID's optimistic
// guess. When the set_model RPC is slower than modelSwitchSyncGrace,
// applyModelPreference defers it to the background and the turn genuinely
// dispatches on the OLD model (logged as "Deferring model switch to
// background") — so the render must reflect that model, not the one the
// dispatch merely intended to reach.
//
// This test drives the fixed sequence resolveAndSubstitute now performs:
// applyModelPreference (switch attempt) happens-before buildProcessorInput
// (render), with meta.modelPreferenceResolved set exactly as
// resolveAndSubstitute sets it in between the two calls. Before the fix,
// buildProcessorInput ran first and unconditionally trusted intendedModelID,
// so a tier-check fragment consuming these fields reported "tier confirmed"
// for a turn that actually ran degraded — a false negative in the audit
// trail.
func TestPromptDispatcher_TierCheck_RendersOptimisticWhenSwitchDeferred(t *testing.T) {
	// Shrink the synchronous grace so the test is fast and deterministic.
	origGrace := modelSwitchSyncGrace
	modelSwitchSyncGrace = 30 * time.Millisecond
	defer func() { modelSwitchSyncGrace = origGrace }()

	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.agentModels = &SessionModelState{
		CurrentModelId: "m-opus",
		AvailableModels: []ModelInfo{
			{ModelId: "m-opus", Name: "Claude Opus"},
			{ModelId: "m-sonnet", Name: "Claude Sonnet"},
		},
	}
	d.baselineModel = "m-opus"
	d.modelProfiles = []config.ModelProfile{
		{Name: "Coding", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Sonnet"}, Tags: []string{"Coding"}},
		{Name: "Reasoning", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}, Tags: []string{"Reasoning"}},
	}
	d.modelTagsByName = map[string][]string{
		"Claude Opus":   {"Reasoning"},
		"Claude Sonnet": {"Coding"},
	}
	// The set_model RPC never returns within the test's lifetime — this is the
	// "cold/slow agent" precondition that pushes applyModelPreference past
	// modelSwitchSyncGrace. sessionCtx must be non-nil so the background
	// goroutine's WithTimeout(d.pdSessionCtx(), modelSwitchAsyncBudget) doesn't
	// panic on a nil parent context.
	d.setActiveModelGate = make(chan struct{}) // never closed in this test
	d.sessionCtx = context.Background()

	meta := PromptMeta{PreferredModels: []config.PromptPreferredModel{{ModelTag: "Coding"}}}

	// Step 1: the switch path (applyModelPreference), now hoisted by
	// resolveAndSubstitute to run BEFORE the render (bgsession_prompt.go:355's
	// resolveAndSubstitute calls it ahead of buildProcessorInput). Because the
	// RPC never lands within modelSwitchSyncGrace, it defers to the
	// background, leaving the turn on the OLD model.
	var buf bytes.Buffer
	d.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p.applyModelPreference(d, meta)

	if !strings.Contains(buf.String(), "Deferring model switch to background") {
		t.Fatalf("expected applyModelPreference to defer the switch (precondition of the bug), got log: %s", buf.String())
	}
	// The model actually active for this turn never changed.
	if d.agentModels.CurrentModelId != "m-opus" {
		t.Fatalf("expected CurrentModelId to remain m-opus (switch deferred, not landed), got %q", d.agentModels.CurrentModelId)
	}

	// Step 2: the render path (buildProcessorInput), with
	// meta.modelPreferenceResolved set exactly as resolveAndSubstitute sets it
	// after attempting the switch above.
	meta.modelPreferenceResolved = true
	input := p.buildProcessorInput(d, "msg", false, meta)

	// FIXED (mitto-y78i): the render must reflect the ACTUAL landed model when
	// the switch is deferred — Opus/Reasoning, matching what the turn actually
	// dispatched on — not the intended Sonnet/Coding tier.
	if input.ModelName == "Claude Sonnet" || (len(input.ModelTags) == 1 && input.ModelTags[0] == "Coding") {
		t.Fatalf("tier-check renders optimistically when the model switch is deferred to the background: "+
			"got ModelName=%q ModelTags=%v (claims the intended Coding tier), "+
			"want the ACTUAL landed model (Claude Opus / Reasoning) since applyModelPreference deferred the switch",
			input.ModelName, input.ModelTags)
	}
	if input.ModelName != "Claude Opus" {
		t.Errorf("ModelName = %q, want %q (the actual model this turn ran on)", input.ModelName, "Claude Opus")
	}
	if len(input.ModelTags) != 1 || input.ModelTags[0] != "Reasoning" {
		t.Errorf("ModelTags = %v, want [Reasoning] (the actual model's tags)", input.ModelTags)
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
	// mitto-vn3: sessionIdle now also requires a completed turn (endTurn)
	// AND a non-empty agent_message on the session.
	d.lastAgentMessage = "agent said something"
	resp := acp.PromptResponse{StopReason: acp.StopReasonEndTurn}

	sessionIdle := p.handlePromptSuccess(d, 3, 2, resp, "msg", PromptMeta{}, time.Now(), time.Now())

	if !sessionIdle {
		t.Fatal("expected sessionIdle=true when no queued message dispatched and turn completed with an agent_message")
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
	// Even a fully completed turn must NOT be sessionIdle when a queued
	// message was dispatched (session is not actually idle).
	d.lastAgentMessage = "agent said something"
	resp := acp.PromptResponse{StopReason: acp.StopReasonEndTurn}

	sessionIdle := p.handlePromptSuccess(d, 1, 1, resp, "msg", PromptMeta{}, time.Now(), time.Now())

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

// TestPromptDispatcher_HandlePromptError_WatchdogFired_PersistsErrorEvent
// reproduces mitto-vxn: when the inactivity watchdog cancels a hung prompt,
// handlePromptError must persist a session.EventTypeError event via
// pdRecordErrorEvent (the same recorder-then-notify idiom already used by
// completeHandshakeOrAbort), not just notify live observers. Without this,
// the cancelled turn leaves no trace in events.jsonl and is invisible after
// a page reload or when no WebSocket client is attached.
func TestPromptDispatcher_HandlePromptError_WatchdogFired_PersistsErrorEvent(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = false // irrelevant when watchdog fires
	d.hasRecorder = true

	autoRetried := false
	retry := p.handlePromptError(d, transientErr(), &autoRetried, 1, true /* watchdogFired */)

	if retry {
		t.Fatal("expected retry=false for watchdog-fired path")
	}
	if len(d.recordedErrorEvents) != 1 {
		t.Fatalf("expected watchdog-fired error to be persisted via pdRecordErrorEvent, got %d recorded events: %v",
			len(d.recordedErrorEvents), d.recordedErrorEvents)
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

// mitto-r5o: Authentication-required errors from the upstream CLI (e.g. Claude
// Code's -32000 "Authentication required" when the OAuth token expires) must
// NOT advance the queue — every queued message will hit the same failure until
// the user re-authenticates, so cascading them just spams identical errors.
func TestPromptDispatcher_HandlePromptError_AuthError_QueueNotAdvanced(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.acpDead = false

	authErr := &fakeAuthError{}
	autoRetried := false
	p.handlePromptError(d, authErr, &autoRetried, 0, false)

	if d.processNextCalled != 0 {
		t.Fatalf("expected no queue advance for auth error, got %d", d.processNextCalled)
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

// fakeContextTooLargeError mimics the shape IsContextTooLargeError checks.
type fakeContextTooLargeError struct{}

func (e *fakeContextTooLargeError) Error() string { return "context_length_exceeded: 413" }

// fakeAuthError mimics the shape IsAuthError checks (mitto-r5o). The predicate
// matches "authentication required" case-insensitively — same JSON-RPC -32000
// payload Claude Code emits when the Anthropic OAuth token expires.
type fakeAuthError struct{}

func (e *fakeAuthError) Error() string {
	return `{"code":-32000,"message":"Authentication required"}`
}

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
	d.resolver = func(name, _ string) (string, error) { return "Hi {{ .Args.NAME }}", nil }
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
// past its TTL is NOT auto-filled. With Go templates the Arg helper in the body
// still renders the declared default when no arg is filled.
func TestResolveAndSubstitute_Cache_ExpiredNotAutoFilled(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.resolver = func(name, _ string) (string, error) {
		return `Hi {{ Arg "NAME" "stranger" }}`, nil
	}
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

	// Second call with no args: cache expired → arg not filled → argCount=0.
	// The Arg helper renders the declared default "stranger".
	msg, argCount, _, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "Hi stranger" {
		t.Fatalf("expected Arg helper default rendered, got %q", msg)
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
	d.resolver = func(name, _ string) (string, error) { return "Hi {{ .Args.NAME }}", nil }
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
	d.resolver = func(name, _ string) (string, error) { return "Hi {{ .Args.NAME }}", nil }
	d.promptParams = nil // resolver returns nil — simulates unknown/unparameterised prompt

	msg, argCount, _, err := p.resolveAndSubstitute(d, "",
		PromptMeta{PromptName: "greet", Arguments: map[string]string{"NAME": "Alice"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "Hi Alice" {
		t.Fatalf("expected rendered message, got %q", msg)
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
	d.resolver = func(name, _ string) (string, error) { return "Hi {{ .Args.NAME }}", nil }
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

// --- Reproduction for mitto-vn3 ---
// onCompletion loop trigger fires while the agent is still processing the
// initial prompt (runaway). Root cause per investigation on mitto-vn3:
// handlePromptSuccess/finalizeTurn advance sessionIdle → pdOnTurnIdle purely
// from queue-drain state, without gating on stopReason or on whether the agent
// actually emitted any agent_message on this turn. A session/prompt that
// returns after tool-call-only activity therefore fires pdOnTurnIdle →
// LoopRunner.OnConversationIdle → armCompletionTimer, and the onCompletion
// loop re-fires while the agent has produced zero assistant text.
//
// The following two tests express the expected (post-fix) behaviour and
// currently FAIL against the buggy code — they are the reproduction pinned to
// the bead.

// TestPromptDispatcher_FinalizeTurn_ToolOnlyTurn_DoesNotFireTurnIdle_MittoVN3
// asserts that when the turn ended without any agent_message (a tool-call-only
// turn: lastAgentMessage == "", stopReason != endTurn), the on-turn-idle hook
// MUST NOT be invoked so the onCompletion loop cannot re-arm mid-turn.
//
// Bug reference: mitto-vn3.
func TestPromptDispatcher_FinalizeTurn_ToolOnlyTurn_DoesNotFireTurnIdle_MittoVN3(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	// Tool-call-only turn: agent emitted no assistant text this turn.
	d.lastAgentMessage = ""
	// No queued message dispatched (drives sessionIdle=true today).
	d.processNextResult = false

	// Simulate a session/prompt that returned without a proper endTurn (matches
	// the mitto-vn3 timeline where the agent emitted only tool calls before the
	// RPC returned).
	resp := acp.PromptResponse{StopReason: acp.StopReasonMaxTurnRequests}
	sessionIdle := p.handlePromptSuccess(d, 2 /*tool_call events*/, 1, resp,
		"user prompt", PromptMeta{}, time.Now(), time.Now())
	p.finalizeTurn(d, nil, PromptMeta{}, sessionIdle)

	if d.turnIdleCalls != 0 {
		t.Fatalf("mitto-vn3: pdOnTurnIdle must NOT fire on a tool-only turn "+
			"(stopReason=%v, no agent_message), got %d calls",
			resp.StopReason, d.turnIdleCalls)
	}
}

// TestPromptDispatcher_FinalizeTurn_EndTurnWithoutAgentMessage_DoesNotFireTurnIdle_MittoVN3
// covers the degenerate endTurn case: the ACP session returned stopReason=endTurn
// but produced zero agent_message events on this turn. The onCompletion loop must
// not re-arm since the agent said nothing — otherwise a runaway is possible where
// every re-fire itself ends the same way (see mitto-vn3 timeline seq 4/5 → seq 9).
//
// Bug reference: mitto-vn3.
func TestPromptDispatcher_FinalizeTurn_EndTurnWithoutAgentMessage_DoesNotFireTurnIdle_MittoVN3(t *testing.T) {
	p := promptDispatcher{}
	d := newFakePromptDeps()
	d.lastAgentMessage = "" // zero assistant text produced this turn
	d.processNextResult = false

	resp := acp.PromptResponse{StopReason: acp.StopReasonEndTurn}
	sessionIdle := p.handlePromptSuccess(d, 2, 1, resp,
		"user prompt", PromptMeta{}, time.Now(), time.Now())
	p.finalizeTurn(d, nil, PromptMeta{}, sessionIdle)

	if d.turnIdleCalls != 0 {
		t.Fatalf("mitto-vn3: pdOnTurnIdle must NOT fire on endTurn with zero "+
			"agent_message events, got %d calls", d.turnIdleCalls)
	}
}
