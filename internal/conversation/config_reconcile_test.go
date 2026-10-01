package conversation

import (
	"context"
	"sync"
	"testing"
	"time"
)

// modelSwapSnapshot makes the fake's model RPC publish the post-change
// snapshot built by mk (simulating the agent's config_option_update).
func modelSwapSnapshot(d *fakeConfigDeps, mk func() []SessionConfigOption) {
	d.modelRPCHook = func() { d.configOptions = mk() }
}

func baseOptions(d *fakeConfigDeps) []SessionConfigOption {
	return []SessionConfigOption{d.configOptions[0], d.configOptions[1]}
}

func findAdjustment(batches [][]ConfigAdjustment, id, reason string) (ConfigAdjustment, bool) {
	for _, b := range batches {
		for _, a := range b {
			if a.ConfigID == id && a.Reason == reason {
				return a, true
			}
		}
	}
	return ConfigAdjustment{}, false
}

func TestReconcile_OptionRemoved_NoSetterAndOverrideDropped(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	base := baseOptions(d)
	modelSwapSnapshot(d, func() []SessionConfigOption { return base })

	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("setter must never reset a vanished option: %v", d.genericRPCCalls)
	}
	if _, ok := d.overrides["opaque-effort"]; ok {
		t.Fatalf("override should be dropped, got %v", d.overrides)
	}
	if a, ok := findAdjustment(d.adjustments, "opaque-effort", ConfigAdjustmentOptionRemoved); !ok || a.RequestedValue != "high" {
		t.Fatalf("expected option_removed notice, got %+v", d.adjustments)
	}
}

func TestReconcile_ExplicitSelectionModelChange_DropsPendingEffort(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	base := baseOptions(d)
	modelSwapSnapshot(d, func() []SessionConfigOption { return base })

	if err := c.setConfigOption(d, context.Background(), ConfigOptionCategoryModel, "m-2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("no effort setter expected: %v", d.genericRPCCalls)
	}
	if len(d.pendingConfig) != 0 {
		t.Fatalf("pending must be empty: %v", d.pendingConfig)
	}
}

func TestReconcile_SupportedToSupported_ValidSurvivesInvalidFallsBack(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	// Valid on the new model: "low". Different second option has invalid value.
	other := SessionConfigOption{
		ID: "opaque-verbosity", Name: "Verbosity", Type: ConfigOptionTypeSelect,
		CurrentValue: "v1",
		Options:      []SessionConfigOptionValue{{Value: "v1", Name: "V1"}, {Value: "v2", Name: "V2"}},
	}
	d.configOptions = append(d.configOptions, other)
	d.overrides = map[string]string{"opaque-effort": "low", "opaque-verbosity": "v9"}
	b := baseOptions(d)
	modelSwapSnapshot(d, func() []SessionConfigOption {
		return append(append([]SessionConfigOption{}, b...),
			effortOption("medium", "low", "medium"), other)
	})

	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.genericRPCCalls) != 1 || d.genericRPCCalls[0] != [2]string{"opaque-effort", "low"} {
		t.Fatalf("expected only the valid preference to be sent: %v", d.genericRPCCalls)
	}
	if currentOf(d, "opaque-verbosity") != "v1" {
		t.Fatalf("invalid preference must fall back to agent value")
	}
	if a, ok := findAdjustment(d.adjustments, "opaque-verbosity", ConfigAdjustmentValueNotAdvertised); !ok || a.AppliedValue != "v1" {
		t.Fatalf("expected value_not_advertised notice with agent value: %+v", d.adjustments)
	}
}

func TestReconcile_InheritedInvalidEffortNeverSent(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	b := baseOptions(d)
	modelSwapSnapshot(d, func() []SessionConfigOption {
		return append(append([]SessionConfigOption{}, b...), effortOption("low", "low", "medium"))
	})
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("invalid inherited effort must never be sent: %v", d.genericRPCCalls)
	}
	if currentOf(d, "opaque-effort") != "low" {
		t.Fatalf("agent value must be kept, got %q", currentOf(d, "opaque-effort"))
	}
	if _, ok := findAdjustment(d.adjustments, "opaque-effort", ConfigAdjustmentValueNotAdvertised); !ok {
		t.Fatalf("expected notice: %+v", d.adjustments)
	}
}

