package config

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/secrets"
)

func TestLoadSettings_CreatesDefaultSettings(t *testing.T) {
	// Use temp dir - t.Setenv automatically restores original value
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Verify settings.json doesn't exist
	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatal("settings.json should not exist initially")
	}

	// Load settings - should create from defaults
	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	// Verify settings.json was created
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.json was not created: %v", err)
	}

	// Default config ships with zero ACP servers — discovery guides user on first run
	_ = cfg // config is valid even with no servers
}

func TestGlobalShortcuts_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Persist a couple of sections.
	in := map[string][]ShortcutButton{
		"conversations": {{Prompt: "Commit changes"}},
		"beadsIssue":    {{Icon: "lightning", Prompt: "Start work"}},
		// Empty section must be pruned on save.
		"tasksList": {},
	}
	if err := SetGlobalShortcuts(in); err != nil {
		t.Fatalf("SetGlobalShortcuts failed: %v", err)
	}

	got := GlobalShortcuts()
	if len(got) != 2 {
		t.Fatalf("GlobalShortcuts sections = %d, want 2 (%v)", len(got), got)
	}
	if len(got["conversations"]) != 1 || got["conversations"][0].Prompt != "Commit changes" {
		t.Errorf("conversations = %+v, want one Commit changes button", got["conversations"])
	}
	if len(got["beadsIssue"]) != 1 || got["beadsIssue"][0].Icon != "lightning" {
		t.Errorf("beadsIssue = %+v, want one button with icon lightning", got["beadsIssue"])
	}
	if _, ok := got["tasksList"]; ok {
		t.Errorf("empty tasksList section should have been pruned, got %+v", got["tasksList"])
	}

	// Global shortcuts must survive a full settings load/convert round-trip.
	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}
	if len(cfg.Shortcuts) != 2 {
		t.Errorf("cfg.Shortcuts sections = %d, want 2 (%v)", len(cfg.Shortcuts), cfg.Shortcuts)
	}

	// Clearing all sections removes the field entirely.
	if err := SetGlobalShortcuts(map[string][]ShortcutButton{}); err != nil {
		t.Fatalf("SetGlobalShortcuts(clear) failed: %v", err)
	}
	if got := GlobalShortcuts(); len(got) != 0 {
		t.Errorf("GlobalShortcuts after clear = %v, want empty", got)
	}
}

