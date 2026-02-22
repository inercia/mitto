package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrateNormalizeACPServerNames(t *testing.T) {
	// Create a temporary sessions directory
	tmpDir, err := os.MkdirTemp("", "migration_acp_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some test sessions with different ACP server names
	sessions := []struct {
		id         string
		acpServer  string
		wantServer string // expected after migration
	}{
		{"session-1", "auggie", "Auggie (Opus 4.5)"},                 // lowercase -> new name
		{"session-2", "Auggie", "Auggie (Opus 4.5)"},                 // different case -> new name
		{"session-3", "Auggie (Opus 4.5)", "Auggie (Opus 4.5)"},      // already correct
		{"session-4", "claude-code", "claude-code"},                  // different server, unchanged
		{"session-5", "unknown-server", "unknown-server"},            // unknown, unchanged
	}

	for _, s := range sessions {
		sessionDir := filepath.Join(tmpDir, s.id)
		if err := os.MkdirAll(sessionDir, 0755); err != nil {
			t.Fatalf("failed to create session dir: %v", err)
		}

		meta := &Metadata{
			SessionID:  s.id,
			ACPServer:  s.acpServer,
			WorkingDir: "/test",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := writeSessionMetadata(sessionDir, meta); err != nil {
			t.Fatalf("failed to write metadata for %s: %v", s.id, err)
		}
	}

	// Create migration context with name mappings
	ctx := &MigrationContext{
		ACPServerNames: map[string]string{
			// Map old names to new canonical name
			"auggie":            "Auggie (Opus 4.5)",
			"Auggie":            "Auggie (Opus 4.5)",
			"Auggie (Opus 4.5)": "Auggie (Opus 4.5)",
			"claude-code":       "claude-code",
		},
	}

	// Run the migration
	modified, err := migrateNormalizeACPServerNames(tmpDir, ctx)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Should have modified session-1 and session-2
	if modified != 2 {
		t.Errorf("expected 2 sessions modified, got %d", modified)
	}

	// Verify each session has the expected ACP server name
	for _, s := range sessions {
		sessionDir := filepath.Join(tmpDir, s.id)
		meta, err := readSessionMetadata(sessionDir)
		if err != nil {
			t.Fatalf("failed to read metadata for %s: %v", s.id, err)
		}

		if meta.ACPServer != s.wantServer {
			t.Errorf("session %s: expected acp_server=%q, got %q",
				s.id, s.wantServer, meta.ACPServer)
		}
	}
}

func TestMigrateNormalizeACPServerNames_NoContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration_acp_nocontext")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// With nil context, should return 0 and not error
	modified, err := migrateNormalizeACPServerNames(tmpDir, nil)
	if err != nil {
		t.Fatalf("migration with nil context failed: %v", err)
	}
	if modified != 0 {
		t.Errorf("expected 0 modified with nil context, got %d", modified)
	}

	// With empty context, should return 0 and not error
	modified, err = migrateNormalizeACPServerNames(tmpDir, &MigrationContext{})
	if err != nil {
		t.Fatalf("migration with empty context failed: %v", err)
	}
	if modified != 0 {
		t.Errorf("expected 0 modified with empty context, got %d", modified)
	}
}

func TestMigrateNormalizeACPServerNames_CaseInsensitive(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migration_acp_case")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create session with uppercase name
	sessionDir := filepath.Join(tmpDir, "session-1")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	meta := &Metadata{
		SessionID: "session-1",
		ACPServer: "AUGGIE", // all uppercase
	}
	if err := writeSessionMetadata(sessionDir, meta); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}

	// Context only has lowercase mapping
	ctx := &MigrationContext{
		ACPServerNames: map[string]string{
			"auggie": "Auggie (New Name)",
		},
	}

	modified, err := migrateNormalizeACPServerNames(tmpDir, ctx)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if modified != 1 {
		t.Errorf("expected 1 modified, got %d", modified)
	}

	// Verify the name was updated
	meta, _ = readSessionMetadata(sessionDir)
	if meta.ACPServer != "Auggie (New Name)" {
		t.Errorf("expected ACPServer='Auggie (New Name)', got %q", meta.ACPServer)
	}
}

