package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

func TestLoadSettings_CreatesDefaultSettings(t *testing.T) {
	// Save original env
	originalDir := os.Getenv(appdir.MittoDirEnv)
	defer func() {
		os.Setenv(appdir.MittoDirEnv, originalDir)
		appdir.ResetCache()
	}()

	// Use temp dir
	tmpDir := t.TempDir()
	os.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()

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
	// Save original env
	originalDir := os.Getenv(appdir.MittoDirEnv)
	defer func() {
		os.Setenv(appdir.MittoDirEnv, originalDir)
		appdir.ResetCache()
	}()

	// Use temp dir
	tmpDir := t.TempDir()
	os.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()

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
		Web: WebConfig{
			Host: "127.0.0.1",
			Port: 8080,
			Prompts: []WebPrompt{
				{Name: "Global", Prompt: "Global prompt"},
			},
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
	if len(result.Web.Prompts) != 1 {
		t.Fatalf("Web.Prompts count = %d, want 1", len(result.Web.Prompts))
	}
	if result.Web.Prompts[0].Name != "Global" {
		t.Errorf("Web.Prompts[0].Name = %q, want %q", result.Web.Prompts[0].Name, "Global")
	}
}