func TestLoadSettings_ReadsExistingSettings(t *testing.T) {
	// Use temp dir - t.Setenv automatically restores original value
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create a custom settings.json
	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	customSettings := `{
		"acp_servers": [
			{"name": "custom-server", "command": "custom-cmd --acp"}
		],
		"web": {
			"port": 9999
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(customSettings), 0644); err != nil {
		t.Fatalf("failed to create test settings.json: %v", err)
	}

	// Load settings
	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	// Verify custom config was loaded
	if len(cfg.ACPServers) != 1 {
		t.Fatalf("expected 1 ACP server, got %d", len(cfg.ACPServers))
	}
	if cfg.ACPServers[0].Name != "custom-server" {
		t.Errorf("server name = %q, want %q", cfg.ACPServers[0].Name, "custom-server")
	}
	if cfg.Web.Port != 9999 {
		t.Errorf("web port = %d, want %d", cfg.Web.Port, 9999)
	}
}

func TestConfigToSettings_RoundTrip(t *testing.T) {
	original := &Config{
		ACPServers: []ACPServer{
			{Name: "server1", Command: "cmd1", Cwd: "/home/user/projects", Tags: []string{"coding", "fast"}},
			{Name: "server2", Command: "cmd2"}, // no cwd, no tags
		},
		Web: WebConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
	}

	// Convert to Settings
	settings := ConfigToSettings(original)

	// Convert back to Config
	result := settings.ToConfig()

	// Verify round-trip
	if len(result.ACPServers) != len(original.ACPServers) {
		t.Fatalf("ACPServers count mismatch: got %d, want %d", len(result.ACPServers), len(original.ACPServers))
	}
	for i := range original.ACPServers {
		if result.ACPServers[i].Name != original.ACPServers[i].Name {
			t.Errorf("ACPServers[%d].Name = %q, want %q", i, result.ACPServers[i].Name, original.ACPServers[i].Name)
		}
		if result.ACPServers[i].Command != original.ACPServers[i].Command {
			t.Errorf("ACPServers[%d].Command = %q, want %q", i, result.ACPServers[i].Command, original.ACPServers[i].Command)
		}
		if result.ACPServers[i].Cwd != original.ACPServers[i].Cwd {
			t.Errorf("ACPServers[%d].Cwd = %q, want %q", i, result.ACPServers[i].Cwd, original.ACPServers[i].Cwd)
		}
	}

	// Verify tags survive round-trip
	if len(result.ACPServers[0].Tags) != 2 {
		t.Fatalf("ACPServers[0].Tags count = %d, want 2", len(result.ACPServers[0].Tags))
	}
	if result.ACPServers[0].Tags[0] != "coding" {
		t.Errorf("ACPServers[0].Tags[0] = %q, want %q", result.ACPServers[0].Tags[0], "coding")
	}
	if result.ACPServers[0].Tags[1] != "fast" {
		t.Errorf("ACPServers[0].Tags[1] = %q, want %q", result.ACPServers[0].Tags[1], "fast")
	}
	// Server2 should have no tags
	if len(result.ACPServers[1].Tags) != 0 {
		t.Errorf("ACPServers[1].Tags count = %d, want 0", len(result.ACPServers[1].Tags))
	}

	if result.Web.Host != original.Web.Host {
		t.Errorf("Web.Host = %q, want %q", result.Web.Host, original.Web.Host)
	}
	if result.Web.Port != original.Web.Port {
		t.Errorf("Web.Port = %d, want %d", result.Web.Port, original.Web.Port)
	}
}

func TestConfigToSettings_RoundTripWithPrompts(t *testing.T) {
	original := &Config{
		ACPServers: []ACPServer{
			{
				Name:    "server1",
				Command: "cmd1",
				Prompts: []WebPrompt{
					{Name: "Prompt1", Prompt: "Do something"},
					{Name: "Prompt2", Prompt: "Do something else"},
				},
			},
			{Name: "server2", Command: "cmd2"}, // no prompts
		},
		Prompts: []WebPrompt{
			{Name: "Global", Prompt: "Global prompt"},
		},
		Web: WebConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
	}

	// Convert to Settings
	settings := ConfigToSettings(original)

	// Convert back to Config
	result := settings.ToConfig()

	// Verify server prompts round-trip
	if len(result.ACPServers[0].Prompts) != 2 {
		t.Fatalf("server1 prompts count = %d, want 2", len(result.ACPServers[0].Prompts))
	}
	if result.ACPServers[0].Prompts[0].Name != "Prompt1" {
		t.Errorf("server1.Prompts[0].Name = %q, want %q", result.ACPServers[0].Prompts[0].Name, "Prompt1")
	}
	if result.ACPServers[0].Prompts[1].Prompt != "Do something else" {
		t.Errorf("server1.Prompts[1].Prompt = %q, want %q", result.ACPServers[0].Prompts[1].Prompt, "Do something else")
	}

	// Verify server2 has no prompts
	if len(result.ACPServers[1].Prompts) != 0 {
		t.Errorf("server2 prompts count = %d, want 0", len(result.ACPServers[1].Prompts))
	}

	// Verify global prompts round-trip
	if len(result.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1", len(result.Prompts))
	}
	if result.Prompts[0].Name != "Global" {
		t.Errorf("Prompts[0].Name = %q, want %q", result.Prompts[0].Name, "Global")
	}
}

func TestConfigToSettings_RoundTripWithSession(t *testing.T) {
	original := &Config{
		ACPServers: []ACPServer{
			{Name: "server1", Command: "cmd1"},
		},
		Web: WebConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Session: &SessionConfig{
			MaxMessagesPerSession: 500,
			MaxSessionSizeBytes:   50000000,
		},
	}

	// Convert to Settings
	settings := ConfigToSettings(original)

	// Verify Session is preserved
	if settings.Session == nil {
		t.Fatal("Session config should not be nil after conversion")
	}
	if settings.Session.MaxMessagesPerSession != 500 {
		t.Errorf("Session.MaxMessagesPerSession = %d, want 500", settings.Session.MaxMessagesPerSession)
	}
	if settings.Session.MaxSessionSizeBytes != 50000000 {
		t.Errorf("Session.MaxSessionSizeBytes = %d, want 50000000", settings.Session.MaxSessionSizeBytes)
	}

	// Convert back to Config
	result := settings.ToConfig()

	// Verify round-trip
	if result.Session == nil {
		t.Fatal("Session config should not be nil after round-trip")
	}
	if result.Session.MaxMessagesPerSession != original.Session.MaxMessagesPerSession {
		t.Errorf("Session.MaxMessagesPerSession = %d, want %d",
			result.Session.MaxMessagesPerSession, original.Session.MaxMessagesPerSession)
	}
	if result.Session.MaxSessionSizeBytes != original.Session.MaxSessionSizeBytes {
		t.Errorf("Session.MaxSessionSizeBytes = %d, want %d",
			result.Session.MaxSessionSizeBytes, original.Session.MaxSessionSizeBytes)
	}
}

func TestLoadSettings_WithSessionConfig(t *testing.T) {
	// Use temp dir - t.Setenv automatically restores original value
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create a settings.json with session config
	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	customSettings := `{
		"acp_servers": [
			{"name": "test-server", "command": "test-cmd"}
		],
		"session": {
			"max_messages_per_session": 1000,
			"max_session_size_bytes": 100000000
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(customSettings), 0644); err != nil {
		t.Fatalf("failed to create test settings.json: %v", err)
	}

	// Load settings
	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	// Verify session config was loaded
	if cfg.Session == nil {
		t.Fatal("Session config should not be nil")
	}
	if cfg.Session.MaxMessagesPerSession != 1000 {
		t.Errorf("Session.MaxMessagesPerSession = %d, want 1000", cfg.Session.MaxMessagesPerSession)
	}
	if cfg.Session.MaxSessionSizeBytes != 100000000 {
		t.Errorf("Session.MaxSessionSizeBytes = %d, want 100000000", cfg.Session.MaxSessionSizeBytes)
	}
}

func TestLoadSettings_NoSessionConfig(t *testing.T) {
	// Use temp dir - t.Setenv automatically restores original value
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create a settings.json without session config
	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	customSettings := `{
		"acp_servers": [
			{"name": "test-server", "command": "test-cmd"}
		]
	}`
	if err := os.WriteFile(settingsPath, []byte(customSettings), 0644); err != nil {
		t.Fatalf("failed to create test settings.json: %v", err)
	}

	// Load settings
	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	// Session config should be nil (not set)
	if cfg.Session != nil {
		t.Errorf("Session config should be nil when not configured, got %+v", cfg.Session)
	}
}

func TestLoadSettingsWithFallback_MergesWebAuthFromSettings(t *testing.T) {
	// This test verifies that when an RC file exists, Web.Auth settings
	// from settings.json are preserved in the merged config.
	// This is important because Auth settings are configured via the UI
	// and saved to settings.json, not in the RC file.

	// Use temp dir for both MITTO_DIR and HOME (for RC file)
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("HOME", tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Create settings.json with auth configured
	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	settingsWithAuth := `{
  "acp_servers": [
    {"name": "settings-server", "command": "settings-cmd"}
  ],
  "web": {
    "host": "0.0.0.0",
    "external_port": 8080,
    "auth": {
      "simple": {
        "username": "admin",
        "password": "test-password"
      }
    },
    "hooks": {
      "up": {"command": "tailscale funnel $PORT"},
      "down": {"command": "pkill tailscale"}
    }
  }
}`
	if err := os.WriteFile(settingsPath, []byte(settingsWithAuth), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	// Create RC file WITHOUT auth (typical setup - RC file has ACP servers,
	// settings.json has UI-configured settings like auth)
	rcPath := filepath.Join(tmpDir, ".mittorc")
	rcWithoutAuth := `
acp:
  - rc-server:
      command: "rc-cmd"
`
	if err := os.WriteFile(rcPath, []byte(rcWithoutAuth), 0644); err != nil {
		t.Fatalf("failed to create .mittorc: %v", err)
	}

	// Load settings with fallback
	result, err := LoadSettingsWithFallback()
	if err != nil {
		t.Fatalf("LoadSettingsWithFallback() failed: %v", err)
	}

	cfg := result.Config

	// Verify Auth was merged from settings.json
	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth should not be nil - it should be merged from settings.json")
	}
	if cfg.Web.Auth.Simple == nil {
		t.Fatal("Web.Auth.Simple should not be nil")
	}
	if cfg.Web.Auth.Simple.Username != "admin" {
		t.Errorf("Web.Auth.Simple.Username = %q, want %q", cfg.Web.Auth.Simple.Username, "admin")
	}
	// Password might be loaded from keychain on macOS, or from settings on other platforms
	// On non-macOS, it should be "test-password"
	// On macOS, loadKeychainPassword might have been called, but since we're using
	// test data (not real keychain), the password should remain as-is
	if cfg.Web.Auth.Simple.Password != "test-password" {
		t.Errorf("Web.Auth.Simple.Password = %q, want %q", cfg.Web.Auth.Simple.Password, "test-password")
	}

	// Verify Host was merged from settings.json (external access enabled)
	if cfg.Web.Host != "0.0.0.0" {
		t.Errorf("Web.Host = %q, want %q", cfg.Web.Host, "0.0.0.0")
	}

	// Verify ExternalPort was merged from settings.json
	if cfg.Web.ExternalPort != 8080 {
		t.Errorf("Web.ExternalPort = %d, want %d", cfg.Web.ExternalPort, 8080)
	}

	// Verify Hooks were merged from settings.json
	if cfg.Web.Hooks.Up.Command != "tailscale funnel $PORT" {
		t.Errorf("Web.Hooks.Up.Command = %q, want %q", cfg.Web.Hooks.Up.Command, "tailscale funnel $PORT")
	}
	if cfg.Web.Hooks.Down.Command != "pkill tailscale" {
		t.Errorf("Web.Hooks.Down.Command = %q, want %q", cfg.Web.Hooks.Down.Command, "pkill tailscale")
	}

	// Verify ACP servers include both RC file and settings servers
	// RC file servers have priority and come first
	if len(cfg.ACPServers) < 2 {
		t.Errorf("expected at least 2 ACP servers, got %d", len(cfg.ACPServers))
	}

	// First server should be from RC file
	foundRCServer := false
	for _, srv := range cfg.ACPServers {
		if srv.Name == "rc-server" {
			foundRCServer = true
			break
		}
	}
	if !foundRCServer {
		t.Error("expected to find rc-server in merged config")
	}
}

func TestParseMemoryRecycleThreshold(t *testing.T) {
	const gib = uint64(1024) * 1024 * 1024

	tests := []struct {
		value       string
		wantBytes   uint64
		wantEnabled bool
	}{
		{"", 0, false},
		{"disabled", 0, false},
		{"3g", 3 * gib, true},
		{"4g", 4 * gib, true},
		{"6g", 6 * gib, true},
		{"8g", 8 * gib, true},
		{"bogus", 0, false}, // unknown → disabled (safe)
	}

	for _, tc := range tests {
		c := &SessionConfig{MemoryRecycleThreshold: tc.value}
		gotBytes, gotEnabled := c.ParseMemoryRecycleThreshold()
		if gotBytes != tc.wantBytes || gotEnabled != tc.wantEnabled {
			t.Errorf("ParseMemoryRecycleThreshold(%q) = (%d, %t), want (%d, %t)",
				tc.value, gotBytes, gotEnabled, tc.wantBytes, tc.wantEnabled)
		}
	}

	// Nil receiver must be safe and report disabled.
	var nilCfg *SessionConfig
	if gotBytes, gotEnabled := nilCfg.ParseMemoryRecycleThreshold(); gotBytes != 0 || gotEnabled {
		t.Errorf("nil ParseMemoryRecycleThreshold() = (%d, %t), want (0, false)", gotBytes, gotEnabled)
	}
}

func TestParseAgentInactivityTimeout(t *testing.T) {
	tests := []struct {
		value       string
		wantDur     time.Duration
		wantEnabled bool
	}{
		{"", 10 * time.Minute, true}, // empty defaults to enabled, unlike MemoryRecycleThreshold
		{"disabled", 0, false},
		{"5m", 5 * time.Minute, true},
		{"10m", 10 * time.Minute, true},
		{"15m", 15 * time.Minute, true},
		{"30m", 30 * time.Minute, true},
		{"bogus", 10 * time.Minute, true}, // unknown → default (10m, enabled)
	}

	for _, tc := range tests {
		c := &SessionConfig{AgentInactivityTimeout: tc.value}
		gotDur, gotEnabled := c.ParseAgentInactivityTimeout()
		if gotDur != tc.wantDur || gotEnabled != tc.wantEnabled {
			t.Errorf("ParseAgentInactivityTimeout(%q) = (%s, %t), want (%s, %t)",
				tc.value, gotDur, gotEnabled, tc.wantDur, tc.wantEnabled)
		}
	}

	// Nil receiver must be safe and report the enabled default.
	var nilCfg *SessionConfig
	if gotDur, gotEnabled := nilCfg.ParseAgentInactivityTimeout(); gotDur != 10*time.Minute || !gotEnabled {
		t.Errorf("nil ParseAgentInactivityTimeout() = (%s, %t), want (10m, true)", gotDur, gotEnabled)
	}
}

// TestParseMcpInitTimeout guards the SessionConfig accessor added for mitto-8ul.1.
// Empty → default 240s (enabled); "disabled" → 0/false; explicit durations parse.
// Unknown values fall back to the enabled default rather than silently disabling.
func TestParseMcpInitTimeout(t *testing.T) {
	tests := []struct {
		value       string
		wantDur     time.Duration
		wantEnabled bool
	}{
		{"", 240 * time.Second, true},
		{"disabled", 0, false},
		{"120s", 120 * time.Second, true},
		{"2m", 120 * time.Second, true},
		{"240s", 240 * time.Second, true},
		{"4m", 240 * time.Second, true},
		{"300s", 300 * time.Second, true},
		{"5m", 300 * time.Second, true},
		{"bogus", 240 * time.Second, true},
	}
	for _, tc := range tests {
		c := &SessionConfig{McpInitTimeout: tc.value}
		gotDur, gotEnabled := c.ParseMcpInitTimeout()
		if gotDur != tc.wantDur || gotEnabled != tc.wantEnabled {
			t.Errorf("ParseMcpInitTimeout(%q) = (%s, %t), want (%s, %t)",
				tc.value, gotDur, gotEnabled, tc.wantDur, tc.wantEnabled)
		}
	}
}

// TestPrewarmConfig_Defaults guards the PrewarmConfig accessor/parse helpers
// added for mitto-mw0. Empty struct and nil receiver must both return the
// documented defaults; unknown string values fall back to the defaults; the
// "disabled" MaxPinDuration returns (0, false).
func TestPrewarmConfig_Defaults(t *testing.T) {
	// Nil receiver → all defaults.
	var nilCfg *PrewarmConfig
	if d, ok := nilCfg.ParseSessionNewFast(); d != 10*time.Second || !ok {
		t.Errorf("nil ParseSessionNewFast() = (%s, %t), want (10s, true)", d, ok)
	}
	if d, ok := nilCfg.ParseMcpReady(); d != 10*time.Second || !ok {
		t.Errorf("nil ParseMcpReady() = (%s, %t), want (10s, true)", d, ok)
	}
	if d, ok := nilCfg.ParseMaxPinDuration(); d != 30*time.Minute || !ok {
		t.Errorf("nil ParseMaxPinDuration() = (%s, %t), want (30m, true)", d, ok)
	}
	if n := nilCfg.GetHealthyProbesToUnpin(); n != 3 {
		t.Errorf("nil GetHealthyProbesToUnpin() = %d, want 3", n)
	}
	if n := nilCfg.GetMaxPinnedWorkspaces(); n != 5 {
		t.Errorf("nil GetMaxPinnedWorkspaces() = %d, want 5", n)
	}

	// Empty struct → same defaults as nil.
	empty := &PrewarmConfig{}
	if d, _ := empty.ParseSessionNewFast(); d != 10*time.Second {
		t.Errorf("empty ParseSessionNewFast() = %s, want 10s", d)
	}
	if d, _ := empty.ParseMcpReady(); d != 10*time.Second {
		t.Errorf("empty ParseMcpReady() = %s, want 10s", d)
	}
	if d, _ := empty.ParseMaxPinDuration(); d != 30*time.Minute {
		t.Errorf("empty ParseMaxPinDuration() = %s, want 30m", d)
	}
	if n := empty.GetHealthyProbesToUnpin(); n != 3 {
		t.Errorf("empty GetHealthyProbesToUnpin() = %d, want 3", n)
	}
	if n := empty.GetMaxPinnedWorkspaces(); n != 5 {
		t.Errorf("empty GetMaxPinnedWorkspaces() = %d, want 5", n)
	}

	// Explicit values parse; unknown values fall back to default.
	cases := []struct {
		name  string
		cfg   PrewarmConfig
		snf   time.Duration
		mcp   time.Duration
		pin   time.Duration
		pinOk bool
	}{
		{"explicit", PrewarmConfig{SessionNewFast: "5s", McpReady: "20s", MaxPinDuration: "1h"}, 5 * time.Second, 20 * time.Second, time.Hour, true},
		{"disabled_pin", PrewarmConfig{MaxPinDuration: "disabled"}, 10 * time.Second, 10 * time.Second, 0, false},
		{"unknown", PrewarmConfig{SessionNewFast: "bogus", McpReady: "bogus", MaxPinDuration: "bogus"}, 10 * time.Second, 10 * time.Second, 30 * time.Minute, true},
	}
	for _, tc := range cases {
		if d, _ := tc.cfg.ParseSessionNewFast(); d != tc.snf {
			t.Errorf("[%s] ParseSessionNewFast() = %s, want %s", tc.name, d, tc.snf)
		}
		if d, _ := tc.cfg.ParseMcpReady(); d != tc.mcp {
			t.Errorf("[%s] ParseMcpReady() = %s, want %s", tc.name, d, tc.mcp)
		}
		d, ok := tc.cfg.ParseMaxPinDuration()
		if d != tc.pin || ok != tc.pinOk {
			t.Errorf("[%s] ParseMaxPinDuration() = (%s, %t), want (%s, %t)", tc.name, d, ok, tc.pin, tc.pinOk)
		}
	}
}

// TestPrewarmConfig_AuxPrewarmSchedule guards the mitto-cgc per-purpose
// staggered auxiliary-prewarm schedule and the mitto-7yj fork/multiplex
// split. Verifies nil-safety, per-purpose overrides, empty/invalid fallback,
// nondecreasing Delay ordering, and that fork-per-session picks the widely
// spread default set.
func TestPrewarmConfig_AuxPrewarmSchedule(t *testing.T) {
	// Nil receiver, multiplex (false) → auggie defaults in tier order
	// (mitto-7yj: no two purposes share the 0s slot).
	var nilCfg *PrewarmConfig
	got := nilCfg.AuxPrewarmSchedule(false)
	want := []AuxPrewarmEntry{
		{Purpose: "mcp-check", Delay: 0},
		{Purpose: "mcp-tools", Delay: 2 * time.Second},
		{Purpose: "title-gen", Delay: 8 * time.Second},
		{Purpose: "follow-up", Delay: 12 * time.Second},
	}
	if len(got) != len(want) {
		t.Fatalf("nil AuxPrewarmSchedule(false) len = %d, want %d (got=%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nil AuxPrewarmSchedule(false)[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Empty PrewarmConfig (no AuxSchedule), multiplex → same as nil.
	got = (&PrewarmConfig{}).AuxPrewarmSchedule(false)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("empty AuxPrewarmSchedule(false)[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Fork-per-session (true) → widely spread defaults (mitto-7yj).
	wantFork := []AuxPrewarmEntry{
		{Purpose: "mcp-check", Delay: 0},
		{Purpose: "mcp-tools", Delay: 8 * time.Second},
		{Purpose: "title-gen", Delay: 20 * time.Second},
		{Purpose: "follow-up", Delay: 35 * time.Second},
	}
	got = nilCfg.AuxPrewarmSchedule(true)
	if len(got) != len(wantFork) {
		t.Fatalf("nil AuxPrewarmSchedule(true) len = %d, want %d (got=%+v)", len(got), len(wantFork), got)
	}
	for i := range wantFork {
		if got[i] != wantFork[i] {
			t.Errorf("nil AuxPrewarmSchedule(true)[%d] = %+v, want %+v", i, got[i], wantFork[i])
		}
	}
	got = (&PrewarmConfig{}).AuxPrewarmSchedule(true)
	for i := range wantFork {
		if got[i] != wantFork[i] {
			t.Errorf("empty AuxPrewarmSchedule(true)[%d] = %+v, want %+v", i, got[i], wantFork[i])
		}
	}

	// Override title-gen only (multiplex) → other purposes keep defaults;
	// ordering still nondecreasing (title-gen 3s < follow-up 12s).
	cfg := &PrewarmConfig{AuxSchedule: &AuxScheduleConfig{TitleGen: "3s"}}
	got = cfg.AuxPrewarmSchedule(false)
	wantOverride := []AuxPrewarmEntry{
		{Purpose: "mcp-check", Delay: 0},
		{Purpose: "mcp-tools", Delay: 2 * time.Second},
		{Purpose: "title-gen", Delay: 3 * time.Second},
		{Purpose: "follow-up", Delay: 12 * time.Second},
	}
	for i := range wantOverride {
		if got[i] != wantOverride[i] {
			t.Errorf("override AuxPrewarmSchedule(false)[%d] = %+v, want %+v", i, got[i], wantOverride[i])
		}
	}

	// Override also wins over fork defaults (mitto-7yj: explicitly-set
	// AuxScheduleConfig strings override both default sets). mcp-tools is
	// NOT overridden here so it keeps the fork default (8s), which sorts
	// after the overridden title-gen (3s).
	cfg = &PrewarmConfig{AuxSchedule: &AuxScheduleConfig{TitleGen: "3s"}}
	got = cfg.AuxPrewarmSchedule(true)
	wantOverrideFork := []AuxPrewarmEntry{
		{Purpose: "mcp-check", Delay: 0},
		{Purpose: "title-gen", Delay: 3 * time.Second},
		{Purpose: "mcp-tools", Delay: 8 * time.Second},
		{Purpose: "follow-up", Delay: 35 * time.Second},
	}
	for i := range wantOverrideFork {
		if got[i] != wantOverrideFork[i] {
			t.Errorf("override AuxPrewarmSchedule(true)[%d] = %+v, want %+v", i, got[i], wantOverrideFork[i])
		}
	}

	// Empty and invalid duration strings both fall back to per-purpose
	// multiplex defaults.
	cfg = &PrewarmConfig{AuxSchedule: &AuxScheduleConfig{
		McpCheck: "",
		McpTools: "not-a-duration",
		TitleGen: "",
		FollowUp: "bogus",
	}}
	got = cfg.AuxPrewarmSchedule(false)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fallback AuxPrewarmSchedule(false)[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Any override still yields nondecreasing Delay order (multiplex).
	cfg = &PrewarmConfig{AuxSchedule: &AuxScheduleConfig{
		McpCheck: "30s", // pushes tier-0 past tier-1/tier-2 defaults
		TitleGen: "1s",
		FollowUp: "2s",
	}}
	got = cfg.AuxPrewarmSchedule(false)
	for i := 1; i < len(got); i++ {
		if got[i-1].Delay > got[i].Delay {
			t.Errorf("AuxPrewarmSchedule(false) not nondecreasing at [%d]: %+v", i, got)
			break
		}
	}
	// And fork.
	got = cfg.AuxPrewarmSchedule(true)
	for i := 1; i < len(got); i++ {
		if got[i-1].Delay > got[i].Delay {
			t.Errorf("AuxPrewarmSchedule(true) not nondecreasing at [%d]: %+v", i, got)
			break
		}
	}
}

// TestPrewarmConfig_LoadFromSettings verifies the Prewarm section is wired
// through settings.json load and reaches Config.Prewarm intact (mitto-mw0).
func TestPrewarmConfig_LoadFromSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	customSettings := `{
		"acp_servers": [{"name": "test", "command": "cmd"}],
		"prewarm": {
			"session_new_fast": "5s",
			"mcp_ready": "20s",
			"healthy_probes_to_unpin": 4,
			"max_pin_duration": "1h",
			"max_pinned_workspaces": 7
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(customSettings), 0644); err != nil {
		t.Fatalf("failed to create test settings.json: %v", err)
	}
	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}
	if cfg.Prewarm == nil {
		t.Fatal("Prewarm config should not be nil")
	}
	if d, _ := cfg.Prewarm.ParseSessionNewFast(); d != 5*time.Second {
		t.Errorf("SessionNewFast = %s, want 5s", d)
	}
	if d, _ := cfg.Prewarm.ParseMcpReady(); d != 20*time.Second {
		t.Errorf("McpReady = %s, want 20s", d)
	}
	if n := cfg.Prewarm.GetHealthyProbesToUnpin(); n != 4 {
		t.Errorf("HealthyProbesToUnpin = %d, want 4", n)
	}
	if d, ok := cfg.Prewarm.ParseMaxPinDuration(); d != time.Hour || !ok {
		t.Errorf("MaxPinDuration = (%s, %t), want (1h, true)", d, ok)
	}
	if n := cfg.Prewarm.GetMaxPinnedWorkspaces(); n != 7 {
		t.Errorf("MaxPinnedWorkspaces = %d, want 7", n)
	}
}

func TestContextFlushCommand_RoundTrip(t *testing.T) {
	original := &Config{
		ACPServers: []ACPServer{
			{Name: "server1", Command: "cmd1", ContextFlushCommand: "/clear"},
			{Name: "server2", Command: "cmd2"}, // no ContextFlushCommand
		},
	}

	settings := ConfigToSettings(original)

	// Verify JSON tag is present
	if settings.ACPServers[0].ContextFlushCommand != "/clear" {
		t.Errorf("ACPServers[0].ContextFlushCommand = %q, want %q", settings.ACPServers[0].ContextFlushCommand, "/clear")
	}
	if settings.ACPServers[1].ContextFlushCommand != "" {
		t.Errorf("ACPServers[1].ContextFlushCommand = %q, want empty", settings.ACPServers[1].ContextFlushCommand)
	}

	result := settings.ToConfig()

	if result.ACPServers[0].ContextFlushCommand != "/clear" {
		t.Errorf("round-trip ACPServers[0].ContextFlushCommand = %q, want %q", result.ACPServers[0].ContextFlushCommand, "/clear")
	}
	if result.ACPServers[1].ContextFlushCommand != "" {
		t.Errorf("round-trip ACPServers[1].ContextFlushCommand = %q, want empty", result.ACPServers[1].ContextFlushCommand)
	}
}

func TestConfigToSettings_RoundTripWithModels(t *testing.T) {
	original := &Config{
		Models: []ModelProfile{
			{
				Name:     "Opus",
				Criteria: &ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"},
				Tags:     []string{"Smartest", "Expensive"},
			},
			{
				Name: "TagsOnly",
				Tags: []string{"Fast"},
			},
		},
		Web: WebConfig{Host: "127.0.0.1", Port: 8080},
	}

	// Round-trip through Settings and JSON (settings.json) to ensure no loss.
	settings := ConfigToSettings(original)
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("json.Marshal(settings) failed: %v", err)
	}
	var reloaded Settings
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("json.Unmarshal(settings) failed: %v", err)
	}
	result := reloaded.ToConfig()

	if len(result.Models) != 2 {
		t.Fatalf("Models count = %d, want 2", len(result.Models))
	}

	opus := result.Models[0]
	if opus.Name != "Opus" {
		t.Errorf("Models[0].Name = %q, want %q", opus.Name, "Opus")
	}
	if opus.Criteria == nil {
		t.Fatalf("Models[0].Criteria is nil after round-trip")
	}
	if opus.Criteria.MatchMode != "contains" || opus.Criteria.Pattern != "Opus" {
		t.Errorf("Models[0].Criteria = %+v, want {contains Opus}", *opus.Criteria)
	}
	if len(opus.Tags) != 2 || opus.Tags[0] != "Smartest" || opus.Tags[1] != "Expensive" {
		t.Errorf("Models[0].Tags = %v, want [Smartest Expensive]", opus.Tags)
	}

	tagsOnly := result.Models[1]
	if tagsOnly.Name != "TagsOnly" {
		t.Errorf("Models[1].Name = %q, want %q", tagsOnly.Name, "TagsOnly")
	}
	if tagsOnly.Criteria != nil {
		t.Errorf("Models[1].Criteria = %+v, want nil", tagsOnly.Criteria)
	}
	if len(tagsOnly.Tags) != 1 || tagsOnly.Tags[0] != "Fast" {
		t.Errorf("Models[1].Tags = %v, want [Fast]", tagsOnly.Tags)
	}
}

// TestMigrateSettingsPeriodicKeys_MovesOldToNew verifies that legacy periodic_*
// keys nested under "session" and "conversations" are moved to their loop_*
// equivalents in-place, and that the change is reported.
func TestMigrateSettingsPeriodicKeys_MovesOldToNew(t *testing.T) {
	raw := map[string]interface{}{
		"session": map[string]interface{}{
			"startup_periodic_delay_seconds": float64(15),
			"periodic_suspend_timeout":       "30m",
		},
		"conversations": map[string]interface{}{
			"max_periodic_iterations":               float64(42),
			"min_periodic_completion_delay_seconds": float64(10),
		},
	}

	changed := migrateSettingsPeriodicKeys(raw)
	if !changed {
		t.Fatal("expected changed=true when legacy keys are present")
	}

	session := raw["session"].(map[string]interface{})
	if v, ok := session["startup_periodic_delay_seconds"]; ok {
		t.Errorf("startup_periodic_delay_seconds should have been removed, still present: %v", v)
	}
	if v := session["startup_loop_delay_seconds"]; v != float64(15) {
		t.Errorf("startup_loop_delay_seconds = %v, want 15", v)
	}
	if v, ok := session["periodic_suspend_timeout"]; ok {
		t.Errorf("periodic_suspend_timeout should have been removed, still present: %v", v)
	}
	if v := session["loop_suspend_timeout"]; v != "30m" {
		t.Errorf("loop_suspend_timeout = %v, want 30m", v)
	}

	conversations := raw["conversations"].(map[string]interface{})
	if v, ok := conversations["max_periodic_iterations"]; ok {
		t.Errorf("max_periodic_iterations should have been removed, still present: %v", v)
	}
	if v := conversations["max_loop_iterations"]; v != float64(42) {
		t.Errorf("max_loop_iterations = %v, want 42", v)
	}
	if v, ok := conversations["min_periodic_completion_delay_seconds"]; ok {
		t.Errorf("min_periodic_completion_delay_seconds should have been removed, still present: %v", v)
	}
	if v := conversations["min_loop_completion_delay_seconds"]; v != float64(10) {
		t.Errorf("min_loop_completion_delay_seconds = %v, want 10", v)
	}
}

// TestMigrateSettingsPeriodicKeys_Idempotent verifies that running the migration
// twice in a row on an already-migrated map is a no-op (second call reports no change).
func TestMigrateSettingsPeriodicKeys_Idempotent(t *testing.T) {
	raw := map[string]interface{}{
		"session": map[string]interface{}{
			"startup_periodic_delay_seconds": float64(15),
		},
	}

	if !migrateSettingsPeriodicKeys(raw) {
		t.Fatal("expected changed=true on first run")
	}
	if migrateSettingsPeriodicKeys(raw) {
		t.Error("expected changed=false on idempotent re-run")
	}
}

// TestMigrateSettingsPeriodicKeys_NewKeyedUntouched verifies that a settings map
// already using the new loop_* keys is left completely untouched.
func TestMigrateSettingsPeriodicKeys_NewKeyedUntouched(t *testing.T) {
	raw := map[string]interface{}{
		"session": map[string]interface{}{
			"startup_loop_delay_seconds": float64(20),
			"loop_suspend_timeout":       "1h",
		},
		"conversations": map[string]interface{}{
			"max_loop_iterations":               float64(99),
			"min_loop_completion_delay_seconds": float64(7),
		},
	}

	if migrateSettingsPeriodicKeys(raw) {
		t.Error("expected changed=false for an already new-keyed map")
	}

	session := raw["session"].(map[string]interface{})
	if v := session["startup_loop_delay_seconds"]; v != float64(20) {
		t.Errorf("startup_loop_delay_seconds = %v, want 20", v)
	}
	if v := session["loop_suspend_timeout"]; v != "1h" {
		t.Errorf("loop_suspend_timeout = %v, want 1h", v)
	}
}

// TestMigrateSettingsPeriodicKeys_PartialOldNewMix verifies that when a key has
// BOTH the old and the new name present, the old value is not moved (new wins,
// old moved only when new absent), while sibling old-only keys still migrate.
func TestMigrateSettingsPeriodicKeys_PartialOldNewMix(t *testing.T) {
	raw := map[string]interface{}{
		"session": map[string]interface{}{
			// Both present: new key must win untouched.
			"startup_periodic_delay_seconds": float64(15),
			"startup_loop_delay_seconds":     float64(20),
			// Only old present: must migrate.
			"periodic_suspend_timeout": "30m",
		},
	}

	if !migrateSettingsPeriodicKeys(raw) {
		t.Fatal("expected changed=true because periodic_suspend_timeout has no new-key counterpart")
	}

	session := raw["session"].(map[string]interface{})
	// Old key with an existing new counterpart is left as-is (not deleted, not moved).
	if v := session["startup_loop_delay_seconds"]; v != float64(20) {
		t.Errorf("startup_loop_delay_seconds = %v, want 20 (new value preserved)", v)
	}
	if v, ok := session["startup_periodic_delay_seconds"]; !ok || v != float64(15) {
		t.Errorf("startup_periodic_delay_seconds should be left untouched when new key already present, got %v (ok=%v)", v, ok)
	}
	// Old-only key migrates normally.
	if v, ok := session["periodic_suspend_timeout"]; ok {
		t.Errorf("periodic_suspend_timeout should have been removed, still present: %v", v)
	}
	if v := session["loop_suspend_timeout"]; v != "30m" {
		t.Errorf("loop_suspend_timeout = %v, want 30m", v)
	}
}

// TestMigrateSettingsPeriodicKeys_NoSectionsPresent verifies the migration is a
// safe no-op when neither "session" nor "conversations" objects are present.
func TestMigrateSettingsPeriodicKeys_NoSectionsPresent(t *testing.T) {
	raw := map[string]interface{}{
		"web": map[string]interface{}{"port": float64(8080)},
	}
	if migrateSettingsPeriodicKeys(raw) {
		t.Error("expected changed=false when session/conversations sections are absent")
	}
}

// TestLoadSettings_MigratesLegacyPeriodicKeys is an end-to-end test: an
// old-keyed settings.json on disk is migrated to loop_* keys on load, values
// are preserved under the new names, the on-disk file is rewritten, and a
// second load is a no-op (idempotent; file content unchanged).
func TestLoadSettings_MigratesLegacyPeriodicKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	legacySettings := `{
		"acp_servers": [],
		"web": {"port": 9999},
		"session": {
			"startup_periodic_delay_seconds": 15,
			"periodic_suspend_timeout": "30m"
		},
		"conversations": {
			"max_periodic_iterations": 42,
			"min_periodic_completion_delay_seconds": 10
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(legacySettings), 0644); err != nil {
		t.Fatalf("failed to create test settings.json: %v", err)
	}

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	if got := cfg.Session.GetStartupLoopDelay(); got.Seconds() != 15 {
		t.Errorf("GetStartupLoopDelay() = %v, want 15s", got)
	}
	if got := cfg.Session.GetLoopSuspendTimeout(); got != "30m" {
		t.Errorf("GetLoopSuspendTimeout() = %q, want %q", got, "30m")
	}
	if got := cfg.Conversations.GetMaxLoopIterations(); got != 42 {
		t.Errorf("GetMaxLoopIterations() = %d, want 42", got)
	}
	if got := cfg.Conversations.GetMinLoopCompletionDelaySeconds(); got != 10 {
		t.Errorf("GetMinLoopCompletionDelaySeconds() = %d, want 10", got)
	}

	// The on-disk file must have been rewritten to the new keys.
	rewritten, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json after migration: %v", err)
	}
	if strings.Contains(string(rewritten), "periodic") {
		t.Errorf("settings.json still contains a legacy periodic_* key after migration:\n%s", rewritten)
	}
	if !strings.Contains(string(rewritten), "startup_loop_delay_seconds") {
		t.Errorf("settings.json missing startup_loop_delay_seconds after migration:\n%s", rewritten)
	}

	// Second load must be idempotent: file content stays exactly the same.
	if _, err := LoadSettings(); err != nil {
		t.Fatalf("second LoadSettings() failed: %v", err)
	}
	rewrittenAgain, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json after second load: %v", err)
	}
	if string(rewritten) != string(rewrittenAgain) {
		t.Errorf("settings.json changed on idempotent second load:\nfirst:\n%s\nsecond:\n%s", rewritten, rewrittenAgain)
	}
}

// TestLoadSettings_NewKeyedSettingsUntouched verifies that a settings.json
// already using the new loop_* keys loads correctly and is not rewritten.
func TestLoadSettings_NewKeyedSettingsUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	newSettings := `{
		"acp_servers": [],
		"web": {"port": 9999},
		"session": {
			"startup_loop_delay_seconds": 20,
			"loop_suspend_timeout": "1h"
		},
		"conversations": {
			"max_loop_iterations": 99,
			"min_loop_completion_delay_seconds": 7
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(newSettings), 0644); err != nil {
		t.Fatalf("failed to create test settings.json: %v", err)
	}

	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json before load: %v", err)
	}

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	if got := cfg.Session.GetStartupLoopDelay(); got.Seconds() != 20 {
		t.Errorf("GetStartupLoopDelay() = %v, want 20s", got)
	}
	if got := cfg.Conversations.GetMaxLoopIterations(); got != 99 {
		t.Errorf("GetMaxLoopIterations() = %d, want 99", got)
	}

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json after load: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("settings.json was rewritten even though it already used loop_* keys:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestSessionConfig_GetStartupResumeConcurrency covers the mitto-54k.1 config
// accessor: nil/zero → default, negative → clamped to 1, positive → passthrough.
func TestSessionConfig_GetStartupResumeConcurrency(t *testing.T) {
	// nil receiver → default
	var nilCfg *SessionConfig
	if got := nilCfg.GetStartupResumeConcurrency(); got != DefaultStartupResumeConcurrency {
		t.Errorf("nil.GetStartupResumeConcurrency() = %d, want %d", got, DefaultStartupResumeConcurrency)
	}

	// zero value → default
	cfg := &SessionConfig{}
	if got := cfg.GetStartupResumeConcurrency(); got != DefaultStartupResumeConcurrency {
		t.Errorf("zero.GetStartupResumeConcurrency() = %d, want %d", got, DefaultStartupResumeConcurrency)
	}

	// negative → clamped to 1 (a size-0 semaphore would deadlock)
	cfg.StartupResumeConcurrency = -5
	if got := cfg.GetStartupResumeConcurrency(); got != 1 {
		t.Errorf("negative.GetStartupResumeConcurrency() = %d, want 1", got)
	}

	// explicit positive → passthrough
	cfg.StartupResumeConcurrency = 8
	if got := cfg.GetStartupResumeConcurrency(); got != 8 {
		t.Errorf("positive.GetStartupResumeConcurrency() = %d, want 8", got)
	}
}

// TestLoadSettings_BeadsReadCacheTTL verifies that a settings.json with
// web.beads.read_cache_ttl parses to the expected duration via
// EffectiveReadCacheTTL (mitto-9ni).
func TestLoadSettings_BeadsReadCacheTTL(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	customSettings := `{
		"web": {
			"beads": {
				"read_cache_ttl": "5m"
			}
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(customSettings), 0644); err != nil {
		t.Fatalf("failed to create test settings.json: %v", err)
	}

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}
	if cfg.Web.Beads == nil {
		t.Fatal("Web.Beads should not be nil")
	}
	if got, want := cfg.Web.Beads.ReadCacheTTL, "5m"; got != want {
		t.Errorf("ReadCacheTTL = %q, want %q", got, want)
	}
	if got, want := cfg.Web.Beads.EffectiveReadCacheTTL(), 5*time.Minute; got != want {
		t.Errorf("EffectiveReadCacheTTL() = %v, want %v", got, want)
	}
}

// --- Shared bearer token resolution tests (mitto-7gta.26) ---
//
// LoadSettingsWithFallback (not LoadSettings) is the function actually called
// at startup by the CLI (internal/cmd/root.go) and the macOS app
// (cmd/mitto-app/main.go), so the resolution behaviour must be verified
// against it directly -- testing LoadSettings alone would validate a path
// with no production caller.

// TestLoadSettingsWithFallback_SharedToken_EnvVar_NoRCFile verifies the env
// var is resolved into the config when there is no RC file.
func TestLoadSettingsWithFallback_SharedToken_EnvVar_NoRCFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("HOME", tmpDir) // ensure no real ~/.mittorc is picked up
	t.Setenv("MITTO_SHARED_TOKEN", "env-token-value")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	result, err := LoadSettingsWithFallback()
	if err != nil {
		t.Fatalf("LoadSettingsWithFallback() failed: %v", err)
	}
	if result.Config.Web.Auth == nil {
		t.Fatal("Web.Auth should not be nil")
	}
	if result.Config.Web.Auth.SharedToken != "env-token-value" {
		t.Errorf("SharedToken = %q, want %q", result.Config.Web.Auth.SharedToken, "env-token-value")
	}
}

// TestLoadSettingsWithFallback_SharedToken_EnvVar_WithRCFile verifies the env
// var is still resolved and survives the RC-file merge (mitto-7gta.26 was
// originally wired only into the unused LoadSettings function; this pins the
// fix onto the actual RC-file merge path).
func TestLoadSettingsWithFallback_SharedToken_EnvVar_WithRCFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("MITTO_SHARED_TOKEN", "env-token-value")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	rcPath := filepath.Join(tmpDir, ".mittorc")
	rcContent := "acp:\n  - rc-server:\n      command: \"rc-cmd\"\n"
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("failed to create .mittorc: %v", err)
	}

	result, err := LoadSettingsWithFallback()
	if err != nil {
		t.Fatalf("LoadSettingsWithFallback() failed: %v", err)
	}
	if result.Config.Web.Auth == nil {
		t.Fatal("Web.Auth should not be nil (RC-file merge must carry the resolved token)")
	}
	if result.Config.Web.Auth.SharedToken != "env-token-value" {
		t.Errorf("SharedToken = %q, want %q", result.Config.Web.Auth.SharedToken, "env-token-value")
	}
}

