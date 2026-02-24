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
	// RCFilePath is the path to the RC file if one was used in the merge.
	// This is set even when Source is ConfigSourceSettingsJSON if an RC file was merged.
	RCFilePath string
	// HasRCFileServers indicates whether any ACP servers came from the RC file.
	// When true, those servers should be treated as read-only in the UI.
	HasRCFileServers bool
}

// Settings represents the persisted Mitto settings in JSON format.
// This struct mirrors the Config struct but uses JSON serialization
// and is stored in the Mitto data directory as settings.json.
type Settings struct {
	// ACPServers is the list of configured ACP servers (order matters - first is default)
	ACPServers []ACPServerSettings `json:"acp_servers"`
	// Prompts is a list of predefined prompts for the dropup menu (global prompts)
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// PromptsDirs is a list of additional directories to search for prompt files.
	// These are searched in addition to the default MITTO_DIR/prompts/ directory.
	PromptsDirs []string `json:"prompts_dirs,omitempty"`
	// Web contains web interface configuration
	Web WebConfig `json:"web"`
	// UI contains desktop app UI configuration
	UI UIConfig `json:"ui,omitempty"`
	// Session contains session storage limits configuration
	Session *SessionConfig `json:"session,omitempty"`
	// Conversations contains global conversation processing configuration
	Conversations *ConversationsConfig `json:"conversations,omitempty"`
	// RestrictedRunners contains per-runner-type global configuration
	RestrictedRunners map[string]*WorkspaceRunnerConfig `json:"restricted_runners,omitempty"`
}

// SessionConfig represents session storage configuration.
type SessionConfig struct {
	// MaxMessagesPerSession is the maximum number of messages to retain per conversation.
	// When exceeded, oldest messages are pruned. Default: 0 (unlimited)
	// Not exposed in the Settings dialog.
	MaxMessagesPerSession int `json:"max_messages_per_session,omitempty"`
	// MaxSessionSizeBytes is the maximum total size in bytes for a session's stored data.
	// When exceeded, oldest messages are pruned. Default: 0 (unlimited)
	// Not exposed in the Settings dialog.
	MaxSessionSizeBytes int64 `json:"max_session_size_bytes,omitempty"`
	// ArchiveRetentionPeriod specifies how long archived conversations are kept before auto-deletion.
	// Values: "never" (default - keep forever), "1d", "1w", "1m", "3m" (1 day, 1 week, 1 month, 3 months)
	ArchiveRetentionPeriod string `json:"archive_retention_period,omitempty"`
}

// ArchiveRetentionNever is the value for keeping archived conversations forever.
const ArchiveRetentionNever = "never"

// ValidArchiveRetentionPeriods contains all valid retention period values.
var ValidArchiveRetentionPeriods = []string{ArchiveRetentionNever, "1d", "1w", "1m", "3m"}

// GetArchiveRetentionPeriod returns the archive retention period, or "never" if not set.
func (c *SessionConfig) GetArchiveRetentionPeriod() string {
	if c == nil || c.ArchiveRetentionPeriod == "" {
		return ArchiveRetentionNever
	}
	return c.ArchiveRetentionPeriod
}

// ScannerDefenseConfig holds configuration for the scanner defense system.
type ScannerDefenseConfig struct {
	// Enabled controls whether scanner defense is active.
	Enabled bool `json:"enabled"`
	// RateLimit is the maximum number of requests per RateWindowSeconds before blocking.
	RateLimit int `json:"rate_limit,omitempty"`
	// RateWindowSeconds is the rate limiting window in seconds.
	RateWindowSeconds int `json:"rate_window_seconds,omitempty"`
	// ErrorRateThreshold is the error rate (0.0-1.0) above which an IP is blocked.
	ErrorRateThreshold float64 `json:"error_rate_threshold,omitempty"`
	// MinRequestsForAnalysis is the minimum requests needed before analyzing error rates.
	MinRequestsForAnalysis int `json:"min_requests,omitempty"`
	// SuspiciousPathThreshold is the number of suspicious path hits that trigger a block.
	SuspiciousPathThreshold int `json:"suspicious_path_threshold,omitempty"`
	// BlockDurationSeconds is how long an IP remains blocked in seconds.
	BlockDurationSeconds int `json:"block_duration_seconds,omitempty"`
	// Whitelist contains CIDR notation ranges that should never be blocked.
	Whitelist []string `json:"whitelist,omitempty"`
}

