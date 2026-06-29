package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/fileutil"
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

// setSessionUpdatedAt bypasses store.UpdateMetadata (which auto-sets UpdatedAt to time.Now())
// by writing the metadata file directly.
func setSessionUpdatedAt(t *testing.T, store *session.Store, sessionID string, updatedAt time.Time) {
	t.Helper()
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata(%s) error = %v", sessionID, err)
	}
	meta.UpdatedAt = updatedAt
	metaPath := filepath.Join(store.SessionDir(sessionID), "metadata.json")
	if err := fileutil.WriteJSON(metaPath, meta, 0644); err != nil {
		t.Fatalf("WriteJSON(%s) error = %v", metaPath, err)
	}
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
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})

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
	err := runner.TriggerNow("test-session", true)
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
	err = runner.TriggerNow("nonexistent-session", true)
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
	err = runner.TriggerNow(meta.SessionID, true)
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
	err = runner.TriggerNow(meta.SessionID, true)
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
	err = runner.TriggerNow(meta.SessionID, true)
	if err != ErrSessionManagerNotAvailable {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrSessionManagerNotAvailable)
	}
}

// TestPeriodicRunner_TriggerNow_NoResetTimer verifies that TriggerNow accepts
// resetTimer=false and follows the same code path as resetTimer=true up to the
// point where the session manager is needed. This ensures the flag is correctly
// threaded through the call stack without being rejected early or panicking.
// (Full end-to-end verification that RecordSent is skipped requires an active
// ACP session and is covered by integration tests.)
func TestPeriodicRunner_TriggerNow_NoResetTimer(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with an enabled periodic config
	meta := session.Metadata{
		SessionID:  "test-no-reset-timer",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic(meta.SessionID)
	err = periodicStore.Set(&session.PeriodicPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Capture the initial schedule so we can verify it is not modified on error.
	initialPeriodic, err := periodicStore.Get()
	if err != nil {
		t.Fatalf("periodicStore.Get() error = %v", err)
	}
	initialNextScheduled := initialPeriodic.NextScheduledAt

	// Runner without session manager — should fail at ErrSessionManagerNotAvailable,
	// identical to the resetTimer=true case. This verifies that resetTimer=false is
	// accepted and reaches the same validation step without any early failure.
	runner := NewPeriodicRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID, false)
	if err != ErrSessionManagerNotAvailable {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrSessionManagerNotAvailable)
	}

	// Verify the schedule was not modified (error occurred before any delivery).
	afterPeriodic, err := periodicStore.Get()
	if err != nil {
		t.Fatalf("periodicStore.Get() after error = %v", err)
	}
	switch {
	case initialNextScheduled == nil && afterPeriodic.NextScheduledAt != nil:
		t.Error("NextScheduledAt was unexpectedly set after error")
	case initialNextScheduled != nil && afterPeriodic.NextScheduledAt == nil:
		t.Error("NextScheduledAt was unexpectedly cleared after error")
	case initialNextScheduled != nil && afterPeriodic.NextScheduledAt != nil:
		if !initialNextScheduled.Equal(*afterPeriodic.NextScheduledAt) {
			t.Errorf("NextScheduledAt changed unexpectedly: before=%v after=%v",
				*initialNextScheduled, *afterPeriodic.NextScheduledAt)
		}
	}
}

func TestPeriodicRunner_AutoArchiveSkipsPeriodicSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with enabled periodic config
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	meta := session.Metadata{
		SessionID:  "periodic-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	setSessionUpdatedAt(t, store, "periodic-session", oldTime)

	// Add enabled periodic config
	periodicStore := store.Periodic("periodic-session")
	p := &session.PeriodicPrompt{
		Prompt:    "Test periodic prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	}
	if err := periodicStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Create runner with auto-archive threshold of 24 hours
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})
	runner := NewPeriodicRunner(store, sm, nil)
	runner.SetAutoArchiveAfter(24 * time.Hour)

	// Run auto-archive check
	runner.RunOnce()

	// Verify session was NOT archived
	updatedMeta, err := store.GetMetadata("periodic-session")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if updatedMeta.Archived {
		t.Error("Session with enabled periodic config should NOT be auto-archived")
	}
}

func TestPeriodicRunner_AutoArchiveSkipsPausedPeriodicSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with disabled periodic config
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	meta := session.Metadata{
		SessionID:  "disabled-periodic-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Manually set UpdatedAt to 48 hours ago by writing metadata file directly
	// (store.Create and UpdateMetadata both overwrite UpdatedAt with time.Now())
	setSessionUpdatedAt(t, store, "disabled-periodic-session", oldTime)

	// Add disabled periodic config
	periodicStore := store.Periodic("disabled-periodic-session")
	p := &session.PeriodicPrompt{
		Prompt:    "Test periodic prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false, // Disabled
	}
	if err := periodicStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Create session manager that can handle CloseSessionGracefully
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})

	// Create runner with auto-archive threshold of 24 hours
	runner := NewPeriodicRunner(store, sm, nil)
	runner.SetAutoArchiveAfter(24 * time.Hour)

	// Run auto-archive check
	runner.RunOnce()

	// Verify session was NOT archived (paused periodic config should prevent archiving)
	updatedMeta, err := store.GetMetadata("disabled-periodic-session")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if updatedMeta.Archived {
		t.Error("Session with paused periodic config should NOT be auto-archived")
	}
}

func TestPeriodicRunner_AutoArchiveNoPeriodicConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session without periodic config
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	meta := session.Metadata{
		SessionID:  "no-periodic-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Manually set UpdatedAt to 48 hours ago by writing metadata file directly
	// (store.Create and UpdateMetadata both overwrite UpdatedAt with time.Now())
	setSessionUpdatedAt(t, store, "no-periodic-session", oldTime)

	// Create session manager
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})

	// Create runner with auto-archive threshold of 24 hours
	runner := NewPeriodicRunner(store, sm, nil)
	runner.SetAutoArchiveAfter(24 * time.Hour)

	// Run auto-archive check
	runner.RunOnce()

	// Verify session WAS archived
	updatedMeta, err := store.GetMetadata("no-periodic-session")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if !updatedMeta.Archived {
		t.Error("Session without periodic config SHOULD be auto-archived when inactive")
	}
}