// TestLoadSettingsWithFallbackNoKeychain_SharedToken_NotResolved verifies the
// headless/no-keychain variant never resolves the shared token either (not
// even via the env var), matching its documented "must never touch web
// authentication" contract.
func TestLoadSettingsWithFallbackNoKeychain_SharedToken_NotResolved(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("MITTO_SHARED_TOKEN", "env-token-value")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	result, err := LoadSettingsWithFallbackNoKeychain()
	if err != nil {
		t.Fatalf("LoadSettingsWithFallbackNoKeychain() failed: %v", err)
	}
	if result.Config.Web.Auth != nil && result.Config.Web.Auth.SharedToken != "" {
		t.Errorf("SharedToken = %q, want empty (no-keychain variant must not resolve it)", result.Config.Web.Auth.SharedToken)
	}
}

// TestLoadSettings_SharedToken_EnvVarTakesPrecedence verifies MITTO_SHARED_TOKEN
// wins outright over any settings.json value. The fixture below also carries
// a simple-auth password, which unconditionally triggers
// migratePasswordToKeychain (mitto-klux) regardless of the shared-token env
// var -- so this test redirects the secret store to an in-memory fake
// (secrets.SetStoreForTest) to guarantee it never touches the real macOS
// Keychain, while still exercising the real migration code path.
func TestLoadSettings_SharedToken_EnvVarTakesPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("MITTO_SHARED_TOKEN", "env-token-value")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	if secrets.IsSupported() {
		t.Cleanup(secrets.SetStoreForTest(secrets.NewFakeStore()))
	}

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	settingsJSON := `{"web":{"auth":{"simple":{"username":"admin","password":"pw"},"shared_token":"settings-token-value"}}}`
	if err := os.WriteFile(settingsPath, []byte(settingsJSON), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}
	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth should not be nil")
	}
	if cfg.Web.Auth.SharedToken != "env-token-value" {
		t.Errorf("SharedToken = %q, want %q (env var must win)", cfg.Web.Auth.SharedToken, "env-token-value")
	}
}

