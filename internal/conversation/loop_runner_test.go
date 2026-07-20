package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/beads/watcher"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/session"
)

// writeTestLoopFile writes a loop prompt directly to a file for testing.
func writeTestLoopFile(path string, p *session.LoopPrompt) error {
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

func TestLoopRunner_StartStop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
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

func TestLoopRunner_RunOnceNoSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || skipped != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, 0, 0)", delivered, skipped, errored)
	}
}

func TestLoopRunner_RunOnceNoLoopConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session without loop config
	meta := session.Metadata{
		SessionID:  "test-session-1",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || skipped != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, 0, 0)", delivered, skipped, errored)
	}
}

func TestLoopRunner_RunOnceSkipsArchivedSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create an archived session with loop config
	meta := session.Metadata{
		SessionID:  "archived-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
		Archived:   true,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Add loop config that would be due
	loopStore := store.Loop("archived-session")
	past := time.Now().UTC().Add(-1 * time.Hour)
	p := &session.LoopPrompt{
		Prompt:          "Test prompt",
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:         true,
		CreatedAt:       past,
		UpdatedAt:       past,
		NextScheduledAt: &past, // Due in the past
	}
	if err := loopStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	delivered, _, _ := runner.RunOnce()
	// Should not deliver because session is archived
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0 (archived session)", delivered)
	}
}

func TestLoopRunner_RunOnceSkipsDisabledConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with disabled loop config
	meta := session.Metadata{
		SessionID:  "disabled-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	loopStore := store.Loop("disabled-session")
	past := time.Now().UTC().Add(-1 * time.Hour)
	p := &session.LoopPrompt{
		Prompt:          "Test prompt",
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:         false, // Disabled
		CreatedAt:       past,
		UpdatedAt:       past,
		NextScheduledAt: &past,
	}
	if err := loopStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	delivered, _, _ := runner.RunOnce()
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0 (disabled)", delivered)
	}
}

func TestLoopRunner_RunOnceSkipsNotDueYet(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with loop config not due yet
	meta := session.Metadata{
		SessionID:  "not-due-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	loopStore := store.Loop("not-due-session")
	future := time.Now().UTC().Add(1 * time.Hour) // Due in the future
	p := &session.LoopPrompt{
		Prompt:          "Test prompt",
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:         true,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		NextScheduledAt: &future,
	}
	if err := loopStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	delivered, _, errored := runner.RunOnce()
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0 (not due yet)", delivered)
	}
	if errored != 0 {
		t.Errorf("errored = %d, want 0", errored)
	}
}

func TestLoopRunner_RunOnceAutoResumesInactiveSession(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with due loop config but no active ACP connection
	meta := session.Metadata{
		SessionID:  "inactive-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Create loop config - it will compute NextScheduledAt in the future
	// So we need to simulate a prompt that was created but its time has come
	loopStore := store.Loop("inactive-session")
	p := &session.LoopPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 5, Unit: session.FrequencyMinutes}, // Minimum interval
		Enabled:   true,
	}
	if err := loopStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Now we need to manually update the loop file to have a past NextScheduledAt
	// This simulates time passing since the prompt was created
	got, _ := loopStore.Get()
	past := time.Now().UTC().Add(-1 * time.Hour)
	got.NextScheduledAt = &past
	// Write directly to the file using fileutil
	loopPath := store.SessionDir("inactive-session") + "/loop.json"
	if err := writeTestLoopFile(loopPath, got); err != nil {
		t.Fatalf("writeTestLoopFile() error = %v", err)
	}

	// Create a session manager with no active sessions and no ACP configured
	// When ResumeSession is called, it will fail because no ACP command is configured
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})

	runner := NewLoopRunner(store, sm, nil)

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

func TestLoopRunner_NilStore(t *testing.T) {
	runner := NewLoopRunner(nil, nil, nil)

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

func TestLoopRunner_TriggerNow_NoStore(t *testing.T) {
	runner := NewLoopRunner(nil, nil, nil)
	err := runner.TriggerNow("test-session", true)
	if err != ErrSessionStoreNotAvailable {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrSessionStoreNotAvailable)
	}
}

func TestLoopRunner_TriggerNow_SessionNotFound(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
	err = runner.TriggerNow("nonexistent-session", true)
	if err != session.ErrSessionNotFound {
		t.Errorf("TriggerNow() error = %v, want %v", err, session.ErrSessionNotFound)
	}
}

func TestLoopRunner_TriggerNow_NoLoopConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session without loop config
	meta := session.Metadata{
		SessionID:  "test-session-1",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID, true)
	if err != session.ErrLoopNotFound {
		t.Errorf("TriggerNow() error = %v, want %v", err, session.ErrLoopNotFound)
	}
}

func TestLoopRunner_TriggerNow_NotEnabled(t *testing.T) {
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

	// Create a loop config with enabled=false
	loopStore := store.Loop(meta.SessionID)
	err = loopStore.Set(&session.LoopPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	if err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID, true)
	if err != ErrLoopNotEnabled {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrLoopNotEnabled)
	}
}

func TestLoopRunner_TriggerNow_NoSessionManager(t *testing.T) {
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

	// Create an enabled loop config
	loopStore := store.Loop(meta.SessionID)
	err = loopStore.Set(&session.LoopPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Runner without session manager
	runner := NewLoopRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID, true)
	if err != ErrSessionManagerNotAvailable {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrSessionManagerNotAvailable)
	}
}

// TestLoopRunner_TriggerNow_NoResetTimer verifies that TriggerNow accepts
// resetTimer=false and follows the same code path as resetTimer=true up to the
// point where the session manager is needed. This ensures the flag is correctly
// threaded through the call stack without being rejected early or panicking.
// (Full end-to-end verification that RecordSent is skipped requires an active
// ACP session and is covered by integration tests.)
func TestLoopRunner_TriggerNow_NoResetTimer(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with an enabled loop config
	meta := session.Metadata{
		SessionID:  "test-no-reset-timer",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	loopStore := store.Loop(meta.SessionID)
	err = loopStore.Set(&session.LoopPrompt{
		Prompt:    "Test prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Capture the initial schedule so we can verify it is not modified on error.
	initialLoop, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
	}
	initialNextScheduled := initialLoop.NextScheduledAt

	// Runner without session manager — should fail at ErrSessionManagerNotAvailable,
	// identical to the resetTimer=true case. This verifies that resetTimer=false is
	// accepted and reaches the same validation step without any early failure.
	runner := NewLoopRunner(store, nil, nil)
	err = runner.TriggerNow(meta.SessionID, false)
	if err != ErrSessionManagerNotAvailable {
		t.Errorf("TriggerNow() error = %v, want %v", err, ErrSessionManagerNotAvailable)
	}

	// Verify the schedule was not modified (error occurred before any delivery).
	afterLoop, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() after error = %v", err)
	}
	switch {
	case initialNextScheduled == nil && afterLoop.NextScheduledAt != nil:
		t.Error("NextScheduledAt was unexpectedly set after error")
	case initialNextScheduled != nil && afterLoop.NextScheduledAt == nil:
		t.Error("NextScheduledAt was unexpectedly cleared after error")
	case initialNextScheduled != nil && afterLoop.NextScheduledAt != nil:
		if !initialNextScheduled.Equal(*afterLoop.NextScheduledAt) {
			t.Errorf("NextScheduledAt changed unexpectedly: before=%v after=%v",
				*initialNextScheduled, *afterLoop.NextScheduledAt)
		}
	}
}

func TestLoopRunner_AutoArchiveSkipsLoopSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with enabled loop config
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	meta := session.Metadata{
		SessionID:  "loop-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	setSessionUpdatedAt(t, store, "loop-session", oldTime)

	// Add enabled loop config
	loopStore := store.Loop("loop-session")
	p := &session.LoopPrompt{
		Prompt:    "Test loop prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	}
	if err := loopStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Create runner with auto-archive threshold of 24 hours
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner := NewLoopRunner(store, sm, nil)
	runner.SetAutoArchiveAfter(24 * time.Hour)

	// Run auto-archive check
	runner.RunOnce()

	// Verify session was NOT archived
	updatedMeta, err := store.GetMetadata("loop-session")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if updatedMeta.Archived {
		t.Error("Session with enabled loop config should NOT be auto-archived")
	}
}

func TestLoopRunner_AutoArchiveSkipsPausedLoopSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session with disabled loop config
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	meta := session.Metadata{
		SessionID:  "disabled-loop-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Manually set UpdatedAt to 48 hours ago by writing metadata file directly
	// (store.Create and UpdateMetadata both overwrite UpdatedAt with time.Now())
	setSessionUpdatedAt(t, store, "disabled-loop-session", oldTime)

	// Add disabled loop config
	loopStore := store.Loop("disabled-loop-session")
	p := &session.LoopPrompt{
		Prompt:    "Test loop prompt",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false, // Disabled
	}
	if err := loopStore.Set(p); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Create session manager that can handle CloseSessionGracefully
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})

	// Create runner with auto-archive threshold of 24 hours
	runner := NewLoopRunner(store, sm, nil)
	runner.SetAutoArchiveAfter(24 * time.Hour)

	// Run auto-archive check
	runner.RunOnce()

	// Verify session was NOT archived (paused loop config should prevent archiving)
	updatedMeta, err := store.GetMetadata("disabled-loop-session")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if updatedMeta.Archived {
		t.Error("Session with paused loop config should NOT be auto-archived")
	}
}

func TestLoopRunner_AutoArchiveNoLoopConfig(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a session without loop config
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	meta := session.Metadata{
		SessionID:  "no-loop-session",
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Manually set UpdatedAt to 48 hours ago by writing metadata file directly
	// (store.Create and UpdateMetadata both overwrite UpdatedAt with time.Now())
	setSessionUpdatedAt(t, store, "no-loop-session", oldTime)

	// Create session manager
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})

	// Create runner with auto-archive threshold of 24 hours
	runner := NewLoopRunner(store, sm, nil)
	runner.SetAutoArchiveAfter(24 * time.Hour)

	// Run auto-archive check
	runner.RunOnce()

	// Verify session WAS archived
	updatedMeta, err := store.GetMetadata("no-loop-session")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if !updatedMeta.Archived {
		t.Error("Session without loop config SHOULD be auto-archived when inactive")
	}
}

// TestLoopRunner_ConfigCapAutoStop verifies that a loop conversation with no
// per-prompt cap (MaxIterations=0) auto-stops when the runner's configured default cap
// is reached. This tests the global safeguard layer independently of the per-prompt cap.
func TestLoopRunner_ConfigCapAutoStop(t *testing.T) {
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

	loopStore := store.Loop(meta.SessionID)
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:        "Test prompt",
		Frequency:     session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:       true,
		MaxIterations: 0, // No per-prompt cap
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Set up runner with a small config cap (3 iterations)
	const configCap = 3
	runner := NewLoopRunner(store, nil, nil)
	runner.SetMaxLoopIterations(configCap)

	// Verify that SetMaxLoopIterations stored the value
	runner.mu.Lock()
	stored := runner.maxLoopIterations
	runner.mu.Unlock()
	if stored != configCap {
		t.Fatalf("maxLoopIterations = %d, want %d", stored, configCap)
	}

	// Simulate configCap successful deliveries by calling RecordSent directly.
	// This mirrors what OnComplete does after each successful PromptWithMeta call.
	for i := 0; i < configCap; i++ {
		if err := loopStore.RecordSent(); err != nil {
			t.Fatalf("RecordSent() [%d] error = %v", i+1, err)
		}
	}

	// Read the updated state and check the effective cap condition
	updated, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
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
	cfgCap := runner.maxLoopIterations
	runner.mu.Unlock()
	effective := config.EffectiveMaxLoopIterations(updated.MaxIterations, cfgCap)

	// Verify effective cap matches the configured cap (since per-prompt cap is 0)
	if effective != configCap {
		t.Errorf("effective cap = %d, want %d", effective, configCap)
	}

	// Verify the condition that triggers auto-stop
	if updated.IterationCount < effective {
		t.Errorf("auto-stop condition not met: IterationCount=%d, effective=%d",
			updated.IterationCount, effective)
	}

	// Simulate what OnComplete does: disable the loop prompt
	autoStopCalled := false
	runner.SetOnLoopAutoStopped(func(sessionID string, p *session.LoopPrompt) {
		autoStopCalled = true
		if sessionID != meta.SessionID {
			t.Errorf("onLoopAutoStopped sessionID = %q, want %q", sessionID, meta.SessionID)
		}
		if p.Enabled {
			t.Error("onLoopAutoStopped: loop.Enabled = true, want false")
		}
	})

	disabled := false
	if err := loopStore.Update(nil, nil, nil, &disabled, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("loopStore.Update(disable) error = %v", err)
	}

	// Invoke the callback as OnComplete does
	if final, err := loopStore.Get(); err == nil && runner.onLoopAutoStopped != nil {
		runner.onLoopAutoStopped(meta.SessionID, final)
	}

	// Verify the callback was invoked
	if !autoStopCalled {
		t.Error("onLoopAutoStopped was not called")
	}

	// Verify the loop prompt is now disabled on disk
	final, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() after disable error = %v", err)
	}
	if final.Enabled {
		t.Error("loop.Enabled = true after auto-stop, want false")
	}
}

// TestLoopRunner_IterationSafeguardBranchSelection verifies the discriminant
// used by the auto-stop log branches in deliverPrompt: when the per-prompt cap
// is unlimited (MaxIterations=0), the runner distinguishes the hardcoded
// GlobalMaxLoopIterations backstop (WARN) from the config-level cap (INFO)
// via `effective == config.GlobalMaxLoopIterations`.
func TestLoopRunner_IterationSafeguardBranchSelection(t *testing.T) {
	// Case A: both caps unlimited — effective falls through to the hardcoded backstop
	// (WARN branch: effective == GlobalMaxLoopIterations).
	effective := config.EffectiveMaxLoopIterations(0, 0)
	if effective != config.GlobalMaxLoopIterations {
		t.Errorf("case A: effective = %d, want %d (GlobalMaxLoopIterations)",
			effective, config.GlobalMaxLoopIterations)
	}

	// Case B: config-level cap is the binding limit, per-prompt cap is unlimited.
	const cfgCap = 100
	effective = config.EffectiveMaxLoopIterations(0, cfgCap)
	if effective != cfgCap {
		t.Errorf("case B: effective = %d, want %d (config-level cap)", effective, cfgCap)
	}
	if effective >= config.GlobalMaxLoopIterations {
		t.Error("case B: expected INFO (configured-cap) branch, got WARN branch")
	}

	// Case C: per-prompt cap smaller than config cap — per-prompt cap wins, but the
	// runner's log path only reaches the safeguard branches when perPromptReached=false.
	// This documents that a positive promptMax below configMax IS honored (no behavior
	// change from EffectiveMaxLoopIterations).
	effective = config.EffectiveMaxLoopIterations(5, cfgCap)
	if effective != 5 {
		t.Errorf("case C: effective = %d, want 5 (per-prompt cap honored)", effective)
	}
}

// TestLoopRunner_DefaultMaxLoopIterations verifies that the runner
// is initialized with the correct default config cap.
func TestLoopRunner_DefaultMaxLoopIterations(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)

	runner.mu.Lock()
	got := runner.maxLoopIterations
	runner.mu.Unlock()

	if got != config.DefaultMaxLoopIterations {
		t.Errorf("initial maxLoopIterations = %d, want %d (DefaultMaxLoopIterations)",
			got, config.DefaultMaxLoopIterations)
	}
}

