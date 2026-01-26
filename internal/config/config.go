// Package config handles configuration loading and management for Mitto.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// ACPServer represents a single ACP server configuration.
type ACPServer struct {
	// Name is the identifier for this ACP server
	Name string
	// Command is the shell command to start the ACP server
	Command string
}

// WebPrompt represents a predefined prompt for the web interface.
type WebPrompt struct {
	// Name is the display name for the prompt button
	Name string `json:"name"`
	// Prompt is the actual prompt text to send
	Prompt string `json:"prompt"`
}

// WebConfig represents web interface configuration.
type WebConfig struct {
	// Host is the HTTP server host/IP address (default: 127.0.0.1)
	// Use "0.0.0.0" to listen on all interfaces
	Host string `json:"host,omitempty"`
	// Port is the HTTP server port (default: 8080)
	Port int `json:"port,omitempty"`
	// Prompts is a list of predefined prompts for the dropup menu
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// StaticDir is an optional directory to serve static files from instead of embedded assets.
	// When set, files are served from this directory, enabling hot-reloading during development.
	StaticDir string `json:"staticDir,omitempty"`
}

// Config represents the complete Mitto configuration.
type Config struct {
	// ACPServers is the list of configured ACP servers (order matters - first is default)
	ACPServers []ACPServer
	// Web contains web interface configuration
	Web WebConfig
}

// rawConfig is used for YAML unmarshaling to handle the map-based format.
type rawConfig struct {
	ACP []map[string]struct {
		Command string `yaml:"command"`
	} `yaml:"acp"`
	Web struct {
		Host      string `yaml:"host"`
		Port      int    `yaml:"port"`
		StaticDir string `yaml:"static_dir"`
		Prompts   []struct {
			Name   string `yaml:"name"`
			Prompt string `yaml:"prompt"`
		} `yaml:"prompts"`
	} `yaml:"web"`
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
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	return Parse(data)
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
			cfg.ACPServers = append(cfg.ACPServers, ACPServer{
				Name:    name,
				Command: server.Command,
			})
		}
	}

	if len(cfg.ACPServers) == 0 {
		return nil, fmt.Errorf("no ACP servers configured")
	}

	// Populate web config
	cfg.Web.Host = raw.Web.Host
	cfg.Web.Port = raw.Web.Port
	cfg.Web.StaticDir = raw.Web.StaticDir
	for _, p := range raw.Web.Prompts {
		cfg.Web.Prompts = append(cfg.Web.Prompts, WebPrompt{
			Name:   p.Name,
			Prompt: p.Prompt,
		})
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
