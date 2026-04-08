package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	embeddedconfig "github.com/inercia/mitto/config"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/processors"
)

var processorsCmd = &cobra.Command{
	Use:   "processors",
	Short: "Manage global processors",
	Long: `Manage global processors stored in MITTO_DIR/processors/.

Processors are YAML files that define pre/post processing of messages.
They can inject context, transform messages, or execute external commands.

Example processor file (processors/git-context.yaml):

  name: git-context
  description: "Adds recent git commits to context"
  when: first
  command: ./git-context.sh
  input: message
  output: prepend`,
}

var processorsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all global processors",
	Long: `List all global processors from MITTO_DIR/processors/.

Shows the name, description, priority, and file path for each processor.
Disabled processors are marked accordingly.`,
	RunE: runProcessorsList,
}

var (
	processorsListDir             string
	updateBuiltinProcessorsDryRun bool
	updateBuiltinProcessorsForce  bool
)

var processorsUpdateBuiltinCmd = &cobra.Command{
	Use:   "update-builtin",
	Short: "Update builtin processors from embedded files",
	Long: `Update the builtin processors in MITTO_DIR/processors/builtin/ with the
latest versions embedded in the Mitto binary.

This command will overwrite any local modifications to builtin processors.
Use --dry-run to see what would be updated without making changes.
Use --force to skip the confirmation prompt.

Note: Custom processors in other directories are not affected.`,
	RunE: runProcessorsUpdateBuiltin,
}

func init() {
	rootCmd.AddCommand(processorsCmd)
	processorsCmd.AddCommand(processorsListCmd)
	processorsCmd.AddCommand(processorsUpdateBuiltinCmd)

	processorsListCmd.Flags().StringVarP(&processorsListDir, "dir", "d", "",
		"Additional directory to search for processors (e.g., workspace .mitto/processors)")

	processorsUpdateBuiltinCmd.Flags().BoolVar(&updateBuiltinProcessorsDryRun, "dry-run", false,
		"Show what would be updated without making changes")
	processorsUpdateBuiltinCmd.Flags().BoolVarP(&updateBuiltinProcessorsForce, "force", "f", false,
		"Skip confirmation prompt and overwrite without asking")
}

func runProcessorsList(cmd *cobra.Command, args []string) error {
	processorsDir, err := appdir.ProcessorsDir()
	if err != nil {
		return fmt.Errorf("failed to get processors directory: %w", err)
	}

	// Check if directory exists
	if _, err := os.Stat(processorsDir); os.IsNotExist(err) {
		fmt.Printf("Processors directory: %s\n", processorsDir)
		fmt.Println("No processors found. Create .yaml files in the processors directory to add global processors.")
		return nil
	}

	mgr := processors.NewManager(processorsDir, nil)
	if err := mgr.Load(); err != nil {
		return fmt.Errorf("failed to load processors: %w", err)
	}

	// Merge workspace processors from --dir if specified
	if processorsListDir != "" {
		mgr = mgr.CloneWithDirProcessors([]string{processorsListDir}, nil)
		fmt.Printf("Processors directory: %s\n", processorsDir)
		fmt.Printf("Workspace directory:  %s\n\n", processorsListDir)
	} else {
		fmt.Printf("Processors directory: %s\n\n", processorsDir)
	}

	procs := mgr.Processors()

	if len(procs) == 0 {
		fmt.Println("No processors found. Create .yaml files in the processors directory to add global processors.")
		return nil
	}

	// Use tabwriter for aligned output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tPRIORITY\tWHEN\tFILE")
	fmt.Fprintln(w, "----\t-----------\t--------\t----\t----")

	for _, p := range procs {
		desc := p.Description
		if desc == "" {
			desc = "-"
		}
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", p.Name, desc, p.GetPriority(), p.When, filepath.Base(p.FilePath))
	}
	w.Flush()

	fmt.Printf("\nTotal: %d processor(s)\n", len(procs))
	return nil
}

func runProcessorsUpdateBuiltin(cmd *cobra.Command, args []string) error {
	builtinDir, err := appdir.BuiltinProcessorsDir()
	if err != nil {
		return fmt.Errorf("failed to get builtin processors directory: %w", err)
	}

	fmt.Printf("Builtin processors directory: %s\n\n", builtinDir)

	// List embedded processors
	embeddedFiles, err := embeddedconfig.ListEmbeddedProcessors()
	if err != nil {
		return fmt.Errorf("failed to list embedded processors: %w", err)
	}

	if len(embeddedFiles) == 0 {
		fmt.Println("No embedded processors found.")
		return nil
	}

	if updateBuiltinProcessorsDryRun {
		fmt.Println("Dry run mode - no changes will be made.")
		fmt.Println()
		fmt.Println("The following processors would be deployed:")
		for _, f := range embeddedFiles {
			targetPath := filepath.Join(builtinDir, f)
			if _, err := os.Stat(targetPath); err == nil {
				fmt.Printf("  [overwrite] %s\n", f)
			} else {
				fmt.Printf("  [new]       %s\n", f)
			}
		}
		fmt.Printf("\nTotal: %d processor(s)\n", len(embeddedFiles))
		return nil
	}

	// Confirm before overwriting (unless --force is set)
	if !updateBuiltinProcessorsForce {
		fmt.Println("WARNING: This will overwrite any local modifications to builtin processors.")
		fmt.Print("Continue? [y/N] ")

		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Clean existing *.yaml files in the builtin directory before deploying
	// This ensures removed processors don't linger
	existingFiles, err := filepath.Glob(filepath.Join(builtinDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to list existing processors: %w", err)
	}
	for _, f := range existingFiles {
		if err := os.Remove(f); err != nil {
			return fmt.Errorf("failed to remove old processor %s: %w", filepath.Base(f), err)
		}
	}

	// Deploy with force=true to overwrite existing files
	result, err := embeddedconfig.DeployBuiltinProcessors(builtinDir, true)
	if err != nil {
		return fmt.Errorf("failed to deploy builtin processors: %w", err)
	}

	// Report results
	if len(result.Deployed) > 0 {
		fmt.Println("\nDeployed processors:")
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
