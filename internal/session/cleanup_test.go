package session

import (
	"os"
	"testing"
)

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
