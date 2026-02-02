package config

import (
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/secrets"

	defaultConfig "github.com/inercia/mitto/config"
)

// ConfigSource indicates where the configuration was loaded from.
type ConfigSource int

const (
	// ConfigSourceNone indicates no configuration was loaded.
	ConfigSourceNone ConfigSource = iota
	// ConfigSourceRCFile indicates configuration was loaded from ~/.mittorc or equivalent.
	ConfigSourceRCFile
	// ConfigSourceSettingsJSON indicates configuration was loaded from settings.json.
	ConfigSourceSettingsJSON
	// ConfigSourceEmbeddedDefaults indicates configuration was loaded from embedded defaults.
	ConfigSourceEmbeddedDefaults
	// ConfigSourceCustomFile indicates configuration was loaded from a custom file (--config flag).
	ConfigSourceCustomFile
)

// LoadResult contains the loaded configuration and metadata about its source.
type LoadResult struct {
	// Config is the loaded configuration.
	Config *Config
	// Source indicates where the configuration was loaded from.
	Source ConfigSource
	// SourcePath is the path to the configuration file (empty for embedded defaults).
	SourcePath string
}

// Settings represents the persisted Mitto settings in JSON format.
// This struct mirrors the Config struct but uses JSON serialization
// and is stored in the Mitto data directory as settings.json.
type Settings struct {
	// ACPServers is the list of configured ACP servers (order matters - first is default)
	ACPServers []ACPServerSettings `json:"acp_servers"`
	// Prompts is a list of predefined prompts for the dropup menu (global prompts)
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// Web contains web interface configuration
	Web WebConfig `json:"web"`
	// UI contains desktop app UI configuration
	UI UIConfig `json:"ui,omitempty"`
	// Session contains session storage limits configuration
	Session *SessionConfig `json:"session,omitempty"`
	// Conversations contains global conversation processing configuration
	Conversations *ConversationsConfig `json:"conversations,omitempty"`
}

// SessionConfig represents session storage configuration.
// These settings are not exposed in the Settings dialog.
type SessionConfig struct {
	// MaxMessagesPerSession is the maximum number of messages to retain per conversation.
	// When exceeded, oldest messages are pruned. Default: 0 (unlimited)
	MaxMessagesPerSession int `json:"max_messages_per_session,omitempty"`
	// MaxSessionSizeBytes is the maximum total size in bytes for a session's stored data.
	// When exceeded, oldest messages are pruned. Default: 0 (unlimited)
	MaxSessionSizeBytes int64 `json:"max_session_size_bytes,omitempty"`
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
		ACPServers:    make([]ACPServer, len(s.ACPServers)),
		Prompts:       s.Prompts,
		Web:           s.Web,
		UI:            s.UI,
		Session:       s.Session,
		Conversations: s.Conversations,
	}
	for i, srv := range s.ACPServers {
		cfg.ACPServers[i] = ACPServer(srv)
	}
	return cfg
}

// ConfigToSettings converts a Config to Settings for persistence.
func ConfigToSettings(cfg *Config) *Settings {
	s := &Settings{
		ACPServers:    make([]ACPServerSettings, len(cfg.ACPServers)),
		Prompts:       cfg.Prompts,
		Web:           cfg.Web,
		UI:            cfg.UI,
		Session:       cfg.Session,
		Conversations: cfg.Conversations,
	}
	for i, srv := range cfg.ACPServers {
		s.ACPServers[i] = ACPServerSettings(srv)
	}
	return s
}

// LoadSettings loads settings from the Mitto data directory.
// If settings.json doesn't exist, it creates it from the embedded default config.
// This function also ensures the Mitto directory exists.
//
// On platforms with secure credential storage (macOS Keychain):
//   - If a password exists in settings.json, it is migrated to the keychain
//     and removed from settings.json for security
//   - If no password is in settings.json, it is loaded from the keychain
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
		if err := createDefaultSettings(); err != nil {
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

	// Handle external access password with secure storage
	if cfg.Web.Auth != nil && cfg.Web.Auth.Simple != nil {
		if secrets.IsSupported() {
			if cfg.Web.Auth.Simple.Password != "" {
				// Password found in settings.json - migrate it to keychain
				if err := migratePasswordToKeychain(&settings, cfg); err != nil {
					// Log warning but don't fail - password still works from settings
					// The migration will be attempted again on next load
					_ = err // Ignore migration error, password is still usable
				}
			} else {
				// No password in settings.json - try to load from keychain
				password, err := secrets.GetExternalAccessPassword()
				if err == nil && password != "" {
					cfg.Web.Auth.Simple.Password = password
				}
				// If password not found in Keychain, leave it empty
				// Validation should catch this case when external access is attempted
			}
		}
	}

	return cfg, nil
}

// migratePasswordToKeychain moves the password from settings.json to the system keychain.
// This improves security by not storing passwords in plain text files.
func migratePasswordToKeychain(settings *Settings, cfg *Config) error {
	password := cfg.Web.Auth.Simple.Password

	// Save password to keychain
	if err := secrets.SetExternalAccessPassword(password); err != nil {
		return fmt.Errorf("failed to save password to keychain: %w", err)
	}

	// Clear password from settings and save
	settings.Web.Auth.Simple.Password = ""
	if err := SaveSettings(settings); err != nil {
		// Password is in keychain but settings.json still has the old password
		// This is not ideal but not critical - next load will try again
		return fmt.Errorf("failed to update settings after keychain migration: %w", err)
	}

	return nil
}

// createDefaultSettings parses the embedded YAML config and saves it as JSON.
func createDefaultSettings() error {
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
// Before writing, it creates a backup of the existing settings file (if it exists)
// at settings.json.bak. Only one backup is maintained at a time.
func SaveSettings(settings *Settings) error {
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return err
	}

	// Create backup if settings.json already exists
	if _, err := os.Stat(settingsPath); err == nil {
		backupPath := settingsPath + ".bak"
		// Read existing settings and write to backup (overwrites any existing backup)
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			return fmt.Errorf("failed to read settings for backup: %w", err)
		}
		if err := os.WriteFile(backupPath, data, 0644); err != nil {
			return fmt.Errorf("failed to create settings backup: %w", err)
		}
	}

	// Use atomic write for safety
	return fileutil.WriteJSONAtomic(settingsPath, settings, 0644)
}

// LoadSettingsWithFallback loads configuration with the following hierarchy:
//  1. If an RC file exists (~/.mittorc), use it exclusively (ignores settings.json)
//  2. If no RC file exists, use settings.json (creates from defaults if needed)
//
// This is intended for the macOS native app which can work without an RC file.
func LoadSettingsWithFallback() (*LoadResult, error) {
	// Check for RC file first (highest priority)
	rcPath, err := appdir.RCFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed to check RC file: %w", err)
	}

	if rcPath != "" {
		// RC file exists - use it exclusively
		cfg, err := Load(rcPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load RC file %s: %w", rcPath, err)
		}
		return &LoadResult{
			Config:     cfg,
			Source:     ConfigSourceRCFile,
			SourcePath: rcPath,
		}, nil
	}

	// No RC file - fall back to settings.json (existing behavior)
	cfg, err := LoadSettings()
	if err != nil {
		return nil, err
	}

	settingsPath, _ := appdir.SettingsPath()
	return &LoadResult{
		Config:     cfg,
		Source:     ConfigSourceSettingsJSON,
		SourcePath: settingsPath,
	}, nil
}
