package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestCreateSpecPrompt_ParametersAndContextModes(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("../../config/prompts/builtin/docs", "create-spec.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("docs/create-spec.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	if len(prompt.Parameters) != 2 {
		t.Fatalf("parameters = %d, want 2", len(prompt.Parameters))
	}
	dir, filename := prompt.Parameters[0], prompt.Parameters[1]
	if dir.Name != "Dirname" || dir.Type != "dirname" || dir.Remember != RememberFolder {
		t.Errorf("Dirname metadata = %+v", dir)
	}
	if dir.Required == nil || !*dir.Required {
		t.Errorf("Dirname.Required = %v, want explicit true", dir.Required)
	}
	wantGlobs := []string{"docs", "spec", "specs", "**/docs", "**/spec", "**/specs"}
	if strings.Join(dir.Glob, "|") != strings.Join(wantGlobs, "|") {
		t.Errorf("Dirname.Glob = %v, want %v", dir.Glob, wantGlobs)
	}
	if filename.Name != "Filename" || filename.Type != "text" || filename.Group != "Advanced" {
		t.Errorf("Filename metadata = %+v", filename)
	}
	if filename.Required == nil || *filename.Required || filename.Default != "spec-<feature>.md" {
		t.Errorf("Filename required/default = %v/%q", filename.Required, filename.Default)
	}

	render := func(hasMessages bool, dirname, name string) string {
		return renderBuiltinPromptWithFragments(t, "Create spec", &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "spec-session", HasMessages: hasMessages},
			Args:    map[string]string{"Dirname": dirname, "Filename": name},
		})
	}
	withContext := render(true, "docs", "")
	for _, want := range []string{
		"Context mode — synthesize the discussed feature",
		"do **not** restart with the generic",
		"Target directory: `docs`",
		"`docs/spec-<derived-feature>.md`",
	} {
		if !strings.Contains(withContext, want) {
			t.Errorf("context render missing %q", want)
		}
	}
	if strings.Contains(withContext, "Fresh-conversation mode") {
		t.Error("context render included fresh-conversation branch")
	}

	fresh := render(false, "tests/ui/specs", "")
	for _, want := range []string{
		"Fresh-conversation mode — establish the topic first",
		"What do you want to create a spec for?",
		"Do **not** draft or write a specification",
		"Filename setting: `spec-<feature>.md`",
		"`tests/ui/specs/<resolved-filename>`",
	} {
		if !strings.Contains(fresh, want) {
			t.Errorf("fresh render missing %q", want)
		}
	}
	if strings.Contains(fresh, "Context mode — synthesize") {
		t.Error("fresh render included context branch")
	}

	custom := render(false, "docs", "custom-name.md")
	for _, want := range []string{
		"use `custom-name.md` as the advanced filename override",
		"filename only (no `/`, `..`, or absolute path)",
	} {
		if !strings.Contains(custom, want) {
			t.Errorf("custom-filename render missing %q", want)
		}
	}
}
