// Package config handles configuration loading and management for Mitto.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// ACPServer represents a single ACP server configuration.
type ACPServer struct {
	// Name is the identifier for this ACP server
	Name string
	// Command is the shell command to start the ACP server
	Command string
	// Prompts is an optional list of predefined prompts specific to this ACP server
	Prompts []WebPrompt
}

// WebPrompt represents a predefined prompt for the web interface.
type WebPrompt struct {
	// Name is the display name for the prompt button
	Name string `json:"name"`
	// Prompt is the actual prompt text to send
	Prompt string `json:"prompt"`
	// BackgroundColor is an optional hex color string for the prompt button (e.g., "#E8F5E9")
	BackgroundColor string `json:"backgroundColor,omitempty"`
}

// WebHook represents a shell command hook configuration.
type WebHook struct {
	// Command is the shell command to execute.
	// Supports ${PORT} placeholder which is replaced with the actual port number.
	Command string `json:"command,omitempty"`
	// Name is an optional display name for the hook (shown in output)
	Name string `json:"name,omitempty"`
}

// WebHooks contains lifecycle hooks for the web server.
type WebHooks struct {
	// Up is executed after the web server starts listening
	Up WebHook `json:"up,omitempty"`
	// Down is executed right before the web server shuts down
	Down WebHook `json:"down,omitempty"`
}

// SimpleAuth represents simple username/password authentication.
type SimpleAuth struct {
	// Username is the required username for authentication
	Username string `json:"username"`
	// Password is the required password for authentication (stored as bcrypt hash in config recommended)
	Password string `json:"password"`
}

// AuthAllow represents IP-based authentication bypass configuration.
type AuthAllow struct {
	// IPs is a list of IP addresses or CIDR ranges that bypass authentication.
	// Examples: "127.0.0.1", "192.168.0.0/24", "::1"
	IPs []string `json:"ips,omitempty"`
}

// WebAuth represents authentication configuration for the web interface.
type WebAuth struct {
	// Simple enables simple username/password authentication when set
	Simple *SimpleAuth `json:"simple,omitempty"`
	// Allow contains IP addresses/CIDR ranges that bypass authentication
	Allow *AuthAllow `json:"allow,omitempty"`
}

// WebSecurity represents security configuration for the web interface.
type WebSecurity struct {
	// TrustedProxies is a list of IP addresses or CIDR ranges of trusted reverse proxies.
	// Only requests from these IPs will have X-Forwarded-For and X-Real-IP headers trusted.
	// If empty, these headers are never trusted (direct connections only).
	// Examples: "127.0.0.1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"
	TrustedProxies []string `json:"trusted_proxies,omitempty"`

	// AllowedOrigins is a list of allowed origins for WebSocket connections.
	// If empty, only same-origin requests are allowed.
	// Use "*" to allow all origins (not recommended for production).
	AllowedOrigins []string `json:"allowed_origins,omitempty"`

	// RateLimitRPS is the rate limit for API requests per second per IP.
	// Default: 10
	RateLimitRPS float64 `json:"rate_limit_rps,omitempty"`

	// RateLimitBurst is the maximum burst size for rate limiting.
	// Default: 20
	RateLimitBurst int `json:"rate_limit_burst,omitempty"`

	// MaxWSConnectionsPerIP is the maximum number of concurrent WebSocket connections per IP.
	// Default: 10
	MaxWSConnectionsPerIP int `json:"max_ws_connections_per_ip,omitempty"`

	// MaxWSMessageSize is the maximum size of a WebSocket message in bytes.
	// Default: 65536 (64KB)
	MaxWSMessageSize int64 `json:"max_ws_message_size,omitempty"`
}

