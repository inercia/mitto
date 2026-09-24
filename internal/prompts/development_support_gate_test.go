package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkflowPromptsHiddenForLinkedSupportIssues(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	type gateSpec struct {
		group       string
		enabledWhen string
	}
	want := map[string]gateSpec{
		"child/create-minions.prompt.yaml":       {"Work flow", `!Session.IsChild && Permissions.CanStartConversation && !Session.IsLoopConversation && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"ci/fix-errors.prompt.yaml":              {"Development", `!(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"docs/implement-spec.prompt.yaml":        {"Development", `CommandExists("bd") && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"loop/fixing.prompt.yaml":                {"Development", `Session.HasMessages && CommandExists("bd") && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"loop/implementing.prompt.yaml":          {"Development", `Session.HasMessages && CommandExists("bd") && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"git/commit-and-submit.prompt.yaml":      {"Submission of changes", `!(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"git/create-commits.prompt.yaml":         {"Submission of changes", `!(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"git/rebase-changes.prompt.yaml":         {"Submission of changes", `!(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"git/submit-changes.prompt.yaml":         {"Submission of changes", `!(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"github/address-pr-comments.prompt.yaml": {"Submission of changes", `FileExists(".git/config") && (Tools.HasPattern("github_*") || CommandExists("gh") || CommandExists("glab")) && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
		"github/check-pr-comments.prompt.yaml":   {"Submission of changes", `FileExists(".git/config") && (Tools.HasPattern("github_*") || CommandExists("gh") || CommandExists("glab")) && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`},
	}

	for rel, spec := range want {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join("../../config/prompts/builtin", rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := ParsePromptFile(rel, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile: %v", err)
			}
			if prompt.Group != spec.group {
				t.Fatalf("group = %q, want %q", prompt.Group, spec.group)
			}
			if prompt.EnabledWhen != spec.enabledWhen {
				t.Errorf("enabledWhen = %q, want %q", prompt.EnabledWhen, spec.enabledWhen)
			}
		})
	}
}

func TestTasksBeadsIssuePromptsHiddenForSupportIssues(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	prompts, err := LoadPromptsFromDir("../../config/prompts/builtin")
	if err != nil {
		t.Fatalf("LoadPromptsFromDir: %v", err)
	}

	const itemGuard = `!(Item.Labels != null && "support-question" in Item.Labels)`
	const sessionGuard = `!(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`
	found := 0
	for _, prompt := range prompts {
		menus, _ := ParseMenuTokens(prompt.Menus)
		hasBeadsIssuesMenu := false
		for _, menu := range menus {
			if menu == "beadsIssues" {
				hasBeadsIssuesMenu = true
				break
			}
		}
		if prompt.Group != "Tasks" || !hasBeadsIssuesMenu {
			continue
		}
		found++
		if !strings.Contains(prompt.EnabledWhen, itemGuard) {
			t.Errorf("%s: enabledWhen does not exclude selected support-question rows: %q", prompt.Path, prompt.EnabledWhen)
		}
		if !strings.Contains(prompt.EnabledWhen, sessionGuard) {
			t.Errorf("%s: enabledWhen does not exclude linked support-question conversations: %q", prompt.Path, prompt.EnabledWhen)
		}
	}
	if found == 0 {
		t.Fatal("builtin corpus contains no Tasks prompts in the beadsIssues menu")
	}
}
