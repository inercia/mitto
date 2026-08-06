package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inercia/mitto/internal/prompts/migrate"
)

// migrate flags
var (
	migrateExtraDir string
	migrateDryRun   bool
	migrateCheck    bool
)

var promptsMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate prompt files to the current schema",
	Long: `Scan the prompt tree for .prompt.yaml files written against a legacy
schema and rewrite them onto the current one (mitto-r6j.3).

Scans the same directories as "mitto prompts verify" (MITTO_DIR/prompts,
which already recurses into builtin/, plus an optional --dir). Currently the
only registered migration is "0001-loop-grouped-triggers", which rewrites
the pre-r6j flat loop: schema (single implicit trigger, flat delay/
condition/... siblings) onto the grouped multi-trigger schema (trigger: [...]
plus nested schedule/onCompletion/onTasks blocks).

A file already on the current schema is left completely untouched (no
write, no mtime change). Loading a legacy-schema file always migrates it in
memory with a WARN regardless of this command — this command exists for
bulk/CI use: --dry-run reports what would change without writing, --check
exits non-zero if any file would change.`,
	RunE: runPromptsMigrate,
}

func init() {
	promptsCmd.AddCommand(promptsMigrateCmd)

	promptsMigrateCmd.Flags().StringVar(&migrateExtraDir, "dir", "",
		"Additional prompts directory to include (same semantics as 'prompts verify')")
	promptsMigrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false,
		"Report what would change without writing")
	promptsMigrateCmd.Flags().BoolVar(&migrateCheck, "check", false,
		"Exit non-zero if any file would change (for CI)")
}

func runPromptsMigrate(cmd *cobra.Command, args []string) error {
	promptDirs, _, err := promptTreeDirs(migrateExtraDir)
	if err != nil {
		return err
	}

	var changedFiles []string
	var failedFiles []string

	for _, dir := range promptDirs {
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			continue
		}
		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".prompt.yaml") {
				return nil
			}

			data, readErr := os.ReadFile(path)
			if readErr != nil {
				failedFiles = append(failedFiles, fmt.Sprintf("%s: %v", path, readErr))
				return nil
			}
			migrated, result, migErr := migrate.MigrateYAML(data)
			if migErr != nil {
				failedFiles = append(failedFiles, fmt.Sprintf("%s: %v", path, migErr))
				return nil
			}
			if !result.Changed {
				return nil
			}
			changedFiles = append(changedFiles, path)
			if migrateDryRun || migrateCheck {
				return nil
			}
			migrate.WriteBackIfNeeded(path, migrated, result)
			return nil
		})
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", dir, walkErr)
		}
	}

	verb := "Migrated"
	if migrateDryRun || migrateCheck {
		verb = "Would migrate"
	}
	if len(changedFiles) == 0 {
		fmt.Println("No prompt files need migration.")
	} else {
		fmt.Printf("%s %d prompt file(s):\n", verb, len(changedFiles))
		for _, f := range changedFiles {
			fmt.Printf("  %s\n", f)
		}
	}
	if len(failedFiles) > 0 {
		fmt.Println("\nErrors:")
		for _, f := range failedFiles {
			fmt.Printf("  %s\n", f)
		}
	}

	if migrateCheck && len(changedFiles) > 0 {
		return fmt.Errorf("%d prompt file(s) need migration (run 'mitto prompts migrate' to fix)", len(changedFiles))
	}
	if len(failedFiles) > 0 {
		return fmt.Errorf("%d prompt file(s) failed to migrate", len(failedFiles))
	}
	return nil
}