// HotkeyConfig represents a keyboard shortcut configuration.
// Format: "modifier+modifier+key" (e.g., "cmd+ctrl+m", "ctrl+alt+space")
// Supported modifiers: cmd, ctrl, alt, shift
// Supported keys: a-z, 0-9, space, tab, return, escape, delete, f1-f12
type HotkeyConfig struct {
	// Enabled controls whether this hotkey is active (default: true)
	Enabled *bool `json:"enabled,omitempty"`
	// Key is the hotkey combination (e.g., "cmd+ctrl+m")
	Key string `json:"key,omitempty"`
}

// MacHotkeys represents macOS-specific hotkey configuration.
type MacHotkeys struct {
	// ShowHide is the hotkey to toggle app visibility (default: "cmd+ctrl+m")
	ShowHide *HotkeyConfig `json:"show_hide,omitempty"`
}

// NotificationSoundsConfig represents notification sound settings.
type NotificationSoundsConfig struct {
	// AgentCompleted enables a sound when the agent finishes a response (default: false)
	AgentCompleted bool `json:"agent_completed,omitempty"`
}

// NotificationsConfig represents notification settings.
type NotificationsConfig struct {
	// Sounds contains notification sound settings
	Sounds *NotificationSoundsConfig `json:"sounds,omitempty"`
}

// MacUIConfig represents macOS-specific UI configuration.
type MacUIConfig struct {
	// Hotkeys contains hotkey configuration for macOS
	Hotkeys *MacHotkeys `json:"hotkeys,omitempty"`
	// Notifications contains notification settings for macOS
	Notifications *NotificationsConfig `json:"notifications,omitempty"`
	// ShowInAllSpaces makes the window appear in all macOS Spaces (virtual desktops)
	// When enabled, the Mitto window will be visible across all Spaces.
	// Requires app restart to take effect. (default: false)
	ShowInAllSpaces bool `json:"show_in_all_spaces,omitempty"`
}

// ConfirmationsConfig represents confirmation dialog settings.
type ConfirmationsConfig struct {
	// DeleteSession controls whether to show confirmation when deleting a session (default: true)
	DeleteSession *bool `json:"delete_session,omitempty"`
	// QuitWithRunningSessions controls whether to show confirmation when quitting with running sessions (default: true)
	// This only applies to the macOS desktop app.
	QuitWithRunningSessions *bool `json:"quit_with_running_sessions,omitempty"`
}

// UIConfig represents UI configuration for the desktop app.
type UIConfig struct {
	// Confirmations contains confirmation dialog settings
	Confirmations *ConfirmationsConfig `json:"confirmations,omitempty"`
	// Mac contains macOS-specific UI configuration
	Mac *MacUIConfig `json:"mac,omitempty"`
}

// WebConfig represents web interface configuration.
type WebConfig struct {
	// Host is the HTTP server host/IP address (default: 127.0.0.1)
	// Use "0.0.0.0" to listen on all interfaces
	Host string `json:"host,omitempty"`
	// Port is the HTTP server port for local access (default: 8080, or random if 0)
	// This is the primary port used by the Web UI and macOS native app.
	Port int `json:"port,omitempty"`
	// ExternalPort is the HTTP server port for external access.
	// This port is only used when external access is enabled (Auth is configured).
	// The external listener binds to 0.0.0.0 on this port.
	// Values:
	//   -1 = disabled (no external listener, default)
	//    0 = random port (OS chooses an available port)
	//   >0 = specific port number
	// Note: omitempty is NOT used here because 0 is a valid value meaning "random port".
	ExternalPort int `json:"external_port"`
	// Theme is the UI theme/stylesheet to use.
	// Options: "default" (original Tailwind-based), "v2" (Clawdbot-inspired)
	// Default: "default"
	Theme string `json:"theme,omitempty"`
	// Hooks contains lifecycle hooks for the web server
	Hooks WebHooks `json:"hooks,omitempty"`
	// StaticDir is an optional directory to serve static files from instead of embedded assets.
	// When set, files are served from this directory, enabling hot-reloading during development.
	StaticDir string `json:"staticDir,omitempty"`
	// Auth contains authentication configuration
	Auth *WebAuth `json:"auth,omitempty"`
	// Security contains security configuration (rate limiting, WebSocket security, etc.)
	Security *WebSecurity `json:"security,omitempty"`
}

