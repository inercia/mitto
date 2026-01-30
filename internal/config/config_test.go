package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse_ValidConfig(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
  - claude:
      command: "claude-code --acp"
prompts:
  - name: "Review"
    prompt: "Review this code"
web:
  host: "0.0.0.0"
  port: 9000
  theme: "v2"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.ACPServers) != 2 {
		t.Errorf("ACPServers count = %d, want 2", len(cfg.ACPServers))
	}

	if cfg.ACPServers[0].Name != "auggie" {
		t.Errorf("first server name = %q, want %q", cfg.ACPServers[0].Name, "auggie")
	}

	if cfg.ACPServers[0].Command != "auggie --acp" {
		t.Errorf("first server command = %q, want %q", cfg.ACPServers[0].Command, "auggie --acp")
	}

	if cfg.Web.Host != "0.0.0.0" {
		t.Errorf("Web.Host = %q, want %q", cfg.Web.Host, "0.0.0.0")
	}

	if cfg.Web.Port != 9000 {
		t.Errorf("Web.Port = %d, want %d", cfg.Web.Port, 9000)
	}

	if cfg.Web.Theme != "v2" {
		t.Errorf("Web.Theme = %q, want %q", cfg.Web.Theme, "v2")
	}

	if len(cfg.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(cfg.Prompts))
	}
}

func TestParse_EmptyACPServers(t *testing.T) {
	yaml := `
acp: []
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Error("expected error for empty ACP servers, got nil")
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	yaml := `{{invalid yaml`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestParse_ExternalPort(t *testing.T) {
	tests := []struct {
		name         string
		yaml         string
		expectedPort int
		description  string
	}{
		{
			name: "disabled",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  external_port: -1
`,
			expectedPort: -1,
			description:  "Port -1 means external listener is disabled",
		},
		{
			name: "random",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  external_port: 0
`,
			expectedPort: 0,
			description:  "Port 0 means OS chooses a random available port",
		},
		{
			name: "specific",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  external_port: 8443
`,
			expectedPort: 8443,
			description:  "Port > 0 means use that specific port",
		},
		{
			name: "not_specified",
			yaml: `
acp:
  - test:
      command: "test-cmd"
web:
  port: 8080
`,
			expectedPort: 0,
			description:  "When not specified, defaults to 0 (Go zero value)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			if cfg.Web.ExternalPort != tt.expectedPort {
				t.Errorf("ExternalPort = %d, want %d (%s)", cfg.Web.ExternalPort, tt.expectedPort, tt.description)
			}
		})
	}
}

func TestParse_WebHooks(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  hooks:
    up:
      command: "open http://localhost:${PORT}"
      name: "Open Browser"
    down:
      command: "echo Shutting down on port ${PORT}"
      name: "Cleanup"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Hooks.Up.Command != "open http://localhost:${PORT}" {
		t.Errorf("Up hook command = %q, want %q", cfg.Web.Hooks.Up.Command, "open http://localhost:${PORT}")
	}

	if cfg.Web.Hooks.Up.Name != "Open Browser" {
		t.Errorf("Up hook name = %q, want %q", cfg.Web.Hooks.Up.Name, "Open Browser")
	}

	if cfg.Web.Hooks.Down.Command != "echo Shutting down on port ${PORT}" {
		t.Errorf("Down hook command = %q, want %q", cfg.Web.Hooks.Down.Command, "echo Shutting down on port ${PORT}")
	}

	if cfg.Web.Hooks.Down.Name != "Cleanup" {
		t.Errorf("Down hook name = %q, want %q", cfg.Web.Hooks.Down.Name, "Cleanup")
	}
}

func TestParse_WebHooks_UpOnly(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  hooks:
    up:
      command: "echo starting"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Hooks.Up.Command != "echo starting" {
		t.Errorf("Up hook command = %q, want %q", cfg.Web.Hooks.Up.Command, "echo starting")
	}

	// Down hook should be empty
	if cfg.Web.Hooks.Down.Command != "" {
		t.Errorf("Down hook command = %q, want empty", cfg.Web.Hooks.Down.Command)
	}
}