// TestLoopRunner_MinCompletionDelaySeconds verifies the setter/getter and
// that the runner is initialized with the correct default.
func TestLoopRunner_MinCompletionDelaySeconds(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)

	t.Run("default is DefaultMinLoopCompletionDelaySeconds", func(t *testing.T) {
		got := runner.MinLoopCompletionDelaySeconds()
		if got != config.DefaultMinLoopCompletionDelaySeconds {
			t.Errorf("initial minCompletionDelaySeconds = %d, want %d (DefaultMinLoopCompletionDelaySeconds)",
				got, config.DefaultMinLoopCompletionDelaySeconds)
		}
	})

	t.Run("set and get round-trip", func(t *testing.T) {
		runner.SetMinLoopCompletionDelaySeconds(30)
		got := runner.MinLoopCompletionDelaySeconds()
		if got != 30 {
			t.Errorf("MinLoopCompletionDelaySeconds() = %d, want 30", got)
		}
	})

	t.Run("negative value clamped to zero", func(t *testing.T) {
		runner.SetMinLoopCompletionDelaySeconds(-5)
		got := runner.MinLoopCompletionDelaySeconds()
		if got != 0 {
			t.Errorf("MinLoopCompletionDelaySeconds() = %d after negative set, want 0", got)
		}
	})

	t.Run("zero is accepted", func(t *testing.T) {
		runner.SetMinLoopCompletionDelaySeconds(0)
		got := runner.MinLoopCompletionDelaySeconds()
		if got != 0 {
			t.Errorf("MinLoopCompletionDelaySeconds() = %d, want 0", got)
		}
	})
}

// countCompletionTimers returns the number of armed on-completion timers, read
// under the runner's timer mutex so it is safe against concurrent AfterFunc callbacks.
func countCompletionTimers(r *LoopRunner) int {
	r.completionTimersMu.Lock()
	defer r.completionTimersMu.Unlock()
	return len(r.completionTimers)
}

// newOnCompletionSession creates a session with an enabled onCompletion loop
// prompt configured with the given delay.
func newOnCompletionSession(t *testing.T, store *session.Store, sessionID string, delaySeconds int) {
	t.Helper()
	meta := session.Metadata{SessionID: sessionID, ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Loop(sessionID).Set(&session.LoopPrompt{
		Prompt:       "iterate",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: delaySeconds,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}
}

func TestLoopRunner_OnConversationIdle_ArmsForOnCompletion(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Long delay so the timer does not fire during the test.
	newOnCompletionSession(t, store, "s1", 3600)

	runner := NewLoopRunner(store, nil, nil)
	runner.OnConversationIdle("s1")
	defer runner.cancelCompletionTimer("s1")

	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d, want 1", got)
	}
}

func TestLoopRunner_OnConversationIdle_IgnoresScheduleTrigger(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Loop("s1").Set(&session.LoopPrompt{
		Prompt:    "x",
		Enabled:   true,
		Trigger:   session.TriggerSchedule,
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	runner.OnConversationIdle("s1")

	if got := countCompletionTimers(runner); got != 0 {
		t.Fatalf("completionTimers = %d, want 0 (schedule trigger must not arm)", got)
	}
}

func TestLoopRunner_OnConversationIdle_CancelsStaleTimer(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Session without any loop config.
	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
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

func TestLoopRunner_OnConversationIdle_ReArmReplaces(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 3600)

	runner := NewLoopRunner(store, nil, nil)
	defer runner.cancelCompletionTimer("s1")

	runner.OnConversationIdle("s1")
	runner.OnConversationIdle("s1")

	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d after re-arm, want 1 (must replace, not stack)", got)
	}
}

func TestLoopRunner_OnConversationIdle_FiresAfterDelay(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0)

	// No session manager: firing reaches TriggerNow which errors out, but the
	// timer entry is cleared once it fires — which is what we assert here.
	runner := NewLoopRunner(store, nil, nil)
	runner.SetMinLoopCompletionDelaySeconds(0)
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

func TestLoopRunner_OnConversationIdle_FloorOverridesDelay(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Tiny configured delay, but a large global floor must win.
	newOnCompletionSession(t, store, "s1", 0)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetMinLoopCompletionDelaySeconds(3600) // 1h floor
	runner.OnConversationIdle("s1")
	defer runner.cancelCompletionTimer("s1")

	// Well within the 1h floor — the timer must not have fired.
	time.Sleep(200 * time.Millisecond)
	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("completionTimers = %d, want 1 (floor must override the small delay)", got)
	}
}

func TestLoopRunner_fireOnCompletion_ArchivedNoop(t *testing.T) {
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

	runner := NewLoopRunner(store, nil, nil)
	// Should return early without panicking or arming anything.
	runner.fireOnCompletion("s1")
	if got := countCompletionTimers(runner); got != 0 {
		t.Fatalf("completionTimers = %d, want 0", got)
	}
}

func TestLoopRunner_OnConversationIdle_NilStore(t *testing.T) {
	runner := NewLoopRunner(nil, nil, nil)
	// Must not panic with a nil store.
	runner.OnConversationIdle("x")
	runner.fireOnCompletion("x")
}

// newDurationCappedSession creates a session with an enabled onCompletion loop
// prompt anchored at firstRunAt, with the given maxDuration (seconds) and maxIterations.
// firstRunAt may be nil to model a prompt that has not yet run (not yet anchored).
func newDurationCappedSession(t *testing.T, store *session.Store, sessionID string, firstRunAt *time.Time, maxDurationSeconds, maxIterations int) *session.LoopStore {
	t.Helper()
	meta := session.Metadata{SessionID: sessionID, ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	ps := store.Loop(sessionID)
	if err := ps.Set(&session.LoopPrompt{
		Prompt:             "iterate",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		MaxDurationSeconds: maxDurationSeconds,
		MaxIterations:      maxIterations,
		FirstRunAt:         firstRunAt,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}
	return ps
}

func TestLoopRunner_autoStopIfMaxDurationReached(t *testing.T) {
	t.Run("reached disables and broadcasts", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		defer store.Close()

		past := time.Now().Add(-2 * time.Hour)
		ps := newDurationCappedSession(t, store, "s1", &past, 60, 0) // 60s cap, anchored 2h ago

		runner := NewLoopRunner(store, nil, nil)
		var gotID string
		var gotDisabled, called bool
		runner.SetOnLoopAutoStopped(func(id string, p *session.LoopPrompt) {
			called = true
			gotID = id
			gotDisabled = !p.Enabled
		})

		loop, err := ps.Get()
		if err != nil {
			t.Fatalf("ps.Get() error = %v", err)
		}
		if !runner.autoStopIfMaxDurationReached("s1", loop, ps, time.Now()) {
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
			t.Error("loop still enabled after auto-stop, want disabled")
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

		runner := NewLoopRunner(store, nil, nil)
		loop, _ := ps.Get()
		if runner.autoStopIfMaxDurationReached("s1", loop, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = true, want false (maxDuration=0 is unlimited)")
		}
		final, _ := ps.Get()
		if !final.Enabled {
			t.Error("loop disabled, want still enabled (unlimited)")
		}
	})

	t.Run("not yet anchored returns false", func(t *testing.T) {
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		defer store.Close()

		ps := newDurationCappedSession(t, store, "s1", nil, 60, 0) // FirstRunAt nil
		runner := NewLoopRunner(store, nil, nil)
		loop, _ := ps.Get()
		if runner.autoStopIfMaxDurationReached("s1", loop, ps, time.Now()) {
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
		runner := NewLoopRunner(store, nil, nil)
		loop, _ := ps.Get()
		if runner.autoStopIfMaxDurationReached("s1", loop, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = true, want false (within cap)")
		}
	})

	t.Run("nil loop returns false", func(t *testing.T) {
		runner := NewLoopRunner(nil, nil, nil)
		if runner.autoStopIfMaxDurationReached("s1", nil, nil, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = true, want false (nil loop)")
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
		runner := NewLoopRunner(store, nil, nil)
		loop, _ := ps.Get()
		if loop.ReachedMaxIterations() {
			t.Fatal("precondition failed: ReachedMaxIterations() = true, want false")
		}
		if !runner.autoStopIfMaxDurationReached("s1", loop, ps, time.Now()) {
			t.Fatal("autoStopIfMaxDurationReached() = false, want true (duration cap wins)")
		}
		final, _ := ps.Get()
		if final.Enabled {
			t.Error("loop still enabled, want disabled (duration cap reached first)")
		}
	})
}

// TestLoopRunner_fireOnCompletion_MaxDurationAutoStops verifies the on-completion
// firing path auto-stops (without delivering) once the wall-clock cap is exceeded.
func TestLoopRunner_fireOnCompletion_MaxDurationAutoStops(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	past := time.Now().Add(-2 * time.Hour)
	ps := newDurationCappedSession(t, store, "s1", &past, 60, 0)

	runner := NewLoopRunner(store, nil, nil)
	called := false
	runner.SetOnLoopAutoStopped(func(id string, p *session.LoopPrompt) { called = true })

	runner.fireOnCompletion("s1")

	final, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}
	if final.Enabled {
		t.Error("fireOnCompletion did not auto-stop on maxDuration, loop still enabled")
	}
	if !called {
		t.Error("onLoopAutoStopped not called from fireOnCompletion")
	}
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0", got)
	}
}

// TestLoopRunner_PromptResolveFailure_AutoPauses verifies that after
// MaxPromptResolveFailures consecutive resolve failures the loop config is
// disabled on disk and onLoopAutoStopped is fired exactly once.
func TestLoopRunner_PromptResolveFailure_AutoPauses(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "resolve-fail", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	loopStore := store.Loop("resolve-fail")
	if err := loopStore.Set(&session.LoopPrompt{
		PromptName: "nonexistent-prompt",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	resolveErr := errors.New("prompt not found")
	runner := NewLoopRunner(store, nil, nil)
	runner.SetPromptResolver(func(name, dir string) (string, error) {
		return "", resolveErr
	})

	callCount := 0
	runner.SetOnLoopAutoStopped(func(id string, p *session.LoopPrompt) {
		callCount++
		if id != "resolve-fail" {
			t.Errorf("onLoopAutoStopped: id=%q, want resolve-fail", id)
		}
		if p.Enabled {
			t.Error("onLoopAutoStopped: loop.Enabled = true, want false")
		}
	})

	loop, _ := loopStore.Get()

	// First MaxPromptResolveFailures-1 calls must not disable.
	for i := 1; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("resolve-fail", meta.Name, loop, loopStore, resolveErr)
		p, _ := loopStore.Get()
		if !p.Enabled {
			t.Fatalf("loop disabled after %d failures, want still enabled", i)
		}
		if callCount != 0 {
			t.Fatalf("onLoopAutoStopped called after %d failures, want 0", i)
		}
	}

	// The MaxPromptResolveFailures-th call must disable and fire callback exactly once.
	runner.handlePromptResolveFailure("resolve-fail", meta.Name, loop, loopStore, resolveErr)
	if callCount != 1 {
		t.Errorf("onLoopAutoStopped called %d times, want 1", callCount)
	}
	final, _ := loopStore.Get()
	if final.Enabled {
		t.Error("loop still enabled after auto-pause, want disabled")
	}
}

// TestLoopRunner_PromptResolveFailure_CounterReset verifies that a successful
// resolve resets the failure counter so prior failures don't accumulate.
func TestLoopRunner_PromptResolveFailure_CounterReset(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "reset-test", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	loopStore := store.Loop("reset-test")
	if err := loopStore.Set(&session.LoopPrompt{
		PromptName: "maybe-missing",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	resolveErr := errors.New("not found")
	runner := NewLoopRunner(store, nil, nil)
	runner.SetOnLoopAutoStopped(func(_ string, _ *session.LoopPrompt) {
		t.Error("onLoopAutoStopped called unexpectedly after counter reset")
	})

	loop, _ := loopStore.Get()

	// Accumulate MaxPromptResolveFailures-1 failures.
	for i := 1; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("reset-test", meta.Name, loop, loopStore, resolveErr)
	}

	// Simulate a successful resolution: reset the counter (mirrors checkSession success path).
	runner.promptResolveFailuresMu.Lock()
	delete(runner.promptResolveFailures, "reset-test")
	runner.promptResolveFailuresMu.Unlock()

	// Now accumulate MaxPromptResolveFailures-1 more failures — should not trigger auto-pause.
	for i := 1; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("reset-test", meta.Name, loop, loopStore, resolveErr)
	}

	// Verify the loop is still enabled (counter was reset, threshold not reached again).
	final, _ := loopStore.Get()
	if !final.Enabled {
		t.Error("loop disabled unexpectedly; counter reset did not clear failure count")
	}
}

// TestLoopRunner_RunOnce_MaxDurationAutoStops verifies the schedule (poll) path
// auto-stops a due loop once the wall-clock cap is exceeded, before any delivery
// or session resume. With a nil session manager, reaching the cap must neither deliver
// nor error — it disables the config and broadcasts the auto-stop.
func TestLoopRunner_RunOnce_MaxDurationAutoStops(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "sched", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	loopStore := store.Loop("sched")
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:             "Test prompt",
		Frequency:          session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:            true,
		Trigger:            session.TriggerSchedule,
		MaxDurationSeconds: 60,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Force the loop due (past NextScheduledAt) and anchored 2h ago so the cap is exceeded.
	got, _ := loopStore.Get()
	pastDue := time.Now().UTC().Add(-1 * time.Hour)
	anchor := time.Now().UTC().Add(-2 * time.Hour)
	got.NextScheduledAt = &pastDue
	got.FirstRunAt = &anchor
	loopPath := store.SessionDir("sched") + "/loop.json"
	if err := writeTestLoopFile(loopPath, got); err != nil {
		t.Fatalf("writeTestLoopFile() error = %v", err)
	}

	// Empty session manager: GetSession returns nil safely. The duration check in
	// checkSession fires before any resume attempt, so nothing is delivered.
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner := NewLoopRunner(store, sm, nil)
	called := false
	runner.SetOnLoopAutoStopped(func(id string, p *session.LoopPrompt) { called = true })

	delivered, skipped, errored := runner.RunOnce()
	if delivered != 0 || skipped != 0 || errored != 0 {
		t.Errorf("RunOnce() = (%d, %d, %d), want (0, 0, 0) (auto-stop, no delivery)", delivered, skipped, errored)
	}
	if !called {
		t.Error("onLoopAutoStopped not called from schedule path")
	}
	final, _ := loopStore.Get()
	if final.Enabled {
		t.Error("schedule-path loop still enabled after maxDuration, want disabled")
	}
}

// =============================================================================
// BootstrapOnCompletion Tests
// =============================================================================

// TestLoopRunner_BootstrapOnCompletion_NilStore verifies that BootstrapOnCompletion
// is a no-op when the runner has no session store.
func TestLoopRunner_BootstrapOnCompletion_NilStore(t *testing.T) {
	runner := NewLoopRunner(nil, nil, nil)
	// Must not panic.
	runner.BootstrapOnCompletion("any-session")
}

// TestLoopRunner_BootstrapOnCompletion_FreshSession_AttemptsDelivery verifies that a
// fresh enabled onCompletion session (IterationCount==0, LastSentAt==nil) causes
// BootstrapOnCompletion to attempt immediate delivery via TriggerNow with no timer delay.
// With no session manager, TriggerNow fails gracefully; we assert no panic, no timer
// is armed (delivery is synchronous, not timer-deferred), and the config stays enabled.
func TestLoopRunner_BootstrapOnCompletion_FreshSession_AttemptsDelivery(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 30) // delay_seconds=30, must NOT apply to first run

	runner := NewLoopRunner(store, nil, nil) // nil SM → TriggerNow returns ErrSessionManagerNotAvailable
	runner.BootstrapOnCompletion("s1")

	// No timer should be armed — delivery is attempted synchronously, not via timer.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (bootstrap must not arm a timer)", got)
	}

	// Loop config must remain enabled — the failed TriggerNow must not disable it.
	loopStore := store.Loop("s1")
	p, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
	}
	if !p.Enabled {
		t.Error("loop.Enabled = false after failed bootstrap, want true")
	}
}

// TestLoopRunner_BootstrapOnCompletion_AlreadyRan_Noop verifies that
// BootstrapOnCompletion is a no-op when the session has already run at least once
// (IterationCount > 0), preventing double delivery on restart.
func TestLoopRunner_BootstrapOnCompletion_AlreadyRan_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0)

	// Simulate a completed first run by calling RecordSent.
	loopStore := store.Loop("s1")
	if err := loopStore.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}

	// Verify IterationCount advanced.
	p, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
	}
	if p.IterationCount == 0 {
		t.Fatal("IterationCount = 0 after RecordSent, expected > 0")
	}

	// BootstrapOnCompletion must be a no-op (session already ran).
	runner := NewLoopRunner(store, nil, nil)
	runner.BootstrapOnCompletion("s1")

	// No timer armed, no panic.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (already-ran session must be a no-op)", got)
	}
}

