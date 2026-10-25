package conversation

import (
	"context"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/config"
)

func wiringSession(hidden []string, recorded *[]ModelInfo) *BackgroundSession {
	bs := &BackgroundSession{
		acpServer: "srv",
		mittoConfig: &config.Config{ACPServers: []config.ACPServer{
			{Name: "srv", Command: "x", HiddenModels: hidden},
		}},
	}
	bs.onAgentModels = func(obs ModelCatalogObservation) error {
		*recorded = append([]ModelInfo(nil), obs.Models...)
		return nil
	}
	return bs
}

func wiringResp() *acp.SessionModelState {
	return &acp.SessionModelState{
		CurrentModelId: "a",
		AvailableModels: []acp.ModelInfo{
			{ModelId: "a", Name: "A"}, {ModelId: "b", Name: "B"},
		},
	}
}

func TestDeriveSelectableModels_RawRecordedBeforeFiltering(t *testing.T) {
	var rec []ModelInfo
	bs := wiringSession([]string{"^b$"}, &rec)
	models, _, _ := bs.deriveSelectableModels(nil, wiringResp())
	if len(rec) != 2 {
		t.Fatalf("recorder got %d models, want raw 2", len(rec))
	}
	if models == nil || len(models.AvailableModels) != 1 || models.AvailableModels[0].ModelId != "a" {
		t.Fatalf("selectable = %+v, want only a", models)
	}
}

func TestDeriveSelectableModels_PolicyEmptyNoSynthesis(t *testing.T) {
	var rec []ModelInfo
	bs := wiringSession([]string{".*"}, &rec)
	models, _, _ := bs.deriveSelectableModels(nil, wiringResp())
	if models == nil || len(models.AvailableModels) != 0 || models.Synthesized {
		t.Fatalf("policy-empty must be non-nil empty, got %+v", models)
	}
	if len(rec) != 2 {
		t.Fatalf("raw catalog must still be recorded, got %d", len(rec))
	}
}

func TestDeriveSelectableModels_BadPolicyFailsClosed(t *testing.T) {
	var rec []ModelInfo
	bs := wiringSession([]string{"("}, &rec)
	models, _, _ := bs.deriveSelectableModels(nil, wiringResp())
	if models == nil || len(models.AvailableModels) != 0 {
		t.Fatalf("bad policy must publish no choices, got %+v", models)
	}
	if len(rec) != 2 {
		t.Fatalf("raw catalog must still be recorded, got %d", len(rec))
	}
}

func TestSelectableConfigOptions_FiltersAndFailsClosed(t *testing.T) {
	opts := []SessionConfigOption{{
		ID: "model", Type: ConfigOptionTypeSelect, Category: ConfigOptionCategoryModel, CurrentValue: "a",
		Options: []SessionConfigOptionValue{{Value: "a", Name: "A"}, {Value: "b", Name: "B"}},
	}}
	var rec []ModelInfo
	got := wiringSession([]string{"^b$"}, &rec).selectableConfigOptions(opts, "model")
	if len(got) != 1 || len(got[0].Options) != 1 || got[0].Options[0].Value != "a" {
		t.Fatalf("filtered = %+v", got)
	}
	if len(opts[0].Options) != 2 {
		t.Fatal("input mutated")
	}
	bad := wiringSession([]string{"("}, &rec).selectableConfigOptions(opts, "model")
	for _, o := range bad {
		if len(o.Options) != 0 {
			t.Fatalf("bad policy leaked choices: %+v", o)
		}
	}
}

func TestCbClearModelConfigOption_ClearsOnlyModel(t *testing.T) {
	bs := &BackgroundSession{configOptions: []SessionConfigOption{
		{ID: "model", Category: ConfigOptionCategoryModel},
		{ID: "mode", Category: "mode"},
	}}
	bs.cbClearModelConfigOption()
	if len(bs.configOptions) != 1 || bs.configOptions[0].ID != "mode" {
		t.Fatalf("got %+v", bs.configOptions)
	}
}

