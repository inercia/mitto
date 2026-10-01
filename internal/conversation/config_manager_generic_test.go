package conversation

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

func effortOption(current string, values ...string) SessionConfigOption {
	vals := make([]SessionConfigOptionValue, 0, len(values))
	for _, v := range values {
		vals = append(vals, SessionConfigOptionValue{Value: v, Name: strings.ToUpper(v)})
	}
	return SessionConfigOption{
		ID:           "opaque-effort",
		Name:         "Effort",
		Type:         ConfigOptionTypeSelect,
		Category:     ConfigOptionCategoryThoughtLevel,
		CurrentValue: current,
		Options:      vals,
	}
}

func newFakeWithEffort() *fakeConfigDeps {
	d := newFakeConfigDeps()
	d.configOptions = append(d.configOptions, effortOption("medium", "low", "medium", "high"))
	return d
}

func currentOf(d *fakeConfigDeps, id string) string {
	for _, o := range d.cmGetConfigOptions() {
		if o.ID == id {
			return o.CurrentValue
		}
	}
	return "<absent>"
}

func TestConfigOptionsFromACP_GroupedBooleanUnknownEmpty(t *testing.T) {
	if got := ConfigOptionsFromACP(nil); got != nil {
		t.Fatalf("nil input must yield nil, got %v", got)
	}
	if got := ConfigOptionsFromACP([]acp.SessionConfigOption{}); got != nil {
		t.Fatalf("empty input must yield nil, got %v", got)
	}

	cat := acp.SessionConfigOptionCategoryThoughtLevel
	desc := "how hard to think"
	in := []acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{
			Id: "opaque-1", Name: "Effort", Description: &desc, Category: &cat,
			CurrentValue: "b",
			Options: acp.SessionConfigSelectOptions{
				Ungrouped: &acp.SessionConfigSelectOptionsUngrouped{
					{Value: "a", Name: "A"}, {Value: "b", Name: "B"},
				},
			},
		}},
		{Boolean: &acp.SessionConfigOptionBoolean{Id: "flag", Name: "Flag", CurrentValue: true}},
		{}, // unknown variant: skipped, must not break
	}
	got := ConfigOptionsFromACP(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 options (unknown skipped), got %d: %+v", len(got), got)
	}
	if got[0].ID != "opaque-1" || got[0].Category != "thought_level" || got[0].CurrentValue != "b" || got[0].Description != desc {
		t.Fatalf("select not preserved: %+v", got[0])
	}
	if len(got[0].Options) != 2 || got[0].Options[0].Value != "a" || got[0].Options[1].Value != "b" {
		t.Fatalf("agent ordering lost: %+v", got[0].Options)
	}
	if got[1].Type != ConfigOptionTypeToggle || got[1].CurrentValue != "true" {
		t.Fatalf("boolean not mapped to toggle: %+v", got[1])
	}
}

func TestConfigManager_Generic_IdleSetAcksAndReplacesSnapshot(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.genericSnapshot = func(id, value string) []SessionConfigOption {
		return []SessionConfigOption{effortOption(value, "low", "medium", "high")}
	}

	if err := c.setConfigOption(d, context.Background(), "opaque-effort", "high"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.genericRPCCalls) != 1 || d.genericRPCCalls[0] != [2]string{"opaque-effort", "high"} {
		t.Fatalf("expected one generic RPC, got %v", d.genericRPCCalls)
	}
	if got := currentOf(d, "opaque-effort"); got != "high" {
		t.Fatalf("snapshot not installed, current=%q", got)
	}
	if len(d.persistedGeneric) != 1 || d.persistedGeneric[0] != [2]string{"opaque-effort", "high"} {
		t.Fatalf("override not persisted: %v", d.persistedGeneric)
	}
	if n := len(d.notifiedConfig); n == 0 || d.notifiedConfig[n-1][1] != "opaque-effort" || d.notifiedConfig[n-1][2] != "high" {
		t.Fatalf("expected notify for effort=high, got %v", d.notifiedConfig)
	}
	// Mode/model entries must be untouched by the replacement.
	if got := currentOf(d, ConfigOptionCategoryModel); got != "m-1" {
		t.Fatalf("model entry disturbed: %q", got)
	}
}

func TestConfigManager_Generic_ValidationErrors(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.configOptions = append(d.configOptions, SessionConfigOption{
		ID: "flag", Type: ConfigOptionTypeToggle, CurrentValue: "false",
		Options: []SessionConfigOptionValue{{Value: "true"}, {Value: "false"}},
	})

	if err := c.setConfigOption(d, context.Background(), "nope", "x"); err == nil || !strings.Contains(err.Error(), "unknown config option") {
		t.Fatalf("unknown id: got %v", err)
	}
	if err := c.setConfigOption(d, context.Background(), "opaque-effort", "extreme"); err == nil || !strings.Contains(err.Error(), "invalid value") {
		t.Fatalf("invalid value: got %v", err)
	}
	if err := c.setConfigOption(d, context.Background(), "flag", "true"); err == nil || !strings.Contains(err.Error(), "toggle") {
		t.Fatalf("toggle: got %v", err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("no RPC may be sent for rejected requests: %v", d.genericRPCCalls)
	}
	if got := currentOf(d, "opaque-effort"); got != "medium" {
		t.Fatalf("value mutated by rejected request: %q", got)
	}
}

func TestConfigManager_Generic_SetterFailureKeepsStateAndRebroadcasts(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.genericRPCErr = errors.New("boom")

	err := c.setConfigOption(d, context.Background(), "opaque-effort", "high")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected setter error, got %v", err)
	}
	if got := currentOf(d, "opaque-effort"); got != "medium" {
		t.Fatalf("value must be unchanged after failure, got %q", got)
	}
	if len(d.persistedGeneric) != 0 {
		t.Fatalf("failed change must not be persisted: %v", d.persistedGeneric)
	}
	if n := len(d.notifiedConfig); n == 0 || d.notifiedConfig[n-1][1] != "" {
		t.Fatalf("expected an authoritative snapshot re-broadcast, got %v", d.notifiedConfig)
	}
}

