package conversation

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

// compile-time check.
var _ followUpDeps = (*fakeFollowUpDeps)(nil)

type fakeFollowUpDeps struct {
	mu sync.Mutex

	// state knobs
	sessionID                      string
	logger                         *slog.Logger
	closed                         bool
	prompting                      bool
	workspaceUUID                  string
	workingDir                     string
	sessionDir                     string
	storeAvailable                 bool
	workspaceProcessorArgOverrides map[string]map[string]string
	casResult                      bool // what fuCASFollowUpInProgress returns
	loadResult                     bool // what fuLoadFollowUpInProgress returns
	auxAvailable                   bool
	abEnabled                      bool
	eventCount                     int

	// in-memory button cache
	cacheMu       sync.RWMutex
	cachedButtons []ActionButton

	// injected returns
	analyzeResult     []ActionButton
	analyzeErr        error
	readEventsResult  []session.Event
	readEventsErr     error
	getUserDataResult *session.UserData
	getUserDataErr    error
	setUserDataErr    error
	applyAfterResult  processors.ApplyAfterResult

	// recorders
	storedFalse      int
	notifiedEvents   []string
	uiNotifyReqs     []UINotifyRequest
	setUserDataCalls []*session.UserData
}

func newFakeFollowUpDeps() *fakeFollowUpDeps {
	return &fakeFollowUpDeps{
		sessionID:      "test-session",
		logger:         slog.Default(),
		storeAvailable: true,
		abEnabled:      true,
		casResult:      true, // by default CAS succeeds
		analyzeResult:  []ActionButton{{Label: "Q?", Response: "A"}},
	}
}

// --- followUpDeps implementation ---

func (f *fakeFollowUpDeps) fuSessionID() string     { return f.sessionID }
func (f *fakeFollowUpDeps) fuLogger() *slog.Logger  { return f.logger }
func (f *fakeFollowUpDeps) fuIsClosed() bool        { return f.closed }
func (f *fakeFollowUpDeps) fuIsPrompting() bool     { return f.prompting }
func (f *fakeFollowUpDeps) fuWorkspaceUUID() string { return f.workspaceUUID }
func (f *fakeFollowUpDeps) fuWorkingDir() string    { return f.workingDir }
func (f *fakeFollowUpDeps) fuSessionDir() string    { return f.sessionDir }

func (f *fakeFollowUpDeps) fuCASFollowUpInProgress() bool  { return f.casResult }
func (f *fakeFollowUpDeps) fuLoadFollowUpInProgress() bool { return f.loadResult }
func (f *fakeFollowUpDeps) fuStoreFollowUpInProgressFalse() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storedFalse++
}

func (f *fakeFollowUpDeps) fuRLockActionButtons()   { f.cacheMu.RLock() }
func (f *fakeFollowUpDeps) fuRUnlockActionButtons() { f.cacheMu.RUnlock() }
func (f *fakeFollowUpDeps) fuLockActionButtons()    { f.cacheMu.Lock() }
func (f *fakeFollowUpDeps) fuUnlockActionButtons()  { f.cacheMu.Unlock() }

func (f *fakeFollowUpDeps) fuGetCachedActionButtons() []ActionButton  { return f.cachedButtons }
func (f *fakeFollowUpDeps) fuSetCachedActionButtons(b []ActionButton) { f.cachedButtons = b }

func (f *fakeFollowUpDeps) fuGetActionButtonsStore() *session.ActionButtonsStore { return nil }
func (f *fakeFollowUpDeps) fuGetEventCount() int                                 { return f.eventCount }

func (f *fakeFollowUpDeps) fuHasAuxiliaryManager() bool { return f.auxAvailable }
func (f *fakeFollowUpDeps) fuAnalyzeFollowUpQuestions(_ context.Context, _, _, _ string) ([]ActionButton, error) {
	return f.analyzeResult, f.analyzeErr
}

