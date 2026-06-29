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
	periodicFileName = "periodic.json"
)

// StoppedReason is the reason a periodic conversation was automatically stopped.
// These values are part of the frontend contract — do not change.
type StoppedReason string

const (
	// StoppedReasonMaxDuration is set when the wall-clock cap (MaxDurationSeconds) is reached.
	StoppedReasonMaxDuration StoppedReason = "maxDuration"
	// StoppedReasonMaxIterations is set when the per-prompt MaxIterations cap is reached.
	StoppedReasonMaxIterations StoppedReason = "maxIterations"
	// StoppedReasonIterationSafeguard is set when the global/config iteration backstop is hit
	// (MaxIterations was 0/unlimited but the effective safeguard stopped the loop).
	StoppedReasonIterationSafeguard StoppedReason = "iterationSafeguard"
	// StoppedReasonPromptUnresolved is set when the prompt name cannot be resolved after
	// MaxPromptResolveFailures consecutive failures.
	StoppedReasonPromptUnresolved StoppedReason = "promptUnresolved"
	// StoppedReasonResumeFailures is set when ACP resume fails MaxPeriodicResumeFailures
	// consecutive times and the session is auto-archived.
	StoppedReasonResumeFailures StoppedReason = "resumeFailures"

	// StoppedReasonPausedByUser is a resumable (paused) reason set when the user manually
	// disables the loop (e.g. via the pause button). Re-enabling clears it.
	StoppedReasonPausedByUser StoppedReason = "pausedByUser"
	// StoppedReasonDisabledByAgent is a resumable (paused) reason set when the agent
	// self-disables the loop via mitto_conversation_update. Re-enabling clears it.
	StoppedReasonDisabledByAgent StoppedReason = "disabledByAgent"

	// StoppedReasonArchived is set when the conversation is archived (manual or auto),
	// which authoritatively stops the periodic loop.
	StoppedReasonArchived StoppedReason = "archived"
)

var (
	// ErrPeriodicNotFound is returned when no periodic prompt is configured.
	ErrPeriodicNotFound = errors.New("periodic prompt not found")
	// ErrInvalidFrequency is returned when the frequency configuration is invalid.
	ErrInvalidFrequency = errors.New("invalid frequency configuration")
	// ErrPromptEmpty is returned when the prompt text is empty.
	ErrPromptEmpty = errors.New("prompt cannot be empty")
	// ErrInvalidMaxIterations is returned when max_iterations is negative.
	ErrInvalidMaxIterations = errors.New("invalid max_iterations: must be >= 0")
	// ErrInvalidTrigger is returned when the trigger value is not recognised.
	ErrInvalidTrigger = errors.New("invalid trigger: must be empty, schedule, or onCompletion")
	// ErrInvalidDelay is returned when delay_seconds is negative.
	ErrInvalidDelay = errors.New("invalid delay_seconds: must be >= 0")
	// ErrInvalidMaxDuration is returned when max_duration_seconds is negative.
	ErrInvalidMaxDuration = errors.New("invalid max_duration_seconds: must be >= 0")
)

// PeriodicTrigger defines how/when a periodic prompt is fired.
type PeriodicTrigger string

const (
	// TriggerSchedule is the default trigger: fire based on Frequency.
	TriggerSchedule PeriodicTrigger = "schedule"
	// TriggerOnCompletion fires after the agent stops responding (event-driven).
	TriggerOnCompletion PeriodicTrigger = "onCompletion"
)

// FrequencyUnit represents the time unit for periodic scheduling.
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

