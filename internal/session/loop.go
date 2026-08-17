// Package session provides session persistence and management for Mitto.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/inercia/mitto/internal/fileutil"
)

const (
	loopFileName = "loop.json"
	// savedLoopFileName holds a detached loop configuration preserved when a
	// conversation is reverted to a regular one via the "un-loop" button. It lets
	// the settings (prompt, frequency, trigger, enabled state, …) be restored when
	// the conversation is looped again, mirroring the archive/unarchive flow.
	savedLoopFileName = "loop.saved.json"
)

// StoppedReason is the reason a loop conversation was automatically stopped.
// These values are part of the frontend contract — do not change.
type StoppedReason string

const (
	// StoppedReasonMaxDuration is set when the wall-clock cap (MaxDurationSeconds) is reached.
	StoppedReasonMaxDuration StoppedReason = "maxDuration"
	// StoppedReasonMaxIterations is set when the per-prompt MaxIterations cap is reached.
	StoppedReasonMaxIterations StoppedReason = "maxIterations"
	// StoppedReasonIterationSafeguard is set when the global/config iteration backstop is hit
	// (MaxIterations was 0/unlimited but the effective safeguard stopped the loop).
	// Covers both the config-level default cap and the hardcoded GlobalMaxLoopIterations
	// backstop — the two cases are only distinguished in server logs, not in this
	// frontend-facing reason value.
	StoppedReasonIterationSafeguard StoppedReason = "iterationSafeguard"
	// StoppedReasonPromptUnresolved is set when the prompt name cannot be resolved after
	// MaxPromptResolveFailures consecutive failures.
	StoppedReasonPromptUnresolved StoppedReason = "promptUnresolved"
	// StoppedReasonResumeFailures is set when ACP resume fails MaxLoopResumeFailures
	// consecutive times and the session is auto-archived.
	StoppedReasonResumeFailures StoppedReason = "resumeFailures"
	// StoppedReasonContextWindowExceeded is set when the loop's prompt is rejected
	// MaxLoopContextWindowFailures consecutive times because the conversation
	// context exceeds the model's window (Augment API `augmentTooLarge` / HTTP 413).
	// The loop is auto-paused so it stops re-firing every backoff tick against a
	// context that will only grow. The user must archive the session or trim MCP
	// servers before re-enabling. Not resumable by simply toggling Enabled.
	StoppedReasonContextWindowExceeded StoppedReason = "contextWindowExceeded"
	// StoppedReasonDeliveryFailures is set when a scheduled loop's prompt
	// delivery fails MaxLoopDeliveryFailures consecutive times for a generic
	// (non context-window) reason. Before this reason existed, the schedule
	// backoff had no ceiling: a deterministically permanent failure (e.g. a
	// 404 "selected model is not available") would re-fire forever at the
	// capped backoff interval with the loop still reporting Enabled and no
	// stopped_reason (mitto-aeb). Not resumable by simply toggling Enabled —
	// the underlying delivery error must be addressed first.
	StoppedReasonDeliveryFailures StoppedReason = "deliveryFailures"

	// StoppedReasonPausedByUser is a resumable (paused) reason set when the user manually
	// disables the loop (e.g. via the pause button). Re-enabling clears it.
	StoppedReasonPausedByUser StoppedReason = "pausedByUser"
	// StoppedReasonDisabledByAgent is a resumable (paused) reason set when the agent
	// self-disables the loop via mitto_conversation_update. Re-enabling clears it.
	StoppedReasonDisabledByAgent StoppedReason = "disabledByAgent"

	// StoppedReasonArchived is set when the conversation is archived (manual or auto),
	// which authoritatively stops the loop.
	StoppedReasonArchived StoppedReason = "archived"
)

// isBenignStopReason reports whether reason is an intentional, resumable pause
// (user pause, agent self-disable, or archive) rather than a genuine automatic
// safeguard stop (max iterations/duration, resume failures, etc.). It is used
// by RecordSent's resurrection guard: an OnComplete callback captured before
// such a benign stop can legitimately still be in flight when the stop lands
// mid-turn, so that sequence must not be flagged as a resurrection (mitto-8wx).
func isBenignStopReason(reason StoppedReason) bool {
	switch reason {
	case StoppedReasonPausedByUser, StoppedReasonDisabledByAgent, StoppedReasonArchived:
		return true
	default:
		return false
	}
}

var (
	// ErrLoopNotFound is returned when no loop prompt is configured.
	ErrLoopNotFound = errors.New("loop prompt not found")
	// ErrInvalidFrequency is returned when the frequency configuration is invalid.
	ErrInvalidFrequency = errors.New("invalid frequency configuration")
	// ErrPromptEmpty is returned when the prompt text is empty.
	ErrPromptEmpty = errors.New("prompt cannot be empty")
	// ErrInvalidMaxIterations is returned when max_iterations is negative.
	ErrInvalidMaxIterations = errors.New("invalid max_iterations: must be >= 0")
	// ErrInvalidTrigger is returned when the trigger value is not recognised.
	ErrInvalidTrigger = errors.New("invalid trigger: must be empty, schedule, onCompletion, onTasks, onChild, or onSlack")
	// ErrInvalidChildEvent is returned when a child_events entry is not one of
	// the recognised onChild event names.
	ErrInvalidChildEvent = errors.New("invalid child event: must be anyEndResponse, anyDeleted, or anyLoopStopped")
	// ErrOnChildAlone is returned when onChild is the only armed trigger. The
	// onChild leg is purely reactive to a child's lifecycle, so a loop armed
	// with nothing else would never fire on its own for a conversation that has
	// no children yet.
	ErrOnChildAlone = errors.New("invalid trigger: onChild cannot be the only trigger")
	// ErrInvalidSlackSubscription is returned when an onSlack subscription has
	// missing identifiers, an unsupported filter, or duplicates another row.
	ErrInvalidSlackSubscription = errors.New("invalid Slack subscription")
	// ErrSlackSubscriptionsRequired is returned when an enabled onSlack loop has
	// no deployment-specific installation/channel subscription.
	ErrSlackSubscriptionsRequired = errors.New("onSlack requires at least one subscription when enabled")
	// ErrInvalidDelay is returned when delay_seconds is negative.
	ErrInvalidDelay = errors.New("invalid delay_seconds: must be >= 0")
	// ErrInvalidMaxDuration is returned when max_duration_seconds is negative.
	ErrInvalidMaxDuration = errors.New("invalid max_duration_seconds: must be >= 0")
	// ErrRecordSentOnStoppedLoop is returned by RecordSent when it is called on
	// a config that is already in the auto-stopped state (Enabled=false && a
	// non-empty StoppedReason). It is a belt-and-suspenders sentinel for
	// resurrection regressions (mitto-uun); the on-disk write still succeeds.
	ErrRecordSentOnStoppedLoop = errors.New("record sent called on an already-stopped loop (auto-stop resurrection)")
)

// LoopTrigger defines how/when a loop prompt is fired.
type LoopTrigger string