func (f *fakeFollowUpDeps) fuApplyAfterProcessors(_ context.Context, _ processors.AfterProcessorInput) processors.ApplyAfterResult {
	return f.applyAfterResult
}
func (f *fakeFollowUpDeps) fuWorkspaceProcessorArgOverrides() map[string]map[string]string {
	return f.workspaceProcessorArgOverrides
}

func (f *fakeFollowUpDeps) fuIsStoreAvailable() bool { return f.storeAvailable }
func (f *fakeFollowUpDeps) fuReadEvents() ([]session.Event, error) {
	return f.readEventsResult, f.readEventsErr
}
func (f *fakeFollowUpDeps) fuGetUserData() (*session.UserData, error) {
	if f.getUserDataResult == nil && f.getUserDataErr == nil {
		return &session.UserData{}, nil
	}
	return f.getUserDataResult, f.getUserDataErr
}
func (f *fakeFollowUpDeps) fuSetUserData(data *session.UserData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setUserDataCalls = append(f.setUserDataCalls, data)
	return f.setUserDataErr
}

func (f *fakeFollowUpDeps) fuActionButtonsEnabled() bool { return f.abEnabled }

func (f *fakeFollowUpDeps) fuNotifyObservers(fn func(SessionObserver)) {
	fn(&followUpRecorderObserver{deps: f})
}
func (f *fakeFollowUpDeps) fuUINotify(req UINotifyRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uiNotifyReqs = append(f.uiNotifyReqs, req)
	return nil
}

// followUpRecorderObserver records observer calls as stable strings.
type followUpRecorderObserver struct{ deps *fakeFollowUpDeps }

func (r *followUpRecorderObserver) record(s string) {
	r.deps.mu.Lock()
	r.deps.notifiedEvents = append(r.deps.notifiedEvents, s)
	r.deps.mu.Unlock()
}
func (r *followUpRecorderObserver) OnActionButtons(b []ActionButton)              { r.record("action_buttons") }
func (r *followUpRecorderObserver) OnAgentMessage(int64, string)                  {}
func (r *followUpRecorderObserver) OnAgentThought(int64, string)                  {}
func (r *followUpRecorderObserver) OnToolCall(int64, string, string, string)      {}
func (r *followUpRecorderObserver) OnToolUpdate(int64, string, *string)           {}
func (r *followUpRecorderObserver) OnPlan(int64, []PlanEntry)                     {}
func (r *followUpRecorderObserver) OnFileWrite(int64, string, int)                {}
func (r *followUpRecorderObserver) OnFileRead(int64, string, int)                 {}
func (r *followUpRecorderObserver) OnContextUsageUpdate(int, int)                 {}
func (r *followUpRecorderObserver) OnAvailableCommandsUpdated([]AvailableCommand) {}
func (r *followUpRecorderObserver) OnQueueMessageSending(string)                  {}
func (r *followUpRecorderObserver) OnQueueMessageSent(string)                     {}
func (r *followUpRecorderObserver) OnQueueUpdated(int, string, string)            {}
func (r *followUpRecorderObserver) OnQueueReordered([]session.QueuedMessage)      {}
func (r *followUpRecorderObserver) OnError(string)                                {}
func (r *followUpRecorderObserver) OnPromptComplete(int)                          {}
func (r *followUpRecorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int) {
}
func (r *followUpRecorderObserver) OnACPStopped(string)              {}
func (r *followUpRecorderObserver) OnACPStarted()                    {}
func (r *followUpRecorderObserver) OnUIPrompt(UIPromptRequest)       {}
func (r *followUpRecorderObserver) OnUIPromptDismiss(string, string) {}
func (r *followUpRecorderObserver) OnNotification(UINotifyRequest)   {}

// --- Tests ---

func TestFollowUpCoordinator_GetActionButtons_MemoryHit(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.cachedButtons = []ActionButton{{Label: "A", Response: "B"}}

	got := c.getActionButtons(d)
	if len(got) != 1 || got[0].Label != "A" {
		t.Fatalf("expected cached button, got %v", got)
	}
}

