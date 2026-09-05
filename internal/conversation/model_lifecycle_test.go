package conversation

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// Record the session addressed by every switch, not just the optimistic UI value.
type lifecycleModelProcess struct {
	*fakeSharedProcess
	modelMu sync.Mutex
	calls   [][2]string
}

func TestModelLifecycle_StartupFailureRetainsConversationChoice(t *testing.T) {
	for _, tc := range []struct {
		name, baseline string
		rpcErr         error
		wantRPC        bool
	}{
		{name: "unavailable persisted model", baseline: "removed-model"},
		{name: "agent rejects switch", baseline: "m-2", rpcErr: errors.New("agent refused model"), wantRPC: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeConfigDeps()
			d.baselineModel = tc.baseline
			d.currentModelID = "m-1"
			d.setModelErr = tc.rpcErr
			d.constraint = map[string]*config.ACPServerConstraint{
				ConfigOptionCategoryModel: {Pattern: "Model 1", MatchMode: "exact"},
			}
			if err := (configManager{}).applyConfigConstraints(d, ConfigOptionCategoryModel); err == nil {
				t.Fatal("failed restore must keep startup gated, not silently use the default")
			}
			if d.baselineModel != tc.baseline || d.currentModelID != "m-1" || len(d.persistedBaseline) != 0 {
				t.Fatalf("failure changed model state: baseline=%q active=%q persisted=%v", d.baselineModel, d.currentModelID, d.persistedBaseline)
			}
			if (len(d.modelRPCCalls) > 0) != tc.wantRPC {
				t.Fatalf("unexpected model RPCs: %v", d.modelRPCCalls)
			}
		})
	}
}

func (p *lifecycleModelProcess) SetSessionModel(_ context.Context, id acp.SessionId, model string) error {
	p.modelMu.Lock()
	defer p.modelMu.Unlock()
	p.calls = append(p.calls, [2]string{string(id), model})
	return nil
}

func lifecycleModels() *SessionModelState {
	return &SessionModelState{CurrentModelId: "default", AvailableModels: []ModelInfo{
		{ModelId: "default", Name: "Default"},
		{ModelId: "initial", Name: "Initial"},
		{ModelId: "manual", Name: "Manual"},
	}}
}

func TestModelLifecycle_InitializeOnce(t *testing.T) {
	for _, tc := range []struct {
		name, persisted, initial, constraint, want string
	}{
		{name: "agent default", want: "default"},
		{name: "initial preference", initial: "Initial", want: "initial"},
		{name: "legacy ACP default", constraint: "Initial", want: "initial"},
		{name: "explicit initial wins ACP default", initial: "Manual", constraint: "Initial", want: "manual"},
		{name: "resume without constraint", persisted: "manual", want: "manual"},
		{name: "resume with constraint", persisted: "manual", constraint: "Initial", want: "manual"},
		{name: "resume ignores changed initial", persisted: "manual", initial: "Initial", want: "manual"},
		{name: "unmatched initial falls back", initial: "Unavailable", want: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := session.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Create(session.Metadata{SessionID: "conversation", BaselineModel: tc.persisted}); err != nil {
				t.Fatal(err)
			}
			proc := &lifecycleModelProcess{fakeSharedProcess: newFakeSharedProcess()}
			bs := &BackgroundSession{
				ctx: context.Background(), persistedID: "conversation", acpID: "acp-conversation",
				store: store, sharedProcess: proc, pendingConfig: make(map[string]string),
				mittoConfig: &config.Config{Models: []config.ModelProfile{{
					Name: "Selected", Criteria: &config.ACPServerConstraint{Pattern: tc.initial, MatchMode: "exact"},
				}}},
			}
			if tc.initial != "" {
				bs.initialModelPreference = []config.PromptPreferredModel{{ModelName: "Selected"}}
			}
			if tc.constraint != "" {
				bs.acpServerConstraints = map[string]*config.ACPServerConstraint{
					ConfigOptionCategoryModel: {Pattern: tc.constraint, MatchMode: "exact"},
				}
			}
			bs.setAgentModels(lifecycleModels())
			bs.waitForStartupConfigConstraints()
			if got := bs.GetBaselineModel(); got != tc.want {
				t.Errorf("conversation current model = %q, want %q", got, tc.want)
			}
			if got := bs.GetConfigValue("model"); got != tc.want {
				t.Errorf("UI model = %q, want %q", got, tc.want)
			}
			if got := bs.cmGetCurrentModelID(); got != tc.want {
				t.Errorf("agent-confirmed model = %q, want %q", got, tc.want)
			}
			meta, err := store.GetMetadata("conversation")
			if err != nil || meta.BaselineModel != tc.want {
				t.Errorf("persisted model = %q, want %q (error %v)", meta.BaselineModel, tc.want, err)
			}
			var wantCalls [][2]string
			if tc.want != "default" {
				wantCalls = append(wantCalls, [2]string{"acp-conversation", tc.want})
			}
			if !reflect.DeepEqual(proc.calls, wantCalls) {
				t.Errorf("set_model calls = %v, want %v", proc.calls, wantCalls)
			}
		})
	}
}
