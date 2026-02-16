package web

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// writeTestPeriodicFile writes a periodic prompt directly to a file for testing.
func writeTestPeriodicFile(path string, p *session.PeriodicPrompt) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func TestPeriodicRunner_StartStop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetPollInterval(100 * time.Millisecond)

	if runner.IsRunning() {
		t.Error("IsRunning() = true before Start()")
	}

	runner.Start()
	if !runner.IsRunning() {
		t.Error("IsRunning() = false after Start()")
	}

	// Start again should be idempotent
	runner.Start()
	if !runner.IsRunning() {
		t.Error("IsRunning() = false after second Start()")
	}

	runner.Stop()
	if runner.IsRunning() {
		t.Error("IsRunning() = true after Stop()")
	}

	// Stop again should be idempotent
	runner.Stop()
	if runner.IsRunning() {
		t.Error("IsRunning() = true after second Stop()")
	}
}

func TestPeriodicRunner_RunOnceNoSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewPeriodicRunner(store, nil, nil)

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || skipped != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, 0, 0)", delivered, skipped, errored)
	}
}

func TestPeriodicRunner_RunOnceNoPeriodicConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session without periodic config
	meta := session.Metadata{
		SessionID:  "test-session-1",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || skipped != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, 0, 0)", delivered, skipped, errored)
	}
}

func TestPeriodicRunner_RunOnceSkipsArchivedSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create an archived session with periodic config
	meta := session.Metadata{
		SessionID:  "archived-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
		Archived:   true,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Add periodic config that would be due
	periodicStore := store.Periodic("archived-session")
	past := time.Now().UTC().Add(-1 * time.Hour)
	p := &session.PeriodicPrompt{
		Prompt:          "Test prompt",
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:         true,
		CreatedAt:       past,
		UpdatedAt:       past,
		NextScheduledAt: &past, // Due in the past
	}
	if err := periodicStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)

	delivered, _, _ := runner.RunOnce()
	// Should not deliver because session is archived
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0 (archived session)", delivered)
	}
}

func TestPeriodicRunner_RunOnceSkipsDisabledConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with disabled periodic config
	meta := session.Metadata{
		SessionID:  "disabled-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	periodicStore := store.Periodic("disabled-session")
	past := time.Now().UTC().Add(-1 * time.Hour)
	p := &session.PeriodicPrompt{
		Prompt:          "Test prompt",
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:         false, // Disabled
		CreatedAt:       past,
		UpdatedAt:       past,
		NextScheduledAt: &past,
	}
	if err := periodicStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)

	delivered, _, _ := runner.RunOnce()
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0 (disabled)", delivered)
	}
}

func TestPeriodicRunner_RunOnceSkipsNotDueYet(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with periodic config not due yet
	meta := session.Metadata{
		SessionID:  "not-due-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	periodicStore := store.Periodic("not-due-session")
	future := time.Now().UTC().Add(1 * time.Hour) // Due in the future
	p := &session.PeriodicPrompt{
		Prompt:          "Test prompt",
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:         true,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		NextScheduledAt: &future,
	}
	if err := periodicStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)

	delivered, _, errored := runner.RunOnce()
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0 (not due yet)", delivered)
	}
	if errored != 0 {
		t.Errorf("errored = %d, want 0", errored)
	}
}

func TestPeriodicRunner_RunOnceAutoResumesInactiveSession(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with due periodic config but no active ACP connection
	meta := session.Metadata{
		SessionID:  "inactive-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Create periodic config - it will compute NextScheduledAt in the future
	// So we need to simulate a prompt that was created but its time has come
	periodicStore := store.Periodic("inactive-session")
	p := &session.PeriodicPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 5, Unit: session.FrequencyMinutes}, // Minimum interval
		Enabled:   true,
	}
	if err := periodicStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Now we need to manually update the periodic file to have a past NextScheduledAt
	// This simulates time passing since the prompt was created
	got, _ := periodicStore.Get()
	past := time.Now().UTC().Add(-1 * time.Hour)
	got.NextScheduledAt = &past
	// Write directly to the file using fileutil
	periodicPath := store.SessionDir("inactive-session") + "/periodic.json"
	if err := writeTestPeriodicFile(periodicPath, got); err != nil {
		t.Fatalf("writeTestPeriodicFile() error = %v", err)
	}

	// Create a session manager with no active sessions and no ACP configured
	// When ResumeSession is called, it will fail because no ACP command is configured
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})

	runner := NewPeriodicRunner(store, sm, nil)

	delivered, skipped, errored := runner.RunOnce()
	// The runner will attempt to resume the session, but it will fail
	// because the session manager has no ACP command configured.
	// This results in an error, not a skip (unlike the old behavior).
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0", delivered)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0 (we attempt to resume, not skip)", skipped)
	}
	if errored != 1 {
		t.Errorf("errored = %d, want 1 (resume fails without ACP config)", errored)
	}
}

func TestPeriodicRunner_NilStore(t *testing.T) {
	runner := NewPeriodicRunner(nil, nil, nil)

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || skipped != 0 || errored != 0 {
		t.Errorf("RunOnce() with nil store = (%d, %d, %d), want (0, 0, 0)", delivered, skipped, errored)
	}
}

func TestTruncatePrompt(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"hello", 3, "..."},
		{"a", 1, "a"},
	}

	for _, tt := range tests {
		got := truncatePrompt(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncatePrompt(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestPeriodicRunner_TriggerNow_NoStore(t *testing.T) {
	runner := NewPeriodicRunner(nil, nil, nil)
	err := runner.TriggerNow("test-session")
	if err != ErrSessionStoreNotAvailable {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrSessionStoreNotAvailable)
	}
}

func TestPeriodicRunner_TriggerNow_SessionNotFound(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewPeriodicRunner(store, nil, nil)
	err = runner.TriggerNow("nonexistent-session")
	if err != session.ErrSessionNotFound {
		t.Errorf("TriggerNow() error = %v, want %v", err, session.ErrSessionNotFound)
	}
}

func TestPeriodicRunner_TriggerNow_NoPeriodicConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session without periodic config
	meta := session.Metadata{
		SessionID:  "test-session-1",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID)
	if err != session.ErrPeriodicNotFound {
		t.Errorf("TriggerNow() error = %v, want %v", err, session.ErrPeriodicNotFound)
	}
}

func TestPeriodicRunner_TriggerNow_NotEnabled(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-2",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	// Create a periodic config with enabled=false
	periodicStore := store.Periodic(meta.SessionID)
	err = periodicStore.Set(&session.PeriodicPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	if err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID)
	if err != ErrPeriodicNotEnabled {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrPeriodicNotEnabled)
	}
}

func TestPeriodicRunner_TriggerNow_NoSessionManager(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-3",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	// Create an enabled periodic config
	periodicStore := store.Periodic(meta.SessionID)
	err = periodicStore.Set(&session.PeriodicPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Runner without session manager
	runner := NewPeriodicRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID)
	if err != ErrSessionManagerNotAvailable {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrSessionManagerNotAvailable)
	}
}