func TestParse_WebHooks_DownOnly(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  hooks:
    down:
      command: "echo stopping"
      name: "Stop"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Up hook should be empty
	if cfg.Web.Hooks.Up.Command != "" {
		t.Errorf("Up hook command = %q, want empty", cfg.Web.Hooks.Up.Command)
	}

	if cfg.Web.Hooks.Down.Command != "echo stopping" {
		t.Errorf("Down hook command = %q, want %q", cfg.Web.Hooks.Down.Command, "echo stopping")
	}

	if cfg.Web.Hooks.Down.Name != "Stop" {
		t.Errorf("Down hook name = %q, want %q", cfg.Web.Hooks.Down.Name, "Stop")
	}
}

func TestParse_PerServerPrompts(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Improve Rules"
          prompt: "Please improve the rules"
        - name: "Run Tests"
          prompt: "Run all tests and fix failures"
  - claude:
      command: "claude-code --acp"
prompts:
  - name: "Continue"
    prompt: "Continue with the task"
web:
  host: "127.0.0.1"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check that auggie has 2 prompts
	if len(cfg.ACPServers) != 2 {
		t.Fatalf("ACPServers count = %d, want 2", len(cfg.ACPServers))
	}

	auggie := cfg.ACPServers[0]
	if auggie.Name != "auggie" {
		t.Errorf("first server name = %q, want %q", auggie.Name, "auggie")
	}
	if len(auggie.Prompts) != 2 {
		t.Fatalf("auggie prompts count = %d, want 2", len(auggie.Prompts))
	}
	if auggie.Prompts[0].Name != "Improve Rules" {
		t.Errorf("auggie first prompt name = %q, want %q", auggie.Prompts[0].Name, "Improve Rules")
	}
	if auggie.Prompts[1].Prompt != "Run all tests and fix failures" {
		t.Errorf("auggie second prompt text = %q, want %q", auggie.Prompts[1].Prompt, "Run all tests and fix failures")
	}

	// Check that claude has no prompts
	claude := cfg.ACPServers[1]
	if len(claude.Prompts) != 0 {
		t.Errorf("claude prompts count = %d, want 0", len(claude.Prompts))
	}

	// Check global prompts are still parsed
	if len(cfg.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(cfg.Prompts))
	}
}

func TestParse_PromptBackgroundColor(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
      prompts:
        - name: "Server Prompt"
          prompt: "Server prompt text"
          backgroundColor: "#FF5733"
prompts:
  - name: "Global Prompt"
    prompt: "Global prompt text"
    backgroundColor: "#E8F5E9"
  - name: "No Color"
    prompt: "Prompt without color"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check server prompt has backgroundColor
	if len(cfg.ACPServers[0].Prompts) != 1 {
		t.Fatalf("server prompts count = %d, want 1", len(cfg.ACPServers[0].Prompts))
	}
	if cfg.ACPServers[0].Prompts[0].BackgroundColor != "#FF5733" {
		t.Errorf("server prompt backgroundColor = %q, want %q", cfg.ACPServers[0].Prompts[0].BackgroundColor, "#FF5733")
	}

	// Check global prompts
	if len(cfg.Prompts) != 2 {
		t.Fatalf("global prompts count = %d, want 2", len(cfg.Prompts))
	}
	if cfg.Prompts[0].BackgroundColor != "#E8F5E9" {
		t.Errorf("first global prompt backgroundColor = %q, want %q", cfg.Prompts[0].BackgroundColor, "#E8F5E9")
	}
	if cfg.Prompts[1].BackgroundColor != "" {
		t.Errorf("second global prompt backgroundColor = %q, want empty", cfg.Prompts[1].BackgroundColor)
	}
}

