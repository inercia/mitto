package defense

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBlocklist_AddAndContains(t *testing.T) {
	bl := NewBlocklist([]string{"127.0.0.0/8"})

	// Initially empty
	if bl.Contains("192.168.1.1") {
		t.Error("Expected empty blocklist to not contain IP")
	}

	// Add an IP
	bl.Add(&BlockEntry{
		IP:        "192.168.1.1",
		BlockedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Reason:    "test",
	})

	// Should now be blocked
	if !bl.Contains("192.168.1.1") {
		t.Error("Expected blocklist to contain added IP")
	}

	// Other IPs should not be blocked
	if bl.Contains("192.168.1.2") {
		t.Error("Expected blocklist to not contain other IP")
	}
}

func TestBlocklist_Whitelist(t *testing.T) {
	bl := NewBlocklist([]string{"127.0.0.0/8", "::1/128"})

	// Try to add localhost
	bl.Add(&BlockEntry{
		IP:        "127.0.0.1",
		BlockedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Reason:    "test",
	})

	// Localhost should never be blocked (whitelisted)
	if bl.Contains("127.0.0.1") {
		t.Error("Expected whitelisted IP to never be blocked")
	}

	// IPv6 localhost
	bl.Add(&BlockEntry{
		IP:        "::1",
		BlockedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Reason:    "test",
	})

	if bl.Contains("::1") {
		t.Error("Expected IPv6 localhost to never be blocked")
	}
}

func TestBlocklist_Expiration(t *testing.T) {
	bl := NewBlocklist(nil)

	// Add an already-expired entry
	bl.Add(&BlockEntry{
		IP:        "192.168.1.1",
		BlockedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Hour), // Expired 1 hour ago
		Reason:    "test",
	})

	// Expired entry should not be blocked
	if bl.Contains("192.168.1.1") {
		t.Error("Expected expired entry to not be blocked")
	}
}

func TestBlocklist_CleanExpired(t *testing.T) {
	bl := NewBlocklist(nil)

	// Add an expired entry
	bl.Add(&BlockEntry{
		IP:        "192.168.1.1",
		BlockedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Hour),
		Reason:    "test",
	})

	// Add a valid entry
	bl.Add(&BlockEntry{
		IP:        "192.168.1.2",
		BlockedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Reason:    "test",
	})

	// Clean expired
	removed := bl.CleanExpired()
	if removed != 1 {
		t.Errorf("Expected 1 removed, got %d", removed)
	}

	if bl.Count() != 1 {
		t.Errorf("Expected 1 entry remaining, got %d", bl.Count())
	}
}

func TestBlocklist_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "blocklist.json")

	// Create and save
	bl := NewBlocklist(nil)
	bl.Add(&BlockEntry{
		IP:        "192.168.1.1",
		BlockedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Reason:    "test",
	})

	if err := bl.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load into new blocklist
	bl2 := NewBlocklist(nil)
	if err := bl2.Load(path); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if bl2.Count() != 1 {
		t.Errorf("Expected 1 entry after load, got %d", bl2.Count())
	}

	if !bl2.Contains("192.168.1.1") {
		t.Error("Expected loaded blocklist to contain saved IP")
	}
}

func TestBlocklist_LoadNonExistent(t *testing.T) {
	bl := NewBlocklist(nil)
	err := bl.Load("/nonexistent/path/blocklist.json")
	if err != nil {
		t.Errorf("Load of non-existent file should not error: %v", err)
	}
}

func TestBlocklist_LoadExpiredEntries(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "blocklist.json")

	// Write file with expired entry
	content := `[{"ip":"192.168.1.1","blocked_at":"2020-01-01T00:00:00Z","expires_at":"2020-01-02T00:00:00Z","reason":"test"}]`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	bl := NewBlocklist(nil)
	if err := bl.Load(path); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Expired entries should not be loaded
	if bl.Count() != 0 {
		t.Errorf("Expected 0 entries (expired), got %d", bl.Count())
	}
}
