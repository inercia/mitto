package conversation

import (
	"reflect"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/modelcache"
)

func hidePolicy(t *testing.T, rules ...string) *config.HiddenModelPolicy {
	t.Helper()
	p, err := config.CompileHiddenModels("srv", rules)
	if err != nil {
		t.Fatalf("CompileHiddenModels: %v", err)
	}
	return p
}

func acpModelState(cur string, ids ...string) *acp.SessionModelState {
	s := &acp.SessionModelState{CurrentModelId: acp.ModelId(cur)}
	for _, id := range ids {
		s.AvailableModels = append(s.AvailableModels, acp.ModelInfo{ModelId: acp.ModelId(id), Name: "Name " + id})
	}
	return s
}

func modelIDs(m *SessionModelState) []string {
	out := []string{}
	for _, mi := range m.AvailableModels {
		out = append(out, mi.ModelId)
	}
	return out
}

func TestDeriveAgentModelsFiltered_States(t *testing.T) {
	profiles := []config.ModelProfile{{Name: "ProfA"}, {Name: "ProfB"}}

	t.Run("available keeps hidden current id truthful", func(t *testing.T) {
		m, _, src, st := DeriveAgentModelsFiltered(hidePolicy(t, "m1"), nil, acpModelState("m1", "m1", "m2"), profiles)
		if st != modelcache.CatalogAvailable || src != ModelCatalogSourceACPModels {
			t.Fatalf("state=%q src=%q", st, src)
		}
		if got := modelIDs(m); !reflect.DeepEqual(got, []string{"m2"}) {
			t.Fatalf("ids=%v", got)
		}
		if m.CurrentModelId != "m1" {
			t.Fatalf("CurrentModelId=%q", m.CurrentModelId)
		}
	})

	t.Run("all hidden real catalog is policy_empty without synthesis", func(t *testing.T) {
		m, _, src, st := DeriveAgentModelsFiltered(hidePolicy(t, "m*"), nil, acpModelState("m1", "m1", "m2"), profiles)
		if st != modelcache.CatalogPolicyEmpty || src != ModelCatalogSourceACPModels {
			t.Fatalf("state=%q src=%q", st, src)
		}
		if m == nil || m.AvailableModels == nil || len(m.AvailableModels) != 0 || m.Synthesized {
			t.Fatalf("unexpected state %+v", m)
		}
	})

	t.Run("absent catalog synthesizes profiles filtered by policy", func(t *testing.T) {
		m, _, src, st := DeriveAgentModelsFiltered(hidePolicy(t, "ProfA"), nil, nil, profiles)
		if st != modelcache.CatalogAvailable || src != ModelCatalogSourceLocalProfileFallback || !m.Synthesized {
			t.Fatalf("state=%q src=%q m=%+v", st, src, m)
		}
		if len(m.AvailableModels) != 1 || m.AvailableModels[0].Name != "ProfB" {
			t.Fatalf("models=%+v", m.AvailableModels)
		}
	})

	t.Run("all synthesized profiles hidden", func(t *testing.T) {
		m, _, _, st := DeriveAgentModelsFiltered(hidePolicy(t, "Prof*"), nil, nil, profiles)
		if st != modelcache.CatalogPolicyEmpty || m == nil || len(m.AvailableModels) != 0 {
			t.Fatalf("state=%q m=%+v", st, m)
		}
	})

	t.Run("absent without profiles", func(t *testing.T) {
		m, _, _, st := DeriveAgentModelsFiltered(hidePolicy(t, "x"), nil, nil, nil)
		if m != nil || st != modelcache.CatalogAbsent {
			t.Fatalf("state=%q m=%+v", st, m)
		}
	})

	t.Run("trustworthy empty acp models without profiles", func(t *testing.T) {
		m, _, _, st := DeriveAgentModelsFiltered(nil, nil, &acp.SessionModelState{}, nil)
		if m == nil || m.AvailableModels == nil || len(m.AvailableModels) != 0 || st != modelcache.CatalogEmpty {
			t.Fatalf("state=%q m=%+v", st, m)
		}
	})

	t.Run("trustworthy empty acp models with profiles does not synthesize", func(t *testing.T) {
		resp := &acp.SessionModelState{CurrentModelId: "cur"}
		m, cfgId, src, st := DeriveAgentModelsFiltered(hidePolicy(t, "Prof*"), nil, resp, profiles)
		if st != modelcache.CatalogEmpty || src != ModelCatalogSourceACPModelsEmpty || cfgId != "" {
			t.Fatalf("state=%q src=%q cfg=%q", st, src, cfgId)
		}
		if m == nil || m.AvailableModels == nil || len(m.AvailableModels) != 0 || m.Synthesized || m.CurrentModelId != "cur" {
			t.Fatalf("unexpected state %+v", m)
		}
	})

	t.Run("nil policy keeps everything", func(t *testing.T) {
		m, _, _, st := DeriveAgentModelsFiltered(nil, nil, acpModelState("m1", "m1", "m2"), nil)
		if st != modelcache.CatalogAvailable || len(m.AvailableModels) != 2 {
			t.Fatalf("state=%q m=%+v", st, m)
		}
	})
}