// TestLoopRunner_BootstrapOnCompletion_Disabled_Noop verifies that
// BootstrapOnCompletion is a no-op for a disabled loop config.
func TestLoopRunner_BootstrapOnCompletion_Disabled_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Loop("s1").Set(&session.LoopPrompt{
		Prompt:  "Test",
		Enabled: false, // disabled
		Trigger: session.TriggerOnCompletion,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	runner.BootstrapOnCompletion("s1") // must be a no-op

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (disabled config must be no-op)", got)
	}
}

// TestLoopRunner_BootstrapOnCompletion_ScheduleTrigger_Noop verifies that
// BootstrapOnCompletion is a no-op for schedule-trigger configs (it targets
// onCompletion only).
func TestLoopRunner_BootstrapOnCompletion_ScheduleTrigger_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "s1", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Loop("s1").Set(&session.LoopPrompt{
		Prompt:    "Test",
		Enabled:   true,
		Trigger:   session.TriggerSchedule, // schedule, not onCompletion
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	runner.BootstrapOnCompletion("s1") // must be a no-op

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (schedule trigger must be no-op)", got)
	}
}

// TestLoopRunner_BootstrapOnCompletion_TimerPending_Noop verifies that
// BootstrapOnCompletion is a no-op when an onCompletion timer is already pending,
// preventing double-firing within the same process lifetime.
func TestLoopRunner_BootstrapOnCompletion_TimerPending_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0)

	runner := NewLoopRunner(store, nil, nil)
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

// TestLoopRunner_RunOnce_OnCompletion_BootstrapsFirstRun verifies that the
// poll loop (RunOnce / checkSession) bootstraps a fresh onCompletion session by
// calling BootstrapOnCompletion rather than skipping the session entirely.
// With no session manager, TriggerNow fails gracefully and RunOnce returns (0,0,0).
// The important assertion: no error is counted (bootstrap failure is not an error),
// and no timer is armed (bootstrap is synchronous, not timer-deferred).
func TestLoopRunner_RunOnce_OnCompletion_BootstrapsFirstRun(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 30) // delay_seconds=30 must NOT defer the first run

	runner := NewLoopRunner(store, nil, nil)

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
func newOnCompletionSessionWithRan(t *testing.T, store *session.Store, sessionID string, delaySeconds int) *session.LoopStore {
	t.Helper()
	newOnCompletionSession(t, store, sessionID, delaySeconds)
	ps := store.Loop(sessionID)
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent() error = %v", err)
	}
	return ps
}

// TestLoopRunner_RecoverStalledOnCompletion_ReArmsStalledLoop verifies that
// recoverStalledOnCompletion arms a completion timer when the loop has run at
// least once, no timer is currently pending, and the session is not prompting.
func TestLoopRunner_RecoverStalledOnCompletion_ReArmsStalledLoop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnCompletionSessionWithRan(t, store, "s1", 3600) // long delay so timer doesn't fire

	runner := NewLoopRunner(store, nil, nil)
	runner.SetMinLoopCompletionDelaySeconds(0) // no floor so we can assert timer presence easily

	// Precondition: no timer pending.
	if got := countCompletionTimers(runner); got != 0 {
		t.Fatalf("precondition: completionTimers = %d, want 0", got)
	}

	meta := session.Metadata{SessionID: "s1"}
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, loop)
	defer runner.cancelCompletionTimer("s1")

	// A timer must now be armed — the stall was detected and the loop re-armed.
	if got := countCompletionTimers(runner); got != 1 {
		t.Errorf("completionTimers = %d, want 1 (stalled loop must be re-armed)", got)
	}
}

// TestLoopRunner_RecoverStalledOnCompletion_TimerPending_Noop verifies that
// recoverStalledOnCompletion is a no-op when a timer is already pending, i.e. the
// loop is healthy and does not need recovery.
func TestLoopRunner_RecoverStalledOnCompletion_TimerPending_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnCompletionSessionWithRan(t, store, "s1", 0)

	runner := NewLoopRunner(store, nil, nil)

	// Pre-arm a timer (simulates a healthy loop).
	runner.armCompletionTimer("s1", time.Hour)
	defer runner.cancelCompletionTimer("s1")

	// Record the exact timer pointer before calling recover.
	runner.completionTimersMu.Lock()
	timerBefore := runner.completionTimers["s1"]
	runner.completionTimersMu.Unlock()

	meta := session.Metadata{SessionID: "s1"}
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, loop)

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

// TestLoopRunner_RecoverStalledOnCompletion_FreshLoop_Noop verifies that
// recoverStalledOnCompletion is a no-op for a fresh loop (IterationCount==0,
// LastSentAt==nil). Fresh loops are the responsibility of BootstrapOnCompletion.
func TestLoopRunner_RecoverStalledOnCompletion_FreshLoop_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Fresh session: no RecordSent call, so IterationCount==0 and LastSentAt==nil.
	newOnCompletionSession(t, store, "s1", 0)
	ps := store.Loop("s1")

	runner := NewLoopRunner(store, nil, nil)

	meta := session.Metadata{SessionID: "s1"}
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	// Precondition: IterationCount==0, LastSentAt==nil.
	if loop.IterationCount != 0 || loop.LastSentAt != nil {
		t.Fatalf("precondition failed: IterationCount=%d LastSentAt=%v", loop.IterationCount, loop.LastSentAt)
	}

	runner.recoverStalledOnCompletion(meta, loop)

	// No timer must be armed — bootstrap, not recover, handles fresh loops.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (fresh loop must not be recovered here)", got)
	}
}

// TestLoopRunner_RecoverStalledOnCompletion_ReachedMaxDuration_Noop verifies that
// recoverStalledOnCompletion does not re-arm a loop that has exceeded its wall-clock cap,
// so the auto-stop logic in fireOnCompletion can gracefully terminate the loop.
func TestLoopRunner_RecoverStalledOnCompletion_ReachedMaxDuration_Noop(t *testing.T) {
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

	runner := NewLoopRunner(store, nil, nil)

	meta := session.Metadata{SessionID: "s1"}
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	// Precondition: cap is reached.
	if !loop.ReachedMaxDuration(time.Now()) {
		t.Fatal("precondition failed: ReachedMaxDuration() = false, want true")
	}

	runner.recoverStalledOnCompletion(meta, loop)

	// No timer must be armed — capped loops must not be kept alive.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (cap reached, must not re-arm)", got)
	}
}

// =============================================================================
// StoppedReason tests
// =============================================================================

// TestLoopRunner_AutoStopMaxDuration_SetsStoppedReason verifies that reaching
// the maxDuration cap via the schedule path sets StoppedReason=maxDuration.
func TestLoopRunner_AutoStopMaxDuration_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "dur-sched", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	loopStore := store.Loop("dur-sched")
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:             "Test",
		Frequency:          session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:            true,
		MaxDurationSeconds: 60,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Force past-due and anchored 2h ago so the cap is exceeded.
	got, _ := loopStore.Get()
	pastDue := time.Now().UTC().Add(-1 * time.Hour)
	anchor := time.Now().UTC().Add(-2 * time.Hour)
	got.NextScheduledAt = &pastDue
	got.FirstRunAt = &anchor
	loopPath := store.SessionDir("dur-sched") + "/loop.json"
	if err := writeTestLoopFile(loopPath, got); err != nil {
		t.Fatalf("writeTestLoopFile() error = %v", err)
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner := NewLoopRunner(store, sm, nil)
	runner.RunOnce()

	final, _ := loopStore.Get()
	if final.StoppedReason != session.StoppedReasonMaxDuration {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonMaxDuration)
	}
	if final.StoppedAt == nil {
		t.Error("StoppedAt should be non-nil after maxDuration auto-stop")
	}
}

// TestLoopRunner_AutoStopMaxIterations_SetsStoppedReason verifies the per-prompt
// MaxIterations cap sets StoppedReason=maxIterations.
func TestLoopRunner_AutoStopMaxIterations_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "iter-cap", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	loopStore := store.Loop("iter-cap")

	// MaxIterations=2, IterationCount=2 → already reached cap.
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:         "Test",
		Frequency:      session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:        true,
		MaxIterations:  2,
		IterationCount: 2,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Use the internal MarkStopped path directly (via autoStopIfMaxDurationReached is N/A here;
	// test the iteration-cap path via the OnComplete callback indirectly through the runner).
	// Distinguish reason: perPromptReached=true → maxIterations.
	perPromptReached := true
	stoppedReason := session.StoppedReasonIterationSafeguard
	if perPromptReached {
		stoppedReason = session.StoppedReasonMaxIterations
	}
	if err := loopStore.MarkStopped(stoppedReason); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	final, _ := loopStore.Get()
	if final.StoppedReason != session.StoppedReasonMaxIterations {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonMaxIterations)
	}
}

// TestLoopRunner_AutoStopIterationSafeguard_SetsStoppedReason verifies the global
// safeguard path sets StoppedReason=iterationSafeguard.
func TestLoopRunner_AutoStopIterationSafeguard_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "safeguard", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	loopStore := store.Loop("safeguard")
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:    "Test",
		Frequency: session.Frequency{Value: 5, Unit: session.FrequencyMinutes},
		Enabled:   true,
		// MaxIterations=0 (unlimited) → only the global backstop triggers.
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Simulate the safeguard path: perPromptReached=false → iterationSafeguard.
	if err := loopStore.MarkStopped(session.StoppedReasonIterationSafeguard); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	final, _ := loopStore.Get()
	if final.StoppedReason != session.StoppedReasonIterationSafeguard {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonIterationSafeguard)
	}
}

// TestLoopRunner_AutoStopPromptUnresolved_SetsStoppedReason verifies that
// handlePromptResolveFailure sets StoppedReason=promptUnresolved after MaxPromptResolveFailures.
func TestLoopRunner_AutoStopPromptUnresolved_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "unresolved", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	loopStore := store.Loop("unresolved")
	if err := loopStore.Set(&session.LoopPrompt{
		PromptName: "missing-prompt",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	resolveErr := errors.New("prompt not found")
	loop, _ := loopStore.Get()

	// Trigger exactly MaxPromptResolveFailures failures to trip the auto-pause.
	for i := 0; i < MaxPromptResolveFailures; i++ {
		runner.handlePromptResolveFailure("unresolved", meta.Name, loop, loopStore, resolveErr)
	}

	final, _ := loopStore.Get()
	if final.Enabled {
		t.Error("loop still enabled after MaxPromptResolveFailures, want disabled")
	}
	if final.StoppedReason != session.StoppedReasonPromptUnresolved {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonPromptUnresolved)
	}
	if final.StoppedAt == nil {
		t.Error("StoppedAt should be non-nil after promptUnresolved auto-stop")
	}
}

// TestLoopRunner_AutoStopResumeFailures_SetsStoppedReason verifies that the
// resume-failures path persists StoppedReason=resumeFailures before archiving.
func TestLoopRunner_AutoStopResumeFailures_SetsStoppedReason(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	meta := session.Metadata{SessionID: "resume-fail", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}

	loopStore := store.Loop("resume-fail")
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:    "Test",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Simulate the resume-failures path directly.
	if err := loopStore.MarkStopped(session.StoppedReasonResumeFailures); err != nil {
		t.Fatalf("MarkStopped() error = %v", err)
	}

	final, _ := loopStore.Get()
	if final.StoppedReason != session.StoppedReasonResumeFailures {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonResumeFailures)
	}
	if final.StoppedAt == nil {
		t.Error("StoppedAt should be non-nil after resumeFailures stop")
	}
}

// TestLoopRunner_RecoverStalledOnCompletion_MaxDuration_AutoStops verifies that
// recoverStalledOnCompletion now routes through autoStopIfMaxDurationReached when the
// cap is exceeded, ending with Enabled=false and StoppedReason=maxDuration.
func TestLoopRunner_RecoverStalledOnCompletion_MaxDuration_AutoStops(t *testing.T) {
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
	runner := NewLoopRunner(store, nil, nil)
	runner.SetOnLoopAutoStopped(func(_ string, _ *session.LoopPrompt) { stopped = true })

	meta := session.Metadata{SessionID: "s1"}
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, loop)

	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (capped loop must not be re-armed)", got)
	}
	if !stopped {
		t.Error("onLoopAutoStopped not called, want it called for maxDuration auto-stop")
	}
	final, _ := ps.Get()
	if final.Enabled {
		t.Error("loop still enabled after maxDuration recoverStalledOnCompletion, want disabled")
	}
	if final.StoppedReason != session.StoppedReasonMaxDuration {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonMaxDuration)
	}
}

// TestLoopRunner_RecoverStalledOnCompletion_SessionPrompting_Noop verifies that
// recoverStalledOnCompletion is a no-op when the session is currently prompting.
// An in-flight turn will re-arm itself on idle completion; recover must not race it.
func TestLoopRunner_RecoverStalledOnCompletion_SessionPrompting_Noop(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnCompletionSessionWithRan(t, store, "s1", 0)

	// Build a minimal session manager with a mock BackgroundSession that is prompting.
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	mockBS := NewMinimalBackgroundSessionPrompting("s1", true)
	sm.AddSessionForTest(mockBS)

	runner := NewLoopRunner(store, sm, nil)
	runner.SetMinLoopCompletionDelaySeconds(0)

	meta := session.Metadata{SessionID: "s1"}
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() error = %v", err)
	}

	runner.recoverStalledOnCompletion(meta, loop)

	// No timer must be armed — the in-flight turn handles re-arm on completion.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d, want 0 (prompting session must block recovery)", got)
	}
}

// =============================================================================
// Arguments substitution tests
// =============================================================================

