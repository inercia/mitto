package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/prompts"
)

// Regression for mitto-7ul8.2: MCP prompt discovery must use the workspace's
// scoped fragment registry even when the files appear after process startup.
func TestLoadMergedPrompts_LateWorkspaceFragment(t *testing.T) {
	previous := prompts.CurrentFragments()
	prompts.SetCurrentFragments(prompts.NewFragmentRegistry())
	t.Cleanup(func() { prompts.SetCurrentFragments(previous) })

	workspace := t.TempDir()
	server := &Server{}
	if got := server.loadMergedPrompts(workspace); len(got) != 0 {
		t.Fatalf("initial prompt count = %d, want 0", len(got))
	}

	promptsDir := appdir.WorkspacePromptsDir(workspace)
	if err := os.MkdirAll(filepath.Join(promptsDir, "shared"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "shared", "foo.tmpl"), []byte("workspace fragment"), 0o644); err != nil {
		t.Fatalf("WriteFile fragment: %v", err)
	}
	promptBody := "name: Workspace consumer\nprompt: |\n  {{ template \"shared/foo\" . }}\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "consumer.prompt.yaml"), []byte(promptBody), 0o644); err != nil {
		t.Fatalf("WriteFile prompt: %v", err)
	}

	got := server.loadMergedPrompts(workspace)
	if len(got) != 1 || got[0].Name != "Workspace consumer" {
		t.Fatalf("workspace prompts = %+v, want Workspace consumer", got)
	}
}
