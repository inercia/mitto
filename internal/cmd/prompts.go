package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	embeddedconfig "github.com/inercia/mitto/config"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

var promptsCmd = &cobra.Command{
	Use:   "prompts",
	Short: "Manage global prompts",
	Long: `Manage global prompts stored in MITTO_DIR/prompts/.

Prompts are markdown files with optional YAML front-matter that define
quick actions available in the chat interface.

Example prompt file (prompts/code-review.md):

  ---
  name: "Code Review"
  description: "Review the selected code"
  backgroundColor: "#E8F5E9"
  ---

  Please review the following code for:
  - Bugs and potential issues
  - Performance improvements
  - Code style and best practices`,
}

var promptsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all global prompts",
	Long: `List all global prompts from MITTO_DIR/prompts/.

Shows the name, description, and file path for each prompt.
Disabled prompts (enabled: false) are not shown.`,
	RunE: runPromptsList,
}

var (
	updateBuiltinDryRun bool
	updateBuiltinForce  bool
)

var promptsUpdateBuiltinCmd = &cobra.Command{
	Use:   "update-builtin",
	Short: "Update builtin prompts from embedded files",
	Long: `Update the builtin prompts in MITTO_DIR/prompts/builtin/ with the
latest versions embedded in the Mitto binary.

This command will overwrite any local modifications to builtin prompts.
Use --dry-run to see what would be updated without making changes.
Use --force to skip the confirmation prompt.

Note: Custom prompts in other directories are not affected.`,
	RunE: runPromptsUpdateBuiltin,
}

func init() {
	rootCmd.AddCommand(promptsCmd)
	promptsCmd.AddCommand(promptsListCmd)
	promptsCmd.AddCommand(promptsUpdateBuiltinCmd)

	promptsUpdateBuiltinCmd.Flags().BoolVar(&updateBuiltinDryRun, "dry-run", false,
		"Show what would be updated without making changes")
	promptsUpdateBuiltinCmd.Flags().BoolVarP(&updateBuiltinForce, "force", "f", false,
		"Skip confirmation prompt and overwrite without asking")
}

func runPromptsList(cmd *cobra.Command, args []string) error {
	promptsDir, err := appdir.PromptsDir()
	if err != nil {
		return fmt.Errorf("failed to get prompts directory: %w", err)
	}

	// Check if directory exists
	if _, err := os.Stat(promptsDir); os.IsNotExist(err) {
		fmt.Printf("Prompts directory: %s\n", promptsDir)
		fmt.Println("No prompts found. Create .md files in the prompts directory to add global prompts.")
		return nil
	}

	prompts, err := config.LoadPromptsFromDir(promptsDir)
	if err != nil {
		return fmt.Errorf("failed to load prompts: %w", err)
	}

	fmt.Printf("Prompts directory: %s\n\n", promptsDir)

	if len(prompts) == 0 {
		fmt.Println("No prompts found. Create .md files in the prompts directory to add global prompts.")
		return nil
	}

	// Use tabwriter for aligned output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tFILE")
	fmt.Fprintln(w, "----\t-----------\t----")

	for _, p := range prompts {
		desc := p.Description
		if desc == "" {
			desc = "-"
		}
		// Truncate long descriptions
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, desc, filepath.Base(p.Path))
	}
	w.Flush()

	fmt.Printf("\nTotal: %d prompt(s)\n", len(prompts))
	return nil
}

func runPromptsUpdateBuiltin(cmd *cobra.Command, args []string) error {
	builtinDir, err := appdir.BuiltinPromptsDir()
	if err != nil {
		return fmt.Errorf("failed to get builtin prompts directory: %w", err)
	}

	fmt.Printf("Builtin prompts directory: %s\n\n", builtinDir)

	// List embedded prompts
	embeddedFiles, err := embeddedconfig.ListEmbeddedPrompts()
	if err != nil {
		return fmt.Errorf("failed to list embedded prompts: %w", err)
	}

	if len(embeddedFiles) == 0 {
		fmt.Println("No embedded prompts found.")
		return nil
	}

	if updateBuiltinDryRun {
		fmt.Println("Dry run mode - no changes will be made.")
		fmt.Println()
		fmt.Println("The following prompts would be deployed:")
		for _, f := range embeddedFiles {
			targetPath := filepath.Join(builtinDir, f)
			if _, err := os.Stat(targetPath); err == nil {
				fmt.Printf("  [overwrite] %s\n", f)
			} else {
				fmt.Printf("  [new]       %s\n", f)
			}
		}
		fmt.Printf("\nTotal: %d prompt(s)\n", len(embeddedFiles))
		return nil
	}

	// Confirm before overwriting (unless --force is set)
	if !updateBuiltinForce {
		fmt.Println("WARNING: This will overwrite any local modifications to builtin prompts.")
		fmt.Print("Continue? [y/N] ")

		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Deploy with force=true to overwrite existing files
	result, err := embeddedconfig.DeployBuiltinPrompts(builtinDir, true)
	if err != nil {
		return fmt.Errorf("failed to deploy builtin prompts: %w", err)
	}

	// Report results
	if len(result.Deployed) > 0 {
		fmt.Println("\nDeployed prompts:")
		for _, f := range result.Deployed {
			fmt.Printf("  ✓ %s\n", f)
		}
	}

	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.Errors {
			fmt.Printf("  ✗ %s\n", e)
		}
	}

	fmt.Printf("\nTotal: %d deployed, %d errors\n", len(result.Deployed), len(result.Errors))
	return nil
}