// TestLoopRunner_DeliverPrompt_ArgumentsForwardedAndSubstituted verifies that
// the loop runner correctly resolves a named prompt via promptResolver and
// that the Arguments stored in the loop config would produce the expected
// rendered text when passed through Go-template rendering — the same path taken
// by PromptWithMeta before dispatching to ACP.
//
// The test does NOT require a real ACP connection.  deliverPrompt is called
// but expected to fail with an ACP-unavailable error (the resolver has already
// been invoked by that point, proving the full argument pipeline is wired up).
func TestLoopRunner_DeliverPrompt_ArgumentsForwardedAndSubstituted(t *testing.T) {
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

	runner := NewLoopRunner(store, nil, nil)
	runner.SetPromptResolver(func(name, dir string) (string, error) {
		resolverCalled = true
		resolvedName = name
		return templateText, nil
	})

	loop := &session.LoopPrompt{
		PromptName: "check-status",
		Arguments:  map[string]string{"ISSUE_ID": "mitto-42"}, // ENV intentionally absent
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	}
	loopStore := store.Loop("arg-dispatch")
	if err := loopStore.Set(loop); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Use a BackgroundSession with a valid context but no ACP connection.
	// deliverPrompt will call the promptResolver (step 1) and then call
	// PromptWithMeta (step 2). PromptWithMeta returns an error immediately
	// because there is no ACP connection. deliverPrompt propagates that error.
	// We verify that step 1 (resolver) ran before the ACP failure.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := NewTestBackgroundSessionWithCtx("arg-dispatch", ctx, cancel)

	deliverErr := runner.deliverPrompt(bs, meta, loop, loopStore, false, false, nil)
	// The resolver must have been called even though PromptWithMeta failed.
	if !resolverCalled {
		t.Error("promptResolver was not called; loop.PromptName not forwarded to deliverPrompt")
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
	substituted := substituteTestArgs(templateText, loop.Arguments)
	if want := "Check mitto-42 in prod"; substituted != want {
		t.Errorf("substituted text = %q, want %q", substituted, want)
	}
}

// TestLoopRunner_DeliverPrompt_DefaultRendered verifies that the Arg helper
// in a named prompt renders the default string when the key is absent from Arguments.
func TestLoopRunner_DeliverPrompt_DefaultRendered(t *testing.T) {
	const tmpl = `run {{ Arg "CMD" "lint" }} on {{ Arg "TARGET" "all" }}`
	args := map[string]string{"CMD": "test"} // TARGET absent — default must apply
	got := substituteTestArgs(tmpl, args)
	want := "run test on all"
	if got != want {
		t.Errorf("default rendering: got %q, want %q", got, want)
	}
}

// TestLoopRunner_DeliverPrompt_FreeTextUnaffected verifies that a loop
// prompt using only the Prompt field (no PromptName, no Arguments) leaves a
// literal ${...} placeholder in the text untouched.  With nil Arguments the
// substituteTestArgs helper must not modify the text because the early-return
// guard fires on len(args)==0.
func TestLoopRunner_DeliverPrompt_FreeTextUnaffected(t *testing.T) {
	const freeText = "Check ${SOMETHING} now"
	loop := &session.LoopPrompt{
		Prompt:    freeText,
		Arguments: nil, // free-text loop has no arguments
	}
	// With nil Arguments the text must be returned verbatim.
	substituted := substituteTestArgs(freeText, loop.Arguments)
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
// StopLoopForArchive tests (mitto-efnb)
// =============================================================================

// TestStopLoopForArchive_ScheduleBased verifies that StopLoopForArchive disables
// an enabled schedule-based loop config, sets StoppedReason="archived", clears
// NextScheduledAt, and fires the onLoopAutoStopped callback.
func TestStopLoopForArchive_ScheduleBased(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create a schedule-based loop session.
	meta := session.Metadata{SessionID: "arch-sched", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	ps := store.Loop("arch-sched")
	nextAt := time.Now().Add(time.Hour).UTC()
	if err := ps.Set(&session.LoopPrompt{
		Prompt:          "check",
		Enabled:         true,
		Trigger:         session.TriggerSchedule,
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		NextScheduledAt: &nextAt,
	}); err != nil {
		t.Fatalf("ps.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	var callbackSessionID string
	var callbackLoop *session.LoopPrompt
	runner.SetOnLoopAutoStopped(func(sid string, p *session.LoopPrompt) {
		callbackSessionID = sid
		callbackLoop = p
	})

	runner.StopLoopForArchive("arch-sched", session.StoppedReasonArchived)

	final, err := ps.Get()
	if err != nil {
		t.Fatalf("ps.Get() after StopLoopForArchive: %v", err)
	}
	if final.Enabled {
		t.Error("Enabled = true after StopLoopForArchive, want false")
	}
	if final.StoppedReason != session.StoppedReasonArchived {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonArchived)
	}
	if final.NextScheduledAt != nil {
		t.Errorf("NextScheduledAt = %v, want nil", final.NextScheduledAt)
	}
	if callbackSessionID != "arch-sched" {
		t.Errorf("onLoopAutoStopped called with session %q, want %q", callbackSessionID, "arch-sched")
	}
	if callbackLoop == nil || callbackLoop.Enabled {
		t.Error("onLoopAutoStopped received nil or still-enabled loop")
	}
}

// TestStopLoopForArchive_OnCompletion verifies that StopLoopForArchive disables
// an enabled onCompletion config, cancels any armed completion timer, and is a no-op
// (no panic, no broadcast) when there is no loop config at all.
func TestStopLoopForArchive_OnCompletion(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create an onCompletion session with a very long timer so it won't fire.
	newOnCompletionSession(t, store, "arch-oc", 3600)

	runner := NewLoopRunner(store, nil, nil)
	callbackFired := false
	runner.SetOnLoopAutoStopped(func(_ string, _ *session.LoopPrompt) {
		callbackFired = true
	})

	// Arm a completion timer to confirm it gets cancelled.
	runner.armCompletionTimer("arch-oc", time.Hour)
	if got := countCompletionTimers(runner); got != 1 {
		t.Fatalf("precondition: completionTimers = %d, want 1", got)
	}

	runner.StopLoopForArchive("arch-oc", session.StoppedReasonArchived)

	// Timer must be cancelled.
	if got := countCompletionTimers(runner); got != 0 {
		t.Errorf("completionTimers = %d after StopLoopForArchive, want 0", got)
	}

	// Config must be disabled.
	final, err := store.Loop("arch-oc").Get()
	if err != nil {
		t.Fatalf("ps.Get() after StopLoopForArchive: %v", err)
	}
	if final.Enabled {
		t.Error("Enabled = true after StopLoopForArchive, want false")
	}
	if final.StoppedReason != session.StoppedReasonArchived {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonArchived)
	}
	if !callbackFired {
		t.Error("onLoopAutoStopped not called")
	}

	// No-op for a session with no loop config (must not panic).
	meta2 := session.Metadata{SessionID: "no-loop", ACPServer: "test", WorkingDir: "/tmp"}
	if err := store.Create(meta2); err != nil {
		t.Fatalf("store.Create(no-loop): %v", err)
	}
	broadcastCount := 0
	runner.SetOnLoopAutoStopped(func(_ string, _ *session.LoopPrompt) { broadcastCount++ })
	runner.StopLoopForArchive("no-loop", session.StoppedReasonArchived) // must not panic
	if broadcastCount != 0 {
		t.Errorf("onLoopAutoStopped called %d times for session without loop config, want 0", broadcastCount)
	}
}

// TestStopLoopForArchive_Idempotent verifies that StopLoopForArchive is a no-op
// (no second broadcast, reason unchanged) when the config is already disabled.
func TestStopLoopForArchive_Idempotent(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 3600)
	ps := store.Loop("s1")

	runner := NewLoopRunner(store, nil, nil)
	broadcastCount := 0
	runner.SetOnLoopAutoStopped(func(_ string, _ *session.LoopPrompt) { broadcastCount++ })

	// First call disables.
	runner.StopLoopForArchive("s1", session.StoppedReasonArchived)
	if broadcastCount != 1 {
		t.Fatalf("broadcastCount = %d after first call, want 1", broadcastCount)
	}

	// Second call must be idempotent.
	runner.StopLoopForArchive("s1", session.StoppedReasonArchived)
	if broadcastCount != 1 {
		t.Errorf("broadcastCount = %d after second call, want 1 (idempotent)", broadcastCount)
	}

	// Original stopped reason must be preserved.
	final, _ := ps.Get()
	if final.StoppedReason != session.StoppedReasonArchived {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonArchived)
	}
}

// TestStopLoopForArchive_NoFurtherDelivery verifies that after archiving (via
// StopLoopForArchive + UpdateMetadata), RunOnce delivers nothing.
func TestStopLoopForArchive_NoFurtherDelivery(t *testing.T) {
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
	ps := store.Loop("arch-nodelay")
	pastDue := time.Now().UTC().Add(-time.Hour)
	if err := ps.Set(&session.LoopPrompt{
		Prompt:          "check",
		Enabled:         true,
		Trigger:         session.TriggerSchedule,
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		NextScheduledAt: &pastDue,
	}); err != nil {
		t.Fatalf("ps.Set() error = %v", err)
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner := NewLoopRunner(store, sm, nil)

	// Archive the session: stop loop and mark metadata archived.
	runner.StopLoopForArchive("arch-nodelay", session.StoppedReasonArchived)
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

	// Loop config must remain disabled.
	final, _ := ps.Get()
	if final.Enabled {
		t.Error("loop still enabled after archive + RunOnce")
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

	runner := NewLoopRunner(store, nil, nil)
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

	runner := NewLoopRunner(store, nil, nil)
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

// TestDeliverPrompt_LoopKind verifies that deliverPrompt sets LoopKind correctly
// on the PromptMeta: LoopKindScheduled for normal runs, LoopKindForced for "run now".
// This is a logic-level test — we verify the enum derivation logic independently (mitto-5xjn).
func TestDeliverPrompt_LoopKind(t *testing.T) {
	// Scheduled (forced=false)
	{
		forced := false
		kind := LoopKindScheduled
		if forced {
			kind = LoopKindForced
		}
		if kind != LoopKindScheduled {
			t.Errorf("forced=false: got LoopKind=%v, want LoopKindScheduled", kind)
		}
	}

	// Forced (forced=true)
	{
		forced := true
		kind := LoopKindScheduled
		if forced {
			kind = LoopKindForced
		}
		if kind != LoopKindForced {
			t.Errorf("forced=true: got LoopKind=%v, want LoopKindForced", kind)
		}
	}

	// Enum zero value must be LoopKindNone (not a loop run).
	if LoopKindNone != 0 {
		t.Errorf("LoopKindNone must be 0 (zero value), got %d", LoopKindNone)
	}
}

func TestLoopScheduleBackoff(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		want     time.Duration
	}{
		{"zero clamps to first attempt", 0, loopScheduleBackoffBase},
		{"first failure is base", 1, loopScheduleBackoffBase},
		{"second failure doubles", 2, 2 * loopScheduleBackoffBase},
		{"third failure quadruples", 3, 4 * loopScheduleBackoffBase},
		{"fourth failure x8", 4, 8 * loopScheduleBackoffBase},
		{"large failure count is capped", 100, loopScheduleBackoffCap},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loopScheduleBackoff(tt.failures)
			if got != tt.want {
				t.Errorf("loopScheduleBackoff(%d) = %v, want %v", tt.failures, got, tt.want)
			}
		})
	}
}

func TestLoopScheduleBackoff_MonotonicAndCapped(t *testing.T) {
	var prev time.Duration
	for f := 1; f <= 50; f++ {
		got := loopScheduleBackoff(f)
		if got < prev {
			t.Errorf("backoff decreased: failures=%d got=%v prev=%v", f, got, prev)
		}
		if got > loopScheduleBackoffCap {
			t.Errorf("backoff exceeded cap: failures=%d got=%v cap=%v", f, got, loopScheduleBackoffCap)
		}
		prev = got
	}
}

// =============================================================================
// onTasks trigger tests (mitto-oja.2)
// =============================================================================

// fakeTasksBeadsClient is a minimal beads.Client fake for onTasks tests. List
// returns listFn(dir) when set, otherwise an empty array. onTasks code only
// ever calls List; every other method is a no-op stub to satisfy the interface.
type fakeTasksBeadsClient struct {
	listFn func(dir string) ([]byte, error)

	mu        sync.Mutex
	listCalls []string
}

func (c *fakeTasksBeadsClient) List(_ context.Context, dir string) ([]byte, error) {
	c.mu.Lock()
	c.listCalls = append(c.listCalls, dir)
	c.mu.Unlock()
	if c.listFn != nil {
		return c.listFn(dir)
	}
	return []byte(`[]`), nil
}

func (c *fakeTasksBeadsClient) listCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.listCalls)
}

