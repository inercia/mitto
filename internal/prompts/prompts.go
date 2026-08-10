// Package prompts contains the workspace/settings/file prompts model, the
// on-disk .prompt.yaml loader, the prompts cache and file-system watcher, the
// legacy .md migration helper, the Go text/template renderer used at dispatch
// time, and the WebPrompt DTO with merge/filter helpers. Split out of
// internal/config to shrink that package and let downstream code depend on
// only what it needs (mitto-b8k.3).
package prompts

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/inercia/mitto/internal/cel"
	"github.com/inercia/mitto/internal/prompts/migrate"
)

// loadWarnSeen dedupes the "failed to load prompt file" WARN across reloads:
// key is (absPath, err.Error()) and the WARN fires only the first time a given
// (file, error) pair is observed in this process. Structured PromptLoadError is
// still returned to every caller — the UI toasts (mitto-mqe) and the
// `mitto prompts verify` CLI depend on it, so callers see the failure on every
// reload; only the log emission is bounded (mitto-e8r).
var loadWarnSeen sync.Map

// PromptLoop declares that selecting this prompt should start a loop
// (recurring) conversation instead of a one-time one. A prompt falls into one
// of three categories:
//   - No `loop:` block at all → never loop (unchanged one-time send).
//   - `mode: always` (or `mode` absent) → always loop; not user-toggleable.
//   - `mode: optional` → user-choosable; `default` sets the initial per-send state.
//
// Schema (mitto-r6j): `trigger:` is a list of one or more of
// "schedule" | "onCompletion" | "onTasks", and each trigger's attributes are
// grouped under a nested block of the same name. A block for a trigger not
// listed in `trigger:` is inert (parses fine, warns at load time). Loop-wide
// fields (MaxIterations, MaxDuration, FreshContext, RunOnStart, Mode, Default)
// remain flat siblings of `trigger:` since they apply regardless of which
// trigger fires.
//
// This is a breaking change from the pre-r6j single-trigger flat schema
// (`trigger: onCompletion` + flat `delay`/`condition`/... siblings). The
// mitto-r6j.3 migration registry (internal/prompts/migrate, run from
// ParsePromptFile ahead of the yaml.Unmarshal below) rewrites an old-form
// loop: block in memory before validation, so loading one only logs a WARN
// — LoadPromptFile additionally persists the rewritten form back to disk
// (mitto-r6j.4 covers the one-time builtin prompt rewrite). A leftover flat
// key that reaches this type's UnmarshalYAML directly (i.e. any caller that
// decodes YAML into *PromptLoop without going through the migration
// registry first) is still a parse error naming the migration, as a
// defense-in-depth safety net against the migration being skipped or buggy.
//
// Example frontmatter (always loop, schedule-based):
//
//	loop:
//	  trigger: [schedule]
//	  schedule:
//	    value: 1
//	    unit: hours          # minutes | hours | days
//	    at: "09:00"          # optional, only for days (UTC)
//	  maxIterations: 10      # optional; 0/absent = unlimited scheduled runs
//
// Example frontmatter (always loop, on-completion trigger):
//
//	loop:
//	  trigger: [onCompletion]
//	  onCompletion:
//	    delay: 30              # seconds to wait after agent stops (clamped to floor at consumption)
//	  maxIterations: 20        # optional safety cap
//	  maxDuration: "4h"        # optional wall-clock cap; 0/absent = unlimited
//
// Example frontmatter (always loop, on-tasks trigger with CEL condition):
//
//	loop:
//	  trigger: [onTasks]
//	  onTasks:
//	    condition: 'tasks.exists(t, t.status == "open" && "backend" in t.labels)'
//	  maxIterations: 20
//	  maxDuration: "4h"
//
// Example frontmatter (on-child trigger, fires when a child conversation
// reaches a lifecycle event):
//
//	loop:
//	  trigger: [onChild, onCompletion]
//	  onChild:
//	    when: [anyEndResponse, anyDeleted]   # optional; absent = anyEndResponse + anyDeleted
//	    # anyEndResponse fires on EVERY child turn/idle transition — not a
//	    # "child is done" signal for a multi-phase child driver. anyDeleted
//	    # never fires for archived (rather than deleted) children. Opt into
//	    # anyLoopStopped instead for a real "child driver declared itself
//	    # done" signal: it fires once, when the child's own loop transitions
//	    # into the stopped state (any StoppedReason), and is NOT part of the
//	    # default event set.
//	  onCompletion:
//	    delay: 30
//
// Example frontmatter (multiple simultaneous triggers):
//
//	loop:
//	  trigger: [schedule, onCompletion]
//	  schedule:
//	    value: 1
//	    unit: hours
//	  onCompletion:
//	    delay: 30
//
// Example frontmatter (optionally loop, off by default):
//
//	loop:
//	  mode: optional
//	  default: false         # initial per-send toggle state; nil/absent => true (on)
//	  trigger: [onCompletion]
//	  onCompletion:
//	    delay: 30
type PromptLoop struct {
	// Trigger is the list of triggers that arm this loop; each entry must be
	// one of "schedule", "onCompletion", "onTasks", or "onChild". Duplicates
	// are rejected. Empty/absent defaults to ["schedule"] (preserves pre-r6j
	// implicit-schedule prompts). Validated by ValidatePromptLoop /
	// ValidateLoopTriggers.
	Trigger []string `yaml:"trigger,omitempty" json:"trigger,omitempty"`
	// Schedule groups the frequency-based trigger's attributes. Meaningful only
	// when "schedule" is present in Trigger.
	Schedule *PromptLoopSchedule `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	// OnCompletion groups the event-driven "fire after the agent stops
	// responding" trigger's attributes. Meaningful only when "onCompletion" is
	// present in Trigger.
	OnCompletion *PromptLoopOnCompletion `yaml:"onCompletion,omitempty" json:"onCompletion,omitempty"`
	// OnTasks groups the event-driven "fire when beads/tasks change" trigger's
	// attributes. Meaningful only when "onTasks" is present in Trigger.
	OnTasks *PromptLoopOnTasks `yaml:"onTasks,omitempty" json:"onTasks,omitempty"`
	// OnChild groups the event-driven "fire when a child conversation reaches
	// a lifecycle event" trigger's attributes. Meaningful only when "onChild"
	// is present in Trigger.
	OnChild *PromptLoopOnChild `yaml:"onChild,omitempty" json:"onChild,omitempty"`
	// MaxIterations caps the number of scheduled runs when the conversation is made loop (0 / absent = unlimited).
	MaxIterations int `yaml:"maxIterations,omitempty" json:"maxIterations,omitempty"`
	// MaxDuration is an optional wall-clock cap (e.g. "2h", "30m"); 0/absent = unlimited.
	// Parsed to seconds at the consumption boundary.
	MaxDuration string `yaml:"maxDuration,omitempty" json:"maxDuration,omitempty"`
	// FreshContext, when true, starts each scheduled/re-fired run with a clean
	// agent context: no history injection on resumed sessions, and a fresh ACP
	// session is created per run (see createFreshContextSession). Nil/absent =
	// default false (persistent context). Meaningful for any trigger; primarily
	// used by stateless supervisor loops that re-hydrate from external state on
	// every fire.
	FreshContext *bool `yaml:"freshContext,omitempty" json:"freshContext,omitempty"`
	// RunOnStart, when true, causes the LoopRunner to fire this loop exactly
	// once shortly after Mitto boots (with an anti-flap window guarding against
	// a recent run). Complements onTasks loops (which otherwise only fire on
	// task changes) and lets scheduled/onCompletion loops kick off at startup
	// instead of waiting for the next tick. Nil/absent = default false.
	RunOnStart *bool `yaml:"runOnStart,omitempty" json:"runOnStart,omitempty"`
	// Mode selects whether loop is mandatory or user-toggleable: "always"
	// (default when empty/absent) or "optional". Validated by ValidatePromptLoop.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// Default is the initial per-send toggle state when Mode is "optional".
	// nil/absent => true (on). Ignored (with a lint warning) when Mode is "always".
	Default *bool `yaml:"default,omitempty" json:"default,omitempty"`
}

// PromptLoopSchedule groups the frequency-based ("schedule") trigger's
// attributes, nested under loop.schedule. Unknown keys are rejected (see
// UnmarshalYAML).
type PromptLoopSchedule struct {
	// Value is the number of time units between runs (min 1).
	Value int `yaml:"value" json:"value"`
	// Unit is the time unit: "minutes", "hours", or "days".
	Unit string `yaml:"unit" json:"unit"`
	// At is the time of day in HH:MM format (UTC). Only meaningful for the "days" unit.
	At string `yaml:"at,omitempty" json:"at,omitempty"`
}

// PromptLoopOnCompletion groups the "onCompletion" trigger's attributes,
// nested under loop.onCompletion. Unknown keys are rejected (see
// UnmarshalYAML).
type PromptLoopOnCompletion struct {
	// Delay is the number of seconds to wait after the agent stops responding
	// before the next run. Clamped to a global minimum (default 5s) at the
	// consumption boundary.
	Delay int `yaml:"delay,omitempty" json:"delay,omitempty"`
}

// PromptLoopOnTasks groups the "onTasks" trigger's attributes, nested under
// loop.onTasks. Unknown keys are rejected (see UnmarshalYAML).
type PromptLoopOnTasks struct {
	// Condition is an optional CEL expression gating which beads/task changes
	// fire the run; empty = fire on any change. Validated at parse time in
	// ParsePromptFile.
	Condition string `yaml:"condition,omitempty" json:"condition,omitempty"`
	// ConditionPreset is an optional UI preset id that was compiled into Condition.
	ConditionPreset string `yaml:"conditionPreset,omitempty" json:"conditionPreset,omitempty"`
	// CoalesceDuringBusy controls how the onTasks trigger handles beads changes
	// that arrive while the loop's subtree is busy. Nil/absent or true = silently
	// absorb into the quiescence rebase (default). False = at quiescence, fire
	// once more with the accumulated pre-run→current delta available as
	// .Trigger.OnTasks.*, gated by Layer 0 and the CEL condition (mitto-dmb).
	CoalesceDuringBusy *bool `yaml:"coalesceDuringBusy,omitempty" json:"coalesceDuringBusy,omitempty"`
	// SettleWindow is an optional pre-fire debounce window (seconds). When > 0,
	// a single coalesced fire is dispatched after the timer expires instead of
	// firing immediately on the first delta (mitto-1uv).
	SettleWindow int `yaml:"settleWindow,omitempty" json:"settleWindow,omitempty"`
	// Cooldown is the per-conversation cooldown floor (seconds) honoured by the
	// runner between onTasks firings. 0 means use the global floor.
	Cooldown int `yaml:"cooldown,omitempty" json:"cooldown,omitempty"`
}

// PromptLoopOnChild groups the "onChild" trigger's attributes, nested under
// loop.onChild. Unknown keys are rejected (see UnmarshalYAML).
type PromptLoopOnChild struct {
	// When lists the child-conversation lifecycle events that arm this
	// trigger; each entry must be one of "anyEndResponse", "anyDeleted", or
	// "anyLoopStopped" (mirrors internal/session/loop.go's
	// ChildEventAnyEndResponse / ChildEventAnyDeleted / ChildEventAnyLoopStopped
	// — internal/prompts does not import internal/session, so this list is
	// validated against a local copy; keep the two in sync).
	// Empty/absent defaults to both events at the session-layer consumption
	// boundary (session.LoopPrompt.EffectiveChildEvents); this field returns
	// the authored list verbatim and does not apply that default itself.
	When []string `yaml:"when,omitempty" json:"when,omitempty"`
}

// knownLoopTriggers enumerates valid PromptLoop.Trigger entries.
var knownLoopTriggers = map[string]bool{
	"schedule":     true,
	"onCompletion": true,
	"onTasks":      true,
	"onChild":      true,
}

// knownPromptLoopChildEvents enumerates valid PromptLoopOnChild.When entries.
// Local copy of internal/session/loop.go's ChildEventAnyEndResponse /
// ChildEventAnyDeleted / ChildEventAnyLoopStopped (mitto-987y.1, mitto-q6my)
// — internal/prompts must not import internal/session, so keep this in sync
// with that file if it changes.
var knownPromptLoopChildEvents = map[string]bool{
	"anyEndResponse": true,
	"anyDeleted":     true,
	"anyLoopStopped": true,
}

// Triggers returns the effective, resolved trigger list: p.Trigger verbatim
// when non-empty, otherwise ["schedule"] (the pre-r6j implicit default). Safe
// to call on a nil receiver (returns ["schedule"]).
func (p *PromptLoop) Triggers() []string {
	if p == nil || len(p.Trigger) == 0 {
		return []string{"schedule"}
	}
	return p.Trigger
}

// hasTrigger reports whether name is present in the effective trigger list.
func (p *PromptLoop) hasTrigger(name string) bool {
	for _, t := range p.Triggers() {
		if t == name {
			return true
		}
	}
	return false
}

// ChildEvents returns the authored loop.onChild.when list verbatim, or nil
// when unset/nil (nil-safe on a nil receiver or nil OnChild). It does NOT
// default to both events — session.LoopPrompt.EffectiveChildEvents already
// owns that defaulting at the consumption boundary; duplicating it here would
// fork the semantics between the two layers.
func (p *PromptLoop) ChildEvents() []string {
	if p == nil || p.OnChild == nil {
		return nil
	}
	return p.OnChild.When
}

// FrequencyValue returns loop.schedule.value, or 0 when unset/nil.
func (p *PromptLoop) FrequencyValue() int {
	if p == nil || p.Schedule == nil {
		return 0
	}
	return p.Schedule.Value
}

// FrequencyUnit returns loop.schedule.unit, or "" when unset/nil.
func (p *PromptLoop) FrequencyUnit() string {
	if p == nil || p.Schedule == nil {
		return ""
	}
	return p.Schedule.Unit
}

// FrequencyAt returns loop.schedule.at, or "" when unset/nil.
func (p *PromptLoop) FrequencyAt() string {
	if p == nil || p.Schedule == nil {
		return ""
	}
	return p.Schedule.At
}

// CompletionDelay returns loop.onCompletion.delay, or 0 when unset/nil.
func (p *PromptLoop) CompletionDelay() int {
	if p == nil || p.OnCompletion == nil {
		return 0
	}
	return p.OnCompletion.Delay
}

// TasksCondition returns loop.onTasks.condition, or "" when unset/nil.
func (p *PromptLoop) TasksCondition() string {
	if p == nil || p.OnTasks == nil {
		return ""
	}
	return p.OnTasks.Condition
}

// TasksConditionPreset returns loop.onTasks.conditionPreset, or "" when unset/nil.
func (p *PromptLoop) TasksConditionPreset() string {
	if p == nil || p.OnTasks == nil {
		return ""
	}
	return p.OnTasks.ConditionPreset
}

// TasksCoalesceDuringBusy returns loop.onTasks.coalesceDuringBusy, or nil when
// unset/nil (pointer-presence semantics: nil means "not declared", matching
// the frontmatter's own tri-state).
func (p *PromptLoop) TasksCoalesceDuringBusy() *bool {
	if p == nil || p.OnTasks == nil {
		return nil
	}
	return p.OnTasks.CoalesceDuringBusy
}

// TasksSettleWindow returns loop.onTasks.settleWindow, or 0 when unset/nil.
func (p *PromptLoop) TasksSettleWindow() int {
	if p == nil || p.OnTasks == nil {
		return 0
	}
	return p.OnTasks.SettleWindow
}

// TasksCooldown returns loop.onTasks.cooldown, or 0 when unset/nil.
func (p *PromptLoop) TasksCooldown() int {
	if p == nil || p.OnTasks == nil {
		return 0
	}
	return p.OnTasks.Cooldown
}

// legacyPromptLoopFlatKeys enumerates the pre-r6j flat keys that lived
// directly under loop: and are now only valid nested under a trigger block
// (or have moved: `value`/`unit`/`at` → `schedule.*`, `delay` →
// `onCompletion.delay`, `condition`/`coalesceDuringBusy` → `onTasks.*`). A
// leftover top-level occurrence is a strict parse error naming the migration
// (mitto-r6j.3) since yaml.v3 would otherwise silently ignore the unknown key.
var legacyPromptLoopFlatKeys = map[string]string{
	"value":              "schedule.value",
	"unit":               "schedule.unit",
	"at":                 "schedule.at",
	"delay":              "onCompletion.delay",
	"condition":          "onTasks.condition",
	"conditionPreset":    "onTasks.conditionPreset",
	"coalesceDuringBusy": "onTasks.coalesceDuringBusy",
	"settleWindow":       "onTasks.settleWindow",
	"cooldown":           "onTasks.cooldown",
}

// promptLoopAux mirrors PromptLoop's real (grouped) fields; used as the
// UnmarshalYAML decode target so the custom method can add the legacy-key
// rejection pass without recursing into itself.
type promptLoopAux struct {
	Trigger       []string                `yaml:"trigger,omitempty"`
	Schedule      *PromptLoopSchedule     `yaml:"schedule,omitempty"`
	OnCompletion  *PromptLoopOnCompletion `yaml:"onCompletion,omitempty"`
	OnTasks       *PromptLoopOnTasks      `yaml:"onTasks,omitempty"`
	OnChild       *PromptLoopOnChild      `yaml:"onChild,omitempty"`
	MaxIterations int                     `yaml:"maxIterations,omitempty"`
	MaxDuration   string                  `yaml:"maxDuration,omitempty"`
	FreshContext  *bool                   `yaml:"freshContext,omitempty"`
	RunOnStart    *bool                   `yaml:"runOnStart,omitempty"`
	Mode          string                  `yaml:"mode,omitempty"`
	Default       *bool                   `yaml:"default,omitempty"`
}

// promptLoopKnownKeys enumerates the keys valid directly under loop:.
var promptLoopKnownKeys = map[string]bool{
	"trigger": true, "schedule": true, "onCompletion": true, "onTasks": true,
	"onChild": true, "maxIterations": true, "maxDuration": true,
	"freshContext": true, "runOnStart": true, "mode": true, "default": true,
}

// promptLoopScheduleKnownKeys enumerates the keys valid under loop.schedule.
var promptLoopScheduleKnownKeys = map[string]bool{
	"value": true, "unit": true, "at": true,
}

// promptLoopOnCompletionKnownKeys enumerates the keys valid under loop.onCompletion.
var promptLoopOnCompletionKnownKeys = map[string]bool{
	"delay": true,
}

// promptLoopOnTasksKnownKeys enumerates the keys valid under loop.onTasks.
var promptLoopOnTasksKnownKeys = map[string]bool{
	"condition": true, "conditionPreset": true, "coalesceDuringBusy": true,
	"settleWindow": true, "cooldown": true,
}

// promptLoopOnChildKnownKeys enumerates the keys valid under loop.onChild.
var promptLoopOnChildKnownKeys = map[string]bool{
	"when": true,
}

// rejectUnknownLoopKeys returns an error for the first key in the mapping node
// that is absent from known. path is the dotted prefix used in the message
// (e.g. "loop.schedule"). yaml.v3 silently ignores unknown keys, so a typo
// would otherwise parse into a zero-valued field instead of failing loudly.
func rejectUnknownLoopKeys(path string, node *yaml.Node, known map[string]bool) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: must be a mapping", path)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if !known[k.Value] {
			return fmt.Errorf("%s.%s is not a known key", path, k.Value)
		}
	}
	return nil
}

// UnmarshalYAML implements yaml.Unmarshaler for PromptLoop. It first scans the
// raw mapping node for any pre-r6j flat key (value/unit/at/delay/condition/...)
// and returns a clear migration error if one is found — yaml.v3 otherwise
// silently ignores unknown keys, which would parse a stale flat-form loop:
// block into an all-zero grouped struct instead of failing loudly. Any other
// unrecognized key is rejected too. Files that go through the mitto-r6j.3
// migration registry never reach this path with a legacy key present.
func (p *PromptLoop) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("loop: must be a mapping")
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if newPath, ok := legacyPromptLoopFlatKeys[k.Value]; ok {
			return fmt.Errorf("loop.%s is the pre-r6j flat schema — nest it under loop.%s, or run the prompt migration (see mitto-r6j.3)", k.Value, newPath)
		}
	}
	if err := rejectUnknownLoopKeys("loop", node, promptLoopKnownKeys); err != nil {
		return err
	}

	var aux promptLoopAux
	if err := node.Decode(&aux); err != nil {
		return err
	}

	p.Trigger = aux.Trigger
	p.Schedule = aux.Schedule
	p.OnCompletion = aux.OnCompletion
	p.OnTasks = aux.OnTasks
	p.OnChild = aux.OnChild
	p.MaxIterations = aux.MaxIterations
	p.MaxDuration = aux.MaxDuration
	p.FreshContext = aux.FreshContext
	p.RunOnStart = aux.RunOnStart
	p.Mode = aux.Mode
	p.Default = aux.Default
	return nil
}

// promptLoopScheduleAux mirrors PromptLoopSchedule's fields; decode target for
// UnmarshalYAML so the strict-key pass does not recurse into itself.
type promptLoopScheduleAux struct {
	Value int    `yaml:"value"`
	Unit  string `yaml:"unit"`
	At    string `yaml:"at,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler for PromptLoopSchedule, rejecting
// any key that is not part of the schedule-trigger block.
func (s *PromptLoopSchedule) UnmarshalYAML(node *yaml.Node) error {
	if err := rejectUnknownLoopKeys("loop.schedule", node, promptLoopScheduleKnownKeys); err != nil {
		return err
	}
	var aux promptLoopScheduleAux
	if err := node.Decode(&aux); err != nil {
		return err
	}
	s.Value = aux.Value
	s.Unit = aux.Unit
	s.At = aux.At
	return nil
}

// promptLoopOnCompletionAux mirrors PromptLoopOnCompletion's fields; decode
// target for UnmarshalYAML so the strict-key pass does not recurse into itself.
type promptLoopOnCompletionAux struct {
	Delay int `yaml:"delay,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler for PromptLoopOnCompletion,
// rejecting any key that is not part of the onCompletion-trigger block.
func (c *PromptLoopOnCompletion) UnmarshalYAML(node *yaml.Node) error {
	if err := rejectUnknownLoopKeys("loop.onCompletion", node, promptLoopOnCompletionKnownKeys); err != nil {
		return err
	}
	var aux promptLoopOnCompletionAux
	if err := node.Decode(&aux); err != nil {
		return err
	}
	c.Delay = aux.Delay
	return nil
}

// promptLoopOnTasksAux mirrors PromptLoopOnTasks's fields; decode target for
// UnmarshalYAML so the strict-key pass does not recurse into itself.
type promptLoopOnTasksAux struct {
	Condition          string `yaml:"condition,omitempty"`
	ConditionPreset    string `yaml:"conditionPreset,omitempty"`
	CoalesceDuringBusy *bool  `yaml:"coalesceDuringBusy,omitempty"`
	SettleWindow       int    `yaml:"settleWindow,omitempty"`
	Cooldown           int    `yaml:"cooldown,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler for PromptLoopOnTasks, rejecting
// any key that is not part of the onTasks-trigger block.
func (t *PromptLoopOnTasks) UnmarshalYAML(node *yaml.Node) error {
	if err := rejectUnknownLoopKeys("loop.onTasks", node, promptLoopOnTasksKnownKeys); err != nil {
		return err
	}
	var aux promptLoopOnTasksAux
	if err := node.Decode(&aux); err != nil {
		return err
	}
	t.Condition = aux.Condition
	t.ConditionPreset = aux.ConditionPreset
	t.CoalesceDuringBusy = aux.CoalesceDuringBusy
	t.SettleWindow = aux.SettleWindow
	t.Cooldown = aux.Cooldown
	return nil
}

// promptLoopOnChildAux mirrors PromptLoopOnChild's fields; decode target for
// UnmarshalYAML so the strict-key pass does not recurse into itself.
type promptLoopOnChildAux struct {
	When []string `yaml:"when,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler for PromptLoopOnChild, rejecting
// any key that is not part of the onChild-trigger block.
func (c *PromptLoopOnChild) UnmarshalYAML(node *yaml.Node) error {
	if err := rejectUnknownLoopKeys("loop.onChild", node, promptLoopOnChildKnownKeys); err != nil {
		return err
	}
	var aux promptLoopOnChildAux
	if err := node.Decode(&aux); err != nil {
		return err
	}
	c.When = aux.When
	return nil
}

// PromptTarget groups routing/dispatch behaviors for a prompt when it is
// used to create a new conversation. Future keys can extend routing modes
// without introducing new top-level prompt-frontmatter fields.
type PromptTarget struct {
	// Title, when set, is adopted as the new conversation's Name when the
	// caller does not supply one. When Reuse.Title is true, Title is also
	// the lookup key used to funnel dispatches into an existing conversation
	// whose Name matches (see PromptTargetReuse.Title).
	Title string `yaml:"title,omitempty" json:"title,omitempty"`

	// BackgroundColor, when set, is applied as a creation-time default color
	// on the conversation this prompt creates (mitto-8sk), rendered by the
	// sidebar as a left accent stripe. Must be a `#RGB` or `#RRGGBB` hex
	// value (case-insensitive); validated by ValidatePromptTarget at
	// prompt-load time. A reuse dispatch (Reuse.Issue / Reuse.Title / a
	// singleton hit) never re-applies this value — it is only set when a
	// NEW conversation is created, so a user's manual recolor is never
	// clobbered by a later dispatch. Empty/absent = no color set.
	BackgroundColor string `yaml:"backgroundColor,omitempty" json:"backgroundColor,omitempty"`

	// Reuse groups the three "funnel this dispatch into an existing
	// conversation" routing modes (issue / title / coalesce). All three
	// default to false / nil (unchanged behavior: every dispatch creates a
	// new conversation). A nil block is equivalent to all three off.
	Reuse *PromptTargetReuse `yaml:"reuse,omitempty" json:"reuse,omitempty"`

	// SuppressAutoChildren, when true, prevents the workspace-level
	// auto_children configuration from spawning child conversations when
	// this prompt originates a new top-level session (REST POST
	// /api/sessions). Create-time only; orthogonal to the Reuse modes.
	// Defaults to false (unchanged behavior: workspace auto_children spawn
	// as configured). Has no effect on non-top-level creates — auto-children
	// are already gated to top-level sessions in the session manager
	// (mitto-nlx).
	SuppressAutoChildren bool `yaml:"suppressAutoChildren,omitempty" json:"suppressAutoChildren,omitempty"`

	// NoArchive, when true, marks the conversation this prompt creates as
	// non-archivable (mitto-yvel). Create-time only; orthogonal to the
	// Reuse modes and to SuppressAutoChildren. This field only carries the
	// flag through frontmatter parsing — resolving it onto the created
	// conversation and enforcing it at archive entry points is done by
	// downstream work (mitto-yvel.2, mitto-yvel.3). Defaults to false
	// (unchanged behavior: the conversation remains archivable).
	NoArchive bool `yaml:"noArchive,omitempty" json:"noArchive,omitempty"`
}

// PromptTargetReuse groups reuse-mode routing keys under target.reuse. Split
// out from PromptTarget so related keys nest under a single YAML/JSON block
// instead of being three flat siblings of target.title.
type PromptTargetReuse struct {
	// Issue, when true, causes a dispatch that carries a beads_issue to
	// find an existing non-archived conversation in the same working_dir
	// that is already linked to that beads issue and enqueue the prompt
	// into it, instead of creating a new conversation. Falls through to
	// normal create when no candidate is found or when no beads_issue is
	// provided.
	Issue bool `yaml:"issue,omitempty" json:"issue,omitempty"`
	// Title, when true, causes a dispatch to find an existing non-archived
	// conversation in the same working_dir whose Name matches the enclosing
	// PromptTarget.Title (byte-for-byte) and enqueue the prompt into it,
	// instead of creating a new conversation. Requires PromptTarget.Title
	// to be non-empty (enforced by ValidatePromptTarget at prompt-load
	// time). Falls through to normal create when no candidate is found; the
	// created conversation is named Title so a subsequent scan matches it.
	Title bool `yaml:"title,omitempty" json:"title,omitempty"`
	// Coalesce, when non-nil and true, suppresses a dispatch to a reused
	// conversation when an identical prompt (same PromptName and Arguments,
	// deep-equal treating nil and empty maps as equivalent) is already
	// queued or currently in flight on that conversation. The second
	// dispatch becomes a no-op — the caller still gets a
	// {"session_id": existingID, "reused": true} response so it can focus
	// the target, but no duplicate work is enqueued. Only meaningful when
	// combined with a reuse mode (Issue, Title, or a singleton prompt);
	// enforced by ValidatePromptTarget at prompt-load time. Defaults to nil
	// (behavior unchanged: every dispatch is delivered).
	Coalesce *bool `yaml:"coalesce,omitempty" json:"coalesce,omitempty"`
}

// PromptLoopModeAlways means the prompt is always loop; not user-toggleable.
// Also the implied mode when PromptLoop.Mode is empty.
const PromptLoopModeAlways = "always"

// PromptLoopModeOptional means loop is user-choosable for this prompt;
// PromptLoop.Default sets the initial per-send toggle state.
const PromptLoopModeOptional = "optional"

// knownPromptLoopModes enumerates valid PromptLoop.Mode values (besides "").
var knownPromptLoopModes = map[string]bool{
	PromptLoopModeAlways:   true,
	PromptLoopModeOptional: true,
}

// MaxDurationSeconds returns MaxDuration parsed to seconds. Returns 0 with a
// nil error when the field is absent/empty (meaning "unlimited"). Returns a
// non-nil error when the string cannot be parsed as a Go duration.
func (p *PromptLoop) MaxDurationSeconds() (int, error) {
	if p == nil || p.MaxDuration == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(p.MaxDuration)
	if err != nil {
		return 0, fmt.Errorf("invalid maxDuration %q: %w", p.MaxDuration, err)
	}
	return int(d.Seconds()), nil
}

// ValidatePromptLoop validates the loop block: the mode/default combination
// and the trigger list + per-trigger blocks (see ValidateLoopTriggers).
// Returns an error for unknown mode values or invalid trigger configuration.
// Emits a non-fatal warning when default is set together with mode: always
// (or mode absent), since the value is ignored.
func ValidatePromptLoop(promptName string, p *PromptLoop) error {
	if p == nil {
		return nil
	}
	if p.Mode != "" && !knownPromptLoopModes[p.Mode] {
		return fmt.Errorf("prompt %q: loop.mode %q is not valid (must be one of: always, optional)", promptName, p.Mode)
	}
	if p.Default != nil && p.Mode != PromptLoopModeOptional {
		slog.Warn("prompt loop.default is ignored unless loop.mode is \"optional\"",
			"prompt", promptName, "mode", p.Mode)
	}
	if err := ValidateLoopTriggers(promptName, p); err != nil {
		return err
	}
	return nil
}

// ValidateLoopTriggers validates the loop.trigger list and its per-trigger
// blocks (mitto-r6j.1, mitto-987y.2):
//   - Every entry in Trigger must be one of "schedule", "onCompletion",
//     "onTasks", "onChild".
//   - Duplicate entries are rejected.
//   - An empty/absent Trigger list is not an error — it defaults to ["schedule"]
//     (see PromptLoop.Triggers), preserving pre-r6j implicit-schedule prompts.
//   - loop.schedule.at is only valid when loop.schedule.unit is "days".
//   - loop.onChild.when entries must be one of "anyEndResponse", "anyDeleted",
//     "anyLoopStopped".
//   - onChild can never be the only armed trigger (purely reactive to a
//     child's lifecycle; mirrors session.ErrOnChildAlone).
//   - A block present for a trigger NOT listed in Trigger is inert (matches
//     today's tolerance for inert flat fields) and only logs a warning, not an error.
func ValidateLoopTriggers(promptName string, p *PromptLoop) error {
	if p == nil {
		return nil
	}
	seen := make(map[string]bool, len(p.Trigger))
	for _, t := range p.Trigger {
		if !knownLoopTriggers[t] {
			return fmt.Errorf("prompt %q: loop.trigger %q is not valid (must be one of: schedule, onCompletion, onTasks, onChild)", promptName, t)
		}
		if seen[t] {
			return fmt.Errorf("prompt %q: loop.trigger contains duplicate entry %q", promptName, t)
		}
		seen[t] = true
	}

	if p.Schedule != nil {
		if !p.hasTrigger("schedule") {
			slog.Warn("prompt loop.schedule is set but \"schedule\" is not in loop.trigger — this block is inert",
				"prompt", promptName)
		}
		if p.Schedule.At != "" && p.Schedule.Unit != "days" {
			return fmt.Errorf("prompt %q: loop.schedule.at is only valid when loop.schedule.unit is \"days\"", promptName)
		}
	}
	if p.OnCompletion != nil && !p.hasTrigger("onCompletion") {
		slog.Warn("prompt loop.onCompletion is set but \"onCompletion\" is not in loop.trigger — this block is inert",
			"prompt", promptName)
	}
	if p.OnTasks != nil && !p.hasTrigger("onTasks") {
		slog.Warn("prompt loop.onTasks is set but \"onTasks\" is not in loop.trigger — this block is inert",
			"prompt", promptName)
	}
	if p.OnChild != nil {
		if !p.hasTrigger("onChild") {
			slog.Warn("prompt loop.onChild is set but \"onChild\" is not in loop.trigger — this block is inert",
				"prompt", promptName)
		}
		for _, e := range p.OnChild.When {
			if !knownPromptLoopChildEvents[e] {
				return fmt.Errorf("prompt %q: loop.onChild.when %q is not valid (must be one of: anyEndResponse, anyDeleted, anyLoopStopped)", promptName, e)
			}
		}
	}
	// onChild is purely reactive to a child conversation's lifecycle, so it
	// must never be the sole armed trigger (mirrors session.ErrOnChildAlone,
	// internal/session/loop.go). p.Triggers() defaults an empty/absent
	// Trigger to ["schedule"], so this can only trip on an explicitly
	// authored onChild-only list (including the legacy scalar form).
	if eff := p.Triggers(); len(eff) == 1 && eff[0] == "onChild" {
		return fmt.Errorf("prompt %q: loop.trigger onChild cannot be the only trigger", promptName)
	}
	return nil
}

// hexColorRe matches a `#RGB` or `#RRGGBB` hex color, case-insensitive.
// Used only to validate PromptTarget.BackgroundColor (mitto-8sk) — the
// existing top-level prompt BackgroundColor field is intentionally left
// unvalidated (out of scope, would break existing unvalidated prompts).
var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// ValidatePromptTarget validates the target block's field combination.
// Returns an error when Reuse.Title is true but Title is empty, since a
// reuse-by-title dispatch has no lookup key in that case. A nil target or
// an empty target block is always valid.
//
// promptSingleton reports whether the containing PromptFile declares the
// top-level singleton flag; it is consulted only to authorize Reuse.Coalesce
// (which requires at least one reuse mode: Reuse.Issue, Reuse.Title, or the
// top-level singleton). Callers that do not need the Reuse.Coalesce check may
// pass false.
func ValidatePromptTarget(promptName string, t *PromptTarget, promptSingleton bool) error {
	if t == nil {
		return nil
	}
	if t.Reuse != nil {
		if t.Reuse.Title && strings.TrimSpace(t.Title) == "" {
			return fmt.Errorf("prompt %q: target.reuse.title is true but target.title is empty (a title is required to key the lookup)", promptName)
		}
		if t.Reuse.Coalesce != nil && *t.Reuse.Coalesce && !t.Reuse.Issue && !t.Reuse.Title && !promptSingleton {
			return fmt.Errorf("prompt %q: target.reuse.coalesce requires at least one reuse mode (target.reuse.issue, target.reuse.title, or top-level singleton: true)", promptName)
		}
	}
	if strings.TrimSpace(t.BackgroundColor) != "" && !hexColorRe.MatchString(strings.TrimSpace(t.BackgroundColor)) {
		return fmt.Errorf("prompt %q: target.backgroundColor %q is not a valid hex color (expected #RGB or #RRGGBB)", promptName, t.BackgroundColor)
	}
	// Validate target.title Go-template syntax at load time (mitto-5qbo).
	// Fast-path no-op for literal titles (no "{{"); catches unbalanced actions
	// or unknown funcs so a broken frontmatter is rejected at ParsePromptFile
	// time rather than at dispatch. Mirrors the body precompile pass below.
	if strings.TrimSpace(t.Title) != "" {
		if err := ValidatePromptTemplateSyntax(promptName+".target.title", t.Title); err != nil {
			return err
		}
	}
	return nil
}

// legacyTargetReuseMigration maps each legacy flat key (removed in mitto-6b3)
// to the short field name it becomes under the nested target.reuse block,
// plus the full dotted path for WARN messages.
var legacyTargetReuseMigration = []struct {
	old, short, new string
}{
	{"reuseIssue", "issue", "target.reuse.issue"},
	{"reuseTitle", "title", "target.reuse.title"},
	{"reuseCoalesce", "coalesce", "target.reuse.coalesce"},
}

// migrateLegacyTargetReuseKeys rewrites the pre-mitto-6b3 flat target.reuse*
// keys (reuseIssue / reuseTitle / reuseCoalesce) onto the nested target.reuse
// block in place, logging one WARN per migrated key. Mirrors the mitto-r6j.3
// "preserve operator data, emit a WARN, never a file-level error" precedent
// established for the loop.* schema: a lint-class problem in one target
// attribute must not take the whole prompt out of the registry (mitto-a4yg;
// the earlier hard-reject here evicted the entire prompt file for a single
// stale key).
//
// Uses a *yaml.Node walk so it only fires on the ONE mapping at document-root
// target: — a document body that happens to mention the string "reuseTitle"
// somewhere else (prose, sample YAML in comments) does not trip it. Returns
// whether anything was migrated.
func migrateLegacyTargetReuseKeys(path string, doc *yaml.Node) bool {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}
	var targetNode *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Kind == yaml.ScalarNode && root.Content[i].Value == "target" {
			targetNode = root.Content[i+1]
			break
		}
	}
	if targetNode == nil || targetNode.Kind != yaml.MappingNode {
		return false
	}

	// Read-only pass: bail out (untouched) unless a legacy key is present.
	hasLegacy := false
	for i := 0; i+1 < len(targetNode.Content); i += 2 {
		k := targetNode.Content[i]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		for _, m := range legacyTargetReuseMigration {
			if k.Value == m.old {
				hasLegacy = true
			}
		}
	}
	if !hasLegacy {
		return false
	}

	// Split into "kept" (everything except an existing reuse: mapping) and
	// the existing reuse: node (if any) so legacy keys can be merged into it.
	var reuseKey, reuseVal *yaml.Node
	var kept []*yaml.Node
	for i := 0; i+1 < len(targetNode.Content); i += 2 {
		k, v := targetNode.Content[i], targetNode.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == "reuse" && v.Kind == yaml.MappingNode {
			reuseKey, reuseVal = k, v
			continue
		}
		kept = append(kept, k, v)
	}
	if reuseVal == nil {
		reuseKey = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "reuse"}
		reuseVal = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}

	out := make([]*yaml.Node, 0, len(kept)+2)
	for i := 0; i+1 < len(kept); i += 2 {
		k, v := kept[i], kept[i+1]
		migrated := false
		for _, m := range legacyTargetReuseMigration {
			if k.Value == m.old {
				newKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: m.short}
				reuseVal.Content = append(reuseVal.Content, newKey, v)
				slog.Warn("prompt file uses a legacy target.reuse key and was migrated in memory",
					"path", path, "old_key", "target."+m.old, "new_key", m.new)
				migrated = true
				break
			}
		}
		if !migrated {
			out = append(out, k, v)
		}
	}
	out = append(out, reuseKey, reuseVal)
	targetNode.Content = out
	return true
}

