package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestReconcileHiddenModelPolicyForDispatch_ConfirmsAndPersistsReplacement(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	recorder := session.NewRecorder(store)
	if err := recorder.Start("policy-reconcile", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	var policies config.HiddenModelPolicies
	snapshot, err := policies.Prepare([]config.ACPServer{{Name: "srv", HiddenModels: []string{"^old$"}}})
	if err != nil || !policies.Publish(snapshot) {
		t.Fatalf("publish policy: %v", err)
	}
	shared := newFakeSharedProcess()
	bs := &BackgroundSession{
		ctx: context.Background(), store: store, persistedID: recorder.SessionID(),
		sharedProcess: shared, acpID: "acp-policy", acpServer: "srv",
		hiddenModelPolicies: &policies,
		mittoConfig:         &config.Config{ACPServers: []config.ACPServer{{Name: "srv"}}},
		baselineModel:       "old",
		agentModels: &SessionModelState{CurrentModelId: "old", AvailableModels: []ModelInfo{
			{ModelId: "old", Name: "Old"}, {ModelId: "new", Name: "New"},
		}},
		configOptions: []SessionConfigOption{{ID: "model", Category: ConfigOptionCategoryModel, Options: []SessionConfigOptionValue{
			{Value: "old", Name: "Old"}, {Value: "new", Name: "New"},
		}}},
	}
	if err := bs.configMgr.reconcileHiddenModelPolicyForDispatch(bs, context.Background()); err != nil {
		t.Fatalf("reconcileHiddenModelPolicyForDispatch() error = %v", err)
	}
	if got := bs.cmGetCurrentModelID(); got != "new" {
		t.Fatalf("current model = %q, want new", got)
	}
	if got := bs.GetBaselineModel(); got != "new" {
		t.Fatalf("baseline = %q, want new", got)
	}
	meta, err := store.GetMetadata(recorder.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if meta.BaselineModel != "new" {
		t.Fatalf("persisted baseline = %q, want new", meta.BaselineModel)
	}
}

func TestFinalPolicyAdmissionBlocksLiveHiddenActiveModel(t *testing.T) {
	var policies config.HiddenModelPolicies
	snapshot, err := policies.Prepare([]config.ACPServer{{Name: "srv", HiddenModels: []string{"^old$"}}})
	if err != nil || !policies.Publish(snapshot) {
		t.Fatalf("publish policy: %v", err)
	}
	bs := &BackgroundSession{
		acpServer:           "srv",
		hiddenModelPolicies: &policies,
		baselineModel:       "new",
		agentModels: &SessionModelState{CurrentModelId: "old", AvailableModels: []ModelInfo{
			{ModelId: "old", Name: "Old"}, {ModelId: "new", Name: "New"},
		}},
	}
	if err := bs.finalPolicyAdmission(bs); !errors.Is(err, errModelHiddenByPolicy) {
		t.Fatalf("finalPolicyAdmission() error = %v, want hidden-model refusal", err)
	}
}
