package conversation

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// compile-time check.
var _ configDeps = (*fakeConfigDeps)(nil)

type fakeConfigDeps struct {
	mu sync.Mutex

	// state knobs
	sessionID      string
	logger         *slog.Logger
	closed         bool
	hasParent      bool
	hasACPConn     bool
	isPrompting    bool
	usesLegacy     bool
	hasAgentModels bool
	currentModelID string
	baselineModel  string
	overrideActive bool

	configOptions []SessionConfigOption
	constraint    map[string]*config.ACPServerConstraint

	// pending config
	pendingMu     sync.Mutex
	pendingConfig map[string]string

	// prompt mu
	promptMuLocked bool

	// injected errors
	setModeErr  error
	setModelErr error

	// recorders
	modeRPCCalls      []string
	modelRPCCalls     []string
	persistedConfig   [][2]string
	persistedBaseline []string
	notifiedConfig    [][3]string // sessionID, configID, value
	baselineUpdates   []string
	overrideClears    int
	sessionCtx        context.Context
}

func newFakeConfigDeps() *fakeConfigDeps {
	return &fakeConfigDeps{
		sessionID:      "sess-1",
		logger:         slog.Default(),
		hasACPConn:     true,
		hasAgentModels: true,
		pendingConfig:  make(map[string]string),
		configOptions: []SessionConfigOption{
			{
				ID:           ConfigOptionCategoryModel,
				Category:     ConfigOptionCategoryModel,
				CurrentValue: "m-1",
				Options: []SessionConfigOptionValue{
					{Value: "m-1", Name: "Model 1"},
					{Value: "m-2", Name: "Model 2"},
				},
			},
			{
				ID:           ConfigOptionCategoryMode,
				Category:     ConfigOptionCategoryMode,
				CurrentValue: "code",
				Options: []SessionConfigOptionValue{
					{Value: "code", Name: "Code"},
					{Value: "chat", Name: "Chat"},
				},
			},
		},
		sessionCtx: context.Background(),
	}
}

// --- configDeps implementation ---

func (f *fakeConfigDeps) cmSessionID() string           { return f.sessionID }
func (f *fakeConfigDeps) cmLogger() *slog.Logger        { return f.logger }
func (f *fakeConfigDeps) cmIsClosed() bool              { return f.closed }
func (f *fakeConfigDeps) cmHasParent() bool             { return f.hasParent }
func (f *fakeConfigDeps) cmSessionCtx() context.Context { return f.sessionCtx }
func (f *fakeConfigDeps) cmHasACPConn() bool            { return f.hasACPConn }

func (f *fakeConfigDeps) cmSetSessionMode(_ context.Context, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modeRPCCalls = append(f.modeRPCCalls, value)
	return f.setModeErr
}
func (f *fakeConfigDeps) cmSetSessionModel(_ context.Context, modelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelRPCCalls = append(f.modelRPCCalls, modelID)
	return f.setModelErr
}

func (f *fakeConfigDeps) cmGetConfigOptions() []SessionConfigOption {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]SessionConfigOption, len(f.configOptions))
	copy(result, f.configOptions)
	return result
}
func (f *fakeConfigDeps) cmFindByID(id string) (SessionConfigOption, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, opt := range f.configOptions {
		if opt.ID == id {
			return opt, true
		}
	}
	return SessionConfigOption{}, false
}
func (f *fakeConfigDeps) cmFindByCategory(cat string) (SessionConfigOption, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, opt := range f.configOptions {
		if opt.Category == cat {
			return opt, true
		}
	}
	return SessionConfigOption{}, false
}
func (f *fakeConfigDeps) cmUsesLegacyModes() bool { return f.usesLegacy }
func (f *fakeConfigDeps) cmUpdateConfigOptionValue(id, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.configOptions {
		if f.configOptions[i].ID == id {
			f.configOptions[i].CurrentValue = value
			return
		}
	}
}

func (f *fakeConfigDeps) cmLockPendingConfig()               { f.pendingMu.Lock() }
func (f *fakeConfigDeps) cmUnlockPendingConfig()             { f.pendingMu.Unlock() }
func (f *fakeConfigDeps) cmSetPendingEntry(id, value string) { f.pendingConfig[id] = value }
func (f *fakeConfigDeps) cmDeletePendingEntry(id string)     { delete(f.pendingConfig, id) }
func (f *fakeConfigDeps) cmDrainPendingConfig() map[string]string {
	f.pendingMu.Lock()
	defer f.pendingMu.Unlock()
	if len(f.pendingConfig) == 0 {
		return nil
	}
	drained := f.pendingConfig
	f.pendingConfig = make(map[string]string)
	return drained
}

func (f *fakeConfigDeps) cmLockPromptMu()     { f.mu.Lock() }
func (f *fakeConfigDeps) cmUnlockPromptMu()   { f.mu.Unlock() }
func (f *fakeConfigDeps) cmIsPrompting() bool { return f.isPrompting }

