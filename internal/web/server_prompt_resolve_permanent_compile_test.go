package web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
)

// TestResolvePromptByName_UndefinedFunctionIsPermanent reproduces mitto-5ym:
// a permanent template compile error must not be classified as a fragment race.
func TestResolvePromptByName_UndefinedFunctionIsPermanent(t *testing.T) {
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(mittoDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}
	promptPath := filepath.Join(promptsDir, "feature-phase-plan.prompt.yaml")
	const promptBody = "name: 'Feature — plan phase'\nprompt: |\n  {{ BeadHasLabelsStrictFromNewerBinary \"cgw-translation-5i9.1\" \"planned\" }}\n"
	if err := os.WriteFile(promptPath, []byte(promptBody), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cache := prompts.NewPromptsCache()
	s := &Server{config: Config{PromptsCache: cache, MittoConfig: &configPkg.Config{}}}
	if _, err := cache.GetWebPrompts(); err != nil {
		t.Fatalf("GetWebPrompts(): %v", err)
	}
	loadErrors := cache.LoadErrors()
	if len(loadErrors) == 0 || loadErrors[0].Err == nil {
		t.Fatalf("LoadErrors() = %#v, want the bad prompt's compile error", loadErrors)
	}
	if loadErrors[0].PromptName != "Feature — plan phase" {
		t.Fatalf("LoadErrors()[0].PromptName = %q, want declared name", loadErrors[0].PromptName)
	}
	if !strings.Contains(loadErrors[0].Err.Error(), "not defined") {
		t.Fatalf("LoadErrors()[0].Err = %v, want an undefined-function compile error", loadErrors[0].Err)
	}

	_, resolveErr := s.resolvePromptByName("Feature — plan phase", "/work")
	if resolveErr == nil {
		t.Fatal("resolvePromptByName() succeeded for the failed prompt")
	}
	if errors.Is(resolveErr, conversation.ErrPromptTransientCompileRace) {
		t.Fatalf("resolvePromptByName() error = %v; undefined function is a permanent failure, not a transient fragment race", resolveErr)
	}
}

func TestResolvePromptByName_UnrelatedSharedFragmentFailureIsNotTransient(t *testing.T) {
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(mittoDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}
	const broken = "name: unrelated prompt\nprompt: '{{ template \"_shared/missing\" . }}'\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "unrelated.prompt.yaml"), []byte(broken), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cache := prompts.NewPromptsCache()
	if _, err := cache.GetWebPrompts(); err != nil {
		t.Fatalf("GetWebPrompts(): %v", err)
	}
	s := &Server{config: Config{PromptsCache: cache, MittoConfig: &configPkg.Config{}}}
	_, resolveErr := s.resolvePromptByName("different missing prompt", "/work")
	if resolveErr == nil {
		t.Fatal("resolvePromptByName() unexpectedly succeeded")
	}
	if errors.Is(resolveErr, conversation.ErrPromptTransientCompileRace) {
		t.Fatalf("unrelated prompt load error misclassified requested missing prompt as transient: %v", resolveErr)
	}
}

func TestResolvePromptByName_MatchingMissingSharedFragmentIsTransient(t *testing.T) {
	mittoDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, mittoDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	promptsDir := filepath.Join(mittoDir, appdir.PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}
	const promptName = "Feature — plan phase"
	const source = "name: 'Feature — plan phase'\nprompt: '{{ template \"_shared/missing\" . }}'\n"
	if err := os.WriteFile(filepath.Join(promptsDir, "feature-plan.prompt.yaml"), []byte(source), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	cache := prompts.NewPromptsCache()
	if _, err := cache.GetWebPrompts(); err != nil {
		t.Fatalf("GetWebPrompts(): %v", err)
	}
	s := &Server{config: Config{PromptsCache: cache, MittoConfig: &configPkg.Config{}}}
	_, resolveErr := s.resolvePromptByName(promptName, "/work")
	if !errors.Is(resolveErr, conversation.ErrPromptTransientCompileRace) {
		t.Fatalf("resolvePromptByName() error = %v; want matching missing shared fragment to be transient", resolveErr)
	}
}
