package conversation

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// Model names/tags are deliberately distinct from the shipped default profiles so
// these tests do not depend on config.DefaultModelProfiles().
func behaviorProfiles() []config.ModelProfile {
	return []config.ModelProfile{
		{Name: "Deep", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "alpha-deep"}, Tags: []string{"bh-deep"}},
		{Name: "Fast", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "gamma-fast"}, Tags: []string{"bh-fast"}},
	}
}

func behaviorCatalog(current string) *SessionModelState {
	return &SessionModelState{
		CurrentModelId: current,
		AvailableModels: []ModelInfo{
			{ModelId: "m-alpha", Name: "alpha-deep"},
			{ModelId: "m-beta", Name: "beta-mid"},
			{ModelId: "m-gamma", Name: "gamma-fast"},
		},
	}
}

// seedBaseline mimics a conversation creation/resume: the initial preference chain
// computed by SessionManager is handed to a BackgroundSession whose agent reported
// agentDefault, and cbInitBaselineModelIfEmpty picks the baseline. persisted, when
// non-empty, simulates an existing conversation with a stored BaselineModel.
func seedBaseline(t *testing.T, prefs []config.PromptPreferredModel, agentDefault, persisted string) (*BackgroundSession, *session.Store, string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sid := "20260101-000000-behavior1"
	if err := store.Create(session.Metadata{
		SessionID: sid, ACPServer: "test-server", WorkingDir: "/tmp", Name: "Test", BaselineModel: persisted,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bs := &BackgroundSession{
		persistedID:            sid,
		store:                  store,
		mittoConfig:            &config.Config{Models: behaviorProfiles()},
		initialModelPreference: prefs,
		agentModels:            behaviorCatalog(agentDefault),
	}
	bs.cbInitBaselineModelIfEmpty(agentDefault)
	return bs, store, sid
}

func persistedBaseline(t *testing.T, store *session.Store, sid string) string {
	t.Helper()
	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	return meta.BaselineModel
}

func newPromptPrefManager(promptPrefs []config.PromptPreferredModel) *SessionManager {
	sm := NewSessionManager("", "test-server", false, nil)
	sm.SetPreferredModelsResolver(func(string, string) []config.PromptPreferredModel { return promptPrefs })
	return sm
}

func TestNamedPromptInitialModel_BecomesPersistedBaselineWithoutFirstTurnSwitch(t *testing.T) {
	promptPrefs := []config.PromptPreferredModel{{ModelTag: "bh-deep"}}
	workspaceFallback := []config.PromptPreferredModel{{ModelTag: "bh-fast"}}

	sm := newPromptPrefManager(promptPrefs)
	prefs := sm.resolveInitialModelPreference("Deep prompt", "/work", workspaceFallback)

	// Agent default is a different model than the prompt's preference.
	bs, store, sid := seedBaseline(t, prefs, "m-beta", "")
	if got := bs.GetBaselineModel(); got != "m-alpha" {
		t.Fatalf("in-memory baseline = %q, want m-alpha (prompt preference)", got)
	}
	if got := persistedBaseline(t, store, sid); got != "m-alpha" {
		t.Fatalf("persisted baseline = %q, want m-alpha", got)
	}

	// First turn: agent is on the baseline, so dispatching the prompt must not
	// switch the model or record a transient override.
	d := newFakePromptDeps()
	d.agentModels = behaviorCatalog(bs.GetBaselineModel())
	d.modelProfiles = behaviorProfiles()
	d.baselineModel = bs.GetBaselineModel()

	p := promptDispatcher{}
	p.applyModelPreference(d, PromptMeta{PromptName: "Deep prompt", PreferredModels: promptPrefs})

	if len(d.setActiveModelCalls) != 0 {
		t.Errorf("first turn switched model: %v, want none", d.setActiveModelCalls)
	}
	for _, c := range d.recordedSessionChanges {
		if c.Kind == ConfigOptionCategoryModelOverride {
			t.Errorf("first turn recorded a model_override change: %+v", c)
		}
	}
	if d.overrideActive {
		t.Error("first turn left a transient override active")
	}
}

func TestNamedPromptInitialModel_UnmatchedPromptPrefFallsBackToWorkspacePreference(t *testing.T) {
	sm := newPromptPrefManager([]config.PromptPreferredModel{{ModelTag: "no-such-tag"}})
	workspaceFallback := []config.PromptPreferredModel{{ModelTag: "bh-fast"}}

	prefs := sm.resolveInitialModelPreference("Unmatched prompt", "/work", workspaceFallback)
	bs, store, sid := seedBaseline(t, prefs, "m-beta", "")

	if got := bs.GetBaselineModel(); got != "m-gamma" {
		t.Errorf("baseline = %q, want m-gamma (workspace preference)", got)
	}
	if got := persistedBaseline(t, store, sid); got != "m-gamma" {
		t.Errorf("persisted baseline = %q, want m-gamma", got)
	}
}

func TestNamedPromptInitialModel_NothingMatchesKeepsAgentDefault(t *testing.T) {
	sm := newPromptPrefManager([]config.PromptPreferredModel{{ModelTag: "no-such-tag"}})

	prefs := sm.resolveInitialModelPreference("Unmatched prompt", "/work", nil)
	bs, _, _ := seedBaseline(t, prefs, "m-beta", "")

	if got := bs.GetBaselineModel(); got != "m-beta" {
		t.Errorf("baseline = %q, want agent default m-beta", got)
	}
}

func TestNamedPromptInitialModel_ExistingSessionKeepsPersistedBaseline(t *testing.T) {
	sm := newPromptPrefManager([]config.PromptPreferredModel{{ModelTag: "bh-deep"}})
	prefs := sm.resolveInitialModelPreference("Deep prompt", "/work", nil)

	bs, store, sid := seedBaseline(t, prefs, "m-beta", "m-gamma")

	if got := bs.GetBaselineModel(); got != "m-gamma" {
		t.Errorf("baseline = %q, want persisted m-gamma", got)
	}
	if got := persistedBaseline(t, store, sid); got != "m-gamma" {
		t.Errorf("persisted baseline = %q, want m-gamma (unchanged)", got)
	}
}