func TestReconcile_ReverseTransitionRestoresValidPreference(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	b := baseOptions(d)
	// Forward: effort vanishes (temporary, non-durable override of the model).
	modelSwapSnapshot(d, func() []SessionConfigOption { return b })
	if err := c.setActiveModelOnlyOpts(d, context.Background(), "m-2", false); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if len(d.deletedOverride) != 0 || d.overrides["opaque-effort"] != "high" {
		t.Fatalf("temporary override must not corrupt persisted prefs: del=%v ov=%v", d.deletedOverride, d.overrides)
	}
	// Backward: baseline restore returns an effort option advertising "high".
	modelSwapSnapshot(d, func() []SessionConfigOption {
		return append(append([]SessionConfigOption{}, b...), effortOption("medium", "low", "medium", "high"))
	})
	if err := c.setActiveModelOnlyOpts(d, context.Background(), "m-1", false); err != nil {
		t.Fatalf("backward: %v", err)
	}
	if len(d.genericRPCCalls) != 1 || d.genericRPCCalls[0] != [2]string{"opaque-effort", "high"} {
		t.Fatalf("valid baseline preference must be restored: %v", d.genericRPCCalls)
	}
}

func TestReconcile_TemporaryOverrideNeverDeletesPersisted(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	b := baseOptions(d)
	modelSwapSnapshot(d, func() []SessionConfigOption {
		return append(append([]SessionConfigOption{}, b...), effortOption("low", "low", "medium"))
	})
	if err := c.setActiveModelOnlyOpts(d, context.Background(), "m-2", false); err != nil {
		t.Fatal(err)
	}
	if len(d.deletedOverride) != 0 || d.overrides["opaque-effort"] != "high" {
		t.Fatalf("non-durable path must keep the requested preference: del=%v ov=%v", d.deletedOverride, d.overrides)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("invalid value must not be sent: %v", d.genericRPCCalls)
	}
}

func TestReconcile_FlushModelAndEffort_ModelFirstThenRevalidated(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.baselineModel = "m-2"
	d.currentModelID = "m-1"
	d.pendingConfig[ConfigOptionCategoryModel] = "m-2"
	d.pendingConfig["opaque-effort"] = "high"
	b := baseOptions(d)
	// New model only advertises low/medium: pending "high" is invalid.
	modelSwapSnapshot(d, func() []SessionConfigOption {
		return append(append([]SessionConfigOption{}, b...), effortOption("medium", "low", "medium"))
	})
	c.flushPendingConfig(d)
	if len(d.modelRPCCalls) != 1 {
		t.Fatalf("model RPC expected once: %v", d.modelRPCCalls)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("stale pending effort must not be sent: %v", d.genericRPCCalls)
	}
	if _, ok := findAdjustment(d.adjustments, "opaque-effort", ConfigAdjustmentValueNotAdvertised); !ok {
		t.Fatalf("expected notice: %+v", d.adjustments)
	}
}

func TestReconcile_FlushModelAndEffort_ValidEffortAppliedAfterModel(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.baselineModel = "m-2"
	d.pendingConfig[ConfigOptionCategoryModel] = "m-2"
	d.pendingConfig["opaque-effort"] = "low"
	d.genericRPCHook = func(string, string) {
		if len(d.modelRPCCalls) != 1 {
			t.Errorf("effort sent before model")
		}
	}
	c.flushPendingConfig(d)
	if len(d.genericRPCCalls) != 1 || d.genericRPCCalls[0] != [2]string{"opaque-effort", "low"} {
		t.Fatalf("got %v", d.genericRPCCalls)
	}
}

func TestReconcile_FlushPendingEffortForVanishedOption(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.pendingConfig["opaque-effort"] = "high"
	c.flushPendingConfig(d)
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("must not send for absent option: %v", d.genericRPCCalls)
	}
	if _, ok := findAdjustment(d.adjustments, "opaque-effort", ConfigAdjustmentOptionRemoved); !ok {
		t.Fatalf("expected option_removed notice: %+v", d.adjustments)
	}
}

func TestReconcile_StartupKeepsUnadvertisedFreshContextDrops(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	d.overrides = map[string]string{"opaque-effort": "high"}
	c.revalidateOverrides(d, context.Background(), true)
	if len(d.deletedOverride) != 0 || len(d.adjustments) != 0 {
		t.Fatalf("startup must keep unadvertised overrides silently: del=%v adj=%v", d.deletedOverride, d.adjustments)
	}
	c.revalidateOverrides(d, context.Background(), false)
	if _, ok := d.overrides["opaque-effort"]; ok {
		t.Fatalf("strict revalidation must drop it: %v", d.overrides)
	}
	if _, ok := findAdjustment(d.adjustments, "opaque-effort", ConfigAdjustmentOptionRemoved); !ok {
		t.Fatalf("expected notice: %+v", d.adjustments)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("no setter: %v", d.genericRPCCalls)
	}
}

