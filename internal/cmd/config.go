package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	embeddedconfig "github.com/inercia/mitto/config"
)

var (
	configOutputPath string
	configForce      bool
)

// configCmd represents the config parent command
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Mitto configuration",
	Long: `Manage Mitto configuration files.

Use the subcommands to create or manage configuration files.`,
}

// configCreateCmd represents the config create subcommand
var configCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a default configuration file",
	Long: `Create a default configuration file at ~/.mittorc.

This command writes the embedded default configuration (config.default.yaml)
to the specified path. The configuration file contains default settings for
ACP servers, web interface, and other options.

After creating the file, review and customize it for your environment.

Examples:
  mitto config create                    # Create ~/.mittorc
  mitto config create --output /path/to  # Create /path/to/.mittorc
  mitto config create --force            # Overwrite existing file`,
	RunE: runConfigCreate,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configCreateCmd)

	configCreateCmd.Flags().StringVarP(&configOutputPath, "output", "o", "",
		"Directory to write the config file (default: $HOME)")
	configCreateCmd.Flags().BoolVarP(&configForce, "force", "f", false,
		"Overwrite existing configuration file without prompting")
}

func runConfigCreate(cmd *cobra.Command, args []string) error {
	// Determine output directory
	outputDir := configOutputPath
	if outputDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		outputDir = homeDir
	}

	// Build the full path
	configPath := filepath.Join(outputDir, ".mittorc")

	// Check if file already exists
	if _, err := os.Stat(configPath); err == nil && !configForce {
		fmt.Printf("⚠️  Configuration file already exists: %s\n", configPath)
		fmt.Println("Use --force to overwrite the existing file.")
		return nil
	}

	// Write the embedded default config
	if err := os.WriteFile(configPath, embeddedconfig.DefaultConfigYAML, 0644); err != nil {
		return fmt.Errorf("failed to write configuration file: %w", err)
	}

	fmt.Printf("✅ Configuration file created: %s\n", configPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Review and customize the configuration file")
	fmt.Println("  2. Configure your ACP server (auggie, claude-code, etc.)")
	fmt.Println("  3. Run 'mitto web' to start the web interface")
	fmt.Println()
	fmt.Println("For more information, see: https://github.com/inercia/mitto/blob/main/docs/config/README.md")

	return nil
}
