// Package cmd provides the CLI commands for Mitto.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/config"
)

var (
	// Global flags
	acpServerName string
	configPath    string
	autoApprove   bool
	debug         bool

	// Loaded configuration
	cfg *config.Config
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

		// Load configuration
		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Set default config path
	defaultConfigPath := config.DefaultConfigPath()

	// Global flags
	rootCmd.PersistentFlags().StringVar(&acpServerName, "acp", "", "ACP server name to use (defaults to first in config)")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", defaultConfigPath, "Configuration file path")
	rootCmd.PersistentFlags().BoolVar(&autoApprove, "auto-approve", false, "Automatically approve permission requests")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "Enable debug logging")
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

// mustGetCwd returns the current working directory or "." if it fails.
func mustGetCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