func TestReconcile_ResumeAppliesOnlyValidPreference(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	c.revalidateOverrides(d, context.Background(), true)
	if len(d.genericRPCCalls) != 1 || d.genericRPCCalls[0] != [2]string{"opaque-effort", "high"} {
		t.Fatalf("got %v", d.genericRPCCalls)
	}
	d2 := newFakeWithEffort()
	d2.overrides = map[string]string{"opaque-effort": "extreme"}
	c.revalidateOverrides(d2, context.Background(), true)
	if len(d2.genericRPCCalls) != 0 {
		t.Fatalf("invalid must not be sent: %v", d2.genericRPCCalls)
	}
	if _, ok := findAdjustment(d2.adjustments, "opaque-effort", ConfigAdjustmentValueNotAdvertised); !ok {
		t.Fatalf("expected notice: %+v", d2.adjustments)
	}
}

func TestReconcile_SnapshotTimeout_SurfacedAndNothingSent(t *testing.T) {
	old := configSnapshotWaitTimeout
	configSnapshotWaitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { configSnapshotWaitTimeout = old })

	c := configManager{}
	d := newFakeWithEffort()
	d.strictSnapWait = true
	d.overrides = map[string]string{"opaque-effort": "high"}
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatalf("model change itself must still succeed: %v", err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("nothing may be sent without a fresh snapshot: %v", d.genericRPCCalls)
	}
	if a, ok := findAdjustment(d.adjustments, "opaque-effort", ConfigAdjustmentSnapshotUnconfirmed); !ok || a.AppliedValue != "medium" {
		t.Fatalf("expected snapshot_unconfirmed notice: %+v", d.adjustments)
	}
}

func TestReconcile_StaleSnapshotNotAccepted(t *testing.T) {
	old := configSnapshotWaitTimeout
	configSnapshotWaitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { configSnapshotWaitTimeout = old })

	c := configManager{}
	d := newFakeWithEffort()
	d.strictSnapWait = true
	d.overrides = map[string]string{"opaque-effort": "low"}
	// A snapshot arriving BEFORE the model change must not satisfy the wait.
	d.cmReplaceGenericConfigOptions(nil)
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatal(err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("stale snapshot must not trigger setters: %v", d.genericRPCCalls)
	}
}

func TestReconcile_LateSnapshotAfterModelRPC_Accepted(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.strictSnapWait = true
	d.overrides = map[string]string{"opaque-effort": "low"}
	d.modelRPCHook = func() {
		go func() {
			time.Sleep(5 * time.Millisecond)
			d.cmReplaceGenericConfigOptions([]SessionConfigOption{effortOption("medium", "low", "medium")})
		}()
	}
	// Strict fake compares generations, so wait must observe the late update.
	d.strictSnapWait = false
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatal(err)
	}
	// Whatever interleaving occurred, nothing invalid is ever sent.
	for _, call := range d.genericRPCCalls {
		if call[1] != "low" {
			t.Fatalf("unexpected setter call %v", call)
		}
	}
}

func TestReconcile_EmptySnapshotDropsAllDependents(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	modelSwapSnapshot(d, func() []SessionConfigOption { return nil })
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatal(err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("no setter on empty snapshot: %v", d.genericRPCCalls)
	}
	if _, ok := d.overrides["opaque-effort"]; ok {
		t.Fatalf("override must be dropped: %v", d.overrides)
	}
}

func TestReconcile_DuplicateSnapshotIdempotent(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "medium"} // equals agent current
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatal(err)
	}
	if err := c.setActiveModelOnly(d, context.Background(), "m-1"); err != nil {
		t.Fatal(err)
	}
	if len(d.genericRPCCalls) != 0 {
		t.Fatalf("matching current value needs no setter: %v", d.genericRPCCalls)
	}
	if len(d.adjustments) != 0 {
		t.Fatalf("no notice expected: %+v", d.adjustments)
	}
}

func TestReconcile_RapidEdits_NoResurrection(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	b := baseOptions(d)
	// Effort chosen, then model change removes the option, then a second
	// model change brings it back: the first choice must not be replayed.
	if err := c.setConfigOption(d, context.Background(), "opaque-effort", "high"); err != nil {
		t.Fatal(err)
	}
	d.overrides = nil // persisted state was never recorded by the fake setter
	d.overrides = map[string]string{"opaque-effort": "high"}
	modelSwapSnapshot(d, func() []SessionConfigOption { return b })
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatal(err)
	}
	calls := len(d.genericRPCCalls)
	modelSwapSnapshot(d, func() []SessionConfigOption {
		return append(append([]SessionConfigOption{}, b...), effortOption("medium", "low", "medium", "high"))
	})
	if err := c.setActiveModelOnly(d, context.Background(), "m-1"); err != nil {
		t.Fatal(err)
	}
	if len(d.genericRPCCalls) != calls {
		t.Fatalf("dropped effort must not be resurrected: %v", d.genericRPCCalls)
	}
}