func TestLoad_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, ".mittorc")

	yaml := `
acp:
  - test:
      command: "test-cmd"
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.ACPServers) != 1 {
		t.Errorf("ACPServers count = %d, want 1", len(cfg.ACPServers))
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/.mittorc")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestLoad_JSONFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	jsonConfig := `{
		"acp_servers": [
			{"name": "json-server", "command": "json-cmd --acp"}
		],
		"web": {
			"host": "0.0.0.0",
			"port": 9000,
			"theme": "v2"
		}
	}`
	if err := os.WriteFile(path, []byte(jsonConfig), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.ACPServers) != 1 {
		t.Errorf("ACPServers count = %d, want 1", len(cfg.ACPServers))
	}
	if cfg.ACPServers[0].Name != "json-server" {
		t.Errorf("server name = %q, want %q", cfg.ACPServers[0].Name, "json-server")
	}
	if cfg.ACPServers[0].Command != "json-cmd --acp" {
		t.Errorf("server command = %q, want %q", cfg.ACPServers[0].Command, "json-cmd --acp")
	}
	if cfg.Web.Host != "0.0.0.0" {
		t.Errorf("Web.Host = %q, want %q", cfg.Web.Host, "0.0.0.0")
	}
	if cfg.Web.Port != 9000 {
		t.Errorf("Web.Port = %d, want %d", cfg.Web.Port, 9000)
	}
	if cfg.Web.Theme != "v2" {
		t.Errorf("Web.Theme = %q, want %q", cfg.Web.Theme, "v2")
	}
}

func TestLoad_YAMLFileWithYmlExtension(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yml")

	yamlConfig := `
acp:
  - yml-server:
      command: "yml-cmd --acp"
