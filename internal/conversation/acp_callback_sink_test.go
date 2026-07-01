package conversation

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// compile-time check that fakeCallbackDeps satisfies acpCallbackDeps.
var _ acpCallbackDeps = (*fakeCallbackDeps)(nil)

// fakeCallbackDeps is a test double for acpCallbackDeps. It records every
// state mutation and notification so tests can assert ordering, content and
// counts without going through BackgroundSession internals.
type fakeCallbackDeps struct {
	mu sync.Mutex

	// state knobs (read by sink methods)
	closed              bool
	sessionID           string
	logger              *slog.Logger
	observerCount       int
	hasObservers        bool
	autoApprove         bool
	sessionAutoApprove  bool
	mcpAvailable        bool
	availableCmds       []AvailableCommand
	constraints         map[string]*config.ACPServerConstraint
	uiResp              UIPromptResponse
	uiErr               error
	baselineModel       string // simulates persisted baselineModel; init only if empty
	defaultBaselineUsed bool
	streamingSuppressed bool // mitto-2tm: gates streaming callback short-circuit

	// recorders
	notifiedEvents      []string
	recordedEvents      []session.Event
	recordedEventKinds  []string
	recordedPermissions []recordedPermission
	contextUsages       [][2]int
	mcpRequests         []string
	planEntries         [][]PlanEntry
	uiPromptCalls       []UIPromptRequest
	modeCurrentValues   []string
	persistedConfig     [][2]string
	configChanged       [][2]string
	legacyModesSet      []SessionConfigOption
	storedAgentModels   []*acp.UnstableSessionModelState
	modelReplacements   []SessionConfigOption
	asyncConstraintCats []string
}

type recordedPermission struct{ Title, OptionID, Outcome string }

// --- acpCallbackDeps impl ---

func (f *fakeCallbackDeps) cbIsClosed() bool       { return f.closed }
func (f *fakeCallbackDeps) cbSessionID() string    { return f.sessionID }
func (f *fakeCallbackDeps) cbLogger() *slog.Logger { return f.logger }

func (f *fakeCallbackDeps) cbNotifyObservers(fn func(SessionObserver)) {
	fn(&callbackRecorderObserver{deps: f})
}
func (f *fakeCallbackDeps) cbObserverCount() int { return f.observerCount }
func (f *fakeCallbackDeps) cbHasObservers() bool { return f.hasObservers }

func (f *fakeCallbackDeps) cbRecordEventWithSeq(event session.Event, kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedEvents = append(f.recordedEvents, event)
	f.recordedEventKinds = append(f.recordedEventKinds, kind)
}
func (f *fakeCallbackDeps) cbRecordPermission(title, opt, outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedPermissions = append(f.recordedPermissions, recordedPermission{title, opt, outcome})
}

func (f *fakeCallbackDeps) cbSetContextUsage(size, used int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contextUsages = append(f.contextUsages, [2]int{size, used})
}

func (f *fakeCallbackDeps) cbSetAvailableCommands(cmds []AvailableCommand) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.availableCmds = cmds
}
func (f *fakeCallbackDeps) cbGetAvailableCommands() []AvailableCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.availableCmds == nil {
		return nil
	}
	out := make([]AvailableCommand, len(f.availableCmds))
	copy(out, f.availableCmds)
	return out
}

func (f *fakeCallbackDeps) cbRegisterPendingMCPRequest(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcpRequests = append(f.mcpRequests, id)
	return f.mcpAvailable
}

func (f *fakeCallbackDeps) cbNotifyPlanStateChanged(entries []PlanEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planEntries = append(f.planEntries, entries)
}

func (f *fakeCallbackDeps) cbAutoApprove() bool                   { return f.autoApprove }
func (f *fakeCallbackDeps) cbSessionAutoApprovePermissions() bool { return f.sessionAutoApprove }
func (f *fakeCallbackDeps) cbUIPrompt(_ context.Context, req UIPromptRequest) (UIPromptResponse, error) {
	f.mu.Lock()
	f.uiPromptCalls = append(f.uiPromptCalls, req)
	f.mu.Unlock()
	return f.uiResp, f.uiErr
}

