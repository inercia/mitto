package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
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

	// Verify config has ACP servers (from embedded defaults)
	if len(cfg.ACPServers) == 0 {
		t.Error("expected at least one ACP server from default config")
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
			{Name: "server1", Command: "cmd1"},
			{Name: "server2", Command: "cmd2"},
		},
		Web: WebConfig{
			Host:  "0.0.0.0",
			Port:  8080,
			Theme: "v2",
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
	}
	if result.Web.Host != original.Web.Host {
		t.Errorf("Web.Host = %q, want %q", result.Web.Host, original.Web.Host)
	}
	if result.Web.Port != original.Web.Port {
		t.Errorf("Web.Port = %d, want %d", result.Web.Port, original.Web.Port)
	}
	if result.Web.Theme != original.Web.Theme {
		t.Errorf("Web.Theme = %q, want %q", result.Web.Theme, original.Web.Theme)
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