// promptFileKnownKeys enumerates the keys valid directly under a prompt
// file's document root (top-level frontmatter), mirroring PromptFile's yaml
// tags. Used by collectUnknownPromptKeys (mitto-yo8o) to flag typo'd or
// misplaced keys that yaml.v3 would otherwise silently drop.
var promptFileKnownKeys = map[string]bool{
	"name": true, "description": true, "group": true, "menus": true,
	"backgroundColor": true, "icon": true, "tags": true, "singleton": true,
	"target": true, "enabled": true, "enabledWhen": true, "loop": true,
	"preferredModels": true, "parameters": true, "prompt": true,
}

// promptTargetKnownKeys enumerates the keys valid directly under target:,
// mirroring PromptTarget's yaml tags. See promptFileKnownKeys.
var promptTargetKnownKeys = map[string]bool{
	"title": true, "backgroundColor": true, "reuse": true,
	"suppressAutoChildren": true, "noArchive": true,
}

// collectUnknownPromptKeys walks the document-root mapping and, if present,
// the nested target: mapping, returning one dotted-path diagnostic per key
// absent from promptFileKnownKeys / promptTargetKnownKeys (e.g. "target.titel").
// Unlike the loop.* subtree (rejectUnknownLoopKeys, which hard-fails via
// UnmarshalYAML), this is WARN-only: a typo'd or misplaced key at these two
// levels must not evict an otherwise-working prompt from the registry, per
// the mitto-a4yg precedent for target.* fields — extended here to cover the
// top level too (mitto-yo8o). Callers should append the result into
// PromptFile.Warnings so it survives to the UI (mitto-tigh's channel), not
// just a slog line.
//
// Must run AFTER migrateLegacyTargetReuseKeys so already-migrated legacy
// target.reuse* flat keys are not misreported as unknown (mirrors the
// ordering constraint that migration precedes the loop.* strict pass).
func collectUnknownPromptKeys(doc *yaml.Node) []string {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}

	var unknown []string
	var targetNode *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if !promptFileKnownKeys[k.Value] {
			unknown = append(unknown, k.Value)
		}
		if k.Value == "target" {
			targetNode = root.Content[i+1]
		}
	}
	if targetNode != nil && targetNode.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(targetNode.Content); i += 2 {
			k := targetNode.Content[i]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			if !promptTargetKnownKeys[k.Value] {
				unknown = append(unknown, "target."+k.Value)
			}
		}
	}
	return unknown
}