func (f *fakeCallbackDeps) cbSetModeCurrentValue(modeID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modeCurrentValues = append(f.modeCurrentValues, modeID)
}
func (f *fakeCallbackDeps) cbPersistConfigValue(configID, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistedConfig = append(f.persistedConfig, [2]string{configID, value})
}
func (f *fakeCallbackDeps) cbNotifyConfigChanged(configID, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configChanged = append(f.configChanged, [2]string{configID, value})
}

func (f *fakeCallbackDeps) cbSetLegacyModes(opt SessionConfigOption) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.legacyModesSet = append(f.legacyModesSet, opt)
}

func (f *fakeCallbackDeps) cbStoreAgentModels(m *acp.UnstableSessionModelState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storedAgentModels = append(f.storedAgentModels, m)
}
func (f *fakeCallbackDeps) cbACPServerConstraint(cat string) *config.ACPServerConstraint {
	return f.constraints[cat]
}
func (f *fakeCallbackDeps) cbReplaceModelConfigOption(opt SessionConfigOption) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelReplacements = append(f.modelReplacements, opt)
}
func (f *fakeCallbackDeps) cbInitBaselineModelIfEmpty(defaultModel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.baselineModel == "" {
		f.baselineModel = defaultModel
		f.defaultBaselineUsed = true
	}
}
func (f *fakeCallbackDeps) cbApplyConfigConstraintsAsync(category string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asyncConstraintCats = append(f.asyncConstraintCats, category)
}

func (f *fakeCallbackDeps) cbStreamingSuppressed() bool {
	return f.streamingSuppressed
}

// callbackRecorderObserver records observer events with a stable string key.
type callbackRecorderObserver struct{ deps *fakeCallbackDeps }

func (r *callbackRecorderObserver) record(s string) {
	r.deps.mu.Lock()
	r.deps.notifiedEvents = append(r.deps.notifiedEvents, s)
	r.deps.mu.Unlock()
}

func (r *callbackRecorderObserver) OnAgentMessage(seq int64, _ string) {
	r.record("agent_message:" + strconv.FormatInt(seq, 10))
}
func (r *callbackRecorderObserver) OnAgentThought(seq int64, _ string) {
	r.record("agent_thought:" + strconv.FormatInt(seq, 10))
}
func (r *callbackRecorderObserver) OnToolCall(seq int64, _, _, _ string) {
	r.record("tool_call:" + strconv.FormatInt(seq, 10))
}
func (r *callbackRecorderObserver) OnToolUpdate(seq int64, _ string, _ *string) {
	r.record("tool_update:" + strconv.FormatInt(seq, 10))
}
func (r *callbackRecorderObserver) OnPlan(seq int64, _ []PlanEntry) {
	r.record("plan:" + strconv.FormatInt(seq, 10))
}
func (r *callbackRecorderObserver) OnFileWrite(seq int64, _ string, _ int) {
	r.record("file_write:" + strconv.FormatInt(seq, 10))
}
func (r *callbackRecorderObserver) OnFileRead(seq int64, _ string, _ int) {
	r.record("file_read:" + strconv.FormatInt(seq, 10))
}
func (r *callbackRecorderObserver) OnContextUsageUpdate(size, used int) {
	r.record("ctx:" + strconv.Itoa(size) + "/" + strconv.Itoa(used))
}
func (r *callbackRecorderObserver) OnAvailableCommandsUpdated(c []AvailableCommand) {
	r.record("available_commands:" + strconv.Itoa(len(c)))
}
func (r *callbackRecorderObserver) OnQueueMessageSending(string)             {}
func (r *callbackRecorderObserver) OnQueueMessageSent(string)                {}
func (r *callbackRecorderObserver) OnQueueUpdated(int, string, string)       {}
func (r *callbackRecorderObserver) OnQueueReordered([]session.QueuedMessage) {}
func (r *callbackRecorderObserver) OnError(string)                           {}
func (r *callbackRecorderObserver) OnPromptComplete(int)                     {}
func (r *callbackRecorderObserver) OnActionButtons([]ActionButton)           {}
func (r *callbackRecorderObserver) OnUserPrompt(int64, string, string, string, []string, []string, string, int) {
}
func (r *callbackRecorderObserver) OnACPStopped(string)              {}
func (r *callbackRecorderObserver) OnACPStarted()                    {}
func (r *callbackRecorderObserver) OnUIPrompt(UIPromptRequest)       {}
func (r *callbackRecorderObserver) OnUIPromptDismiss(string, string) {}
func (r *callbackRecorderObserver) OnNotification(UINotifyRequest)   {}

