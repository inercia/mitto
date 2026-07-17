// Package session provides session persistence and management for Mitto.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	// StoppedReasonPausedByUser is a resumable (paused) reason set when the user manually
	// disables the loop (e.g. via the pause button). Re-enabling clears it.
	StoppedReasonPausedByUser StoppedReason = "pausedByUser"
	// StoppedReasonDisabledByAgent is a resumable (paused) reason set when the agent
	// self-disables the loop via mitto_conversation_update. Re-enabling clears it.
	StoppedReasonDisabledByAgent StoppedReason = "disabledByAgent"

	// StoppedReasonArchived is set when the conversation is archived (manual or auto),
	// which authoritatively stops the loop.
	StoppedReasonArchived StoppedReason = "archived"

	// StoppedReasonNoProgress is set when the onTasks trigger's circuit breaker fires
	// repeatedly with no newly-touched issue relative to the previous fire (e.g. a
	// steady-state-true condition with no genuine forward progress), auto-pausing the
	// loop to stop the hot-fire storm. Re-enabling clears it.
	StoppedReasonNoProgress StoppedReason = "noProgress"
)

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
	ErrInvalidTrigger = errors.New("invalid trigger: must be empty, schedule, onCompletion, or onTasks")
	// ErrInvalidDelay is returned when delay_seconds is negative.
	ErrInvalidDelay = errors.New("invalid delay_seconds: must be >= 0")
	// ErrInvalidMaxDuration is returned when max_duration_seconds is negative.
	ErrInvalidMaxDuration = errors.New("invalid max_duration_seconds: must be >= 0")
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
)

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
	Trigger LoopTrigger `json:"trigger,omitempty"`
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
	// NoProgressLimit overrides the onTasks Layer 3 circuit-breaker threshold —
	// the number of consecutive no-progress fires (fires that touch no issue
	// beyond what the previous fire already touched) that auto-pause the loop.
	// Nil/absent = default 3 (existing behaviour). *0 = unlimited (opt-out;
	// intended for supervisor-style loops whose steady-state legitimately
	// includes empty/at-cap fires). *N (N > 0) = custom threshold. Only
	// meaningful when Trigger is onTasks.
	NoProgressLimit *int `json:"no_progress_limit,omitempty"`
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

// EffectiveNoProgressLimit returns the effective onTasks Layer 3 circuit-breaker
// threshold for this loop. Nil/unset returns 3 (default). *0 returns 0
// (unlimited — opt-out). *N returns N. Runner callers must treat 0 as "never
// trip the breaker".
func (p *LoopPrompt) EffectiveNoProgressLimit() int {
	if p.NoProgressLimit == nil {
		return 3
	}
	return *p.NoProgressLimit
}

// ReachedMaxIterations returns true if the prompt has been delivered the maximum number of scheduled times.
// Returns false when MaxIterations is 0 (unlimited).
func (p *LoopPrompt) ReachedMaxIterations() bool {
	return p.MaxIterations > 0 && p.IterationCount >= p.MaxIterations
}

// EffectiveTrigger returns the resolved trigger type.
// When Trigger is empty, TriggerSchedule (the default) is returned.
func (p *LoopPrompt) EffectiveTrigger() LoopTrigger {
	if p.Trigger == "" {
		return TriggerSchedule
	}
	return p.Trigger
}

// IsOnCompletion returns true when this loop prompt uses the onCompletion trigger.
func (p *LoopPrompt) IsOnCompletion() bool {
	return p.EffectiveTrigger() == TriggerOnCompletion
}

// IsOnTasks returns true when this loop prompt uses the onTasks trigger.
func (p *LoopPrompt) IsOnTasks() bool {
	return p.EffectiveTrigger() == TriggerOnTasks
}

// pendingPlaceholder is the placeholder value treated as "no prompt" for preview purposes.
const pendingPlaceholder = "(pending)"

// promptPreviewMaxRunes is the maximum number of runes shown in PromptPreview.
const promptPreviewMaxRunes = 80

