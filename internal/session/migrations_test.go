package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationState(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "migration_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	statePath := filepath.Join(tmpDir, migrationsFileName)

	// Load state from non-existent file
	state, err := loadMigrationState(statePath)
	if err != nil {
		t.Fatalf("loadMigrationState failed: %v", err)
	}
	if len(state.Applied) != 0 {
		t.Errorf("expected empty Applied map, got %d entries", len(state.Applied))
	}

	// Save state with some applied migrations
	state.Applied["test_migration_1"] = time.Now()
	state.Applied["test_migration_2"] = time.Now().Add(-time.Hour)
	if err := saveMigrationState(statePath, state); err != nil {
		t.Fatalf("saveMigrationState failed: %v", err)
	}

	// Reload and verify
	reloaded, err := loadMigrationState(statePath)
	if err != nil {
		t.Fatalf("loadMigrationState (reload) failed: %v", err)
	}
	if len(reloaded.Applied) != 2 {
		t.Errorf("expected 2 applied migrations, got %d", len(reloaded.Applied))
	}
	if _, ok := reloaded.Applied["test_migration_1"]; !ok {
		t.Error("test_migration_1 not found in reloaded state")
	}
	if _, ok := reloaded.Applied["test_migration_2"]; !ok {
		t.Error("test_migration_2 not found in reloaded state")
	}
}

func TestRunMigrations(t *testing.T) {
	// Save original registry and restore after test
	originalRegistry := migrationRegistry
	defer func() { migrationRegistry = originalRegistry }()

	// Clear registry for test
	migrationRegistry = nil

	// Create a temporary directory with session structure
	tmpDir, err := os.MkdirTemp("", "migration_run_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Track which migrations ran
	var ran []string

	// Register test migrations
	RegisterMigration(Migration{
		Name:        "001_first",
		Description: "First test migration",
		Run: func(baseDir string, ctx *MigrationContext) (int, error) {
			ran = append(ran, "001_first")
			return 1, nil
		},
	})
	RegisterMigration(Migration{
		Name:        "002_second",
		Description: "Second test migration",
		Run: func(baseDir string, ctx *MigrationContext) (int, error) {
			ran = append(ran, "002_second")
			return 2, nil
		},
	})

	// Run migrations
	if err := RunMigrations(tmpDir, nil); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}

	// Verify both ran in order
	if len(ran) != 2 {
		t.Fatalf("expected 2 migrations to run, got %d", len(ran))
	}
	if ran[0] != "001_first" || ran[1] != "002_second" {
		t.Errorf("migrations ran out of order: %v", ran)
	}

	// Run again - should be idempotent
	ran = nil
	if err := RunMigrations(tmpDir, nil); err != nil {
		t.Fatalf("RunMigrations (second run) failed: %v", err)
	}
	if len(ran) != 0 {
		t.Errorf("expected no migrations to run on second call, got %d", len(ran))
	}
}

func TestGetPendingMigrations(t *testing.T) {
	// Save original registry and restore after test
	originalRegistry := migrationRegistry
	defer func() { migrationRegistry = originalRegistry }()

	// Clear registry for test
	migrationRegistry = nil

	tmpDir, err := os.MkdirTemp("", "migration_pending_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Register some migrations
	RegisterMigration(Migration{Name: "001_test", Description: "Test 1"})
	RegisterMigration(Migration{Name: "002_test", Description: "Test 2"})

	// All should be pending initially
	pending, err := GetPendingMigrations(tmpDir)
	if err != nil {
		t.Fatalf("GetPendingMigrations failed: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(pending))
	}

	// Mark one as applied
	statePath := filepath.Join(tmpDir, migrationsFileName)
	state := &MigrationState{Applied: map[string]time.Time{"001_test": time.Now()}}
	if err := saveMigrationState(statePath, state); err != nil {
		t.Fatalf("saveMigrationState failed: %v", err)
	}

	// Now only one should be pending
	pending, err = GetPendingMigrations(tmpDir)
	if err != nil {
		t.Fatalf("GetPendingMigrations (after apply) failed: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].Name != "002_test" {
		t.Errorf("expected 002_test to be pending, got %s", pending[0].Name)
	}
}

