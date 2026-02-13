package cmd

import (
	"github.com/spf13/cobra"
)

// toolsCmd represents the tools command
var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Utility tools for maintenance and debugging",
	Long: `Utility tools for maintenance and debugging.

These commands provide various utilities for managing sessions,
debugging issues, and performing maintenance tasks.`,
}

func init() {
	rootCmd.AddCommand(toolsCmd)
}
