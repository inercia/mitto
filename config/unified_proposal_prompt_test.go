package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureBuiltinPrompts_UnifiesProposalPrompts(t *testing.T) {
	target := t.TempDir()
	retired := filepath.Join(target, "docs", "propose-a-plan.prompt.yaml")
	if err := os.MkdirAll(filepath.Dir(retired), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retired, []byte("name: Propose a plan\nprompt: Old plan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureBuiltinPrompts(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Fatalf("retired plan prompt must be pruned, got %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "misc", "propose-something.prompt.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: Propose something\n", "menus: conversation\n", "propose a concise plan", "mitto_ui_options"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("deployed proposal missing %q", want)
		}
	}
}