// PromptParameterCache configures value caching for a single prompt parameter.
// When present, a successfully collected argument value may be reused within the
// same conversation without re-prompting the user.
//
// Example YAML:
//
//	cache:
//	  destination: memory   # only "memory" is valid in v1
//	  ttl: 1h               # optional Go duration; absent => cached for conversation lifetime
type PromptParameterCache struct {
	// Destination is the cache backend. Only "memory" is valid in v1; future versions
	// may introduce additional backends (e.g. "disk"). The value is validated at parse
	// time against KnownPromptCacheDestinations.
	Destination string `yaml:"destination" json:"destination"`
	// TTL is an optional Go duration string (e.g. "1h", "30m") that limits how long
	// the cached value is valid. When absent or empty, the value is cached for the
	// entire conversation lifetime (no expiry).
	TTL string `yaml:"ttl,omitempty" json:"ttl,omitempty"`
}

// PromptParameter declares a single named, typed parameter that the prompt body
// references via Go-template {{ .Args.NAME }} or {{ Arg "NAME" "default" }} syntax.
type PromptParameter struct {
	// Name is the placeholder name used in the prompt body (e.g. "id" for {{ .Args.id }}).
	Name string `yaml:"name" json:"name"`
	// Type is one of the known parameter types (see KnownPromptParameterTypes).
	Type string `yaml:"type" json:"type"`
	// Description is an optional human-readable hint shown in the UI / MCP schema.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// MultiLine, when true, renders the input as a multi-line, resizable textarea
	// instead of a single-line field. Only meaningful for the "text" type (see
	// ValidatePromptParameters); ignored when collected outside the UI.
	MultiLine bool `yaml:"multiLine,omitempty" json:"multiLine,omitempty"`
	// Options, when non-empty, constrains a "text" parameter to a fixed enumeration.
	// The parameter dialog renders a dropdown of these values instead of a free-text
	// input. The bound value remains a plain string so template consumers are
	// unchanged. Only valid on type "text"; mutually exclusive with MultiLine.
	// Empty strings and duplicate values are rejected at validation; when Default
	// is non-empty it must be one of the listed options.
	Options []string `yaml:"options,omitempty" json:"options,omitempty"`
	// Required, when explicitly set to true, signals that the parameter must be
	// supplied before the prompt is dispatched. Defaults to unset (caller decides).
	// Declarative defaults are handled by the Arg helper in the template body, not here.
	Required *bool `yaml:"required,omitempty" json:"required,omitempty"`
	// Show controls whether the parameter is rendered in the parameter dialog
	// (the RENDER axis) and, for "always" only, whether its presence forces the
	// dialog to open (the OPEN axis). Valid values (see IsValidShow): "" or
	// "auto" (default: rendered whenever the dialog opens for any reason, but
	// does not by itself force it open), "always" (rendered, and forces the
	// dialog open even for an otherwise-satisfied prompt), and "never" (never
	// rendered, never opens the dialog; the value comes from a menu, a
	// declared default, or a cached value). Unknown values are rejected by
	// ValidatePromptParameters.
	Show string `yaml:"show,omitempty" json:"show,omitempty"`
	// Default is the default value substituted when the parameter is not explicitly
	// supplied. Required for processor parameters (mandatory); optional for prompt-file
	// parameters (the Arg helper in the template body also provides per-site defaults).
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// Cache, when non-nil, enables per-conversation value caching for this parameter.
	// The collected argument value is stored so the UI can skip re-asking within the
	// same conversation. See PromptParameterCache for the configuration schema.
	Cache *PromptParameterCache `yaml:"cache,omitempty" json:"cache,omitempty"`
	// Dir, when set, scopes the "filename" parameter's dropdown to files under
	// this workspace-relative directory. Empty = workspace root. Non-recursive.
	// Only valid for type "filename"; rejected by ValidatePromptParameters
	// elsewhere. Absolute paths and ".." segments are rejected at validation.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
	// Glob, when set, filters candidates in Dir via filepath.Match syntax plus
	// doublestar "**" for recursive matches (e.g. "*.md", "**/*.md",
	// "docs/**/*.md"). Non-"**" patterns match base names only under Dir
	// (non-recursive). Patterns containing "**" walk recursively from Dir and
	// match against the entry's workspace-relative path. A candidate is
	// included when it matches ANY listed pattern (union semantics). Empty or
	// nil = all regular files. Only valid for types "filename"/"dirname";
	// each entry is validated with a doublestar.ValidatePattern compile-check
	// at parse time. YAML must use the list form (`glob: ["*.md", "*.rst"]`
	// or block-list); a scalar string is rejected at unmarshal time.
	Glob []string `yaml:"glob,omitempty" json:"glob,omitempty"`
	// Remember controls whether the most recently submitted value for this
	// parameter is persisted and pre-filled the next time the same prompt
	// dialog opens. Valid values (see IsValidRemember): "" or "never"
	// (default: do not persist), "folder" (per-workspace persistence, keyed by
	// workspace UUID), and "global" (reserved; enum-accepted but not stored
	// in v1). Unknown values are rejected by ValidatePromptParameters.
	Remember string `yaml:"remember,omitempty" json:"remember,omitempty"`
	// CollectInnerArgs controls whether a "prompts" picker's nested parameter
	// dialog collects and ships the picked prompt's own parameters as a
	// `<Name>_Args` companion argument. Defaults to true (collect) when nil —
	// see ShouldCollectInnerArgs. Set to false when the picker is used only
	// as an edit subject / name reference and the picked prompt's own
	// parameter values are never consumed, so asking for them would be
	// wasted interaction (mitto-48c). Only valid for type "prompts"; rejected
	// by ValidatePromptParameters elsewhere.
	CollectInnerArgs *bool `yaml:"collectInnerArgs,omitempty" json:"collectInnerArgs,omitempty"`
	// Group is an optional, purely presentational label used to cluster
	// related parameters under a shared tab in the parameter dialog. Valid on
	// every parameter type. When at least one declared parameter has a
	// non-empty Group, the dialog renders a tab bar: ungrouped parameters
	// (empty Group) are collected into a "General" tab, and each distinct
	// Group value gets its own tab, in first-appearance order. "General" is
	// not a reserved name — an explicit `group: General` merges into the
	// same tab as the ungrouped parameters. When no parameter declares a
	// Group, the dialog renders the flat, untabbed list exactly as before
	// (back-compat invariant; see ValidatePromptParameters for the
	// whitespace-only rejection). Grouping never affects the collected
	// argument values themselves.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
}