func (f *fakeConfigDeps) cmSetBaselineAndClearOverride(baseline string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.baselineModel = baseline
	f.overrideActive = false
	f.baselineUpdates = append(f.baselineUpdates, baseline)
	f.overrideClears++
}
func (f *fakeConfigDeps) cmTakeBaselineIfOverride() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.overrideActive {
		return "", false
	}
	baseline := f.baselineModel
	f.overrideActive = false
	return baseline, true
}
func (f *fakeConfigDeps) cmHasAgentModels() bool      { return f.hasAgentModels }
func (f *fakeConfigDeps) cmGetCurrentModelID() string { return f.currentModelID }
func (f *fakeConfigDeps) cmSetCurrentModelID(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.currentModelID = id
}
func (f *fakeConfigDeps) cmGetACPServerConstraint(category string) *config.ACPServerConstraint {
	if f.constraint == nil {
		return nil
	}
	return f.constraint[category]
}
func (f *fakeConfigDeps) cmPersistConfigValue(configID, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistedConfig = append(f.persistedConfig, [2]string{configID, value})
}
func (f *fakeConfigDeps) cmPersistBaselineModel(value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistedBaseline = append(f.persistedBaseline, value)
}
func (f *fakeConfigDeps) cmNotifyConfigChanged(configID, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifiedConfig = append(f.notifiedConfig, [3]string{f.sessionID, configID, value})
}
func (f *fakeConfigDeps) cmRecordSessionChange(kind, value, previousValue string) {}

// --- Tests ---

func TestConfigManager_ConfigOptions_ReturnsCopy(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	opts := c.configOptions(d)
	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}
	// Modify the copy — should not affect the source.
	opts[0].CurrentValue = "MODIFIED"
	opts2 := c.configOptions(d)
	if opts2[0].CurrentValue == "MODIFIED" {
		t.Fatal("modifying returned copy should not affect source")
	}
}

func TestConfigManager_GetConfigValue_Found(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	got := c.getConfigValue(d, ConfigOptionCategoryModel)
	if got != "m-1" {
		t.Fatalf("expected 'm-1', got %q", got)
	}
}

func TestConfigManager_GetConfigValue_NotFound(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	got := c.getConfigValue(d, "nonexistent")
	if got != "" {
		t.Fatalf("expected empty string for unknown configID, got %q", got)
	}
}

func TestConfigManager_SetConfigOption_Closed(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.closed = true

	err := c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "m-2")
	if err == nil {
		t.Fatal("expected error when session closed")
	}
}

func TestConfigManager_SetConfigOption_NoConn(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.hasACPConn = false

	err := c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "m-2")
	if err == nil {
		t.Fatal("expected error when no ACP connection")
	}
}

func TestConfigManager_SetConfigOption_UnknownID(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	err := c.setConfigOption(d, context.Background(), "unknown", "x")
	if err == nil {
		t.Fatal("expected error for unknown configID")
	}
}

func TestConfigManager_SetConfigOption_InvalidValue(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	err := c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "bad-model")
	if err == nil {
		t.Fatal("expected error for invalid value")
	}
}

func TestConfigManager_SetConfigOption_IdlePath_ModelRPC(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	// not prompting → apply immediately

	err := c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "m-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.modelRPCCalls) != 1 || d.modelRPCCalls[0] != "m-2" {
		t.Fatalf("expected model RPC for 'm-2', got %v", d.modelRPCCalls)
	}
	// Baseline should be updated.
	if len(d.baselineUpdates) != 1 || d.baselineUpdates[0] != "m-2" {
		t.Fatalf("expected baseline update to 'm-2', got %v", d.baselineUpdates)
	}
	// Config notify should fire.
	if len(d.notifiedConfig) == 0 {
		t.Fatal("expected config changed notification")
	}
}

func TestConfigManager_SetConfigOption_PromptingPath_DefersToPending(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.isPrompting = true

	err := c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "m-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should NOT issue ACP RPC when prompting.
	if len(d.modelRPCCalls) != 0 {
		t.Fatalf("expected no model RPC while prompting, got %v", d.modelRPCCalls)
	}
	// Should store to pending (checked via drain).
	pending := d.cmDrainPendingConfig()
	if pending[ConfigOptionCategoryModel] != "m-2" {
		t.Fatalf("expected pending config entry for model, got %v", pending)
	}
	// Baseline should still be updated immediately for model changes.
	if len(d.baselineUpdates) != 1 || d.baselineUpdates[0] != "m-2" {
		t.Fatalf("expected immediate baseline update, got %v", d.baselineUpdates)
	}
}

func TestConfigManager_ApplyConfigOption_ModeUsesLegacy(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.usesLegacy = true

	err := c.applyConfigOption(d, context.Background(), ConfigOptionCategoryMode, "chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.modeRPCCalls) != 1 || d.modeRPCCalls[0] != "chat" {
		t.Fatalf("expected mode RPC for 'chat', got %v", d.modeRPCCalls)
	}
}

func TestConfigManager_ApplyConfigOption_ModeRPCError(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.usesLegacy = true
	d.setModeErr = errors.New("rpc fail")

	err := c.applyConfigOption(d, context.Background(), ConfigOptionCategoryMode, "chat")
	if err == nil {
		t.Fatal("expected error when mode RPC fails")
	}
}

