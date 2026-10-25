package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureBuiltinPrompts_UnifiesStatusAndPrunesSupersededPrompts(t *testing.T) {
	targetDir := t.TempDir()
	issuesDir := filepath.Join(targetDir, "beads-issues")
	if err := os.MkdirAll(issuesDir, 0755); err != nil {
		t.Fatal(err)
	}
	for file, name := range map[string]string{
		"status.prompt.yaml":   "Show status",
		"assess.prompt.yaml":   "Assess issue readiness",
		"resolved.prompt.yaml": "Check if issue resolved",
	} {
		if err := os.WriteFile(filepath.Join(issuesDir, file), []byte("name: "+name+"\nprompt: Old prompt\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EnsureBuiltinPrompts(targetDir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"assess.prompt.yaml", "resolved.prompt.yaml"} {
		if _, err := os.Stat(filepath.Join(issuesDir, file)); !os.IsNotExist(err) {
			t.Errorf("retired %s must be absent after deployment, got %v", file, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(issuesDir, "status.prompt.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: Check status\n") {
		t.Error("existing status prompt must be updated to Check status")
	}
}