func TestFilterModelInfos_PolicyAndFailClosed(t *testing.T) {
	in := []ModelInfo{{ModelId: "a", Name: "A"}, {ModelId: "b", Name: "B"}}
	p, _ := config.CompileHiddenModels("srv", []string{"^b$"})
	if got := filterModelInfos(p, in); len(got) != 1 || got[0].ModelId != "a" {
		t.Fatalf("got %+v", got)
	}
	if got := filterModelInfos(hideAllModelsPolicy(), in); got == nil || len(got) != 0 {
		t.Fatalf("hide-all got %+v", got)
	}
}

type fakeSharedWithSetter struct {
	SharedProcess
	snap []SessionConfigOption
}

func (f fakeSharedWithSetter) SetSessionConfigOption(ctx context.Context, sid, id, value string) ([]SessionConfigOption, error) {
	return f.snap, nil
}

func TestCmSetSessionConfigOption_FiltersReplacementSnapshot(t *testing.T) {
	snap := []SessionConfigOption{
		{ID: "model", Type: ConfigOptionTypeSelect, Category: ConfigOptionCategoryModel, CurrentValue: "b",
			Options: []SessionConfigOptionValue{{Value: "a", Name: "A"}, {Value: "b", Name: "B"}}},
		{ID: "effort", Type: ConfigOptionTypeSelect, CurrentValue: "hi",
			Options: []SessionConfigOptionValue{{Value: "hi"}, {Value: "lo"}}},
	}
	var rec []ModelInfo
	bs := wiringSession([]string{"^b$"}, &rec)
	bs.sharedProcess = fakeSharedWithSetter{snap: snap}
	got, err := bs.cmSetSessionConfigOption(context.Background(), "effort", "lo")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if len(got[0].Options) != 1 || got[0].Options[0].Value != "a" || got[0].CurrentValue != "b" {
		t.Fatalf("model not filtered/truthful: %+v", got[0])
	}
	if len(got[1].Options) != 2 {
		t.Fatalf("non-model changed: %+v", got[1])
	}

	bs2 := wiringSession(nil, &rec)
	bs2.sharedProcess = fakeSharedWithSetter{snap: snap}
	got, _ = bs2.cmSetSessionConfigOption(context.Background(), "effort", "lo")
	if len(got[0].Options) != 2 {
		t.Fatalf("no-rule changed: %+v", got[0])
	}
}

func TestRefilterModelPolicy_HideThenUnhideWithoutRPC(t *testing.T) {
	var rec []ModelInfo
	calls := 0
	bs := wiringSession([]string{"^b$"}, &rec)
	bs.onAgentModels = func(ModelCatalogObservation) error { calls++; return nil }
	models, _, _ := bs.deriveSelectableModels(nil, wiringResp())
	bs.cbStoreAgentModels(models)
	if calls != 1 || len(bs.AgentModels().AvailableModels) != 1 {
		t.Fatalf("setup: calls=%d models=%v", calls, bs.AgentModels())
	}
	// bs has no shared process, so any RPC attempt would panic/fail.
	bs.mittoConfig.ACPServers[0].HiddenModels = nil
	bs.RefilterModelPolicy()
	if got := bs.AgentModels().AvailableModels; len(got) != 2 {
		t.Fatalf("unhide: got %v, want a and b", got)
	}
	if calls != 1 {
		t.Fatalf("raw catalog re-recorded: calls=%d", calls)
	}
	bs.mittoConfig.ACPServers[0].HiddenModels = []string{".*"}
	bs.RefilterModelPolicy()
	if len(bs.AgentModels().AvailableModels) != 0 {
		t.Fatalf("hide-all should leave none")
	}
	for _, o := range bs.configOptions {
		if o.Category == ConfigOptionCategoryModel {
			t.Fatalf("stale model option: %v", o)
		}
	}
	bs.mittoConfig.ACPServers[0].HiddenModels = nil
	bs.RefilterModelPolicy()
	if len(bs.AgentModels().AvailableModels) != 2 {
		t.Fatalf("restore failed")
	}
}