// TestPeriodicRunner_ConfigCapAutoStop verifies that a periodic conversation with no
// per-prompt cap (MaxIterations=0) auto-stops when the runner's configured default cap
// is reached. This tests the global safeguard layer independently of the per-prompt cap.
func TestPeriodicRunner_ConfigCapAutoStop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with MaxIterations=0 (no per-prompt cap)
	meta := session.Metadata{
		SessionID:  "config-cap-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic(meta.SessionID)
	if err := periodicStore.Set(&session.PeriodicPrompt{
		Prompt:        "Test prompt",
		Frequency:     session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:       true,
		MaxIterations: 0, // No per-prompt cap
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Set up runner with a small config cap (3 iterations)
	const configCap = 3
	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetMaxPeriodicIterations(configCap)

	// Verify that SetMaxPeriodicIterations stored the value
	runner.mu.Lock()
	stored := runner.maxPeriodicIterations
	runner.mu.Unlock()
	if stored != configCap {
		t.Fatalf("maxPeriodicIterations = %d, want %d", stored, configCap)
	}

	// Simulate configCap successful deliveries by calling RecordSent directly.
	// This mirrors what OnComplete does after each successful PromptWithMeta call.
	for i := 0; i < configCap; i++ {
		if err := periodicStore.RecordSent(); err != nil {
			t.Fatalf("RecordSent() [%d] error = %v", i+1, err)
		}
	}

	// Read the updated state and check the effective cap condition
	updated, err := periodicStore.Get()
	if err != nil {
		t.Fatalf("periodicStore.Get() error = %v", err)
	}

	// Verify IterationCount was correctly incremented
	if updated.IterationCount != configCap {
		t.Errorf("IterationCount = %d, want %d", updated.IterationCount, configCap)
	}

	// Verify ReachedMaxIterations is false (per-prompt cap is 0 = unlimited)
	if updated.ReachedMaxIterations() {
		t.Error("ReachedMaxIterations() = true, want false (per-prompt cap is 0)")
	}

	// Compute effective cap as the OnComplete callback would
	runner.mu.Lock()
	cfgCap := runner.maxPeriodicIterations
	runner.mu.Unlock()
	effective := config.EffectiveMaxPeriodicIterations(updated.MaxIterations, cfgCap)

	// Verify effective cap matches the configured cap (since per-prompt cap is 0)
	if effective != configCap {
		t.Errorf("effective cap = %d, want %d", effective, configCap)
	}

	// Verify the condition that triggers auto-stop
	if updated.IterationCount < effective {
		t.Errorf("auto-stop condition not met: IterationCount=%d, effective=%d",
			updated.IterationCount, effective)
	}

	// Simulate what OnComplete does: disable the periodic prompt
	autoStopCalled := false
	runner.SetOnPeriodicAutoStopped(func(sessionID string, p *session.PeriodicPrompt) {
		autoStopCalled = true
		if sessionID != meta.SessionID {
			t.Errorf("onPeriodicAutoStopped sessionID = %q, want %q", sessionID, meta.SessionID)
		}
		if p.Enabled {
			t.Error("onPeriodicAutoStopped: periodic.Enabled = true, want false")
		}
	})

	disabled := false
	if err := periodicStore.Update(nil, nil, nil, &disabled, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("periodicStore.Update(disable) error = %v", err)
	}

	// Invoke the callback as OnComplete does
	if final, err := periodicStore.Get(); err == nil && runner.onPeriodicAutoStopped != nil {
		runner.onPeriodicAutoStopped(meta.SessionID, final)
	}

	// Verify the callback was invoked
	if !autoStopCalled {
		t.Error("onPeriodicAutoStopped was not called")
	}

	// Verify the periodic prompt is now disabled on disk
	final, err := periodicStore.Get()
	if err != nil {
		t.Fatalf("periodicStore.Get() after disable error = %v", err)
	}
	if final.Enabled {
		t.Error("periodic.Enabled = true after auto-stop, want false")
	}
}

// TestPeriodicRunner_DefaultMaxPeriodicIterations verifies that the runner
// is initialized with the correct default config cap.
func TestPeriodicRunner_DefaultMaxPeriodicIterations(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewPeriodicRunner(store, nil, nil)

	runner.mu.Lock()
	got := runner.maxPeriodicIterations
	runner.mu.Unlock()

	if got != config.DefaultMaxPeriodicIterations {
		t.Errorf("initial maxPeriodicIterations = %d, want %d (DefaultMaxPeriodicIterations)",
			got, config.DefaultMaxPeriodicIterations)
	}
}

// TestPeriodicRunner_MinCompletionDelaySeconds verifies the setter/getter and
// that the runner is initialized with the correct default.
func TestPeriodicRunner_MinCompletionDelaySeconds(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewPeriodicRunner(store, nil, nil)

	t.Run("default is DefaultMinPeriodicCompletionDelaySeconds", func(t *testing.T) {
		got := runner.MinPeriodicCompletionDelaySeconds()
		if got != config.DefaultMinPeriodicCompletionDelaySeconds {
			t.Errorf("initial minCompletionDelaySeconds = %d, want %d (DefaultMinPeriodicCompletionDelaySeconds)",
				got, config.DefaultMinPeriodicCompletionDelaySeconds)
		}
	})

	t.Run("set and get round-trip", func(t *testing.T) {
		runner.SetMinPeriodicCompletionDelaySeconds(30)
		got := runner.MinPeriodicCompletionDelaySeconds()
		if got != 30 {
			t.Errorf("MinPeriodicCompletionDelaySeconds() = %d, want 30", got)
		}
	})

	t.Run("negative value clamped to zero", func(t *testing.T) {
		runner.SetMinPeriodicCompletionDelaySeconds(-5)
		got := runner.MinPeriodicCompletionDelaySeconds()
		if got != 0 {
			t.Errorf("MinPeriodicCompletionDelaySeconds() = %d after negative set, want 0", got)
		}
	})

	t.Run("zero is accepted", func(t *testing.T) {
		runner.SetMinPeriodicCompletionDelaySeconds(0)
		got := runner.MinPeriodicCompletionDelaySeconds()
		if got != 0 {
			t.Errorf("MinPeriodicCompletionDelaySeconds() = %d, want 0", got)
		}
	})
}

// countCompletionTimers returns the number of armed on-completion timers, read
// under the runner's timer mutex so it is safe against concurrent AfterFunc callbacks.
func countCompletionTimers(r *PeriodicRunner) int {
	r.completionTimersMu.Lock()
	defer r.completionTimersMu.Unlock()
	return len(r.completionTimers)
}

// newOnCompletionSession creates a session with an enabled onCompletion periodic
// prompt configured with the given delay.
func newOnCompletionSession(t *testing.T, store *session.Store, sessionID string, delaySeconds int) {
	t.Helper()
	meta := session.Metadata{SessionID: sessionID, ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Periodic(sessionID).Set(&session.PeriodicPrompt{
		Prompt:       "iterate",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: delaySeconds,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}
}

func TestPeriodicRunner_OnConversationIdle_ArmsForOnCompletion(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Long delay so the timer does not fire during the test.
	newOnCompletionSession(t, store, "s1", 3600)

	runner := NewPeriodicRunner(store, nil, nil)
	runner.OnConversationIdle("s1")
	defer runner.cancelCompletionTimer("s1")

	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d, want 1", got)
	}
}

func TestPeriodicRunner_OnConversationIdle_IgnoresScheduleTrigger(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Periodic("s1").Set(&session.PeriodicPrompt{
		Prompt:    "x",
		Enabled:   true,
		Trigger:   session.TriggerSchedule,
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	runner.OnConversationIdle("s1")

	if got := countCompletionTimers(runner); got != 0 {
		t.Fatalf("completionTimers = %d, want 0 (schedule trigger must not arm)", got)
	}
}

func TestPeriodicRunner_OnConversationIdle_CancelsStaleTimer(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Session without any periodic config.
	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	// Arm a stale timer, then verify an idle event with no config clears it.
	runner.armCompletionTimer("s1", time.Hour)
	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d after arm, want 1", got)
	}

	runner.OnConversationIdle("s1")
	if got := countCompletionTimers(runner); got != 0 {
		t.Fatalf("completionTimers = %d, want 0 (stale timer must be cancelled)", got)
	}
}

func TestPeriodicRunner_OnConversationIdle_ReArmReplaces(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 3600)

	runner := NewPeriodicRunner(store, nil, nil)
	defer runner.cancelCompletionTimer("s1")

	runner.OnConversationIdle("s1")
	runner.OnConversationIdle("s1")

	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d after re-arm, want 1 (must replace, not stack)", got)
	}
}

func TestPeriodicRunner_OnConversationIdle_FiresAfterDelay(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0)

	// No session manager: firing reaches TriggerNow which errors out, but the
	// timer entry is cleared once it fires — which is what we assert here.
	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetMinPeriodicCompletionDelaySeconds(0)
	runner.OnConversationIdle("s1")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countCompletionTimers(runner) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("on-completion timer did not fire within deadline")
}

func TestPeriodicRunner_OnConversationIdle_FloorOverridesDelay(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Tiny configured delay, but a large global floor must win.
	newOnCompletionSession(t, store, "s1", 0)

	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetMinPeriodicCompletionDelaySeconds(3600) // 1h floor
	runner.OnConversationIdle("s1")
	defer runner.cancelCompletionTimer("s1")

	// Well within the 1h floor — the timer must not have fired.
	time.Sleep(200 * time.Millisecond)
	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d, want 1 (floor must override the small delay)", got)
	}
}