// ACPServerSettings is the JSON representation of an ACP server.
type ACPServerSettings struct {
	// Name is the identifier for this ACP server
	Name string `json:"name"`
	// Command is the shell command to start the ACP server
	Command string `json:"command"`
	// Cwd is the working directory for the ACP server process
	Cwd string `json:"cwd,omitempty"`
	// Type is an optional type identifier for prompt matching.
	// Servers with the same type share prompts. If empty, Name is used as the type.
	Type string `json:"type,omitempty"`
	// Prompts is an optional list of predefined prompts specific to this ACP server
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// RestrictedRunners contains per-runner-type configuration for this agent
	RestrictedRunners map[string]*WorkspaceRunnerConfig `json:"restricted_runners,omitempty"`
	// Source indicates where this server configuration originated from.
	// Used for config layering: servers from RC file are read-only in the UI.
	Source ConfigItemSource `json:"source,omitempty"`
}

// ToConfig converts Settings to the internal Config struct.
func (s *Settings) ToConfig() *Config {
	cfg := &Config{
		ACPServers:        make([]ACPServer, len(s.ACPServers)),
		Prompts:           s.Prompts,
		PromptsDirs:       s.PromptsDirs,
		Web:               s.Web,
		UI:                s.UI,
		Session:           s.Session,
		Conversations:     s.Conversations,
		RestrictedRunners: s.RestrictedRunners,
	}
	for i, srv := range s.ACPServers {
		cfg.ACPServers[i] = ACPServer(srv)
	}
	return cfg
}