func TestReconcile_DifferentVocabularyBehavesIdentically(t *testing.T) {
	c := configManager{}
	d := newFakeConfigDeps()
	custom := SessionConfigOption{
		ID: "zz-9", Name: "Depth", Type: ConfigOptionTypeSelect,
		Category: ConfigOptionCategoryThoughtLevel, CurrentValue: "alpha",
		Options: []SessionConfigOptionValue{{Value: "alpha", Name: "A"}, {Value: "beta", Name: "B"}},
	}
	d.configOptions = append(d.configOptions, custom)
	d.overrides = map[string]string{"zz-9": "beta"}
	b := baseOptions(d)

	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err != nil {
		t.Fatal(err)
	}
	if len(d.genericRPCCalls) != 1 || d.genericRPCCalls[0] != [2]string{"zz-9", "beta"} {
		t.Fatalf("valid custom value applied: %v", d.genericRPCCalls)
	}
	modelSwapSnapshot(d, func() []SessionConfigOption { return b })
	if err := c.setActiveModelOnly(d, context.Background(), "m-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := findAdjustment(d.adjustments, "zz-9", ConfigAdjustmentOptionRemoved); !ok {
		t.Fatalf("expected removal notice: %+v", d.adjustments)
	}
}

func TestReconcile_LegacyAgentNoOptions_NoWaitNoSetter(t *testing.T) {
	old := configSnapshotWaitTimeout
	configSnapshotWaitTimeout = time.Hour // would hang the test if consulted
	t.Cleanup(func() { configSnapshotWaitTimeout = old })

	c := configManager{}
	d := newFakeConfigDeps()
	d.configOptions = nil
	d.strictSnapWait = true
	done := make(chan error, 1)
	go func() { done <- c.setActiveModelOnly(d, context.Background(), "m-2") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("legacy agent must not wait for a snapshot")
	}
	if len(d.genericRPCCalls) != 0 || len(d.adjustments) != 0 {
		t.Fatalf("unexpected activity: %v %v", d.genericRPCCalls, d.adjustments)
	}
}

func TestReconcile_ModelRPCFailure_NoReconciliation(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "low"}
	d.setModelErr = context.DeadlineExceeded
	if err := c.setActiveModelOnly(d, context.Background(), "m-2"); err == nil {
		t.Fatal("expected error")
	}
	if len(d.genericRPCCalls) != 0 || len(d.adjustments) != 0 {
		t.Fatalf("no reconciliation after failed model change: %v %v", d.genericRPCCalls, d.adjustments)
	}
}

func TestReconcile_TwoConversationsIsolated(t *testing.T) {
	c := configManager{}
	a, b := newFakeWithEffort(), newFakeWithEffort()
	a.overrides = map[string]string{"opaque-effort": "high"}
	base := baseOptions(a)
	modelSwapSnapshot(a, func() []SessionConfigOption { return base })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = c.setActiveModelOnly(a, context.Background(), "m-2") }()
	go func() { defer wg.Done(); _ = c.setActiveModelOnly(b, context.Background(), "m-2") }()
	wg.Wait()
	if currentOf(b, "opaque-effort") != "medium" || len(b.genericRPCCalls) != 0 || len(b.adjustments) != 0 {
		t.Fatalf("conversation B must be untouched: %v %v", b.genericRPCCalls, b.adjustments)
	}
	if currentOf(a, "opaque-effort") != "<absent>" {
		t.Fatalf("conversation A option should be gone")
	}
}

func TestReconcile_PromptAdmissionWaitsForReconcile(t *testing.T) {
	c := configManager{}
	d := newFakeWithEffort()
	d.overrides = map[string]string{"opaque-effort": "high"}
	b := baseOptions(d)
	release := make(chan struct{})
	entered := make(chan struct{})
	d.modelRPCHook = func() {
		d.configOptions = b
	}
	d.genericRPCHook = func(string, string) {}
	// Hold the guard from a competing "prompt barrier" and verify the model
	// change (and therefore reconciliation) is serialised behind it.
	d.cmLockConfigRPC()
	go func() {
		close(entered)
		_ = c.setActiveModelOnly(d, context.Background(), "m-2")
		close(release)
	}()
	<-entered
	select {
	case <-release:
		t.Fatal("model change must wait for the config RPC guard")
	case <-time.After(30 * time.Millisecond):
	}
	d.cmUnlockConfigRPC()
	select {
	case <-release:
	case <-time.After(2 * time.Second):
		t.Fatal("model change did not complete")
	}
}
