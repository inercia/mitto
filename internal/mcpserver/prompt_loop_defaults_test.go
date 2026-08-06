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
			Trigger: []string{"onTasks"},
			OnTasks: &config.PromptLoopOnTasks{CoalesceDuringBusy: boolPtr(false)},
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
			Trigger: []string{"onTasks"},
			OnTasks: &config.PromptLoopOnTasks{CoalesceDuringBusy: boolPtr(false)},
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopCoalesceDuringBusy == nil || *input.LoopCoalesceDuringBusy != true {
			t.Errorf("caller's explicit true should have been preserved, got %v", input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("nil frontmatter leaves caller nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Trigger: []string{"onTasks"}} // CoalesceDuringBusy unset
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopCoalesceDuringBusy != nil {
			t.Errorf("LoopCoalesceDuringBusy should remain nil when frontmatter is silent, got %v", *input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationStartInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks"},
			OnTasks: &config.PromptLoopOnTasks{CoalesceDuringBusy: boolPtr(false)},
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
			Trigger: []string{"onTasks"},
			OnTasks: &config.PromptLoopOnTasks{CoalesceDuringBusy: boolPtr(false)},
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
			Trigger: []string{"onTasks"},
			OnTasks: &config.PromptLoopOnTasks{CoalesceDuringBusy: boolPtr(false)},
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopCoalesceDuringBusy == nil || *input.LoopCoalesceDuringBusy != true {
			t.Errorf("caller's explicit true should have been preserved, got %v", input.LoopCoalesceDuringBusy)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks"},
			OnTasks: &config.PromptLoopOnTasks{CoalesceDuringBusy: boolPtr(false)},
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopCoalesceDuringBusy != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopCoalesceDuringBusy)
		}
	})
}

// TestApplyPromptLoopDefaultsToStartInput_FreshContext mirrors the
// CoalesceDuringBusy tests for the freshContext frontmatter field: when the
// seeded prompt sets it and the caller did not, the value flows through to
// ConversationStartInput; explicit caller wins; and the global opt-out skips
// the merge entirely.
func TestApplyPromptLoopDefaultsToStartInput_FreshContext(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{
			Trigger:      []string{"onTasks"},
			FreshContext: boolPtr(true),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopFreshContext == nil {
			t.Fatal("LoopFreshContext should have been filled from frontmatter")
		}
		if *input.LoopFreshContext != true {
			t.Errorf("LoopFreshContext = %v, want true", *input.LoopFreshContext)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := false
		input := &ConversationStartInput{LoopFreshContext: &callerVal}
		pl := &config.PromptLoop{
			Trigger:      []string{"onTasks"},
			FreshContext: boolPtr(true),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopFreshContext == nil || *input.LoopFreshContext != false {
			t.Errorf("caller's explicit false should have been preserved, got %v", input.LoopFreshContext)
		}
	})

	t.Run("nil frontmatter leaves caller nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Trigger: []string{"onTasks"}} // FreshContext unset
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopFreshContext != nil {
			t.Errorf("LoopFreshContext should remain nil when frontmatter is silent, got %v", *input.LoopFreshContext)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationStartInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger:      []string{"onTasks"},
			FreshContext: boolPtr(true),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopFreshContext != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopFreshContext)
		}
	})
}

// TestApplyPromptLoopDefaultsToUpdateInput_FreshContext is the update-tool
// equivalent; the semantics mirror the start-input helper.
func TestApplyPromptLoopDefaultsToUpdateInput_FreshContext(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{
			Trigger:      []string{"onTasks"},
			FreshContext: boolPtr(true),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopFreshContext == nil || *input.LoopFreshContext != true {
			t.Errorf("LoopFreshContext should have been filled with true, got %v", input.LoopFreshContext)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := false
		input := &ConversationUpdateInput{LoopFreshContext: &callerVal}
		pl := &config.PromptLoop{
			Trigger:      []string{"onTasks"},
			FreshContext: boolPtr(true),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopFreshContext == nil || *input.LoopFreshContext != false {
			t.Errorf("caller's explicit false should have been preserved, got %v", input.LoopFreshContext)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger:      []string{"onTasks"},
			FreshContext: boolPtr(true),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopFreshContext != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopFreshContext)
		}
	})
}

