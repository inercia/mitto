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
	err := ps.Update(nil, nil, nil, &enabled, nil, nil, nil, nil, nil)
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
	if err := ps.Update(nil, nil, nil, &disabled, nil, nil, nil, nil, nil); err != nil {
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
	if err := ps.Update(&newPrompt, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ = ps.Get()
	if got.Prompt != "New prompt text" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "New prompt text")
	}

	// Update frequency
	newFreq := Frequency{Value: 30, Unit: FrequencyMinutes}
	if err := ps.Update(nil, nil, &newFreq, nil, nil, nil, nil, nil, nil); err != nil {
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
	err := ps.Update(nil, nil, &invalidFreq, nil, nil, nil, nil, nil, nil)
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
	ps.Update(nil, nil, nil, &enabled, nil, nil, nil, nil, nil)

	got, _ = ps.Get()
	if got.NextScheduledAt == nil {
		t.Error("NextScheduledAt should not be nil when enabled")
	}

	// Disable again
	disabled := false
	ps.Update(nil, nil, nil, &disabled, nil, nil, nil, nil, nil)

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
	if err := ps.Update(&newPrompt, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
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
	if err := ps.Update(nil, nil, nil, nil, nil, nil, &trig, &delay, &maxDur); err != nil {
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
	if err := ps.Update(nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
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