// ShouldCollectInnerArgs reports whether a "prompts" picker parameter should
// collect and ship the picked prompt's own parameters as a `<Name>_Args`
// companion argument. Absent/nil CollectInnerArgs defaults to true so every
// pre-existing picker (declared before mitto-48c) is unaffected.
func (p PromptParameter) ShouldCollectInnerArgs() bool {
	return p.CollectInnerArgs == nil || *p.CollectInnerArgs
}

// PromptPreferredModel references a global model profile (Settings → Models) either
// by profile name or by capability tag. Exactly one of ModelName / ModelTag is set per entry.
type PromptPreferredModel struct {
	// ModelName is the name of a Model profile (Config.Models) to resolve, e.g. "Opus".
	ModelName string `yaml:"modelName,omitempty" json:"modelName,omitempty"`
	// ModelTag selects any Model profile carrying this capability tag, e.g. "Cheap".
	ModelTag string `yaml:"modelTag,omitempty" json:"modelTag,omitempty"`
}

// PromptFile represents a parsed YAML prompt file.
// Files are stored in MITTO_DIR/prompts/ and can be organized in subdirectories.
type PromptFile struct {
	// Path is the relative path from the prompts directory (e.g., "git/commit.prompt.yaml")
	Path string `yaml:"-" json:"-"`

	// Name is the display name for the prompt button.
	// If not specified in front-matter, derived from filename.
	Name string `yaml:"name" json:"name"`

	// Description is an optional description shown as tooltip in the UI.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Group is an optional group name for organizing prompts in the UI.
	// Prompts with the same group will be displayed together under a group header.
	// If empty, the prompt will appear in an "Other" section.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`

	// Menus is a comma-separated list of UI menus this prompt should appear in
	// (beyond the default ChatInput dropup). For example, "conversation" makes the
	// prompt available in the per-conversation context menu. Multiple values may be
	// combined, e.g. "conversation,group".
	Menus string `yaml:"menus,omitempty" json:"menus,omitempty"`

	// BackgroundColor is an optional hex color for the prompt button (e.g., "#E8F5E9").
	BackgroundColor string `yaml:"backgroundColor,omitempty" json:"backgroundColor,omitempty"`

	// Icon is an optional icon identifier for future use.
	Icon string `yaml:"icon,omitempty" json:"icon,omitempty"`

	// Tags is an optional list of categorization tags for future use.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// Singleton, when true, declares that this prompt must not have multiple
	// concurrent conversation instances (subject to find-or-route logic).
	Singleton bool `yaml:"singleton,omitempty" json:"singleton,omitempty"`

	// Target groups routing/dispatch behaviors for this prompt when it is
	// used to create a new conversation. Currently only Title (peer) and
	// the nested Reuse block are defined; future keys can extend routing
	// modes without introducing new top-level prompt-frontmatter fields.
	Target *PromptTarget `yaml:"target,omitempty" json:"target,omitempty"`

	// Enabled controls whether the prompt is active. Defaults to true if not specified.
	Enabled *bool `yaml:"enabled,omitempty" json:"-"`

	// EnabledWhen is an optional CEL expression that determines when this prompt is visible.
	// If empty, the prompt is always visible.
	// If the expression evaluates to true, the prompt is visible; otherwise hidden.
	// Available context: acp.*, workspace.*, session.*, parent.*, children.*, tools.*
	// Example: "!session.isChild" hides the prompt in child conversations.
	EnabledWhen string `yaml:"enabledWhen,omitempty" json:"-"`

	// Loop, if set, declares that selecting this prompt in a menu creates a
	// loop (recurring) conversation instead of a one-time seed.
	// Presence implies opt-in; the fields provide default values for the schedule
	// dialog. The "at" field is in HH:MM UTC and is only valid for the "days" unit.
	Loop *PromptLoop `yaml:"loop,omitempty" json:"loop,omitempty"`

	// PreferredModels is an ordered list of references to global model profiles
	// (Settings → Models), by profile name or capability tag. The first entry that
	// resolves to an available model wins. Empty/absent means use the session's
	// baseline model. See PromptPreferredModel.
	PreferredModels []PromptPreferredModel `yaml:"preferredModels,omitempty" json:"preferredModels,omitempty"`

	// Parameters declares the named, typed inputs this prompt expects.
	// Each entry must have a non-empty name and a recognised type (see KnownPromptParameterTypes).
	// Callers substitute values via Go-template .Args.NAME or Arg helper in Content.
	Parameters []PromptParameter `yaml:"parameters,omitempty" json:"parameters,omitempty"`

	// Content is the prompt body text, stored under the "prompt" key in the YAML file.
	Content string `yaml:"prompt" json:"prompt"`

	// FileModTime is the file's modification time for cache invalidation.
	FileModTime time.Time `yaml:"-" json:"-"`

	// Warnings holds non-fatal diagnostics detected while parsing this prompt
	// (deprecated @mitto: tokens, unrecognised menus tokens, legacy-schema
	// migrations). Populated by parsePromptFileData from the dedup-free
	// detectors (DeprecatedMittoVars, UnknownMenuTokens) rather than captured
	// from the Warn* slog side effects, so every load/reload retains the full
	// set even though the Warn* helpers themselves are deduped per process
	// lifetime (mitto-tigh). Carried into WebPrompt.Warnings via ToWebPrompt.
	Warnings []string `yaml:"-" json:"-"`
}

