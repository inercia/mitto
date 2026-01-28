package config

import (
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"

	defaultConfig "github.com/inercia/mitto/config"
)

// Settings represents the persisted Mitto settings in JSON format.
// This struct mirrors the Config struct but uses JSON serialization
// and is stored in the Mitto data directory as settings.json.
type Settings struct {
	// ACPServers is the list of configured ACP servers (order matters - first is default)
	ACPServers []ACPServerSettings `json:"acp_servers"`
	// Web contains web interface configuration
	Web WebConfig `json:"web"`
	// UI contains desktop app UI configuration
	UI UIConfig `json:"ui,omitempty"`
}

// ACPServerSettings is the JSON representation of an ACP server.
type ACPServerSettings struct {
	// Name is the identifier for this ACP server
	Name string `json:"name"`
	// Command is the shell command to start the ACP server
	Command string `json:"command"`
	// Prompts is an optional list of predefined prompts specific to this ACP server
	Prompts []WebPrompt `json:"prompts,omitempty"`
}

// ToConfig converts Settings to the internal Config struct.
func (s *Settings) ToConfig() *Config {
	cfg := &Config{
		ACPServers: make([]ACPServer, len(s.ACPServers)),
		Web:        s.Web,
		UI:         s.UI,
	}
	for i, srv := range s.ACPServers {
		cfg.ACPServers[i] = ACPServer{
			Name:    srv.Name,
			Command: srv.Command,
			Prompts: srv.Prompts,
		}
	}
	return cfg
}

// ConfigToSettings converts a Config to Settings for persistence.
func ConfigToSettings(cfg *Config) *Settings {
	s := &Settings{
		ACPServers: make([]ACPServerSettings, len(cfg.ACPServers)),
		Web:        cfg.Web,
		UI:         cfg.UI,
	}
	for i, srv := range cfg.ACPServers {
		s.ACPServers[i] = ACPServerSettings{
			Name:    srv.Name,
			Command: srv.Command,
			Prompts: srv.Prompts,
		}
	}
	return s
}

// LoadSettings loads settings from the Mitto data directory.
// If settings.json doesn't exist, it creates it from the embedded default config.
// This function also ensures the Mitto directory exists.
func LoadSettings() (*Config, error) {
	// Ensure Mitto directory exists
	if err := appdir.EnsureDir(); err != nil {
		return nil, fmt.Errorf("failed to create Mitto directory: %w", err)
	}

	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return nil, err
	}

	// Check if settings.json exists
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		// Create settings.json from embedded default config
		if err := createDefaultSettings(settingsPath); err != nil {
			return nil, fmt.Errorf("failed to create default settings: %w", err)
		}
	}

	// Load settings from JSON file
	var settings Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		return nil, fmt.Errorf("failed to read settings file %s: %w", settingsPath, err)
	}

	cfg := settings.ToConfig()

	// Validate the config has at least one ACP server
	if len(cfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured in settings")
	}

	return cfg, nil
}

// createDefaultSettings parses the embedded YAML config and saves it as JSON.
func createDefaultSettings(settingsPath string) error {
	// Parse the embedded default YAML config
	cfg, err := Parse(defaultConfig.DefaultConfigYAML)
	if err != nil {
		return fmt.Errorf("failed to parse embedded default config: %w", err)
	}

	// Convert to Settings and save
	settings := ConfigToSettings(cfg)
	if err := SaveSettings(settings); err != nil {
		return err
	}

	return nil
}

// SaveSettings saves settings to the Mitto data directory.
func SaveSettings(settings *Settings) error {
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return err
	}

	// Use atomic write for safety
	return fileutil.WriteJSONAtomic(settingsPath, settings, 0644)
}

// SettingsPath returns the path to the settings.json file.
// This is a convenience function that delegates to appdir.SettingsPath().
func SettingsPath() (string, error) {
	return appdir.SettingsPath()
}
