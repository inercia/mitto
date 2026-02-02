package session

import (
	"os"
	"testing"
)

func TestCleanup_LockRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "test-cleanup-registration",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Initially no active locks
	initialCount := ActiveLockCount()

	// Acquire lock
	lock, err := store.TryAcquireLock("test-cleanup-registration", "cli")
	if err != nil {
		t.Fatalf("TryAcquireLock failed: %v", err)
	}

	// Should have one more lock registered
	if ActiveLockCount() != initialCount+1 {
		t.Errorf("Expected %d active locks, got %d", initialCount+1, ActiveLockCount())
	}

	// Release lock
	lock.Release()

	// Should be back to initial count
	if ActiveLockCount() != initialCount {
		t.Errorf("Expected %d active locks after release, got %d", initialCount, ActiveLockCount())
	}
}

func TestCleanup_CleanupAllLocks(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create multiple sessions
	for i := 0; i < 3; i++ {
		meta := Metadata{
			SessionID:  "test-cleanup-all-" + string(rune('a'+i)),
			ACPServer:  "test-server",
			WorkingDir: "/test/dir",
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	initialCount := ActiveLockCount()

	// Acquire multiple locks - we don't use the locks directly,
	// but they must remain in scope until CleanupAllLocks is called
	locks := make([]*Lock, 3)
	for i := 0; i < 3; i++ {
		lock, err := store.TryAcquireLock("test-cleanup-all-"+string(rune('a'+i)), "cli")
		if err != nil {
			t.Fatalf("TryAcquireLock failed: %v", err)
		}
		locks[i] = lock
	}
	_ = locks // Silence unused variable warning - locks kept in scope for cleanup test

	// Should have 3 more locks
	if ActiveLockCount() != initialCount+3 {
		t.Errorf("Expected %d active locks, got %d", initialCount+3, ActiveLockCount())
	}

	// Cleanup all locks
	CleanupAllLocks()

	// All locks should be released
	if ActiveLockCount() != initialCount {
		t.Errorf("Expected %d active locks after cleanup, got %d", initialCount, ActiveLockCount())
	}

	// Verify lock files are removed
	for i := 0; i < 3; i++ {
		sessionID := "test-cleanup-all-" + string(rune('a'+i))
		locked, _, err := store.IsLocked(sessionID)
		if err != nil {
			t.Errorf("IsLocked failed for %s: %v", sessionID, err)
		}
		if locked {
			t.Errorf("Session %s should not be locked after cleanup", sessionID)
		}
	}
}

func TestIsPIDRunning(t *testing.T) {
	// Current process should be running
	if !isPIDRunning(os.Getpid()) {
		t.Error("Current process should be detected as running")
	}

	// PID 0 should not be running (or at least not accessible)
	if isPIDRunning(0) {
		t.Error("PID 0 should not be detected as running")
	}

	// Very high PID should not be running
	if isPIDRunning(999999999) {
		t.Error("Very high PID should not be detected as running")
	}
}

func TestLockInfo_IsProcessDead(t *testing.T) {
	hostname, _ := os.Hostname()

	// Current process should not be dead
	info := LockInfo{
		PID:      os.Getpid(),
		Hostname: hostname,
	}
	if info.IsProcessDead(hostname) {
		t.Error("Current process should not be detected as dead")
	}

	// Non-existent PID should be dead
	info = LockInfo{
		PID:      999999999,
		Hostname: hostname,
	}
	if !info.IsProcessDead(hostname) {
		t.Error("Non-existent PID should be detected as dead")
	}

	// Different hostname - can't determine, should return false
	info = LockInfo{
		PID:      999999999,
		Hostname: "different-host",
	}
	if info.IsProcessDead(hostname) {
		t.Error("Different hostname should return false (can't determine)")
	}
}