func TestConfigManager_Generic_DeferredWhilePromptingThenFlushed(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.isPrompting = true

	if err := c.setConfigOption(d, context.Background(), "opaque-effort", "high"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("running turn must not be mutated: %v", d.genericRPCCalls)
	}
	if got := d.pendingConfig["opaque-effort"]; got != "high" {
		t.Fatalf("expected pending effort=high, got %q", got)
	}

	d.isPrompting = false
	c.flushPendingConfig(d)
	if len(d.genericRPCCalls) != 1 || d.genericRPCCalls[0] != [2]string{"opaque-effort", "high"} {
		t.Fatalf("flush must issue the RPC after the turn: %v", d.genericRPCCalls)
	}
}

func TestConfigManager_Flush_ModelBeforeDependentOption(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	// Effort comes first in snapshot order here to prove the model still leads.
	d.configOptions = []SessionConfigOption{
		effortOption("medium", "low", "medium", "high"),
		d.configOptions[0], d.configOptions[1],
	}
	d.pendingConfig[ConfigOptionCategoryModel] = "m-2"
	d.pendingConfig["opaque-effort"] = "high"
	d.baselineModel = "m-2"
	d.currentModelID = "m-1"

	d.genericRPCHook = func(id, value string) {
		if len(d.modelRPCCalls) != 1 {
			t.Errorf("effort RPC issued before model RPC (model calls=%v)", d.modelRPCCalls)
		}
	}
	c.flushPendingConfig(d)
	if len(d.genericRPCCalls) != 1 || len(d.modelRPCCalls) != 1 {
		t.Fatalf("expected both RPCs: model=%v generic=%v", d.modelRPCCalls, d.genericRPCCalls)
	}
}

func TestConfigManager_Flush_DropsOptionRemovedByModelChange(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.pendingConfig[ConfigOptionCategoryModel] = "m-2"
	d.pendingConfig["opaque-effort"] = "high"
	d.baselineModel = "m-2"
	d.currentModelID = "m-1"
	// The model change drops the effort option from the agent's snapshot.
	d.modelRPCHook = func() {
		d.configOptions = d.configOptions[:2]
	}

	c.flushPendingConfig(d)
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("option removed by the model change must not be applied: %v", d.genericRPCCalls)
	}
}

func TestOrderPendingConfigIDs_Deterministic(t *testing.T) {
	snapshot := []SessionConfigOption{
		{ID: "z-effort", Category: ConfigOptionCategoryThoughtLevel},
		{ID: "model", Category: ConfigOptionCategoryModel},
		{ID: "a-other"},
	}
	pending := map[string]string{"a-other": "1", "z-effort": "2", "model": "3", "unknown-b": "4", "unknown-a": "5"}
	for i := 0; i < 20; i++ {
		got := orderPendingConfigIDs(pending, snapshot)
		want := []string{"model", "z-effort", "a-other", "unknown-a", "unknown-b"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("iteration %d: got %v want %v", i, got, want)
		}
	}
}

func TestConfigManager_Generic_IdleAckBlocksPromptAdmission(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	release := make(chan struct{})
	entered := make(chan struct{})
	d.genericRPCHook = func(id, value string) {
		close(entered)
		<-release
	}

	done := make(chan error, 1)
	go func() { done <- c.setConfigOption(d, context.Background(), "opaque-effort", "high") }()
	<-entered

	// Simulates waitForStartupModel's barrier: it must block until the ack.
	var admitted atomic.Bool
	go func() {
		d.cmLockConfigRPC()
		d.cmUnlockConfigRPC()
		admitted.Store(true)
	}()
	time.Sleep(50 * time.Millisecond)
	if admitted.Load() {
		t.Fatal("prompt admitted before set_config_option was acked")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !admitted.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !admitted.Load() {
		t.Fatal("prompt never admitted after ack")
	}
}

func TestConfigManager_Generic_TwoConversationsIsolated(t *testing.T) {
	c := configManager{}
	a, b := newFakeWithEffort(), newFakeWithEffort()
	if err := c.setConfigOption(a, context.Background(), "opaque-effort", "high"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.genericRPCCalls) != 0 || currentOf(b, "opaque-effort") != "medium" {
		t.Fatalf("conversation B must be untouched: calls=%v cur=%q", b.genericRPCCalls, currentOf(b, "opaque-effort"))
	}
}

func TestConfigManager_Generic_StaleCompletionAfterCloseIsHarmless(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.genericRPCHook = func(id, value string) { d.closed = true }
	// The RPC completes after the session closed; the manager must not panic and
	// the replacement is simply installed on the (dead) snapshot.
	_ = c.setConfigOption(d, context.Background(), "opaque-effort", "high")
}