const (
	// TriggerSchedule is the default trigger: fire based on Frequency.
	TriggerSchedule LoopTrigger = "schedule"
	// TriggerOnCompletion fires after the agent stops responding (event-driven).
	TriggerOnCompletion LoopTrigger = "onCompletion"
	// TriggerOnTasks fires when beads/tasks in the workspace change, optionally
	// gated by a CEL Condition (event-driven).
	TriggerOnTasks LoopTrigger = "onTasks"
	// TriggerOnChild fires when a child conversation of the loop conversation
	// reaches one of the lifecycle events listed in ChildEvents (event-driven).
	// It is an additive trigger only: it cannot be armed on its own (see
	// ErrOnChildAlone).
	TriggerOnChild LoopTrigger = "onChild"
	// TriggerOnSlack fires when a matching event reaches one of the loop's Slack
	// subscriptions. It may be armed alone or alongside any other trigger.
	TriggerOnSlack LoopTrigger = "onSlack"
)

// SlackEventMode selects which normalized Slack events a subscription accepts.
type SlackEventMode string

const (
	SlackEventModeAnyHumanMessage SlackEventMode = "anyHumanMessage"
	SlackEventModeAppMention      SlackEventMode = "appMention"
)

// SlackThreadPolicy controls whether thread roots/replies are accepted.
type SlackThreadPolicy string

const (
	SlackThreadPolicyAny         SlackThreadPolicy = "any"
	SlackThreadPolicyRootOnly    SlackThreadPolicy = "rootOnly"
	SlackThreadPolicyRepliesOnly SlackThreadPolicy = "repliesOnly"
)

// SlackSubscription is one stable, credential-free Slack channel reference.
// InstallationID resolves through the process-level integration catalog; no
// token, SDK object, workspace name, or channel name is persisted in loop.json.
type SlackSubscription struct {
	InstallationID string            `json:"installation_id"`
	ChannelID      string            `json:"channel_id"`
	EventMode      SlackEventMode    `json:"event_mode,omitempty"`
	ThreadPolicy   SlackThreadPolicy `json:"thread_policy,omitempty"`
}

// ChildEvent names a child-conversation lifecycle event that arms the onChild
// trigger.
type ChildEvent string

const (
	// ChildEventAnyEndResponse fires when any child of the loop conversation
	// finishes an agent response and goes idle.
	ChildEventAnyEndResponse ChildEvent = "anyEndResponse"
	// ChildEventAnyDeleted fires when any child of the loop conversation is
	// deleted.
	ChildEventAnyDeleted ChildEvent = "anyDeleted"
	// ChildEventAnyLoopStopped fires when any child's OWN loop transitions
	// into the stopped state (LoopStore.MarkStopped), regardless of the
	// StoppedReason — a real "the child driver declared itself done" signal,
	// unlike anyEndResponse (fires on every turn) or anyDeleted (never fires
	// for archived children). Not part of DefaultChildEvents(): must be
	// opted into explicitly so existing onChild loops are unaffected.
	ChildEventAnyLoopStopped ChildEvent = "anyLoopStopped"
)

// DefaultChildEvents is the event set applied when onChild is armed without an
// explicit ChildEvents list: both recognised events.
func DefaultChildEvents() []ChildEvent {
	return []ChildEvent{ChildEventAnyEndResponse, ChildEventAnyDeleted}
}

// ConditionValidator is an optional package-level seam that compile-validates a
// CEL Condition expression. It is nil by default; the config package wires it up
// at startup to avoid an import cycle (session must stay independent of config).
// When nil, Condition compile-validation is skipped in Validate().
var ConditionValidator func(string) error

// FrequencyUnit represents the time unit for loop scheduling.
type FrequencyUnit string

const (
	FrequencyMinutes FrequencyUnit = "minutes"
	FrequencyHours   FrequencyUnit = "hours"
	FrequencyDays    FrequencyUnit = "days"
)

// Frequency defines how often the prompt should be sent.
type Frequency struct {
	// Value is the number of units between sends (e.g., 30 for "every 30 minutes").
	Value int `json:"value"`
	// Unit is the time unit (minutes, hours, days).
	Unit FrequencyUnit `json:"unit"`
	// At is the time of day in HH:MM format (UTC). Only valid for days unit.
	At string `json:"at,omitempty"`
}

// Validate checks if the frequency configuration is valid.
func (f *Frequency) Validate() error {
	if f.Value < 1 {
		return fmt.Errorf("%w: value must be >= 1", ErrInvalidFrequency)
	}

	switch f.Unit {
	case FrequencyMinutes:
		if f.At != "" {
			return fmt.Errorf("%w: 'at' is not allowed for minutes", ErrInvalidFrequency)
		}
	case FrequencyHours:
		if f.At != "" {
			return fmt.Errorf("%w: 'at' is not allowed for hours", ErrInvalidFrequency)
		}
	case FrequencyDays:
		if f.At != "" {
			// Validate HH:MM format
			if len(f.At) != 5 || f.At[2] != ':' {
				return fmt.Errorf("%w: 'at' must be in HH:MM format", ErrInvalidFrequency)
			}
			var h, m int
			if _, err := fmt.Sscanf(f.At, "%d:%d", &h, &m); err != nil {
				return fmt.Errorf("%w: 'at' must be in HH:MM format", ErrInvalidFrequency)
			}
			if h < 0 || h > 23 || m < 0 || m > 59 {
				return fmt.Errorf("%w: invalid time in 'at' field", ErrInvalidFrequency)
			}
		}
	default:
		return fmt.Errorf("%w: unit must be minutes, hours, or days", ErrInvalidFrequency)
	}

	return nil
}

// Duration returns the frequency as a time.Duration.
// For days with 'at' specified, this returns 24h * Value.
func (f *Frequency) Duration() time.Duration {
	switch f.Unit {
	case FrequencyMinutes:
		return time.Duration(f.Value) * time.Minute
	case FrequencyHours:
		return time.Duration(f.Value) * time.Hour
	case FrequencyDays:
		return time.Duration(f.Value) * 24 * time.Hour
	default:
		return 0
	}
}