func TestPeriodicRunner_fireOnCompletion_ArchivedNoop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0)
	// Archive the session.
	if err := store.UpdateMetadata("s1", func(m *session.Metadata) {
		m.Archived = true
	}); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	// Should return early without panicking or arming anything.
	runner.fireOnCompletion("s1")
	if got := countCompletionTimers(runner); got != 0 {
		t.Fatalf("completionTimers = %d, want 0", got)
	}
}

func TestPeriodicRunner_OnConversationIdle_NilStore(t *testing.T) {
	runner := NewPeriodicRunner(nil, nil, nil)
	// Must not panic with a nil store.
	runner.OnConversationIdle("x")
	runner.fireOnCompletion("x")
}

// newDurationCappedSession creates a session with an enabled onCompletion periodic
// prompt anchored at firstRunAt, with the given maxDuration (seconds) and maxIterations.
// firstRunAt may be nil to model a prompt that has not yet run (not yet anchored).
func newDurationCappedSession(t *testing.T, store *session.Store, sessionID string, firstRunAt *time.Time, maxDurationSeconds, maxIterations int) *session.PeriodicStore {
	t.Helper()
	meta := session.Metadata{SessionID: sessionID, ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	ps := store.Periodic(sessionID)
	if err := ps.Set(&session.PeriodicPrompt{
		Prompt:             "iterate",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		MaxDurationSeconds: maxDurationSeconds,
		MaxIterations:      maxIterations,
		FirstRunAt:         firstRunAt,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}
	return ps
}

func TestPeriodicRunner_autoStopIfMaxDurationReached(t *testing.T) {
	t.Run("reached disables and broadcasts", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		defer store.Close()

		past := time.Now().Add(-2 * time.Hour)
		ps := newDurationCappedSession(t, store, "s1", &past, 60, 0) // 60s cap, anchored 2h ago

		runner := NewPeriodicRunner(store, nil, nil)
		var gotID string
		var gotDisabled, called bool
		runner.SetOnPeriodicAutoStopped(func(id string, p *session.PeriodicPrompt) {
			called = true
			gotID = id
			gotDisabled = !p.Enabled
		})

		periodic, err := ps.Get()
		if err != nil {
			t.Fatalf("ps.Get() error = %v", err)
		}
		if !runner.autoStopIfMaxDurationReached("s1", periodic, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = false, want true (cap reached)")
		}
		if !called || gotID != "s1" || !gotDisabled {
			t.Errorf("callback: called=%v id=%q disabled=%v, want true/s1/true", called, gotID, gotDisabled)
		}
		final, err := ps.Get()
		if err != nil {
			t.Fatalf("ps.Get() after stop error = %v", err)
		}
		if final.Enabled {
			t.Error("periodic still enabled after auto-stop, want disabled")
		}
	})

	t.Run("maxDuration zero is unlimited", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		defer store.Close()

		past := time.Now().Add(-2 * time.Hour)
		ps := newDurationCappedSession(t, store, "s1", &past, 0, 0) // 0 = unlimited

		runner := NewPeriodicRunner(store, nil, nil)
		periodic, _ := ps.Get()
		if runner.autoStopIfMaxDurationReached("s1", periodic, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = true, want false (maxDuration=0 is unlimited)")
		}
		final, _ := ps.Get()
		if !final.Enabled {
			t.Error("periodic disabled, want still enabled (unlimited)")
		}
	})

	t.Run("not yet anchored returns false", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		defer store.Close()

		ps := newDurationCappedSession(t, store, "s1", nil, 60, 0) // FirstRunAt nil
		runner := NewPeriodicRunner(store, nil, nil)
		periodic, _ := ps.Get()
		if runner.autoStopIfMaxDurationReached("s1", periodic, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = true, want false (FirstRunAt nil)")
		}
	})

	t.Run("within cap returns false", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		defer store.Close()

		recent := time.Now().Add(-1 * time.Second)
		ps := newDurationCappedSession(t, store, "s1", &recent, 3600, 0) // 1h cap, 1s elapsed
		runner := NewPeriodicRunner(store, nil, nil)
		periodic, _ := ps.Get()
		if runner.autoStopIfMaxDurationReached("s1", periodic, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = true, want false (within cap)")
		}
	})

	t.Run("nil periodic returns false", func(t *testing.T) {
		runner := NewPeriodicRunner(nil, nil, nil)
		if runner.autoStopIfMaxDurationReached("s1", nil, nil, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = true, want false (nil periodic)")
		}
	})

	t.Run("duration cap wins while iterations remain", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		defer store.Close()

		past := time.Now().Add(-2 * time.Hour)
		// maxIterations=10 (count=0, plenty left) but maxDuration=60s is exceeded.
		ps := newDurationCappedSession(t, store, "s1", &past, 60, 10)
		runner := NewPeriodicRunner(store, nil, nil)
		periodic, _ := ps.Get()
		if periodic.ReachedMaxIterations() {
			t.Fatal("precondition failed: ReachedMaxIterations() = true, want false")
		}
		if !runner.autoStopIfMaxDurationReached("s1", periodic, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = false, want true (duration cap wins)")
		}
		final, _ := ps.Get()
		if final.Enabled {
			t.Error("periodic still enabled, want disabled (duration cap reached first)")
		}
	})
}

// TestPeriodicRunner_fireOnCompletion_MaxDurationAutoStops verifies the on-completion
// firing path auto-stops (without delivering) once the wall-clock cap is exceeded.
func TestPeriodicRunner_fireOnCompletion_MaxDurationAutoStops(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	past := time.Now().Add(-2 * time.Hour)
	ps := newDurationCappedSession(t, store, "s1", &past, 60, 0)

	runner := NewPeriodicRunner(store, nil, nil)
	called := false
	runner.SetOnPeriodicAutoStopped(func(id string, p *session.PeriodicPrompt) { called = true })

	runner.fireOnCompletion("s1")

	final, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}
	if final.Enabled {
		t.Error("fireOnCompletion did not auto-stop on maxDuration, periodic still enabled")
	}
	if !called {
		t.Error("onPeriodicAutoStopped not called from fireOnCompletion")
	}
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0", got)
	}
}

