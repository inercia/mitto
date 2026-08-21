package handlers

import (
	"testing"

	configPkg "github.com/inercia/mitto/internal/config"
)

// TestSanitizeWebConfig_RedactsSharedToken verifies that a configured shared
// bearer token (mitto-7gta.26) is never returned from GET /api/config, since
// it could otherwise be exfiltrated via XSS, screen-sharing, or devtools --
// mirroring the existing Simple.Password redaction.
func TestSanitizeWebConfig_RedactsSharedToken(t *testing.T) {
	cfg := configPkg.WebConfig{
		Auth: &configPkg.WebAuth{
			Simple:      &configPkg.SimpleAuth{Username: "admin", Password: "secret"},
			SharedToken: "s3cr3t-bearer-token",
		},
	}

	sanitized := SanitizeWebConfig(cfg)

	if sanitized.Auth == nil {
		t.Fatal("Auth should not be nil")
	}
	if sanitized.Auth.SharedToken != "" {
		t.Errorf("SharedToken = %q, want empty (redacted)", sanitized.Auth.SharedToken)
	}
	if sanitized.Auth.Simple.Password != "" {
		t.Errorf("Simple.Password = %q, want empty (redacted)", sanitized.Auth.Simple.Password)
	}
	// Username is not a secret and must survive.
	if sanitized.Auth.Simple.Username != "admin" {
		t.Errorf("Simple.Username = %q, want %q", sanitized.Auth.Simple.Username, "admin")
	}

	// Original input must be untouched (deep copy, not mutation in place).
	if cfg.Auth.SharedToken != "s3cr3t-bearer-token" {
		t.Error("SanitizeWebConfig mutated the original config's SharedToken")
	}
	if cfg.Auth.Simple.Password != "secret" {
		t.Error("SanitizeWebConfig mutated the original config's Simple.Password")
	}
}

// TestSanitizeWebConfig_NoTokenConfigured verifies the no-op case: an empty
// SharedToken stays empty and no other fields are disturbed.
func TestSanitizeWebConfig_NoTokenConfigured(t *testing.T) {
	cfg := configPkg.WebConfig{
		Auth: &configPkg.WebAuth{
			Simple: &configPkg.SimpleAuth{Username: "admin", Password: "secret"},
		},
	}

	sanitized := SanitizeWebConfig(cfg)

	if sanitized.Auth.SharedToken != "" {
		t.Errorf("SharedToken = %q, want empty", sanitized.Auth.SharedToken)
	}
}

// TestSanitizeWebConfig_NilAuth verifies SanitizeWebConfig does not panic and
// returns a zero-value Auth when the input has none configured at all.
func TestSanitizeWebConfig_NilAuth(t *testing.T) {
	cfg := configPkg.WebConfig{}

	sanitized := SanitizeWebConfig(cfg)

	if sanitized.Auth != nil {
		t.Errorf("Auth = %+v, want nil", sanitized.Auth)
	}
}

// TestSanitizeWebConfig_SharedTokenOnly verifies redaction also applies when
// only a shared token is configured (no Simple auth at all).
func TestSanitizeWebConfig_SharedTokenOnly(t *testing.T) {
	cfg := configPkg.WebConfig{
		Auth: &configPkg.WebAuth{
			SharedToken: "s3cr3t-bearer-token",
		},
	}

	sanitized := SanitizeWebConfig(cfg)

	if sanitized.Auth.SharedToken != "" {
		t.Errorf("SharedToken = %q, want empty (redacted)", sanitized.Auth.SharedToken)
	}
	if sanitized.Auth.Simple != nil {
		t.Errorf("Simple = %+v, want nil", sanitized.Auth.Simple)
	}
}