// --- Tests ---

func TestCallbackSink_ClosedShortCircuits(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{closed: true}

	s.onAgentMessage(d, 1, "x")
	s.onAgentThought(d, 1, "x")
	s.onToolCall(d, 1, "i", "t", "s")
	s.onToolUpdate(d, 1, "i", nil)
	s.onPlan(d, 1, []PlanEntry{{Content: "x"}})
	s.onFileWrite(d, 1, "/a", 1)
	s.onFileRead(d, 1, "/a", 1)
	s.onMittoToolCall(d, "req")
	s.onAvailableCommands(d, []AvailableCommand{{Name: "a"}})
	s.onCurrentModeChanged(d, "code")

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no observer notifications when closed, got %v", d.notifiedEvents)
	}
	if len(d.recordedEvents) != 0 {
		t.Fatalf("expected no recorded events when closed, got %d", len(d.recordedEvents))
	}
}

func TestCallbackSink_StreamCallbacksRecordAndNotify(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{}

	status := "ok"
	s.onAgentMessage(d, 1, "<p>hi</p>")
	s.onAgentThought(d, 2, "thinking")
	s.onToolCall(d, 3, "tc1", "title", "running")
	s.onToolUpdate(d, 4, "tc1", &status)
	s.onPlan(d, 5, []PlanEntry{{Content: "step", Priority: "high", Status: "pending"}})
	s.onFileWrite(d, 6, "/a", 10)
	s.onFileRead(d, 7, "/b", 20)

	if len(d.recordedEvents) != 7 {
		t.Fatalf("expected 7 recorded events, got %d", len(d.recordedEvents))
	}
	wantKinds := []string{"agent message", "agent thought", "tool call", "tool call update", "plan", "file write", "file read"}
	if !reflect.DeepEqual(d.recordedEventKinds, wantKinds) {
		t.Fatalf("kinds mismatch:\n got %v\nwant %v", d.recordedEventKinds, wantKinds)
	}

	wantNotif := []string{
		"agent_message:1", "agent_thought:2", "tool_call:3", "tool_update:4",
		"plan:5", "file_write:6", "file_read:7",
	}
	if !reflect.DeepEqual(d.notifiedEvents, wantNotif) {
		t.Fatalf("notifications mismatch:\n got %v\nwant %v", d.notifiedEvents, wantNotif)
	}

	if len(d.planEntries) != 1 || len(d.planEntries[0]) != 1 || d.planEntries[0][0].Content != "step" {
		t.Fatalf("plan state callback not invoked correctly: %+v", d.planEntries)
	}
}

func TestCallbackSink_ContextUsage(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{}
	s.onContextUsageUpdate(d, 1000, 250)
	if len(d.contextUsages) != 1 || d.contextUsages[0] != [2]int{1000, 250} {
		t.Fatalf("context usage not stored: %v", d.contextUsages)
	}
	if !reflect.DeepEqual(d.notifiedEvents, []string{"ctx:1000/250"}) {
		t.Fatalf("expected ctx notification, got %v", d.notifiedEvents)
	}
}

func TestCallbackSink_MittoToolCall_NoMCPServer(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{mcpAvailable: false}
	s.onMittoToolCall(d, "req-1")
	if len(d.mcpRequests) != 1 || d.mcpRequests[0] != "req-1" {
		t.Fatalf("expected register attempt, got %v", d.mcpRequests)
	}
}