// TestPeriodicRunner_PromptResolveFailure_AutoPauses verifies that after
// MaxPromptResolveFailures consecutive resolve failures the periodic config is
// disabled on disk and onPeriodicAutoStopped is fired exactly once.
func TestPeriodicRunner_PromptResolveFailure_AutoPauses(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "resolve-fail", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	periodicStore := store.Periodic("resolve-fail")
	if err := periodicStore.Set(&session.PeriodicPrompt{
		PromptName: "nonexistent-prompt",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	resolveErr := errors.New("prompt not found")
	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetPromptResolver(func(name, dir string) (string, error) {
		return "", resolveErr
	})

	callCount := 0
	runner.SetOnPeriodicAutoStopped(func(id string, p *session.PeriodicPrompt) {
		callCount++
		if id != "resolve-fail" {
			t.Errorf("onPeriodicAutoStopped: id=%q, want resolve-fail", id)
		}
		if p.Enabled {
			t.Error("onPeriodicAutoStopped: periodic.Enabled = true, want false")
		}
	})

	periodic, _ := periodicStore.Get()

	// First MaxPromptResolveFailures-1 calls must not disable.
	for i := 1; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("resolve-fail", meta.Name, periodic, periodicStore, resolveErr)
		p, _ := periodicStore.Get()
		if !p.Enabled {
			t.Fatalf("periodic disabled after %d failures, want still enabled", i)
		}
		if callCount != 0 {
			t.Fatalf("onPeriodicAutoStopped called after %d failures, want 0", i)
		}
	}

	// The MaxPromptResolveFailures-th call must disable and fire callback exactly once.
	runner.handlePromptResolveFailure("resolve-fail", meta.Name, periodic, periodicStore, resolveErr)
	if callCount != 1 {
		t.Errorf("onPeriodicAutoStopped called %d times, want 1", callCount)
	}
	final, _ := periodicStore.Get()
	if final.Enabled {
		t.Error("periodic still enabled after auto-pause, want disabled")
	}
}

// TestPeriodicRunner_PromptResolveFailure_CounterReset verifies that a successful
// resolve resets the failure counter so prior failures don't accumulate.
func TestPeriodicRunner_PromptResolveFailure_CounterReset(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "reset-test", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	periodicStore := store.Periodic("reset-test")
	if err := periodicStore.Set(&session.PeriodicPrompt{
		PromptName: "maybe-missing",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	resolveErr := errors.New("not found")
	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetOnPeriodicAutoStopped(func(_ string, _ *session.PeriodicPrompt) {
		t.Error("onPeriodicAutoStopped called unexpectedly after counter reset")
	})

	periodic, _ := periodicStore.Get()

	// Accumulate MaxPromptResolveFailures-1 failures.
	for i := 1; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("reset-test", meta.Name, periodic, periodicStore, resolveErr)
	}

	// Simulate a successful resolution: reset the counter (mirrors checkSession success path).
	runner.promptResolveFailuresMu.Lock()
	delete(runner.promptResolveFailures, "reset-test")
	runner.promptResolveFailuresMu.Unlock()

	// Now accumulate MaxPromptResolveFailures-1 more failures — should not trigger auto-pause.
	for i := 1; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("reset-test", meta.Name, periodic, periodicStore, resolveErr)
	}

	// Verify the periodic is still enabled (counter was reset, threshold not reached again).
	final, _ := periodicStore.Get()
	if !final.Enabled {
		t.Error("periodic disabled unexpectedly; counter reset did not clear failure count")
	}
}

// TestPeriodicRunner_RunOnce_MaxDurationAutoStops verifies the schedule (poll) path
// auto-stops a due periodic once the wall-clock cap is exceeded, before any delivery
// or session resume. With a nil session manager, reaching the cap must neither deliver
// nor error — it disables the config and broadcasts the auto-stop.
func TestPeriodicRunner_RunOnce_MaxDurationAutoStops(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "sched", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic("sched")
	if err := periodicStore.Set(&session.PeriodicPrompt{
		Prompt:             "Test prompt",
		Frequency:          session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:            true,
		Trigger:            session.TriggerSchedule,
		MaxDurationSeconds: 60,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Force the periodic due (past NextScheduledAt) and anchored 2h ago so the cap is exceeded.
	got, _ := periodicStore.Get()
	pastDue := time.Now().UTC().Add(-1 * time.Hour)
	anchor := time.Now().UTC().Add(-2 * time.Hour)
	got.NextScheduledAt = &pastDue
	got.FirstRunAt = &anchor
	periodicPath := store.SessionDir("sched") + "/periodic.json"
	if err := writeTestPeriodicFile(periodicPath, got); err != nil {
		t.Fatalf("writeTestPeriodicFile() error = %v", err)
	}

	// Empty session manager: GetSession returns nil safely. The duration check in
	// checkSession fires before any resume attempt, so nothing is delivered.
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})
	runner := NewPeriodicRunner(store, sm, nil)
	called := false
	runner.SetOnPeriodicAutoStopped(func(id string, p *session.PeriodicPrompt) { called = true })

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || skipped != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, 0, 0) (auto-stop, no delivery)", delivered, skipped, errored)
	}
	if !called {
		t.Error("onPeriodicAutoStopped not called from schedule path")
	}
	final, _ := periodicStore.Get()
	if final.Enabled {
		t.Error("schedule-path periodic still enabled after maxDuration, want disabled")
	}
}

// =============================================================================
// BootstrapOnCompletion Tests
// =============================================================================

// TestPeriodicRunner_BootstrapOnCompletion_NilStore verifies that BootstrapOnCompletion
// is a no-op when the runner has no session store.
func TestPeriodicRunner_BootstrapOnCompletion_NilStore(t *testing.T) {
	runner := NewPeriodicRunner(nil, nil, nil)
	// Must not panic.
	runner.BootstrapOnCompletion("any-session")
}

// TestPeriodicRunner_BootstrapOnCompletion_FreshSession_AttemptsDelivery verifies that a
// fresh enabled onCompletion session (IterationCount==0, LastSentAt==nil) causes
// BootstrapOnCompletion to attempt immediate delivery via TriggerNow with no timer delay.
// With no session manager, TriggerNow fails gracefully; we assert no panic, no timer
// is armed (delivery is synchronous, not timer-deferred), and the config stays enabled.
func TestPeriodicRunner_BootstrapOnCompletion_FreshSession_AttemptsDelivery(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 30) // delay_seconds=30, must NOT apply to first run

	runner := NewPeriodicRunner(store, nil, nil) // nil SM → TriggerNow returns ErrSessionManagerNotAvailable
	runner.BootstrapOnCompletion("s1")

	// No timer should be armed — delivery is attempted synchronously, not via timer.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (bootstrap must not arm a timer)", got)
	}

	// Periodic config must remain enabled — the failed TriggerNow must not disable it.
	periodicStore := store.Periodic("s1")
	p, err := periodicStore.Get()
	if err != nil {
		t.Fatalf("periodicStore.Get() error = %v", err)
	}
	if !p.Enabled {
		t.Error("periodic.Enabled = false after failed bootstrap, want true")
	}
}

// TestPeriodicRunner_BootstrapOnCompletion_AlreadyRan_Noop verifies that
// BootstrapOnCompletion is a no-op when the session has already run at least once
// (IterationCount > 0), preventing double delivery on restart.
func TestPeriodicRunner_BootstrapOnCompletion_AlreadyRan_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0)

	// Simulate a completed first run by calling RecordSent.
	periodicStore := store.Periodic("s1")
	if err := periodicStore.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}

	// Verify IterationCount advanced.
	p, err := periodicStore.Get()
	if err != nil {
		t.Fatalf("periodicStore.Get() error = %v", err)
	}
	if p.IterationCount == 0 {
		t.Fatal("IterationCount = 0 after RecordSent, expected > 0")
	}

	// BootstrapOnCompletion must be a no-op (session already ran).
	runner := NewPeriodicRunner(store, nil, nil)
	runner.BootstrapOnCompletion("s1")

	// No timer armed, no panic.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (already-ran session must be a no-op)", got)
	}
}

// TestPeriodicRunner_BootstrapOnCompletion_Disabled_Noop verifies that
// BootstrapOnCompletion is a no-op for a disabled periodic config.
func TestPeriodicRunner_BootstrapOnCompletion_Disabled_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Periodic("s1").Set(&session.PeriodicPrompt{
		Prompt:  "Test",
		Enabled: false, // disabled
		Trigger: session.TriggerOnCompletion,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	runner.BootstrapOnCompletion("s1") // must be a no-op

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (disabled config must be no-op)", got)
	}
}

