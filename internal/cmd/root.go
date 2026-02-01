// Package cmd provides the CLI commands for Mitto.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
)

var (
	// Global flags
	acpServerName string
	configPath    string // Legacy YAML config override
	autoApprove   bool
	debug         bool
	logLevel      string   // --log-level flag (debug, info, warn, error)
	dirFlags      []string // --dir flags: can be "path" or "server:path"
	logFile       string
	logComponents string

	// Loaded configuration
	cfg *config.Config
	// configResult contains metadata about where config was loaded from
	configResult *config.LoadResult
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "mitto",
	Short: "Mitto - A CLI tool for interacting with ACP servers",
	Long: `Mitto is a command-line interface for communicating with
Agent Communication Protocol (ACP) servers.

It allows you to interactively chat with AI coding agents
like auggie, claude-code, and others that implement ACP.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for help and completion commands
		if cmd.Name() == "help" || cmd.Name() == "completion" {
			return nil
		}

		// Initialize logging
		// Priority: --log-level flag > --debug flag > default (info)
		effectiveLogLevel := "info"
		if logLevel != "" {
			effectiveLogLevel = logLevel
		} else if debug {
			effectiveLogLevel = "debug"
		}
		var components []string
		if logComponents != "" {
			for _, c := range strings.Split(logComponents, ",") {
				c = strings.TrimSpace(c)
				if c != "" {
					components = append(components, c)
				}
			}
		}
		if err := logging.Initialize(logging.Config{
			Level:      effectiveLogLevel,
			LogFile:    logFile,
			Components: components,
		}); err != nil {
			return fmt.Errorf("failed to initialize logging: %w", err)
		}

		// Ensure Mitto directory exists
		if err := appdir.EnsureDir(); err != nil {
			return fmt.Errorf("failed to create Mitto directory: %w", err)
		}
		// Load configuration using the hierarchy:
		// 1. --config flag (explicit path) takes highest priority
		// 2. RC file (~/.mittorc) if it exists
		// 3. settings.json (created from embedded defaults if needed)
		//
		// This matches the macOS app behavior, allowing the web interface
		// to work without requiring an RC file.
		var err error
		if configPath != "" {
			// Load from specified config file (YAML or JSON format)
			cfg, err = config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to load configuration from %s: %w", configPath, err)
			}
			configResult = &config.LoadResult{
				Config:     cfg,
				Source:     config.ConfigSourceCustomFile,
				SourcePath: configPath,
			}
		} else {
			// Use hierarchy with fallback to settings.json
			// This allows the web interface to work without an RC file
			configResult, err = config.LoadSettingsWithFallback()
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}
			cfg = configResult.Config
		}
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		// Clean up logging resources
		return logging.Close()
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&acpServerName, "acp", "", "ACP server name to use (defaults to first in config)")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Configuration file path (YAML or JSON format, overrides settings.json)")
	rootCmd.PersistentFlags().BoolVar(&autoApprove, "auto-approve", false, "Automatically approve permission requests")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "Enable debug logging (shorthand for --log-level=debug)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "Log level: debug, info, warn, error (default: info)")
	rootCmd.PersistentFlags().StringArrayVarP(&dirFlags, "dir", "d", nil, "Working directory for ACP sessions. Can be specified multiple times.\nFormat: [server-name:]path (e.g., --dir /path or --dir auggie:/path)")
	rootCmd.PersistentFlags().StringVarP(&logFile, "logfile", "l", "", "Log file path (logs are also written to console)")
	rootCmd.PersistentFlags().StringVar(&logComponents, "log-components", "", "Comma-separated list of components to log (e.g., 'web,session,acp'). Empty means all components.")
}

// getSelectedServer returns the ACP server to use based on flags and config.
func getSelectedServer() (*config.ACPServer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}

	if acpServerName != "" {
		return cfg.GetServer(acpServerName)
	}

	server := cfg.DefaultServer()
	if server == nil {
		return nil, fmt.Errorf("no ACP servers configured")
	}
	return server, nil
}

// Workspace represents a working directory associated with an ACP server.
type Workspace struct {
	ServerName string // ACP server name (from config)
	Server     *config.ACPServer
	Dir        string // Absolute path to working directory
}

// parseWorkspaces parses the --dir flags and returns a list of workspaces.
// Each flag can be either "path" or "server:path".
// If no flags are provided, uses the current working directory with the default server.
func parseWorkspaces() ([]Workspace, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}

	defaultServer := cfg.DefaultServer()
	if defaultServer == nil {
		return nil, fmt.Errorf("no ACP servers configured")
	}

	// If no --dir flags provided, use current directory with default server
	if len(dirFlags) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			wd = "."
		}
		return []Workspace{{
			ServerName: defaultServer.Name,
			Server:     defaultServer,
			Dir:        wd,
		}}, nil
	}

	// Parse each --dir flag
	workspaces := make([]Workspace, 0, len(dirFlags))
	seenDirs := make(map[string]bool) // Track directories to enforce one-dir <-> one-ACP

	for _, dirFlag := range dirFlags {
		serverName := ""
		dirPath := dirFlag

		// Check for server:path format
		if idx := strings.Index(dirFlag, ":"); idx != -1 {
			// Handle Windows absolute paths like C:\path
			if idx == 1 && len(dirFlag) > 2 && (dirFlag[0] >= 'A' && dirFlag[0] <= 'Z' || dirFlag[0] >= 'a' && dirFlag[0] <= 'z') {
				// This is likely a Windows path, not a server:path format
				serverName = ""
				dirPath = dirFlag
			} else {
				serverName = dirFlag[:idx]
				dirPath = dirFlag[idx+1:]
			}
		}

		// Resolve to absolute path
		absPath, err := filepath.Abs(dirPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve path %q: %w", dirPath, err)
		}

		// Verify the directory exists
		info, err := os.Stat(absPath)
		if err != nil {
			return nil, fmt.Errorf("directory does not exist: %s", absPath)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("path is not a directory: %s", absPath)
		}

		// Check for duplicate directories
		if seenDirs[absPath] {
			return nil, fmt.Errorf("duplicate directory specified: %s", absPath)
		}
		seenDirs[absPath] = true

		// Get the server
		var server *config.ACPServer
		if serverName != "" {
			server, err = cfg.GetServer(serverName)
			if err != nil {
				return nil, fmt.Errorf("unknown ACP server %q: %w", serverName, err)
			}
		} else {
			server = defaultServer
			serverName = defaultServer.Name
		}

		workspaces = append(workspaces, Workspace{
			ServerName: serverName,
			Server:     server,
			Dir:        absPath,
		})
	}

	return workspaces, nil
}

// getWorkingDir returns the first working directory from parsed workspaces.
// This is for backward compatibility with code that expects a single directory.
// Deprecated: Use parseWorkspaces() for multi-directory support.
func getWorkingDir() (string, error) {
	workspaces, err := parseWorkspaces()
	if err != nil {
		return "", err
	}
	if len(workspaces) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			return ".", nil
		}
		return wd, nil
	}
	return workspaces[0].Dir, nil
}
