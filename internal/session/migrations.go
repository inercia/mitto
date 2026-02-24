package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/logging"
)

const migrationsFileName = "migrations.json"

// MigrationContext provides context for migrations that need external information.
type MigrationContext struct {
	// ACPServerNames maps old/alias names to current canonical names.
	// Used by migrations that need to normalize server names.
	// Example: {"auggie": "Auggie (Opus 4.5)", "old-name": "new-name"}
	ACPServerNames map[string]string
}

// NewMigrationContext creates a MigrationContext from a list of canonical server names.
// It builds a mapping for server name normalization that handles:
//  1. Exact matches (server name already correct)
//  2. Case changes (e.g., "auggie" -> "Auggie")
//  3. Server renames where the old name is a case-insensitive prefix of the new name
//     (e.g., "auggie" -> "Auggie (Opus 4.5)")
func NewMigrationContext(serverNames []string) *MigrationContext {
	if len(serverNames) == 0 {
		return nil
	}

	// Build a map of known server names for normalization.
	nameMap := make(map[string]string)
	for _, name := range serverNames {
		// Map the canonical name to itself
		nameMap[name] = name

		// Also map lowercase version for case-insensitive matching
		lower := strings.ToLower(name)
		if lower != name {
			nameMap[lower] = name
		}

		// Extract base name (part before any parentheses) for prefix matching
		// This handles renames like "auggie" -> "Auggie (Opus 4.5)"
		baseName := name
		if idx := strings.Index(name, " ("); idx > 0 {
			baseName = strings.TrimSpace(name[:idx])
		}
		if baseName != name {
			nameMap[baseName] = name
			nameMap[strings.ToLower(baseName)] = name
		}
	}

	return &MigrationContext{
		ACPServerNames: nameMap,
	}
}

// Migration represents a single data migration.
type Migration struct {
	// Name is a unique identifier for the migration (e.g., "001_normalize_acp_server_names")
	Name string
	// Description explains what this migration does
	Description string
	// Run executes the migration on all sessions. Returns the number of sessions modified.
	// The function receives the base directory containing all session folders.
	Run func(baseDir string, ctx *MigrationContext) (int, error)
}

// MigrationState tracks which migrations have been applied.
type MigrationState struct {
	// Applied maps migration names to their completion timestamps
	Applied map[string]time.Time `json:"applied"`
}

// migrationRegistry holds all registered migrations in order.
var migrationRegistry []Migration

// RegisterMigration adds a migration to the registry.
// Migrations are run in the order they are registered.
func RegisterMigration(m Migration) {
	migrationRegistry = append(migrationRegistry, m)
}

// RunMigrations runs all pending migrations on the session store.
// It tracks which migrations have been applied to avoid re-running them.
// The context parameter is optional and provides external information to migrations.
func RunMigrations(baseDir string, ctx *MigrationContext) error {
	log := logging.Session()

	// Load migration state
	statePath := filepath.Join(baseDir, migrationsFileName)
	state, err := loadMigrationState(statePath)
	if err != nil {
		return fmt.Errorf("failed to load migration state: %w", err)
	}

	// Run pending migrations
	for _, migration := range migrationRegistry {
		if _, applied := state.Applied[migration.Name]; applied {
			continue // Already applied
		}

		log.Info("running migration",
			"name", migration.Name,
			"description", migration.Description)

		modified, err := migration.Run(baseDir, ctx)
		if err != nil {
			return fmt.Errorf("migration %s failed: %w", migration.Name, err)
		}

		// Mark as applied
		state.Applied[migration.Name] = time.Now()
		if err := saveMigrationState(statePath, state); err != nil {
			return fmt.Errorf("failed to save migration state: %w", err)
		}

		log.Info("migration completed",
			"name", migration.Name,
			"sessions_modified", modified)
	}

	return nil
}

// loadMigrationState loads the migration state from disk.
func loadMigrationState(path string) (*MigrationState, error) {
	state := &MigrationState{
		Applied: make(map[string]time.Time),
	}

	if err := fileutil.ReadJSON(path, state); err != nil {
		if os.IsNotExist(err) {
			return state, nil // New state, no migrations applied yet
		}
		return nil, err
	}

	// Ensure map is initialized (in case JSON had null)
	if state.Applied == nil {
		state.Applied = make(map[string]time.Time)
	}

	return state, nil
}

// saveMigrationState saves the migration state to disk.
func saveMigrationState(path string, state *MigrationState) error {
	return fileutil.WriteJSONAtomic(path, state, 0644)
}

// GetPendingMigrations returns the list of migrations that haven't been applied yet.
func GetPendingMigrations(baseDir string) ([]Migration, error) {
	statePath := filepath.Join(baseDir, migrationsFileName)
	state, err := loadMigrationState(statePath)
	if err != nil {
		return nil, err
	}

	var pending []Migration
	for _, m := range migrationRegistry {
		if _, applied := state.Applied[m.Name]; !applied {
			pending = append(pending, m)
		}
	}
	return pending, nil
}

// GetAppliedMigrations returns the list of migrations that have been applied.
func GetAppliedMigrations(baseDir string) ([]string, error) {
	statePath := filepath.Join(baseDir, migrationsFileName)
	state, err := loadMigrationState(statePath)
	if err != nil {
		return nil, err
	}

	var applied []string
	for name := range state.Applied {
		applied = append(applied, name)
	}
	sort.Strings(applied)
	return applied, nil
}

// readSessionMetadata reads metadata from a session directory.
func readSessionMetadata(sessionDir string) (*Metadata, error) {
	metaPath := filepath.Join(sessionDir, metadataFileName)
	var meta Metadata
	if err := fileutil.ReadJSON(metaPath, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// writeSessionMetadata writes metadata to a session directory.
func writeSessionMetadata(sessionDir string, meta *Metadata) error {
	metaPath := filepath.Join(sessionDir, metadataFileName)
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0644)
}
