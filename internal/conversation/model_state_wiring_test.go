package conversation

import (
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
	bs.onAgentModels = func(_ string, m []ModelInfo) error {
		*recorded = append([]ModelInfo(nil), m...)
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