func TestFollowUpCoordinator_GetActionButtons_NilStoreReturnsNil(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.storeAvailable = false // store not available

	got := c.getActionButtons(d)
	if got != nil {
		t.Fatalf("expected nil when no store, got %v", got)
	}
}

func TestFollowUpCoordinator_ClearActionButtons_WithButtons_Notifies(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.cachedButtons = []ActionButton{{Label: "X"}}

	c.clearActionButtons(d)

	if d.cachedButtons != nil {
		t.Fatal("expected cache cleared")
	}
	if len(d.notifiedEvents) != 1 || d.notifiedEvents[0] != "action_buttons" {
		t.Fatalf("expected action_buttons notification, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_ClearActionButtons_Empty_NoNotify(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	// no cached buttons

	c.clearActionButtons(d)

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notification when nothing to clear, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_SendCachedActionButtonsTo_WithButtons(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.cachedButtons = []ActionButton{{Label: "Z"}}

	obs := &followUpRecorderObserver{deps: d}
	c.sendCachedActionButtonsTo(d, obs)

	if len(d.notifiedEvents) != 1 || d.notifiedEvents[0] != "action_buttons" {
		t.Fatalf("expected action_buttons sent, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_SendCachedActionButtonsTo_Empty_NoOp(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	// no cached buttons

	obs := &followUpRecorderObserver{deps: d}
	c.sendCachedActionButtonsTo(d, obs)

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no call when empty, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_TriggerFollowUpSuggestions_Disabled(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.abEnabled = false

	if c.triggerFollowUpSuggestions(d) {
		t.Fatal("expected false when disabled")
	}
}

func TestFollowUpCoordinator_TriggerFollowUpSuggestions_Closed(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.closed = true

	if c.triggerFollowUpSuggestions(d) {
		t.Fatal("expected false when closed")
	}
}

func TestFollowUpCoordinator_TriggerFollowUpSuggestions_UsesCache(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.cachedButtons = []ActionButton{{Label: "cached"}}

	if !c.triggerFollowUpSuggestions(d) {
		t.Fatal("expected true when cached buttons exist")
	}
}

func TestFollowUpCoordinator_TriggerFollowUpSuggestions_NoAgentMessage(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.readEventsResult = []session.Event{} // no events → no agent message

	if c.triggerFollowUpSuggestions(d) {
		t.Fatal("expected false when no agent message")
	}
}

func TestFollowUpCoordinator_AnalyzeFollowUpQuestions_CASFail_Skips(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.casResult = false // CAS fails = another analysis in progress

	c.analyzeFollowUpQuestions(d, "prompt", "agent msg")

	// Must not have stored false (because we never claimed the flag)
	if d.storedFalse != 0 {
		t.Fatalf("expected 0 storedFalse calls, got %d", d.storedFalse)
	}
	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notifications, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_AnalyzeFollowUpQuestions_SessionClosed_Skips(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.closed = true

	c.analyzeFollowUpQuestions(d, "p", "m")

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notifications when closed, got %v", d.notifiedEvents)
	}
	if d.storedFalse != 1 {
		t.Fatalf("expected defer storedFalse=1, got %d", d.storedFalse)
	}
}

func TestFollowUpCoordinator_AnalyzeFollowUpQuestions_NoAux_Skips(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.auxAvailable = false

	c.analyzeFollowUpQuestions(d, "p", "m")

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notifications, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_AnalyzeFollowUpQuestions_AnalysisError_Skips(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.auxAvailable = true
	d.analyzeErr = errors.New("boom")

	c.analyzeFollowUpQuestions(d, "p", "m")

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notifications on error, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_AnalyzeFollowUpQuestions_HappyPath_CachesAndNotifies(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.auxAvailable = true
	d.analyzeResult = []ActionButton{{Label: "Do X", Response: "x"}}

	c.analyzeFollowUpQuestions(d, "user prompt", "agent msg")

	d.cacheMu.RLock()
	cached := d.cachedButtons
	d.cacheMu.RUnlock()

	if len(cached) != 1 || cached[0].Label != "Do X" {
		t.Fatalf("expected button cached, got %v", cached)
	}
	if len(d.notifiedEvents) != 1 || d.notifiedEvents[0] != "action_buttons" {
		t.Fatalf("expected action_buttons notification, got %v", d.notifiedEvents)
	}
	if d.storedFalse != 1 {
		t.Fatalf("expected defer storedFalse=1, got %d", d.storedFalse)
	}
}

func TestFollowUpCoordinator_AnalyzeFollowUpQuestions_IsPrompting_Discards(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.auxAvailable = true
	d.analyzeResult = []ActionButton{{Label: "Q"}}
	d.prompting = true // set AFTER analysis returns

	c.analyzeFollowUpQuestions(d, "p", "m")

	// Buttons should NOT be cached or notified
	d.cacheMu.RLock()
	cached := d.cachedButtons
	d.cacheMu.RUnlock()
	if len(cached) != 0 {
		t.Fatalf("expected no cached buttons when prompting, got %v", cached)
	}
	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no notifications when prompting, got %v", d.notifiedEvents)
	}
}

func TestFollowUpCoordinator_ApplyAfterProcessors_Notifications(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.applyAfterResult = processors.ApplyAfterResult{
		Notifications: []processors.AfterNotification{
			{Title: "hi", Message: "world", Style: "info"},
		},
	}

	c.applyAfterProcessors(d, context.Background(), "prompt", "user", "stop", tNow(), tNow(), acp.PromptResponse{}, true)

	if len(d.uiNotifyReqs) != 1 || d.uiNotifyReqs[0].Title != "hi" {
		t.Fatalf("expected UINotify for notification, got %v", d.uiNotifyReqs)
	}
}

func TestFollowUpCoordinator_ApplyAfterProcessors_ActionButtons_MergesAndNotifies(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.cachedButtons = []ActionButton{{Label: "existing"}}
	d.applyAfterResult = processors.ApplyAfterResult{
		ActionButtons: []processors.AfterActionButton{
			{Label: "new", Prompt: "do new"},
		},
	}

	c.applyAfterProcessors(d, context.Background(), "p", "u", "s", tNow(), tNow(), acp.PromptResponse{}, true)

	d.cacheMu.RLock()
	cached := d.cachedButtons
	d.cacheMu.RUnlock()

	if len(cached) != 2 || cached[0].Label != "existing" || cached[1].Label != "new" {
		t.Fatalf("expected merged buttons [existing, new], got %v", cached)
	}
	if len(d.notifiedEvents) == 0 {
		t.Fatal("expected action_buttons notification after merge")
	}
}

func TestFollowUpCoordinator_ApplyAfterProcessors_UserDataPatch(t *testing.T) {
	c := followUpCoordinator{}
	d := newFakeFollowUpDeps()
	d.getUserDataResult = &session.UserData{
		Attributes: []session.UserDataAttribute{{Name: "k1", Value: "v1"}},
	}
	d.applyAfterResult = processors.ApplyAfterResult{
		UserDataPatch: map[string]string{"k1": "updated", "k2": "new"},
	}

	c.applyAfterProcessors(d, context.Background(), "p", "u", "s", tNow(), tNow(), acp.PromptResponse{}, true)

	if len(d.setUserDataCalls) != 1 {
		t.Fatalf("expected 1 SetUserData call, got %d", len(d.setUserDataCalls))
	}
	attrs := d.setUserDataCalls[0].Attributes
	attrMap := make(map[string]string)
	for _, a := range attrs {
		attrMap[a.Name] = a.Value
	}
	if attrMap["k1"] != "updated" || attrMap["k2"] != "new" {
		t.Fatalf("unexpected patched attrs: %v", attrMap)
	}
}

func tNow() time.Time { return time.Now() }
