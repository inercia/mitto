package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/prompts"
)

func TestDeployBuiltinPromptsPublishesTransactionBoundary(t *testing.T) {
	promptsDir := t.TempDir()
	builtinDir := filepath.Join(promptsDir, "builtin")

	result, err := DeployBuiltinPrompts(builtinDir, true)
	if err != nil {
		t.Fatalf("DeployBuiltinPrompts: %v", err)
	}
	if len(result.Deployed) == 0 {
		t.Fatal("expected embedded prompts to be deployed")
	}
	if _, err := os.Stat(filepath.Join(promptsDir, prompts.DeploymentMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("deployment marker remains after successful deploy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(promptsDir, prompts.DeploymentGenerationName)); err != nil {
		t.Fatalf("deployment generation was not published: %v", err)
	}
}
