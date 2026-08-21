// Package handlers cross-helper sync guard for the mitto-r6j merge helpers.
// Sibling test in internal/mcpserver pins the two MCP helpers
// (applyPromptLoopDefaultsToStartInput / applyPromptLoopDefaultsToUpdateInput)
// against the same PromptLoop fixture; this file pins the REST-PUT helper
// (applyPromptLoopDefaultsToLoopPrompt). Together the three tests keep every
// PromptLoop frontmatter field currently merged from drifting between the
// three call sites.
package handlers

import (
	"testing"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// TestApplyPromptLoopDefaultsToLoopPrompt_SyncGuard is the REST-PUT half of
// the cross-helper sync guard. Fixture must stay in sync with the same-named
// test in internal/mcpserver so a new frontmatter field wired into one of
// the three helpers but not the others trips exactly one side of the guard.
func TestApplyPromptLoopDefaultsToLoopPrompt_SyncGuard(t *testing.T) {
	// Exhaustive fixture matches internal/mcpserver's TestApplyPromptLoopDefaults_SyncGuard.
	pl := &configPkg.PromptLoop{
		Trigger:      []string{"schedule", "onCompletion", "onTasks", "onChild"},
		Schedule:     &configPkg.PromptLoopSchedule{Value: 7, Unit: "hours", At: ""},
		OnCompletion: &configPkg.PromptLoopOnCompletion{Delay: 45},
		OnTasks: &configPkg.PromptLoopOnTasks{
			Condition:          "e2e:pr:closed",
			ConditionPreset:    "pr-closed",
			Cooldown:           90,
			SettleWindow:       33,
			CoalesceDuringBusy: boolPtr(false),
		},
		OnChild:       &configPkg.PromptLoopOnChild{When: []string{"anyDeleted"}},
		MaxIterations: 11,
		MaxDuration:   "2h",
		FreshContext:  boolPtr(true),
		RunOnStart:    boolPtr(true),
	}

	lp := &session.LoopPrompt{}
	applyPromptLoopDefaultsToLoopPrompt(lp, pl, nil)

	// Triggers: whole list is filled (not just primary).
	if len(lp.Triggers) != 4 ||
		lp.Triggers[0] != session.TriggerSchedule ||
		lp.Triggers[1] != session.TriggerOnCompletion ||
		lp.Triggers[2] != session.TriggerOnTasks ||
		lp.Triggers[3] != session.TriggerOnChild {
		t.Errorf("Triggers = %v, want [schedule onCompletion onTasks onChild]", lp.Triggers)
	}
	if len(lp.ChildEvents) != 1 || lp.ChildEvents[0] != session.ChildEventAnyDeleted {
		t.Errorf("ChildEvents = %v, want [anyDeleted]", lp.ChildEvents)
	}
	if lp.DelaySeconds != 45 {
		t.Errorf("DelaySeconds = %d, want 45", lp.DelaySeconds)
	}
	if lp.Frequency.Value != 7 {
		t.Errorf("Frequency.Value = %d, want 7", lp.Frequency.Value)
	}
	if lp.Frequency.Unit != session.FrequencyHours {
		t.Errorf("Frequency.Unit = %q, want %q", lp.Frequency.Unit, session.FrequencyHours)
	}
	if lp.MaxIterations != 11 {
		t.Errorf("MaxIterations = %d, want 11", lp.MaxIterations)
	}
	if lp.MaxDurationSeconds != 7200 {
		t.Errorf("MaxDurationSeconds = %d, want 7200 (2h)", lp.MaxDurationSeconds)
	}
	if lp.Condition != "e2e:pr:closed" {
		t.Errorf("Condition = %q, want %q", lp.Condition, "e2e:pr:closed")
	}
	// ConditionPreset and CooldownSeconds are merged only by this helper
	// today (mitto-r6j.5). If a future change wires them into the MCP
	// helpers as well, mirror the merge here so the guard stays exhaustive
	// on this side.
	if lp.ConditionPreset != "pr-closed" {
		t.Errorf("ConditionPreset = %q, want %q", lp.ConditionPreset, "pr-closed")
	}
	if lp.CooldownSeconds != 90 {
		t.Errorf("CooldownSeconds = %d, want 90", lp.CooldownSeconds)
	}
	if lp.SettleWindowSeconds == nil || *lp.SettleWindowSeconds != 33 {
		t.Errorf("SettleWindowSeconds = %v, want *33", lp.SettleWindowSeconds)
	}
	if lp.CoalesceDuringBusy == nil || *lp.CoalesceDuringBusy != false {
		t.Errorf("CoalesceDuringBusy = %v, want *false", lp.CoalesceDuringBusy)
	}
	if !lp.FreshContext {
		// FreshContext is a plain bool on the request DTO (see helper doc):
		// frontmatter *true fills a false request, frontmatter false is a no-op.
		t.Error("FreshContext = false, want true (frontmatter *true fills a false request)")
	}
	if lp.RunOnStart == nil || *lp.RunOnStart != true {
		t.Errorf("RunOnStart = %v, want *true", lp.RunOnStart)
	}
}