// Config represents the complete Mitto configuration.
type Config struct {
	// ACPServers is the list of configured ACP servers (order matters - first is default)
	ACPServers []ACPServer
	// Prompts is a list of predefined prompts for the dropup menu (global prompts)
	Prompts []WebPrompt
	// Web contains web interface configuration
	Web WebConfig
	// UI contains desktop app UI configuration
	UI UIConfig
	// Session contains session storage limits configuration (not exposed in Settings dialog)
	Session *SessionConfig
}

// rawACPServerConfig is used for YAML unmarshaling of ACP server entries.
type rawACPServerConfig struct {
	Command string `yaml:"command"`
	Prompts []struct {
		Name            string `yaml:"name"`
		Prompt          string `yaml:"prompt"`
		BackgroundColor string `yaml:"backgroundColor"`
	} `yaml:"prompts"`
}

// rawConfig is used for YAML unmarshaling to handle the map-based format.
type rawConfig struct {
	ACP []map[string]rawACPServerConfig `yaml:"acp"`
	// Prompts is the top-level prompts section for global prompts
	Prompts []struct {
		Name            string `yaml:"name"`
		Prompt          string `yaml:"prompt"`
		BackgroundColor string `yaml:"backgroundColor"`
	} `yaml:"prompts"`
	Web struct {
		Host         string `yaml:"host"`
		Port         int    `yaml:"port"`
		ExternalPort int    `yaml:"external_port"`
		Theme        string `yaml:"theme"`
		StaticDir    string `yaml:"static_dir"`
		Hooks        struct {
			Up struct {
				Command string `yaml:"command"`
				Name    string `yaml:"name"`
			} `yaml:"up"`
			Down struct {
				Command string `yaml:"command"`
				Name    string `yaml:"name"`
			} `yaml:"down"`
		} `yaml:"hooks"`
		Auth *struct {
			Simple *struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			} `yaml:"simple"`
			Allow *struct {
				IPs []string `yaml:"ips"`
			} `yaml:"allow"`
		} `yaml:"auth"`
		Security *struct {
			TrustedProxies        []string `yaml:"trusted_proxies"`
			AllowedOrigins        []string `yaml:"allowed_origins"`
			RateLimitRPS          float64  `yaml:"rate_limit_rps"`
			RateLimitBurst        int      `yaml:"rate_limit_burst"`
			MaxWSConnectionsPerIP int      `yaml:"max_ws_connections_per_ip"`
			MaxWSMessageSize      int64    `yaml:"max_ws_message_size"`
		} `yaml:"security"`
	} `yaml:"web"`
	UI *struct {
		Confirmations *struct {
			DeleteSession           *bool `yaml:"delete_session"`
			QuitWithRunningSessions *bool `yaml:"quit_with_running_sessions"`
		} `yaml:"confirmations"`
		Mac *struct {
			Hotkeys *struct {
				ShowHide *struct {
					Enabled *bool  `yaml:"enabled"`
					Key     string `yaml:"key"`
				} `yaml:"show_hide"`
			} `yaml:"hotkeys"`
			Notifications *struct {
				Sounds *struct {
					AgentCompleted bool `yaml:"agent_completed"`
				} `yaml:"sounds"`
			} `yaml:"notifications"`
			ShowInAllSpaces bool `yaml:"show_in_all_spaces"`
		} `yaml:"mac"`
	} `yaml:"ui"`
}

