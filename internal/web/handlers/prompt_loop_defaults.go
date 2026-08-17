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
//
// Deliberate exception: unlike the MCP helpers, this one does NOT merge
// pl.Mode/pl.Default into an initial enabled state (mitto-ydj). LoopPrompt.Enabled
// on the request DTO (session_loop.go) is a plain bool, so "caller left it unset"
// is indistinguishable from "caller explicitly sent false" — the same value-type
// constraint already noted below for FreshContext. The web UI does not need this
// merge either: it already computes the initial toggle state client-side via
// promptLoopInitialState (web/static/utils/prompts.js) before building the request.
func applyPromptLoopDefaultsToLoopPrompt(lp *session.LoopPrompt, pl *configPkg.PromptLoop, optOut *bool) {
	if lp == nil || pl == nil {
		return
	}
	if optOut != nil && !*optOut {
		return
	}

	// Trigger list + on-completion fields. Fills the WHOLE declared trigger
	// list (mitto-r6j.5) — not just the primary/first entry — when the caller
	// left Triggers unset. Normalize() (called by Set/Update) keeps the legacy
	// scalar Trigger field in sync with Triggers[0] for back-compat.
	if len(lp.Triggers) == 0 && len(pl.Trigger) > 0 {
		triggers := make([]session.LoopTrigger, len(pl.Trigger))
		for i, t := range pl.Trigger {
			triggers[i] = session.LoopTrigger(t)
		}
		lp.Triggers = triggers
	}
	if len(lp.ChildEvents) == 0 && len(pl.ChildEvents()) > 0 {
		events := make([]session.ChildEvent, len(pl.ChildEvents()))
		for i, e := range pl.ChildEvents() {
			events[i] = session.ChildEvent(e)
		}
		lp.ChildEvents = events
	}
	for i := range lp.SlackSubscriptions {
		if lp.SlackSubscriptions[i].EventMode == "" && pl.SlackEventMode() != "" {
			lp.SlackSubscriptions[i].EventMode = session.SlackEventMode(pl.SlackEventMode())
		}
		if lp.SlackSubscriptions[i].ThreadPolicy == "" && pl.SlackThreadPolicy() != "" {
			lp.SlackSubscriptions[i].ThreadPolicy = session.SlackThreadPolicy(pl.SlackThreadPolicy())
		}
	}
	if lp.DelaySeconds == 0 && pl.CompletionDelay() > 0 {
		lp.DelaySeconds = pl.CompletionDelay()
	}

	// Frequency (only meaningful for schedule trigger, but fill unconditionally —
	// downstream code ignores frequency for onCompletion/onTasks).
	if lp.Frequency.Value == 0 && pl.FrequencyValue() > 0 {
		lp.Frequency.Value = pl.FrequencyValue()
	}
	if lp.Frequency.Unit == "" && pl.FrequencyUnit() != "" {
		lp.Frequency.Unit = session.FrequencyUnit(pl.FrequencyUnit())
	}
	if lp.Frequency.At == "" && pl.FrequencyAt() != "" {
		lp.Frequency.At = pl.FrequencyAt()
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

	// onTasks condition + related fields (mitto-r6j.5 closes the
	// ConditionPreset/CooldownSeconds/SettleWindowSeconds gaps — previously
	// only Condition was merged here).
	if lp.Condition == "" && pl.TasksCondition() != "" {
		lp.Condition = pl.TasksCondition()
	}
	if lp.ConditionPreset == "" && pl.TasksConditionPreset() != "" {
		lp.ConditionPreset = pl.TasksConditionPreset()
	}
	if lp.CooldownSeconds == 0 && pl.TasksCooldown() > 0 {
		lp.CooldownSeconds = pl.TasksCooldown()
	}
	if lp.SettleWindowSeconds == nil && pl.TasksSettleWindow() > 0 {
		v := pl.TasksSettleWindow()
		lp.SettleWindowSeconds = &v
	}

	// *bool fields — pointer-presence semantics: both nil and *false are
	// meaningful frontmatter values, so check the pointer for presence rather
	// than dereferencing. Mirrors the MCP helper rules.
	if lp.CoalesceDuringBusy == nil && pl.TasksCoalesceDuringBusy() != nil {
		v := *pl.TasksCoalesceDuringBusy()
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