// TestLoadSettings_SharedToken_EnvVarNeverPersisted verifies the env-provided
// token is never written back to settings.json.
func TestLoadSettings_SharedToken_EnvVarNeverPersisted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("MITTO_SHARED_TOKEN", "env-token-value")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	if _, err := LoadSettings(); err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	if strings.Contains(string(raw), "env-token-value") {
		t.Errorf("settings.json must never persist the env-provided token, got: %s", raw)
	}
}

// TestLoadSettings_SharedToken_NotConfigured verifies the feature-off case:
// no env var, no settings.json entry -> Web.Auth stays nil, no Keychain touched.
func TestLoadSettings_SharedToken_NotConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("MITTO_SHARED_TOKEN", "")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}
	if cfg.Web.Auth != nil && cfg.Web.Auth.SharedToken != "" {
		t.Errorf("SharedToken = %q, want empty (feature off)", cfg.Web.Auth.SharedToken)
	}
}

// TestLoadSettings_SharedToken_FromSettingsJSON verifies a token configured
// directly in settings.json (no env var) is resolved into cfg.Web.Auth.SharedToken.
//
// On a platform where secrets.IsSupported() is true (macOS), loading also
// triggers migrateSharedTokenToKeychain and (because the fixture also carries
// a simple-auth password) migratePasswordToKeychain -- both of which write to
// the production Keychain by default (mitto-klux). This test redirects the
// secret store to an in-memory fake (secrets.SetStoreForTest) for the
// duration of the test so neither migration ever touches the real Keychain,
// while still exercising the real migration code paths end-to-end.
func TestLoadSettings_SharedToken_FromSettingsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	t.Setenv("MITTO_SHARED_TOKEN", "")
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	if secrets.IsSupported() {
		t.Cleanup(secrets.SetStoreForTest(secrets.NewFakeStore()))
	}

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	settingsJSON := `{"web":{"auth":{"simple":{"username":"admin","password":"pw"},"shared_token":"settings-token-value"}}}`
	if err := os.WriteFile(settingsPath, []byte(settingsJSON), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}
	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth should not be nil")
	}
	// The in-memory cfg keeps the value regardless of migration, mirroring
	// migratePasswordToKeychain's behaviour (only settings.json on disk is
	// blanked; the already-converted cfg copy is untouched).
	if cfg.Web.Auth.SharedToken != "settings-token-value" {
		t.Errorf("SharedToken = %q, want %q", cfg.Web.Auth.SharedToken, "settings-token-value")
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	if secrets.IsSupported() {
		if strings.Contains(string(raw), "settings-token-value") {
			t.Errorf("settings.json should have been blanked after keychain migration, got: %s", raw)
		}
	} else {
		if !strings.Contains(string(raw), "settings-token-value") {
			t.Errorf("settings.json should retain the token when secrets are unsupported, got: %s", raw)
		}
	}
}