// PeriodicPrompt represents a scheduled recurring prompt for a session.
type PeriodicPrompt struct {
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
	// Enabled indicates whether the periodic prompt is active.
	Enabled bool `json:"enabled"`
	// FreshContext indicates whether each scheduled run should start with a clean
	// agent context (no history injection, new ACP session). Default is false.
	FreshContext bool `json:"fresh_context,omitempty"`
	// MaxIterations is the maximum number of scheduled runs to deliver (0 = unlimited).
	MaxIterations int `json:"max_iterations,omitempty"`
	// IterationCount is the number of scheduled runs delivered so far.
	IterationCount int `json:"iteration_count"`
	// CreatedAt is when the periodic prompt was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the periodic prompt was last modified.
	UpdatedAt time.Time `json:"updated_at"`
	// LastSentAt is when the prompt was last delivered (nil if never sent).
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	// NextScheduledAt is the computed next delivery time (nil if not scheduled).
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
	// Trigger controls how this periodic prompt is fired.
	// Empty or "schedule" means frequency-based; "onCompletion" means event-driven.
	Trigger PeriodicTrigger `json:"trigger,omitempty"`
	// DelaySeconds is the number of seconds to wait after the agent stops responding
	// before the next run. Only meaningful when Trigger is onCompletion.
	DelaySeconds int `json:"delay_seconds,omitempty"`
	// MaxDurationSeconds is the wall-clock cap in seconds since iterating started (0 = unlimited).
	MaxDurationSeconds int `json:"max_duration_seconds,omitempty"`
	// FirstRunAt is the elapsed-time anchor: set on the first RecordSent call.
	// Used by ReachedMaxDuration to compute how long iterating has been running.
	FirstRunAt *time.Time `json:"first_run_at,omitempty"`
	// StoppedReason records why the periodic loop was automatically stopped.
	// Empty when still running or not yet stopped.
	StoppedReason StoppedReason `json:"stopped_reason,omitempty"`
	// StoppedAt is the timestamp when the loop was auto-stopped (nil when still running).
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
}

// ReachedMaxIterations returns true if the prompt has been delivered the maximum number of scheduled times.
// Returns false when MaxIterations is 0 (unlimited).
func (p *PeriodicPrompt) ReachedMaxIterations() bool {
	return p.MaxIterations > 0 && p.IterationCount >= p.MaxIterations
}

// EffectiveTrigger returns the resolved trigger type.
// When Trigger is empty, TriggerSchedule (the default) is returned.
func (p *PeriodicPrompt) EffectiveTrigger() PeriodicTrigger {
	if p.Trigger == "" {
		return TriggerSchedule
	}
	return p.Trigger
}

