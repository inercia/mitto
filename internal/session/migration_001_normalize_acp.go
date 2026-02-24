package session

import (
	"os"
	"strings"

	"github.com/inercia/mitto/internal/logging"
)

func init() {
	RegisterMigration(Migration{
		Name:        "001_normalize_acp_server_names",
		Description: "Normalize ACP server names to match current configuration",
		Run:         migrateNormalizeACPServerNames,
	})
}

// migrateNormalizeACPServerNames normalizes ACP server names in session metadata
// to match the current configuration. This handles cases where:
// 1. User renamed an ACP server in their config
// 2. Server names changed case (e.g., "auggie" -> "Auggie")
//
// The migration uses the ACPServerNames map from context to determine mappings.
// If no context is provided or no mapping matches, names are left unchanged.
func migrateNormalizeACPServerNames(baseDir string, ctx *MigrationContext) (int, error) {
	log := logging.Session()

	// If no context or no server name mappings, skip this migration
	if ctx == nil || len(ctx.ACPServerNames) == 0 {
		log.Debug("no ACP server name mappings provided, skipping migration")
		return 0, nil
	}

	// Build a case-insensitive lookup map
	// This allows matching "auggie" to "Auggie (Opus 4.5)"
	lowerToCanonical := make(map[string]string)
	for old, new := range ctx.ACPServerNames {
		lowerToCanonical[strings.ToLower(old)] = new
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return 0, err
	}

	modified := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip non-session directories (like migrations.json if it were a dir)
		sessionDir := baseDir + "/" + entry.Name()
		meta, err := readSessionMetadata(sessionDir)
		if err != nil {
			// Skip directories without valid metadata
			continue
		}

		// Check if this session's ACP server needs normalization
		oldName := meta.ACPServer
		if oldName == "" {
			continue
		}

		// Try exact match first, then case-insensitive
		newName, found := ctx.ACPServerNames[oldName]
		if !found {
			newName, found = lowerToCanonical[strings.ToLower(oldName)]
		}

		// If no mapping found or already matches, skip
		if !found || newName == oldName {
			continue
		}

		// Update the metadata
		log.Debug("normalizing ACP server name",
			"session_id", meta.SessionID,
			"old_name", oldName,
			"new_name", newName)

		meta.ACPServer = newName
		if err := writeSessionMetadata(sessionDir, meta); err != nil {
			log.Warn("failed to update session metadata",
				"session_id", meta.SessionID,
				"error", err)
			continue
		}

		modified++
	}

	return modified, nil
}