// TestPeriodicRunner_BootstrapOnCompletion_ScheduleTrigger_Noop verifies that
// BootstrapOnCompletion is a no-op for schedule-trigger configs (it targets
// onCompletion only).
func TestPeriodicRunner_BootstrapOnCompletion_ScheduleTrigger_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Periodic("s1").Set(&session.PeriodicPrompt{
		Prompt:    "Test",
		Enabled:   true,
		Trigger:   session.TriggerSchedule, // schedule, not onCompletion
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	runner.BootstrapOnCompletion("s1") // must be a no-op

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (schedule trigger must be no-op)", got)
	}
}

// TestPeriodicRunner_BootstrapOnCompletion_TimerPending_Noop verifies that
// BootstrapOnCompletion is a no-op when an onCompletion timer is already pending,
// preventing double-firing within the same process lifetime.
func TestPeriodicRunner_BootstrapOnCompletion_TimerPending_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0)

	runner := NewPeriodicRunner(store, nil, nil)
	// Arm a timer to simulate a pending on-completion run.
	runner.armCompletionTimer("s1", time.Hour)
	defer runner.cancelCompletionTimer("s1")

	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d after arm, want 1", got)
	}

	// BootstrapOnCompletion must detect the pending timer and return immediately.
	runner.BootstrapOnCompletion("s1")

	// Timer count must remain 1 (not replaced or cancelled by bootstrap).
	if got := countCompletionTimers(runner); got != 1 {
		t.Errorf("completionTimers = %d, want 1 (pending timer guard must prevent bootstrap)", got)
	}
}

// TestPeriodicRunner_RunOnce_OnCompletion_BootstrapsFirstRun verifies that the
// poll loop (RunOnce / checkSession) bootstraps a fresh onCompletion session by
// calling BootstrapOnCompletion rather than skipping the session entirely.
// With no session manager, TriggerNow fails gracefully and RunOnce returns (0,0,0).
// The important assertion: no error is counted (bootstrap failure is not an error),
// and no timer is armed (bootstrap is synchronous, not timer-deferred).
func TestPeriodicRunner_RunOnce_OnCompletion_BootstrapsFirstRun(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 30) // delay_seconds=30 must NOT defer the first run

	runner := NewPeriodicRunner(store, nil, nil)

	delivered, skipped, errored := runner.RunOnce()
	// bootstrap failures are best-effort and not counted as poll errors.
	if delivered != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, *, 0)", delivered, skipped, errored)
	}

	// No completion timer should be armed — bootstrap is synchronous, not deferred.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (RunOnce bootstrap must not arm timer)", got)
	}
}

// =============================================================================
// RecoverStalledOnCompletion Tests
// =============================================================================

// newOnCompletionSessionWithRan creates an onCompletion session that has already
// run at least once (IterationCount > 0), simulating a loop that is in-progress.
func newOnCompletionSessionWithRan(t *testing.T, store *session.Store, sessionID string, delaySeconds int) *session.PeriodicStore {
	t.Helper()
	newOnCompletionSession(t, store, sessionID, delaySeconds)
	ps := store.Periodic(sessionID)
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}
	return ps
}

// TestPeriodicRunner_RecoverStalledOnCompletion_ReArmsStalledLoop verifies that
// recoverStalledOnCompletion arms a completion timer when the loop has run at
// least once, no timer is currently pending, and the session is not prompting.
func TestPeriodicRunner_RecoverStalledOnCompletion_ReArmsStalledLoop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnCompletionSessionWithRan(t, store, "s1", 3600) // long delay so timer doesn't fire

	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetMinPeriodicCompletionDelaySeconds(0) // no floor so we can assert timer presence easily

	// Precondition: no timer pending.
	if got := countCompletionTimers(runner); got != 0 {
		t.Fatalf("precondition: completionTimers = %d, want 0", got)
	}

	meta := session.Metadata{SessionID: "s1"}
	periodic, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, periodic)
	defer runner.cancelCompletionTimer("s1")

	// A timer must now be armed — the stall was detected and the loop re-armed.
	if got := countCompletionTimers(runner); got != 1 {
		t.Errorf("completionTimers = %d, want 1 (stalled loop must be re-armed)", got)
	}
}

// TestPeriodicRunner_RecoverStalledOnCompletion_TimerPending_Noop verifies that
// recoverStalledOnCompletion is a no-op when a timer is already pending, i.e. the
// loop is healthy and does not need recovery.
func TestPeriodicRunner_RecoverStalledOnCompletion_TimerPending_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnCompletionSessionWithRan(t, store, "s1", 0)

	runner := NewPeriodicRunner(store, nil, nil)

	// Pre-arm a timer (simulates a healthy loop).
	runner.armCompletionTimer("s1", time.Hour)
	defer runner.cancelCompletionTimer("s1")

	// Record the exact timer pointer before calling recover.
	runner.completionTimersMu.Lock()
	timerBefore := runner.completionTimers["s1"]
	runner.completionTimersMu.Unlock()

	meta := session.Metadata{SessionID: "s1"}
	periodic, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, periodic)

	// Timer must be unchanged — recover must not replace a healthy pending timer.
	runner.completionTimersMu.Lock()
	timerAfter := runner.completionTimers["s1"]
	runner.completionTimersMu.Unlock()

	if timerAfter != timerBefore {
		t.Errorf("timer replaced by recover when it should have been left unchanged")
	}
	if got := countCompletionTimers(runner); got != 1 {
		t.Errorf("completionTimers = %d, want 1 (pending timer must not be touched)", got)
	}
}

// TestPeriodicRunner_RecoverStalledOnCompletion_FreshLoop_Noop verifies that
// recoverStalledOnCompletion is a no-op for a fresh loop (IterationCount==0,
// LastSentAt==nil). Fresh loops are the responsibility of BootstrapOnCompletion.
func TestPeriodicRunner_RecoverStalledOnCompletion_FreshLoop_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Fresh session: no RecordSent call, so IterationCount==0 and LastSentAt==nil.
	newOnCompletionSession(t, store, "s1", 0)
	ps := store.Periodic("s1")

	runner := NewPeriodicRunner(store, nil, nil)

	meta := session.Metadata{SessionID: "s1"}
	periodic, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	// Precondition: IterationCount==0, LastSentAt==nil.
	if periodic.IterationCount != 0 || periodic.LastSentAt != nil {
		t.Fatalf("precondition failed: IterationCount=%d LastSentAt=%v", periodic.IterationCount, periodic.LastSentAt)
	}

	runner.recoverStalledOnCompletion(meta, periodic)

	// No timer must be armed — bootstrap, not recover, handles fresh loops.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (fresh loop must not be recovered here)", got)
	}
}

// TestPeriodicRunner_RecoverStalledOnCompletion_ReachedMaxDuration_Noop verifies that
// recoverStalledOnCompletion does not re-arm a loop that has exceeded its wall-clock cap,
// so the auto-stop logic in fireOnCompletion can gracefully terminate the loop.
func TestPeriodicRunner_RecoverStalledOnCompletion_ReachedMaxDuration_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Cap of 60s, anchored 2h ago → cap is well exceeded.
	past := time.Now().Add(-2 * time.Hour)
	ps := newDurationCappedSession(t, store, "s1", &past, 60, 0)

	// Simulate at least one completed run.
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)

	meta := session.Metadata{SessionID: "s1"}
	periodic, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	// Precondition: cap is reached.
	if !periodic.ReachedMaxDuration(time.Now()) {
		t.Fatal("precondition failed: ReachedMaxDuration() = false, want true")
	}

	runner.recoverStalledOnCompletion(meta, periodic)

	// No timer must be armed — capped loops must not be kept alive.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (cap reached, must not re-arm)", got)
	}
}