// IsEnabled returns true if the prompt is enabled.
// A nil Enabled field is treated as true (enabled by default).
func (p *PromptFile) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

// IsSpecificToACP returns true if the prompt is specifically targeted at the given ACP server.
// Unlike IsAllowedForACP, this returns false for prompts with empty ACPs field (generic prompts).
// This is used to show ACP-specific prompts in the server settings UI.
func (p *PromptFile) IsSpecificToACP(acpServer string) bool {
	if acpServer == "" {
		return false
	}

	// Check enabledWhen CEL expression for ACP.MatchesServerType("serverType").
	// We lowercase both sides for a case-insensitive prefix match: "acp.matchesserver"
	// is a deliberate prefix of the lowercased canonical form "acp.matchesservertype",
	// which still matches correctly while tolerating minor capitalisation variations.
	if p.EnabledWhen != "" {
		lowerExpr := strings.ToLower(p.EnabledWhen)
		lowerServer := strings.ToLower(acpServer)
		if strings.Contains(lowerExpr, "acp.matchesservertype") && strings.Contains(lowerExpr, lowerServer) {
			return true
		}
	}

	return false
}

// ToWebPrompt converts the PromptFile to a WebPrompt for API responses.
// File-based prompts are marked with Source=PromptSourceFile.
func (p *PromptFile) ToWebPrompt() WebPrompt {
	return WebPrompt{
		Name:            p.Name,
		Prompt:          p.Content,
		BackgroundColor: p.BackgroundColor,
		Icon:            p.Icon,
		Description:     p.Description,
		Group:           p.Group,
		Menus:           p.Menus,
		Singleton:       p.Singleton,
		Target:          p.Target,
		Source:          PromptSourceFile,
		EnabledWhen:     p.EnabledWhen,
		Enabled:         p.Enabled,
		Loop:            p.Loop,
		PreferredModels: p.PreferredModels,
		Parameters:      p.Parameters,
		Tags:            p.Tags,
		Warnings:        p.Warnings,
	}
}

