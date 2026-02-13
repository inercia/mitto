package cmd

import (
	"github.com/spf13/cobra"
)

// toolsSessionCmd represents the tools session command
var toolsSessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Session management tools",
	Long: `Session management tools.

Commands for managing, inspecting, and maintaining session data.`,
}

func init() {
	toolsCmd.AddCommand(toolsSessionCmd)
}