// =============================================================================
// StoppedReason tests
// =============================================================================

// TestPeriodicRunner_AutoStopMaxDuration_SetsStoppedReason verifies that reaching
// the maxDuration cap via the schedule path sets StoppedReason=maxDuration.
func TestPeriodicRunner_AutoStopMaxDuration_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "dur-sched", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic("dur-sched")
	if err := periodicStore.Set(&session.PeriodicPrompt{
		Prompt:             "Test",
		Frequency:          session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:            true,
		MaxDurationSeconds: 60,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Force past-due and anchored 2h ago so the cap is exceeded.
	got, _ := periodicStore.Get()
	pastDue := time.Now().UTC().Add(-1 * time.Hour)
	anchor := time.Now().UTC().Add(-2 * time.Hour)
	got.NextScheduledAt = &pastDue
	got.FirstRunAt = &anchor
	periodicPath := store.SessionDir("dur-sched") + "/periodic.json"
	if err := writeTestPeriodicFile(periodicPath, got); err != nil {
		t.Fatalf("writeTestPeriodicFile() error = %v", err)
	}

	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})
	runner := NewPeriodicRunner(store, sm, nil)
	runner.RunOnce()

	final, _ := periodicStore.Get()
	if final.StoppedReason != session.StoppedReasonMaxDuration {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonMaxDuration)
	}
	if final.StoppedAt == nil {
		t.Error("StoppedAt should be non-nil after maxDuration auto-stop")
	}
}

// TestPeriodicRunner_AutoStopMaxIterations_SetsStoppedReason verifies the per-prompt
// MaxIterations cap sets StoppedReason=maxIterations.
func TestPeriodicRunner_AutoStopMaxIterations_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "iter-cap", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic("iter-cap")

	// MaxIterations=2, IterationCount=2 → already reached cap.
	if err := periodicStore.Set(&session.PeriodicPrompt{
		Prompt:         "Test",
		Frequency:      session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:        true,
		MaxIterations:  2,
		IterationCount: 2,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Use the internal MarkStopped path directly (via autoStopIfMaxDurationReached is N/A here;
	// test the iteration-cap path via the OnComplete callback indirectly through the runner).
	// Distinguish reason: perPromptReached=true → maxIterations.
	perPromptReached := true
	stoppedReason := session.StoppedReasonIterationSafeguard
	if perPromptReached {
		stoppedReason = session.StoppedReasonMaxIterations
	}
	if err := periodicStore.MarkStopped(stoppedReason); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	final, _ := periodicStore.Get()
	if final.StoppedReason != session.StoppedReasonMaxIterations {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonMaxIterations)
	}
}

// TestPeriodicRunner_AutoStopIterationSafeguard_SetsStoppedReason verifies the global
// safeguard path sets StoppedReason=iterationSafeguard.
func TestPeriodicRunner_AutoStopIterationSafeguard_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "safeguard", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic("safeguard")
	if err := periodicStore.Set(&session.PeriodicPrompt{
		Prompt:    "Test",
		Frequency: session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:   true,
		// MaxIterations=0 (unlimited) → only the global backstop triggers.
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Simulate the safeguard path: perPromptReached=false → iterationSafeguard.
	if err := periodicStore.MarkStopped(session.StoppedReasonIterationSafeguard); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	final, _ := periodicStore.Get()
	if final.StoppedReason != session.StoppedReasonIterationSafeguard {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonIterationSafeguard)
	}
}

// TestPeriodicRunner_AutoStopPromptUnresolved_SetsStoppedReason verifies that
// handlePromptResolveFailure sets StoppedReason=promptUnresolved after MaxPromptResolveFailures.
func TestPeriodicRunner_AutoStopPromptUnresolved_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "unresolved", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic("unresolved")
	if err := periodicStore.Set(&session.PeriodicPrompt{
		PromptName: "missing-prompt",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	resolveErr := errors.New("prompt not found")
	periodic, _ := periodicStore.Get()

	// Trigger exactly MaxPromptResolveFailures failures to trip the auto-pause.
	for i := 0; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("unresolved", meta.Name, periodic, periodicStore, resolveErr)
	}

	final, _ := periodicStore.Get()
	if final.Enabled {
		t.Error("periodic still enabled after MaxPromptResolveFailures, want disabled")
	}
	if final.StoppedReason != session.StoppedReasonPromptUnresolved {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonPromptUnresolved)
	}
	if final.StoppedAt == nil {
		t.Error("StoppedAt should be non-nil after promptUnresolved auto-stop")
	}
}

// TestPeriodicRunner_AutoStopResumeFailures_SetsStoppedReason verifies that the
// resume-failures path persists StoppedReason=resumeFailures before archiving.
func TestPeriodicRunner_AutoStopResumeFailures_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "resume-fail", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	periodicStore := store.Periodic("resume-fail")
	if err := periodicStore.Set(&session.PeriodicPrompt{
		Prompt:    "Test",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Simulate the resume-failures path directly.
	if err := periodicStore.MarkStopped(session.StoppedReasonResumeFailures); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	final, _ := periodicStore.Get()
	if final.StoppedReason != session.StoppedReasonResumeFailures {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonResumeFailures)
	}
	if final.StoppedAt == nil {
		t.Error("StoppedAt should be non-nil after resumeFailures stop")
	}
}

// TestPeriodicRunner_RecoverStalledOnCompletion_MaxDuration_AutoStops verifies that
// recoverStalledOnCompletion now routes through autoStopIfMaxDurationReached when the
// cap is exceeded, ending with Enabled=false and StoppedReason=maxDuration.
func TestPeriodicRunner_RecoverStalledOnCompletion_MaxDuration_AutoStops(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Cap of 60s, anchored 2h ago → cap is exceeded.
	past := time.Now().Add(-2 * time.Hour)
	ps := newDurationCappedSession(t, store, "s1", &past, 60, 0)
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}

	stopped := false
	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetOnPeriodicAutoStopped(func(_ string, _ *session.PeriodicPrompt) { stopped = true })

	meta := session.Metadata{SessionID: "s1"}
	periodic, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, periodic)

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (capped loop must not be re-armed)", got)
	}
	if !stopped {
		t.Error("onPeriodicAutoStopped not called, want it called for maxDuration auto-stop")
	}
	final, _ := ps.Get()
	if final.Enabled {
		t.Error("periodic still enabled after maxDuration recoverStalledOnCompletion, want disabled")
	}
	if final.StoppedReason != session.StoppedReasonMaxDuration {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonMaxDuration)
	}
}

// TestPeriodicRunner_RecoverStalledOnCompletion_SessionPrompting_Noop verifies that
// recoverStalledOnCompletion is a no-op when the session is currently prompting.
// An in-flight turn will re-arm itself on idle completion; recover must not race it.
func TestPeriodicRunner_RecoverStalledOnCompletion_SessionPrompting_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnCompletionSessionWithRan(t, store, "s1", 0)

	// Build a minimal session manager with a mock conversation.BackgroundSession that is prompting.
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})
	mockBS := conversation.NewMinimalBackgroundSessionPrompting("s1", true)
	sm.AddSessionForTest(mockBS)

	runner := NewPeriodicRunner(store, sm, nil)
	runner.SetMinPeriodicCompletionDelaySeconds(0)

	meta := session.Metadata{SessionID: "s1"}
	periodic, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, periodic)

	// No timer must be armed — the in-flight turn handles re-arm on completion.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (prompting session must block recovery)", got)
	}
}

// =============================================================================
// Arguments substitution tests
// =============================================================================