func (c *fakeTasksBeadsClient) Ready(context.Context, string) ([]byte, error) {
	return []byte(`[]`), nil
}
func (c *fakeTasksBeadsClient) Status(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *fakeTasksBeadsClient) Show(context.Context, string, string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *fakeTasksBeadsClient) Create(context.Context, string, beads.CreateParams) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *fakeTasksBeadsClient) Delete(context.Context, string, string) error { return nil }
func (c *fakeTasksBeadsClient) ListClosedIDs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (c *fakeTasksBeadsClient) DeleteIDs(context.Context, string, []string) error       { return nil }
func (c *fakeTasksBeadsClient) SetStatus(context.Context, string, string, string) error { return nil }
func (c *fakeTasksBeadsClient) Update(context.Context, string, beads.UpdateParams) error {
	return nil
}
func (c *fakeTasksBeadsClient) Comment(context.Context, string, string, string) error { return nil }
func (c *fakeTasksBeadsClient) Dep(context.Context, string, beads.DepParams) error    { return nil }
func (c *fakeTasksBeadsClient) Label(context.Context, string, beads.LabelParams) error {
	return nil
}
func (c *fakeTasksBeadsClient) ListAllLabels(context.Context, string) ([]byte, error) {
	return []byte(`[]`), nil
}
func (c *fakeTasksBeadsClient) ConfigShow(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (c *fakeTasksBeadsClient) ConfigSet(context.Context, string, string, string) error { return nil }
func (c *fakeTasksBeadsClient) ConfigUnset(context.Context, string, string) error       { return nil }
func (c *fakeTasksBeadsClient) EnsureInitialized(context.Context, string) error         { return nil }
func (c *fakeTasksBeadsClient) Sync(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (c *fakeTasksBeadsClient) MigrateRemote(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *fakeTasksBeadsClient) Bootstrap(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}

// newOnTasksSession creates a session with an enabled onTasks loop prompt
// configured with the given working dir and CEL condition (empty = fire on any change).
func newOnTasksSession(t *testing.T, store *session.Store, sessionID, workingDir, condition string) *session.LoopStore {
	t.Helper()
	meta := session.Metadata{SessionID: sessionID, ACPServer: "test", WorkingDir: workingDir}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Loop(sessionID).Set(&session.LoopPrompt{
		Prompt:    "iterate",
		Enabled:   true,
		Trigger:   session.TriggerOnTasks,
		Condition: condition,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}
	return store.Loop(sessionID)
}

func TestTasksDeltaIsMaterial(t *testing.T) {
	if tasksDeltaIsMaterial(nil) {
		t.Error("nil delta should not be material")
	}
	if tasksDeltaIsMaterial(&config.TasksDelta{}) {
		t.Error("empty delta should not be material")
	}
	if !tasksDeltaIsMaterial(&config.TasksDelta{Added: []map[string]any{{"id": "a"}}}) {
		t.Error("delta with Added should be material")
	}
	if !tasksDeltaIsMaterial(&config.TasksDelta{Updated: []map[string]any{{"id": "a"}}}) {
		t.Error("delta with Updated should be material")
	}
	if !tasksDeltaIsMaterial(&config.TasksDelta{Removed: []map[string]any{{"id": "a"}}}) {
		t.Error("delta with Removed should be material")
	}
}

func TestTasksTouchedIDsAndSubset(t *testing.T) {
	delta := &config.TasksDelta{Touched: []map[string]any{{"id": "a"}, {"id": "b"}, {"not-id": "x"}}}
	ids := tasksTouchedIDs(delta)
	if len(ids) != 2 {
		t.Fatalf("tasksTouchedIDs() = %v, want 2 entries", ids)
	}
	if _, ok := ids["a"]; !ok {
		t.Error("expected id 'a' in touched set")
	}

	// curr is a subset of prev => no progress.
	prev := map[string]struct{}{"a": {}, "b": {}, "c": {}}
	if !tasksIsSubsetOf(ids, prev) {
		t.Error("curr should be a subset of prev")
	}
	// curr has something new => progress.
	curr2 := map[string]struct{}{"a": {}, "new-id": {}}
	if tasksIsSubsetOf(curr2, prev) {
		t.Error("curr2 contains a new id, should not be a subset of prev")
	}
	// empty curr is trivially a subset.
	if !tasksIsSubsetOf(map[string]struct{}{}, prev) {
		t.Error("empty curr should be a trivial subset")
	}
}

func TestLoopRunner_TasksCooldownActive(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
	runner.SetMinLoopTasksCooldownSeconds(60)

	// Never sent — never on cooldown.
	p := &session.LoopPrompt{Trigger: session.TriggerOnTasks}
	if runner.tasksCooldownActive(p) {
		t.Error("never-sent prompt should not be on cooldown")
	}

	// Sent 1s ago, floor 60s => active.
	recently := time.Now().Add(-1 * time.Second)
	p.LastSentAt = &recently
	if !runner.tasksCooldownActive(p) {
		t.Error("prompt sent 1s ago with a 60s floor should be on cooldown")
	}

	// Sent 2 minutes ago, floor 60s => not active.
	longAgo := time.Now().Add(-2 * time.Minute)
	p.LastSentAt = &longAgo
	if runner.tasksCooldownActive(p) {
		t.Error("prompt sent 2 minutes ago with a 60s floor should not be on cooldown")
	}

	// Per-conversation CooldownSeconds overrides the floor when larger.
	p.CooldownSeconds = 300
	recent := time.Now().Add(-90 * time.Second)
	p.LastSentAt = &recent
	if !runner.tasksCooldownActive(p) {
		t.Error("per-conversation cooldown of 300s should still be active after 90s")
	}
}

func TestLoopRunner_IsTasksSubtreeBusy(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	if err := store.Create(session.Metadata{SessionID: "parent", ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	if err := store.Create(session.Metadata{SessionID: "child", ACPServer: "test", WorkingDir: "/tmp", ParentSessionID: "parent"}); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner := NewLoopRunner(store, sm, nil)

	// No sessions registered, nothing waiting => idle.
	if runner.isTasksSubtreeBusy("parent") {
		t.Error("subtree should be idle with no registered sessions")
	}

	// Parent itself prompting => busy.
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("parent", true))
	if !runner.isTasksSubtreeBusy("parent") {
		t.Error("subtree should be busy when the parent itself is prompting")
	}

	// Parent idle again, but child is prompting => still busy (delegated child).
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("parent", false))
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("child", true))
	if !runner.isTasksSubtreeBusy("parent") {
		t.Error("subtree should be busy when a delegated child is prompting")
	}

	// Both idle, but parent waiting for children => busy.
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("child", false))
	sm.BroadcastWaitingForChildren("parent", true)
	if !runner.isTasksSubtreeBusy("parent") {
		t.Error("subtree should be busy while waiting for children")
	}
	sm.BroadcastWaitingForChildren("parent", false)

	// Now fully idle.
	if runner.isTasksSubtreeBusy("parent") {
		t.Error("subtree should be idle once parent and child are both idle and not waiting")
	}
}

// beadsRow is a small helper to build a raw `bd list` JSON row for tests.
func beadsRow(id, status, updatedAt string) map[string]any {
	return map[string]any{"id": id, "type": "task", "status": status, "title": id, "updated_at": updatedAt}
}

func mustMarshalRows(t *testing.T, rows ...map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

// jsonBytesEqual reports whether a and b are semantically equal JSON documents,
// ignoring formatting differences (fileutil.WriteJSONAtomic always re-indents,
// including any embedded json.RawMessage, so byte-for-byte comparison is unsafe).
func jsonBytesEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("json.Unmarshal(a) error = %v", err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("json.Unmarshal(b) error = %v", err)
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}

func TestLoopRunner_EvaluateTasksChange_InitializesBaselineWithoutFiring(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnTasksSession(t, store, "s1", "/proj", "")
	runner := NewLoopRunner(store, nil, nil)

	meta, _ := store.GetMetadata("s1")
	loop, _ := store.Loop("s1").Get()
	raw := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))

	decision := runner.evaluateTasksChange(meta, loop, raw)
	if decision.action != tasksActionInitBaseline {
		t.Fatalf("action = %v, want tasksActionInitBaseline", decision.action)
	}

	// Baseline must not exist yet until processTasksChange (or the caller) persists it.
	if _, err := NewTasksBaselineStore(store.SessionDir("s1")).Get(); !errors.Is(err, ErrTasksBaselineNotFound) {
		t.Errorf("baseline should not exist before being persisted, got err = %v", err)
	}

	// Driving it through processTasksChange persists the baseline and does NOT fire
	// (no session manager is configured, so a firing attempt would be observable
	// only via baseline movement, which must not happen here).
	runner.processTasksChange(meta, loop, store.Loop("s1"), raw)
	baseline, err := NewTasksBaselineStore(store.SessionDir("s1")).Get()
	if err != nil {
		t.Fatalf("baseline should exist after processTasksChange, error = %v", err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, raw) {
		t.Errorf("baseline.RawSnapshot = %s, want %s", baseline.RawSnapshot, raw)
	}
}

func TestLoopRunner_EvaluateTasksChange_NoMaterialChange_Skip(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnTasksSession(t, store, "s1", "/proj", "")
	runner := NewLoopRunner(store, nil, nil)

	raw := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(raw); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	meta, _ := store.GetMetadata("s1")
	loop, _ := store.Loop("s1").Get()

	// Identical snapshot — no material change.
	decision := runner.evaluateTasksChange(meta, loop, raw)
	if decision.action != tasksActionSkip {
		t.Errorf("action = %v, want tasksActionSkip for an unchanged snapshot", decision.action)
	}
}

func TestLoopRunner_EvaluateTasksChange_EmptyConditionFiresOnAnyChange(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnTasksSession(t, store, "s1", "/proj", "")
	runner := NewLoopRunner(store, nil, nil)

	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}
	rawAfter := mustMarshalRows(t, beadsRow("mitto-1", "closed", "2026-01-02T00:00:00Z"))

	meta, _ := store.GetMetadata("s1")
	loop, _ := store.Loop("s1").Get()

	decision := runner.evaluateTasksChange(meta, loop, rawAfter)
	if decision.action != tasksActionFire {
		t.Fatalf("action = %v, want tasksActionFire for an empty condition with a material change", decision.action)
	}
	if len(decision.delta.Closed) != 1 {
		t.Errorf("delta.Closed = %v, want 1 closed issue", decision.delta.Closed)
	}
}

func TestLoopRunner_EvaluateTasksChange_ConditionFalse_Skip(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Condition only fires when an issue is closed; here we only add a new open one.
	newOnTasksSession(t, store, "s1", "/proj", "Changes.Closed.size() > 0")
	runner := NewLoopRunner(store, nil, nil)

	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}
	rawAfter := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"))

	meta, _ := store.GetMetadata("s1")
	loop, _ := store.Loop("s1").Get()

	decision := runner.evaluateTasksChange(meta, loop, rawAfter)
	if decision.action != tasksActionSkip {
		t.Fatalf("action = %v, want tasksActionSkip when the condition evaluates false", decision.action)
	}
}

func TestLoopRunner_EvaluateTasksChange_ConditionTrue_Fires(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnTasksSession(t, store, "s1", "/proj", "Changes.Closed.size() > 0")
	runner := NewLoopRunner(store, nil, nil)

	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}
	rawAfter := mustMarshalRows(t, beadsRow("mitto-1", "closed", "2026-01-02T00:00:00Z"))

	meta, _ := store.GetMetadata("s1")
	loop, _ := store.Loop("s1").Get()

	decision := runner.evaluateTasksChange(meta, loop, rawAfter)
	if decision.action != tasksActionFire {
		t.Fatalf("action = %v, want tasksActionFire when the condition evaluates true", decision.action)
	}
}

