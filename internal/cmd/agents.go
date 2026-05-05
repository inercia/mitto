package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	embeddedconfig "github.com/inercia/mitto/config"
	"github.com/inercia/mitto/internal/appdir"
)

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Manage agent configurations",
	Long: `Manage agent configurations stored in MITTO_DIR/agents/.

Agent configurations are organized in subdirectories under MITTO_DIR/agents/:
  - builtin/   Agents shipped with Mitto (deployed/updated automatically)
  - custom/    User-created agent definitions (create manually)
  - <any>/     Any other subdirectory name works too

Each agent is a subdirectory containing a metadata.yaml file and an optional
cmds/ directory with scripts (install.sh, status.sh, mcp-list.sh, mcp-install.sh).`,
}

var agentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List deployed agent configurations",
	Long: `List all deployed agent configurations from MITTO_DIR/agents/.

Shows the name, source directory, and description for each agent.
Agents can be defined in any subdirectory under MITTO_DIR/agents/
(e.g., agents/builtin/, agents/custom/, agents/my-company/).`,
	RunE: runAgentsList,
}

var (
	updateBuiltinAgentsDryRun bool
	updateBuiltinAgentsForce  bool
)

var agentsUpdateBuiltinCmd = &cobra.Command{
	Use:   "update-builtin",
	Short: "Update builtin agent configurations from embedded files",
	Long: `Update the builtin agent configs in MITTO_DIR/agents/builtin/ with the
latest versions embedded in the Mitto binary.

This command will overwrite any local modifications to builtin agent configs.
Use --dry-run to see what would be updated without making changes.
Use --force to skip the confirmation prompt.

Note: Custom agent configs in other directories are not affected.`,
	RunE: runAgentsUpdateBuiltin,
}

func init() {
	rootCmd.AddCommand(agentsCmd)
	agentsCmd.AddCommand(agentsListCmd)
	agentsCmd.AddCommand(agentsUpdateBuiltinCmd)

	agentsUpdateBuiltinCmd.Flags().BoolVar(&updateBuiltinAgentsDryRun, "dry-run", false,
		"Show what would be updated without making changes")
	agentsUpdateBuiltinCmd.Flags().BoolVarP(&updateBuiltinAgentsForce, "force", "f", false,
		"Skip confirmation prompt and overwrite without asking")
}

// agentMetadata holds the parsed content of a metadata.yaml file.
type agentMetadata struct {
	Name        string `yaml:"name"`
	DisplayName string `yaml:"displayName"`
	ACPId       string `yaml:"acpId"`
	Description string `yaml:"description"`
}

func runAgentsList(cmd *cobra.Command, args []string) error {
	agentsDir, err := appdir.AgentsDir()
	if err != nil {
		return fmt.Errorf("failed to get agents directory: %w", err)
	}

	fmt.Printf("Agents directory: %s\n\n", agentsDir)

	// Check if directory exists
	if _, err := os.Stat(agentsDir); os.IsNotExist(err) {
		fmt.Println("No agent configurations found.")
		return nil
	}

	// Read top-level subdirectories (e.g., "builtin", "custom", etc.)
	topEntries, err := os.ReadDir(agentsDir)
	if err != nil {
		return fmt.Errorf("failed to read agents directory: %w", err)
	}

	type agentEntry struct {
		source  string // e.g., "builtin", "custom"
		dirName string // e.g., "augment", "claude-code"
		meta    agentMetadata
	}

	var agents []agentEntry
	for _, topEntry := range topEntries {
		if !topEntry.IsDir() {
			continue
		}
		sourceName := topEntry.Name()
		sourceDir := filepath.Join(agentsDir, sourceName)

		entries, err := os.ReadDir(sourceDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			metaPath := filepath.Join(sourceDir, entry.Name(), "metadata.yaml")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				// Skip agents without metadata
				continue
			}
			var meta agentMetadata
			if err := yaml.Unmarshal(data, &meta); err != nil {
				continue
			}
			agents = append(agents, agentEntry{source: sourceName, dirName: entry.Name(), meta: meta})
		}
	}

	if len(agents) == 0 {
		fmt.Println("No agent configurations found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSOURCE\tDESCRIPTION")
	fmt.Fprintln(w, "----\t------\t-----------")
	for _, a := range agents {
		desc := a.meta.Description
		if desc == "" {
			desc = "-"
		}
		if len(desc) > 55 {
			desc = desc[:52] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", a.meta.Name, a.source, desc)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d agent(s)\n", len(agents))
	return nil
}

func runAgentsUpdateBuiltin(cmd *cobra.Command, args []string) error {
	builtinDir, err := appdir.BuiltinAgentsDir()
	if err != nil {
		return fmt.Errorf("failed to get builtin agents directory: %w", err)
	}

	fmt.Printf("Builtin agents directory: %s\n\n", builtinDir)

	// List embedded agents
	embeddedAgents, err := embeddedconfig.ListEmbeddedAgents()
	if err != nil {
		return fmt.Errorf("failed to list embedded agents: %w", err)
	}

	if len(embeddedAgents) == 0 {
		fmt.Println("No embedded agents found.")
		return nil
	}

	if updateBuiltinAgentsDryRun {
		fmt.Println("Dry run mode - no changes will be made.")
		fmt.Println()
		fmt.Println("The following agents would be deployed:")
		for _, name := range embeddedAgents {
			agentDir := filepath.Join(builtinDir, name)
			if _, err := os.Stat(agentDir); err == nil {
				fmt.Printf("  [overwrite] %s/\n", name)
			} else {
				fmt.Printf("  [new]       %s/\n", name)
			}
		}
		fmt.Printf("\nTotal: %d agent(s)\n", len(embeddedAgents))
		return nil
	}

	// Confirm before overwriting (unless --force is set)
	if !updateBuiltinAgentsForce {
		fmt.Println("WARNING: This will overwrite any local modifications to builtin agent configurations.")
		fmt.Print("Continue? [y/N] ")

		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Clean the entire builtin agents directory and recreate it
	// This ensures removed agents don't linger (recursive structure)
	if err := os.RemoveAll(builtinDir); err != nil {
		return fmt.Errorf("failed to clean builtin agents directory: %w", err)
	}
	if err := os.MkdirAll(builtinDir, 0755); err != nil {
		return fmt.Errorf("failed to recreate builtin agents directory: %w", err)
	}

	// Deploy with force=true to overwrite existing files
	result, err := embeddedconfig.DeployBuiltinAgents(builtinDir, true)
	if err != nil {
		return fmt.Errorf("failed to deploy builtin agents: %w", err)
	}

	// Report results
	if len(result.Deployed) > 0 {
		fmt.Println("\nDeployed files:")
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