// DefaultConfigPath returns the default configuration file path for the current platform.
func DefaultConfigPath() string {
	// Check for environment variable override first
	if envPath := os.Getenv("MITTORC"); envPath != "" {
		return envPath
	}

	// Use platform-specific config directory
	var configDir string
	switch runtime.GOOS {
	case "windows":
		configDir = os.Getenv("APPDATA")
		if configDir == "" {
			configDir = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		configDir = home // macOS traditionally uses ~/.mittorc
	default: // linux and others
		if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
			configDir = xdgConfig
		} else {
			home, _ := os.UserHomeDir()
			configDir = home
		}
	}

	return filepath.Join(configDir, ".mittorc")
}

// Load reads and parses the configuration file from the given path.
// It supports both YAML and JSON formats, detected by file extension:
//   - .json: parsed as JSON (Settings format)
//   - .yaml, .yml, or any other extension: parsed as YAML
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	// Detect format by file extension
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" {
		return ParseJSON(data)
	}

	// Default to YAML for .yaml, .yml, or any other extension
	return Parse(data)
}

// ParseJSON parses JSON configuration data (Settings format) into a Config struct.
func ParseJSON(data []byte) (*Config, error) {
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	cfg := settings.ToConfig()

	if len(cfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured")
	}

	return cfg, nil
}

// Parse parses YAML configuration data into a Config struct.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	cfg := &Config{
		ACPServers: make([]ACPServer, 0, len(raw.ACP)),
	}

	for _, entry := range raw.ACP {
		for name, server := range entry {
			acpServer := ACPServer{
				Name:    name,
				Command: server.Command,
			}
			// Copy server-specific prompts
			for _, p := range server.Prompts {
				acpServer.Prompts = append(acpServer.Prompts, WebPrompt{
					Name:            p.Name,
					Prompt:          p.Prompt,
					BackgroundColor: p.BackgroundColor,
				})
			}
			cfg.ACPServers = append(cfg.ACPServers, acpServer)
		}
	}

	if len(cfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured")
	}

	// Populate global prompts (top-level)
	for _, p := range raw.Prompts {
		cfg.Prompts = append(cfg.Prompts, WebPrompt{
			Name:            p.Name,
			Prompt:          p.Prompt,
			BackgroundColor: p.BackgroundColor,
		})
	}

	// Populate web config
	cfg.Web.Host = raw.Web.Host
	cfg.Web.Port = raw.Web.Port
	cfg.Web.ExternalPort = raw.Web.ExternalPort
	cfg.Web.Theme = raw.Web.Theme
	cfg.Web.StaticDir = raw.Web.StaticDir
	cfg.Web.Hooks.Up.Command = raw.Web.Hooks.Up.Command
	cfg.Web.Hooks.Up.Name = raw.Web.Hooks.Up.Name
	cfg.Web.Hooks.Down.Command = raw.Web.Hooks.Down.Command
	cfg.Web.Hooks.Down.Name = raw.Web.Hooks.Down.Name

	// Populate auth config
	if raw.Web.Auth != nil {
		cfg.Web.Auth = &WebAuth{}
		if raw.Web.Auth.Simple != nil {
			cfg.Web.Auth.Simple = &SimpleAuth{
				Username: raw.Web.Auth.Simple.Username,
				Password: raw.Web.Auth.Simple.Password,
			}
		}
		if raw.Web.Auth.Allow != nil && len(raw.Web.Auth.Allow.IPs) > 0 {
			cfg.Web.Auth.Allow = &AuthAllow{
				IPs: raw.Web.Auth.Allow.IPs,
			}
		}
	}

	// Populate security config
	if raw.Web.Security != nil {
		cfg.Web.Security = &WebSecurity{
			TrustedProxies:        raw.Web.Security.TrustedProxies,
			AllowedOrigins:        raw.Web.Security.AllowedOrigins,
			RateLimitRPS:          raw.Web.Security.RateLimitRPS,
			RateLimitBurst:        raw.Web.Security.RateLimitBurst,
			MaxWSConnectionsPerIP: raw.Web.Security.MaxWSConnectionsPerIP,
			MaxWSMessageSize:      raw.Web.Security.MaxWSMessageSize,
		}
	}

	// Populate UI config
	if raw.UI != nil {
		// Populate confirmations
		if raw.UI.Confirmations != nil {
			cfg.UI.Confirmations = &ConfirmationsConfig{
				DeleteSession:           raw.UI.Confirmations.DeleteSession,
				QuitWithRunningSessions: raw.UI.Confirmations.QuitWithRunningSessions,
			}
		}

		// Populate Mac-specific config
		if raw.UI.Mac != nil {
			cfg.UI.Mac = &MacUIConfig{}

			// Populate hotkeys
			if raw.UI.Mac.Hotkeys != nil {
				cfg.UI.Mac.Hotkeys = &MacHotkeys{}
				if raw.UI.Mac.Hotkeys.ShowHide != nil {
					cfg.UI.Mac.Hotkeys.ShowHide = &HotkeyConfig{
						Enabled: raw.UI.Mac.Hotkeys.ShowHide.Enabled,
						Key:     raw.UI.Mac.Hotkeys.ShowHide.Key,
					}
				}
			}

			// Populate notifications
			if raw.UI.Mac.Notifications != nil {
				cfg.UI.Mac.Notifications = &NotificationsConfig{}
				if raw.UI.Mac.Notifications.Sounds != nil {
					cfg.UI.Mac.Notifications.Sounds = &NotificationSoundsConfig{
						AgentCompleted: raw.UI.Mac.Notifications.Sounds.AgentCompleted,
					}
				}
			}

			// Populate show in all spaces setting
			cfg.UI.Mac.ShowInAllSpaces = raw.UI.Mac.ShowInAllSpaces
		}
	}

	return cfg, nil
}