func TestDeriveAgentModelsFiltered_ConfigOptionsAllHidden(t *testing.T) {
	cat := acp.SessionConfigOptionCategoryModel
	opts := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
		Id:           acp.SessionConfigId("agent-model"),
		Name:         "Model",
		Category:     &cat,
		CurrentValue: acp.SessionConfigValueId("c1"),
		Options: acp.SessionConfigSelectOptions{Ungrouped: &acp.SessionConfigSelectOptionsUngrouped{
			{Name: "C1", Value: acp.SessionConfigValueId("c1")},
		}},
	}}}
	m, id, src, st := DeriveAgentModelsFiltered(hidePolicy(t, "c1"), opts, nil, []config.ModelProfile{{Name: "P"}})
	if st != modelcache.CatalogPolicyEmpty || src != ModelCatalogSourceConfigOptions || id != "agent-model" {
		t.Fatalf("state=%q src=%q id=%q", st, src, id)
	}
	if m.CurrentModelId != "c1" || len(m.AvailableModels) != 0 || m.Synthesized {
		t.Fatalf("m=%+v", m)
	}
}

func cfgOpt(id, category string, vals ...SessionConfigOptionValue) SessionConfigOption {
	return SessionConfigOption{
		ID: id, Name: "N " + id, Type: ConfigOptionTypeSelect, Category: category,
		CurrentValue: "hidden-x", Description: "d", Options: vals,
	}
}

func TestFilterConfigOptions(t *testing.T) {
	desc := "desc"
	grouped := cfgOpt("model", "model",
		SessionConfigOptionValue{Value: "a", Name: "A", Group: "g1", GroupName: "G1", Description: desc},
		SessionConfigOptionValue{Value: "hide1", Name: "H1", Group: "g1", GroupName: "G1"},
		SessionConfigOptionValue{Value: "hide2", Name: "H2", Group: "g2", GroupName: "G2"},
		SessionConfigOptionValue{Value: "hide3", Name: "H3", Group: "g2", GroupName: "G2"},
		SessionConfigOptionValue{Value: "b", Name: "B", Group: "g3", GroupName: "G3"},
	)
	other := cfgOpt("thought_level", "thought_level", SessionConfigOptionValue{Value: "hide1", Name: "hide1"})
	policy := hidePolicy(t, "hide*")

	in := []SessionConfigOption{grouped, other}
	snapshot := []SessionConfigOption{grouped, other}
	out := FilterConfigOptions(policy, in, "")

	if !reflect.DeepEqual(in, snapshot) {
		t.Fatal("input mutated")
	}
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	want := []SessionConfigOptionValue{
		{Value: "a", Name: "A", Group: "g1", GroupName: "G1", Description: desc},
		{Value: "b", Name: "B", Group: "g3", GroupName: "G3"},
	}
	if !reflect.DeepEqual(out[0].Options, want) {
		t.Fatalf("options=%+v", out[0].Options)
	}
	if out[0].CurrentValue != "hidden-x" || out[0].Description != "d" || out[0].Name != "N model" {
		t.Fatalf("metadata changed: %+v", out[0])
	}
	if !reflect.DeepEqual(out[1], other) {
		t.Fatalf("non-model option changed: %+v", out[1])
	}
}

func TestFilterConfigOptions_Identification(t *testing.T) {
	policy := hidePolicy(t, "x")
	vals := func() []SessionConfigOptionValue {
		return []SessionConfigOptionValue{{Value: "x", Name: "x"}, {Value: "y", Name: "y"}}
	}

	t.Run("default id without category", func(t *testing.T) {
		out := FilterConfigOptions(policy, []SessionConfigOption{cfgOpt("model", "", vals()...)}, "")
		if len(out[0].Options) != 1 {
			t.Fatalf("%+v", out[0].Options)
		}
	})
	t.Run("custom model config id", func(t *testing.T) {
		out := FilterConfigOptions(policy, []SessionConfigOption{cfgOpt("custom-model", "", vals()...)}, "custom-model")
		if len(out[0].Options) != 1 {
			t.Fatalf("%+v", out[0].Options)
		}
	})
	t.Run("default id ignored when another id resolved", func(t *testing.T) {
		out := FilterConfigOptions(policy, []SessionConfigOption{cfgOpt("model", "", vals()...)}, "custom-model")
		if len(out[0].Options) != 2 {
			t.Fatalf("%+v", out[0].Options)
		}
	})
	t.Run("all hidden gives non-nil empty", func(t *testing.T) {
		out := FilterConfigOptions(policy, []SessionConfigOption{cfgOpt("m", "model", SessionConfigOptionValue{Value: "x", Name: "x"})}, "")
		if out[0].Options == nil || len(out[0].Options) != 0 {
			t.Fatalf("%#v", out[0].Options)
		}
	})
	t.Run("nil policy keeps all", func(t *testing.T) {
		out := FilterConfigOptions(nil, []SessionConfigOption{cfgOpt("model", "model", vals()...)}, "")
		if len(out[0].Options) != 2 {
			t.Fatalf("%+v", out[0].Options)
		}
	})
	t.Run("nil input", func(t *testing.T) {
		if FilterConfigOptions(policy, nil, "") != nil {
			t.Fatal("expected nil")
		}
	})
}
