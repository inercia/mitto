package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestImproveDocsPrompt_ContextModesAndSharedChecklist(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("../../config/prompts/builtin/docs", "improve-docs.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("docs/improve-docs.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	if prompt.Name != "Improve docs" || prompt.Group != "Documentation" || prompt.Icon != "edit" {
		t.Errorf("metadata = name %q, group %q, icon %q", prompt.Name, prompt.Group, prompt.Icon)
	}
	if prompt.BackgroundColor != "#E1BEE7" || prompt.Menus != "prompts, !promptsLoop" {
		t.Errorf("metadata = color %q, menus %q", prompt.BackgroundColor, prompt.Menus)
	}
	if prompt.EnabledWhen != "" || len(prompt.Parameters) != 0 {
		t.Errorf("generic prompt should have no enabledWhen or parameters")
	}

	render := func(hasMessages bool) string {
		return renderBuiltinPromptWithFragments(t, "Improve docs", &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "docs-1", HasMessages: hasMessages},
		})
	}

	withContext := render(true)
	for _, want := range []string{
		"substantive prior",
		"without asking for confirmation first",
		"**Accuracy**: Correct stale behavior",
	} {
		if !strings.Contains(withContext, want) {
			t.Errorf("context render missing %q", want)
		}
	}

	withoutContext := render(false)
	for _, want := range []string{
		"There is no prior conversation context",
		"Ask the user which document or documents",
		"make no file changes until they answer",
		"**Accuracy**: Correct stale behavior",
	} {
		if !strings.Contains(withoutContext, want) {
			t.Errorf("no-context render missing %q", want)
		}
	}

	for _, promptName := range []string{"Document", "Document Architecture"} {
		document := renderBuiltinPromptWithFragments(t, promptName, &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "docs-2", HasMessages: true},
		})
		if !strings.Contains(document, "**Accuracy**: Correct stale behavior") {
			t.Errorf("%s did not expand shared documentation-quality fragment", promptName)
		}
	}
}