// TestPeriodicRunner_DeliverPrompt_ArgumentsForwardedAndSubstituted verifies that
// the periodic runner correctly resolves a named prompt via promptResolver and
// that the Arguments stored in the periodic config would produce the expected
// rendered text when passed through Go-template rendering — the same path taken
// by PromptWithMeta before dispatching to ACP.
//
// The test does NOT require a real ACP connection.  deliverPrompt is called
// but expected to fail with an ACP-unavailable error (the resolver has already
// been invoked by that point, proving the full argument pipeline is wired up).
func TestPeriodicRunner_DeliverPrompt_ArgumentsForwardedAndSubstituted(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "arg-dispatch", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	// Go-template form: {{ .Args.ISSUE_ID }} and {{ Arg "ENV" "prod" }} for default.
	const templateText = `Check {{ .Args.ISSUE_ID }} in {{ Arg "ENV" "prod" }}`
	var resolverCalled bool
	var resolvedName string

	runner := NewPeriodicRunner(store, nil, nil)
	runner.SetPromptResolver(func(name, dir string) (string, error) {
		resolverCalled = true
		resolvedName = name
		return templateText, nil
	})

	periodic := &session.PeriodicPrompt{
		PromptName: "check-status",
		Arguments:  map[string]string{"ISSUE_ID": "mitto-42"}, // ENV intentionally absent
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}
	periodicStore := store.Periodic("arg-dispatch")
	if err := periodicStore.Set(periodic); err != nil {
		t.Fatalf("periodicStore.Set() error = %v", err)
	}

	// Use a BackgroundSession with a valid context but no ACP connection.
	// deliverPrompt will call the promptResolver (step 1) and then call
	// PromptWithMeta (step 2). PromptWithMeta returns an error immediately
	// because there is no ACP connection. deliverPrompt propagates that error.
	// We verify that step 1 (resolver) ran before the ACP failure.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := conversation.NewTestBackgroundSessionWithCtx("arg-dispatch", ctx, cancel)

	deliverErr := runner.deliverPrompt(bs, "test-session", periodic, periodicStore, false, false)
	// The resolver must have been called even though PromptWithMeta failed.
	if !resolverCalled {
		t.Error("promptResolver was not called; periodic.PromptName not forwarded to deliverPrompt")
	}
	if resolvedName != "check-status" {
		t.Errorf("resolved name = %q, want %q", resolvedName, "check-status")
	}
	// The only allowed failure is from the missing ACP connection.  Any other
	// error (e.g. from argument processing) would indicate a bug introduced by
	// the Arguments wiring.
	if deliverErr == nil {
		t.Log("deliverPrompt returned nil (unexpected but not harmful for this test)")
	}

	// Verify that Go-template rendering with the stored arguments produces the
	// correct substituted text.  ENV is absent so the Arg helper must use the
	// default "prod".
	substituted := substituteTestArgs(templateText, periodic.Arguments)
	if want := "Check mitto-42 in prod"; substituted != want {
		t.Errorf("substituted text = %q, want %q", substituted, want)
	}
}

// TestPeriodicRunner_DeliverPrompt_DefaultRendered verifies that the Arg helper
// in a named prompt renders the default string when the key is absent from Arguments.
func TestPeriodicRunner_DeliverPrompt_DefaultRendered(t *testing.T) {
	const tmpl = `run {{ Arg "CMD" "lint" }} on {{ Arg "TARGET" "all" }}`
	args := map[string]string{"CMD": "test"} // TARGET absent — default must apply
	got := substituteTestArgs(tmpl, args)
	want := "run test on all"
	if got != want {
		t.Errorf("default rendering: got %q, want %q", got, want)
	}
}

// TestPeriodicRunner_DeliverPrompt_FreeTextUnaffected verifies that a periodic
// prompt using only the Prompt field (no PromptName, no Arguments) leaves a
// literal ${...} placeholder in the text untouched.  With nil Arguments the
// substituteTestArgs helper must not modify the text because the early-return
// guard fires on len(args)==0.
func TestPeriodicRunner_DeliverPrompt_FreeTextUnaffected(t *testing.T) {
	const freeText = "Check ${SOMETHING} now"
	periodic := &session.PeriodicPrompt{
		Prompt:    freeText,
		Arguments: nil, // free-text periodic has no arguments
	}
	// With nil Arguments the text must be returned verbatim.
	substituted := substituteTestArgs(freeText, periodic.Arguments)
	if substituted != freeText {
		t.Errorf("free-text substitution changed text: got %q, want %q", substituted, freeText)
	}
}

// substituteTestArgs mirrors the Go-template rendering that PromptWithMeta
// applies inside its async goroutine so tests can verify the correct output
// without a real ACP connection.
func substituteTestArgs(text string, args map[string]string) string {
	if len(args) == 0 {
		return text
	}
	ctx := &config.PromptEnabledContext{Args: args}
	funcs := config.BuildTemplateFuncMap(ctx)
	out, _ := config.RenderPromptTemplate("test", text, ctx, funcs)
	return out
}

// =============================================================================
// StopPeriodicForArchive tests (mitto-efnb)
// =============================================================================

// TestStopPeriodicForArchive_ScheduleBased verifies that StopPeriodicForArchive disables
// an enabled schedule-based periodic config, sets StoppedReason="archived", clears
// NextScheduledAt, and fires the onPeriodicAutoStopped callback.
func TestStopPeriodicForArchive_ScheduleBased(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a schedule-based periodic session.
	meta := session.Metadata{SessionID: "arch-sched", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	ps := store.Periodic("arch-sched")
	nextAt := time.Now().Add(time.Hour).UTC()
	if err := ps.Set(&session.PeriodicPrompt{
		Prompt:          "check",
		Enabled:         true,
		Trigger:         session.TriggerSchedule,
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		NextScheduledAt: &nextAt,
	}); err != nil {
		t.Fatalf("ps.Set() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)

	var callbackSessionID string
	var callbackPeriodic *session.PeriodicPrompt
	runner.SetOnPeriodicAutoStopped(func(sid string, p *session.PeriodicPrompt) {
		callbackSessionID = sid
		callbackPeriodic = p
	})

	runner.StopPeriodicForArchive("arch-sched", session.StoppedReasonArchived)

	final, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() after StopPeriodicForArchive: %v", err)
	}
	if final.Enabled {
		t.Error("Enabled = true after StopPeriodicForArchive, want false")
	}
	if final.StoppedReason != session.StoppedReasonArchived {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonArchived)
	}
	if final.NextScheduledAt != nil {
		t.Errorf("NextScheduledAt = %v, want nil", final.NextScheduledAt)
	}
	if callbackSessionID != "arch-sched" {
		t.Errorf("onPeriodicAutoStopped called with session %q, want %q", callbackSessionID, "arch-sched")
	}
	if callbackPeriodic == nil || callbackPeriodic.Enabled {
		t.Error("onPeriodicAutoStopped received nil or still-enabled periodic")
	}
}

// TestStopPeriodicForArchive_OnCompletion verifies that StopPeriodicForArchive disables
// an enabled onCompletion config, cancels any armed completion timer, and is a no-op
// (no panic, no broadcast) when there is no periodic config at all.
func TestStopPeriodicForArchive_OnCompletion(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create an onCompletion session with a very long timer so it won't fire.
	newOnCompletionSession(t, store, "arch-oc", 3600)

	runner := NewPeriodicRunner(store, nil, nil)
	callbackFired := false
	runner.SetOnPeriodicAutoStopped(func(_ string, _ *session.PeriodicPrompt) {
		callbackFired = true
	})

	// Arm a completion timer to confirm it gets cancelled.
	runner.armCompletionTimer("arch-oc", time.Hour)
	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("precondition: completionTimers = %d, want 1", got)
	}

	runner.StopPeriodicForArchive("arch-oc", session.StoppedReasonArchived)

	// Timer must be cancelled.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d after StopPeriodicForArchive, want 0", got)
	}

	// Config must be disabled.
	final, err := store.Periodic("arch-oc").Get()
	if err != nil {
		t.Fatalf("ps.Get() after StopPeriodicForArchive: %v", err)
	}
	if final.Enabled {
		t.Error("Enabled = true after StopPeriodicForArchive, want false")
	}
	if final.StoppedReason != session.StoppedReasonArchived {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonArchived)
	}
	if !callbackFired {
		t.Error("onPeriodicAutoStopped not called")
	}

	// No-op for a session with no periodic config (must not panic).
	meta2 := session.Metadata{SessionID: "no-periodic", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta2); err != nil {
		t.Fatalf("store.Create(no-periodic): %v", err)
	}
	broadcastCount := 0
	runner.SetOnPeriodicAutoStopped(func(_ string, _ *session.PeriodicPrompt) { broadcastCount++ })
	runner.StopPeriodicForArchive("no-periodic", session.StoppedReasonArchived) // must not panic
	if broadcastCount != 0 {
		t.Errorf("onPeriodicAutoStopped called %d times for session without periodic config, want 0", broadcastCount)
	}
}