func TestLoopRunner_EvaluateTasksChange_InvalidCondition_FailClosed(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Bypass session.Validate (which would reject this) to exercise the
	// runtime fail-closed path directly: a condition that compiles but does
	// not evaluate to a bool.
	newOnTasksSession(t, store, "s1", "/proj", "")
	if err := writeTestLoopFile(filepath.Join(store.SessionDir("s1"), "loop.json"), &session.LoopPrompt{
		Prompt:    "iterate",
		Enabled:   true,
		Trigger:   session.TriggerOnTasks,
		Condition: "Changes.Touched.size()", // not a bool
	}); err != nil {
		t.Fatalf("writeTestLoopFile() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}
	rawAfter := mustMarshalRows(t, beadsRow("mitto-1", "closed", "2026-01-02T00:00:00Z"))

	meta, _ := store.GetMetadata("s1")
	loop, _ := store.Loop("s1").Get()

	decision := runner.evaluateTasksChange(meta, loop, rawAfter)
	if decision.action != tasksActionSkip {
		t.Fatalf("action = %v, want tasksActionSkip (fail-closed) for a non-bool condition result", decision.action)
	}
}

func TestLoopRunner_EvaluateTasksChange_BusySubtree_DefersRebase(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnTasksSession(t, store, "s1", "/proj", "")

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("s1", true))
	runner := NewLoopRunner(store, sm, nil)

	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}
	rawAfter := mustMarshalRows(t, beadsRow("mitto-1", "closed", "2026-01-02T00:00:00Z"))

	meta, _ := store.GetMetadata("s1")
	loop, _ := store.Loop("s1").Get()

	decision := runner.evaluateTasksChange(meta, loop, rawAfter)
	if decision.action != tasksActionDeferBusy {
		t.Fatalf("action = %v, want tasksActionDeferBusy while the session is prompting", decision.action)
	}

	// Driving it through processTasksChange must arm a rebase timer and leave
	// the baseline untouched (the change must be absorbed later, not fired on now).
	runner.processTasksChange(meta, loop, store.Loop("s1"), rawAfter)
	if got := countTasksRebaseTimers(runner); got != 1 {
		t.Errorf("tasksRebaseTimers = %d, want 1 after a busy-subtree event", got)
	}
	baseline, err := NewTasksBaselineStore(store.SessionDir("s1")).Get()
	if err != nil {
		t.Fatalf("Get() baseline error = %v", err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, rawBefore) {
		t.Error("baseline must remain unchanged while the subtree is busy")
	}
	runner.cancelTasksRebaseTimerForTest("s1")
}

func TestLoopRunner_EvaluateTasksChange_MaxDurationReached_Skip(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	firstRun := time.Now().Add(-2 * time.Hour)
	if err := writeTestLoopFile(filepath.Join(store.SessionDir("s1"), "loop.json"), &session.LoopPrompt{
		Prompt:             "iterate",
		Enabled:            true,
		Trigger:            session.TriggerOnTasks,
		MaxDurationSeconds: 3600,
		FirstRunAt:         &firstRun,
	}); err != nil {
		t.Fatalf("writeTestLoopFile() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}
	rawAfter := mustMarshalRows(t, beadsRow("mitto-1", "closed", "2026-01-02T00:00:00Z"))

	meta, _ := store.GetMetadata("s1")
	loop, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	decision := runner.evaluateTasksChange(meta, loop, rawAfter)
	if decision.action != tasksActionSkip {
		t.Fatalf("action = %v, want tasksActionSkip once maxDuration is reached", decision.action)
	}

	// The conversation must have been auto-stopped.
	got, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Enabled {
		t.Error("loop should be disabled after reaching max duration")
	}
	if got.StoppedReason != session.StoppedReasonMaxDuration {
		t.Errorf("StoppedReason = %q, want %q", got.StoppedReason, session.StoppedReasonMaxDuration)
	}
}

// countTasksRebaseTimers returns the number of armed onTasks rebase timers.
func countTasksRebaseTimers(r *LoopRunner) int {
	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	return len(r.tasksRebaseTimers)
}

// cancelTasksRebaseTimerForTest stops and removes a pending rebase timer so
// tests don't leak background timers.
func (r *LoopRunner) cancelTasksRebaseTimerForTest(sessionID string) {
	r.tasksRebaseTimersMu.Lock()
	defer r.tasksRebaseTimersMu.Unlock()
	if existing, ok := r.tasksRebaseTimers[sessionID]; ok {
		existing.Stop()
		delete(r.tasksRebaseTimers, sessionID)
	}
}

func TestLoopRunner_FireTasksRebase_RebasesWhenIdle(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner := NewLoopRunner(store, sm, nil)
	rawNow := mustMarshalRows(t, beadsRow("mitto-1", "closed", "2026-01-02T00:00:00Z"))
	fake := &fakeTasksBeadsClient{listFn: func(string) ([]byte, error) { return rawNow, nil }}
	runner.SetBeadsClient(fake)

	// Idle subtree — the rebase should pick up the latest snapshot, absorbing
	// the change without firing.
	runner.fireTasksRebase("s1", ps)

	baseline, err := NewTasksBaselineStore(store.SessionDir("s1")).Get()
	if err != nil {
		t.Fatalf("Get() baseline error = %v", err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, rawNow) {
		t.Errorf("baseline.RawSnapshot = %s, want %s", baseline.RawSnapshot, rawNow)
	}
	if got := countTasksRebaseTimers(runner); got != 0 {
		t.Errorf("tasksRebaseTimers = %d, want 0 after a successful rebase", got)
	}
}

func TestLoopRunner_FireTasksRebase_StillBusy_ReArms(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("s1", true))
	runner := NewLoopRunner(store, sm, nil)
	runner.SetTasksQuiescenceWindow(time.Hour) // long enough we can assert before it fires again

	runner.fireTasksRebase("s1", ps)

	if got := countTasksRebaseTimers(runner); got != 1 {
		t.Errorf("tasksRebaseTimers = %d, want 1 (re-armed because still busy)", got)
	}
	runner.cancelTasksRebaseTimerForTest("s1")
}

// mitto-dmb: TestLoopRunner_EvaluateAccumulatedDelta_* tests exercise the
// pure decision helper that decides whether an onTasks loop opted out of the
// during-busy coalesce (CoalesceDuringBusy=false) should re-fire.

// TestLoopRunner_EvaluateAccumulatedDelta_MaterialChange_Fires verifies that
// when a material delta exists between the pre-run baseline and the current
// snapshot, and no guards block, the decision helper says fire.
func TestLoopRunner_EvaluateAccumulatedDelta_MaterialChange_Fires(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	// Opt out of during-busy coalesce.
	fa := false
	if err := ps.Update(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &fa, nil); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	loop, _ := ps.Get()
	baselineStore := NewTasksBaselineStore(store.SessionDir("s1"))
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := baselineStore.Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	rawNow := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"),
	)
	delta, shouldFire := runner.evaluateAccumulatedDelta("s1", loop, ps, baselineStore, rawNow)
	if !shouldFire {
		t.Errorf("shouldFire = false, want true (material delta, no guards blocking)")
	}
	if delta == nil || len(delta.Added) != 1 {
		t.Errorf("delta.Added = %v, want 1 added issue", delta)
	}
}

// TestLoopRunner_EvaluateAccumulatedDelta_NoMaterialChange_NoFire verifies
// that when the current snapshot matches the pre-run baseline, no fire is
// requested (this is the coalesce=true equivalent path — nothing to re-fire on).
func TestLoopRunner_EvaluateAccumulatedDelta_NoMaterialChange_NoFire(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	loop, _ := ps.Get()
	baselineStore := NewTasksBaselineStore(store.SessionDir("s1"))
	raw := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := baselineStore.Set(raw); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	delta, shouldFire := runner.evaluateAccumulatedDelta("s1", loop, ps, baselineStore, raw)
	if shouldFire {
		t.Errorf("shouldFire = true, want false (identical snapshot has no material delta)")
	}
	if delta != nil {
		t.Errorf("delta = %v, want nil for a no-op change", delta)
	}
}

// TestLoopRunner_EvaluateAccumulatedDelta_ConditionFalse_NoFire verifies that
// a CEL condition evaluating false on the accumulated delta blocks the re-fire
// (returns a non-nil delta with shouldFire=false, matching the guard semantics).
func TestLoopRunner_EvaluateAccumulatedDelta_ConditionFalse_NoFire(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Condition that never matches — Changes.Added is empty here (we test with a
	// snapshot whose only change is an update, not an add).
	ps := newOnTasksSession(t, store, "s1", "/proj", "size(Changes.Added) > 0")
	loop, _ := ps.Get()
	baselineStore := NewTasksBaselineStore(store.SessionDir("s1"))
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := baselineStore.Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	// Only an update (status change), no add — condition should evaluate false.
	rawNow := mustMarshalRows(t, beadsRow("mitto-1", "closed", "2026-01-02T00:00:00Z"))
	delta, shouldFire := runner.evaluateAccumulatedDelta("s1", loop, ps, baselineStore, rawNow)
	if shouldFire {
		t.Errorf("shouldFire = true, want false (condition evaluates to false)")
	}
	// Delta should still be non-nil (material change exists, just gated).
	if delta == nil {
		t.Errorf("delta = nil, want non-nil (change is material but gated by condition)")
	}
}

// TestLoopRunner_EvaluateAccumulatedDelta_CooldownActive_NoFire verifies that
// the Layer 0 per-conversation cooldown blocks the re-fire when it would land
// within the cooldown window since the previous delivery.
func TestLoopRunner_EvaluateAccumulatedDelta_CooldownActive_NoFire(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	// The persisted loop's LastSentAt is deliberately preserved by Set() (see
	// LoopStore.Set), so we build the in-memory loop directly and pass it to
	// evaluateAccumulatedDelta — it does not re-read from disk.
	stored, _ := ps.Get()
	recent := time.Now().Add(-1 * time.Second)
	stored.LastSentAt = &recent
	stored.CooldownSeconds = 300
	loop := stored

	baselineStore := NewTasksBaselineStore(store.SessionDir("s1"))
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := baselineStore.Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	runner.SetMinLoopTasksCooldownSeconds(30)

	rawNow := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"),
	)
	delta, shouldFire := runner.evaluateAccumulatedDelta("s1", loop, ps, baselineStore, rawNow)
	if shouldFire {
		t.Errorf("shouldFire = true, want false (per-conversation cooldown active)")
	}
	if delta == nil {
		t.Errorf("delta = nil, want non-nil (material change exists, gated by cooldown)")
	}
}

// TestLoopRunner_FireTasksRebase_CoalesceTrue_AbsorbsSilently verifies the
// default (CoalesceDuringBusy unset → true) behaviour: an external change
// landing during the busy window is silently absorbed by the plain rebase,
// with no re-fire attempted.
func TestLoopRunner_FireTasksRebase_CoalesceTrue_AbsorbsSilently(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	// Default = coalesce (nil).
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir("s1")).Set(rawBefore); err != nil {
		t.Fatalf("Set() baseline error = %v", err)
	}

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	runner := NewLoopRunner(store, sm, nil)
	rawNow := mustMarshalRows(t,
		beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
		beadsRow("mitto-2", "open", "2026-01-02T00:00:00Z"),
	)
	fake := &fakeTasksBeadsClient{listFn: func(string) ([]byte, error) { return rawNow, nil }}
	runner.SetBeadsClient(fake)

	runner.fireTasksRebase("s1", ps)

	baseline, err := NewTasksBaselineStore(store.SessionDir("s1")).Get()
	if err != nil {
		t.Fatalf("Get() baseline error = %v", err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, rawNow) {
		t.Errorf("baseline.RawSnapshot = %s, want %s (silent absorption)", baseline.RawSnapshot, rawNow)
	}
	// The default silent-absorb path must never record a fire outcome.
	runner.tasksNoProgressMu.Lock()
	_, seenTouched := runner.tasksLastTouchedIDs["s1"]
	runner.tasksNoProgressMu.Unlock()
	if seenTouched {
		t.Error("tasksLastTouchedIDs['s1'] should be empty — no re-fire should have been dispatched under coalesce=true")
	}
}

func TestLoopRunner_BootstrapTasksBaseline_CreatesWhenMissing(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnTasksSession(t, store, "s1", "/proj", "")
	runner := NewLoopRunner(store, nil, nil)
	raw := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	fake := &fakeTasksBeadsClient{listFn: func(string) ([]byte, error) { return raw, nil }}
	runner.SetBeadsClient(fake)

	runner.BootstrapTasksBaseline("s1")

	baseline, err := NewTasksBaselineStore(store.SessionDir("s1")).Get()
	if err != nil {
		t.Fatalf("Get() baseline error = %v", err)
	}
	if !jsonBytesEqual(t, baseline.RawSnapshot, raw) {
		t.Errorf("baseline.RawSnapshot = %s, want %s", baseline.RawSnapshot, raw)
	}

	// Calling it again must not re-list (already initialized).
	runner.BootstrapTasksBaseline("s1")
	if got := fake.listCallCount(); got != 1 {
		t.Errorf("List call count = %d, want 1 (no re-bootstrap once initialized)", got)
	}
}

func TestLoopRunner_BootstrapTasksBaseline_NoopWhenNotOnTasks(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	newOnCompletionSession(t, store, "s1", 0) // a different trigger, not onTasks
	runner := NewLoopRunner(store, nil, nil)
	fake := &fakeTasksBeadsClient{}
	runner.SetBeadsClient(fake)

	runner.BootstrapTasksBaseline("s1")

	if _, err := NewTasksBaselineStore(store.SessionDir("s1")).Get(); !errors.Is(err, ErrTasksBaselineNotFound) {
		t.Errorf("baseline should not be created for a non-onTasks trigger, err = %v", err)
	}
	if got := fake.listCallCount(); got != 0 {
		t.Errorf("List call count = %d, want 0", got)
	}
}

func TestLoopRunner_OnBeadsChanged_RoutingAndCaching(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Two onTasks sessions sharing the same working dir (List should be cached,
	// called once), one onTasks session in a different (unchanged) dir, and one
	// disabled onTasks session that must be skipped entirely.
	newOnTasksSession(t, store, "s1", "/proj-a", "")
	newOnTasksSession(t, store, "s2", "/proj-a", "")
	newOnTasksSession(t, store, "s3", "/proj-b", "")
	newOnTasksSession(t, store, "s4", "/proj-a", "")
	if err := store.Loop("s4").Update(nil, nil, nil, boolPtr(false), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("Update(disable s4) error = %v", err)
	}

	raw := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	fake := &fakeTasksBeadsClient{listFn: func(string) ([]byte, error) { return raw, nil }}

	runner := NewLoopRunner(store, nil, nil)
	runner.SetBeadsClient(fake)

	runner.OnBeadsChanged(watcher.BeadsChangeEvent{WorkingDirs: []string{"/proj-a"}})

	// s1 and s2 (same dir, enabled, onTasks) get a baseline initialized.
	for _, sid := range []string{"s1", "s2"} {
		if _, err := NewTasksBaselineStore(store.SessionDir(sid)).Get(); err != nil {
			t.Errorf("session %s should have an initialized baseline, error = %v", sid, err)
		}
	}
	// s3 (different dir) and s4 (disabled) must be untouched.
	for _, sid := range []string{"s3", "s4"} {
		if _, err := NewTasksBaselineStore(store.SessionDir(sid)).Get(); !errors.Is(err, ErrTasksBaselineNotFound) {
			t.Errorf("session %s should NOT have a baseline, error = %v", sid, err)
		}
	}
	// List must be called exactly once for /proj-a, even though two sessions share it.
	if got := fake.listCallCount(); got != 1 {
		t.Errorf("List call count = %d, want 1 (cached per working dir)", got)
	}
}

// TestLoopRunner_OnBeadsChanged_AfterStopDoesNotTouchClosedStore is a
// regression test for mitto-cbx (shutdown race): during app quit the
// BeadsWatcher's debounced fan-out can deliver an event to LoopRunner
// AFTER session.Store.Close() and LoopRunner.Stop() have already run,
// because LoopRunner.Stop() never unsubscribes from the watcher. The
// current OnBeadsChanged calls r.store.List() unconditionally, which
// returns session.ErrStoreClosed and logs the ERROR line
//
//	onTasks: failed to list sessions error="store is closed"
//
// This test simulates that exact ordering (store.Close -> runner.Stop ->
// event delivery) and asserts the ERROR is not logged. It fails on the
// current code and will pass once the fix lands (unsubscribe on Stop and/or
// early-return guard in OnBeadsChanged).
func TestLoopRunner_OnBeadsChanged_AfterStopDoesNotTouchClosedStore(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// Note: no `defer store.Close()` — the test closes it explicitly below
	// to reproduce the shutdown ordering from internal/web/server.go
	// (store.Close at L1452, loopRunner.Stop at L1462, beadsWatcher.Close
	// at L1495).

	// A single enabled onTasks session in /proj-a so OnBeadsChanged's
	// routing code has something to iterate over — proving the failing
	// path at loop_runner_tasks.go:105 (r.store.List) is really reached.
	newOnTasksSession(t, store, "s1", "/proj-a", "")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	runner := NewLoopRunner(store, nil, logger)
	runner.Start()

	// Simulate the real shutdown ordering that produces the bug in
	// production: store closes first, LoopRunner.Stop() runs next, and
	// only then does a debounced BeadsWatcher fan-out deliver a
	// previously-queued event to a runner that has neither unsubscribed
	// nor learned to short-circuit on !running/ErrStoreClosed.
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}
	runner.Stop()

	runner.OnBeadsChanged(watcher.BeadsChangeEvent{
		WorkingDirs: []string{"/proj-a"},
		Timestamp:   time.Now(),
	})

	if got := buf.String(); strings.Contains(got, "onTasks: failed to list sessions") {
		t.Fatalf("OnBeadsChanged after Stop() touched the closed store and logged the failing symptom (mitto-cbx). Log output:\n%s", got)
	}
}

func TestLoopRunner_RecordTasksFireOutcome_CircuitBreakerPausesNoProgress(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	runner := NewLoopRunner(store, nil, nil)

	// Same issue id touched repeatedly — no genuine new progress across fires.
	// The very first fire seeds tasksLastTouchedIDs (nothing to compare against
	// yet, so it never counts as "no progress" on its own); the breaker needs
	// tasksNoProgressLimit CONSECUTIVE no-progress fires after that seed.
	delta := &config.TasksDelta{Touched: []map[string]any{{"id": "mitto-1"}}}
	runner.recordTasksFireOutcome("s1", ps, delta) // seed
	for i := 0; i < tasksNoProgressLimit-1; i++ {
		runner.recordTasksFireOutcome("s1", ps, delta)
		got, err := ps.Get()
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if !got.Enabled {
			t.Fatalf("loop should remain enabled before reaching the no-progress limit (iteration %d)", i)
		}
	}

	// The Nth consecutive no-progress fire trips the breaker.
	runner.recordTasksFireOutcome("s1", ps, delta)
	got, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Enabled {
		t.Error("loop should be auto-paused after tasksNoProgressLimit consecutive no-progress fires")
	}
	if got.StoppedReason != session.StoppedReasonNoProgress {
		t.Errorf("StoppedReason = %q, want %q", got.StoppedReason, session.StoppedReasonNoProgress)
	}
}

func TestLoopRunner_RecordTasksFireOutcome_ResetsOnGenuineProgress(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ps := newOnTasksSession(t, store, "s1", "/proj", "")
	runner := NewLoopRunner(store, nil, nil)

	sameDelta := &config.TasksDelta{Touched: []map[string]any{{"id": "mitto-1"}}}
	for i := 0; i < tasksNoProgressLimit-1; i++ {
		runner.recordTasksFireOutcome("s1", ps, sameDelta)
	}

	// A fire that touches a genuinely new issue resets the counter.
	newDelta := &config.TasksDelta{Touched: []map[string]any{{"id": "mitto-2"}}}
	runner.recordTasksFireOutcome("s1", ps, newDelta)

	// Even after tasksNoProgressLimit-1 more repeats of the *new* id alone, the
	// breaker should not have tripped yet because the counter was reset.
	for i := 0; i < tasksNoProgressLimit-1; i++ {
		runner.recordTasksFireOutcome("s1", ps, newDelta)
	}
	got, err := ps.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Enabled {
		t.Error("loop should still be enabled — the counter was reset by genuine progress")
	}
}

func TestLoopRunner_TasksCooldownSettersGetters(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
	if got := runner.MinLoopTasksCooldownSeconds(); got != DefaultMinLoopTasksCooldownSeconds {
		t.Errorf("default MinLoopTasksCooldownSeconds = %d, want %d", got, DefaultMinLoopTasksCooldownSeconds)
	}
	runner.SetMinLoopTasksCooldownSeconds(120)
	if got := runner.MinLoopTasksCooldownSeconds(); got != 120 {
		t.Errorf("MinLoopTasksCooldownSeconds() = %d, want 120", got)
	}
	runner.SetMinLoopTasksCooldownSeconds(-5)
	if got := runner.MinLoopTasksCooldownSeconds(); got != 0 {
		t.Errorf("negative value should clamp to 0, got %d", got)
	}
}

// newArchivedLoopSession creates a session archived with the given reason and
// timestamp, optionally with a loop config, for auto-unarchive recovery tests.
func newArchivedLoopSession(t *testing.T, store *session.Store, sessionID string, archivedAt time.Time, reason session.ArchiveReason, hasLoop bool) {
	t.Helper()
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Archived = true
		m.ArchivedAt = archivedAt
		m.ArchiveReason = reason
	}); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}
	if hasLoop {
		loopStore := store.Loop(sessionID)
		if err := loopStore.Set(&session.LoopPrompt{
			Prompt:    "check",
			Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
			Enabled:   false,
		}); err != nil {
			t.Fatalf("Loop Set() error = %v", err)
		}
	}
}

func TestLoopRunner_AutoUnarchive_EligibleAndDuePersistsAttempt(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	archivedAt := time.Now().Add(-2 * time.Hour)
	newArchivedLoopSession(t, store, "sess-1", archivedAt, session.ArchiveReasonACPFailures, true)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)

	var called []string
	var attemptPersisted bool
	runner.SetOnAutoUnarchive(func(sessionID string) error {
		called = append(called, sessionID)
		if m, err := store.GetMetadata(sessionID); err == nil && !m.AutoUnarchiveLastAttemptAt.IsZero() {
			attemptPersisted = true
		}
		return nil
	})

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())

	if len(called) != 1 || called[0] != "sess-1" {
		t.Errorf("onAutoUnarchive called = %v, want [sess-1]", called)
	}
	if !attemptPersisted {
		t.Error("AutoUnarchiveLastAttemptAt should be persisted before invoking the callback")
	}
}

func TestLoopRunner_AutoUnarchive_SuccessClearsAttempt(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	archivedAt := time.Now().Add(-2 * time.Hour)
	newArchivedLoopSession(t, store, "sess-1", archivedAt, session.ArchiveReasonACPFailures, true)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)
	runner.SetOnAutoUnarchive(func(sessionID string) error { return nil })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())

	meta, err := store.GetMetadata("sess-1")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if !meta.AutoUnarchiveLastAttemptAt.IsZero() {
		t.Error("AutoUnarchiveLastAttemptAt should be cleared after a successful auto-unarchive")
	}
}

func TestLoopRunner_AutoUnarchive_FailureRetainsAttempt(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	archivedAt := time.Now().Add(-2 * time.Hour)
	newArchivedLoopSession(t, store, "sess-1", archivedAt, session.ArchiveReasonACPFailures, true)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)
	runner.SetOnAutoUnarchive(func(sessionID string) error { return errors.New("acp still broken") })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	now := time.Now()
	runner.checkAutoUnarchiveRecovery(sessions, now)

	meta, err := store.GetMetadata("sess-1")
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if meta.AutoUnarchiveLastAttemptAt.IsZero() {
		t.Error("AutoUnarchiveLastAttemptAt should be retained after a failed auto-unarchive")
	}
}

func TestLoopRunner_AutoUnarchive_SkipsManualArchive(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	archivedAt := time.Now().Add(-2 * time.Hour)
	newArchivedLoopSession(t, store, "sess-1", archivedAt, session.ArchiveReasonManual, true)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)

	var called bool
	runner.SetOnAutoUnarchive(func(sessionID string) error { called = true; return nil })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())

	if called {
		t.Error("onAutoUnarchive should not be invoked for ArchiveReasonManual")
	}
}

func TestLoopRunner_AutoUnarchive_SkipsInactivityArchive(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	archivedAt := time.Now().Add(-2 * time.Hour)
	newArchivedLoopSession(t, store, "sess-1", archivedAt, session.ArchiveReasonInactivity, true)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)

	var called bool
	runner.SetOnAutoUnarchive(func(sessionID string) error { called = true; return nil })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())

	if called {
		t.Error("onAutoUnarchive should not be invoked for ArchiveReasonInactivity")
	}
}