func TestRefilterModelPolicy_GenericOptionsHideThenUnhideWithoutRPC(t *testing.T) {
	var rec []ModelInfo
	bs := wiringSession([]string{"^b$"}, &rec)
	bs.modelConfigId = "pick"
	raw := []SessionConfigOption{
		{ID: "temp", Name: "Temp", Type: ConfigOptionTypeSelect, CurrentValue: "lo",
			Options: []SessionConfigOptionValue{{Value: "lo", Name: "b"}, {Value: "hi", Name: "hi"}}},
		{ID: "pick", Name: "Pick", Type: ConfigOptionTypeSelect, CurrentValue: "b",
			Options: []SessionConfigOptionValue{{Value: "a", Name: "A", Group: "g1"}, {Value: "b", Name: "B", Group: "g1"}}},
	}
	bs.setConfigOptionsSnapshot(raw)
	find := func(id string) *SessionConfigOption {
		bs.configMu.Lock()
		defer bs.configMu.Unlock()
		for _, o := range bs.configOptions {
			if o.ID == id {
				c := cloneConfigOption(o)
				return &c
			}
		}
		return nil
	}
	p := find("pick")
	if p == nil || len(p.Options) != 1 || p.Options[0].Value != "a" || p.CurrentValue != "b" {
		t.Fatalf("hide setup: %+v", p)
	}
	if tmp := find("temp"); tmp == nil || len(tmp.Options) != 2 {
		t.Fatalf("non-model option altered: %+v", tmp)
	}
	obs := len(bs.rawGenericOpts)
	bs.RefilterModelPolicy()
	bs.RefilterModelPolicy()
	if p = find("pick"); len(p.Options) != 1 {
		t.Fatalf("repeat refilter: %+v", p)
	}
	bs.mittoConfig.ACPServers[0].HiddenModels = nil
	bs.RefilterModelPolicy()
	p = find("pick")
	if len(p.Options) != 2 || p.Options[1].Value != "b" || p.Options[1].Group != "g1" || p.CurrentValue != "b" {
		t.Fatalf("unhide: %+v", p)
	}
	if tmp := find("temp"); len(tmp.Options) != 2 || tmp.Options[0].Name != "b" {
		t.Fatalf("non-model option: %+v", tmp)
	}
	if len(raw[1].Options) != 2 || len(bs.rawGenericOpts) != obs {
		t.Fatalf("raw mutated")
	}
	bs.mittoConfig.ACPServers[0].HiddenModels = []string{"^b$"}
	bs.RefilterModelPolicy()
	if p = find("pick"); len(p.Options) != 1 {
		t.Fatalf("rehide: %+v", p)
	}
}

func captureObs(bs *BackgroundSession) *[]ModelCatalogObservation {
	var got []ModelCatalogObservation
	bs.acpServer = "srv"
	bs.workingDir = "/w"
	bs.onAgentModels = func(o ModelCatalogObservation) error { got = append(got, o); return nil }
	return &got
}

func TestU3_ConfigUpdateObservations(t *testing.T) {
	bs := &BackgroundSession{modelConfigId: "model"}
	got := captureObs(bs)
	bs.recordRawConfigOptionCatalog([]SessionConfigOption{{ID: "mode", Category: "mode"}})
	if len(*got) != 0 {
		t.Fatalf("no model option must be a no-op")
	}
	bs.recordRawConfigOptionCatalog([]SessionConfigOption{{ID: "model", Category: "model",
		Options: []SessionConfigOptionValue{{Value: "a", Name: "A"}, {Value: "b", Name: "B"}}}})
	if len(*got) != 1 || !(*got)[0].Complete || len((*got)[0].Models) != 2 ||
		(*got)[0].Server != "srv" || (*got)[0].WorkingDir != "/w" || (*got)[0].Generation != 0 {
		t.Fatalf("bad obs %+v", *got)
	}
	bs.recordRawConfigOptionCatalog([]SessionConfigOption{{ID: "model", Category: "model"}})
	if len(*got) != 2 || !(*got)[1].Complete || (*got)[1].Models == nil || len((*got)[1].Models) != 0 {
		t.Fatalf("complete-empty not recorded: %+v", *got)
	}
}

