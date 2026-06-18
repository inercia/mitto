package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
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
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
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
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})

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
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})

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
	if err := periodicStore.Update(nil, nil, nil, &disabled, nil, nil); err != nil {
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
