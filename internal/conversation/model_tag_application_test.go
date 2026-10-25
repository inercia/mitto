package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func modelTagProfile(name, model string) config.ModelProfile {
	return config.ModelProfile{Name: name, Tags: []string{"Coding"}, Criteria: &config.ACPServerConstraint{Pattern: model, MatchMode: "exact"}}
}

func TestModelTagApplicationResolution(t *testing.T) {
	for _, tc := range []struct{ name, agent, tag, override, want string }{
		{"priority", "agent", "Coding", "", "initial"},
		{"profile case", "agent", "coding", "", "initial"},
		{"override", "agent", "Coding", "manual", "manual"},
		{"unavailable override", "agent", "Coding", "absent", "initial"},
		{"wrong agent", "other", "Coding", "manual", "initial"},
		{"override exact case", "agent", "coding", "manual", "initial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bs, proc := newPendingModelSession(t)
			bs.acpServer = "agent"
			bs.modelOverrides = config.ModelOverrides{{ACPServer: tc.agent, Tag: "Coding", ModelID: tc.override}}
			bs.mittoConfig.Models = []config.ModelProfile{modelTagProfile("first", "Initial"), modelTagProfile("second", "Default")}
			type outcome struct {
				result ModelTagApplyResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() { r, err := bs.ApplyModelTagWithResult(context.Background(), tc.tag); done <- outcome{r, err} }()
			call := modelTurnExpectModel(t, proc, tc.want)
			call.reply <- nil
			got := modelTurnReceive(t, done)
			if got.err != nil || got.result != (ModelTagApplyResult{ModelID: tc.want}) {
				t.Fatalf("result = %+v, %v", got.result, got.err)
			}
			assertPendingModelBaseline(t, bs, tc.want)
			if bs.GetConfigValue(ConfigOptionCategoryModel) != tc.want {
				t.Fatal("config did not select resolved model")
			}
			modelTurnNoRPC(t, proc.models)
		})
	}
}

func TestModelTagApplicationIdleAndDeferred(t *testing.T) {
	for _, prompting := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "prompting"}[prompting], func(t *testing.T) {
			bs, proc := newPendingModelSession(t)
			bs.mittoConfig.Models = []config.ModelProfile{modelTagProfile("current", "Default")}
			bs.baselineModel = "stale"
			bs.isPrompting = prompting
			got, err := bs.ApplyModelTagWithResult(context.Background(), "Coding")
			if err != nil || got.ModelID != "default" || got.AlreadyActive == prompting {
				t.Fatalf("result = %+v, %v", got, err)
			}
			assertPendingModelBaseline(t, bs, "default")
			if prompting && bs.pendingConfig[ConfigOptionCategoryModel] != "default" {
				t.Fatal("selection was not deferred")
			}
			modelTurnNoRPC(t, proc.models)
		})
	}
}

func TestModelTagApplicationErrorsAndCompatibility(t *testing.T) {
	bs := &BackgroundSession{overrideActive: true}
	if id, err := bs.ApplyModelTag(context.Background(), ""); id != "" || err != nil || bs.overrideActive {
		t.Fatal("empty wrapper did not clear override")
	}
	if r, err := bs.ApplyModelTagWithResult(context.Background(), "Coding"); r != (ModelTagApplyResult{}) || !errors.Is(err, ErrModelTagCatalogUnavailable) {
		t.Fatalf("nil catalog: %+v %v", r, err)
	}
	bs.agentModels = &SessionModelState{}
	if _, err := bs.ApplyModelTagWithResult(context.Background(), "arbitrary"); !errors.Is(err, ErrModelTagNoMatch) {
		t.Fatalf("empty catalog: %v", err)
	}
	for _, failure := range []error{errors.New("RPC failed"), context.DeadlineExceeded} {
		bs, proc := newPendingModelSession(t)
		bs.mittoConfig.Models = []config.ModelProfile{modelTagProfile("target", "Initial")}
		done := make(chan error, 1)
		go func() {
			r, err := bs.ApplyModelTagWithResult(context.Background(), "Coding")
			if r != (ModelTagApplyResult{}) {
				done <- errors.New("nonzero error result")
				return
			}
			done <- err
		}()
		call := modelTurnExpectModel(t, proc, "initial")
		call.reply <- failure
		if err := modelTurnReceive(t, done); !errors.Is(err, failure) {
			t.Fatalf("underlying error lost: %v", err)
		}
	}
}

func TestModelTagApplication_HiddenResolvedModelRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hidden string
		policy bool
	}{{"hidden", "initial", true}, {"policy hides current", "default", true}} {
		t.Run(tc.name, func(t *testing.T) {
			bs, proc := newPendingModelSession(t)
			bs.acpServer = "agent"
			bs.mittoConfig.Models = []config.ModelProfile{modelTagProfile("target", "Initial")}
			if tc.hidden == "default" {
				bs.mittoConfig.Models = []config.ModelProfile{modelTagProfile("target", "Default")}
			}
			bs.mittoConfig.ACPServers = []config.ACPServer{{Name: "agent", Command: "x", HiddenModels: []string{tc.hidden}}}
			r, err := bs.ApplyModelTagWithResult(context.Background(), "Coding")
			if !errors.Is(err, errModelHiddenByPolicy) || r != (ModelTagApplyResult{}) {
				t.Fatalf("result = %+v, %v", r, err)
			}
			if msg := err.Error(); msg == "" || !strings.Contains(msg, "Coding") || !strings.Contains(msg, "hidden by policy") {
				t.Fatalf("error not user-readable: %q", msg)
			}
			modelTurnNoRPC(t, proc.models)
		})
	}
}

func TestModelTagApplication_InvalidPolicyFailsClosed(t *testing.T) {
	bs, proc := newPendingModelSession(t)
	bs.acpServer = "agent"
	bs.mittoConfig.Models = []config.ModelProfile{modelTagProfile("target", "Initial")}
	bs.mittoConfig.ACPServers = []config.ACPServer{{Name: "agent", Command: "x", HiddenModels: []string{"("}}}
	if _, err := bs.ApplyModelTagWithResult(context.Background(), "Coding"); !errors.Is(err, errModelHiddenByPolicy) {
		t.Fatalf("got %v", err)
	}
	modelTurnNoRPC(t, proc.models)
}
