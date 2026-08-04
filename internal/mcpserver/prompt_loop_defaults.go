// Package mcpserver auto-apply of a seeded prompt's loop: frontmatter block to
// the ConversationStart/ConversationUpdate inputs (mitto-r7y). When a named
// prompt is resolved and it carries a loop: block, its fields fill any loop_*
// fields the caller did not set explicitly. Explicit caller arguments always
// win. Callers can disable the merge with loop_apply_prompt_defaults=false.
package mcpserver

import (
	"github.com/inercia/mitto/internal/config"
)

// promptLoopDefaultEnabled reports whether a prompt's loop: frontmatter implies
// the loop should start enabled, per pl.Mode/pl.Default (mitto-ydj). Mirrors the
// frontend's promptLoopInitialState (web/static/utils/prompts.js):
//
//   - Mode != "optional" (i.e. "always" or absent): always enabled — Default is
//     not applicable and, if present, is ignored (with a load-time lint warning
//     from ValidatePromptLoop).
//   - Mode == "optional": nil or *true => enabled (on by default); *false =>
//     disabled. This is a deliberate reversal of a naive "Default nil => off"
//     reading — nil/absent means "on by default, user-toggleable", matching
//     config.PromptLoop's own doc comment and the frontend.
func promptLoopDefaultEnabled(pl *config.PromptLoop) bool {
	if pl == nil || pl.Mode != config.PromptLoopModeOptional {
		return true
	}
	return pl.Default == nil || *pl.Default
}

// applyPromptLoopDefaultsToStartInput merges a seeded prompt's loop: frontmatter
// into the ConversationStartInput. Only fields the caller did not set are
// filled; explicit non-zero / non-nil caller values win.
//
// seedPromptName is the resolved prompt name; used as the loop body default
// (self-referential loop) when the caller passed no loop_prompt / loop_prompt_name.
// A nil pl or a nil optOut==false disables the merge.
func applyPromptLoopDefaultsToStartInput(input *ConversationStartInput, pl *config.PromptLoop, seedPromptName string) {
	if input == nil || pl == nil {
		return
	}
	if input.LoopApplyPromptDefaults != nil && !*input.LoopApplyPromptDefaults {
		return
	}

	// Loop body default: when caller passed neither loop_prompt nor
	// loop_prompt_name, the seed prompt drives its own loop (self-referential).
	if input.LoopPrompt == "" && input.LoopPromptName == "" && seedPromptName != "" {
		input.LoopPromptName = seedPromptName
	}

	// Trigger + on-completion fields
	if input.LoopTrigger == "" && pl.Trigger != "" {
		input.LoopTrigger = pl.Trigger
	}
	if input.LoopCompletionDelaySeconds == nil && pl.Delay > 0 {
		d := pl.Delay
		input.LoopCompletionDelaySeconds = &d
	}

	// Frequency (only meaningful for schedule trigger, but fill unconditionally —
	// downstream code ignores frequency for onCompletion/onTasks).
	if input.LoopFrequencyValue == 0 && pl.Value > 0 {
		input.LoopFrequencyValue = pl.Value
	}
	if input.LoopFrequencyUnit == "" && pl.Unit != "" {
		input.LoopFrequencyUnit = pl.Unit
	}
	if input.LoopFrequencyAt == "" && pl.At != "" {
		input.LoopFrequencyAt = pl.At
	}

	// Caps
	if input.LoopMaxIterations == nil && pl.MaxIterations > 0 {
		m := pl.MaxIterations
		input.LoopMaxIterations = &m
	}
	if input.LoopMaxDurationSeconds == nil {
		if secs, err := pl.MaxDurationSeconds(); err == nil && secs > 0 {
			input.LoopMaxDurationSeconds = &secs
		}
	}

	// onTasks condition
	if input.LoopCondition == "" && pl.Condition != "" {
		input.LoopCondition = pl.Condition
	}

	// onTasks opt-in re-fire (mitto-dmb). Fill only when the caller did not
	// explicitly set it. Both nil and *false are meaningful frontmatter values,
	// so we check the pointer for presence rather than dereferencing.
	if input.LoopCoalesceDuringBusy == nil && pl.CoalesceDuringBusy != nil {
		v := *pl.CoalesceDuringBusy
		input.LoopCoalesceDuringBusy = &v
	}

	// Fresh-context per run. Same pointer-presence semantics as CoalesceDuringBusy.
	if input.LoopFreshContext == nil && pl.FreshContext != nil {
		v := *pl.FreshContext
		input.LoopFreshContext = &v
	}

	// Boot pulse (mitto-ystk). Same pointer-presence semantics as CoalesceDuringBusy.
	if input.LoopRunOnStart == nil && pl.RunOnStart != nil {
		v := *pl.RunOnStart
		input.LoopRunOnStart = &v
	}

	// Initial enabled state (mitto-ydj). Only fills when the caller left
	// loop_enabled unset, and only ever writes false — the "on" case is already
	// handled by the enabled := true default at the ConversationStart call site,
	// so this clause is strictly subtractive: it never turns a loop on that the
	// caller/default path wouldn't have turned on anyway.
	if input.LoopEnabled == nil && !promptLoopDefaultEnabled(pl) {
		v := false
		input.LoopEnabled = &v
	}
}