// HasVisibilityCondition returns true if the prompt has a enabledWhen expression.
func (p *PromptFile) HasVisibilityCondition() bool {
	return strings.TrimSpace(p.EnabledWhen) != ""
}

// ParsePromptFile parses a YAML prompt file.
// The file format is a single YAML document with all fields as top-level keys:
//
//	name: "My Prompt"
//	description: "Optional description"
//	backgroundColor: "#E8F5E9"
//	prompt: |
//	  Prompt content here...
//
// The name is derived from the filename if not specified in the file.
func ParsePromptFile(path string, data []byte, modTime time.Time) (*PromptFile, error) {
	prompt, _, _, err := parsePromptFileData(path, data, modTime)
	return prompt, err
}

// parsePromptFileData is the shared implementation behind ParsePromptFile and
// LoadPromptFile. Before validating/unmarshalling, it runs data through the
// mitto-r6j.3 prompt-file migration registry (currently just
// "0001-loop-grouped-triggers", which rewrites the pre-r6j flat loop: schema
// onto the grouped trigger blocks). Migration always happens in memory
// here — a firing migration logs one WARN naming the file and the migration
// IDs and is never a load error ("old form is a WARN, never an error" per
// the mitto-r6j.3 decision). The migrated bytes and result are also returned
// so LoadPromptFile can additionally persist the write-back to disk; callers
// without a writable absolute path (embedded/inline prompts, tests,
// read-only sources) still get a successfully parsed PromptFile.
func parsePromptFileData(path string, data []byte, modTime time.Time) (*PromptFile, []byte, migrate.Result, error) {
	migrated, result, migrateErr := migrate.MigrateYAML(data)
	if migrateErr != nil {
		// A migration bug must not break prompt loading: fall back to the
		// original bytes and let the normal parse path below surface
		// whatever error the (unmigrated) content produces.
		slog.Warn("prompt file migration failed; parsing original content",
			"path", path, "error", migrateErr)
		migrated = data
		result = migrate.Result{}
	} else if result.Changed {
		slog.Warn("prompt file uses a legacy schema and was migrated",
			"path", path, "migrations", strings.Join(result.Fired, ","))
	}

	prompt := &PromptFile{
		Path:        path,
		FileModTime: modTime,
	}

	if result.Changed {
		prompt.Warnings = append(prompt.Warnings,
			fmt.Sprintf("prompt file uses a legacy schema and was migrated: %s", strings.Join(result.Fired, ", ")))
	}

	// Parse into a mutable node tree so the legacy target.reuse* migration
	// below can rewrite it in memory (WARN, never a file-level error) before
	// the struct decode — mirrors the mitto-r6j.3 loop.* migration precedent
	// (mitto-a4yg: a lint-class field must not evict the whole prompt).
	var doc yaml.Node
	if err := yaml.Unmarshal(migrated, &doc); err != nil {
		return nil, migrated, result, fmt.Errorf("failed to parse prompt file %s: %w", path, err)
	}
	migrateLegacyTargetReuseKeys(path, &doc)

	// Warn (non-fatal) on typo'd or misplaced top-level / target.* keys
	// (mitto-yo8o). Runs after the legacy target.reuse* migration above so
	// already-migrated keys are never misreported as unknown. WARN, not
	// error — a stray key must not evict an otherwise-working prompt from
	// the registry (mitto-a4yg precedent).
	if unknownKeys := collectUnknownPromptKeys(&doc); len(unknownKeys) > 0 {
		slog.Warn("prompt file contains unrecognised key(s); they will have no effect",
			"path", path, "unknown_keys", unknownKeys)
		prompt.Warnings = append(prompt.Warnings,
			fmt.Sprintf("unrecognised key(s) will have no effect: %s", strings.Join(unknownKeys, ", ")))
	}

	if err := doc.Decode(prompt); err != nil {
		return nil, migrated, result, fmt.Errorf("failed to parse prompt file %s: %w", path, err)
	}

	// Derive name from filename if not specified
	if prompt.Name == "" {
		base := filepath.Base(path)
		// Strip .prompt.yaml extension specifically, then fall back to last ext
		name := strings.TrimSuffix(base, ".prompt.yaml")
		if name == base {
			name = strings.TrimSuffix(base, filepath.Ext(base))
		}
		prompt.Name = name
	}

	// Validate parameters block.
	if err := ValidatePromptParameters(prompt.Menus, prompt.Parameters); err != nil {
		return nil, migrated, result, fmt.Errorf("prompt file %s: %w", path, err)
	}

	// Validate loop block (mode/default combination).
	if err := ValidatePromptLoop(prompt.Name, prompt.Loop); err != nil {
		return nil, migrated, result, fmt.Errorf("prompt file %s: %w", path, err)
	}

	// Validate target block (reuseTitle requires a non-empty title;
	// reuseCoalesce requires a reuse mode).
	if err := ValidatePromptTarget(prompt.Name, prompt.Target, prompt.Singleton); err != nil {
		return nil, migrated, result, fmt.Errorf("prompt file %s: %w", path, err)
	}

	// Validate loop.onTasks.condition CEL expression when non-empty (fail-fast,
	// mirrors how the runtime seam is wired via session.ConditionValidator).
	if cond := prompt.Loop.TasksCondition(); cond != "" {
		if err := cel.ValidateCondition(cond); err != nil {
			return nil, migrated, result, fmt.Errorf("prompt file %s: loop.onTasks.condition: %w", path, err)
		}
	}

	// Validate Go-template syntax + cond/when CEL literals (mitto-m7sb.6).
	// Fast-path no-op for bodies without "{{". Fail-fast on invalid templates.
	if err := PrecompileTemplateConds(prompt.Name, prompt.Content); err != nil {
		return nil, migrated, result, fmt.Errorf("prompt file %s: %w", path, err)
	}

	// Warn (non-fatal) when the body still uses deprecated @mitto: tokens (mitto-m7sb.9).
	// WarnDeprecatedMittoVars itself dedupes per (name, vars) for the process
	// lifetime, so a reload can emit nothing at all; Warnings is populated
	// from the dedup-free detector below so it is always retained on the
	// PromptFile regardless of prior slog emission (mitto-tigh).
	WarnDeprecatedMittoVars(prompt.Name, prompt.Content)
	if vars := DeprecatedMittoVars(prompt.Content); len(vars) > 0 {
		prompt.Warnings = append(prompt.Warnings,
			fmt.Sprintf("prompt body uses deprecated @mitto: variables (migrate to Go templates): %s", strings.Join(vars, ", ")))
	}

	// Warn (non-fatal) when menus contains an unrecognised token (mitto-rjg6).
	// Same dedup caveat as above applies to WarnUnknownMenus.
	WarnUnknownMenus(prompt.Name, path, prompt.Menus)
	if unknown := UnknownMenuTokens(prompt.Menus); len(unknown) > 0 {
		prompt.Warnings = append(prompt.Warnings,
			fmt.Sprintf("menus field contains unrecognised token(s): %s", strings.Join(unknown, ", ")))
	}

	return prompt, migrated, result, nil
}

