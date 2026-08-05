package config

import "testing"

// TestPProfEnabled covers the precedence rules documented on PProfEnabled:
// MITTO_PPROF env var (when set) wins over cfg.Web.PProf; a nil cfg with no
// env var resolves to false (mitto-aek).
func TestPProfEnabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string // always set via t.Setenv; "" means "not set" per PProfEnabled's own check
		cfg      *Config
		expected bool
	}{
		{
			name:     "nil config, no env",
			envValue: "",
			cfg:      nil,
			expected: false,
		},
		{
			name:     "nil config, env=1",
			envValue: "1",
			cfg:      nil,
			expected: true,
		},
		{
			name:     "config web.pprof true, no env",
			envValue: "",
			cfg:      &Config{Web: WebConfig{PProf: true}},
			expected: true,
		},
		{
			name:     "config web.pprof false, no env",
			envValue: "",
			cfg:      &Config{Web: WebConfig{PProf: false}},
			expected: false,
		},
		{
			name:     "env=true overrides config false",
			envValue: "true",
			cfg:      &Config{Web: WebConfig{PProf: false}},
			expected: true,
		},
		{
			name:     "env=yes (case-insensitive) overrides config false",
			envValue: "YES",
			cfg:      &Config{Web: WebConfig{PProf: false}},
			expected: true,
		},
		{
			name:     "env=0 overrides config true",
			envValue: "0",
			cfg:      &Config{Web: WebConfig{PProf: true}},
			expected: false,
		},
		{
			name:     "env=false overrides config true",
			envValue: "false",
			cfg:      &Config{Web: WebConfig{PProf: true}},
			expected: false,
		},
		{
			name:     "env=garbage treated as false, overrides config true",
			envValue: "banana",
			cfg:      &Config{Web: WebConfig{PProf: true}},
			expected: false,
		},
		{
			name:     "env empty string is treated as unset, falls back to config true",
			envValue: "",
			cfg:      &Config{Web: WebConfig{PProf: true}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Always call t.Setenv (even with "") to isolate from any ambient
			// MITTO_PPROF in the developer's real environment, and to get its
			// automatic restore-after-test behavior.
			t.Setenv("MITTO_PPROF", tt.envValue)
			got := PProfEnabled(tt.cfg)
			if got != tt.expected {
				t.Errorf("PProfEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}
