package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFrequency_Validate(t *testing.T) {
	tests := []struct {
		name    string
		freq    Frequency
		wantErr bool
	}{
		{
			name:    "valid minutes",
			freq:    Frequency{Value: 30, Unit: FrequencyMinutes},
			wantErr: false,
		},
		{
			name:    "valid hours",
			freq:    Frequency{Value: 2, Unit: FrequencyHours},
			wantErr: false,
		},
		{
			name:    "valid days without at",
			freq:    Frequency{Value: 1, Unit: FrequencyDays},
			wantErr: false,
		},
		{
			name:    "valid days with at",
			freq:    Frequency{Value: 1, Unit: FrequencyDays, At: "09:00"},
			wantErr: false,
		},
		{
			name:    "valid days at midnight",
			freq:    Frequency{Value: 1, Unit: FrequencyDays, At: "00:00"},
			wantErr: false,
		},
		{
			name:    "valid days at end of day",
			freq:    Frequency{Value: 1, Unit: FrequencyDays, At: "23:59"},
			wantErr: false,
		},
		{
			name:    "invalid value zero",
			freq:    Frequency{Value: 0, Unit: FrequencyMinutes},
			wantErr: true,
		},
		{
			name:    "invalid value negative",
			freq:    Frequency{Value: -1, Unit: FrequencyMinutes},
			wantErr: true,
		},
		{
			name:    "valid minutes short interval",
			freq:    Frequency{Value: 1, Unit: FrequencyMinutes},
			wantErr: false,
		},
		{
			name:    "invalid minutes at not allowed",
			freq:    Frequency{Value: 30, Unit: FrequencyMinutes, At: "09:00"},
			wantErr: true,
		},
		{
			name:    "invalid hours at not allowed",
			freq:    Frequency{Value: 2, Unit: FrequencyHours, At: "09:00"},
			wantErr: true,
		},
		{
			name:    "invalid at format too short",
			freq:    Frequency{Value: 1, Unit: FrequencyDays, At: "9:00"},
			wantErr: true,
		},
		{
			name:    "invalid at format no colon",
			freq:    Frequency{Value: 1, Unit: FrequencyDays, At: "09-00"},
			wantErr: true,
		},
		{
			name:    "invalid at hour too high",
			freq:    Frequency{Value: 1, Unit: FrequencyDays, At: "24:00"},
			wantErr: true,
		},
		{
			name:    "invalid at minute too high",
			freq:    Frequency{Value: 1, Unit: FrequencyDays, At: "09:60"},
			wantErr: true,
		},
		{
			name:    "invalid unit",
			freq:    Frequency{Value: 1, Unit: "weeks"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.freq.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFrequency_Duration(t *testing.T) {
	tests := []struct {
		name string
		freq Frequency
		want time.Duration
	}{
		{
			name: "30 minutes",
			freq: Frequency{Value: 30, Unit: FrequencyMinutes},
			want: 30 * time.Minute,
		},
		{
			name: "2 hours",
			freq: Frequency{Value: 2, Unit: FrequencyHours},
			want: 2 * time.Hour,
		},
		{
			name: "1 day",
			freq: Frequency{Value: 1, Unit: FrequencyDays},
			want: 24 * time.Hour,
		},
		{
			name: "7 days",
			freq: Frequency{Value: 7, Unit: FrequencyDays},
			want: 7 * 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.freq.Duration()
			if got != tt.want {
				t.Errorf("Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPeriodicPrompt_Validate(t *testing.T) {
	tests := []struct {
		name    string
		prompt  PeriodicPrompt
		wantErr bool
	}{
		{
			name: "valid prompt",
			prompt: PeriodicPrompt{
				Prompt:    "Check for updates",
				Frequency: Frequency{Value: 1, Unit: FrequencyHours},
				Enabled:   true,
			},
			wantErr: false,
		},
		{
			name: "empty prompt",
			prompt: PeriodicPrompt{
				Prompt:    "",
				Frequency: Frequency{Value: 1, Unit: FrequencyHours},
				Enabled:   true,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.prompt.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPeriodicStore_GetNotFound(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	_, err := ps.Get()
	if err != ErrPeriodicNotFound {
		t.Errorf("Get() error = %v, want ErrPeriodicNotFound", err)
	}
}

func TestPeriodicStore_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Check for updates",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}

	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Verify file was created
	periodicPath := filepath.Join(dir, periodicFileName)
	if _, err := os.Stat(periodicPath); os.IsNotExist(err) {
		t.Fatal("periodic.json should exist after Set()")
	}

	got, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.Prompt != p.Prompt {
		t.Errorf("Get().Prompt = %q, want %q", got.Prompt, p.Prompt)
	}
	if got.Frequency.Value != p.Frequency.Value {
		t.Errorf("Get().Frequency.Value = %d, want %d", got.Frequency.Value, p.Frequency.Value)
	}
	if got.Frequency.Unit != p.Frequency.Unit {
		t.Errorf("Get().Frequency.Unit = %q, want %q", got.Frequency.Unit, p.Frequency.Unit)
	}
	if got.Enabled != p.Enabled {
		t.Errorf("Get().Enabled = %v, want %v", got.Enabled, p.Enabled)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Get().CreatedAt should not be zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("Get().UpdatedAt should not be zero")
	}
	if got.NextScheduledAt == nil {
		t.Error("Get().NextScheduledAt should not be nil when enabled")
	}
}

func TestPeriodicStore_SetValidation(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Empty prompt
	err := ps.Set(&PeriodicPrompt{
		Prompt:    "",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	})
	if err == nil {
		t.Error("Set() with empty prompt should return error")
	}

	// Invalid frequency (value must be >= 1)
	err = ps.Set(&PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 0, Unit: FrequencyMinutes}, // Zero not allowed
		Enabled:   true,
	})
	if err == nil {
		t.Error("Set() with invalid frequency should return error")
	}
}

func TestPeriodicStore_SetPreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Create initial
	p1 := &PeriodicPrompt{
		Prompt:    "First prompt",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p1); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got1, _ := ps.Get()
	originalCreatedAt := got1.CreatedAt

	// Wait a tiny bit to ensure different timestamp
	time.Sleep(10 * time.Millisecond)

	// Update with new prompt
	p2 := &PeriodicPrompt{
		Prompt:    "Updated prompt",
		Frequency: Frequency{Value: 2, Unit: FrequencyHours},
		Enabled:   false,
	}
	if err := ps.Set(p2); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got2, _ := ps.Get()

	// CreatedAt should be preserved
	if !got2.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("CreatedAt changed: got %v, want %v", got2.CreatedAt, originalCreatedAt)
	}

	// UpdatedAt should be different
	if got2.UpdatedAt.Equal(got1.UpdatedAt) {
		t.Error("UpdatedAt should have changed")
	}

	// New values should be set
	if got2.Prompt != "Updated prompt" {
		t.Errorf("Prompt = %q, want %q", got2.Prompt, "Updated prompt")
	}
}

func TestPeriodicStore_Update(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Update on non-existent should fail
	enabled := true
	err := ps.Update(nil, nil, nil, &enabled, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != ErrPeriodicNotFound {
		t.Errorf("Update() on empty store error = %v, want ErrPeriodicNotFound", err)
	}

	// Create initial
	p := &PeriodicPrompt{
		Prompt:    "Initial prompt",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Update only enabled field
	disabled := false
	if err := ps.Update(nil, nil, nil, &disabled, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ := ps.Get()
	if got.Enabled != false {
		t.Error("Update() should have disabled the prompt")
	}
	if got.Prompt != "Initial prompt" {
		t.Error("Update() should not have changed prompt")
	}

	// Update only prompt field
	newPrompt := "New prompt text"
	if err := ps.Update(&newPrompt, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ = ps.Get()
	if got.Prompt != "New prompt text" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "New prompt text")
	}

	// Update frequency
	newFreq := Frequency{Value: 30, Unit: FrequencyMinutes}
	if err := ps.Update(nil, nil, &newFreq, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ = ps.Get()
	if got.Frequency.Value != 30 || got.Frequency.Unit != FrequencyMinutes {
		t.Errorf("Frequency = %+v, want 30 minutes", got.Frequency)
	}
}

func TestPeriodicStore_UpdateValidation(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Create initial
	p := &PeriodicPrompt{
		Prompt:    "Initial prompt",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)

	// Update with invalid frequency should fail (value must be >= 1)
	invalidFreq := Frequency{Value: 0, Unit: FrequencyMinutes} // Zero not allowed
	err := ps.Update(nil, nil, &invalidFreq, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil {
		t.Error("Update() with invalid frequency should return error")
	}

	// Original should be unchanged
	got, _ := ps.Get()
	if got.Frequency.Value != 1 || got.Frequency.Unit != FrequencyHours {
		t.Error("Invalid update should not change data")
	}
}

func TestPeriodicStore_Delete(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Delete non-existent should return error
	err := ps.Delete()
	if err != ErrPeriodicNotFound {
		t.Errorf("Delete() on empty store error = %v, want ErrPeriodicNotFound", err)
	}

	// Create and delete
	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)

	periodicPath := filepath.Join(dir, periodicFileName)
	if _, err := os.Stat(periodicPath); os.IsNotExist(err) {
		t.Fatal("periodic.json should exist after Set()")
	}

	if err := ps.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := os.Stat(periodicPath); !os.IsNotExist(err) {
		t.Error("periodic.json should not exist after Delete()")
	}

	// Get should return not found
	_, err = ps.Get()
	if err != ErrPeriodicNotFound {
		t.Errorf("Get() after Delete() error = %v, want ErrPeriodicNotFound", err)
	}
}

func TestPeriodicStore_RecordSent(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// RecordSent on non-existent should fail
	err := ps.RecordSent()
	if err != ErrPeriodicNotFound {
		t.Errorf("RecordSent() on empty store error = %v, want ErrPeriodicNotFound", err)
	}

	// Create
	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)

	got1, _ := ps.Get()
	if got1.LastSentAt != nil {
		t.Error("LastSentAt should be nil initially")
	}

	// Record sent
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}

	got2, _ := ps.Get()
	if got2.LastSentAt == nil {
		t.Error("LastSentAt should not be nil after RecordSent()")
	}
	if got2.NextScheduledAt == nil {
		t.Error("NextScheduledAt should not be nil after RecordSent()")
	}

	// Next scheduled should be after last sent
	if !got2.NextScheduledAt.After(*got2.LastSentAt) {
		t.Error("NextScheduledAt should be after LastSentAt")
	}
}

func TestPeriodicStore_ResetCounters(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// ResetCounters on non-existent should fail
	if err := ps.ResetCounters(); err != ErrPeriodicNotFound {
		t.Errorf("ResetCounters() on empty store error = %v, want ErrPeriodicNotFound", err)
	}

	// Create and run twice so IterationCount and FirstRunAt are populated.
	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}

	before, _ := ps.Get()
	if before.IterationCount != 2 {
		t.Fatalf("IterationCount = %d, want 2 before reset", before.IterationCount)
	}
	if before.FirstRunAt == nil {
		t.Fatal("FirstRunAt should be set before reset")
	}
	if before.LastSentAt == nil {
		t.Fatal("LastSentAt should be set before reset")
	}

	// Reset the counters.
	if err := ps.ResetCounters(); err != nil {
		t.Fatalf("ResetCounters() error = %v", err)
	}

	after, _ := ps.Get()
	if after.IterationCount != 0 {
		t.Errorf("IterationCount = %d, want 0 after reset", after.IterationCount)
	}
	if after.FirstRunAt != nil {
		t.Errorf("FirstRunAt = %v, want nil after reset", after.FirstRunAt)
	}
	// LastSentAt must be cleared so a restored loop looks never-sent and fires its
	// first run immediately (no onCompletion delay).
	if after.LastSentAt != nil {
		t.Errorf("LastSentAt = %v, want nil after reset", after.LastSentAt)
	}
	// ResetCounters must not change the prompt configuration.
	if after.Prompt != p.Prompt {
		t.Errorf("Prompt = %q, want %q (unchanged by reset)", after.Prompt, p.Prompt)
	}
}

func TestPeriodicStore_NextScheduledAtWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Create disabled prompt
	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   false,
	}
	ps.Set(p)

	got, _ := ps.Get()
	if got.NextScheduledAt != nil {
		t.Error("NextScheduledAt should be nil when disabled")
	}

	// Enable it
	enabled := true
	ps.Update(nil, nil, nil, &enabled, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	got, _ = ps.Get()
	if got.NextScheduledAt == nil {
		t.Error("NextScheduledAt should not be nil when enabled")
	}

	// Disable again
	disabled := false
	ps.Update(nil, nil, nil, &disabled, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	got, _ = ps.Get()
	if got.NextScheduledAt != nil {
		t.Error("NextScheduledAt should be nil when disabled again")
	}
}

func TestPeriodicStore_NextScheduledAtWithDaysAndAt(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Create a daily prompt at 09:00 UTC
	p := &PeriodicPrompt{
		Prompt:    "Daily check",
		Frequency: Frequency{Value: 1, Unit: FrequencyDays, At: "09:00"},
		Enabled:   true,
	}
	ps.Set(p)

	got, _ := ps.Get()
	if got.NextScheduledAt == nil {
		t.Fatal("NextScheduledAt should not be nil")
	}

	// The scheduled time should be at 09:00 UTC
	if got.NextScheduledAt.Hour() != 9 || got.NextScheduledAt.Minute() != 0 {
		t.Errorf("NextScheduledAt time = %s, want 09:00", got.NextScheduledAt.Format("15:04"))
	}
}

func TestPeriodicStore_LastSentAtPreservedOnUpdate(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	// Create and record sent
	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)
	ps.RecordSent()

	got1, _ := ps.Get()
	lastSent := got1.LastSentAt

	// Update with Set (should preserve last_sent_at)
	p2 := &PeriodicPrompt{
		Prompt:    "Updated",
		Frequency: Frequency{Value: 2, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p2)

	got2, _ := ps.Get()
	if got2.LastSentAt == nil {
		t.Error("LastSentAt should be preserved after Set()")
	}
	if !got2.LastSentAt.Equal(*lastSent) {
		t.Error("LastSentAt should not change after Set()")
	}
}

func TestPeriodicPrompt_Validate_MaxIterations(t *testing.T) {
	tests := []struct {
		name    string
		prompt  PeriodicPrompt
		wantErr bool
	}{
		{
			name: "zero max iterations (unlimited)",
			prompt: PeriodicPrompt{
				Prompt:        "Test",
				Frequency:     Frequency{Value: 1, Unit: FrequencyHours},
				MaxIterations: 0,
			},
			wantErr: false,
		},
		{
			name: "positive max iterations",
			prompt: PeriodicPrompt{
				Prompt:        "Test",
				Frequency:     Frequency{Value: 1, Unit: FrequencyHours},
				MaxIterations: 5,
			},
			wantErr: false,
		},
		{
			name: "negative max iterations",
			prompt: PeriodicPrompt{
				Prompt:        "Test",
				Frequency:     Frequency{Value: 1, Unit: FrequencyHours},
				MaxIterations: -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.prompt.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPeriodicPrompt_ReachedMaxIterations(t *testing.T) {
	tests := []struct {
		name           string
		maxIterations  int
		iterationCount int
		want           bool
	}{
		{name: "unlimited (zero)", maxIterations: 0, iterationCount: 100, want: false},
		{name: "count less than max", maxIterations: 5, iterationCount: 3, want: false},
		{name: "count equals max", maxIterations: 5, iterationCount: 5, want: true},
		{name: "count exceeds max", maxIterations: 5, iterationCount: 6, want: true},
		{name: "zero count, nonzero max", maxIterations: 3, iterationCount: 0, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PeriodicPrompt{MaxIterations: tt.maxIterations, IterationCount: tt.iterationCount}
			if got := p.ReachedMaxIterations(); got != tt.want {
				t.Errorf("ReachedMaxIterations() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPeriodicStore_RecordSent_IncrementsIterationCount(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, _ := ps.Get()
	if got.IterationCount != 0 {
		t.Errorf("IterationCount initially = %d, want 0", got.IterationCount)
	}

	for i := 1; i <= 3; i++ {
		if err := ps.RecordSent(); err != nil {
			t.Fatalf("RecordSent() #%d error = %v", i, err)
		}
		got, _ = ps.Get()
		if got.IterationCount != i {
			t.Errorf("After RecordSent #%d: IterationCount = %d, want %d", i, got.IterationCount, i)
		}
	}
}

func TestPeriodicStore_IterationCountPreservedOnSet(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)
	ps.RecordSent()
	ps.RecordSent()

	got, _ := ps.Get()
	if got.IterationCount != 2 {
		t.Fatalf("IterationCount = %d, want 2 before Set()", got.IterationCount)
	}

	// Re-save the config (simulates user updating frequency without resetting counter)
	p2 := &PeriodicPrompt{
		Prompt:    "Updated prompt",
		Frequency: Frequency{Value: 2, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p2); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got2, _ := ps.Get()
	if got2.IterationCount != 2 {
		t.Errorf("IterationCount after Set() = %d, want 2 (should be preserved)", got2.IterationCount)
	}
}

func TestPeriodicStore_UpdateDoesNotTouchIterationCount(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)
	ps.RecordSent()

	got, _ := ps.Get()
	if got.IterationCount != 1 {
		t.Fatalf("IterationCount = %d, want 1", got.IterationCount)
	}

	// Update via partial update — should not touch IterationCount
	newPrompt := "Updated"
	if err := ps.Update(&newPrompt, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got2, _ := ps.Get()
	if got2.IterationCount != 1 {
		t.Errorf("IterationCount after Update() = %d, want 1 (should be unchanged)", got2.IterationCount)
	}
}

// --- New tests for trigger type, delay, maxDuration, FirstRunAt ---

func TestPeriodicPrompt_Validate_Trigger(t *testing.T) {
	validFreq := Frequency{Value: 1, Unit: FrequencyHours}
	tests := []struct {
		name    string
		prompt  PeriodicPrompt
		wantErr error
	}{
		{
			name:    "valid schedule trigger explicit",
			prompt:  PeriodicPrompt{Prompt: "p", Frequency: validFreq, Trigger: TriggerSchedule},
			wantErr: nil,
		},
		{
			name:    "valid empty trigger treated as schedule",
			prompt:  PeriodicPrompt{Prompt: "p", Frequency: validFreq, Trigger: ""},
			wantErr: nil,
		},
		{
			name:    "valid onCompletion with no frequency",
			prompt:  PeriodicPrompt{Prompt: "p", Trigger: TriggerOnCompletion},
			wantErr: nil,
		},
		{
			name:    "valid onCompletion with delay",
			prompt:  PeriodicPrompt{Prompt: "p", Trigger: TriggerOnCompletion, DelaySeconds: 10},
			wantErr: nil,
		},
		{
			name:    "valid onTasks with no frequency",
			prompt:  PeriodicPrompt{Prompt: "p", Trigger: TriggerOnTasks},
			wantErr: nil,
		},
		{
			name:    "valid onTasks with empty condition fires on any change",
			prompt:  PeriodicPrompt{Prompt: "p", Trigger: TriggerOnTasks, Condition: ""},
			wantErr: nil,
		},
		{
			name:    "invalid trigger value",
			prompt:  PeriodicPrompt{Prompt: "p", Frequency: validFreq, Trigger: "weekly"},
			wantErr: ErrInvalidTrigger,
		},
		{
			name:    "negative DelaySeconds",
			prompt:  PeriodicPrompt{Prompt: "p", Trigger: TriggerOnCompletion, DelaySeconds: -1},
			wantErr: ErrInvalidDelay,
		},
		{
			name:    "negative MaxDurationSeconds",
			prompt:  PeriodicPrompt{Prompt: "p", Trigger: TriggerOnCompletion, MaxDurationSeconds: -1},
			wantErr: ErrInvalidMaxDuration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.prompt.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}

// TestPeriodicPrompt_Validate_Condition verifies that Condition compile-validation
// is delegated to the injected ConditionValidator seam: nil validator skips the
// check, a passing validator allows the condition, and a failing validator rejects
// it with a wrapped error.
func TestPeriodicPrompt_Validate_Condition(t *testing.T) {
	t.Cleanup(func() { ConditionValidator = nil })

	p := PeriodicPrompt{Prompt: "p", Trigger: TriggerOnTasks, Condition: "tasks.changed()"}

	// No validator wired up: CEL compile-check is skipped, condition is accepted as-is.
	ConditionValidator = nil
	if err := p.Validate(); err != nil {
		t.Errorf("Validate() with nil ConditionValidator error = %v, want nil", err)
	}

	// Validator wired up and accepts the condition.
	ConditionValidator = func(string) error { return nil }
	if err := p.Validate(); err != nil {
		t.Errorf("Validate() with accepting validator error = %v, want nil", err)
	}

	// Validator wired up and rejects the condition.
	wantErr := errors.New("bad CEL syntax")
	ConditionValidator = func(string) error { return wantErr }
	err := p.Validate()
	if err == nil {
		t.Fatal("Validate() with rejecting validator error = nil, want error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Validate() error = %v, want wrapped %v", err, wantErr)
	}

	// Empty Condition is never validated, even with a rejecting validator wired up.
	pEmpty := PeriodicPrompt{Prompt: "p", Trigger: TriggerOnTasks}
	if err := pEmpty.Validate(); err != nil {
		t.Errorf("Validate() with empty condition and rejecting validator error = %v, want nil", err)
	}
}

func TestPeriodicPrompt_ClampDelay(t *testing.T) {
	tests := []struct {
		name      string
		trigger   PeriodicTrigger
		delay     int
		floor     int
		wantDelay int
	}{
		{name: "onCompletion below floor gets clamped", trigger: TriggerOnCompletion, delay: 2, floor: 5, wantDelay: 5},
		{name: "onCompletion at floor unchanged", trigger: TriggerOnCompletion, delay: 5, floor: 5, wantDelay: 5},
		{name: "onCompletion above floor unchanged", trigger: TriggerOnCompletion, delay: 10, floor: 5, wantDelay: 10},
		{name: "schedule trigger not clamped", trigger: TriggerSchedule, delay: 0, floor: 5, wantDelay: 0},
		{name: "empty trigger (schedule) not clamped", trigger: "", delay: 1, floor: 5, wantDelay: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PeriodicPrompt{Trigger: tt.trigger, DelaySeconds: tt.delay}
			p.ClampDelay(tt.floor)
			if p.DelaySeconds != tt.wantDelay {
				t.Errorf("DelaySeconds = %d, want %d", p.DelaySeconds, tt.wantDelay)
			}
		})
	}
}

func TestPeriodicPrompt_ReachedMaxDuration(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-10 * time.Second)
	future := now.Add(10 * time.Second)

	tests := []struct {
		name               string
		maxDurationSeconds int
		firstRunAt         *time.Time
		now                time.Time
		want               bool
	}{
		{name: "zero = unlimited", maxDurationSeconds: 0, firstRunAt: &past, now: now, want: false},
		{name: "firstRunAt nil", maxDurationSeconds: 5, firstRunAt: nil, now: now, want: false},
		{name: "elapsed >= cap", maxDurationSeconds: 5, firstRunAt: &past, now: now, want: true},
		{name: "elapsed < cap", maxDurationSeconds: 30, firstRunAt: &past, now: now, want: false},
		{name: "not yet started", maxDurationSeconds: 5, firstRunAt: &future, now: now, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PeriodicPrompt{MaxDurationSeconds: tt.maxDurationSeconds, FirstRunAt: tt.firstRunAt}
			if got := p.ReachedMaxDuration(tt.now); got != tt.want {
				t.Errorf("ReachedMaxDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPeriodicStore_RecordSent_SetsFirstRunAt(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, _ := ps.Get()
	if got.FirstRunAt != nil {
		t.Error("FirstRunAt should be nil before any RecordSent")
	}

	// First RecordSent sets the anchor.
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() #1 error = %v", err)
	}
	got, _ = ps.Get()
	if got.FirstRunAt == nil {
		t.Fatal("FirstRunAt should be set after first RecordSent")
	}
	firstRunAt := *got.FirstRunAt

	time.Sleep(5 * time.Millisecond)

	// Second RecordSent must NOT change FirstRunAt.
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() #2 error = %v", err)
	}
	got, _ = ps.Get()
	if !got.FirstRunAt.Equal(firstRunAt) {
		t.Errorf("FirstRunAt changed on second RecordSent: got %v, want %v", got.FirstRunAt, firstRunAt)
	}
}

func TestPeriodicStore_FirstRunAtPreservedOnSet(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	ps.Set(p)
	ps.RecordSent()

	got, _ := ps.Get()
	firstRunAt := *got.FirstRunAt

	// Replace config via Set — FirstRunAt must survive.
	p2 := &PeriodicPrompt{
		Prompt:    "Updated",
		Frequency: Frequency{Value: 2, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p2); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got2, _ := ps.Get()
	if got2.FirstRunAt == nil {
		t.Fatal("FirstRunAt should be preserved after Set()")
	}
	if !got2.FirstRunAt.Equal(firstRunAt) {
		t.Errorf("FirstRunAt changed after Set(): got %v, want %v", got2.FirstRunAt, firstRunAt)
	}
}

func TestPeriodicStore_Update_NewFields(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Update trigger, delay, and maxDuration.
	trig := TriggerOnCompletion
	delay := 15
	maxDur := 3600
	if err := ps.Update(nil, nil, nil, nil, nil, nil, &trig, &delay, &maxDur, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ := ps.Get()
	if got.Trigger != TriggerOnCompletion {
		t.Errorf("Trigger = %q, want %q", got.Trigger, TriggerOnCompletion)
	}
	if got.DelaySeconds != 15 {
		t.Errorf("DelaySeconds = %d, want 15", got.DelaySeconds)
	}
	if got.MaxDurationSeconds != 3600 {
		t.Errorf("MaxDurationSeconds = %d, want 3600", got.MaxDurationSeconds)
	}
	// Unrelated fields should be untouched.
	if got.Prompt != "Test" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "Test")
	}

	// Passing nil for new fields should leave them unchanged.
	if err := ps.Update(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() with all-nil error = %v", err)
	}
	got2, _ := ps.Get()
	if got2.Trigger != TriggerOnCompletion {
		t.Errorf("Trigger changed on nil update: got %q", got2.Trigger)
	}
	if got2.DelaySeconds != 15 {
		t.Errorf("DelaySeconds changed on nil update: got %d", got2.DelaySeconds)
	}
}

// TestPeriodicStore_Update_OnTasksFields verifies that Condition, ConditionPreset,
// and CooldownSeconds round-trip through Update/Get, and that a nil update leaves
// them unchanged.
func TestPeriodicStore_Update_OnTasksFields(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:  "Test",
		Trigger: TriggerOnTasks,
		Enabled: true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	cond := "tasks.changed()"
	preset := "any-change"
	cooldown := 120
	if err := ps.Update(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &cond, &preset, &cooldown); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ := ps.Get()
	if got.Condition != cond {
		t.Errorf("Condition = %q, want %q", got.Condition, cond)
	}
	if got.ConditionPreset != preset {
		t.Errorf("ConditionPreset = %q, want %q", got.ConditionPreset, preset)
	}
	if got.CooldownSeconds != cooldown {
		t.Errorf("CooldownSeconds = %d, want %d", got.CooldownSeconds, cooldown)
	}

	// Passing nil for these fields should leave them unchanged.
	if err := ps.Update(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() with all-nil error = %v", err)
	}
	got2, _ := ps.Get()
	if got2.Condition != cond {
		t.Errorf("Condition changed on nil update: got %q", got2.Condition)
	}
	if got2.ConditionPreset != preset {
		t.Errorf("ConditionPreset changed on nil update: got %q", got2.ConditionPreset)
	}
	if got2.CooldownSeconds != cooldown {
		t.Errorf("CooldownSeconds changed on nil update: got %d", got2.CooldownSeconds)
	}
}

func TestPeriodicStore_OnCompletion_NextScheduledAtIsNil(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:  "Test",
		Trigger: TriggerOnCompletion,
		Enabled: true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, _ := ps.Get()
	if got.NextScheduledAt != nil {
		t.Errorf("NextScheduledAt should be nil for onCompletion trigger, got %v", got.NextScheduledAt)
	}
}

// --- MarkStopped tests ---

func TestPeriodicStore_MarkStopped_SetsAllFields(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	if err := ps.Set(&PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	before := time.Now().UTC()
	if err := ps.MarkStopped(StoppedReasonMaxDuration); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}
	after := time.Now().UTC()

	got, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() after MarkStopped error = %v", err)
	}
	if got.Enabled {
		t.Error("Enabled should be false after MarkStopped")
	}
	if got.StoppedReason != StoppedReasonMaxDuration {
		t.Errorf("StoppedReason = %q, want %q", got.StoppedReason, StoppedReasonMaxDuration)
	}
	if got.StoppedAt == nil {
		t.Fatal("StoppedAt should be non-nil after MarkStopped")
	}
	if got.StoppedAt.Before(before) || got.StoppedAt.After(after) {
		t.Errorf("StoppedAt = %v is outside [%v, %v]", got.StoppedAt, before, after)
	}
	if got.NextScheduledAt != nil {
		t.Errorf("NextScheduledAt should be nil after MarkStopped, got %v", got.NextScheduledAt)
	}
}

func TestPeriodicStore_MarkStopped_AllReasons(t *testing.T) {
	reasons := []StoppedReason{
		StoppedReasonMaxDuration,
		StoppedReasonMaxIterations,
		StoppedReasonIterationSafeguard,
		StoppedReasonPromptUnresolved,
		StoppedReasonResumeFailures,
	}

	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			dir := t.TempDir()
			ps := NewPeriodicStore(dir)
			if err := ps.Set(&PeriodicPrompt{
				Prompt:    "Test",
				Frequency: Frequency{Value: 1, Unit: FrequencyHours},
				Enabled:   true,
			}); err != nil {
				t.Fatalf("Set() error = %v", err)
			}
			if err := ps.MarkStopped(reason); err != nil {
				t.Fatalf("MarkStopped(%q) error = %v", reason, err)
			}
			got, err := ps.Get()
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if got.StoppedReason != reason {
				t.Errorf("StoppedReason = %q, want %q", got.StoppedReason, reason)
			}
		})
	}
}

func TestPeriodicStore_MarkStopped_PersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	if err := ps.Set(&PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := ps.MarkStopped(StoppedReasonMaxIterations); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	// Simulate restart: create a fresh PeriodicStore for the same directory.
	ps2 := NewPeriodicStore(dir)
	got, err := ps2.Get()
	if err != nil {
		t.Fatalf("Get() on fresh store error = %v", err)
	}
	if got.StoppedReason != StoppedReasonMaxIterations {
		t.Errorf("StoppedReason after restart = %q, want %q", got.StoppedReason, StoppedReasonMaxIterations)
	}
	if got.StoppedAt == nil {
		t.Error("StoppedAt should be non-nil after restart")
	}
}

func TestPeriodicStore_MarkStopped_NotFound(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	err := ps.MarkStopped(StoppedReasonMaxDuration)
	if !errors.Is(err, ErrPeriodicNotFound) {
		t.Errorf("MarkStopped() on non-existent config error = %v, want ErrPeriodicNotFound", err)
	}
}

func TestPeriodicStore_Update_EnableTrue_ClearsStoppedState(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	if err := ps.Set(&PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := ps.MarkStopped(StoppedReasonMaxDuration); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	// Re-enable via Update — stopped state must be cleared.
	enabled := true
	if err := ps.Update(nil, nil, nil, &enabled, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update(enabled=true) error = %v", err)
	}

	got, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Enabled {
		t.Error("Enabled should be true after re-enable")
	}
	if got.StoppedReason != "" {
		t.Errorf("StoppedReason should be cleared after re-enable, got %q", got.StoppedReason)
	}
	if got.StoppedAt != nil {
		t.Errorf("StoppedAt should be nil after re-enable, got %v", got.StoppedAt)
	}
}

func TestPeriodicStore_Update_EnableFalse_DoesNotClearStoppedState(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	if err := ps.Set(&PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := ps.MarkStopped(StoppedReasonMaxIterations); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	// Update with enabled=false should not clear the stopped state.
	enabled := false
	if err := ps.Update(nil, nil, nil, &enabled, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update(enabled=false) error = %v", err)
	}

	got, _ := ps.Get()
	if got.StoppedReason != StoppedReasonMaxIterations {
		t.Errorf("StoppedReason changed unexpectedly: got %q", got.StoppedReason)
	}
}

// TestPeriodicStore_Set_ArgumentsPersisted verifies that Arguments set on a PeriodicPrompt
// via Set() survive a round-trip through Get().
func TestPeriodicStore_Set_ArgumentsPersisted(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	args := map[string]string{"ISSUE_ID": "mitto-42", "ENV": "prod"}
	if err := ps.Set(&PeriodicPrompt{
		PromptName: "my-prompt",
		Arguments:  args,
		Frequency:  Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got.Arguments) != len(args) {
		t.Fatalf("Arguments len = %d, want %d", len(got.Arguments), len(args))
	}
	for k, v := range args {
		if got.Arguments[k] != v {
			t.Errorf("Arguments[%q] = %q, want %q", k, got.Arguments[k], v)
		}
	}
}

// TestPeriodicStore_Update_ArgumentsPersisted verifies that the arguments field
// is updated via Update() and that nil leaves it unchanged.
func TestPeriodicStore_Update_ArgumentsPersisted(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	if err := ps.Set(&PeriodicPrompt{
		PromptName: "my-prompt",
		Arguments:  map[string]string{"KEY": "initial"},
		Frequency:  Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// nil arguments → no change
	if err := ps.Update(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update(nil args) error = %v", err)
	}
	got, _ := ps.Get()
	if got.Arguments["KEY"] != "initial" {
		t.Errorf("Arguments[KEY] = %q after nil update, want %q", got.Arguments["KEY"], "initial")
	}

	// non-nil arguments → replace
	newArgs := map[string]string{"KEY": "updated", "NEW": "value"}
	if err := ps.Update(nil, nil, nil, nil, nil, nil, nil, nil, nil, &newArgs, nil, nil, nil); err != nil {
		t.Fatalf("Update(newArgs) error = %v", err)
	}
	got, _ = ps.Get()
	if got.Arguments["KEY"] != "updated" {
		t.Errorf("Arguments[KEY] = %q, want %q", got.Arguments["KEY"], "updated")
	}
	if got.Arguments["NEW"] != "value" {
		t.Errorf("Arguments[NEW] = %q, want %q", got.Arguments["NEW"], "value")
	}
}

func TestPeriodicPrompt_PromptPreview(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "empty prompt returns empty",
			prompt: "",
			want:   "",
		},
		{
			name:   "pending placeholder returns empty",
			prompt: "(pending)",
			want:   "",
		},
		{
			name:   "pending placeholder with surrounding whitespace returns empty",
			prompt: "  (pending)  ",
			want:   "",
		},
		{
			name:   "short single-line prompt returned unchanged",
			prompt: "Do some analysis",
			want:   "Do some analysis",
		},
		{
			name:   "multi-line prompt returns first line only",
			prompt: "First line\nSecond line\nThird line",
			want:   "First line",
		},
		{
			name:   "first line with trailing whitespace is trimmed",
			prompt: "First line   \nSecond line",
			want:   "First line",
		},
		{
			name:   "exactly 80 rune prompt returned unchanged",
			prompt: "12345678901234567890123456789012345678901234567890123456789012345678901234567890",
			want:   "12345678901234567890123456789012345678901234567890123456789012345678901234567890",
		},
		{
			name:   "prompt longer than 80 runes is truncated with ellipsis",
			prompt: "123456789012345678901234567890123456789012345678901234567890123456789012345678901",
			want:   "12345678901234567890123456789012345678901234567890123456789012345678901234567890…",
		},
		{
			// 72 Greek runes + 9 ASCII = 81 runes → truncated at 80 with "…"
			name:   "rune-safe truncation on multibyte characters",
			prompt: "αβγδεζηθικλμνξοπρστυφχψωαβγδεζηθικλμνξοπρστυφχψωαβγδεζηθικλμνξοπρστυφχψωABCDEFGHI",
			want:   "αβγδεζηθικλμνξοπρστυφχψωαβγδεζηθικλμνξοπρστυφχψωαβγδεζηθικλμνξοπρστυφχψωABCDEFGH…",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PeriodicPrompt{Prompt: tt.prompt}
			got := p.PromptPreview()
			if got != tt.want {
				t.Errorf("PromptPreview() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- DeferNextSchedule tests ---

func TestPeriodicStore_DeferNextSchedule(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	// Establish a baseline schedule and counters.
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}
	before, _ := ps.Get()
	iterBefore := before.IterationCount
	lastSentBefore := before.LastSentAt

	delay := 5 * time.Minute
	start := time.Now().UTC()
	if err := ps.DeferNextSchedule(delay); err != nil {
		t.Fatalf("DeferNextSchedule() error = %v", err)
	}

	after, _ := ps.Get()
	if after.NextScheduledAt == nil {
		t.Fatal("NextScheduledAt should be set after DeferNextSchedule")
	}
	// NextScheduledAt should be roughly now+delay (allow scheduling slack).
	wantMin := start.Add(delay - time.Second)
	wantMax := start.Add(delay + time.Minute)
	if after.NextScheduledAt.Before(wantMin) || after.NextScheduledAt.After(wantMax) {
		t.Errorf("NextScheduledAt = %v, want within [%v, %v]", after.NextScheduledAt, wantMin, wantMax)
	}
	// Iteration count and last-sent must be untouched (a backoff is not a delivery).
	if after.IterationCount != iterBefore {
		t.Errorf("IterationCount = %d, want unchanged %d", after.IterationCount, iterBefore)
	}
	if lastSentBefore == nil || after.LastSentAt == nil || !after.LastSentAt.Equal(*lastSentBefore) {
		t.Errorf("LastSentAt changed: before=%v after=%v", lastSentBefore, after.LastSentAt)
	}
}

func TestPeriodicStore_DeferNextSchedule_OnCompletionNoop(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:  "Test",
		Trigger: TriggerOnCompletion,
		Enabled: true,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := ps.DeferNextSchedule(5 * time.Minute); err != nil {
		t.Fatalf("DeferNextSchedule() error = %v", err)
	}
	got, _ := ps.Get()
	if got.NextScheduledAt != nil {
		t.Errorf("NextScheduledAt should stay nil for onCompletion trigger, got %v", got.NextScheduledAt)
	}
}

func TestPeriodicStore_DeferNextSchedule_DisabledNoop(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	p := &PeriodicPrompt{
		Prompt:    "Test",
		Frequency: Frequency{Value: 1, Unit: FrequencyHours},
		Enabled:   false,
	}
	if err := ps.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := ps.DeferNextSchedule(5 * time.Minute); err != nil {
		t.Fatalf("DeferNextSchedule() error = %v", err)
	}
	got, _ := ps.Get()
	if got.NextScheduledAt != nil {
		t.Errorf("NextScheduledAt should stay nil for disabled config, got %v", got.NextScheduledAt)
	}
}

func TestPeriodicStore_DeferNextSchedule_NotFound(t *testing.T) {
	dir := t.TempDir()
	ps := NewPeriodicStore(dir)

	if err := ps.DeferNextSchedule(time.Minute); err != ErrPeriodicNotFound {
		t.Errorf("DeferNextSchedule() on empty store error = %v, want ErrPeriodicNotFound", err)
	}
}
