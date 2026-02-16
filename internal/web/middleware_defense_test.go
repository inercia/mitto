package web

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestShouldEnableScannerDefense(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.WebConfig
		expected bool
	}{
		{
			name:     "nil config",
			config:   nil,
			expected: false,
		},
		{
			name:     "external port disabled (-1)",
			config:   &config.WebConfig{ExternalPort: -1},
			expected: false,
		},
		{
			name:     "external port random (0) - enables defense",
			config:   &config.WebConfig{ExternalPort: 0},
			expected: true,
		},
		{
			name:     "external port specific (8443) - enables defense",
			config:   &config.WebConfig{ExternalPort: 8443},
			expected: true,
		},
		{
			name: "explicitly enabled in security config",
			config: &config.WebConfig{
				ExternalPort: -1, // Disabled but...
				Security: &config.WebSecurity{
					ScannerDefense: &config.ScannerDefenseConfig{Enabled: true},
				},
			},
			expected: true,
		},
		{
			name: "explicitly disabled in security config",
			config: &config.WebConfig{
				ExternalPort: 8443, // Would enable by default but...
				Security: &config.WebSecurity{
					ScannerDefense: &config.ScannerDefenseConfig{Enabled: false},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldEnableScannerDefense(tt.config)
			if got != tt.expected {
				t.Errorf("shouldEnableScannerDefense() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetScannerDefenseConfig(t *testing.T) {
	t.Run("nil web config", func(t *testing.T) {
		got := getScannerDefenseConfig(nil)
		if got != nil {
			t.Error("Expected nil for nil web config")
		}
	})

	t.Run("nil security", func(t *testing.T) {
		got := getScannerDefenseConfig(&config.WebConfig{})
		if got != nil {
			t.Error("Expected nil for nil security")
		}
	})

	t.Run("returns scanner defense config", func(t *testing.T) {
		cfg := &config.WebConfig{
			Security: &config.WebSecurity{
				ScannerDefense: &config.ScannerDefenseConfig{
					Enabled:   true,
					RateLimit: 50,
				},
			},
		}
		got := getScannerDefenseConfig(cfg)
		if got == nil {
			t.Fatal("Expected non-nil scanner defense config")
		}
		if got.RateLimit != 50 {
			t.Errorf("RateLimit = %d, want 50", got.RateLimit)
		}
	})
}

func TestConfigToDefenseConfig(t *testing.T) {
	t.Run("nil config uses defaults with enabled flag", func(t *testing.T) {
		got := configToDefenseConfig(nil, true)
		if !got.Enabled {
			t.Error("Expected Enabled to be true")
		}
		if got.RateLimit != 100 { // Default
			t.Errorf("RateLimit = %d, want 100", got.RateLimit)
		}
	})

	t.Run("explicit config values override defaults", func(t *testing.T) {
		cfg := &config.ScannerDefenseConfig{
			RateLimit:            50,
			RateWindowSeconds:    120,
			BlockDurationSeconds: 3600,
		}
		got := configToDefenseConfig(cfg, true)
		if got.RateLimit != 50 {
			t.Errorf("RateLimit = %d, want 50", got.RateLimit)
		}
		if got.RateWindow.Seconds() != 120 {
			t.Errorf("RateWindow = %v, want 120s", got.RateWindow)
		}
		if got.BlockDuration.Hours() != 1 {
			t.Errorf("BlockDuration = %v, want 1h", got.BlockDuration)
		}
	})
}