`
	if err := os.WriteFile(path, []byte(yamlConfig), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.ACPServers) != 1 {
		t.Errorf("ACPServers count = %d, want 1", len(cfg.ACPServers))
	}
	if cfg.ACPServers[0].Name != "yml-server" {
		t.Errorf("server name = %q, want %q", cfg.ACPServers[0].Name, "yml-server")
	}
}

func TestParseJSON_EmptyACPServers(t *testing.T) {
	jsonConfig := `{"acp_servers": []}`
	_, err := ParseJSON([]byte(jsonConfig))
	if err == nil {
		t.Error("expected error for empty ACP servers, got nil")
	}
}

func TestDefaultServer(t *testing.T) {
	cfg := &Config{
		ACPServers: []ACPServer{
			{Name: "first", Command: "cmd1"},
			{Name: "second", Command: "cmd2"},
		},
	}

	srv := cfg.DefaultServer()
	if srv == nil {
		t.Fatal("DefaultServer returned nil")
	}

	if srv.Name != "first" {
		t.Errorf("DefaultServer name = %q, want %q", srv.Name, "first")
	}
}

func TestDefaultServer_Empty(t *testing.T) {
	cfg := &Config{ACPServers: []ACPServer{}}
	srv := cfg.DefaultServer()
	if srv != nil {
		t.Errorf("DefaultServer = %v, want nil for empty config", srv)
	}
}

func TestGetServer(t *testing.T) {
	cfg := &Config{
		ACPServers: []ACPServer{
			{Name: "auggie", Command: "auggie --acp"},
			{Name: "claude", Command: "claude-code --acp"},
		},
	}

	tests := []struct {
		name       string
		serverName string
		wantErr    bool
		wantCmd    string
	}{
		{"existing server", "auggie", false, "auggie --acp"},
		{"second server", "claude", false, "claude-code --acp"},
		{"non-existent server", "unknown", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := cfg.GetServer(tt.serverName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if srv.Command != tt.wantCmd {
				t.Errorf("Command = %q, want %q", srv.Command, tt.wantCmd)
			}
		})
	}
}

func TestServerNames(t *testing.T) {
	cfg := &Config{
		ACPServers: []ACPServer{
			{Name: "auggie", Command: "cmd1"},
			{Name: "claude", Command: "cmd2"},
			{Name: "gemini", Command: "cmd3"},
		},
	}

	names := cfg.ServerNames()

	if len(names) != 3 {
		t.Fatalf("ServerNames count = %d, want 3", len(names))
	}

	expected := []string{"auggie", "claude", "gemini"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestServerNames_Empty(t *testing.T) {
	cfg := &Config{ACPServers: []ACPServer{}}
	names := cfg.ServerNames()

	if len(names) != 0 {
		t.Errorf("ServerNames = %v, want empty slice", names)
	}
}

func TestDefaultConfigPath_EnvOverride(t *testing.T) {
	// Save original value
	original := os.Getenv("MITTORC")
	defer os.Setenv("MITTORC", original)

	// Set custom path
	customPath := "/custom/path/.mittorc"
	os.Setenv("MITTORC", customPath)

	path := DefaultConfigPath()
	if path != customPath {
		t.Errorf("DefaultConfigPath = %q, want %q", path, customPath)
	}
}

func TestParse_StaticDir(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  static_dir: "/path/to/static"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.StaticDir != "/path/to/static" {
		t.Errorf("Web.StaticDir = %q, want %q", cfg.Web.StaticDir, "/path/to/static")
	}
}

func TestParse_AuthSimple(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    simple:
      username: "admin"
      password: "secret123"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth is nil, expected auth config")
	}

	if cfg.Web.Auth.Simple == nil {
		t.Fatal("Web.Auth.Simple is nil, expected simple auth config")
	}

	if cfg.Web.Auth.Simple.Username != "admin" {
		t.Errorf("Web.Auth.Simple.Username = %q, want %q", cfg.Web.Auth.Simple.Username, "admin")
	}

	if cfg.Web.Auth.Simple.Password != "secret123" {
		t.Errorf("Web.Auth.Simple.Password = %q, want %q", cfg.Web.Auth.Simple.Password, "secret123")
	}
}

func TestParse_NoAuth(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  port: 8080
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth != nil {
		t.Errorf("Web.Auth = %v, want nil when auth is not configured", cfg.Web.Auth)
	}
}

func TestParse_AuthEmptySimple(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    simple:
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// When auth section is present but simple is empty/nil in YAML,
	// WebAuth is created but Simple is nil
	// This allows for auth config with only allow list
	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth should not be nil when auth section is present")
	}

	if cfg.Web.Auth.Simple != nil {
		t.Errorf("Web.Auth.Simple = %v, want nil when simple is empty", cfg.Web.Auth.Simple)
	}
}

func TestParse_AuthAllow(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    simple:
      username: "admin"
      password: "secret"
    allow:
      ips:
        - "127.0.0.1"
        - "::1"
        - "192.168.0.0/24"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth is nil")
	}

	if cfg.Web.Auth.Allow == nil {
		t.Fatal("Web.Auth.Allow is nil")
	}

	if len(cfg.Web.Auth.Allow.IPs) != 3 {
		t.Fatalf("Web.Auth.Allow.IPs length = %d, want 3", len(cfg.Web.Auth.Allow.IPs))
	}

	expected := []string{"127.0.0.1", "::1", "192.168.0.0/24"}
	for i, want := range expected {
		if cfg.Web.Auth.Allow.IPs[i] != want {
			t.Errorf("Web.Auth.Allow.IPs[%d] = %q, want %q", i, cfg.Web.Auth.Allow.IPs[i], want)
		}
	}
}

func TestParse_AuthAllowOnly(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test-cmd"
web:
  auth:
    allow:
      ips:
        - "127.0.0.1"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Web.Auth == nil {
		t.Fatal("Web.Auth is nil when allow is configured")
	}

	if cfg.Web.Auth.Simple != nil {
		t.Error("Web.Auth.Simple should be nil when only allow is configured")
	}

	if cfg.Web.Auth.Allow == nil {
		t.Fatal("Web.Auth.Allow is nil")
	}

	if len(cfg.Web.Auth.Allow.IPs) != 1 {
		t.Fatalf("Web.Auth.Allow.IPs length = %d, want 1", len(cfg.Web.Auth.Allow.IPs))
	}

	if cfg.Web.Auth.Allow.IPs[0] != "127.0.0.1" {
		t.Errorf("Web.Auth.Allow.IPs[0] = %q, want %q", cfg.Web.Auth.Allow.IPs[0], "127.0.0.1")
	}
}

func TestParse_UIHotkeys(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    hotkeys:
      show_hide:
        key: "ctrl+alt+m"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if cfg.UI.Mac.Hotkeys == nil {
		t.Fatal("UI.Mac.Hotkeys is nil")
	}

	if cfg.UI.Mac.Hotkeys.ShowHide == nil {
		t.Fatal("UI.Mac.Hotkeys.ShowHide is nil")
	}

	if cfg.UI.Mac.Hotkeys.ShowHide.Key != "ctrl+alt+m" {
		t.Errorf("ShowHide.Key = %q, want %q", cfg.UI.Mac.Hotkeys.ShowHide.Key, "ctrl+alt+m")
	}

	// Test GetShowHideHotkey helper
	key, enabled := cfg.GetShowHideHotkey()
	if !enabled {
		t.Error("GetShowHideHotkey returned enabled=false, want true")
	}
	if key != "ctrl+alt+m" {
		t.Errorf("GetShowHideHotkey key = %q, want %q", key, "ctrl+alt+m")
	}
}

func TestParse_UIHotkeysDisabled(t *testing.T) {
	disabled := false
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    hotkeys:
      show_hide:
        enabled: false
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil || cfg.UI.Mac.Hotkeys == nil || cfg.UI.Mac.Hotkeys.ShowHide == nil {
		t.Fatal("UI config not properly parsed")
	}

	if cfg.UI.Mac.Hotkeys.ShowHide.Enabled == nil {
		t.Fatal("ShowHide.Enabled is nil")
	}

	if *cfg.UI.Mac.Hotkeys.ShowHide.Enabled != disabled {
		t.Errorf("ShowHide.Enabled = %v, want %v", *cfg.UI.Mac.Hotkeys.ShowHide.Enabled, disabled)
	}

	// Test GetShowHideHotkey helper
	_, enabled := cfg.GetShowHideHotkey()
	if enabled {
		t.Error("GetShowHideHotkey returned enabled=true, want false")
	}
}

func TestGetShowHideHotkey_Default(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	key, enabled := cfg.GetShowHideHotkey()
	if !enabled {
		t.Error("GetShowHideHotkey returned enabled=false, want true")
	}
	if key != DefaultShowHideHotkey {
		t.Errorf("GetShowHideHotkey key = %q, want %q", key, DefaultShowHideHotkey)
	}
}

func TestParse_UINotificationsSounds(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    notifications:
      sounds:
        agent_completed: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if cfg.UI.Mac.Notifications == nil {
		t.Fatal("UI.Mac.Notifications is nil")
	}

	if cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications.Sounds is nil")
	}

	if !cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = false, want true")
	}
}

func TestParse_UINotificationsSoundsDisabled(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    notifications:
      sounds:
        agent_completed: false
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Mac == nil {
		t.Fatal("UI.Mac is nil")
	}

	if cfg.UI.Mac.Notifications == nil {
		t.Fatal("UI.Mac.Notifications is nil")
	}

	if cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications.Sounds is nil")
	}

	if cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = true, want false")
	}
}

func TestParse_UIBothHotkeysAndNotifications(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  mac:
    hotkeys:
      show_hide:
        key: "cmd+alt+m"
    notifications:
      sounds:
        agent_completed: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check hotkeys
	if cfg.UI.Mac == nil || cfg.UI.Mac.Hotkeys == nil || cfg.UI.Mac.Hotkeys.ShowHide == nil {
		t.Fatal("UI.Mac.Hotkeys not properly parsed")
	}
	if cfg.UI.Mac.Hotkeys.ShowHide.Key != "cmd+alt+m" {
		t.Errorf("ShowHide.Key = %q, want %q", cfg.UI.Mac.Hotkeys.ShowHide.Key, "cmd+alt+m")
	}

	// Check notifications
	if cfg.UI.Mac.Notifications == nil || cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications not properly parsed")
	}
	if !cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = false, want true")
	}
}

func TestParse_UIConfirmations(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_session: false
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Confirmations == nil {
		t.Fatal("UI.Confirmations is nil")
	}

	if cfg.UI.Confirmations.DeleteSession == nil {
		t.Fatal("UI.Confirmations.DeleteSession is nil")
	}

	if *cfg.UI.Confirmations.DeleteSession != false {
		t.Error("Confirmations.DeleteSession = true, want false")
	}
}

func TestParse_UIConfirmationsTrue(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_session: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Confirmations == nil {
		t.Fatal("UI.Confirmations is nil")
	}

	if cfg.UI.Confirmations.DeleteSession == nil {
		t.Fatal("UI.Confirmations.DeleteSession is nil")
	}

	if *cfg.UI.Confirmations.DeleteSession != true {
		t.Error("Confirmations.DeleteSession = false, want true")
	}
}

func TestParse_UIConfirmationsWithMac(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_session: false
  mac:
    notifications:
      sounds:
        agent_completed: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check confirmations
	if cfg.UI.Confirmations == nil || cfg.UI.Confirmations.DeleteSession == nil {
		t.Fatal("UI.Confirmations not properly parsed")
	}
	if *cfg.UI.Confirmations.DeleteSession != false {
		t.Error("Confirmations.DeleteSession = true, want false")
	}

	// Check Mac notifications
	if cfg.UI.Mac == nil || cfg.UI.Mac.Notifications == nil || cfg.UI.Mac.Notifications.Sounds == nil {
		t.Fatal("UI.Mac.Notifications not properly parsed")
	}
	if !cfg.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Error("Notifications.Sounds.AgentCompleted = false, want true")
	}
}

func TestParse_UIConfirmationsQuitWithRunningSessions(t *testing.T) {
	yaml := `
