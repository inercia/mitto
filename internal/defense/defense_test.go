package defense

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig_BlockDurationIs7Days(t *testing.T) {
	config := DefaultConfig()
	expected := 7 * 24 * time.Hour
	if config.BlockDuration != expected {
		t.Errorf("Expected default BlockDuration = %v, got %v", expected, config.BlockDuration)
	}
}

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

func TestScannerDefense_BlockCommandExecuted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "blocked.txt")

	config := Config{
		Enabled:       true,
		RateLimit:     1,
		RateWindow:    time.Minute,
		BlockDuration: time.Hour,
		Whitelist:     []string{"127.0.0.0/8"},
		// The block command writes the blocked IP to a marker file
		BlockCommand: "echo {ip} > " + markerFile,
	}

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// Trigger a block by exceeding rate limit
	for i := 0; i < 3; i++ {
		d.RecordRequest("10.0.0.99", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	if !d.IsBlocked("10.0.0.99") {
		t.Fatal("Expected IP to be blocked")
	}

	// Wait for the async command to complete
	time.Sleep(500 * time.Millisecond)

	// Verify the command was executed by checking the marker file
	data, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("Expected marker file to exist: %v", err)
	}

	content := strings.TrimSpace(string(data))
	if content != "10.0.0.99" {
		t.Errorf("Expected marker file to contain '10.0.0.99', got %q", content)
	}
}

func TestScannerDefense_NoBlockCommandWhenEmpty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "blocked.txt")

	config := Config{
		Enabled:       true,
		RateLimit:     1,
		RateWindow:    time.Minute,
		BlockDuration: time.Hour,
		Whitelist:     []string{"127.0.0.0/8"},
		BlockCommand:  "", // Empty — should NOT execute anything
	}

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// Trigger a block
	for i := 0; i < 3; i++ {
		d.RecordRequest("10.0.0.99", &RequestInfo{
			Path:       "/api/test",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	if !d.IsBlocked("10.0.0.99") {
		t.Fatal("Expected IP to be blocked")
	}

	time.Sleep(200 * time.Millisecond)

	// Marker file should NOT exist
	if _, err := os.Stat(markerFile); err == nil {
		t.Error("Expected marker file to NOT exist when BlockCommand is empty")
	}
}

func TestScannerDefense_SuspiciousPathsDoubleBlockDuration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	blockDuration := 1 * time.Hour

	config := Config{
		Enabled:                 true,
		SuspiciousPathThreshold: 2,
		RateLimit:               1000, // High rate limit so only suspicious paths trigger
		RateWindow:              time.Minute,
		BlockDuration:           blockDuration,
		MinRequestsForAnalysis:  100, // High threshold so error rate doesn't trigger first
		Whitelist:               []string{"127.0.0.0/8"},
	}

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// Trigger a block via suspicious paths (use 200 status to avoid error rate trigger)
	suspiciousPaths := []string{"/.env", "/.git/config", "/wp-admin"}
	for _, path := range suspiciousPaths {
		d.RecordRequest("10.0.0.50", &RequestInfo{
			Path:       path,
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	if !d.IsBlocked("10.0.0.50") {
		t.Fatal("Expected IP to be blocked after suspicious paths")
	}

	entry := d.GetBlockEntry("10.0.0.50")
	if entry == nil {
		t.Fatal("Expected to find block entry")
	}

	// Verify reason is suspicious_paths
	if entry.Reason != "suspicious_paths" {
		t.Errorf("Expected reason 'suspicious_paths', got %q", entry.Reason)
	}

	// Verify block duration is doubled (2 * 1h = 2h)
	expectedDuration := 2 * blockDuration
	actualDuration := entry.ExpiresAt.Sub(entry.BlockedAt)

	// Allow 1 second tolerance for test execution time
	tolerance := 1 * time.Second
	if actualDuration < expectedDuration-tolerance || actualDuration > expectedDuration+tolerance {
		t.Errorf("Expected block duration ~%v (2x), got %v", expectedDuration, actualDuration)
	}
}

func TestScannerDefense_RateLimitShortBlockDuration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := Config{
		Enabled:       true,
		RateLimit:     1,
		RateWindow:    time.Minute,
		BlockDuration: 7 * 24 * time.Hour, // configured duration (used for other reasons)
		Whitelist:     []string{"127.0.0.0/8"},
	}

	d, err := New(config, logger)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer d.Close()

	// Trigger a block via rate limiting (not suspicious paths)
	for i := 0; i < 3; i++ {
		d.RecordRequest("10.0.0.51", &RequestInfo{
			Path:       "/api/sessions",
			Method:     "GET",
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	if !d.IsBlocked("10.0.0.51") {
		t.Fatal("Expected IP to be blocked after rate limit")
	}

	entry := d.GetBlockEntry("10.0.0.51")
	if entry == nil {
		t.Fatal("Expected to find block entry")
	}

	// Verify reason is rate_limit_exceeded
	if entry.Reason != "rate_limit_exceeded" {
		t.Errorf("Expected reason 'rate_limit_exceeded', got %q", entry.Reason)
	}

	// Rate limit blocks should use a short duration (10 minutes), NOT the
	// configured BlockDuration. Legitimate users can trigger rate limits
	// easily (page loads, rapid refreshes through tunnels).
	expectedDuration := 10 * time.Minute
	actualDuration := entry.ExpiresAt.Sub(entry.BlockedAt)
	tolerance := 1 * time.Second
	if actualDuration < expectedDuration-tolerance || actualDuration > expectedDuration+tolerance {
		t.Errorf("Expected rate limit block duration ~%v, got %v", expectedDuration, actualDuration)
	}
}