func TestU3_SyntheticAndNilNotObservations(t *testing.T) {
	bs := &BackgroundSession{}
	got := captureObs(bs)
	bs.recordRawAgentCatalog(nil)
	bs.recordRawAgentCatalog(&SessionModelState{Synthesized: true, AvailableModels: []ModelInfo{{ModelId: "x"}}})
	bs.recordRawAgentCatalog(&SessionModelState{Synthesized: true})
	if len(*got) != 0 {
		t.Fatalf("got %+v", *got)
	}
	bs.recordRawAgentCatalog(&SessionModelState{})
	if len(*got) != 1 || !(*got)[0].Complete || len((*got)[0].Models) != 0 {
		t.Fatalf("authoritative empty must record: %+v", *got)
	}
}

// TestHsApplyAgentModels_RetainsRawCatalogForRefilter covers the shared-process
// resume path: the handshake delivers an already-derived catalog (not ACP
// config options), so the unfiltered state must be retained for
// RefilterModelPolicy to be able to undo a policy that has since changed.
func TestHsApplyAgentModels_RetainsRawCatalogForRefilter(t *testing.T) {
	var rec []ModelInfo
	bs := wiringSession([]string{".*"}, &rec)
	bs.ctx = context.Background()
	raw := &SessionModelState{
		CurrentModelId:  "a",
		AvailableModels: []ModelInfo{{ModelId: "a", Name: "A"}, {ModelId: "b", Name: "B"}},
	}
	bs.hsApplyAgentModels(raw)
	bs.startupConstraintWG.Wait()
	if got := bs.AgentModels(); got == nil || len(got.AvailableModels) != 0 {
		t.Fatalf("hide-all setup: %+v", got)
	}
	if bs.rawCatalogState == nil {
		t.Fatal("shared handshake did not retain the raw catalog")
	}
	bs.mittoConfig.ACPServers[0].HiddenModels = nil
	bs.RefilterModelPolicy()
	if got := bs.AgentModels().AvailableModels; len(got) != 2 {
		t.Fatalf("refilter after unhide: %v, want a and b", got)
	}
	if len(raw.AvailableModels) != 2 {
		t.Fatal("retained raw state was mutated")
	}
}

// TestRefilterModelPolicyForServers_ScopesToRequestedServers verifies the
// settings-save entry point only touches sessions of the servers whose rules
// changed.
func TestRefilterModelPolicyForServers_ScopesToRequestedServers(t *testing.T) {
	raw := &SessionModelState{
		CurrentModelId:  "a",
		AvailableModels: []ModelInfo{{ModelId: "a", Name: "A"}},
	}
	var rec []ModelInfo
	target := wiringSession([]string{".*"}, &rec)
	target.persistedID = "s1"
	target.retainRawModelState(raw)
	target.cbStoreAgentModels(filterModelState(hideAllModelsPolicy(), raw))

	untouched := wiringSession([]string{".*"}, &rec)
	untouched.acpServer = "other"
	untouched.persistedID = "s2"
	untouched.retainRawModelState(raw)
	untouched.cbStoreAgentModels(filterModelState(hideAllModelsPolicy(), raw))

	sm := &SessionManager{sessions: map[string]*BackgroundSession{"s1": target, "s2": untouched}}
	target.mittoConfig.ACPServers[0].HiddenModels = nil

	if n := sm.RefilterModelPolicyForServers([]string{"srv"}); n != 1 {
		t.Fatalf("refiltered = %d, want 1", n)
	}
	if got := target.AgentModels().AvailableModels; len(got) != 1 {
		t.Fatalf("target not refiltered: %v", got)
	}
	if got := untouched.AgentModels().AvailableModels; len(got) != 0 {
		t.Fatalf("unrequested server was refiltered: %v", got)
	}
	if n := sm.RefilterModelPolicyForServers(nil); n != 0 {
		t.Fatalf("nil servers refiltered %d sessions", n)
	}
}