// runSecurityCLI runs the macOS `security` command-line tool with the given
// arguments, bounded by a short timeout, and returns combined stdout+stderr
// plus whether the command exited zero. Every call in this file avoids the
// `-w` (decrypt-and-print-password) flag against an *existing* item: doing
// so was observed, while developing this reproduction, to trigger an
// interactive OS Keychain-authorization prompt that hangs indefinitely in a
// headless run (the prompt has nothing to answer it). Metadata-only lookups
// (no `-w`) were consistently fast and prompt-free, which is why the
// assertion below is built on item existence/modification-timestamp rather
// than decrypted content.
func runSecurityCLI(t *testing.T, args ...string) (output string, ok bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "security", args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("security %v timed out after 5s (likely an unanswerable Keychain authorization prompt); output so far: %s", args, out)
	}
	return string(out), err == nil
}

// keychainEntryFingerprint returns a metadata-only fingerprint (existence +
// modification timestamp) for a Keychain entry, without ever decrypting it.
// Two fingerprints taken before/after an operation are equal iff the entry
// was neither created, deleted, nor updated in between.
func keychainEntryFingerprint(t *testing.T, service, account string) string {
	t.Helper()
	out, found := runSecurityCLI(t, "find-generic-password", "-s", service, "-a", account)
	if !found {
		return "<absent>"
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "\"mdat\"") {
			return line
		}
	}
	return out
}