// LoadPromptFile loads and parses a single prompt file. If the file is on a
// legacy schema, the mitto-r6j.3 migration registry rewrites it in memory
// (see parsePromptFileData) and the migrated bytes are additionally written
// back to fullPath (atomically; degrades to a WARN on a read-only source —
// see migrate.WriteBackIfNeeded). A file already on the current schema is
// never written to, so its mtime is untouched.
func LoadPromptFile(promptsDir, relativePath string) (*PromptFile, error) {
	fullPath := filepath.Join(promptsDir, relativePath)

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat prompt file %s: %w", relativePath, err)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read prompt file %s: %w", relativePath, err)
	}

	prompt, migrated, result, err := parsePromptFileData(relativePath, data, info.ModTime())
	if err != nil {
		return nil, err
	}

	if result.Changed {
		migrate.WriteBackIfNeeded(fullPath, migrated, result)
	}

	return prompt, nil
}

// PromptLoadError describes a single prompt file that failed to load/parse/precompile.
type PromptLoadError struct {
	Path string // path (relative to the scanned dir) of the offending file
	Err  error  // underlying parse/template error
}

// LoadPromptsFromDir loads all .prompt.yaml files from a directory recursively.
// Disabled prompts (enabled: false) are included so they can suppress same-named
// prompts from lower-priority directories during the merge phase.
// Returns an empty slice if the directory doesn't exist.
func LoadPromptsFromDir(dir string) ([]*PromptFile, error) {
	prompts, _, err := LoadPromptsFromDirWithErrors(dir)
	return prompts, err
}

