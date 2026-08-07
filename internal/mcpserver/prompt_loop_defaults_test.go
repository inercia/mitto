package mcpserver

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
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

// TestApplyPromptLoopDefaultsToStartInput_ChildEvents covers the mitto-987y.6
// merge: when the seeded prompt carries loop.onChild.when in its frontmatter
// and the caller did not set loop_child_events, the value flows through to
// ConversationStartInput. Mirrors the CoalesceDuringBusy/FreshContext tests.
func TestApplyPromptLoopDefaultsToStartInput_ChildEvents(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks", "onChild"},
			OnChild: &config.PromptLoopOnChild{When: []string{"anyDeleted"}},
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if len(input.LoopChildEvents) != 1 || input.LoopChildEvents[0] != "anyDeleted" {
			t.Errorf("LoopChildEvents = %v, want [anyDeleted]", input.LoopChildEvents)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		input := &ConversationStartInput{LoopChildEvents: []string{"anyEndResponse"}}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks", "onChild"},
			OnChild: &config.PromptLoopOnChild{When: []string{"anyDeleted"}},
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if len(input.LoopChildEvents) != 1 || input.LoopChildEvents[0] != "anyEndResponse" {
			t.Errorf("caller's explicit value should have been preserved, got %v", input.LoopChildEvents)
		}
	})

	t.Run("nil frontmatter leaves caller nil", func(t *testing.T) {
		input := &ConversationStartInput{}
		pl := &config.PromptLoop{Trigger: []string{"onTasks", "onChild"}} // OnChild unset
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopChildEvents != nil {
			t.Errorf("LoopChildEvents should remain nil when frontmatter is silent, got %v", input.LoopChildEvents)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationStartInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks", "onChild"},
			OnChild: &config.PromptLoopOnChild{When: []string{"anyDeleted"}},
		}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopChildEvents != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", input.LoopChildEvents)
		}
	})
}

// TestApplyPromptLoopDefaultsToUpdateInput_ChildEvents is the update-tool
// equivalent; the semantics mirror the start-input helper exactly (both
// LoopChildEvents fields are []string, so the nil-check is identical).
func TestApplyPromptLoopDefaultsToUpdateInput_ChildEvents(t *testing.T) {
	t.Run("frontmatter fills unset caller", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks", "onChild"},
			OnChild: &config.PromptLoopOnChild{When: []string{"anyDeleted"}},
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if len(input.LoopChildEvents) != 1 || input.LoopChildEvents[0] != "anyDeleted" {
			t.Errorf("LoopChildEvents = %v, want [anyDeleted]", input.LoopChildEvents)
		}
	})

	t.Run("explicit caller wins over frontmatter", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopChildEvents: []string{"anyEndResponse"}}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks", "onChild"},
			OnChild: &config.PromptLoopOnChild{When: []string{"anyDeleted"}},
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if len(input.LoopChildEvents) != 1 || input.LoopChildEvents[0] != "anyEndResponse" {
			t.Errorf("caller's explicit value should have been preserved, got %v", input.LoopChildEvents)
		}
	})

	t.Run("opt-out disables the whole merge", func(t *testing.T) {
		input := &ConversationUpdateInput{LoopApplyPromptDefaults: boolPtr(false)}
		pl := &config.PromptLoop{
			Trigger: []string{"onTasks", "onChild"},
			OnChild: &config.PromptLoopOnChild{When: []string{"anyDeleted"}},
		}
		applyPromptLoopDefaultsToUpdateInput(input, pl)
		if input.LoopChildEvents != nil {
			t.Errorf("opt-out should have skipped the merge, got %v", input.LoopChildEvents)
		}
	})
}

