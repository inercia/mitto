package mcpserver

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func boolPtr(b bool) *bool { return &b }

// TestApplyPromptLoopDefaultsToStartInput_CoalesceDuringBusy covers the
// mitto-f9q merge: when the seeded prompt carries coalesceDuringBusy in its
// loop: frontmatter and the caller did not set loop_coalesce_during_busy, the
// value flows through to the ConversationStartInput.
func TestApplyPromptLoopDefaultsToStartInput_CoalesceDuringBusy(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{
			Trigger:            "onTasks",
			CoalesceDuringBusy: boolPtr(false),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopCoalesceDuringBusy == nil {
			t.Fatal("LoopCoalesceDuringBusy should have been filled from frontmatter")
		}
		if *input.LoopCoalesceDuringBusy != false {
			t.Errorf("LoopCoalesceDuringBusy = %v, want false", *input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := true
		input := &ConversationStartInput{LoopCoalesceDuringBusy: &callerVal}
		pl := &config.PromptLoop{
			Trigger:            "onTasks",
			CoalesceDuringBusy: boolPtr(false),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopCoalesceDuringBusy == nil || *input.LoopCoalesceDuringBusy != true {
			t.Errorf("caller's explicit true should have been preserved, got %v", input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("nil frontmatter leaves caller nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Trigger: "onTasks"} // CoalesceDuringBusy unset
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopCoalesceDuringBusy != nil {
			t.Errorf("LoopCoalesceDuringBusy should remain nil when frontmatter is silent, got %v", *input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationStartInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger:            "onTasks",
			CoalesceDuringBusy: boolPtr(false),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopCoalesceDuringBusy != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopCoalesceDuringBusy)
		}
	})
}

// TestApplyPromptLoopDefaultsToUpdateInput_CoalesceDuringBusy is the
// update-tool equivalent; the semantics mirror the start-input helper.
func TestApplyPromptLoopDefaultsToUpdateInput_CoalesceDuringBusy(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{
			Trigger:            "onTasks",
			CoalesceDuringBusy: boolPtr(false),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopCoalesceDuringBusy == nil || *input.LoopCoalesceDuringBusy != false {
			t.Errorf("LoopCoalesceDuringBusy should have been filled with false, got %v", input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := true
		input := &ConversationUpdateInput{LoopCoalesceDuringBusy: &callerVal}
		pl := &config.PromptLoop{
			Trigger:            "onTasks",
			CoalesceDuringBusy: boolPtr(false),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopCoalesceDuringBusy == nil || *input.LoopCoalesceDuringBusy != true {
			t.Errorf("caller's explicit true should have been preserved, got %v", input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger:            "onTasks",
			CoalesceDuringBusy: boolPtr(false),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopCoalesceDuringBusy != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopCoalesceDuringBusy)
		}
	})
}