// TestApplyPromptLoopDefaultsToStartInput_RunOnStart mirrors the
// CoalesceDuringBusy tests for the runOnStart frontmatter field (mitto-ystk):
// when the seeded prompt sets it and the caller did not, the value flows
// through to ConversationStartInput; explicit caller wins; and the global
// opt-out skips the merge entirely.
func TestApplyPromptLoopDefaultsToStartInput_RunOnStart(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{
			Trigger:    []string{"onTasks"},
			RunOnStart: boolPtr(true),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopRunOnStart == nil || *input.LoopRunOnStart != true {
			t.Errorf("LoopRunOnStart should have been filled with true, got %v", input.LoopRunOnStart)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := false
		input := &ConversationStartInput{LoopRunOnStart: &callerVal}
		pl := &config.PromptLoop{
			Trigger:    []string{"onTasks"},
			RunOnStart: boolPtr(true),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopRunOnStart == nil || *input.LoopRunOnStart != false {
			t.Errorf("caller's explicit false should have been preserved, got %v", input.LoopRunOnStart)
		}
	})

	t.Run("nil frontmatter leaves caller nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Trigger: []string{"onTasks"}} // RunOnStart unset
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopRunOnStart != nil {
			t.Errorf("LoopRunOnStart should remain nil when frontmatter is silent, got %v", *input.LoopRunOnStart)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationStartInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger:    []string{"onTasks"},
			RunOnStart: boolPtr(true),
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopRunOnStart != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopRunOnStart)
		}
	})
}

// TestPromptLoopDefaultEnabled pins the mitto-ydj predicate directly: it
// mirrors the frontend's promptLoopInitialState (web/static/utils/prompts.js)
// rather than the bead's originally-proposed "nil-or-false" reading — nil
// Default under mode:optional means "on by default", only an explicit
// *false disables.
func TestPromptLoopDefaultEnabled(t *testing.T) {
	cases := []struct {
		name string
		pl   *config.PromptLoop
		want bool
	}{
		{"nil PromptLoop => true", nil, true},
		{"mode always, no default => true", &config.PromptLoop{Mode: config.PromptLoopModeAlways}, true},
		{"mode empty (defaults to always), no default => true", &config.PromptLoop{}, true},
		{"mode always with default:false ignored => true", &config.PromptLoop{Mode: config.PromptLoopModeAlways, Default: boolPtr(false)}, true},
		{"mode optional, default nil => true (on by default)", &config.PromptLoop{Mode: config.PromptLoopModeOptional}, true},
		{"mode optional, default:true => true", &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(true)}, true},
		{"mode optional, default:false => false", &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptLoopDefaultEnabled(tc.pl); got != tc.want {
				t.Errorf("promptLoopDefaultEnabled(%+v) = %v, want %v", tc.pl, got, tc.want)
			}
		})
	}
}

