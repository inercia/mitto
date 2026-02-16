package defense

import (
	"log/slog"
	"sync"
	"time"
)

// NOTE: The Blocklist also exposes isWhitelisted() but that's private.
// We add IsWhitelisted() as a public method to allow callers to skip
// unnecessary processing for whitelisted IPs.

// cleanupInterval is how often expired entries are cleaned up.
const cleanupInterval = 5 * time.Minute

// metricsCleanupAge is how long to keep metrics for IPs that haven't been seen.
const metricsCleanupAge = 1 * time.Hour

// ScannerDefense coordinates detection and blocking of malicious IPs.
type ScannerDefense struct {
	mu        sync.RWMutex
	config    Config
	blocklist *Blocklist
	metrics   *IPMetrics
	logger    *slog.Logger
	stopCh    chan struct{}
	wg        sync.WaitGroup // waits for cleanup goroutine to exit
	stopped   bool
}

// New creates a new ScannerDefense instance.
// It loads the persistent blocklist from disk and starts a background cleanup goroutine.
func New(config Config, logger *slog.Logger) (*ScannerDefense, error) {
	blocklist := NewBlocklist(config.Whitelist)

	// Load persistent blocklist if path is configured
	if config.PersistPath != "" {
		if err := blocklist.Load(config.PersistPath); err != nil {
			logger.Warn("blocklist_load_error",
				"component", "defense",
				"path", config.PersistPath,
				"error", err,
			)
			// Continue without loaded blocklist - not fatal
		} else {
			logger.Info("blocklist_loaded",
				"component", "defense",
				"entries", blocklist.Count(),
				"path", config.PersistPath,
			)
		}
	}

	d := &ScannerDefense{
		config:    config,
		blocklist: blocklist,
		metrics:   NewIPMetrics(config.RateWindow),
		logger:    logger,
		stopCh:    make(chan struct{}),
	}

	// Start background cleanup goroutine
	d.wg.Add(1)
	go d.cleanupLoop()

	return d, nil
}

// IsBlocked checks if an IP is currently blocked.
// This must be fast (O(1)) as it's called per connection.
func (d *ScannerDefense) IsBlocked(ip string) bool {
	if !d.config.Enabled {
		return false
	}
	return d.blocklist.Contains(ip)
}

// GetBlockReason returns the reason why an IP is blocked, or empty string if not blocked.
func (d *ScannerDefense) GetBlockReason(ip string) string {
	if !d.config.Enabled {
		return ""
	}
	return d.blocklist.GetReason(ip)
}

// RecordRequest records a request for analysis.
// This is called by middleware after the request is processed.
func (d *ScannerDefense) RecordRequest(ip string, req *RequestInfo) {
	if !d.config.Enabled {
		return
	}

	// Skip whitelisted IPs - no need to collect metrics or analyze them
	if d.blocklist.IsWhitelisted(ip) {
		return
	}

	// Record the request
	d.metrics.Record(ip, req)

	// Check if this IP should be blocked
	shouldBlock, reason := d.metrics.ShouldBlock(ip, d.config)
	if shouldBlock {
		d.blockIP(ip, reason)
	}
}

// blockIP adds an IP to the blocklist.
func (d *ScannerDefense) blockIP(ip, reason string) {
	stats := d.metrics.GetStats(ip)
	requestCount := 0
	if stats != nil {
		requestCount = stats.TotalRequests
	}

	entry := &BlockEntry{
		IP:           ip,
		BlockedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(d.config.BlockDuration),
		Reason:       reason,
		RequestCount: requestCount,
	}

	d.blocklist.Add(entry)

	var errorRate float64
	if stats != nil && stats.TotalRequests > 0 {
		errorRate = float64(stats.ErrorRequests) / float64(stats.TotalRequests)
	}

	d.logger.Warn("ip_blocked",
		"component", "defense",
		"ip", ip,
		"reason", reason,
		"block_duration", d.config.BlockDuration,
		"request_count", requestCount,
		"error_rate", errorRate,
	)

	// Persist blocklist if path is configured
	d.persistBlocklist()
}

// persistBlocklist saves the blocklist to disk.
func (d *ScannerDefense) persistBlocklist() {
	if d.config.PersistPath == "" {
		return
	}

	if err := d.blocklist.Save(d.config.PersistPath); err != nil {
		d.logger.Warn("blocklist_save_error",
			"component", "defense",
			"path", d.config.PersistPath,
			"error", err,
		)
	}
}

// cleanupLoop runs background cleanup of expired entries.
func (d *ScannerDefense) cleanupLoop() {
	defer d.wg.Done()

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.cleanup()
		case <-d.stopCh:
			return
		}
	}
}

// cleanup removes expired entries from blocklist and stale metrics.
func (d *ScannerDefense) cleanup() {
	// Clean expired blocklist entries
	if removed := d.blocklist.CleanExpired(); removed > 0 {
		d.logger.Debug("blocklist_cleanup",
			"component", "defense",
			"removed", removed,
		)
		d.persistBlocklist()
	}

	// Clean stale metrics
	if removed := d.metrics.Cleanup(metricsCleanupAge); removed > 0 {
		d.logger.Debug("metrics_cleanup",
			"component", "defense",
			"removed", removed,
		)
	}
}

// Close stops background goroutines and persists the blocklist.
func (d *ScannerDefense) Close() error {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return nil
	}
	d.stopped = true

	// Stop cleanup goroutine
	close(d.stopCh)
	d.mu.Unlock()

	// Wait for cleanup goroutine to exit before persisting
	d.wg.Wait()

	// Final persist (safe now - no concurrent access from cleanup goroutine)
	d.persistBlocklist()

	d.logger.Info("defense_closed",
		"component", "defense",
		"blocked_ips", d.blocklist.Count(),
	)

	return nil
}

// BlockedCount returns the current number of blocked IPs.
func (d *ScannerDefense) BlockedCount() int {
	return d.blocklist.Count()
}
