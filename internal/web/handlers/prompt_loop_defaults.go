// Package handlers auto-apply of a seeded prompt's loop: frontmatter block to
// the REST PUT /api/sessions/{id}/loop request body (mitto-le4.1). Mirrors the
// MCP helpers in internal/mcpserver/prompt_loop_defaults.go so all three
// surfaces (MCP new, MCP update, REST PUT) fill empty caller fields from the
// resolved prompt's loop: block using the same precedence rules.
package handlers

import (
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// applyPromptLoopDefaultsToLoopPrompt fills empty fields on lp from the seeded
// prompt's loop: frontmatter block. Explicit non-zero / non-nil caller values
// always win — only zero / nil fields are filled. optOut, when non-nil and
// *false, disables the merge entirely (mirrors LoopApplyPromptDefaults on the
// request DTO and loop_apply_prompt_defaults on the MCP tools).
//
// This is the REST-PUT equivalent of applyPromptLoopDefaultsToStartInput /
// applyPromptLoopDefaultsToUpdateInput in internal/mcpserver. The field list
// and precedence rules MUST stay in sync across all three helpers — see the
// authoritative merge list in internal/mcpserver/prompt_loop_defaults.go.
func applyPromptLoopDefaultsToLoopPrompt(lp *session.LoopPrompt, pl *configPkg.PromptLoop, optOut *bool) {
	if lp == nil || pl == nil {
		return
	}
	if optOut != nil && !*optOut {
		return
	}

	// Trigger + on-completion fields.
	if lp.Trigger == "" && pl.Trigger != "" {
		lp.Trigger = session.LoopTrigger(pl.Trigger)
	}
	if lp.DelaySeconds == 0 && pl.Delay > 0 {
		lp.DelaySeconds = pl.Delay
	}

	// Frequency (only meaningful for schedule trigger, but fill unconditionally —
	// downstream code ignores frequency for onCompletion/onTasks).
	if lp.Frequency.Value == 0 && pl.Value > 0 {
		lp.Frequency.Value = pl.Value
	}
	if lp.Frequency.Unit == "" && pl.Unit != "" {
		lp.Frequency.Unit = session.FrequencyUnit(pl.Unit)
	}
	if lp.Frequency.At == "" && pl.At != "" {
		lp.Frequency.At = pl.At
	}

	// Caps.
	if lp.MaxIterations == 0 && pl.MaxIterations > 0 {
		lp.MaxIterations = pl.MaxIterations
	}
	if lp.MaxDurationSeconds == 0 {
		if secs, err := pl.MaxDurationSeconds(); err == nil && secs > 0 {
			lp.MaxDurationSeconds = secs
		}
	}

	// onTasks condition.
	if lp.Condition == "" && pl.Condition != "" {
		lp.Condition = pl.Condition
	}

	// *bool fields — pointer-presence semantics: both nil and *false are
	// meaningful frontmatter values, so check the pointer for presence rather
	// than dereferencing. Mirrors the MCP helper rules.
	if lp.CoalesceDuringBusy == nil && pl.CoalesceDuringBusy != nil {
		v := *pl.CoalesceDuringBusy
		lp.CoalesceDuringBusy = &v
	}
	if !lp.FreshContext && pl.FreshContext != nil && *pl.FreshContext {
		// LoopPrompt.FreshContext is a plain bool (not *bool), so we can only
		// fill from frontmatter when the request left it false AND frontmatter
		// says true. Frontmatter false is a no-op. This is the best we can do
		// given the value-type constraint on the persisted field.
		lp.FreshContext = true
	}
	if lp.RunOnStart == nil && pl.RunOnStart != nil {
		v := *pl.RunOnStart
		lp.RunOnStart = &v
	}
}
