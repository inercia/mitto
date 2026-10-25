package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureBuiltinPrompts_RemovesStandaloneImplement(t *testing.T) {
	targetDir := t.TempDir()
	removed := filepath.Join(targetDir, "beads-issues", "implement.prompt.yaml")
	if err := os.MkdirAll(filepath.Dir(removed), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(removed, []byte("name: Implement\nprompt: Implement the bead\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(removed); !os.IsNotExist(err) {
		t.Fatalf("retired Implement prompt must be absent after deployment, got %v", err)
	}
	for _, name := range []string{"fix-phase-fix.prompt.yaml", "feature-phase-implement.prompt.yaml"} {
		if _, err := os.Stat(filepath.Join(targetDir, "beads-issues", name)); err != nil {
			t.Errorf("implementation phase %s must remain available: %v", name, err)
		}
	}
}