func TestCallbackSink_MittoToolCall_WithMCPServer(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{mcpAvailable: true}
	s.onMittoToolCall(d, "req-2")
	if len(d.mcpRequests) != 1 || d.mcpRequests[0] != "req-2" {
		t.Fatalf("expected register attempt, got %v", d.mcpRequests)
	}
}

func TestCallbackSink_AvailableCommands_SortsAndStores(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{}
	s.onAvailableCommands(d, []AvailableCommand{{Name: "zebra"}, {Name: "alpha"}, {Name: "mango"}})

	if len(d.availableCmds) != 3 {
		t.Fatalf("expected 3 commands stored, got %d", len(d.availableCmds))
	}
	want := []string{"alpha", "mango", "zebra"}
	for i, c := range d.availableCmds {
		if c.Name != want[i] {
			t.Fatalf("sort mismatch at %d: got %q, want %q", i, c.Name, want[i])
		}
	}
	if !reflect.DeepEqual(d.notifiedEvents, []string{"available_commands:3"}) {
		t.Fatalf("expected one available_commands notification, got %v", d.notifiedEvents)
	}

	got := s.availableCommands(d)
	if len(got) != 3 || got[0].Name != "alpha" {
		t.Fatalf("availableCommands returned %v", got)
	}
}

func TestCallbackSink_Permission_GlobalAutoApprove(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{autoApprove: true, hasObservers: true}

	title := "Allow?"
	resp, err := s.onPermission(d, context.Background(), acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{ToolCallId: "tc-1", Title: &title},
		Options: []acp.PermissionOption{
			{OptionId: "ok", Name: "OK", Kind: acp.PermissionOptionKindAllowOnce},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Selected == nil {
		t.Fatalf("expected a selection in auto-approve outcome")
	}
	if len(d.recordedPermissions) != 1 || d.recordedPermissions[0].Outcome != "auto_approved" {
		t.Fatalf("expected auto_approved permission record, got %+v", d.recordedPermissions)
	}
	if len(d.uiPromptCalls) != 0 {
		t.Fatalf("auto-approve must not call UIPrompt, got %d calls", len(d.uiPromptCalls))
	}
}

func TestCallbackSink_Permission_NoObservers_Cancels(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{hasObservers: false, autoApprove: false, logger: slog.Default()}
	title := "?"
	resp, err := s.onPermission(d, context.Background(), acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{ToolCallId: "tc-2", Title: &title},
		Options:  []acp.PermissionOption{{OptionId: "x", Name: "X"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("expected cancelled outcome, got %+v", resp.Outcome)
	}
	if len(d.uiPromptCalls) != 0 {
		t.Fatalf("UIPrompt must not be called without observers")
	}
}

func TestCallbackSink_Permission_UserSelects(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{
		hasObservers: true,
		uiResp:       UIPromptResponse{OptionID: "allow"},
	}
	title := "ok?"
	resp, err := s.onPermission(d, context.Background(), acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{ToolCallId: "tc-3", Title: &title},
		Options: []acp.PermissionOption{
			{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Selected == nil || string(resp.Outcome.Selected.OptionId) != "allow" {
		t.Fatalf("expected user-selected allow, got %+v", resp.Outcome)
	}
	if len(d.uiPromptCalls) != 1 || d.uiPromptCalls[0].ToolCallID != "tc-3" {
		t.Fatalf("expected single UIPrompt call for tc-3, got %+v", d.uiPromptCalls)
	}
	if len(d.recordedPermissions) != 1 || d.recordedPermissions[0].Outcome != "user_selected" {
		t.Fatalf("expected user_selected permission record, got %+v", d.recordedPermissions)
	}
}

func TestCallbackSink_Permission_UIPromptError_Cancels(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{hasObservers: true, uiErr: errors.New("boom")}
	title := "ok?"
	resp, err := s.onPermission(d, context.Background(), acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{ToolCallId: "tc-4", Title: &title},
		Options:  []acp.PermissionOption{{OptionId: "ok", Name: "OK"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatalf("expected cancelled outcome on UIPrompt error, got %+v", resp.Outcome)
	}
}

func TestCallbackSink_OnCurrentModeChanged(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{}
	s.onCurrentModeChanged(d, "code")
	if !reflect.DeepEqual(d.modeCurrentValues, []string{"code"}) {
		t.Fatalf("expected mode current value set to 'code', got %v", d.modeCurrentValues)
	}
	if len(d.persistedConfig) != 1 || d.persistedConfig[0] != [2]string{ConfigOptionCategoryMode, "code"} {
		t.Fatalf("expected mode persisted, got %v", d.persistedConfig)
	}
	if len(d.configChanged) != 1 || d.configChanged[0] != [2]string{ConfigOptionCategoryMode, "code"} {
		t.Fatalf("expected onConfigChanged notify, got %v", d.configChanged)
	}
}

func TestCallbackSink_SetSessionModes(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{}
	s.setSessionModes(d, nil)
	if len(d.legacyModesSet) != 0 {
		t.Fatalf("nil modes must be a no-op, got %v", d.legacyModesSet)
	}

	desc := "Code mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "code",
		AvailableModes: []acp.SessionMode{
			{Id: "code", Name: "Code", Description: &desc},
			{Id: "plan", Name: "Plan"},
		},
	}
	s.setSessionModes(d, modes)
	if len(d.legacyModesSet) != 1 {
		t.Fatalf("expected one legacy modes set, got %d", len(d.legacyModesSet))
	}
	opt := d.legacyModesSet[0]
	if opt.ID != ConfigOptionCategoryMode || opt.CurrentValue != "code" || len(opt.Options) != 2 {
		t.Fatalf("unexpected mode option: %+v", opt)
	}
	if opt.Options[0].Description != "Code mode" || opt.Options[1].Description != "" {
		t.Fatalf("descriptions mismatch: %+v", opt.Options)
	}
	if len(d.persistedConfig) != 1 || d.persistedConfig[0] != [2]string{ConfigOptionCategoryMode, "code"} {
		t.Fatalf("expected mode persisted, got %v", d.persistedConfig)
	}
}

func TestCallbackSink_SetAgentModels_NilOrEmpty(t *testing.T) {
	s := acpCallbackSink{}

	t.Run("nil", func(t *testing.T) {
		d := &fakeCallbackDeps{}
		s.setAgentModels(d, nil)
		if len(d.storedAgentModels) != 1 || d.storedAgentModels[0] != nil {
			t.Fatalf("expected agentModels stored as nil, got %+v", d.storedAgentModels)
		}
		if len(d.modelReplacements) != 0 || len(d.asyncConstraintCats) != 0 {
			t.Fatalf("nil models must not trigger downstream work")
		}
	})

	t.Run("empty available", func(t *testing.T) {
		d := &fakeCallbackDeps{}
		s.setAgentModels(d, &acp.UnstableSessionModelState{CurrentModelId: "x"})
		if len(d.modelReplacements) != 0 || len(d.asyncConstraintCats) != 0 {
			t.Fatalf("empty AvailableModels must not trigger downstream work")
		}
	})
}

func TestCallbackSink_SetAgentModels_FullFlow_NoConstraint(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{}
	models := &acp.UnstableSessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []acp.UnstableModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	s.setAgentModels(d, models)

	if len(d.modelReplacements) != 1 {
		t.Fatalf("expected one model config option replacement, got %d", len(d.modelReplacements))
	}
	opt := d.modelReplacements[0]
	if opt.Category != ConfigOptionCategoryModel || opt.CurrentValue != "m-1" || len(opt.Options) != 2 {
		t.Fatalf("unexpected model option: %+v", opt)
	}
	if !d.defaultBaselineUsed || d.baselineModel != "m-1" {
		t.Fatalf("expected baseline initialized to 'm-1', got %q (used=%v)", d.baselineModel, d.defaultBaselineUsed)
	}
	if !reflect.DeepEqual(d.asyncConstraintCats, []string{ConfigOptionCategoryModel}) {
		t.Fatalf("expected async constraints kick-off for model, got %v", d.asyncConstraintCats)
	}
}

func TestCallbackSink_SetAgentModels_PreAppliesConstraint(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{
		constraints: map[string]*config.ACPServerConstraint{
			ConfigOptionCategoryModel: {Pattern: "Model 2", MatchMode: "exact"},
		},
	}
	models := &acp.UnstableSessionModelState{
		CurrentModelId: "m-1",
		AvailableModels: []acp.UnstableModelInfo{
			{ModelId: "m-1", Name: "Model 1"},
			{ModelId: "m-2", Name: "Model 2"},
		},
	}
	s.setAgentModels(d, models)

	if len(d.modelReplacements) != 1 {
		t.Fatalf("expected one model replacement, got %d", len(d.modelReplacements))
	}
	if d.modelReplacements[0].CurrentValue != "m-2" {
		t.Fatalf("expected constraint pre-applied (CurrentValue=m-2), got %q", d.modelReplacements[0].CurrentValue)
	}
	// Baseline must still seed from the agent's reported model, NOT from the constraint match.
	if d.baselineModel != "m-1" {
		t.Fatalf("baseline should seed from agent currentId 'm-1', got %q", d.baselineModel)
	}
}

func TestCallbackSink_LogAgentModels_NilSafe(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{} // nil logger
	s.logAgentModels(d, nil)
	s.logAgentModels(d, &acp.UnstableSessionModelState{})
	// no panic, no recorded state
	if len(d.notifiedEvents) != 0 {
		t.Fatalf("logAgentModels must not produce side effects, got %v", d.notifiedEvents)
	}
}

// TestACPCallbackSink_SuppressionShortCircuits_StreamingCallbacks verifies that when
// cbStreamingSuppressed() returns true, each gated callback is a pure no-op:
// no recorder events, no observer notifications, no state mutations.
func TestACPCallbackSink_SuppressionShortCircuits_StreamingCallbacks(t *testing.T) {
	s := acpCallbackSink{}
	d := &fakeCallbackDeps{
		streamingSuppressed: true,
		hasObservers:        true,
		observerCount:       1,
	}

	status := "running"
	s.onContextUsageUpdate(d, 1000, 500)
	s.onAgentMessage(d, 1, "<p>hi</p>")
	s.onAgentThought(d, 2, "thinking")
	s.onToolCall(d, 3, "tc1", "title", "running")
	s.onToolUpdate(d, 4, "tc1", &status)
	s.onPlan(d, 5, []PlanEntry{{Content: "step"}})
	s.onAvailableCommands(d, []AvailableCommand{{Name: "clear"}})
	s.onCurrentModeChanged(d, "code")

	if len(d.notifiedEvents) != 0 {
		t.Fatalf("expected no observer notifications when suppressed, got %v", d.notifiedEvents)
	}
	if len(d.recordedEvents) != 0 {
		t.Fatalf("expected no recorded events when suppressed, got %d", len(d.recordedEvents))
	}
	if len(d.contextUsages) != 0 {
		t.Fatalf("expected no context usage stored when suppressed, got %v", d.contextUsages)
	}
	if len(d.planEntries) != 0 {
		t.Fatalf("expected no plan state callback when suppressed, got %v", d.planEntries)
	}
	if len(d.modeCurrentValues) != 0 {
		t.Fatalf("expected no mode value update when suppressed, got %v", d.modeCurrentValues)
	}
	if len(d.persistedConfig) != 0 {
		t.Fatalf("expected no config persist when suppressed, got %v", d.persistedConfig)
	}
	// availableCmds must remain nil (cbSetAvailableCommands not called)
	if d.availableCmds != nil {
		t.Fatalf("expected available commands not stored when suppressed, got %v", d.availableCmds)
	}
}