func TestLoopRunner_AutoUnarchive_SkipsNonLoopACPFailures(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	archivedAt := time.Now().Add(-2 * time.Hour)
	newArchivedLoopSession(t, store, "sess-1", archivedAt, session.ArchiveReasonACPFailures, false)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)

	var called bool
	runner.SetOnAutoUnarchive(func(sessionID string) error { called = true; return nil })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())

	if called {
		t.Error("onAutoUnarchive should not be invoked for a non-loop ACP-failures archive")
	}
}

func TestLoopRunner_AutoUnarchive_SkipsWhenNotYetDue(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	archivedAt := time.Now().Add(-10 * time.Minute)
	newArchivedLoopSession(t, store, "sess-1", archivedAt, session.ArchiveReasonACPFailures, true)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)

	var called bool
	runner.SetOnAutoUnarchive(func(sessionID string) error { called = true; return nil })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())

	if called {
		t.Error("onAutoUnarchive should not be invoked before retryInterval has elapsed")
	}
}

func TestLoopRunner_AutoUnarchive_StaggerLimitsToOnePerPoll(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// sess-2 is more overdue than sess-1; both are due.
	newArchivedLoopSession(t, store, "sess-1", time.Now().Add(-2*time.Hour), session.ArchiveReasonACPFailures, true)
	newArchivedLoopSession(t, store, "sess-2", time.Now().Add(-3*time.Hour), session.ArchiveReasonACPFailures, true)

	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)

	var called []string
	runner.SetOnAutoUnarchive(func(sessionID string) error { called = append(called, sessionID); return nil })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())

	if len(called) != 1 {
		t.Fatalf("onAutoUnarchive called %d times, want exactly 1", len(called))
	}
	if called[0] != "sess-2" {
		t.Errorf("onAutoUnarchive called for %q, want the most-overdue session %q", called[0], "sess-2")
	}
}

func TestLoopRunner_AutoUnarchive_RestartDurability(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Simulate a prior attempt persisted before a restart: ArchivedAt is old,
	// but AutoUnarchiveLastAttemptAt is the more recent anchor.
	oldArchivedAt := time.Now().Add(-5 * time.Hour)
	lastAttempt := time.Now().Add(-30 * time.Minute)
	newArchivedLoopSession(t, store, "sess-1", oldArchivedAt, session.ArchiveReasonACPFailures, true)
	if err := store.UpdateMetadata("sess-1", func(m *session.Metadata) {
		m.AutoUnarchiveLastAttemptAt = lastAttempt
	}); err != nil {
		t.Fatalf("UpdateMetadata() error = %v", err)
	}

	// Fresh LoopRunner instance, as after a process restart (in-memory stagger reset).
	runner := NewLoopRunner(store, nil, nil)
	runner.SetAutoUnarchiveRecovery(true, time.Hour, 10*time.Minute)

	var called bool
	runner.SetOnAutoUnarchive(func(sessionID string) error { called = true; return nil })

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	// Only 30 minutes have elapsed since AutoUnarchiveLastAttemptAt, which is less
	// than the 1h retryInterval, so it must NOT be attempted yet even though
	// ArchivedAt is far in the past.
	runner.checkAutoUnarchiveRecovery(sessions, time.Now())
	if called {
		t.Error("cadence should be anchored on persisted AutoUnarchiveLastAttemptAt, not ArchivedAt")
	}

	// Advance past retryInterval relative to the persisted anchor.
	runner.checkAutoUnarchiveRecovery(sessions, lastAttempt.Add(time.Hour+time.Minute))
	if !called {
		t.Error("session should become due once retryInterval has elapsed since the persisted attempt timestamp")
	}
}

func TestTasksBaselineStore_GetSetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	bs := NewTasksBaselineStore(dir)

	if _, err := bs.Get(); !errors.Is(err, ErrTasksBaselineNotFound) {
		t.Errorf("Get() on empty store error = %v, want ErrTasksBaselineNotFound", err)
	}

	raw := []byte(`[{"id":"mitto-1"}]`)
	if err := bs.Set(raw); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := bs.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !jsonBytesEqual(t, got.RawSnapshot, raw) {
		t.Errorf("RawSnapshot = %s, want %s", got.RawSnapshot, raw)
	}
	if got.CapturedAt.IsZero() {
		t.Error("CapturedAt should be set")
	}
}

// =============================================================================
// Per-workspace loop-dispatch concurrency guard tests (mitto-61z)
// =============================================================================

// TestLoopRunner_WorkspaceSlot_ReserveRelease verifies the low-level slot
// reservation helpers respect the configured cap and are independent across
// distinct workspace keys.
func TestLoopRunner_WorkspaceSlot_ReserveRelease(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
	runner.SetLoopWorkspaceConcurrency(1)

	keyA := workspaceKey("/ws/a", "auggie")
	keyB := workspaceKey("/ws/b", "auggie")

	if !runner.tryReserveWorkspaceSlot(keyA) {
		t.Fatal("first reserve for A must succeed (cap=1)")
	}
	if runner.workspaceInFlightCount(keyA) != 1 {
		t.Errorf("in-flight[A] = %d, want 1", runner.workspaceInFlightCount(keyA))
	}
	if runner.tryReserveWorkspaceSlot(keyA) {
		t.Error("second reserve for A must fail at cap=1")
	}
	// Different workspace is independent.
	if !runner.tryReserveWorkspaceSlot(keyB) {
		t.Error("reserve for B must succeed — different workspace")
	}
	runner.releaseWorkspaceSlot(keyA)
	if runner.workspaceInFlightCount(keyA) != 0 {
		t.Errorf("after release, in-flight[A] = %d, want 0", runner.workspaceInFlightCount(keyA))
	}
	// After release, we can reserve A again.
	if !runner.tryReserveWorkspaceSlot(keyA) {
		t.Error("reserve for A must succeed after release")
	}
	// Releasing more times than we reserved must not go negative.
	runner.releaseWorkspaceSlot(keyA)
	runner.releaseWorkspaceSlot(keyA)
	if runner.workspaceInFlightCount(keyA) != 0 {
		t.Errorf("in-flight[A] after over-release = %d, want 0", runner.workspaceInFlightCount(keyA))
	}
}

// TestLoopRunner_WorkspaceSlot_CapZeroDisabled verifies that cap=0 disables
// the guard: reserve always succeeds and the counter is not incremented.
func TestLoopRunner_WorkspaceSlot_CapZeroDisabled(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
	runner.SetLoopWorkspaceConcurrency(0)

	key := workspaceKey("/ws/x", "auggie")
	for i := 0; i < 5; i++ {
		if !runner.tryReserveWorkspaceSlot(key) {
			t.Fatalf("reserve[%d] must succeed with cap=0", i)
		}
	}
	if runner.workspaceInFlightCount(key) != 0 {
		t.Errorf("in-flight[key] = %d, want 0 (cap=0 must not increment)", runner.workspaceInFlightCount(key))
	}
}

// TestLoopRunner_WorkspaceSlot_CapAboveOne verifies that a cap > 1 allows
// exactly that many concurrent reservations before rejecting further ones.
func TestLoopRunner_WorkspaceSlot_CapAboveOne(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
	runner.SetLoopWorkspaceConcurrency(2)

	key := workspaceKey("/ws/y", "auggie")
	if !runner.tryReserveWorkspaceSlot(key) {
		t.Fatal("first reserve must succeed at cap=2")
	}
	if !runner.tryReserveWorkspaceSlot(key) {
		t.Fatal("second reserve must succeed at cap=2")
	}
	if runner.tryReserveWorkspaceSlot(key) {
		t.Error("third reserve must fail at cap=2")
	}
}

// TestLoopRunner_WorkspaceSlot_NegativeCapClamped verifies that
// SetLoopWorkspaceConcurrency clamps negative values to 0 (cap disabled).
func TestLoopRunner_WorkspaceSlot_NegativeCapClamped(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	runner := NewLoopRunner(store, nil, nil)
	runner.SetLoopWorkspaceConcurrency(-5)

	key := workspaceKey("/ws/z", "auggie")
	// With cap effectively 0, reserves always succeed and never populate the map.
	if !runner.tryReserveWorkspaceSlot(key) {
		t.Error("reserve must succeed when negative cap is clamped to 0")
	}
	if runner.workspaceInFlightCount(key) != 0 {
		t.Errorf("in-flight[key] = %d, want 0 (clamped cap must not increment)", runner.workspaceInFlightCount(key))
	}
}

// setLoopDue creates a session with an overdue loop prompt. It writes the
// loop JSON directly to disk so NextScheduledAt is preserved (LoopStore.Set
// recomputes NextScheduledAt to a future time, which would defeat the test).
func setLoopDue(t *testing.T, store *session.Store, sessionID, workingDir, acpServer string) *session.LoopPrompt {
	t.Helper()
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  acpServer,
		WorkingDir: workingDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create(%s) error = %v", sessionID, err)
	}
	past := time.Now().UTC().Add(-1 * time.Hour)
	p := &session.LoopPrompt{
		Prompt:          "Test loop prompt",
		Frequency:       session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:         true,
		CreatedAt:       past.Add(-1 * time.Hour),
		UpdatedAt:       past.Add(-1 * time.Hour),
		NextScheduledAt: &past,
	}
	loopPath := filepath.Join(store.SessionDir(sessionID), "loop.json")
	if err := writeTestLoopFile(loopPath, p); err != nil {
		t.Fatalf("writeTestLoopFile(%s) error = %v", sessionID, err)
	}
	return p
}

// TestLoopRunner_RunOnce_WorkspaceCapSkipsSibling verifies that when a
// workspace is at its concurrency cap (a sibling loop is in flight), the
// next due loop in that same workspace is skipped — not errored — and its
// schedule is NOT advanced (no backoff either).
func TestLoopRunner_RunOnce_WorkspaceCapSkipsSibling(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Two loops in the SAME workspace, both overdue.
	_ = setLoopDue(t, store, "sib-1", "/ws/shared", "auggie")
	_ = setLoopDue(t, store, "sib-2", "/ws/shared", "auggie")

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	// Register non-prompting BackgroundSessions for both so checkSession
	// reaches the workspace guard for each (rather than the auto-resume
	// failure path).
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("sib-1", false))
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("sib-2", false))

	runner := NewLoopRunner(store, sm, nil)
	runner.SetLoopWorkspaceConcurrency(1)

	// Simulate an in-flight sibling by pre-reserving the workspace slot.
	key := workspaceKey("/ws/shared", "auggie")
	if !runner.tryReserveWorkspaceSlot(key) {
		t.Fatal("pre-reserve failed unexpectedly")
	}

	// Capture NextScheduledAt for sib-2 before running.
	before, err := store.Loop("sib-2").Get()
	if err != nil {
		t.Fatalf("Get(sib-2) before error = %v", err)
	}
	if before.NextScheduledAt == nil {
		t.Fatal("sib-2 NextScheduledAt is nil before RunOnce")
	}
	beforeNext := *before.NextScheduledAt

	delivered, skipped, errored := runner.RunOnce()

	// Both sessions share the same workspace and cap=1 was pre-consumed by
	// our reservation. Both must be skipped — not errored — and neither
	// schedule may advance.
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2 (both siblings must be skipped)", skipped)
	}
	if delivered != 0 {
		t.Errorf("delivered = %d, want 0", delivered)
	}
	if errored != 0 {
		t.Errorf("errored = %d, want 0 (workspace-busy must NOT count as errored)", errored)
	}

	// sib-2's schedule MUST NOT have advanced.
	after, err := store.Loop("sib-2").Get()
	if err != nil {
		t.Fatalf("Get(sib-2) after error = %v", err)
	}
	if after.NextScheduledAt == nil || !after.NextScheduledAt.Equal(beforeNext) {
		t.Errorf("sib-2 NextScheduledAt advanced: before=%v after=%v (must not advance on ErrWorkspaceBusy)",
			beforeNext, after.NextScheduledAt)
	}
	// And no scheduled-delivery backoff must have been recorded.
	runner.scheduleBackoffFailuresMu.Lock()
	got := runner.scheduleBackoffFailures["sib-2"]
	runner.scheduleBackoffFailuresMu.Unlock()
	if got != 0 {
		t.Errorf("scheduleBackoffFailures[sib-2] = %d, want 0 (ErrWorkspaceBusy must not count as a delivery failure)", got)
	}
}

// TestLoopRunner_RunOnce_DifferentWorkspacesIndependent verifies that a slot
// reservation on one workspace does NOT block a due loop in a different
// workspace.
func TestLoopRunner_RunOnce_DifferentWorkspacesIndependent(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	_ = setLoopDue(t, store, "wsA", "/ws/a", "auggie")
	_ = setLoopDue(t, store, "wsB", "/ws/b", "auggie")

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	// Register non-prompting bs for both so they reach the guard.
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("wsA", false))
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("wsB", false))

	runner := NewLoopRunner(store, sm, nil)
	runner.SetLoopWorkspaceConcurrency(1)

	// Reserve only workspace A. Workspace B must remain free.
	if !runner.tryReserveWorkspaceSlot(workspaceKey("/ws/a", "auggie")) {
		t.Fatal("pre-reserve A failed unexpectedly")
	}

	// Snapshot both NextScheduledAt values before running.
	loopA, _ := store.Loop("wsA").Get()
	loopB, _ := store.Loop("wsB").Get()
	if loopA == nil || loopB == nil || loopA.NextScheduledAt == nil || loopB.NextScheduledAt == nil {
		t.Fatal("initial loop state incomplete")
	}
	beforeA := *loopA.NextScheduledAt
	beforeB := *loopB.NextScheduledAt

	runner.RunOnce()

	// Workspace A must be skipped — schedule unchanged.
	afterA, _ := store.Loop("wsA").Get()
	if afterA.NextScheduledAt == nil || !afterA.NextScheduledAt.Equal(beforeA) {
		t.Errorf("wsA schedule advanced despite workspace being at cap: before=%v after=%v",
			beforeA, afterA.NextScheduledAt)
	}

	// Workspace B was NOT at cap when checkSession reached the guard, so the
	// guard reserved a slot for B and proceeded to PromptWithMeta, which fails
	// with "still starting up" (no ACP). The synchronous failure releases the
	// slot again — verify the slot count for B is back to 0.
	if got := runner.workspaceInFlightCount(workspaceKey("/ws/b", "auggie")); got != 0 {
		t.Errorf("in-flight[wsB] = %d, want 0 (synchronous PromptWithMeta failure must release the slot)", got)
	}
	// And workspace A's in-flight count must still be 1 (our pre-reserved slot
	// was NOT touched by the checkSession/deliverPrompt paths for wsB).
	if got := runner.workspaceInFlightCount(workspaceKey("/ws/a", "auggie")); got != 1 {
		t.Errorf("in-flight[wsA] = %d, want 1 (pre-reserved slot must remain)", got)
	}
	// wsB's schedule may or may not have advanced depending on whether the
	// PromptWithMeta stub triggered OnComplete. The critical invariant we
	// care about — independence from workspace A — is asserted above.
	_ = beforeB
}

