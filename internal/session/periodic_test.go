package session

import (
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
	err := ps.Update(nil, nil, &enabled)
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
	if err := ps.Update(nil, nil, &disabled); err != nil {
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
	if err := ps.Update(&newPrompt, nil, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ = ps.Get()
	if got.Prompt != "New prompt text" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "New prompt text")
	}

	// Update frequency
	newFreq := Frequency{Value: 30, Unit: FrequencyMinutes}
	if err := ps.Update(nil, &newFreq, nil); err != nil {
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
	err := ps.Update(nil, &invalidFreq, nil)
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
	ps.Update(nil, nil, &enabled)

	got, _ = ps.Get()
	if got.NextScheduledAt == nil {
		t.Error("NextScheduledAt should not be nil when enabled")
	}

	// Disable again
	disabled := false
	ps.Update(nil, nil, &disabled)

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