// TestApplyPromptLoopDefaultsToStartInput_Enabled covers the mitto-ydj merge:
// a resolved prompt's loop:mode/default frontmatter fills LoopEnabled only
// when the caller left loop_enabled unset, and only ever writes false —
// never true (the enabled:=true default at the ConversationStart call site
// already handles the "on" case, so this clause is strictly subtractive).
func TestApplyPromptLoopDefaultsToStartInput_Enabled(t *testing.T) {
	t.Run("mode optional, default:false, caller unset => filled false", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopEnabled == nil || *input.LoopEnabled != false {
			t.Errorf("LoopEnabled = %v, want *false", input.LoopEnabled)
		}
	})

	t.Run("mode optional, default nil, caller unset => left nil (enabled:=true default applies downstream)", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopEnabled != nil {
			t.Errorf("LoopEnabled should remain nil when frontmatter default is nil (on-by-default), got %v", *input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:true, caller unset => left nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(true)}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopEnabled != nil {
			t.Errorf("LoopEnabled should remain nil when frontmatter default is true, got %v", *input.LoopEnabled)
		}
	})

	t.Run("mode always, default:false ignored, caller unset => left nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeAlways, Default: boolPtr(false)}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopEnabled != nil {
			t.Errorf("LoopEnabled should remain nil when mode is always (default not applicable), got %v", *input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:false, caller explicit true wins", func(t *testing.T) {
		callerVal := true
		input := &ConversationStartInput{LoopEnabled: &callerVal}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopEnabled == nil || *input.LoopEnabled != true {
			t.Errorf("caller's explicit true should have been preserved, got %v", input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:false, caller explicit false preserved", func(t *testing.T) {
		callerVal := false
		input := &ConversationStartInput{LoopEnabled: &callerVal}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopEnabled == nil || *input.LoopEnabled != false {
			t.Errorf("caller's explicit false should have been preserved, got %v", input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:false, opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationStartInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopEnabled != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopEnabled)
		}
	})
}

// TestApplyPromptLoopDefaultsToUpdateInput_Enabled is the update-tool
// equivalent; the semantics mirror the start-input helper exactly (both
// LoopEnabled fields are *bool, so the nil-check is identical).
func TestApplyPromptLoopDefaultsToUpdateInput_Enabled(t *testing.T) {
	t.Run("mode optional, default:false, caller unset => filled false", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopEnabled == nil || *input.LoopEnabled != false {
			t.Errorf("LoopEnabled = %v, want *false", input.LoopEnabled)
		}
	})

	t.Run("mode optional, default nil, caller unset => left nil", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopEnabled != nil {
			t.Errorf("LoopEnabled should remain nil when frontmatter default is nil (on-by-default), got %v", *input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:true, caller unset => left nil", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(true)}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopEnabled != nil {
			t.Errorf("LoopEnabled should remain nil when frontmatter default is true, got %v", *input.LoopEnabled)
		}
	})

	t.Run("mode always, default:false ignored, caller unset => left nil", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeAlways, Default: boolPtr(false)}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopEnabled != nil {
			t.Errorf("LoopEnabled should remain nil when mode is always (default not applicable), got %v", *input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:false, caller explicit true wins", func(t *testing.T) {
		callerVal := true
		input := &ConversationUpdateInput{LoopEnabled: &callerVal}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopEnabled == nil || *input.LoopEnabled != true {
			t.Errorf("caller's explicit true should have been preserved, got %v", input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:false, caller explicit false preserved", func(t *testing.T) {
		callerVal := false
		input := &ConversationUpdateInput{LoopEnabled: &callerVal}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopEnabled == nil || *input.LoopEnabled != false {
			t.Errorf("caller's explicit false should have been preserved, got %v", input.LoopEnabled)
		}
	})

	t.Run("mode optional, default:false, opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{Mode: config.PromptLoopModeOptional, Default: boolPtr(false)}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopEnabled != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopEnabled)
		}
	})
}

// TestApplyPromptLoopDefaultsToUpdateInput_RunOnStart is the update-tool
// equivalent; the semantics mirror the start-input helper.
func TestApplyPromptLoopDefaultsToUpdateInput_RunOnStart(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{
			Trigger:    []string{"onTasks"},
			RunOnStart: boolPtr(true),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopRunOnStart == nil || *input.LoopRunOnStart != true {
			t.Errorf("LoopRunOnStart should have been filled with true, got %v", input.LoopRunOnStart)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		callerVal := false
		input := &ConversationUpdateInput{LoopRunOnStart: &callerVal}
		pl := &config.PromptLoop{
			Trigger:    []string{"onTasks"},
			RunOnStart: boolPtr(true),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopRunOnStart == nil || *input.LoopRunOnStart != false {
			t.Errorf("caller's explicit false should have been preserved, got %v", input.LoopRunOnStart)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger:    []string{"onTasks"},
			RunOnStart: boolPtr(true),
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopRunOnStart != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", *input.LoopRunOnStart)
		}
	})
}