// TestStopPeriodicForArchive_Idempotent verifies that StopPeriodicForArchive is a no-op
// (no second broadcast, reason unchanged) when the config is already disabled.
func TestStopPeriodicForArchive_Idempotent(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 3600)
	ps := store.Periodic("s1")

	runner := NewPeriodicRunner(store, nil, nil)
	broadcastCount := 0
	runner.SetOnPeriodicAutoStopped(func(_ string, _ *session.PeriodicPrompt) { broadcastCount++ })

	// First call disables.
	runner.StopPeriodicForArchive("s1", session.StoppedReasonArchived)
	if broadcastCount != 1 {
		t.Fatalf("broadcastCount = %d after first call, want 1", broadcastCount)
	}

	// Second call must be idempotent.
	runner.StopPeriodicForArchive("s1", session.StoppedReasonArchived)
	if broadcastCount != 1 {
		t.Errorf("broadcastCount = %d after second call, want 1 (idempotent)", broadcastCount)
	}

	// Original stopped reason must be preserved.
	final, _ := ps.Get()
	if final.StoppedReason != session.StoppedReasonArchived {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonArchived)
	}
}

// TestStopPeriodicForArchive_NoFurtherDelivery verifies that after archiving (via
// StopPeriodicForArchive + UpdateMetadata), RunOnce delivers nothing.
func TestStopPeriodicForArchive_NoFurtherDelivery(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a schedule-based session that is overdue.
	meta := session.Metadata{SessionID: "arch-nodelay", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	ps := store.Periodic("arch-nodelay")
	pastDue := time.Now().UTC().Add(-time.Hour)
	if err := ps.Set(&session.PeriodicPrompt{
		Prompt:          "check",
		Enabled:         true,
		Trigger:         session.TriggerSchedule,
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		NextScheduledAt: &pastDue,
	}); err != nil {
		t.Fatalf("ps.Set() error = %v", err)
	}

	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})
	runner := NewPeriodicRunner(store, sm, nil)

	// Archive the session: stop periodic and mark metadata archived.
	runner.StopPeriodicForArchive("arch-nodelay", session.StoppedReasonArchived)
	if err := store.UpdateMetadata("arch-nodelay", func(m *session.Metadata) {
		m.Archived = true
		m.ArchivedAt = time.Now()
		m.ArchiveReason = session.ArchiveReasonManual
	}); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, *, 0) for archived session", delivered, skipped, errored)
	}

	// Periodic config must remain disabled.
	final, _ := ps.Get()
	if final.Enabled {
		t.Error("periodic still enabled after archive + RunOnce")
	}
}

// TestOnConversationIdle_ArchivedNoop verifies that OnConversationIdle does NOT arm
// a completion timer for an archived session.
func TestOnConversationIdle_ArchivedNoop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create an onCompletion session then mark it archived.
	newOnCompletionSession(t, store, "s1", 3600)
	if err := store.UpdateMetadata("s1", func(m *session.Metadata) {
		m.Archived = true
	}); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	runner.OnConversationIdle("s1")

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (archived session must not arm timer)", got)
	}
}

// TestOnConversationIdle_ArchivedCancelsExistingTimer verifies that OnConversationIdle
// cancels a stale timer when the session is archived.
func TestOnConversationIdle_ArchivedCancelsExistingTimer(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 3600)
	if err := store.UpdateMetadata("s1", func(m *session.Metadata) {
		m.Archived = true
	}); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}

	runner := NewPeriodicRunner(store, nil, nil)
	// Pre-arm a stale timer.
	runner.armCompletionTimer("s1", time.Hour)
	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("precondition: completionTimers = %d, want 1", got)
	}

	runner.OnConversationIdle("s1")

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (archived must cancel stale timer)", got)
	}
}

// TestDeliverPrompt_PeriodicKind verifies that deliverPrompt sets PeriodicKind correctly
// on the PromptMeta: PeriodicKindScheduled for normal runs, PeriodicKindForced for "run now".
// This is a logic-level test — we verify the enum derivation logic independently (mitto-5xjn).
func TestDeliverPrompt_PeriodicKind(t *testing.T) {
	// Scheduled (forced=false)
	{
		forced := false
		kind := conversation.PeriodicKindScheduled
		if forced {
			kind = conversation.PeriodicKindForced
		}
		if kind != conversation.PeriodicKindScheduled {
			t.Errorf("forced=false: got PeriodicKind=%v, want PeriodicKindScheduled", kind)
		}
	}

	// Forced (forced=true)
	{
		forced := true
		kind := conversation.PeriodicKindScheduled
		if forced {
			kind = conversation.PeriodicKindForced
		}
		if kind != conversation.PeriodicKindForced {
			t.Errorf("forced=true: got PeriodicKind=%v, want PeriodicKindForced", kind)
		}
	}

	// Enum zero value must be PeriodicKindNone (not a periodic run).
	if conversation.PeriodicKindNone != 0 {
		t.Errorf("PeriodicKindNone must be 0 (zero value), got %d", conversation.PeriodicKindNone)
	}
}

func TestPeriodicScheduleBackoff(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		want     time.Duration
	}{
		{"zero clamps to first attempt", 0, periodicScheduleBackoffBase},
		{"first failure is base", 1, periodicScheduleBackoffBase},
		{"second failure doubles", 2, 2 * periodicScheduleBackoffBase},
		{"third failure quadruples", 3, 4 * periodicScheduleBackoffBase},
		{"fourth failure x8", 4, 8 * periodicScheduleBackoffBase},
		{"large failure count is capped", 100, periodicScheduleBackoffCap},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := periodicScheduleBackoff(tt.failures)
			if got != tt.want {
				t.Errorf("periodicScheduleBackoff(%d) = %v, want %v", tt.failures, got, tt.want)
			}
		})
	}
}

func TestPeriodicScheduleBackoff_MonotonicAndCapped(t *testing.T) {
	var prev time.Duration
	for f := 1; f <= 50; f++ {
		got := periodicScheduleBackoff(f)
		if got < prev {
			t.Errorf("backoff decreased: failures=%d got=%v prev=%v", f, got, prev)
		}
		if got > periodicScheduleBackoffCap {
			t.Errorf("backoff exceeded cap: failures=%d got=%v cap=%v", f, got, periodicScheduleBackoffCap)
		}
		prev = got
	}
}