acp:
  - claude:
      command: "claude"
ui:
  confirmations:
    delete_session: true
    quit_with_running_sessions: false
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.UI.Confirmations == nil {
		t.Fatal("UI.Confirmations is nil")
	}

	if cfg.UI.Confirmations.QuitWithRunningSessions == nil {
		t.Fatal("UI.Confirmations.QuitWithRunningSessions is nil")
	}

	if *cfg.UI.Confirmations.QuitWithRunningSessions != false {
		t.Error("Confirmations.QuitWithRunningSessions = true, want false")
	}

	// Also verify the helper method
	if cfg.ShouldConfirmQuitWithRunningSessions() != false {
		t.Error("ShouldConfirmQuitWithRunningSessions() = true, want false")
	}
}

func TestShouldConfirmQuitWithRunningSessions(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name:     "nil confirmations returns true",
			config:   &Config{},
			expected: true,
		},
		{
			name: "nil QuitWithRunningSessions returns true",
			config: &Config{
				UI: UIConfig{
					Confirmations: &ConfirmationsConfig{},
				},
			},
			expected: true,
		},
		{
			name: "explicit true returns true",
			config: &Config{
				UI: UIConfig{
					Confirmations: &ConfirmationsConfig{
						QuitWithRunningSessions: boolPtr(true),
					},
				},
			},
			expected: true,
		},
		{
			name: "explicit false returns false",
			config: &Config{
				UI: UIConfig{
					Confirmations: &ConfirmationsConfig{
						QuitWithRunningSessions: boolPtr(false),
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.ShouldConfirmQuitWithRunningSessions()
			if got != tt.expected {
				t.Errorf("ShouldConfirmQuitWithRunningSessions() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}

// Tests for MessageProcessor

func TestMessageProcessor_ShouldApply(t *testing.T) {
	tests := []struct {
		name           string
		when           ProcessorWhen
		isFirstMessage bool
		expected       bool
	}{
		{"first on first message", ProcessorWhenFirst, true, true},
		{"first on later message", ProcessorWhenFirst, false, false},
		{"all on first message", ProcessorWhenAll, true, true},
		{"all on later message", ProcessorWhenAll, false, true},
		{"all-except-first on first message", ProcessorWhenAllExceptFirst, true, false},
		{"all-except-first on later message", ProcessorWhenAllExceptFirst, false, true},
		{"unknown when value", ProcessorWhen("unknown"), true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MessageProcessor{When: tt.when}
			got := p.ShouldApply(tt.isFirstMessage)
			if got != tt.expected {
				t.Errorf("ShouldApply(%v) = %v, want %v", tt.isFirstMessage, got, tt.expected)
			}
		})
	}
}

func TestMessageProcessor_Apply(t *testing.T) {
	tests := []struct {
		name     string
		position ProcessorPosition
		text     string
		message  string
		expected string
	}{
		{"prepend", ProcessorPositionPrepend, "PREFIX:", "hello", "PREFIX:hello"},
		{"append", ProcessorPositionAppend, ":SUFFIX", "hello", "hello:SUFFIX"},
		{"prepend empty text", ProcessorPositionPrepend, "", "hello", "hello"},
		{"append empty text", ProcessorPositionAppend, "", "hello", "hello"},
		{"unknown position", ProcessorPosition("unknown"), "text", "hello", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MessageProcessor{Position: tt.position, Text: tt.text}
			got := p.Apply(tt.message)
			if got != tt.expected {
				t.Errorf("Apply(%q) = %q, want %q", tt.message, got, tt.expected)
			}
		})
	}
}

func TestApplyProcessors(t *testing.T) {
	processors := []MessageProcessor{
		{When: ProcessorWhenFirst, Position: ProcessorPositionPrepend, Text: "FIRST:"},
		{When: ProcessorWhenAll, Position: ProcessorPositionAppend, Text: ":ALL"},
		{When: ProcessorWhenAllExceptFirst, Position: ProcessorPositionPrepend, Text: "LATER:"},
	}

	tests := []struct {
		name           string
		message        string
		isFirstMessage bool
		expected       string
	}{
		{"first message", "hello", true, "FIRST:hello:ALL"},
		{"second message", "world", false, "LATER:world:ALL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyProcessors(tt.message, processors, tt.isFirstMessage)
			if got != tt.expected {
				t.Errorf("ApplyProcessors(%q, %v) = %q, want %q", tt.message, tt.isFirstMessage, got, tt.expected)
			}
		})
	}
}

func TestApplyProcessors_EmptyProcessors(t *testing.T) {
	message := "hello"
	got := ApplyProcessors(message, nil, true)
	if got != message {
		t.Errorf("ApplyProcessors with nil processors = %q, want %q", got, message)
	}

	got = ApplyProcessors(message, []MessageProcessor{}, true)
	if got != message {
		t.Errorf("ApplyProcessors with empty processors = %q, want %q", got, message)
	}
}

func TestMergeProcessors(t *testing.T) {
	globalProcessors := []MessageProcessor{
		{When: ProcessorWhenAll, Position: ProcessorPositionAppend, Text: ":GLOBAL"},
	}
	workspaceProcessors := []MessageProcessor{
		{When: ProcessorWhenFirst, Position: ProcessorPositionPrepend, Text: "WORKSPACE:"},
	}

	global := &ConversationsConfig{
		Processing: &ConversationProcessing{Processors: globalProcessors},
	}
	workspace := &ConversationsConfig{
		Processing: &ConversationProcessing{Processors: workspaceProcessors},
	}

	// Test merge (global first, then workspace)
	merged := MergeProcessors(global, workspace)
	if len(merged) != 2 {
		t.Fatalf("MergeProcessors returned %d processors, want 2", len(merged))
	}
	if merged[0].Text != ":GLOBAL" {
		t.Errorf("First processor text = %q, want %q", merged[0].Text, ":GLOBAL")
	}
	if merged[1].Text != "WORKSPACE:" {
		t.Errorf("Second processor text = %q, want %q", merged[1].Text, "WORKSPACE:")
	}
}

func TestMergeProcessors_Override(t *testing.T) {
	globalProcessors := []MessageProcessor{
		{When: ProcessorWhenAll, Position: ProcessorPositionAppend, Text: ":GLOBAL"},
	}
	workspaceProcessors := []MessageProcessor{
		{When: ProcessorWhenFirst, Position: ProcessorPositionPrepend, Text: "WORKSPACE:"},
	}

	global := &ConversationsConfig{
		Processing: &ConversationProcessing{Processors: globalProcessors},
	}
	workspace := &ConversationsConfig{
		Processing: &ConversationProcessing{
			Override:   true,
			Processors: workspaceProcessors,
		},
	}

	// Test override (only workspace processors)
	merged := MergeProcessors(global, workspace)
	if len(merged) != 1 {
		t.Fatalf("MergeProcessors with override returned %d processors, want 1", len(merged))
	}
	if merged[0].Text != "WORKSPACE:" {
		t.Errorf("Processor text = %q, want %q", merged[0].Text, "WORKSPACE:")
	}
}

func TestMergeProcessors_NilConfigs(t *testing.T) {
	// Both nil
	merged := MergeProcessors(nil, nil)
	if len(merged) != 0 {
		t.Errorf("MergeProcessors(nil, nil) returned %d processors, want 0", len(merged))
	}

	// Only global
	global := &ConversationsConfig{
		Processing: &ConversationProcessing{
			Processors: []MessageProcessor{{Text: "GLOBAL"}},
		},
	}
	merged = MergeProcessors(global, nil)
	if len(merged) != 1 {
		t.Errorf("MergeProcessors(global, nil) returned %d processors, want 1", len(merged))
	}

	// Only workspace
	workspace := &ConversationsConfig{
		Processing: &ConversationProcessing{
			Processors: []MessageProcessor{{Text: "WORKSPACE"}},
		},
	}
	merged = MergeProcessors(nil, workspace)
	if len(merged) != 1 {
		t.Errorf("MergeProcessors(nil, workspace) returned %d processors, want 1", len(merged))
	}
}

func TestParse_ConversationsConfig(t *testing.T) {
	yaml := `
acp:
  - test:
      command: "test --acp"
conversations:
  processing:
    override: false
    processors:
      - when: first
        position: prepend
        text: "System prompt\n\n"
      - when: all
        position: append
        text: "\n\n[Be concise]"
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if cfg.Conversations.Processing == nil {
		t.Fatal("Conversations.Processing is nil")
	}
	if cfg.Conversations.Processing.Override {
		t.Error("Override should be false")
	}
	if len(cfg.Conversations.Processing.Processors) != 2 {
		t.Fatalf("Processors count = %d, want 2", len(cfg.Conversations.Processing.Processors))
	}

	p0 := cfg.Conversations.Processing.Processors[0]
	if p0.When != ProcessorWhenFirst {
		t.Errorf("Processor[0].When = %q, want %q", p0.When, ProcessorWhenFirst)
	}
	if p0.Position != ProcessorPositionPrepend {
		t.Errorf("Processor[0].Position = %q, want %q", p0.Position, ProcessorPositionPrepend)
	}
	if p0.Text != "System prompt\n\n" {
		t.Errorf("Processor[0].Text = %q, want %q", p0.Text, "System prompt\n\n")
	}

	p1 := cfg.Conversations.Processing.Processors[1]
	if p1.When != ProcessorWhenAll {
		t.Errorf("Processor[1].When = %q, want %q", p1.When, ProcessorWhenAll)
	}
	if p1.Position != ProcessorPositionAppend {
		t.Errorf("Processor[1].Position = %q, want %q", p1.Position, ProcessorPositionAppend)
	}
}
