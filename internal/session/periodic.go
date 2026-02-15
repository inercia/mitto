// Package session provides session persistence and management for Mitto.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

const (
	periodicFileName = "periodic.json"
)

var (
	// ErrPeriodicNotFound is returned when no periodic prompt is configured.
	ErrPeriodicNotFound = errors.New("periodic prompt not found")
	// ErrInvalidFrequency is returned when the frequency configuration is invalid.
	ErrInvalidFrequency = errors.New("invalid frequency configuration")
	// ErrPromptEmpty is returned when the prompt text is empty.
	ErrPromptEmpty = errors.New("prompt cannot be empty")
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
	// Frequency defines how often the prompt should be sent.
	Frequency Frequency `json:"frequency"`
	// Enabled indicates whether the periodic prompt is active.
	Enabled bool `json:"enabled"`
	// CreatedAt is when the periodic prompt was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the periodic prompt was last modified.
	UpdatedAt time.Time `json:"updated_at"`
	// LastSentAt is when the prompt was last delivered (nil if never sent).
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	// NextScheduledAt is the computed next delivery time (nil if not scheduled).
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
}

// Validate checks if the periodic prompt configuration is valid.
func (p *PeriodicPrompt) Validate() error {
	if p.Prompt == "" {
		return ErrPromptEmpty
	}
	return p.Frequency.Validate()
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
		// Update: preserve created_at
		p.CreatedAt = existing.CreatedAt
		p.LastSentAt = existing.LastSentAt
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
func (ps *PeriodicStore) Update(prompt *string, frequency *Frequency, enabled *bool) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	if prompt != nil {
		existing.Prompt = *prompt
	}
	if frequency != nil {
		existing.Frequency = *frequency
	}
	if enabled != nil {
		existing.Enabled = *enabled
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

// RecordSent updates the last_sent_at timestamp and computes next_scheduled_at.
func (ps *PeriodicStore) RecordSent() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing, err := ps.getUnlocked()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	existing.LastSentAt = &now
	existing.UpdatedAt = now
	existing.NextScheduledAt = ps.computeNextScheduledTime(existing)

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
func (ps *PeriodicStore) computeNextScheduledTime(p *PeriodicPrompt) *time.Time {
	if !p.Enabled {
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