// PromptPreview returns a short preview of the free-text Prompt body.
// Returns "" when Prompt is empty or the literal placeholder "(pending)".
// Otherwise returns the first line, trimmed, truncated to 80 runes with a
// trailing "…" appended when the original first line exceeded that length.
// Named-prompt-only configs (PromptName set, Prompt empty) also return "".
func (p *LoopPrompt) PromptPreview() string {
	body := strings.TrimSpace(p.Prompt)
	if body == "" || body == pendingPlaceholder {
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

// Validate checks if the loop prompt configuration is valid.
func (p *LoopPrompt) Validate() error {
	if p.Prompt == "" && p.PromptName == "" {
		return ErrPromptEmpty
	}
	if p.MaxIterations < 0 {
		return ErrInvalidMaxIterations
	}
	switch p.Trigger {
	case "", TriggerSchedule, TriggerOnCompletion, TriggerOnTasks:
		// valid
	default:
		return ErrInvalidTrigger
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
	// For schedule trigger (default), Frequency must be valid.
	// For onCompletion and onTasks, frequency is not required.
	if p.EffectiveTrigger() == TriggerSchedule {
		return p.Frequency.Validate()
	}
	return nil
}

// LoopStore manages the loop prompt for a single session.
// It is safe for concurrent use.
type LoopStore struct {
	sessionDir string
	mu         sync.RWMutex
}

// NewLoopStore creates a new LoopStore for the given session directory.
func NewLoopStore(sessionDir string) *LoopStore {
	return &LoopStore{
		sessionDir: sessionDir,
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

// Update applies a partial update to the loop prompt.
// Only non-nil fields in the update are applied.
// IterationCount is never modified by Update — it is managed exclusively by RecordSent.
func (ps *LoopStore) Update(prompt *string, promptName *string, frequency *Frequency, enabled *bool, freshContext *bool, maxIterations *int, trigger *LoopTrigger, delaySeconds *int, maxDurationSeconds *int, arguments *map[string]string, condition *string, conditionPreset *string, cooldownSeconds *int, coalesceDuringBusy *bool) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	if prompt != nil {
		existing.Prompt = *prompt
	}
	if promptName != nil {
		existing.PromptName = *promptName
	}
	if frequency != nil {
		existing.Frequency = *frequency
	}
	if enabled != nil {
		existing.Enabled = *enabled
		// Re-enabling a stopped loop removes the badge so the UI shows a clean slate.
		if *enabled {
			existing.StoppedReason = ""
			existing.StoppedAt = nil
		}
	}
	if freshContext != nil {
		existing.FreshContext = *freshContext
	}
	if maxIterations != nil {
		existing.MaxIterations = *maxIterations
	}
	if trigger != nil {
		existing.Trigger = *trigger
	}
	if delaySeconds != nil {
		existing.DelaySeconds = *delaySeconds
	}
	if maxDurationSeconds != nil {
		existing.MaxDurationSeconds = *maxDurationSeconds
	}
	if arguments != nil {
		existing.Arguments = *arguments
	}
	if condition != nil {
		existing.Condition = *condition
	}
	if conditionPreset != nil {
		existing.ConditionPreset = *conditionPreset
	}
	if cooldownSeconds != nil {
		existing.CooldownSeconds = *cooldownSeconds
	}
	if coalesceDuringBusy != nil {
		v := *coalesceDuringBusy
		existing.CoalesceDuringBusy = &v
	}

	if err := existing.Validate(); err != nil {
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
func (ps *LoopStore) RecordSent() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

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
	return nil
}

// DeferNextSchedule pushes NextScheduledAt out to now+delay WITHOUT advancing the
// iteration count or LastSentAt. It is used to back off after a transient delivery
// failure so the runner does not re-fire the same prompt on every poll tick.
// It is a no-op (returns nil) for disabled configs and for onCompletion/onTasks
// triggers, whose next run is event-driven (NextScheduledAt is always nil).
func (ps *LoopStore) DeferNextSchedule(delay time.Duration) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}
	if !existing.Enabled || existing.IsOnCompletion() || existing.IsOnTasks() {
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
func (ps *LoopStore) MarkStopped(reason StoppedReason) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	existing.Enabled = false
	existing.StoppedReason = reason
	existing.StoppedAt = &now
	existing.NextScheduledAt = nil
	existing.UpdatedAt = now

	if err := fileutil.WriteJSONAtomic(ps.loopPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write loop file: %w", err)
	}
	return nil
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
// Returns nil for onCompletion/onTasks triggers — their next run is armed by the
// event-driven firing path, not a frequency-based schedule.
func (ps *LoopStore) computeNextScheduledTime(p *LoopPrompt) *time.Time {
	if !p.Enabled {
		return nil
	}
	// Event-driven triggers do not use a frequency-based schedule.
	if p.IsOnCompletion() || p.IsOnTasks() {
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
