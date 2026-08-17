package slackbridge

import (
	"encoding/json"
	"strings"
	"testing"
)

// clearSlackEnv unsets all Slack bridge env vars so tests do not leak state
// across each other or pick up variables from the ambient test environment.
func clearSlackEnv(t *testing.T) {
	t.Helper()
	for _, name := range requiredEnvVars {
		t.Setenv(name, "")
	}
}

func TestInspectEnvironmentReturnsValueFreePartialStatus(t *testing.T) {
	clearSlackEnv(t)
	const canary = "xapp-canary-never-serialize"
	t.Setenv(EnvAppToken, canary)
	t.Setenv(EnvTeamID, "T123")
	t.Setenv(EnvChannelID, "C123")

	cfg, status := InspectEnvironment()
	if cfg.AppToken != canary || !status.Present || status.Complete || status.TeamID != "T123" || status.ChannelID != "C123" {
		t.Fatalf("cfg=%#v status=%#v", cfg, status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) || strings.Contains(string(encoded), "app_token") {
		t.Fatalf("status leaked credential data: %s", encoded)
	}
	for _, missing := range []string{EnvBotToken, EnvTargetSessionID} {
		if !strings.Contains(string(encoded), missing) {
			t.Fatalf("status did not name missing variable %q: %s", missing, encoded)
		}
	}
}

func TestLoadConfigFromEnv_AllAbsent_Disabled(t *testing.T) {
	clearSlackEnv(t)

	cfg, enabled, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if enabled {
		t.Error("enabled = true, want false when no env vars are set")
	}
	if cfg != (Config{}) {
		t.Errorf("cfg = %#v, want zero value", cfg)
	}
}

func TestLoadConfigFromEnv_Partial_ErrorNamesOnlyMissingVars(t *testing.T) {
	clearSlackEnv(t)
	t.Setenv(EnvAppToken, "xapp-test-should-not-appear-in-error")
	t.Setenv(EnvChannelID, "C123")

	cfg, enabled, err := LoadConfigFromEnv()
	if enabled {
		t.Error("enabled = true, want false for a partial configuration")
	}
	if err == nil {
		t.Fatal("err = nil, want a missing-vars error for a partial configuration")
	}
	if cfg != (Config{}) {
		t.Errorf("cfg = %#v, want zero value on error", cfg)
	}

	msg := err.Error()
	// The error must name the missing variables NAMEs only, and must never
	// echo the configured token value (never log tokens, acceptance #8).
	if strings.Contains(msg, "xapp-test-should-not-appear-in-error") {
		t.Errorf("error leaks a configured value: %q", msg)
	}
	for _, missing := range []string{EnvBotToken, EnvTeamID, EnvTargetSessionID} {
		if !strings.Contains(msg, missing) {
			t.Errorf("error %q does not name missing var %q", msg, missing)
		}
	}
	if strings.Contains(msg, EnvAppToken) || strings.Contains(msg, EnvChannelID) {
		t.Errorf("error %q should not list already-set vars", msg)
	}
}

func TestLoadConfigFromEnv_AllPresent_Enabled(t *testing.T) {
	clearSlackEnv(t)
	t.Setenv(EnvAppToken, "xapp-token")
	t.Setenv(EnvBotToken, "xoxb-token")
	t.Setenv(EnvTeamID, "T123")
	t.Setenv(EnvChannelID, "C123")
	t.Setenv(EnvTargetSessionID, "sess-1")

	cfg, enabled, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !enabled {
		t.Fatal("enabled = false, want true when all vars are set")
	}
	want := Config{
		AppToken:        "xapp-token",
		BotToken:        "xoxb-token",
		TeamID:          "T123",
		ChannelID:       "C123",
		TargetSessionID: "sess-1",
	}
	if cfg != want {
		t.Errorf("cfg = %#v, want %#v", cfg, want)
	}
}

func TestLoadConfigFromEnv_WhitespaceOnly_TreatedAsAbsent(t *testing.T) {
	clearSlackEnv(t)
	t.Setenv(EnvAppToken, "   ")

	_, enabled, err := LoadConfigFromEnv()
	if enabled {
		t.Error("enabled = true, want false for a whitespace-only value")
	}
	// present==0 branch requires ALL vars trimmed-empty; here only one is set
	// (to whitespace, which trims to empty), so this is the "all absent" path.
	if err != nil {
		t.Errorf("err = %v, want nil (whitespace-only counts as absent)", err)
	}
}