func TestConfigManager_FlushPendingConfig_Empty(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	// no pending config

	c.flushPendingConfig(d) // should not panic or error

	if len(d.modelRPCCalls) != 0 {
		t.Fatalf("expected no RPC calls with empty pending config, got %v", d.modelRPCCalls)
	}
}

func TestConfigManager_FlushPendingConfig_AppliesPending(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.pendingConfig[ConfigOptionCategoryModel] = "m-2"

	c.flushPendingConfig(d)

	if len(d.modelRPCCalls) != 1 || d.modelRPCCalls[0] != "m-2" {
		t.Fatalf("expected model RPC for 'm-2', got %v", d.modelRPCCalls)
	}
}

func TestConfigManager_PersistConfigValue_Mode(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	c.persistConfigValue(d, ConfigOptionCategoryMode, "chat")

	if len(d.persistedConfig) != 1 || d.persistedConfig[0] != [2]string{ConfigOptionCategoryMode, "chat"} {
		t.Fatalf("expected persisted config, got %v", d.persistedConfig)
	}
}

func TestConfigManager_PersistBaselineModel(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	c.persistBaselineModel(d, "m-2")

	if len(d.persistedBaseline) != 1 || d.persistedBaseline[0] != "m-2" {
		t.Fatalf("expected persisted baseline, got %v", d.persistedBaseline)
	}
}

func TestConfigManager_SetActiveModelOnly_Success(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()

	err := c.setActiveModelOnly(d, context.Background(), "m-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.modelRPCCalls) != 1 || d.modelRPCCalls[0] != "m-2" {
		t.Fatalf("expected model RPC, got %v", d.modelRPCCalls)
	}
	if d.currentModelID != "m-2" {
		t.Fatalf("expected currentModelID='m-2', got %q", d.currentModelID)
	}
	// Must NOT update baseline.
	if len(d.baselineUpdates) != 0 {
		t.Fatalf("setActiveModelOnly must not update baseline, got %v", d.baselineUpdates)
	}
}

func TestConfigManager_SetActiveModelOnly_RPCError(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.setModelErr = errors.New("fail")

	err := c.setActiveModelOnly(d, context.Background(), "m-2")
	if err == nil {
		t.Fatal("expected error on RPC failure")
	}
}

func TestConfigManager_RestoreBaselineIfOverride_NotActive(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.overrideActive = false

	c.restoreBaselineIfOverride(d)

	if len(d.modelRPCCalls) != 0 {
		t.Fatalf("expected no RPC when override not active, got %v", d.modelRPCCalls)
	}
}

func TestConfigManager_RestoreBaselineIfOverride_AlreadyAtBaseline(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.overrideActive = true
	d.baselineModel = "m-1"
	d.currentModelID = "m-1" // already at baseline

	c.restoreBaselineIfOverride(d)

	if len(d.modelRPCCalls) != 0 {
		t.Fatalf("expected no RPC when already at baseline, got %v", d.modelRPCCalls)
	}
}

func TestConfigManager_RestoreBaselineIfOverride_RestoresModel(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.overrideActive = true
	d.baselineModel = "m-1"
	d.currentModelID = "m-2" // different from baseline

	c.restoreBaselineIfOverride(d)

	if len(d.modelRPCCalls) != 1 || d.modelRPCCalls[0] != "m-1" {
		t.Fatalf("expected model RPC restoring to 'm-1', got %v", d.modelRPCCalls)
	}
}

func TestConfigManager_ApplyConfigConstraints_NoConstraint(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	// No constraints configured.

	c.applyConfigConstraints(d, ConfigOptionCategoryModel) // should be no-op

	if len(d.modelRPCCalls) != 0 {
		t.Fatalf("expected no RPC without constraint, got %v", d.modelRPCCalls)
	}
}

func TestConfigManager_ApplyConfigConstraints_MatchesOption(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.constraint = map[string]*config.ACPServerConstraint{
		ConfigOptionCategoryModel: {Pattern: "Model 2", MatchMode: "exact"}, // matches opt.Name
	}
	d.currentModelID = "m-1" // different, so RPC will fire

	c.applyConfigConstraints(d, ConfigOptionCategoryModel)

	if len(d.modelRPCCalls) != 1 || d.modelRPCCalls[0] != "m-2" {
		t.Fatalf("expected model RPC for 'm-2', got %v", d.modelRPCCalls)
	}
}

func TestConfigManager_ApplyConfigConstraints_AlreadySet(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.constraint = map[string]*config.ACPServerConstraint{
		ConfigOptionCategoryModel: {Pattern: "Model 1", MatchMode: "exact"}, // matches opt.Name
	}
	d.currentModelID = "m-1" // already at constraint value

	c.applyConfigConstraints(d, ConfigOptionCategoryModel)

	if len(d.modelRPCCalls) != 0 {
		t.Fatalf("expected no RPC when already at constraint value, got %v", d.modelRPCCalls)
	}
}
