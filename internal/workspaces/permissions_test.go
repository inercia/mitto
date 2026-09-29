package workspaces

import (
	"testing"
	"time"
)

func TestIsValidPermissionMode(t *testing.T) {
	tests := []struct {
		name string
		mode PermissionMode
		want bool
	}{
		{"unset is valid (inherit)", PermissionModeUnset, true},
		{"ask is valid", PermissionModeAsk, true},
		{"allow is valid", PermissionModeAllow, true},
		{"system_one is valid", PermissionModeSystemOne, true},
		{"garbage is invalid", PermissionMode("bogus"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidPermissionMode(tt.mode); got != tt.want {
				t.Errorf("IsValidPermissionMode(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestMigrateLegacyAutoApprove(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name        string
		autoApprove *bool
		isGlobal    bool
		want        PermissionMode
	}{
		{"nil at workspace scope inherits", nil, false, PermissionModeUnset},
		{"nil at global scope defaults to allow", nil, true, PermissionModeAllow},
		{"true always maps to allow (workspace)", &trueVal, false, PermissionModeAllow},
		{"true always maps to allow (global)", &trueVal, true, PermissionModeAllow},
		{"false at workspace scope inherits", &falseVal, false, PermissionModeUnset},
		{"false at global scope maps to ask", &falseVal, true, PermissionModeAsk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MigrateLegacyAutoApprove(tt.autoApprove, tt.isGlobal); got != tt.want {
				t.Errorf("MigrateLegacyAutoApprove(%v, %v) = %q, want %q", tt.autoApprove, tt.isGlobal, got, tt.want)
			}
		})
	}
}

func TestValidateSystemOneConfig(t *testing.T) {
	valid := DefaultSystemOneConfig()

	tests := []struct {
		name    string
		mutate  func(c *SystemOneConfig)
		wantErr bool
	}{
		{"default config is valid", func(c *SystemOneConfig) {}, false},
		{"nil config is invalid", nil, true},
		{"empty endpoint invalid", func(c *SystemOneConfig) { c.Endpoint = "" }, true},
		{"http non-loopback invalid", func(c *SystemOneConfig) { c.Endpoint = "http://example.com/x" }, true},
		{"http loopback 127.0.0.1 valid", func(c *SystemOneConfig) { c.Endpoint = "http://127.0.0.1:8080/x" }, false},
		{"http localhost valid", func(c *SystemOneConfig) { c.Endpoint = "http://localhost:8080/x" }, false},
		{"non-https/http scheme invalid", func(c *SystemOneConfig) { c.Endpoint = "ftp://example.com/x" }, true},
		{"malformed endpoint invalid", func(c *SystemOneConfig) { c.Endpoint = "://bad-url" }, true},
		{"empty payload template invalid", func(c *SystemOneConfig) { c.PayloadTemplate = "" }, true},
		{"empty result field invalid", func(c *SystemOneConfig) { c.ResultField = "" }, true},
		{"invalid result field chars invalid", func(c *SystemOneConfig) { c.ResultField = "not valid!" }, true},
		{"valid result field chars with dash/underscore", func(c *SystemOneConfig) { c.ResultField = "is_safe-2" }, false},
		{"threshold below 0 invalid", func(c *SystemOneConfig) { c.Threshold = -0.1 }, true},
		{"threshold above 1 invalid", func(c *SystemOneConfig) { c.Threshold = 1.1 }, true},
		{"threshold 0 valid", func(c *SystemOneConfig) { c.Threshold = 0 }, false},
		{"threshold 1 valid", func(c *SystemOneConfig) { c.Threshold = 1 }, false},
		{"timeout below 1s invalid", func(c *SystemOneConfig) { c.Timeout = 500 * time.Millisecond }, true},
		{"timeout above 60s invalid", func(c *SystemOneConfig) { c.Timeout = 61 * time.Second }, true},
		{"timeout at boundary 1s valid", func(c *SystemOneConfig) { c.Timeout = time.Second }, false},
		{"timeout at boundary 60s valid", func(c *SystemOneConfig) { c.Timeout = 60 * time.Second }, false},
		{"invalid on_deny invalid", func(c *SystemOneConfig) { c.OnDeny = SystemOneFallback("bogus") }, true},
		{"invalid on_error invalid", func(c *SystemOneConfig) { c.OnError = SystemOneFallback("bogus") }, true},
		{"on_deny reject valid", func(c *SystemOneConfig) { c.OnDeny = SystemOneFallbackReject }, false},
		{"on_error reject valid", func(c *SystemOneConfig) { c.OnError = SystemOneFallbackReject }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mutate == nil {
				if err := ValidateSystemOneConfig(nil); (err != nil) != tt.wantErr {
					t.Errorf("ValidateSystemOneConfig(nil) error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			cfg := valid
			tt.mutate(&cfg)
			err := ValidateSystemOneConfig(&cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSystemOneConfig(%+v) error = %v, wantErr %v", cfg, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePermissionSettings(t *testing.T) {
	validSystemOne := DefaultSystemOneConfig()

	tests := []struct {
		name    string
		p       *PermissionSettings
		wantErr bool
	}{
		{"nil is valid", nil, false},
		{"unset mode is valid", &PermissionSettings{Mode: PermissionModeUnset}, false},
		{"ask mode is valid", &PermissionSettings{Mode: PermissionModeAsk}, false},
		{"allow mode is valid", &PermissionSettings{Mode: PermissionModeAllow}, false},
		{"invalid mode is invalid", &PermissionSettings{Mode: PermissionMode("bogus")}, true},
		{
			"system_one mode without config is invalid",
			&PermissionSettings{Mode: PermissionModeSystemOne},
			true,
		},
		{
			"system_one mode with valid config is valid",
			&PermissionSettings{Mode: PermissionModeSystemOne, SystemOne: &validSystemOne},
			false,
		},
		{
			"system_one mode with invalid config is invalid",
			&PermissionSettings{Mode: PermissionModeSystemOne, SystemOne: &SystemOneConfig{}},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePermissionSettings(tt.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePermissionSettings(%+v) error = %v, wantErr %v", tt.p, err, tt.wantErr)
			}
		})
	}
}

func TestPermissionSettings_EffectiveMode(t *testing.T) {
	tests := []struct {
		name     string
		p        *PermissionSettings
		fallback PermissionMode
		want     PermissionMode
	}{
		{"nil receiver returns fallback", nil, PermissionModeAllow, PermissionModeAllow},
		{"unset mode returns fallback", &PermissionSettings{Mode: PermissionModeUnset}, PermissionModeAsk, PermissionModeAsk},
		{"explicit mode wins over fallback", &PermissionSettings{Mode: PermissionModeAllow}, PermissionModeAsk, PermissionModeAllow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.EffectiveMode(tt.fallback); got != tt.want {
				t.Errorf("EffectiveMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