// LoadPromptsFromDirWithErrors loads all .prompt.yaml files from a directory recursively,
// returning both the successfully-loaded prompts and per-file errors for files that
// failed to load/parse/precompile. Failed files are also logged at WARN.
// Returns an empty slice if the directory doesn't exist.
func LoadPromptsFromDirWithErrors(dir string) ([]*PromptFile, []PromptLoadError, error) {
	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil, nil
	}

	var prompts []*PromptFile
	var loadErrors []PromptLoadError

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only process .prompt.yaml files
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".prompt.yaml") {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}

		// Load and parse the file
		prompt, err := LoadPromptFile(dir, relPath)
		if err != nil {
			loadErrors = append(loadErrors, PromptLoadError{Path: relPath, Err: err})
			// De-dup WARN per (absPath, err) per process lifetime (mitto-e8r):
			// a permanently-broken workspace file otherwise emits one WARN per
			// reload (fs-watcher + cold cache + ForceReload), spamming the log
			// while adding no new signal. Structured error already flows via
			// PromptLoadError → LoadErrors() → UI toasts + verify CLI above.
			absPath := filepath.Join(dir, relPath)
			key := absPath + "\x00" + err.Error()
			if _, loaded := loadWarnSeen.LoadOrStore(key, struct{}{}); !loaded {
				slog.Warn("failed to load prompt file",
					"path", absPath,
					"error", err)
			}
			return nil
		}

		prompts = append(prompts, prompt)
		return nil
	})

	if err != nil {
		return nil, loadErrors, fmt.Errorf("failed to walk prompts directory %s: %w", dir, err)
	}

	return prompts, loadErrors, nil
}

// PromptsToWebPrompts converts a slice of PromptFile to WebPrompt.
func PromptsToWebPrompts(prompts []*PromptFile) []WebPrompt {
	if len(prompts) == 0 {
		return nil
	}

	result := make([]WebPrompt, 0, len(prompts))
	for _, p := range prompts {
		result = append(result, p.ToWebPrompt())
	}
	return result
}

// FilterPromptsSpecificToACP filters prompts to only include those specifically targeted
// at the given ACP server (have acps: field that includes the server name).
// Generic prompts (with empty acps: field) are excluded.
// If acpServer is empty, returns an empty slice.
func FilterPromptsSpecificToACP(prompts []*PromptFile, acpServer string) []*PromptFile {
	if acpServer == "" || len(prompts) == 0 {
		return nil
	}

	result := make([]*PromptFile, 0)
	for _, p := range prompts {
		if p.IsSpecificToACP(acpServer) {
			result = append(result, p)
		}
	}
	return result
}

// GetPromptsDirModTime returns the most recent modification time of any file
// in the prompts directory. Returns zero time if directory doesn't exist.
func GetPromptsDirModTime(dir string) time.Time {
	var latest time.Time

	// Check directory itself
	info, err := os.Stat(dir)
	if err != nil {
		return latest
	}
	latest = info.ModTime()

	// Walk all files to find the most recent modification
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})

	return latest
}

// toolPatternCallRe matches Tools.Has*Pattern* function calls in CEL expressions.
var toolPatternCallRe = regexp.MustCompile(`Tools\.Has(?:All|Any)?Patterns?\([^)]*`)

// quotedStringRe matches double-quoted string literals.
var quotedStringRe = regexp.MustCompile(`"([^"]+)"`)

// extractToolPatternsFromCEL extracts tool glob patterns from enabledWhen CEL expressions.
// Looks for Tools.HasPattern("..."), Tools.HasAllPatterns([...]), Tools.HasAnyPattern([...]).
func extractToolPatternsFromCEL(expr string) []string {
	if expr == "" || !strings.Contains(expr, "Tools.Has") {
		return nil
	}
	var patterns []string
	calls := toolPatternCallRe.FindAllString(expr, -1)
	for _, call := range calls {
		matches := quotedStringRe.FindAllStringSubmatch(call, -1)
		for _, m := range matches {
			if len(m) > 1 {
				patterns = append(patterns, m[1])
			}
		}
	}
	return patterns
}

// CollectRequiredToolPatterns extracts all unique required tool patterns from a list of prompts.
// Patterns come from enabledWhen CEL expressions (Tools.HasPattern, Tools.HasAllPatterns, etc.).
func CollectRequiredToolPatterns(prompts []*PromptFile) []string {
	seen := make(map[string]bool)
	var patterns []string

	addPattern := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}

	for _, p := range prompts {
		// From enabledWhen CEL expression
		for _, pattern := range extractToolPatternsFromCEL(p.EnabledWhen) {
			addPattern(pattern)
		}
	}
	return patterns
}

// CollectRequiredToolPatternsFromWebPrompts extracts all unique required tool patterns from WebPrompts.
// Patterns come from enabledWhen CEL expressions (Tools.HasPattern, Tools.HasAllPatterns, etc.).
func CollectRequiredToolPatternsFromWebPrompts(prompts []WebPrompt) []string {
	seen := make(map[string]bool)
	var patterns []string

	addPattern := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}

	for _, p := range prompts {
		// From enabledWhen CEL expression
		for _, pattern := range extractToolPatternsFromCEL(p.EnabledWhen) {
			addPattern(pattern)
		}
	}
	return patterns
}

// UpdatePromptFileEnabled reads a .prompt.yaml file, updates the enabled field,
// and writes it back. When enabling (enabled=true), the enabled key is removed
// (nil means default=true). When disabling (enabled=false), it is set explicitly.
func UpdatePromptFileEnabled(filePath string, enabled bool) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read prompt file %s: %w", filePath, err)
	}

	var prompt PromptFile
	if err := yaml.Unmarshal(data, &prompt); err != nil {
		return fmt.Errorf("failed to parse prompt file %s: %w", filePath, err)
	}

	if enabled {
		prompt.Enabled = nil // nil means enabled by default
	} else {
		f := false
		prompt.Enabled = &f
	}

	out, err := yaml.Marshal(&prompt)
	if err != nil {
		return fmt.Errorf("failed to marshal prompt file %s: %w", filePath, err)
	}

	if err := os.WriteFile(filePath, out, 0644); err != nil {
		return fmt.Errorf("failed to write prompt file %s: %w", filePath, err)
	}

	return nil
}

// SlugifyPromptName converts a prompt name to a filesystem-safe slug.
// e.g., "Add tests" → "add-tests"
func SlugifyPromptName(name string) string {
	slug := strings.ToLower(name)
	var result []byte
	lastHyphen := false
	for _, c := range slug {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, byte(c))
			lastHyphen = false
		} else if !lastHyphen {
			result = append(result, '-')
			lastHyphen = true
		}
	}
	return strings.Trim(string(result), "-")
}