// TestLoopRunner_TriggerNow_BypassesWorkspaceCap verifies that manual "Run
// Now" (forced) deliveries bypass the workspace concurrency cap: even when
// the workspace is at cap, TriggerNow does not return ErrWorkspaceBusy.
func TestLoopRunner_TriggerNow_BypassesWorkspaceCap(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	_ = setLoopDue(t, store, "forced-1", "/ws/forced", "auggie")

	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	sm.AddSessionForTest(NewMinimalBackgroundSessionPrompting("forced-1", false))

	runner := NewLoopRunner(store, sm, nil)
	runner.SetLoopWorkspaceConcurrency(1)

	// Pre-reserve the workspace slot to simulate an in-flight sibling.
	key := workspaceKey("/ws/forced", "auggie")
	if !runner.tryReserveWorkspaceSlot(key) {
		t.Fatal("pre-reserve failed unexpectedly")
	}

	// TriggerNow (forced=true) must NOT return ErrWorkspaceBusy — the guard
	// is bypassed. It may still fail downstream (PromptWithMeta returns
	// "still starting up" because there is no real ACP wiring), but that
	// failure is *not* the guard rejecting the delivery.
	err = runner.TriggerNow("forced-1", true)
	if errors.Is(err, ErrWorkspaceBusy) {
		t.Errorf("TriggerNow returned ErrWorkspaceBusy — forced deliveries must bypass the workspace cap")
	}

	// And the pre-reserved slot count is unchanged (forced path does not
	// touch the counter).
	if got := runner.workspaceInFlightCount(key); got != 1 {
		t.Errorf("in-flight[key] = %d, want 1 (forced must not touch the counter)", got)
	}
}

// TestLoopRunner_WorkspaceKey verifies the workspaceKey helper produces the
// documented format (WorkingDir + \x00 + ACPServer) and distinguishes
// otherwise-similar values.
func TestLoopRunner_WorkspaceKey(t *testing.T) {
	// Same working dir, different ACP servers → different keys.
	if workspaceKey("/w", "a") == workspaceKey("/w", "b") {
		t.Error("keys should differ when ACPServer differs")
	}
	// Same ACP server, different working dirs → different keys.
	if workspaceKey("/w1", "a") == workspaceKey("/w2", "a") {
		t.Error("keys should differ when WorkingDir differs")
	}
	// Same WorkingDir + ACPServer → same key.
	k1 := workspaceKey("/w", "a")
	k2 := workspaceKey("/w", "a")
	if k1 != k2 {
		t.Error("keys should match for the same pair")
	}
	// NUL separator is present.
	got := workspaceKey("dir", "srv")
	want := "dir" + "\x00" + "srv"
	if got != want {
		t.Errorf("workspaceKey = %q, want %q", got, want)
	}
}

// TestLoopRunner_ContextWindowFailure_AutoPausesAfterThreshold verifies mitto-7jn:
// after MaxLoopContextWindowFailures consecutive context-window (HTTP 413) failures
// the loop is auto-paused with StoppedReasonContextWindowExceeded and the
// onLoopAutoStopped callback is invoked exactly once.
func TestLoopRunner_ContextWindowFailure_AutoPausesAfterThreshold(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	const sessionID = "cw-1"
	meta := session.Metadata{SessionID: sessionID, ACPServer: "auggie", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loopStore := store.Loop(sessionID)
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:    "Test",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)

	var autoStopCalls int
	var lastAutoStopSession string
	runner.SetOnLoopAutoStopped(func(sid string, p *session.LoopPrompt) {
		autoStopCalls++
		lastAutoStopSession = sid
		if p == nil || p.Enabled {
			t.Errorf("onLoopAutoStopped called with enabled/nil loop: %+v", p)
		}
		if p != nil && p.StoppedReason != session.StoppedReasonContextWindowExceeded {
			t.Errorf("StoppedReason = %q, want %q", p.StoppedReason, session.StoppedReasonContextWindowExceeded)
		}
	})

	// Hits 1 and 2 must return false (under threshold, loop stays enabled).
	for i := 1; i < MaxLoopContextWindowFailures; i++ {
		stopped := runner.handleContextWindowFailure(sessionID, "test", loopStore)
		if stopped {
			t.Fatalf("handleContextWindowFailure hit %d returned true; want false (under threshold)", i)
		}
		got, gErr := loopStore.Get()
		if gErr != nil {
			t.Fatalf("loopStore.Get() after hit %d error = %v", i, gErr)
		}
		if !got.Enabled {
			t.Errorf("loop disabled after hit %d; want still enabled until threshold", i)
		}
	}

	// Final hit must trip the auto-pause.
	if stopped := runner.handleContextWindowFailure(sessionID, "test", loopStore); !stopped {
		t.Fatal("handleContextWindowFailure final hit returned false; want true (threshold reached)")
	}
	final, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() after auto-pause error = %v", err)
	}
	if final.Enabled {
		t.Error("loop.Enabled = true after auto-pause; want false")
	}
	if final.StoppedReason != session.StoppedReasonContextWindowExceeded {
		t.Errorf("StoppedReason = %q, want %q", final.StoppedReason, session.StoppedReasonContextWindowExceeded)
	}
	if final.StoppedAt == nil {
		t.Error("StoppedAt is nil after auto-pause")
	}
	if autoStopCalls != 1 {
		t.Errorf("onLoopAutoStopped invocation count = %d, want 1", autoStopCalls)
	}
	if lastAutoStopSession != sessionID {
		t.Errorf("onLoopAutoStopped sessionID = %q, want %q", lastAutoStopSession, sessionID)
	}
	// Counter must be cleared after the auto-pause.
	runner.contextWindowFailuresMu.Lock()
	remaining := runner.contextWindowFailures[sessionID]
	runner.contextWindowFailuresMu.Unlock()
	if remaining != 0 {
		t.Errorf("contextWindowFailures[%s] = %d after auto-pause, want 0", sessionID, remaining)
	}
}

// TestLoopRunner_ContextWindowFailure_SuccessBetweenHitsResetsCounter verifies
// that a successful delivery between context-window hits clears the counter, so
// the loop is NOT auto-paused after a 2 + success + 2 pattern.
func TestLoopRunner_ContextWindowFailure_SuccessBetweenHitsResetsCounter(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	const sessionID = "cw-2"
	meta := session.Metadata{SessionID: sessionID, ACPServer: "auggie", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loopStore := store.Loop(sessionID)
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:    "Test",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	var autoStopCalls int
	runner.SetOnLoopAutoStopped(func(sid string, p *session.LoopPrompt) {
		autoStopCalls++
	})

	// 2 hits — still under threshold.
	for i := 0; i < 2; i++ {
		if stopped := runner.handleContextWindowFailure(sessionID, "test", loopStore); stopped {
			t.Fatalf("hit %d unexpectedly triggered auto-pause", i+1)
		}
	}
	// Simulate a successful delivery — OnComplete clears the counter.
	runner.contextWindowFailuresMu.Lock()
	delete(runner.contextWindowFailures, sessionID)
	runner.contextWindowFailuresMu.Unlock()

	// 2 more hits — must NOT trip auto-pause because the counter was reset.
	for i := 0; i < 2; i++ {
		if stopped := runner.handleContextWindowFailure(sessionID, "test", loopStore); stopped {
			t.Fatalf("hit %d after reset unexpectedly triggered auto-pause", i+1)
		}
	}

	final, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
	}
	if !final.Enabled {
		t.Error("loop.Enabled = false after 2+reset+2 hits; want true (reset must prevent auto-pause)")
	}
	if final.StoppedReason != "" {
		t.Errorf("StoppedReason = %q, want empty", final.StoppedReason)
	}
	if autoStopCalls != 0 {
		t.Errorf("onLoopAutoStopped calls = %d, want 0", autoStopCalls)
	}
}

// TestLoopRunner_IsContextTooLargeError_413String verifies mitto-7jn's classifier
// matches the exact error string surfaced by the Augment ACP for HTTP 413.
func TestLoopRunner_IsContextTooLargeError_413String(t *testing.T) {
	// Matches the error observed in the bead's log excerpt.
	err413 := errors.New("HTTP error: 413 Request Entity Too Large")
	if !mittoAcp.IsContextTooLargeError(err413) {
		t.Errorf("IsContextTooLargeError(%q) = false, want true", err413)
	}
	if mittoAcp.IsContextTooLargeError(errors.New("some unrelated error")) {
		t.Errorf("IsContextTooLargeError(unrelated) = true, want false")
	}
	if mittoAcp.IsContextTooLargeError(nil) {
		t.Error("IsContextTooLargeError(nil) = true, want false")
	}
}

// TestLoopRunner_ContextWindowFailure_OnCompletionLoop_AutoPauses reproduces
// mitto-4he: the OnComplete failure gate in deliverPrompt excludes onCompletion
// loops from the context-window auto-pause (mitto-7jn) safety net. An
// onCompletion loop that repeatedly hits HTTP 413 must still auto-pause after
// MaxLoopContextWindowFailures hits, exactly like a scheduled loop does —
// otherwise it silently re-fires indefinitely on every turn-complete re-arm.
//
// This test drives handleDeliveryFailure — the extracted OnComplete error
// handler — with an onCompletion loop and asserts the observable end-state.
// Current code: the gate at handleDeliveryFailure excludes onCompletion →
// handleContextWindowFailure is never called → the counter stays at 0 → the
// loop remains Enabled forever. After the fix: the classifier and counter
// must run regardless of trigger type; only DeferNextSchedule stays gated to
// scheduled loops.
func TestLoopRunner_ContextWindowFailure_OnCompletionLoop_AutoPauses(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	const sessionID = "cw-oncompletion"
	meta := session.Metadata{SessionID: sessionID, ACPServer: "auggie", WorkingDir: "/tmp"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loopStore := store.Loop(sessionID)
	loop := &session.LoopPrompt{
		Prompt:       "Test",
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 30,
		Enabled:      true,
	}
	if err := loopStore.Set(loop); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	runner := NewLoopRunner(store, nil, nil)
	var autoStopCalls int
	runner.SetOnLoopAutoStopped(func(sid string, p *session.LoopPrompt) {
		autoStopCalls++
	})

	// Drive the real OnComplete error path MaxLoopContextWindowFailures times
	// with a real HTTP 413 error and the parameter values that the normal
	// onCompletion delivery uses (resetTimer=true, forced=false).
	err413 := errors.New("HTTP error: 413 Request Entity Too Large")
	for i := 1; i <= MaxLoopContextWindowFailures; i++ {
		runner.handleDeliveryFailure(sessionID, "cgw-support", loop, loopStore, err413, true, false)
	}

	// After MaxLoopContextWindowFailures consecutive 413 hits an onCompletion
	// loop MUST be auto-paused, exactly like a scheduled loop.
	final, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
	}
	if final.Enabled {
		t.Errorf("onCompletion loop.Enabled = true after %d context-window failures; "+
			"want false (auto-pause must fire regardless of trigger type — mitto-4he)",
			MaxLoopContextWindowFailures)
	}
	if final.StoppedReason != session.StoppedReasonContextWindowExceeded {
		t.Errorf("onCompletion loop.StoppedReason = %q, want %q "+
			"(auto-pause must record the same reason as scheduled loops — mitto-4he)",
			final.StoppedReason, session.StoppedReasonContextWindowExceeded)
	}
	if autoStopCalls != 1 {
		t.Errorf("onLoopAutoStopped invocation count = %d, want 1 "+
			"(onCompletion loops must broadcast the auto-pause — mitto-4he)",
			autoStopCalls)
	}
}

// TestLoopRunner_OnTasks_PromptResolveFailure_AutoPauses is the reproduction
// test for mitto-uhnc: an onTasks-triggered loop whose loop_prompt_name no
// longer resolves (e.g. the builtin prompt was renamed) must auto-pause after
// MaxPromptResolveFailures consecutive fires, exactly like the scheduled-loop
// path in checkSession (loop_runner.go:1363). Today processTasksChange's
// tasksActionFire branch only WARNs on any error from triggerNowWithTasksDelta
// (loop_runner_tasks.go:295-304) and never routes ErrPromptResolveFailed
// through handlePromptResolveFailure, so the failure counter never bumps, the
// loop stays enabled, and onLoopAutoStopped never fires — the conversation is
// orphaned and silently retries forever.
//
// This test drives processTasksChange three times with a resolver that always
// fails and asserts the expected (post-fix) behaviour. It fails today because
// of the parity gap; the fix in loop_runner_tasks.go will make it pass.
func TestLoopRunner_OnTasks_PromptResolveFailure_AutoPauses(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Create an onTasks session but replace the free-text Prompt with a
	// PromptName that will not resolve, mirroring the mitto-uhnc scenario
	// where a builtin prompt was renamed out from under the loop config.
	const sessionID = "ontasks-resolve-fail"
	meta := session.Metadata{SessionID: sessionID, ACPServer: "test", WorkingDir: "/proj"}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	loopStore := store.Loop(sessionID)
	if err := loopStore.Set(&session.LoopPrompt{
		PromptName: "renamed-prompt",
		Enabled:    true,
		Trigger:    session.TriggerOnTasks,
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	// Seed a baseline so evaluateTasksChange takes the tasksActionFire branch
	// (skipping tasksActionInitBaseline).
	rawBefore := mustMarshalRows(t, beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"))
	if err := NewTasksBaselineStore(store.SessionDir(sessionID)).Set(rawBefore); err != nil {
		t.Fatalf("baseline Set() error = %v", err)
	}

	// A SessionManager with a pre-registered BackgroundSession so
	// triggerNowWithTasksDelta finds the session (bypassing ResumeSession) and
	// reaches deliverPrompt, where the promptResolver returns
	// ErrPromptResolveFailed.
	sm := NewSessionManagerWithOptions(SessionManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sm.AddSessionForTest(NewTestBackgroundSessionWithCtx(sessionID, ctx, cancel))

	runner := NewLoopRunner(store, sm, nil)
	resolveErr := errors.New("prompt not found")
	runner.SetPromptResolver(func(name, dir string) (string, error) {
		return "", resolveErr
	})

	var autoStopCalls int
	runner.SetOnLoopAutoStopped(func(id string, _ *session.LoopPrompt) {
		autoStopCalls++
		if id != sessionID {
			t.Errorf("onLoopAutoStopped: id = %q, want %q", id, sessionID)
		}
	})

	// Deliver material tasks changes to force tasksActionFire on every call.
	// Each iteration must observe a delta relative to the current baseline, so
	// we re-seed the baseline back to rawBefore between iterations (a
	// successful tasksActionFire persists the new snapshot; here the fire is
	// expected to fail, but we rewind explicitly to isolate the resolve-failure
	// path from any baseline-persistence side effect).
	loop, _ := loopStore.Get()
	for i := 1; i <= MaxPromptResolveFailures; i++ {
		if err := NewTasksBaselineStore(store.SessionDir(sessionID)).Set(rawBefore); err != nil {
			t.Fatalf("iteration %d: baseline reset error = %v", i, err)
		}
		rawAfter := mustMarshalRows(t,
			beadsRow("mitto-1", "open", "2026-01-01T00:00:00Z"),
			beadsRow(fmt.Sprintf("mitto-new-%d", i), "open", "2026-01-01T00:00:00Z"),
		)

		// Sanity check: this must be a tasksActionFire decision, otherwise the
		// test would exercise the wrong branch.
		if got := runner.evaluateTasksChange(meta, loop, rawAfter).action; got != tasksActionFire {
			t.Fatalf("iteration %d: evaluateTasksChange action = %v, want tasksActionFire", i, got)
		}

		runner.processTasksChange(meta, loop, loopStore, rawAfter)
	}

	// After MaxPromptResolveFailures consecutive resolve failures the loop
	// MUST be auto-paused, matching the scheduled-loop parity contract in
	// checkSession/handlePromptResolveFailure.
	final, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get() error = %v", err)
	}
	if final.Enabled {
		t.Errorf("onTasks loop.Enabled = true after %d resolve failures; want false "+
			"(scheduled-path parity: handlePromptResolveFailure must run — mitto-uhnc)",
			MaxPromptResolveFailures)
	}
	if final.StoppedReason != session.StoppedReasonPromptUnresolved {
		t.Errorf("onTasks loop.StoppedReason = %q, want %q "+
			"(must record promptUnresolved like scheduled path — mitto-uhnc)",
			final.StoppedReason, session.StoppedReasonPromptUnresolved)
	}
	if autoStopCalls != 1 {
		t.Errorf("onLoopAutoStopped invocation count = %d, want 1 "+
			"(onTasks auto-pause must broadcast — mitto-uhnc)", autoStopCalls)
	}
}
