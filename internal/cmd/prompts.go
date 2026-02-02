package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

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

func init() {
	rootCmd.AddCommand(promptsCmd)
	promptsCmd.AddCommand(promptsListCmd)
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