// LoopPrompt represents a scheduled recurring prompt for a session.
type LoopPrompt struct {
	// Prompt is the message text to send.
	Prompt string `json:"prompt"`
	// PromptName is the name of a workspace prompt to resolve at execution time.
	// When set, the prompt text is resolved from the workspace prompts at execution time.
	// Either Prompt or PromptName must be set.
	PromptName string `json:"prompt_name,omitempty"`
	// Arguments holds user-supplied values for Go-template .Args placeholders
	// when PromptName is set. Applied to the resolved prompt text at execution time.
	// Empty for free-text prompts (Prompt field only).
	Arguments map[string]string `json:"arguments,omitempty"`
	// Frequency defines how often the prompt should be sent.
	Frequency Frequency `json:"frequency"`
	// Enabled indicates whether the loop prompt is active.
	Enabled bool `json:"enabled"`
	// FreshContext indicates whether each scheduled run should start with a clean
	// agent context (no history injection, new ACP session). Default is false.
	FreshContext bool `json:"fresh_context,omitempty"`
	// MaxIterations is the maximum number of scheduled runs to deliver (0 = unlimited).
	MaxIterations int `json:"max_iterations,omitempty"`
	// IterationCount is the number of scheduled runs delivered so far.
	IterationCount int `json:"iteration_count"`
	// CreatedAt is when the loop prompt was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the loop prompt was last modified.
	UpdatedAt time.Time `json:"updated_at"`
	// LastSentAt is when the prompt was last delivered (nil if never sent).
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	// NextScheduledAt is the computed next delivery time (nil if not scheduled).
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
	// Trigger controls how this loop prompt is fired.
	// Empty or "schedule" means frequency-based; "onCompletion" means event-driven.
	//
	// Deprecated: retained as a legacy read/write field for on-disk and wire
	// backward-compatibility (loop.json written by pre-r6j Mitto versions, and
	// older API clients that only understand a single trigger). Triggers is
	// now canonical; see EffectiveTriggers. Normalize() keeps this field in
	// sync with Triggers[0] whenever Triggers is non-empty (called by both Set
	// and Update before persisting), so it is never stale on disk. Never
	// derived the other way — Triggers is never inferred from this field
	// except by EffectiveTriggers' read-side fallback for old records that
	// predate Triggers entirely (mitto-r6j.5).
	Trigger LoopTrigger `json:"trigger,omitempty"`
	// Triggers is the canonical list of triggers that arm this loop (mitto-r6j).
	// Empty falls back to []LoopTrigger{Trigger} when Trigger is set, then to
	// []LoopTrigger{TriggerSchedule} — see EffectiveTriggers.
	Triggers []LoopTrigger `json:"triggers,omitempty"`
	// DelaySeconds is the number of seconds to wait after the agent stops responding
	// before the next run. Only meaningful when Trigger is onCompletion.
	DelaySeconds int `json:"delay_seconds,omitempty"`
	// MaxDurationSeconds is the wall-clock cap in seconds since iterating started (0 = unlimited).
	MaxDurationSeconds int `json:"max_duration_seconds,omitempty"`
	// FirstRunAt is the elapsed-time anchor: set on the first RecordSent call.
	// Used by ReachedMaxDuration to compute how long iterating has been running.
	FirstRunAt *time.Time `json:"first_run_at,omitempty"`
	// StoppedReason records why the loop was automatically stopped.
	// Empty when still running or not yet stopped.
	StoppedReason StoppedReason `json:"stopped_reason,omitempty"`
	// StoppedAt is the timestamp when the loop was auto-stopped (nil when still running).
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	// AcknowledgedStoppedReason records the StoppedReason value the user has
	// acknowledged (dismissed the sidebar warning for). The frontend warning
	// icon is suppressed when AcknowledgedStoppedReason == StoppedReason and
	// both are non-empty. Persisting this here (rather than in transient
	// per-tab state) makes the dismissal survive page reloads and sync across
	// all connected browsers. Cleared whenever the loop is re-enabled or the
	// StoppedReason changes (see MarkStopped / RecordSent).
	AcknowledgedStoppedReason StoppedReason `json:"acknowledged_stopped_reason,omitempty"`
	// Condition is a CEL expression gating onTasks firing. Empty means fire on ANY
	// beads/task change. Only meaningful when Trigger is onTasks.
	Condition string `json:"condition,omitempty"`
	// ConditionPreset is an optional UI preset id that was compiled into Condition.
	ConditionPreset string `json:"condition_preset,omitempty"`
	// CooldownSeconds is the per-conversation cooldown floor honoured by the runner
	// between onTasks firings. 0 means use the global floor.
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`
	// CoalesceDuringBusy controls how the onTasks trigger handles beads changes
	// that arrive while the loop's subtree is busy. When nil or *true (default),
	// such changes are silently absorbed by the Layer 2 quiescence rebase. When
	// *false, the quiescence rebase fires exactly once more with the accumulated
	// delta (pre-run baseline → current snapshot) before rebasing, so external
	// changes that landed during the busy window are not lost. Only meaningful
	// when Trigger is onTasks.
	CoalesceDuringBusy *bool `json:"coalesce_during_busy,omitempty"`
	// RunOnStart, when *true, causes the LoopRunner to fire this loop exactly
	// once shortly after Mitto boots (after the interactive-resume startup delay,
	// with a small anti-flap window suppressing the pulse when the loop already
	// ran very recently). Complements onTasks loops that would otherwise only
	// fire on a beads change, and lets scheduled/onCompletion loops kick off
	// immediately at startup without waiting for the next scheduled tick. Defaults
	// to false (unset) — a fresh Mitto restart does not re-fire arbitrary loops.
	RunOnStart *bool `json:"run_on_start,omitempty"`
	// SettleWindowSeconds is an optional pre-fire debounce window (seconds) for
	// the onTasks trigger. When > 0, processTasksChange arms a settle timer on
	// the idle→first-fire path (tasksActionFire) instead of firing immediately,
	// resetting the timer on each subsequent material fs-watcher delta; a single
	// coalesced fire is dispatched when the timer expires. Complements the
	// existing during-busy path (CoalesceDuringBusy + Layer 2 quiescence rebase)
	// by absorbing multi-step agent edits (e.g. `bd create` followed by
	// `bd update`) that would otherwise produce a first-delta fire on partial
	// state. 0 or nil = disabled (current fire-on-first-delta behaviour). Only
	// meaningful when Trigger is onTasks (mitto-1uv).
	SettleWindowSeconds *int `json:"settle_window_seconds,omitempty"`
	// ChildEvents lists the child-conversation lifecycle events that fire this
	// loop. Empty falls back to DefaultChildEvents() (both events). Only
	// meaningful when onChild is among the armed triggers.
	ChildEvents []ChildEvent `json:"child_events,omitempty"`
	// SlackSubscriptions lists credential-free installation/channel references
	// for onSlack. Normalize applies safe filters and canonical ordering.
	SlackSubscriptions []SlackSubscription `json:"slack_subscriptions,omitempty"`
	// unknownFields preserves future top-level loop fields across read/partial-
	// update/write cycles. Full replacements intentionally drop unknown fields.
	unknownFields map[string]json.RawMessage
}

var loopPromptKnownJSONFields = map[string]bool{
	"prompt": true, "prompt_name": true, "arguments": true, "frequency": true,
	"enabled": true, "fresh_context": true, "max_iterations": true,
	"iteration_count": true, "created_at": true, "updated_at": true,
	"last_sent_at": true, "next_scheduled_at": true, "trigger": true,
	"triggers": true, "delay_seconds": true, "max_duration_seconds": true,
	"first_run_at": true, "stopped_reason": true, "stopped_at": true,
	"acknowledged_stopped_reason": true, "condition": true,
	"condition_preset": true, "cooldown_seconds": true,
	"coalesce_during_busy": true, "run_on_start": true,
	"settle_window_seconds": true, "child_events": true,
	"slack_subscriptions": true,
}

// UnmarshalJSON preserves unknown top-level fields so a newer loop.json is not
// destructively downgraded by an unrelated partial update from an older binary.
func (p *LoopPrompt) UnmarshalJSON(data []byte) error {
	type plain LoopPrompt
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key := range raw {
		if loopPromptKnownJSONFields[key] {
			delete(raw, key)
		}
	}
	*p = LoopPrompt(decoded)
	if len(raw) > 0 {
		p.unknownFields = raw
	}
	return nil
}

// MarshalJSON restores unknown fields captured by UnmarshalJSON without
// allowing them to override fields understood by this binary.
func (p LoopPrompt) MarshalJSON() ([]byte, error) {
	type plain LoopPrompt
	data, err := json.Marshal(plain(p))
	if err != nil || len(p.unknownFields) == 0 {
		return data, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for key, value := range p.unknownFields {
		if _, known := raw[key]; !known {
			raw[key] = value
		}
	}
	return json.Marshal(raw)
}

// EffectiveChildEvents returns the resolved onChild event set: ChildEvents
// verbatim when non-empty, otherwise DefaultChildEvents().
func (p *LoopPrompt) EffectiveChildEvents() []ChildEvent {
	if len(p.ChildEvents) > 0 {
		return p.ChildEvents
	}
	return DefaultChildEvents()
}

// HasChildEvent reports whether the resolved onChild event set includes e.
func (p *LoopPrompt) HasChildEvent(e ChildEvent) bool {
	return slices.Contains(p.EffectiveChildEvents(), e)
}

// ShouldCoalesceDuringBusy reports whether the onTasks trigger should silently
// absorb changes that arrive during a busy window. Defaults to true (current
// behaviour) when the field is unset.
func (p *LoopPrompt) ShouldCoalesceDuringBusy() bool {
	if p.CoalesceDuringBusy == nil {
		return true
	}
	return *p.CoalesceDuringBusy
}

// SettleWindow returns the pre-fire settle/debounce window applied on the
// idle→first-fire path of the onTasks trigger. Returns 0 when the field is
// unset or non-positive — the current (undebounced) behaviour. Only meaningful
// when Trigger is onTasks (mitto-1uv).
func (p *LoopPrompt) SettleWindow() time.Duration {
	if p.SettleWindowSeconds == nil || *p.SettleWindowSeconds <= 0 {
		return 0
	}
	return time.Duration(*p.SettleWindowSeconds) * time.Second
}

// ShouldRunOnStart reports whether this loop should fire exactly once when
// Mitto boots. Defaults to false when the field is unset.
func (p *LoopPrompt) ShouldRunOnStart() bool {
	if p.RunOnStart == nil {
		return false
	}
	return *p.RunOnStart
}

// ReachedMaxIterations returns true if the prompt has been delivered the maximum number of scheduled times.
// Returns false when MaxIterations is 0 (unlimited).
func (p *LoopPrompt) ReachedMaxIterations() bool {
	return p.MaxIterations > 0 && p.IterationCount >= p.MaxIterations
}

// EffectiveTriggers returns the resolved trigger list (mitto-r6j): Triggers
// verbatim when non-empty, falling back to []LoopTrigger{Trigger} when the
// legacy singular Trigger field is set, falling back to
// []LoopTrigger{TriggerSchedule} (today's implicit default) when neither is set.
func (p *LoopPrompt) EffectiveTriggers() []LoopTrigger {
	if len(p.Triggers) > 0 {
		return p.Triggers
	}
	if p.Trigger != "" {
		return []LoopTrigger{p.Trigger}
	}
	return []LoopTrigger{TriggerSchedule}
}

// EffectiveTrigger returns the primary (first) resolved trigger. Existing
// single-trigger callers keep their current behaviour; multi-trigger configs
// should prefer EffectiveTriggers.
func (p *LoopPrompt) EffectiveTrigger() LoopTrigger {
	return p.EffectiveTriggers()[0]
}

// HasTrigger returns true when this loop prompt's resolved trigger list
// includes t. Multi-trigger configs (mitto-r6j.2) arm every listed trigger
// independently, so membership — not the primary/first trigger — is what the
// dispatch paths must test.
func (p *LoopPrompt) HasTrigger(t LoopTrigger) bool {
	return slices.Contains(p.EffectiveTriggers(), t)
}

// IsSchedule returns true when this loop prompt's trigger list includes schedule.
func (p *LoopPrompt) IsSchedule() bool {
	return p.HasTrigger(TriggerSchedule)
}

// IsOnCompletion returns true when this loop prompt's trigger list includes onCompletion.
func (p *LoopPrompt) IsOnCompletion() bool {
	return p.HasTrigger(TriggerOnCompletion)
}

// IsOnTasks returns true when this loop prompt's trigger list includes onTasks.
func (p *LoopPrompt) IsOnTasks() bool {
	return p.HasTrigger(TriggerOnTasks)
}

// IsOnChild returns true when this loop prompt's trigger list includes onChild.
func (p *LoopPrompt) IsOnChild() bool {
	return p.HasTrigger(TriggerOnChild)
}

// IsOnSlack returns true when this loop prompt's trigger list includes onSlack.
func (p *LoopPrompt) IsOnSlack() bool {
	return p.HasTrigger(TriggerOnSlack)
}

// pendingPlaceholder is a legacy draft placeholder written by older frontends
// when a conversation was turned into a loop before a prompt was chosen. New
// drafts store an empty Prompt; the constant is kept so configs already on disk
// are still normalised to "no prompt" instead of being delivered literally.
const pendingPlaceholder = "(pending)"

// promptPreviewMaxRunes is the maximum number of runes shown in PromptPreview.
const promptPreviewMaxRunes = 80

// EffectivePromptBody returns the free-text prompt body, trimmed, with the
// legacy "(pending)" draft placeholder normalised to "". This is the only
// value that may be delivered as a prompt — callers must never use the raw
// Prompt field for dispatch.
func (p *LoopPrompt) EffectivePromptBody() string {
	body := strings.TrimSpace(p.Prompt)
	if body == pendingPlaceholder {
		return ""
	}
	return body
}

// HasPrompt reports whether the config has anything deliverable: either a
// non-placeholder free-text body or a named prompt to resolve.
func (p *LoopPrompt) HasPrompt() bool {
	return p.EffectivePromptBody() != "" || p.PromptName != ""
}

// PromptPreview returns a short preview of the free-text Prompt body.
// Returns "" when the effective body is empty (including the legacy
// "(pending)" placeholder). Otherwise returns the first line, trimmed,
// truncated to 80 runes with a trailing "…" appended when the original first
// line exceeded that length. Named-prompt-only configs also return "".
func (p *LoopPrompt) PromptPreview() string {
	body := p.EffectivePromptBody()
	if body == "" {
		return ""
	}
	// Use the first line only.
	firstLine := body
	if idx := strings.IndexByte(body, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(body[:idx])
	}
	if utf8.RuneCountInString(firstLine) <= promptPreviewMaxRunes {
		return firstLine
	}
	// Truncate to promptPreviewMaxRunes runes and append ellipsis.
	runes := []rune(firstLine)
	return string(runes[:promptPreviewMaxRunes]) + "…"
}

// ReachedMaxDuration returns true if the elapsed time since the first run exceeds MaxDurationSeconds.
// Returns false when MaxDurationSeconds is 0 (unlimited) or FirstRunAt is nil (not yet started).
func (p *LoopPrompt) ReachedMaxDuration(now time.Time) bool {
	if p.MaxDurationSeconds <= 0 || p.FirstRunAt == nil {
		return false
	}
	return now.Sub(*p.FirstRunAt) >= time.Duration(p.MaxDurationSeconds)*time.Second
}

// ClampDelay ensures DelaySeconds is at least floorSeconds.
// Only applies when the trigger is onCompletion; schedule prompts are not clamped.
// The floor value is injected by the caller — this method does NOT hardcode any policy minimum.
func (p *LoopPrompt) ClampDelay(floorSeconds int) {
	if !p.IsOnCompletion() {
		return
	}
	if p.DelaySeconds < floorSeconds {
		p.DelaySeconds = floorSeconds
	}
}

// Normalize rewrites the legacy "(pending)" draft placeholder to an empty
// Prompt so it is never persisted — and therefore never delivered — as a
// literal prompt body. It also keeps the legacy scalar Trigger field in sync
// with Triggers[0] whenever Triggers is non-empty, so any on-disk record
// written by Set/Update always has an accurate Trigger for old readers
// (mitto-r6j.5). This only ever derives Trigger FROM Triggers — Triggers is
// never (re)derived from Trigger here; that direction is handled purely as a
// read-side fallback by EffectiveTriggers for records that predate Triggers.
// Callers should invoke it before Validate/persist.
func (p *LoopPrompt) Normalize() {
	if strings.TrimSpace(p.Prompt) == pendingPlaceholder {
		p.Prompt = ""
	}
	if len(p.Triggers) > 0 {
		p.Trigger = p.Triggers[0]
	}
	for i := range p.SlackSubscriptions {
		sub := &p.SlackSubscriptions[i]
		sub.InstallationID = strings.TrimSpace(sub.InstallationID)
		sub.ChannelID = strings.TrimSpace(sub.ChannelID)
		if sub.EventMode == "" {
			sub.EventMode = SlackEventModeAnyHumanMessage
		}
		if sub.ThreadPolicy == "" {
			sub.ThreadPolicy = SlackThreadPolicyAny
		}
	}
	sort.SliceStable(p.SlackSubscriptions, func(i, j int) bool {
		a, b := p.SlackSubscriptions[i], p.SlackSubscriptions[j]
		if a.InstallationID != b.InstallationID {
			return a.InstallationID < b.InstallationID
		}
		if a.ChannelID != b.ChannelID {
			return a.ChannelID < b.ChannelID
		}
		if a.EventMode != b.EventMode {
			return a.EventMode < b.EventMode
		}
		return a.ThreadPolicy < b.ThreadPolicy
	})
}

// Validate checks if the loop prompt configuration is valid. Full writes reject
// trigger names unknown to this binary.
// An enabled config must have something deliverable (free-text body or a named
// prompt). A disabled config may have neither: that is the draft state created
// when a conversation is turned into a loop before a prompt is chosen.
func (p *LoopPrompt) Validate() error {
	return p.validate(false)
}

func (p *LoopPrompt) validate(preserveUnknownTriggers bool) error {
	if p.Enabled && !p.HasPrompt() {
		return ErrPromptEmpty
	}
	if p.MaxIterations < 0 {
		return ErrInvalidMaxIterations
	}
	switch p.Trigger {
	case "", TriggerSchedule, TriggerOnCompletion, TriggerOnTasks, TriggerOnChild, TriggerOnSlack:
		// valid
	default:
		if !preserveUnknownTriggers {
			return ErrInvalidTrigger
		}
	}
	seenTriggers := make(map[LoopTrigger]bool, len(p.Triggers))
	for _, t := range p.Triggers {
		switch t {
		case TriggerSchedule, TriggerOnCompletion, TriggerOnTasks, TriggerOnChild, TriggerOnSlack:
			// valid
		default:
			if !preserveUnknownTriggers {
				return ErrInvalidTrigger
			}
		}
		if seenTriggers[t] {
			return fmt.Errorf("%w: duplicate trigger %q", ErrInvalidTrigger, t)
		}
		seenTriggers[t] = true
	}
	// Reject unknown child_events entries. Validated against the raw slice, not
	// EffectiveChildEvents() — the defaults are valid by construction, so
	// validating the effective set would waste work and mask nothing.
	for _, e := range p.ChildEvents {
		switch e {
		case ChildEventAnyEndResponse, ChildEventAnyDeleted, ChildEventAnyLoopStopped:
			// valid
		default:
			return fmt.Errorf("%w: %q", ErrInvalidChildEvent, e)
		}
	}
	// onChild is purely reactive to a child's lifecycle, so it must never be
	// the only armed trigger. Evaluated against EffectiveTriggers() so this
	// also catches the legacy scalar form (Trigger: onChild alone).
	if eff := p.EffectiveTriggers(); len(eff) == 1 && eff[0] == TriggerOnChild {
		return ErrOnChildAlone
	}
	seenSlack := make(map[string]bool, len(p.SlackSubscriptions))
	for _, sub := range p.SlackSubscriptions {
		if sub.InstallationID == "" || sub.ChannelID == "" {
			return fmt.Errorf("%w: installation_id and channel_id are required", ErrInvalidSlackSubscription)
		}
		switch sub.EventMode {
		case SlackEventModeAnyHumanMessage, SlackEventModeAppMention:
		default:
			return fmt.Errorf("%w: unsupported event_mode %q", ErrInvalidSlackSubscription, sub.EventMode)
		}
		switch sub.ThreadPolicy {
		case SlackThreadPolicyAny, SlackThreadPolicyRootOnly, SlackThreadPolicyRepliesOnly:
		default:
			return fmt.Errorf("%w: unsupported thread_policy %q", ErrInvalidSlackSubscription, sub.ThreadPolicy)
		}
		key := sub.InstallationID + "\x00" + sub.ChannelID
		if seenSlack[key] {
			return fmt.Errorf("%w: duplicate installation/channel reference", ErrInvalidSlackSubscription)
		}
		seenSlack[key] = true
	}
	if p.Enabled && p.IsOnSlack() && len(p.SlackSubscriptions) == 0 {
		return ErrSlackSubscriptionsRequired
	}
	if p.DelaySeconds < 0 {
		return ErrInvalidDelay
	}
	if p.MaxDurationSeconds < 0 {
		return ErrInvalidMaxDuration
	}
	if p.Condition != "" && ConditionValidator != nil {
		if err := ConditionValidator(p.Condition); err != nil {
			return fmt.Errorf("invalid condition: %w", err)
		}
	}
	// Frequency must be valid whenever schedule is one of the armed triggers —
	// membership, not primacy, since a multi-trigger config such as
	// [onTasks, schedule] still runs a schedule leg (mitto-r6j.2).
	// For onCompletion and onTasks alone, frequency is not required.
	if p.IsSchedule() {
		return p.Frequency.Validate()
	}
	return nil
}

// LoopStore manages the loop prompt for a single session.
// It is safe for concurrent use.
type LoopStore struct {
	sessionDir string
	mu         sync.RWMutex

	// sessionID and stoppedObserver, when both set, let MarkStopped notify a
	// listener (the LoopRunner, via Store.SetLoopStoppedObserver) once a loop
	// stops (see MarkStopped for how a stop transition is detected, which
	// covers the Update{Enabled:false}-then-MarkStopped disable paths as well
	// as a direct MarkStopped on an enabled loop). Populated only by
	// Store.Loop(sessionID); a bare NewLoopStore has neither, so MarkStopped
	// is a pure no-op notification-wise (used by tests and by any caller that
	// constructs a LoopStore directly rather than through a *Store).
	sessionID       string
	stoppedObserver func(sessionID string, reason StoppedReason)
}

// NewLoopStore creates a new LoopStore for the given session directory.
func NewLoopStore(sessionDir string) *LoopStore {
	return &LoopStore{
		sessionDir: sessionDir,
	}
}

// newLoopStoreWithObserver creates a LoopStore wired to notify obs (if
// non-nil) from MarkStopped. Used exclusively by Store.Loop(sessionID) so the
// onChild "anyLoopStopped" event (mitto-q6my) can be raised without adding a
// sessionID parameter to the public NewLoopStore constructor.
func newLoopStoreWithObserver(sessionDir, sessionID string, obs func(sessionID string, reason StoppedReason)) *LoopStore {
	return &LoopStore{
		sessionDir:      sessionDir,
		sessionID:       sessionID,
		stoppedObserver: obs,
	}
}

// loopPath returns the path to the loop.json file.
func (ps *LoopStore) loopPath() string {
	return filepath.Join(ps.sessionDir, loopFileName)
}

// Get retrieves the current loop prompt configuration.
// Returns ErrLoopNotFound if no loop prompt is configured.
func (ps *LoopStore) Get() (*LoopPrompt, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var p LoopPrompt
	err := fileutil.ReadJSON(ps.loopPath(), &p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrLoopNotFound
		}
		return nil, fmt.Errorf("failed to read loop file: %w", err)
	}
	return &p, nil
}

// Set creates or replaces the loop prompt configuration.
func (ps *LoopStore) Set(p *LoopPrompt) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now().UTC()

	// Check if this is an update or create
	existing, err := ps.getUnlocked()
	if err == nil && existing != nil {
		// Preserve immutable/accumulated fields across a replace.
		// IterationCount is preserved so re-saving config doesn't reset the delivery counter;
		// the counter only resets if the user explicitly sets it via the API (not supported yet).
		// FirstRunAt is preserved so the maxDuration elapsed-time anchor is not lost on config replace.
		p.CreatedAt = existing.CreatedAt
		p.LastSentAt = existing.LastSentAt
		p.IterationCount = existing.IterationCount
		p.FirstRunAt = existing.FirstRunAt

		// Sticky auto-stop (mitto-uun): once a loop has been MarkStopped'd (e.g. after
		// reaching its max-iterations or max-duration cap), a subsequent Set() that
		// rewrites the config with Enabled=true must not silently resurrect it. Callers
		// that legitimately want to un-stop a loop go through Update(enabled=&true, …)
		// (which clears StoppedReason/StoppedAt explicitly). Preserving the stopped
		// state here closes the write-ordering / clobber path that caused the on-disk
		// state to diverge from the runtime auto-stop across a restart.
		if !existing.Enabled && existing.StoppedReason != "" {
			p.Enabled = existing.Enabled
			p.StoppedReason = existing.StoppedReason
			p.StoppedAt = existing.StoppedAt
			p.AcknowledgedStoppedReason = existing.AcknowledgedStoppedReason
		}
	} else {
		// Create: set created_at
		p.CreatedAt = now
	}

	p.UpdatedAt = now
	p.NextScheduledAt = ps.computeNextScheduledTime(p)

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), p, 0644); err != nil {
		return fmt.Errorf("failed to write loop file: %w", err)
	}
	return nil
}

// RestoreSnapshot replaces loop.json without changing timestamps or counters.
// It is reserved for compensating rollback after a cross-store transaction.
func (ps *LoopStore) RestoreSnapshot(p *LoopPrompt) error {
	if p == nil {
		return ErrLoopNotFound
	}
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(ps.loopPath(), p, 0644); err != nil {
		return fmt.Errorf("failed to restore loop snapshot: %w", err)
	}
	return nil
}

// LoopUpdate holds a partial update to a loop prompt configuration. Only
// non-nil fields are applied by LoopStore.Update; all others are left
// untouched. Replaces the historical 15-positional-pointer-parameter Update
// signature (mitto-r6j.5) — the growing per-trigger field set made positional
// params untenable and error-prone at call sites.
type LoopUpdate struct {
	Prompt        *string
	PromptName    *string
	Frequency     *Frequency
	Enabled       *bool
	FreshContext  *bool
	MaxIterations *int
	// Triggers, when non-nil, REPLACES the stored trigger list wholesale (not
	// merged/appended). A nil pointer means "leave the trigger list
	// unchanged" — the same pointer-presence semantics as every other field
	// here. Update NEVER derives Triggers from a legacy scalar trigger value;
	// Normalize() (called below) keeps the legacy Trigger field in sync with
	// Triggers[0] purely for on-disk/wire back-compat with old readers.
	Triggers *[]LoopTrigger
	// ChildEvents, when non-nil, REPLACES the stored child-event list
	// wholesale (same semantics as Triggers). A nil pointer means "leave the
	// child-event list unchanged".
	ChildEvents *[]ChildEvent
	// SlackSubscriptions follows the same nil=unchanged, non-nil=replace-whole
	// convention as Triggers and ChildEvents. A present empty slice clears it.
	SlackSubscriptions *[]SlackSubscription
	DelaySeconds       *int
	MaxDurationSeconds *int
	Arguments          *map[string]string
	Condition          *string
	ConditionPreset    *string
	CooldownSeconds    *int
	CoalesceDuringBusy *bool
	RunOnStart         *bool
	// SettleWindowSeconds is a partial update for the onTasks pre-fire debounce
	// window (mitto-r6j.5 — previously an orphan field with no write path).
	SettleWindowSeconds *int
}

// Update applies a partial update to the loop prompt.
// Only non-nil fields in u are applied.
// IterationCount is never modified by Update — it is managed exclusively by RecordSent.
func (ps *LoopStore) Update(u LoopUpdate) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	if u.Prompt != nil {
		existing.Prompt = *u.Prompt
	}
	if u.PromptName != nil {
		existing.PromptName = *u.PromptName
	}
	if u.Frequency != nil {
		existing.Frequency = *u.Frequency
	}
	if u.Enabled != nil {
		existing.Enabled = *u.Enabled
		// Re-enabling a stopped loop removes the badge so the UI shows a clean slate.
		if *u.Enabled {
			existing.StoppedReason = ""
			existing.StoppedAt = nil
			// Drop any stale acknowledgment so a future error stop always
			// surfaces the sidebar warning again.
			existing.AcknowledgedStoppedReason = ""
		}
	}
	if u.FreshContext != nil {
		existing.FreshContext = *u.FreshContext
	}
	if u.MaxIterations != nil {
		existing.MaxIterations = *u.MaxIterations
	}
	if u.Triggers != nil {
		// Replace wholesale. Normalize() (below) syncs the legacy scalar
		// Trigger field from Triggers[0] — never the other way around.
		existing.Triggers = *u.Triggers
	}
	if u.ChildEvents != nil {
		// Replace wholesale, same as Triggers.
		existing.ChildEvents = *u.ChildEvents
	}
	if u.SlackSubscriptions != nil {
		existing.SlackSubscriptions = *u.SlackSubscriptions
	}
	if u.DelaySeconds != nil {
		existing.DelaySeconds = *u.DelaySeconds
	}
	if u.MaxDurationSeconds != nil {
		existing.MaxDurationSeconds = *u.MaxDurationSeconds
	}
	if u.Arguments != nil {
		existing.Arguments = *u.Arguments
	}
	if u.Condition != nil {
		existing.Condition = *u.Condition
	}
	if u.ConditionPreset != nil {
		existing.ConditionPreset = *u.ConditionPreset
	}
	if u.CooldownSeconds != nil {
		existing.CooldownSeconds = *u.CooldownSeconds
	}
	if u.CoalesceDuringBusy != nil {
		v := *u.CoalesceDuringBusy
		existing.CoalesceDuringBusy = &v
	}
	if u.RunOnStart != nil {
		v := *u.RunOnStart
		existing.RunOnStart = &v
	}
	if u.SettleWindowSeconds != nil {
		v := *u.SettleWindowSeconds
		existing.SettleWindowSeconds = &v
	}

	existing.Normalize()
	// A partial update that leaves Triggers untouched must preserve trigger names
	// written by a newer Mitto version. Explicit trigger replacement remains
	// strict and rejects values this binary cannot execute.
	if err := existing.validate(u.Triggers == nil); err != nil {
		return err
	}

	existing.UpdatedAt = time.Now().UTC()
	existing.NextScheduledAt = ps.computeNextScheduledTime(existing)

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write loop file: %w", err)
	}
	return nil
}

// Delete removes the loop prompt configuration.
func (ps *LoopStore) Delete() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	err := os.Remove(ps.loopPath())
	if err != nil {
		if os.IsNotExist(err) {
			return ErrLoopNotFound
		}
		return fmt.Errorf("failed to delete loop file: %w", err)
	}
	return nil
}

// savedLoopPath returns the path to the detached loop.saved.json file.
func (ps *LoopStore) savedLoopPath() string {
	return filepath.Join(ps.sessionDir, savedLoopFileName)
}

// Detach preserves the current loop configuration and removes the active one.
// It copies loop.json to loop.saved.json and then deletes loop.json, so the
// conversation reverts to a regular one (loop_configured=false) while its
// settings survive for a later Restore. Any previously-saved config is
// overwritten. Returns ErrLoopNotFound if no loop config exists.
func (ps *LoopStore) Detach() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	if err := fileutil.WriteJSONAtomic(ps.savedLoopPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write saved loop file: %w", err)
	}
	if err := os.Remove(ps.loopPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete loop file: %w", err)
	}
	return nil
}

// GetSaved retrieves the detached loop configuration preserved by Detach.
// Returns ErrLoopNotFound if no saved config exists.
func (ps *LoopStore) GetSaved() (*LoopPrompt, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var p LoopPrompt
	err := fileutil.ReadJSON(ps.savedLoopPath(), &p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrLoopNotFound
		}
		return nil, fmt.Errorf("failed to read saved loop file: %w", err)
	}
	return &p, nil
}

// ClearSaved removes the detached loop configuration. It is a no-op (returns nil)
// when no saved config exists.
func (ps *LoopStore) ClearSaved() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if err := os.Remove(ps.savedLoopPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete saved loop file: %w", err)
	}
	return nil
}

// ResetCounters resets the iteration and elapsed-time anchors so the loop starts
// fresh: IterationCount is set to 0, FirstRunAt is cleared (elapsed time = 0), and
// LastSentAt is cleared (never-sent). This is used when restoring a loop
// conversation that was auto-stopped after reaching its max-iterations or
// max-duration cap. Clearing LastSentAt makes the conversation look brand-new so
// that the restore behaves like the initial run: an onCompletion loop bootstraps
// its first run immediately (no delay_seconds wait — the delay is a between-runs
// gap, not a pre-first-run delay) rather than waiting out the configured delay. It
// does not change Enabled or the prompt configuration; re-enabling is handled
// separately by Update.
func (ps *LoopStore) ResetCounters() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	existing.IterationCount = 0
	existing.FirstRunAt = nil
	existing.LastSentAt = nil
	existing.UpdatedAt = time.Now().UTC()

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write loop file: %w", err)
	}
	return nil
}

// RecordSent updates the last_sent_at timestamp, increments iteration_count, and computes next_scheduled_at.
//
// Returns a non-nil sentinel error ErrRecordSentOnStoppedLoop wrapped alongside a
// successful write when RecordSent is called on a config that is already in the
// auto-stopped state (Enabled=false && StoppedReason != ""). This is a
// belt-and-suspenders detector for the mitto-uun class of resurrection bugs:
// callers can errors.Is-check it and log a WARN without changing behavior. The
// on-disk write still happens (existing behavior preserved) so a caller that
// ignores the error observes no regression.
func (ps *LoopStore) RecordSent() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	// Sanity: detect delivery on an already-stopped loop (mitto-uun regression guard).
	// Benign, intentional stops (pausedByUser/disabledByAgent/archived) are excluded:
	// they can legitimately land mid-turn, after the delivering RecordSent call was
	// already in flight, so treating them as a resurrection is a false positive
	// (mitto-8wx). Only genuine auto-stop safeguards should trip the sentinel.
	stoppedResurrection := !existing.Enabled && existing.StoppedReason != "" && !isBenignStopReason(existing.StoppedReason)

	now := time.Now().UTC()
	// Set the elapsed-time anchor on the very first delivery; preserve it thereafter.
	if existing.FirstRunAt == nil {
		existing.FirstRunAt = &now
	}
	existing.IterationCount++
	existing.LastSentAt = &now
	existing.UpdatedAt = now
	existing.NextScheduledAt = ps.computeNextScheduledTime(existing)

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write loop file: %w", err)
	}
	if stoppedResurrection {
		return ErrRecordSentOnStoppedLoop
	}
	return nil
}

// DeferNextSchedule pushes NextScheduledAt out to now+delay WITHOUT advancing the
// iteration count or LastSentAt. It is used to back off after a transient delivery
// failure so the runner does not re-fire the same prompt on every poll tick.
// It is a no-op (returns nil) for disabled configs and for configs without a
// schedule leg, whose next run is purely event-driven (NextScheduledAt is
// always nil).
func (ps *LoopStore) DeferNextSchedule(delay time.Duration) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}
	if !existing.Enabled || !existing.IsSchedule() {
		return nil
	}

	now := time.Now().UTC()
	next := now.Add(delay)
	existing.NextScheduledAt = &next
	existing.UpdatedAt = now

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write loop file: %w", err)
	}
	return nil
}

// MarkStopped disables the loop prompt and records the reason it was stopped.
// It sets Enabled=false, StoppedReason=reason, StoppedAt=now (UTC),
// NextScheduledAt=nil, and UpdatedAt=now.
// Returns ErrLoopNotFound if no loop config exists.
//
// On a real stop transition — and when this LoopStore was constructed via
// Store.Loop(sessionID) — the registered stopped-observer (see
// Store.SetLoopStoppedObserver) is notified after the write and after ps.mu
// is released — mirroring Store.deleteObserver's invocation discipline so the
// callback may safely call back into the store. This funnels every
// MarkStopped call site (loop_runner auto-stops, MCP loop_enabled:false,
// REST pause, etc.) into a single "child loop stopped" signal for the
// onChild "anyLoopStopped" event (mitto-q6my) without each site needing to
// remember to notify.
//
// A transition is detected as "was enabled, OR has no StoppedReason recorded
// yet". The second arm matters because the two caller-initiated disable paths
// (MCP loop_enabled:false in tools_conversation_lifecycle.go and the REST
// pause in session_loop_write.go) run Update{Enabled:false} *before*
// MarkStopped, so Enabled is already false by the time this is reached and an
// enabled-only check would never fire for them. StoppedReason is written
// exclusively here and cleared exclusively on re-enable (see Update), so it is
// a reliable "this stop was already recorded" marker: a re-stamp of a config
// MarkStopped has already seen never re-notifies.
func (ps *LoopStore) MarkStopped(reason StoppedReason) error {
	ps.mu.Lock()

	existing, err := ps.getUnlocked()
	if err != nil {
		ps.mu.Unlock()
		return err
	}

	isTransition := existing.Enabled || existing.StoppedReason == ""

	now := time.Now().UTC()
	existing.Enabled = false
	existing.StoppedReason = reason
	existing.StoppedAt = &now
	existing.NextScheduledAt = nil
	existing.UpdatedAt = now
	// Clear a stale acknowledgment when the reason changes so a *new* error
	// stop always re-surfaces the sidebar warning even if a prior reason had
	// been acknowledged.
	if existing.AcknowledgedStoppedReason != reason {
		existing.AcknowledgedStoppedReason = ""
	}

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), existing, 0644); err != nil {
		ps.mu.Unlock()
		return fmt.Errorf("failed to write loop file: %w", err)
	}

	observer := ps.stoppedObserver
	sessionID := ps.sessionID
	ps.mu.Unlock()

	if isTransition && observer != nil && sessionID != "" {
		observer(sessionID, reason)
	}
	return nil
}

// AcknowledgeStoppedReason records that the user has acknowledged (dismissed)
// the current StoppedReason so the sidebar warning icon disappears in every
// connected browser. It sets AcknowledgedStoppedReason = StoppedReason and
// updates UpdatedAt. Returns ErrLoopNotFound if no loop config exists.
//
// No-op (no disk write) when the loop has no StoppedReason or the current
// StoppedReason is already acknowledged, so repeated focus events do not
// churn disk or fire redundant broadcasts.
func (ps *LoopStore) AcknowledgeStoppedReason() (*LoopPrompt, bool, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return nil, false, err
	}
	if existing.StoppedReason == "" {
		return existing, false, nil
	}
	if existing.AcknowledgedStoppedReason == existing.StoppedReason {
		return existing, false, nil
	}

	existing.AcknowledgedStoppedReason = existing.StoppedReason
	existing.UpdatedAt = time.Now().UTC()

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), existing, 0644); err != nil {
		return nil, false, fmt.Errorf("failed to write loop file: %w", err)
	}
	return existing, true, nil
}

// getUnlocked reads the loop file without locking (caller must hold lock).
func (ps *LoopStore) getUnlocked() (*LoopPrompt, error) {
	var p LoopPrompt
	err := fileutil.ReadJSON(ps.loopPath(), &p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrLoopNotFound
		}
		return nil, fmt.Errorf("failed to read loop file: %w", err)
	}
	return &p, nil
}

// computeNextScheduledTime calculates when the next prompt should be sent.
// Returns nil when schedule is not one of the armed triggers — a purely
// event-driven loop's next run is armed by its firing path, not a
// frequency-based schedule. A multi-trigger config that lists schedule
// alongside onCompletion/onTasks still gets a next-run anchor (mitto-r6j.2).
func (ps *LoopStore) computeNextScheduledTime(p *LoopPrompt) *time.Time {
	if !p.Enabled {
		return nil
	}
	if !p.IsSchedule() {
		return nil
	}

	now := time.Now().UTC()
	var next time.Time

	if p.LastSentAt == nil {
		// Never sent before - schedule based on current time
		if p.Frequency.Unit == FrequencyDays && p.Frequency.At != "" {
			// For days with 'at', schedule for next occurrence of that time
			next = ps.nextTimeAt(now, p.Frequency.At, p.Frequency.Value)
		} else {
			// For other units, schedule after one interval
			next = now.Add(p.Frequency.Duration())
		}
	} else {
		// Sent before - schedule next based on last sent time
		if p.Frequency.Unit == FrequencyDays && p.Frequency.At != "" {
			next = ps.nextTimeAt(*p.LastSentAt, p.Frequency.At, p.Frequency.Value)
		} else {
			next = p.LastSentAt.Add(p.Frequency.Duration())
		}
		// If computed time is in the past, schedule from now
		if next.Before(now) {
			if p.Frequency.Unit == FrequencyDays && p.Frequency.At != "" {
				next = ps.nextTimeAt(now, p.Frequency.At, p.Frequency.Value)
			} else {
				next = now.Add(p.Frequency.Duration())
			}
		}
	}

	return &next
}

// nextTimeAt computes the next occurrence of a specific time (HH:MM UTC).
func (ps *LoopStore) nextTimeAt(from time.Time, at string, days int) time.Time {
	var h, m int
	fmt.Sscanf(at, "%d:%d", &h, &m)

	// Start with today at the specified time
	next := time.Date(from.Year(), from.Month(), from.Day(), h, m, 0, 0, time.UTC)

	// If that time has passed today, move to next occurrence
	if !next.After(from) {
		next = next.AddDate(0, 0, days)
	}

	return next
}