// TestLoadSettings_PasswordMigration_ClobbersExistingKeychainEntry is the
// regression test for mitto-klux.
//
// Root cause: migratePasswordToKeychain (triggered by LoadSettings whenever
// a non-empty simple-auth password is present in the settings.json fixture)
// writes to the Keychain entry "Mitto"/"external-access" via the
// package-level secrets.Default() store. Before the fix, that store was
// always the real macOS KeychainStore with no override -- any test whose
// fixture carried a password (e.g. this repo's own
// TestLoadSettings_SharedToken_EnvVarTakesPrecedence and
// TestLoadSettings_SharedToken_FromSettingsJSON, both of which use a
// password:"pw" fixture) silently overwrote whatever real credential was
// stored there. That is exactly how a real developer's password was
// permanently lost (see bead description).
//
// The fix is secrets.SetStoreForTest + secrets.NewFakeStore (internal/secrets/
// testing.go): tests redirect the package-level store to an in-memory fake
// before calling LoadSettings(), so migratePasswordToKeychain still runs
// (proving the migration logic itself is exercised) but every Get/Set/Delete
// lands on the fake instead of macOS Keychain syscalls. This test proves
// both halves of that contract:
//  1. the fixture password DOES reach the fake store (the migration path
//     actually ran, not skipped), and
//  2. the REAL Keychain entry's metadata fingerprint (existence + mdat) is
//     byte-for-byte unchanged across the call -- verified via the `security`
//     CLI, decrypt-free (see keychainEntryFingerprint) -- i.e. the real
//     entry was never touched, regardless of whatever value it held before
//     this test ran.
func TestLoadSettings_PasswordMigration_ClobbersExistingKeychainEntry(t *testing.T) {
	if !secrets.IsSupported() {
		t.Skip("secrets.IsSupported() is false on this platform; migratePasswordToKeychain never runs, nothing to reproduce")
	}
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("security CLI not available; cannot safely verify this test without decrypting the real Keychain entry")
	}

	before := keychainEntryFingerprint(t, secrets.ServiceName, secrets.AccountExternalAccess)

	fake := secrets.NewFakeStore()
	t.Cleanup(secrets.SetStoreForTest(fake))

	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	settingsJSON := `{"web":{"auth":{"simple":{"username":"admin","password":"pw"}}}}`
	if err := os.WriteFile(settingsPath, []byte(settingsJSON), 0644); err != nil {
		t.Fatalf("failed to create settings.json: %v", err)
	}

	if _, err := LoadSettings(); err != nil {
		t.Fatalf("LoadSettings() failed: %v", err)
	}

	// The migration path must have actually run against the injected fake
	// (otherwise this test would pass vacuously without exercising anything).
	if got, err := fake.Get(secrets.ServiceName, secrets.AccountExternalAccess); err != nil || got != "pw" {
		t.Fatalf("fake store Get(%s, %s) = (%q, %v), want (\"pw\", nil) -- migration path did not run against the injected store", secrets.ServiceName, secrets.AccountExternalAccess, got, err)
	}

	// The real Keychain entry must be byte-for-byte unchanged -- proving the
	// injected fake, not the real Keychain, absorbed the write above.
	after := keychainEntryFingerprint(t, secrets.ServiceName, secrets.AccountExternalAccess)
	if before != after {
		t.Errorf("real Keychain entry %s/%s changed across LoadSettings() despite an injected fake store: before=%q after=%q", secrets.ServiceName, secrets.AccountExternalAccess, before, after)
	}
}

// TestWebBeadsConfig_EffectiveReadCacheTTL_Fallback verifies that empty,
// invalid, zero, and negative durations all fall back to the mirror default,
// and that a nil receiver is also safe (mitto-9ni).
func TestWebBeadsConfig_EffectiveReadCacheTTL_Fallback(t *testing.T) {
	cases := []struct {
		name string
		in   *WebBeadsConfig
	}{
		{"nil receiver", nil},
		{"empty string", &WebBeadsConfig{ReadCacheTTL: ""}},
		{"unparseable", &WebBeadsConfig{ReadCacheTTL: "not-a-duration"}},
		{"zero", &WebBeadsConfig{ReadCacheTTL: "0s"}},
		{"negative", &WebBeadsConfig{ReadCacheTTL: "-5m"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.EffectiveReadCacheTTL()
			if got != defaultBeadsReadCacheTTL {
				t.Errorf("EffectiveReadCacheTTL() = %v, want default %v",
					got, defaultBeadsReadCacheTTL)
			}
		})
	}
}
