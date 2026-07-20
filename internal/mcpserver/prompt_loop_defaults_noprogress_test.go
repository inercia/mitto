package mcpserver

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// TestApplyPromptLoopDefaultsToStartInput_NoProgressLimit reproduces mitto-erpb:
// when the seeded prompt carries noProgressLimit in its loop: frontmatter and
// the caller did not set loop_no_progress_limit, the value must flow through
// to the ConversationStartInput. Currently NONE of the intermediate layers
// (config.PromptLoop.NoProgressLimit, ConversationStartInput.LoopNoProgressLimit,
// applyPromptLoopDefaultsToStartInput merge) exist, so this test fails to
// compile — which is exactly the reproduction: the schema-extension pattern
// for NoProgressLimit is unwired between the LoopPrompt runtime field and the
// prompt frontmatter surface.
func TestApplyPromptLoopDefaultsToStartInput_NoProgressLimit(t *testing.T) {
	t.Run("frontmatter fills unset caller (0 = opt-out)", func(t *testing.T) {
		input := &ConversationStartInput{}
		zero := 0
		pl := &config.PromptLoop{
			Trigger:         "onTasks",
			NoProgressLimit: &zero,
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopNoProgressLimit == nil {
			t.Fatal("LoopNoProgressLimit should have been filled from frontmatter")
		}
		if *input.LoopNoProgressLimit != 0 {
			t.Errorf("LoopNoProgressLimit = %v, want 0 (opt-out)", *input.LoopNoProgressLimit)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := 5
		input := &ConversationStartInput{LoopNoProgressLimit: &callerVal}
		zero := 0
		pl := &config.PromptLoop{
			Trigger:         "onTasks",
			NoProgressLimit: &zero,
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopNoProgressLimit == nil || *input.LoopNoProgressLimit != 5 {
			t.Errorf("caller's explicit 5 should have been preserved, got %v", input.LoopNoProgressLimit)
		}
	})

	t.Run("nil frontmatter leaves caller nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Trigger: "onTasks"} // NoProgressLimit unset
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopNoProgressLimit != nil {
			t.Errorf("LoopNoProgressLimit should remain nil when frontmatter is silent, got %v", *input.LoopNoProgressLimit)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationStartInput{LoopApplyPromptDefaults: boolPtr(false)}
		zero := 0
		pl := &config.PromptLoop{
			Trigger:         "onTasks",
			NoProgressLimit: &zero,
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopNoProgressLimit != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopNoProgressLimit)
		}
	})
}

// TestApplyPromptLoopDefaultsToUpdateInput_NoProgressLimit is the update-tool
// equivalent of the start-input reproduction.
func TestApplyPromptLoopDefaultsToUpdateInput_NoProgressLimit(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		zero := 0
		pl := &config.PromptLoop{
			Trigger:         "onTasks",
			NoProgressLimit: &zero,
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopNoProgressLimit == nil || *input.LoopNoProgressLimit != 0 {
			t.Errorf("LoopNoProgressLimit should have been filled with 0, got %v", input.LoopNoProgressLimit)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := 5
		input := &ConversationUpdateInput{LoopNoProgressLimit: &callerVal}
		zero := 0
		pl := &config.PromptLoop{
			Trigger:         "onTasks",
			NoProgressLimit: &zero,
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopNoProgressLimit == nil || *input.LoopNoProgressLimit != 5 {
			t.Errorf("caller's explicit 5 should have been preserved, got %v", input.LoopNoProgressLimit)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopApplyPromptDefaults: boolPtr(false)}
		zero := 0
		pl := &config.PromptLoop{
			Trigger:         "onTasks",
			NoProgressLimit: &zero,
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopNoProgressLimit != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopNoProgressLimit)
		}
	})
}
