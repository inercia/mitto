package conversation

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func publishViewPolicy(t *testing.T, policies *config.HiddenModelPolicies, rules ...string) {
	t.Helper()
	snapshot, err := policies.Prepare([]config.ACPServer{{Name: "one", HiddenModels: rules}, {Name: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if !policies.Publish(snapshot) {
		t.Fatal("publication rejected")
	}
}

func policyViewSession(policies *config.HiddenModelPolicies, server string) *BackgroundSession {
	bs := &BackgroundSession{acpServer: server, hiddenModelPolicies: policies, mittoConfig: &config.Config{}, baselineModel: "b"}
	bs.retainRawModelState(&SessionModelState{CurrentModelId: "a", AvailableModels: []ModelInfo{{ModelId: "a", Name: "Alpha"}, {ModelId: "b", Name: "Beta"}}})
	bs.cbStoreAgentModels(bs.rawCatalogState)
	bs.RefilterModelPolicy()
	return bs
}

func assertPolicyView(t *testing.T, bs *BackgroundSession, want int) {
	t.Helper()
	if got := len(bs.AgentModels().AvailableModels); got != want {
		t.Fatalf("models = %d, want %d", got, want)
	}
	count := 0
	for _, opt := range bs.ConfigOptions() {
		if opt.Category == ConfigOptionCategoryModel {
			count += len(opt.Options)
		}
	}
	if count != want {
		t.Fatalf("reconnect options = %d, want %d", count, want)
	}
}

func TestPolicyPublicationLiveViews(t *testing.T) {
	var policies config.HiddenModelPolicies
	publishViewPolicy(t, &policies)
	one, two := policyViewSession(&policies, "one"), policyViewSession(&policies, "two")
	observations := 0
	one.onAgentModels = func(ModelCatalogObservation) error { observations++; return nil }
	one.agentModels.CurrentModelId = "b" // confirmed execution identity newer than retained raw
	gen := one.cbConfigSnapshotGeneration()
	publishViewPolicy(t, &policies, "^b$")
	sm := &SessionManager{sessions: map[string]*BackgroundSession{"one": one, "two": two}}
	if n := sm.RefilterModelPolicyForServers([]string{"one"}); n != 1 {
		t.Fatalf("live manager refreshed %d sessions, want 1", n)
	}
	assertPolicyView(t, one, 1)
	assertPolicyView(t, two, 2)
	if one.AgentModels().CurrentModelId != "b" || one.GetBaselineModel() != "b" {
		t.Fatal("policy refresh switched execution or baseline")
	}
	publishViewPolicy(t, &policies, ".*")
	one.RefilterModelPolicy()
	assertPolicyView(t, one, 0)
	publishViewPolicy(t, &policies)
	one.RefilterModelPolicy()
	assertPolicyView(t, one, 2)
	if observations != 0 || one.cbConfigSnapshotGeneration() != gen {
		t.Fatal("policy replay manufactured an observation or ACP snapshot")
	}
	if len(one.rawCatalogState.AvailableModels) != 2 {
		t.Fatal("raw catalog was filtered")
	}
	// Mutable config must not become a second production policy authority.
	one.mittoConfig.ACPServers = []config.ACPServer{{Name: "one", HiddenModels: []string{".*"}}}
	one.RefilterModelPolicy()
	assertPolicyView(t, one, 2)
}

func TestPolicyPublicationDelayedModelCallbacks(t *testing.T) {
	var policies config.HiddenModelPolicies
	publishViewPolicy(t, &policies)
	bs := policyViewSession(&policies, "one")
	stale := bs.AgentModels()
	bs.agentModels.CurrentModelId = "b"
	publishViewPolicy(t, &policies, "^b$")
	bs.RefilterModelPolicy()
	bs.cbStoreAgentModels(stale)
	bs.cbReplaceModelConfigOption(SessionConfigOption{Category: ConfigOptionCategoryModel, Options: ModelsToConfigOptions(stale)})
	assertPolicyView(t, bs, 1)
	if bs.AgentModels().CurrentModelId != "b" {
		t.Fatal("delayed catalog callback rolled back acknowledged current identity")
	}
	publishViewPolicy(t, &policies)
	bs.RefilterModelPolicy()
	bs.cbClearModelConfigOption() // delayed all-hidden callback must not erase restored options
	assertPolicyView(t, bs, 2)
}

func TestPolicyPublicationGenericReplacement(t *testing.T) {
	var policies config.HiddenModelPolicies
	publishViewPolicy(t, &policies, "^b$")
	bs := policyViewSession(&policies, "one")
	raw := []SessionConfigOption{{ID: "effort", CurrentValue: "low", Options: []SessionConfigOptionValue{{Value: "low"}}}, {ID: "model", Category: ConfigOptionCategoryModel, Options: []SessionConfigOptionValue{{Value: "a", Name: "Alpha"}, {Value: "b", Name: "Beta"}}}}
	bs.setConfigOptionsSnapshot(raw)
	updates := 0
	bs.onConfigChanged = func(_, _, _ string) {
		updates++
		// A callback can publish and refilter: no policy/catalog lock is held.
		publishViewPolicy(t, &policies, "^b$")
		bs.RefilterModelPolicy()
	}
	bs.onConfigOptionsUpdate(raw)
	if updates != 1 {
		t.Fatalf("update broadcasts = %d, want 1", updates)
	}
	// A fresh RPC replacement must not be replaced by a prior retained snapshot.
	raw[0].CurrentValue = "high"
	bs.cbReplaceGenericConfigOptions(raw)
	gen := bs.cbConfigSnapshotGeneration()
	publishViewPolicy(t, &policies)
	bs.RefilterModelPolicy()
	for _, option := range bs.ConfigOptions() {
		if option.ID == "effort" && option.CurrentValue != "high" {
			t.Fatal("fresh RPC options overwritten by retained data")
		}
	}
	assertPolicyView(t, bs, 2)
	if bs.cbConfigSnapshotGeneration() != gen {
		t.Fatal("policy replay advanced RPC snapshot generation")
	}
}

func TestPolicyPublicationManagerConfiguration(t *testing.T) {
	sm := NewSessionManager("test", "one", false, nil)
	valid := &config.Config{ACPServers: []config.ACPServer{{Name: "one", HiddenModels: []string{"^b$"}}}}
	sm.SetMittoConfig(valid)
	policies := sm.HiddenModelPolicies()
	accepted := policies.Snapshot()
	if !accepted.Policy("one").Hides("b", "Beta") {
		t.Fatal("initial policy not published")
	}
	invalid := &config.Config{ACPServers: []config.ACPServer{{Name: "one", HiddenModels: []string{"("}}}}
	sm.SetMittoConfig(invalid)
	// The compiled policy stays on the last accepted snapshot (runtime state),
	// but the current configuration remains visible so consumers can fail closed
	// on rules that do not compile.
	if policies.Snapshot() != accepted || sm.mittoConfig != invalid {
		t.Fatal("invalid configuration must retain accepted policy while exposing current config")
	}
	if sm.mittoConfig.ACPServers[0].HiddenModels[0] != "(" {
		t.Fatal("current invalid rules were dropped")
	}
	sm.SetMittoConfig(nil)
	if policies.Snapshot().Version() <= accepted.Version() || policies.Snapshot().Policy("one") != nil {
		t.Fatal("removing configuration retained stale rules")
	}
}