// TestParseLoopTriggerList_OnChild pins the mitto-987y.6 whitelist fix:
// parseLoopTriggerList must accept "onChild" (previously rejected — see the
// switch statement's pre-fix omission) both alone in a list with another
// trigger and combined with others.
func TestParseLoopTriggerList_OnChild(t *testing.T) {
	got, err := parseLoopTriggerList("onTasks,onChild")
	if err != nil {
		t.Fatalf("parseLoopTriggerList(%q) error = %v, want nil", "onTasks,onChild", err)
	}
	want := []session.LoopTrigger{session.TriggerOnTasks, session.TriggerOnChild}
	if len(got) != len(want) {
		t.Fatalf("parseLoopTriggerList(%q) = %v, want %v", "onTasks,onChild", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("parseLoopTriggerList(%q)[%d] = %q, want %q", "onTasks,onChild", i, got[i], want[i])
		}
	}
}

// TestParseLoopTriggerList covers the flat-MCP-surface fallback (mitto-r6j.5):
// loop_trigger is a plain string that accepts either a single trigger or a
// comma-separated list.
func TestParseLoopTriggerList(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []session.LoopTrigger
		wantErr bool
	}{
		{name: "empty returns nil", raw: "", want: nil},
		{name: "single trigger", raw: "schedule", want: []session.LoopTrigger{session.TriggerSchedule}},
		{
			name: "comma-separated list",
			raw:  "schedule,onCompletion",
			want: []session.LoopTrigger{session.TriggerSchedule, session.TriggerOnCompletion},
		},
		{
			name: "comma-separated list with spaces",
			raw:  "schedule, onTasks , onCompletion",
			want: []session.LoopTrigger{session.TriggerSchedule, session.TriggerOnTasks, session.TriggerOnCompletion},
		},
		{
			name: "duplicates deduped preserving first occurrence order",
			raw:  "onTasks,schedule,onTasks",
			want: []session.LoopTrigger{session.TriggerOnTasks, session.TriggerSchedule},
		},
		{name: "invalid trigger errors", raw: "not-a-trigger", wantErr: true},
		{name: "one invalid entry in a list errors", raw: "schedule,bogus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLoopTriggerList(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseLoopTriggerList(%q) error = nil, want an error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLoopTriggerList(%q) error = %v, want nil", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseLoopTriggerList(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseLoopTriggerList(%q)[%d] = %q, want %q", tt.raw, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestApplyPromptLoopDefaults_MultiTriggerAndSettleWindow verifies that a
// prompt declaring trigger: [schedule, onTasks] fills the WHOLE list (as a
// comma-joined loop_trigger string, matching the flat MCP surface) into both
// ConversationStartInput and ConversationUpdateInput when the caller left it
// unset, and that onTasks.settleWindow reaches LoopSettleWindowSeconds on
// both (mitto-r6j.5).
func TestApplyPromptLoopDefaults_MultiTriggerAndSettleWindow(t *testing.T) {
	pl := &config.PromptLoop{
		Trigger:  []string{"schedule", "onTasks"},
		Schedule: &config.PromptLoopSchedule{Value: 1, Unit: "hours"},
		OnTasks:  &config.PromptLoopOnTasks{SettleWindow: 30},
	}

	t.Run("start input", func(t *testing.T) {
		input := &ConversationStartInput{}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")

		triggers, err := parseLoopTriggerList(input.LoopTrigger)
		if err != nil {
			t.Fatalf("parseLoopTriggerList(%q) error = %v", input.LoopTrigger, err)
		}
		if len(triggers) != 2 || triggers[0] != session.TriggerSchedule || triggers[1] != session.TriggerOnTasks {
			t.Errorf("resolved triggers = %v, want [schedule onTasks]", triggers)
		}
		if input.LoopSettleWindowSeconds == nil || *input.LoopSettleWindowSeconds != 30 {
			t.Errorf("LoopSettleWindowSeconds = %v, want *30", input.LoopSettleWindowSeconds)
		}
	})

	t.Run("update input", func(t *testing.T) {
		input := &ConversationUpdateInput{}
		applyPromptLoopDefaultsToUpdateInput(input, pl)

		if input.LoopTrigger == nil {
			t.Fatal("LoopTrigger should have been filled from frontmatter")
		}
		triggers, err := parseLoopTriggerList(*input.LoopTrigger)
		if err != nil {
			t.Fatalf("parseLoopTriggerList(%q) error = %v", *input.LoopTrigger, err)
		}
		if len(triggers) != 2 || triggers[0] != session.TriggerSchedule || triggers[1] != session.TriggerOnTasks {
			t.Errorf("resolved triggers = %v, want [schedule onTasks]", triggers)
		}
		if input.LoopSettleWindowSeconds == nil || *input.LoopSettleWindowSeconds != 30 {
			t.Errorf("LoopSettleWindowSeconds = %v, want *30", input.LoopSettleWindowSeconds)
		}
	})

	t.Run("explicit caller loop_trigger wins over frontmatter on start input", func(t *testing.T) {
		input := &ConversationStartInput{LoopTrigger: "onCompletion"}
		applyPromptLoopDefaultsToStartInput(input, pl, "seed-prompt")
		if input.LoopTrigger != "onCompletion" {
			t.Errorf("LoopTrigger = %q, want caller's explicit %q preserved", input.LoopTrigger, "onCompletion")
		}
	})
}

// TestApplyPromptLoopDefaults_SyncGuard is a table-driven cross-helper sync
// guard: it asserts that for every PromptLoop frontmatter field currently
// merged into ConversationStartInput, the ConversationUpdateInput helper
// merges the same field to the same value. If the two MCP helpers drift
// (e.g. a new frontmatter field is wired into one but not the other), one
// side of the table will trip the expectation.
//
// This test intentionally uses the exhaustive PromptLoop fixture below
// (every mergeable field set) rather than per-field sub-tests: a drift bug
// is most likely to appear as "helper A merges field X, helper B does not",
// and the exhaustive fixture surfaces that as a single failing assertion
// with a clear diff.
//
// Sibling test in internal/web/handlers pins the third helper
// (applyPromptLoopDefaultsToLoopPrompt) against the same fixture so all
// three merge points stay in lock-step.
func TestApplyPromptLoopDefaults_SyncGuard(t *testing.T) {
	// The exhaustive fixture: every PromptLoop field the helpers currently
	// merge, with distinguishable non-zero values so an assertion can pin
	// exactly which field was mis-merged.
	pl := &config.PromptLoop{
		Trigger:      []string{"schedule", "onCompletion", "onTasks", "onChild"},
		Schedule:     &config.PromptLoopSchedule{Value: 7, Unit: "hours", At: ""},
		OnCompletion: &config.PromptLoopOnCompletion{Delay: 45},
		OnTasks: &config.PromptLoopOnTasks{
			Condition:          "e2e:pr:closed",
			ConditionPreset:    "pr-closed",
			Cooldown:           90,
			SettleWindow:       33,
			CoalesceDuringBusy: boolPtr(false),
		},
		OnChild:       &config.PromptLoopOnChild{When: []string{"anyDeleted"}},
		MaxIterations: 11,
		MaxDuration:   "2h",
		FreshContext:  boolPtr(true),
		RunOnStart:    boolPtr(true),
	}

	t.Run("start input merges every field", func(t *testing.T) {
		in := &ConversationStartInput{}
		applyPromptLoopDefaultsToStartInput(in, pl, "seed")

		// Trigger: whole list, comma-joined (mitto-r6j.5).
		if in.LoopTrigger != "schedule,onCompletion,onTasks,onChild" {
			t.Errorf("LoopTrigger = %q, want the whole comma-joined list", in.LoopTrigger)
		}
		if in.LoopCompletionDelaySeconds == nil || *in.LoopCompletionDelaySeconds != 45 {
			t.Errorf("LoopCompletionDelaySeconds = %v, want *45", in.LoopCompletionDelaySeconds)
		}
		if in.LoopSettleWindowSeconds == nil || *in.LoopSettleWindowSeconds != 33 {
			t.Errorf("LoopSettleWindowSeconds = %v, want *33", in.LoopSettleWindowSeconds)
		}
		if in.LoopFrequencyValue != 7 {
			t.Errorf("LoopFrequencyValue = %d, want 7", in.LoopFrequencyValue)
		}
		if in.LoopFrequencyUnit != "hours" {
			t.Errorf("LoopFrequencyUnit = %q, want hours", in.LoopFrequencyUnit)
		}
		if in.LoopMaxIterations == nil || *in.LoopMaxIterations != 11 {
			t.Errorf("LoopMaxIterations = %v, want *11", in.LoopMaxIterations)
		}
		if in.LoopMaxDurationSeconds == nil || *in.LoopMaxDurationSeconds != 7200 {
			t.Errorf("LoopMaxDurationSeconds = %v, want *7200 (2h)", in.LoopMaxDurationSeconds)
		}
		if in.LoopCondition != "e2e:pr:closed" {
			t.Errorf("LoopCondition = %q, want %q", in.LoopCondition, "e2e:pr:closed")
		}
		if in.LoopCoalesceDuringBusy == nil || *in.LoopCoalesceDuringBusy != false {
			t.Errorf("LoopCoalesceDuringBusy = %v, want *false", in.LoopCoalesceDuringBusy)
		}
		if in.LoopFreshContext == nil || *in.LoopFreshContext != true {
			t.Errorf("LoopFreshContext = %v, want *true", in.LoopFreshContext)
		}
		if in.LoopRunOnStart == nil || *in.LoopRunOnStart != true {
			t.Errorf("LoopRunOnStart = %v, want *true", in.LoopRunOnStart)
		}
		if len(in.LoopChildEvents) != 1 || in.LoopChildEvents[0] != "anyDeleted" {
			t.Errorf("LoopChildEvents = %v, want [anyDeleted]", in.LoopChildEvents)
		}
	})

	t.Run("update input merges every field", func(t *testing.T) {
		in := &ConversationUpdateInput{}
		applyPromptLoopDefaultsToUpdateInput(in, pl)

		if in.LoopTrigger == nil || *in.LoopTrigger != "schedule,onCompletion,onTasks,onChild" {
			t.Errorf("LoopTrigger = %v, want the whole comma-joined list", in.LoopTrigger)
		}
		if in.LoopCompletionDelaySeconds == nil || *in.LoopCompletionDelaySeconds != 45 {
			t.Errorf("LoopCompletionDelaySeconds = %v, want *45", in.LoopCompletionDelaySeconds)
		}
		if in.LoopSettleWindowSeconds == nil || *in.LoopSettleWindowSeconds != 33 {
			t.Errorf("LoopSettleWindowSeconds = %v, want *33", in.LoopSettleWindowSeconds)
		}
		if in.LoopFrequencyValue == nil || *in.LoopFrequencyValue != 7 {
			t.Errorf("LoopFrequencyValue = %v, want *7", in.LoopFrequencyValue)
		}
		if in.LoopFrequencyUnit == nil || *in.LoopFrequencyUnit != "hours" {
			t.Errorf("LoopFrequencyUnit = %v, want *hours", in.LoopFrequencyUnit)
		}
		if in.LoopMaxIterations == nil || *in.LoopMaxIterations != 11 {
			t.Errorf("LoopMaxIterations = %v, want *11", in.LoopMaxIterations)
		}
		if in.LoopMaxDurationSeconds == nil || *in.LoopMaxDurationSeconds != 7200 {
			t.Errorf("LoopMaxDurationSeconds = %v, want *7200 (2h)", in.LoopMaxDurationSeconds)
		}
		if in.LoopCondition == nil || *in.LoopCondition != "e2e:pr:closed" {
			t.Errorf("LoopCondition = %v, want *%q", in.LoopCondition, "e2e:pr:closed")
		}
		if in.LoopCoalesceDuringBusy == nil || *in.LoopCoalesceDuringBusy != false {
			t.Errorf("LoopCoalesceDuringBusy = %v, want *false", in.LoopCoalesceDuringBusy)
		}
		if in.LoopFreshContext == nil || *in.LoopFreshContext != true {
			t.Errorf("LoopFreshContext = %v, want *true", in.LoopFreshContext)
		}
		if in.LoopRunOnStart == nil || *in.LoopRunOnStart != true {
			t.Errorf("LoopRunOnStart = %v, want *true", in.LoopRunOnStart)
		}
		if len(in.LoopChildEvents) != 1 || in.LoopChildEvents[0] != "anyDeleted" {
			t.Errorf("LoopChildEvents = %v, want [anyDeleted]", in.LoopChildEvents)
		}
	})
}