// DefaultServer returns the default ACP server (first in the list).
func (c *Config) DefaultServer() *ACPServer {
	if len(c.ACPServers) == 0 {
		return nil
	}
	return &c.ACPServers[0]
}

// GetServer returns the ACP server with the given name.
func (c *Config) GetServer(name string) (*ACPServer, error) {
	for i := range c.ACPServers {
		if c.ACPServers[i].Name == name {
			return &c.ACPServers[i], nil
		}
	}
	return nil, fmt.Errorf("ACP server %q not found in configuration", name)
}

// ServerNames returns a list of all configured server names.
func (c *Config) ServerNames() []string {
	names := make([]string, len(c.ACPServers))
	for i, srv := range c.ACPServers {
		names[i] = srv.Name
	}
	return names
}

// DefaultShowHideHotkey is the default hotkey for toggling app visibility.
const DefaultShowHideHotkey = "cmd+ctrl+m"

// GetShowHideHotkey returns the configured show/hide hotkey.
// Returns the hotkey string and whether it's enabled.
// If not configured, returns the default ("cmd+ctrl+m", true).
func (c *Config) GetShowHideHotkey() (key string, enabled bool) {
	// Default values
	key = DefaultShowHideHotkey
	enabled = true

	if c.UI.Mac == nil || c.UI.Mac.Hotkeys == nil || c.UI.Mac.Hotkeys.ShowHide == nil {
		return key, enabled
	}

	hk := c.UI.Mac.Hotkeys.ShowHide

	// Check if explicitly disabled
	if hk.Enabled != nil && !*hk.Enabled {
		return "", false
	}

	// Use custom key if provided
	if hk.Key != "" {
		key = hk.Key
	}

	return key, enabled
}

// ShouldConfirmQuitWithRunningSessions returns whether to show a confirmation dialog
// when quitting the app with running sessions. Defaults to true.
func (c *Config) ShouldConfirmQuitWithRunningSessions() bool {
	if c.UI.Confirmations == nil || c.UI.Confirmations.QuitWithRunningSessions == nil {
		return true // Default to true
	}
	return *c.UI.Confirmations.QuitWithRunningSessions
}
