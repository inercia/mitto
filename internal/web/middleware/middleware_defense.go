package middleware

import (
	"time"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/defense"
)

// ShouldEnableScannerDefense determines whether scanner defense should be enabled.
// It is enabled by default when external access is configured (ExternalPort >= 0),
// unless explicitly disabled in config.
func ShouldEnableScannerDefense(webConfig *configPkg.WebConfig) bool {
	if webConfig == nil {
		return false
	}

	// Check if explicitly configured
	if webConfig.Security != nil && webConfig.Security.ScannerDefense != nil {
		return webConfig.Security.ScannerDefense.Enabled
	}

	// Enable by default when external access is configured
	// External port >= 0 means external access is enabled (0 = random, >0 = specific port)
	return webConfig.ExternalPort >= 0
}

// GetScannerDefenseConfig returns the scanner defense config from WebSecurity.
func GetScannerDefenseConfig(webConfig *configPkg.WebConfig) *configPkg.ScannerDefenseConfig {
	if webConfig == nil || webConfig.Security == nil {
		return nil
	}
	return webConfig.Security.ScannerDefense
}

// ConfigToDefenseConfig converts ScannerDefenseConfig to defense.Config.
// If cfg is nil, returns defaults with Enabled set based on externalAccessEnabled.
func ConfigToDefenseConfig(cfg *configPkg.ScannerDefenseConfig, enabled bool) defense.Config {
	c := defense.DefaultConfig()
	c.Enabled = enabled

	// Set persistence path
	if path, err := appdir.DefenseBlocklistPath(); err == nil {
		c.PersistPath = path
	}

	if cfg == nil {
		return c
	}

	// Apply explicit configuration values
	if cfg.RateLimit > 0 {
		c.RateLimit = cfg.RateLimit
	}
	if cfg.RateWindowSeconds > 0 {
		c.RateWindow = time.Duration(cfg.RateWindowSeconds) * time.Second
	}
	if cfg.ErrorRateThreshold > 0 {
		c.ErrorRateThreshold = cfg.ErrorRateThreshold
	}
	if cfg.MinRequestsForAnalysis > 0 {
		c.MinRequestsForAnalysis = cfg.MinRequestsForAnalysis
	}
	if cfg.SuspiciousPathThreshold > 0 {
		c.SuspiciousPathThreshold = cfg.SuspiciousPathThreshold
	}
	if cfg.BlockDurationSeconds > 0 {
		c.BlockDuration = time.Duration(cfg.BlockDurationSeconds) * time.Second
	}
	if len(cfg.Whitelist) > 0 {
		c.Whitelist = cfg.Whitelist
	}
	if cfg.IPBlockCommand != "" {
		c.BlockCommand = cfg.IPBlockCommand
	}

	return c
}
