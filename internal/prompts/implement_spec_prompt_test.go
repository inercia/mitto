package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestImplementSpecPrompt_DecomposesDocumentsIntoBeads(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("../../config/prompts/builtin/docs", "implement-spec.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("docs/implement-spec.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	if prompt.Name != "Implement spec" || prompt.EnabledWhen != `CommandExists("bd") && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))` {
		t.Errorf("metadata = name %q, enabledWhen %q", prompt.Name, prompt.EnabledWhen)
	}
	if len(prompt.Parameters) != 1 {
		t.Fatalf("parameters = %d, want 1", len(prompt.Parameters))
	}
	param := prompt.Parameters[0]
	if param.Name != "SpecFile" || param.Type != "filename" || param.Remember != RememberFolder {
		t.Errorf("SpecFile metadata = %+v", param)
	}
	if param.Required == nil || *param.Required {
		t.Errorf("SpecFile.Required = %v, want explicit false", param.Required)
	}
	wantGlobs := []string{
		"**/*.md", "**/*.markdown", "**/*.txt", "**/*.rst", "**/*.adoc",
		"**/*.asciidoc", "**/*.pdf", "**/*.doc", "**/*.docx", "**/*.odt",
		"**/*.rtf", "**/*.html", "**/*.htm", "**/*.tex", "**/*.yaml",
		"**/*.yml", "**/*.json", "**/*.feature",
	}
	if strings.Join(param.Glob, "|") != strings.Join(wantGlobs, "|") {
		t.Errorf("SpecFile.Glob = %v, want document formats %v", param.Glob, wantGlobs)
	}
	if strings.Contains(strings.Join(param.Glob, "|"), "*.go") {
		t.Errorf("SpecFile.Glob unexpectedly accepts Go source: %v", param.Glob)
	}

	render := func(specFile string) string {
		return renderBuiltinPromptWithFragments(t, "Implement spec", &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "spec-session"},
			Args:    map[string]string{"SpecFile": specFile},
		})
	}
	withFile := render("specs/feature.pdf")
	for _, want := range []string{
		"selected specification is `specs/feature.pdf`",
		"Reject source-code paths such as `.go`",
		"**Acceptance criteria**: concrete, testable",
		"bd dep add <blocked-id> <blocker-id>",
		"Retrospective coverage and coherence pass",
		"coverage matrix",
	} {
		if !strings.Contains(withFile, want) {
			t.Errorf("render with file missing %q", want)
		}
	}
	withoutFile := render("")
	for _, want := range []string{
		"No specification path was supplied",
		`mitto_ui_form(self_id: "spec-session")`,
		"required text field named `spec_file`",
	} {
		if !strings.Contains(withoutFile, want) {
			t.Errorf("render without file missing %q", want)
		}
	}

	decompose := renderBuiltinPromptWithFragments(t, "Decompose issue", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "decompose-session"},
		Args:    map[string]string{"IssueID": "mitto-example"},
	})
	if !strings.Contains(decompose, "**Acceptance criteria**: concrete, testable") {
		t.Error("Decompose issue did not expand shared work-item quality fragment")
	}
}