// applyPromptLoopDefaultsToUpdateInput is the update-tool equivalent. Because
// every loop_* field on ConversationUpdateInput is a pointer, "unset" is
// unambiguously nil, and the merge fills only nil fields.
func applyPromptLoopDefaultsToUpdateInput(input *ConversationUpdateInput, pl *config.PromptLoop) {
	if input == nil || pl == nil {
		return
	}
	if input.LoopApplyPromptDefaults != nil && !*input.LoopApplyPromptDefaults {
		return
	}

	// Trigger + on-completion fields
	if input.LoopTrigger == nil && pl.Trigger != "" {
		t := pl.Trigger
		input.LoopTrigger = &t
	}
	if input.LoopCompletionDelaySeconds == nil && pl.Delay > 0 {
		d := pl.Delay
		input.LoopCompletionDelaySeconds = &d
	}

	// Frequency
	if input.LoopFrequencyValue == nil && pl.Value > 0 {
		v := pl.Value
		input.LoopFrequencyValue = &v
	}
	if input.LoopFrequencyUnit == nil && pl.Unit != "" {
		u := pl.Unit
		input.LoopFrequencyUnit = &u
	}
	if input.LoopFrequencyAt == nil && pl.At != "" {
		a := pl.At
		input.LoopFrequencyAt = &a
	}

	// Caps
	if input.LoopMaxIterations == nil && pl.MaxIterations > 0 {
		m := pl.MaxIterations
		input.LoopMaxIterations = &m
	}
	if input.LoopMaxDurationSeconds == nil {
		if secs, err := pl.MaxDurationSeconds(); err == nil && secs > 0 {
			input.LoopMaxDurationSeconds = &secs
		}
	}

	// onTasks condition
	if input.LoopCondition == nil && pl.Condition != "" {
		c := pl.Condition
		input.LoopCondition = &c
	}

	// onTasks opt-in re-fire (mitto-dmb). Same rules as the start-input helper.
	if input.LoopCoalesceDuringBusy == nil && pl.CoalesceDuringBusy != nil {
		v := *pl.CoalesceDuringBusy
		input.LoopCoalesceDuringBusy = &v
	}

	// Fresh-context per run. Same rules as the start-input helper.
	if input.LoopFreshContext == nil && pl.FreshContext != nil {
		v := *pl.FreshContext
		input.LoopFreshContext = &v
	}

	// Boot pulse (mitto-ystk). Same rules as the start-input helper.
	if input.LoopRunOnStart == nil && pl.RunOnStart != nil {
		v := *pl.RunOnStart
		input.LoopRunOnStart = &v
	}

	// Initial enabled state (mitto-ydj). Same rules as the start-input helper:
	// fills only when the caller left loop_enabled unset, and only ever writes
	// false. Callers of this helper must capture whether loop_enabled was
	// caller-set BEFORE calling it if they need to distinguish "caller explicitly
	// disabled" from "frontmatter disabled" afterwards (see the call site in
	// tools_conversation_lifecycle.go, which uses this for the
	// StoppedReasonDisabledByAgent bookkeeping).
	if input.LoopEnabled == nil && !promptLoopDefaultEnabled(pl) {
		v := false
		input.LoopEnabled = &v
	}
}