// ConfigToSettings converts a Config to Settings for persistence.
func ConfigToSettings(cfg *Config) *Settings {
	s := &Settings{
		ACPServers:        make([]ACPServerSettings, len(cfg.ACPServers)),
		Prompts:           cfg.Prompts,
		PromptsDirs:       cfg.PromptsDirs,
		Web:               cfg.Web,
		UI:                cfg.UI,
		Session:           cfg.Session,
		Conversations:     cfg.Conversations,
		RestrictedRunners: cfg.RestrictedRunners,
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

	// Deduplicate ACP server prompts to clean up any accumulation from previous bugs
	// This is a one-time fix that runs on every load, but is idempotent
	settingsModified := deduplicateACPServerPrompts(&settings)

	cfg := settings.ToConfig()

	// Validate the config has at least one ACP server
	if len(cfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured in settings")
	}

	// If we modified settings during deduplication, save the cleaned version
	if settingsModified {
		_ = SaveSettings(&settings) // Ignore error - deduplication is best-effort
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

// deduplicateACPServerPrompts removes duplicate prompts from ACP server configurations.
// This is a cleanup function to fix settings files that accumulated duplicates due to
// a bug where file-based prompts were being merged without deduplication.
// Returns true if any modifications were made.
func deduplicateACPServerPrompts(settings *Settings) bool {
	modified := false

	for i := range settings.ACPServers {
		prompts := settings.ACPServers[i].Prompts
		if len(prompts) <= 1 {
			continue
		}

		// Deduplicate by name, keeping the first occurrence
		seen := make(map[string]bool)
		var dedupedPrompts []WebPrompt

		for _, p := range prompts {
			if p.Name == "" {
				continue // Skip prompts without names
			}
			if !seen[p.Name] {
				seen[p.Name] = true
				dedupedPrompts = append(dedupedPrompts, p)
			}
		}

		// Check if we removed any duplicates
		if len(dedupedPrompts) < len(prompts) {
			settings.ACPServers[i].Prompts = dedupedPrompts
			modified = true
		}
	}

	return modified
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

// LoadSettingsWithFallback loads configuration using a layered approach:
//  1. Load settings.json as the base (creates from defaults if needed)
//  2. If an RC file exists (~/.mittorc), merge its ACP servers into the config
//
// The RC file servers have higher priority and will override settings servers with the same name.
// RC file servers are marked with Source=SourceRCFile and are read-only in the UI.
// Settings servers are marked with Source=SourceSettings and can be edited via UI.
//
// Non-ACP settings (web, ui, etc.) come from settings.json when no RC file exists,
// or from the RC file when one exists (for backward compatibility).
func LoadSettingsWithFallback() (*LoadResult, error) {
	// Check for RC file
	rcPath, err := appdir.RCFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed to check RC file: %w", err)
	}

	// Always ensure settings.json exists (needed for UI settings persistence)
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		return nil, err
	}

	// Ensure Mitto directory exists
	if err := appdir.EnsureDir(); err != nil {
		return nil, fmt.Errorf("failed to create Mitto directory: %w", err)
	}

	// Create settings.json from defaults if it doesn't exist
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		if err := createDefaultSettings(); err != nil {
			return nil, fmt.Errorf("failed to create default settings: %w", err)
		}
	}

	// Load settings.json
	var settings Settings
	if err := fileutil.ReadJSON(settingsPath, &settings); err != nil {
		return nil, fmt.Errorf("failed to read settings file %s: %w", settingsPath, err)
	}

	// Mark all settings servers with their source
	for i := range settings.ACPServers {
		settings.ACPServers[i].Source = SourceSettings
	}

	// Convert settings to config
	settingsCfg := settings.ToConfig()

	// Handle keychain password loading
	if err := loadKeychainPassword(settingsCfg); err != nil {
		// Non-fatal, just log and continue
		_ = err
	}

	// If no RC file, return settings-only config
	if rcPath == "" {
		// Validate at least one ACP server
		if len(settingsCfg.ACPServers) == 0 {
			return nil, fmt.Errorf("no ACP servers configured in settings")
		}
		return &LoadResult{
			Config:           settingsCfg,
			Source:           ConfigSourceSettingsJSON,
			SourcePath:       settingsPath,
			HasRCFileServers: false,
		}, nil
	}

	// RC file exists - load and merge
	rcCfg, err := Load(rcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load RC file %s: %w", rcPath, err)
	}

	// Mark all RC file servers with their source
	for i := range rcCfg.ACPServers {
		rcCfg.ACPServers[i].Source = SourceRCFile
	}

	// Merge ACP servers: RC file servers take priority
	mergeResult := MergeACPServers(rcCfg.ACPServers, settingsCfg.ACPServers)

	// Build the final merged config
	// Use RC file config as base (for non-ACP settings like web, prompts, etc.)
	// but override ACP servers with the merged list
	mergedCfg := rcCfg
	mergedCfg.ACPServers = mergeResult.Items

	// Merge Web settings from settings.json into the merged config
	// These settings are typically configured via the UI and saved to settings.json,
	// not in the RC file. We need to preserve them even when an RC file is used.
	//
	// Auth settings (username/password for external access)
	if settingsCfg.Web.Auth != nil {
		mergedCfg.Web.Auth = settingsCfg.Web.Auth
	}

	// ExternalPort (external access port configuration)
	if mergedCfg.Web.ExternalPort == 0 && settingsCfg.Web.ExternalPort != 0 {
		mergedCfg.Web.ExternalPort = settingsCfg.Web.ExternalPort
	}

	// Hooks (lifecycle hooks for tunneling etc.) - merge if not set in RC file
	if mergedCfg.Web.Hooks.Up.Command == "" && settingsCfg.Web.Hooks.Up.Command != "" {
		mergedCfg.Web.Hooks.Up = settingsCfg.Web.Hooks.Up
	}
	if mergedCfg.Web.Hooks.Down.Command == "" && settingsCfg.Web.Hooks.Down.Command != "" {
		mergedCfg.Web.Hooks.Down = settingsCfg.Web.Hooks.Down
	}

	// Host setting (for external access - 0.0.0.0 vs 127.0.0.1)
	// If settings.json has 0.0.0.0 (external access enabled), use it
	if settingsCfg.Web.Host == "0.0.0.0" {
		mergedCfg.Web.Host = settingsCfg.Web.Host
	}

	// Load keychain password for the merged config
	// This loads the password from keychain if Auth is configured but password is empty
	if err := loadKeychainPassword(mergedCfg); err != nil {
		// Non-fatal, just log and continue
		_ = err
	}

	// Validate at least one ACP server
	if len(mergedCfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured")
	}

	return &LoadResult{
		Config:           mergedCfg,
		Source:           ConfigSourceRCFile, // Primary source is RC file
		SourcePath:       rcPath,
		RCFilePath:       rcPath,
		HasRCFileServers: mergeResult.HasRCFileItems,
	}, nil
}

// loadKeychainPassword loads the external access password from keychain if available.
func loadKeychainPassword(cfg *Config) error {
	if cfg.Web.Auth == nil || cfg.Web.Auth.Simple == nil {
		return nil
	}
	if !secrets.IsSupported() {
		return nil
	}
	if cfg.Web.Auth.Simple.Password != "" {
		// Password already set, no need to load from keychain
		return nil
	}
	password, err := secrets.GetExternalAccessPassword()
	if err != nil {
		return err
	}
	if password != "" {
		cfg.Web.Auth.Simple.Password = password
	}
	return nil
}
