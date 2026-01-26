package session

import (
	"testing"
	"time"
)

func TestLock_AcquireAndRelease(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session first
	meta := Metadata{
		SessionID:  "test-session-lock",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Acquire lock
	lock, err := store.TryAcquireLock("test-session-lock", "cli")
	if err != nil {
		t.Fatalf("TryAcquireLock failed: %v", err)
	}

	// Verify lock info
	info := lock.Info()
	if info.Status != LockStatusIdle {
		t.Errorf("Expected status %q, got %q", LockStatusIdle, info.Status)
	}
	if info.ClientType != "cli" {
		t.Errorf("Expected client type %q, got %q", "cli", info.ClientType)
	}

	// Release lock
	if err := lock.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Verify lock is released
	locked, _, err := store.IsLocked("test-session-lock")
	if err != nil {
		t.Fatalf("IsLocked failed: %v", err)
	}
	if locked {
		t.Error("Session should not be locked after release")
	}
}

func TestLock_PreventDoubleAcquire(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-double",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// First lock should succeed
	lock1, err := store.TryAcquireLock("test-session-double", "cli")
	if err != nil {
		t.Fatalf("First TryAcquireLock failed: %v", err)
	}
	defer lock1.Release()

	// Second lock should fail
	_, err = store.TryAcquireLock("test-session-double", "web")
	if err != ErrSessionLocked {
		t.Errorf("Expected ErrSessionLocked, got: %v", err)
	}
}

func TestLock_StatusUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-status",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	lock, err := store.TryAcquireLock("test-session-status", "cli")
	if err != nil {
		t.Fatalf("TryAcquireLock failed: %v", err)
	}
	defer lock.Release()

	// Set processing
	if err := lock.SetProcessing("Running tool: file_read"); err != nil {
		t.Fatalf("SetProcessing failed: %v", err)
	}

	info := lock.Info()
	if info.Status != LockStatusProcessing {
		t.Errorf("Expected status %q, got %q", LockStatusProcessing, info.Status)
	}

	// Verify status is persisted
	readInfo, err := store.GetLockInfo("test-session-status")
	if err != nil {
		t.Fatalf("GetLockInfo failed: %v", err)
	}
	if readInfo.Status != LockStatusProcessing {
		t.Errorf("Persisted status should be %q, got %q", LockStatusProcessing, readInfo.Status)
	}

	// Set idle
	if err := lock.SetIdle(); err != nil {
		t.Fatalf("SetIdle failed: %v", err)
	}

	info = lock.Info()
	if info.Status != LockStatusIdle {
		t.Errorf("Expected status %q, got %q", LockStatusIdle, info.Status)
	}
}

func TestLock_ForceAcquireIdle(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-force-idle",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// First lock
	lock1, err := store.TryAcquireLock("test-session-force-idle", "cli")
	if err != nil {
		t.Fatalf("First TryAcquireLock failed: %v", err)
	}

	// Force acquire should succeed when idle
	lock2, err := store.ForceAcquireLock("test-session-force-idle", "web")
	if err != nil {
		t.Fatalf("ForceAcquireLock failed: %v", err)
	}
	defer lock2.Release()

	// Original lock should no longer be valid
	if lock1.IsValid() {
		t.Error("Original lock should no longer be valid after force acquire")
	}
}

func TestLock_ForceAcquireProcessingBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-force-processing",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// First lock and set to processing
	lock1, err := store.TryAcquireLock("test-session-force-processing", "cli")
	if err != nil {
		t.Fatalf("First TryAcquireLock failed: %v", err)
	}
	defer lock1.Release()

	if err := lock1.SetProcessing("Agent is thinking..."); err != nil {
		t.Fatalf("SetProcessing failed: %v", err)
	}

	// Force acquire should fail when processing
	_, err = store.ForceAcquireLock("test-session-force-processing", "web")
	if err != ErrSessionProcessing {
		t.Errorf("Expected ErrSessionProcessing, got: %v", err)
	}
}

func TestLock_ForceInterruptProcessing(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-session-interrupt",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// First lock and set to processing
	lock1, err := store.TryAcquireLock("test-session-interrupt", "cli")
	if err != nil {
		t.Fatalf("First TryAcquireLock failed: %v", err)
	}

	if err := lock1.SetProcessing("Agent is thinking..."); err != nil {
		t.Fatalf("SetProcessing failed: %v", err)
	}

	// Force interrupt should succeed even when processing
	lock2, err := store.ForceInterruptLock("test-session-interrupt", "web")
	if err != nil {
		t.Fatalf("ForceInterruptLock failed: %v", err)
	}
	defer lock2.Release()

	// Original lock should no longer be valid
	if lock1.IsValid() {
		t.Error("Original lock should no longer be valid after force interrupt")
	}
}

func TestLockInfo_StealabilityReason(t *testing.T) {
	tests := []struct {
		name     string
		info     LockInfo
		timeout  time.Duration
		contains string
	}{
		{
			name: "idle session",
			info: LockInfo{
				Status:    LockStatusIdle,
				Heartbeat: time.Now(),
			},
			timeout:  DefaultStaleTimeout,
			contains: "idle and can be resumed",
		},
		{
			name: "processing session",
			info: LockInfo{
				Status:    LockStatusProcessing,
				Heartbeat: time.Now(),
			},
			timeout:  DefaultStaleTimeout,
			contains: "currently processing",
		},
		{
			name: "stale session",
			info: LockInfo{
				Status:    LockStatusProcessing,
				Heartbeat: time.Now().Add(-2 * time.Minute),
			},
			timeout:  DefaultStaleTimeout,
			contains: "appears stale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := tt.info.StealabilityReason(tt.timeout)
			if !containsString(reason, tt.contains) {
				t.Errorf("Expected reason to contain %q, got %q", tt.contains, reason)
			}
		})
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestStore_CheckLockStatus(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-check-status",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Check unlocked session
	result, err := store.CheckLockStatus("test-check-status")
	if err != nil {
		t.Fatalf("CheckLockStatus failed: %v", err)
	}
	if result.IsLocked {
		t.Error("Session should not be locked")
	}
	if !result.CanResume {
		t.Error("Should be able to resume unlocked session")
	}

	// Lock the session
	lock, err := store.TryAcquireLock("test-check-status", "cli")
	if err != nil {
		t.Fatalf("TryAcquireLock failed: %v", err)
	}

	// Check locked idle session
	result, err = store.CheckLockStatus("test-check-status")
	if err != nil {
		t.Fatalf("CheckLockStatus failed: %v", err)
	}
	if !result.IsLocked {
		t.Error("Session should be locked")
	}
	if result.CanResume {
		t.Error("Should not be able to resume without force")
	}
	if !result.CanForce {
		t.Error("Should be able to force resume idle session")
	}

	// Set to processing
	lock.SetProcessing("Agent thinking...")

	result, err = store.CheckLockStatus("test-check-status")
	if err != nil {
		t.Fatalf("CheckLockStatus failed: %v", err)
	}
	if result.CanForce {
		t.Error("Should not be able to force resume processing session")
	}
	if !result.CanInterrupt {
		t.Error("Should always be able to interrupt")
	}

	lock.Release()
}
