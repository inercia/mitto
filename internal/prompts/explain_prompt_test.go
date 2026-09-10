package prompts

import (
	"strings"
	"testing"
)

func TestBuiltinBeadsExplainPrompt(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"

	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, fragErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(%s): %v", builtinDir, err)
	}
	if len(fragErrs) != 0 {
		t.Fatalf("builtin fragment load errors: %+v", fragErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(%s): %v", builtinDir, err)
	}

	var explain *PromptFile
	for _, prompt := range list {
		if prompt.Name == "Explain" {
			if explain != nil {
				t.Fatal("builtin corpus contains duplicate prompts named Explain")
			}
			explain = prompt
		}
	}
	if explain == nil {
		t.Fatal("builtin corpus is missing the beads Explain prompt")
	}

	if explain.Menus != "beadsIssues" {
		t.Fatalf("Explain menus = %q, want beadsIssues only", explain.Menus)
	}
	if explain.Group != "Tasks" {
		t.Fatalf("Explain group = %q, want Tasks", explain.Group)
	}
	if explain.EnabledWhen != `CommandExists("bd") && DirExists(".beads")` {
		t.Fatalf("Explain enabledWhen = %q, want beads availability gate", explain.EnabledWhen)
	}
	if len(explain.Parameters) != 1 || explain.Parameters[0].Name != "IssueID" || explain.Parameters[0].Type != "beadsId" {
		t.Fatalf("Explain parameters = %+v, want one IssueID beadsId parameter", explain.Parameters)
	}

	for _, hallmark := range []string{
		"#### Purpose",
		"#### Deferred rationale",
		"#### Progress & evidence",
		"#### Related tickets",
		"bd comments {{ .Args.IssueID }}",
		"bd dep list {{ .Args.IssueID }}",
		"git log --oneline --all",
		"read-only",
	} {
		if !strings.Contains(explain.Content, hallmark) {
			t.Errorf("Explain prompt missing hallmark %q", hallmark)
		}
	}

	var codeExplain *PromptFile
	for _, prompt := range list {
		if prompt.Name == "Explain code" {
			codeExplain = prompt
			break
		}
	}
	if codeExplain == nil {
		t.Fatal("existing code explanation prompt was not renamed to Explain code")
	}
}
