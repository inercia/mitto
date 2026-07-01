package session

import (
	"os"
	"path/filepath"

	"github.com/inercia/mitto/internal/logging"
)

// legacyPeriodicFileName is the pre-rename per-session loop-state file name.
// See mitto-8ir.1: periodicFileName ("periodic.json") was renamed to
// loopFileName ("loop.json"). This migration moves existing on-disk files
// from the old name to the new one so no data is silently dropped.
const legacyPeriodicFileName = "periodic.json"

func init() {
	RegisterMigration(Migration{
		Name:        "002_rename_periodic_to_loop",
		Description: "Rename per-session periodic.json to loop.json (mitto-8ir)",
		Run:         migrateRenamePeriodicToLoop,
	})
}

// migrateRenamePeriodicToLoop renames the per-session periodic.json file to
// loop.json for every session directory under baseDir. The file content is
// unchanged (the LoopPrompt JSON keys contain no "periodic" string) — only
// the filename changes.
//
// The migration is idempotent and safe:
//   - Sessions without a periodic.json are skipped (no-op).
//   - Sessions that already have a loop.json are skipped without touching
//     periodic.json, so an existing loop.json is never clobbered.
//   - Re-running the migration after a successful rename is a no-op, since
//     periodic.json no longer exists.
func migrateRenamePeriodicToLoop(baseDir string, _ *MigrationContext) (int, error) {
	log := logging.Session()

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return 0, err
	}

	modified := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionDir := filepath.Join(baseDir, entry.Name())
		oldPath := filepath.Join(sessionDir, legacyPeriodicFileName)
		newPath := filepath.Join(sessionDir, loopFileName)

		// No legacy file to migrate — nothing to do.
		if _, err := os.Stat(oldPath); err != nil {
			continue
		}

		// Never clobber an existing loop.json.
		if _, err := os.Stat(newPath); err == nil {
			log.Warn("skipping periodic.json rename: loop.json already exists",
				"session_dir", sessionDir)
			continue
		}

		if err := os.Rename(oldPath, newPath); err != nil {
			log.Warn("failed to rename periodic.json to loop.json",
				"session_dir", sessionDir, "error", err)
			continue
		}

		log.Debug("renamed periodic.json to loop.json",
			"session_dir", sessionDir)
		modified++
	}

	return modified, nil
}
