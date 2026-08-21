package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func loopProcessingPrompts() cel.PromptsContext {
	return cel.PromptsContext{
		Names:        []string{"Loop fixing bug", "Loop implementing feature"},
		EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
	}
}

func TestLoopProcessing_OnChildPreamble_RendersChildIDAndEvent(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Prompts: loopProcessingPrompts(),
		Trigger: &cel.TriggerContext{
			Kind: "onChild",
			OnChild: &cel.TriggerOnChildContext{
				ChildID: "20260821-abc",
				Event:   "anyEndResponse",
			},
		},
	})
	for _, want := range []string{"20260821-abc", "anyEndResponse", "Triggered by child conversation"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	for _, notWant := range []string{"anyLoopStopped", "high-priority reap candidate", "stopped reason"} {
		if strings.Contains(out, notWant) {
			t.Errorf("output unexpectedly contains %q", notWant)
		}
	}
}

func TestLoopProcessing_OnChildPreamble_anyLoopStopped_NotesReapCandidateAndReason(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Prompts: loopProcessingPrompts(),
		Trigger: &cel.TriggerContext{
			Kind: "onChild",
			OnChild: &cel.TriggerOnChildContext{
				ChildID:       "child-x",
				Event:         "anyLoopStopped",
				StoppedReason: "maxIterations",
			},
		},
	})
	for _, want := range []string{"child-x", "anyLoopStopped", "maxIterations", "high-priority reap candidate"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestLoopProcessing_RunOnStartBanner_Renders(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Prompts: loopProcessingPrompts(),
		Trigger: &cel.TriggerContext{
			Kind:         "schedule",
			IsRunOnStart: true,
		},
	})
	for _, want := range []string{"Bootstrap pass", "just started"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(out, "Scheduled recovery pass") {
		t.Errorf("output unexpectedly contains %q", "Scheduled recovery pass")
	}
}

func TestLoopProcessing_ScheduledRecoveryFraming_Renders(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Prompts: loopProcessingPrompts(),
		Trigger: &cel.TriggerContext{
			Kind: "schedule",
		},
	})
	for _, want := range []string{"Scheduled recovery pass", "hourly"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	for _, notWant := range []string{"Bootstrap pass", "Triggered by child conversation"} {
		if strings.Contains(out, notWant) {
			t.Errorf("output unexpectedly contains %q", notWant)
		}
	}
}

func TestLoopProcessing_OnTasksBlock_StillRendersUnchanged(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Prompts: loopProcessingPrompts(),
		Trigger: &cel.TriggerContext{
			Kind: "onTasks",
			OnTasks: &cel.TriggerOnTasksContext{
				Changes: cel.TasksChangesView{
					Touched: []map[string]any{
						{"id": "mitto-a", "title": "T", "status": "open", "priority": 2},
					},
				},
			},
		},
	})
	if !strings.Contains(out, "Triggered by these beads changes") || !strings.Contains(out, "mitto-a") {
		t.Fatalf("existing onTasks block did not render as expected")
	}
	for _, notWant := range []string{"Bootstrap pass", "Scheduled recovery pass", "Triggered by child conversation"} {
		if strings.Contains(out, notWant) {
			t.Errorf("output unexpectedly contains %q", notWant)
		}
	}
}

func TestLoopProcessing_NoTriggerContext_AllNewBranchesSilent(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Prompts: loopProcessingPrompts(),
		Trigger: nil,
	})
	for _, notWant := range []string{
		"Bootstrap pass",
		"Scheduled recovery pass",
		"Triggered by child conversation",
		"Triggered by these beads changes",
	} {
		if strings.Contains(out, notWant) {
			t.Errorf("output unexpectedly contains %q", notWant)
		}
	}
}