// IsOnCompletion returns true when this periodic prompt uses the onCompletion trigger.
func (p *PeriodicPrompt) IsOnCompletion() bool {
	return p.EffectiveTrigger() == TriggerOnCompletion
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
func (p *PeriodicPrompt) PromptPreview() string {
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
func (p *PeriodicPrompt) ReachedMaxDuration(now time.Time) bool {
	if p.MaxDurationSeconds <= 0 || p.FirstRunAt == nil {
		return false
	}
	return now.Sub(*p.FirstRunAt) >= time.Duration(p.MaxDurationSeconds)*time.Second
}

// ClampDelay ensures DelaySeconds is at least floorSeconds.
// Only applies when the trigger is onCompletion; schedule prompts are not clamped.
// The floor value is injected by the caller — this method does NOT hardcode any policy minimum.
func (p *PeriodicPrompt) ClampDelay(floorSeconds int) {
	if !p.IsOnCompletion() {
		return
	}
	if p.DelaySeconds < floorSeconds {
		p.DelaySeconds = floorSeconds
	}
}

// Validate checks if the periodic prompt configuration is valid.
func (p *PeriodicPrompt) Validate() error {
	if p.Prompt == "" && p.PromptName == "" {
		return ErrPromptEmpty
	}
	if p.MaxIterations < 0 {
		return ErrInvalidMaxIterations
	}
	switch p.Trigger {
	case "", TriggerSchedule, TriggerOnCompletion:
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
	// For schedule trigger (default), Frequency must be valid.
	// For onCompletion, frequency is not required.
	if p.EffectiveTrigger() == TriggerSchedule {
		return p.Frequency.Validate()
	}
	return nil
}

// PeriodicStore manages the periodic prompt for a single session.
// It is safe for concurrent use.
type PeriodicStore struct {
	sessionDir string
	mu         sync.RWMutex
}

// NewPeriodicStore creates a new PeriodicStore for the given session directory.
func NewPeriodicStore(sessionDir string) *PeriodicStore {
	return &PeriodicStore{
		sessionDir: sessionDir,
	}
}

// periodicPath returns the path to the periodic.json file.
func (ps *PeriodicStore) periodicPath() string {
	return filepath.Join(ps.sessionDir, periodicFileName)
}

// Get retrieves the current periodic prompt configuration.
// Returns ErrPeriodicNotFound if no periodic prompt is configured.
func (ps *PeriodicStore) Get() (*PeriodicPrompt, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var p PeriodicPrompt
	err := fileutil.ReadJSON(ps.periodicPath(), &p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrPeriodicNotFound
		}
		return nil, fmt.Errorf("failed to read periodic file: %w", err)
	}
	return &p, nil
}

// Set creates or replaces the periodic prompt configuration.
func (ps *PeriodicStore) Set(p *PeriodicPrompt) error {
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

	if err := fileutil.WriteJSONAtomic(ps.periodicPath(), p, 0644); err != nil {
		return fmt.Errorf("failed to write periodic file: %w", err)
	}
	return nil
}

// Update applies a partial update to the periodic prompt.
// Only non-nil fields in the update are applied.
// IterationCount is never modified by Update — it is managed exclusively by RecordSent.
func (ps *PeriodicStore) Update(prompt *string, promptName *string, frequency *Frequency, enabled *bool, freshContext *bool, maxIterations *int, trigger *PeriodicTrigger, delaySeconds *int, maxDurationSeconds *int, arguments *map[string]string) error {
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

	if err := existing.Validate(); err != nil {
		return err
	}

	existing.UpdatedAt = time.Now().UTC()
	existing.NextScheduledAt = ps.computeNextScheduledTime(existing)

	if err := fileutil.WriteJSONAtomic(ps.periodicPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write periodic file: %w", err)
	}
	return nil
}

// Delete removes the periodic prompt configuration.
func (ps *PeriodicStore) Delete() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	err := os.Remove(ps.periodicPath())
	if err != nil {
		if os.IsNotExist(err) {
			return ErrPeriodicNotFound
		}
		return fmt.Errorf("failed to delete periodic file: %w", err)
	}
	return nil
}

// ResetCounters resets the iteration and elapsed-time anchors so the loop starts
// fresh: IterationCount is set to 0, FirstRunAt is cleared (elapsed time = 0), and
// LastSentAt is cleared (never-sent). This is used when restoring a periodic
// conversation that was auto-stopped after reaching its max-iterations or
// max-duration cap. Clearing LastSentAt makes the conversation look brand-new so
// that the restore behaves like the initial run: an onCompletion loop bootstraps
// its first run immediately (no delay_seconds wait — the delay is a between-runs
// gap, not a pre-first-run delay) rather than waiting out the configured delay. It
// does not change Enabled or the prompt configuration; re-enabling is handled
// separately by Update.
func (ps *PeriodicStore) ResetCounters() error {
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

	if err := fileutil.WriteJSONAtomic(ps.periodicPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write periodic file: %w", err)
	}
	return nil
}

// RecordSent updates the last_sent_at timestamp, increments iteration_count, and computes next_scheduled_at.
func (ps *PeriodicStore) RecordSent() error {
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

	if err := fileutil.WriteJSONAtomic(ps.periodicPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write periodic file: %w", err)
	}
	return nil
}

// MarkStopped disables the periodic prompt and records the reason it was stopped.
// It sets Enabled=false, StoppedReason=reason, StoppedAt=now (UTC),
// NextScheduledAt=nil, and UpdatedAt=now.
// Returns ErrPeriodicNotFound if no periodic config exists.
func (ps *PeriodicStore) MarkStopped(reason StoppedReason) error {
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

	if err := fileutil.WriteJSONAtomic(ps.periodicPath(), existing, 0644); err != nil {
		return fmt.Errorf("failed to write periodic file: %w", err)
	}
	return nil
}

// getUnlocked reads the periodic file without locking (caller must hold lock).
func (ps *PeriodicStore) getUnlocked() (*PeriodicPrompt, error) {
	var p PeriodicPrompt
	err := fileutil.ReadJSON(ps.periodicPath(), &p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrPeriodicNotFound
		}
		return nil, fmt.Errorf("failed to read periodic file: %w", err)
	}
	return &p, nil
}

// computeNextScheduledTime calculates when the next prompt should be sent.
// Returns nil for onCompletion triggers — their next run is armed by the event-driven firing path.
func (ps *PeriodicStore) computeNextScheduledTime(p *PeriodicPrompt) *time.Time {
	if !p.Enabled {
		return nil
	}
	// Event-driven triggers do not use a frequency-based schedule.
	if p.IsOnCompletion() {
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
func (ps *PeriodicStore) nextTimeAt(from time.Time, at string, days int) time.Time {
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
