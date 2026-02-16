package defense

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScannerDefense_DisabledByDefault(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := DefaultConfig()
	config.Enabled = false

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// When disabled, nothing should be blocked
	if d.IsBlocked("192.168.1.1") {
		t.Error("Expected no blocking when disabled")
	}
}

func TestScannerDefense_BlocksAfterRateLimit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	tmpDir := t.TempDir()

	config := Config{
		Enabled:       true,
		RateLimit:     5,
		RateWindow:    time.Minute,
		BlockDuration: time.Hour,
		Whitelist:     []string{"127.0.0.0/8"},
		PersistPath:   filepath.Join(tmpDir, "blocklist.json"),
	}

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// Should not be blocked initially
	if d.IsBlocked("192.168.1.1") {
		t.Error("Expected IP to not be blocked initially")
	}

	// Record requests to exceed rate limit
	for i := 0; i < 7; i++ {
		d.RecordRequest("192.168.1.1", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	// Should now be blocked
	if !d.IsBlocked("192.168.1.1") {
		t.Error("Expected IP to be blocked after exceeding rate limit")
	}
}

func TestScannerDefense_WhitelistNeverBlocked(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := Config{
		Enabled:       true,
		RateLimit:     1, // Very low limit
		RateWindow:    time.Minute,
		BlockDuration: time.Hour,
		Whitelist:     []string{"127.0.0.0/8"},
	}

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// Record many requests from localhost
	for i := 0; i < 100; i++ {
		d.RecordRequest("127.0.0.1", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	// Localhost should never be blocked
	if d.IsBlocked("127.0.0.1") {
		t.Error("Expected localhost to never be blocked")
	}
}

func TestScannerDefense_BlockedCount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := Config{
		Enabled:       true,
		RateLimit:     1,
		RateWindow:    time.Minute,
		BlockDuration: time.Hour,
		Whitelist:     []string{"127.0.0.0/8"},
	}

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// Block two IPs
	for i := 0; i < 3; i++ {
		d.RecordRequest("192.168.1.1", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
		d.RecordRequest("192.168.1.2", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	count := d.BlockedCount()
	if count != 2 {
		t.Errorf("Expected 2 blocked IPs, got %d", count)
	}
}

func TestScannerDefense_Persistence(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "blocklist.json")

	config := Config{
		Enabled:       true,
		RateLimit:     1,
		RateWindow:    time.Minute,
		BlockDuration: time.Hour,
		Whitelist:     []string{"127.0.0.0/8"},
		PersistPath:   path,
	}

	// Create first instance and block an IP
	d1, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		d1.RecordRequest("192.168.1.1", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	if !d1.IsBlocked("192.168.1.1") {
		t.Error("Expected IP to be blocked")
	}

	d1.Close()

	// Create new instance - should load persisted blocklist
	d2, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d2.Close()

	if !d2.IsBlocked("192.168.1.1") {
		t.Error("Expected IP to still be blocked after reload")
	}
}
