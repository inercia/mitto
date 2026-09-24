package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func TestBuiltinStateDiagnosisPrompts(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })
	reg, fragmentErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(%s): %v", builtinDir, err)
	}
	if len(fragmentErrs) != 0 {
		t.Fatalf("builtin fragment load errors: %+v", fragmentErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(%s): %v", builtinDir, err)
	}

	byName := make(map[string]*PromptFile, len(list))
	for _, prompt := range list {
		byName[prompt.Name] = prompt
	}

	tests := []struct {
		name        string
		enabledWhen string
		state       string
	}{
		{
			name:        "Why is deferred?",
			enabledWhen: `CommandExists("bd") && DirExists(".beads") && ((Item.Id != "" && Item.Status == "deferred") || (Item.Id == "" && Session.HasBeadsIssue && BeadHasStatus(Session.BeadsIssue, "deferred"))) && !(Item.Labels != null && "support-question" in Item.Labels) && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`,
			state:       "deferred",
		},
		{
			name:        "Why needs human?",
			enabledWhen: `CommandExists("bd") && DirExists(".beads") && ((Item.Id != "" && Item.Status != "closed" && "needs-human" in Item.Labels) || (Item.Id == "" && Session.HasBeadsIssue && BeadIsOpen(Session.BeadsIssue) && BeadHasLabels(Session.BeadsIssue, "needs-human"))) && !(Item.Labels != null && "support-question" in Item.Labels) && !(Session.HasBeadsIssue && BeadHasLabels(Session.BeadsIssue, "support-question"))`,
			state:       "needs-human",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := byName[tt.name]
			if prompt == nil {
				t.Fatalf("builtin corpus is missing %q", tt.name)
			}
			if prompt.Group != "Tasks" || prompt.Menus != "beadsIssues, conversation" {
				t.Errorf("group/menus = %q/%q, want Tasks/beadsIssues, conversation", prompt.Group, prompt.Menus)
			}
			if prompt.EnabledWhen != tt.enabledWhen {
				t.Errorf("enabledWhen = %q, want %q", prompt.EnabledWhen, tt.enabledWhen)
			}
			if len(prompt.Parameters) != 1 || prompt.Parameters[0].Name != "IssueID" || prompt.Parameters[0].Type != "beadsId" {
				t.Fatalf("parameters = %+v, want one IssueID beadsId parameter", prompt.Parameters)
			}
			if prompt.Parameters[0].Required == nil || *prompt.Parameters[0].Required {
				t.Error("IssueID must remain explicitly optional because linked conversations already provide the target")
			}
			if !strings.Contains(prompt.Content, `template "beads-issues/shared/state-diagnosis"`) ||
				!strings.Contains(prompt.Content, `"State"     "`+tt.state+`"`) {
				t.Error("prompt does not delegate to state-diagnosis with the expected state")
			}
		})
	}
}

func TestBuiltinStateDiagnosisRowVisibility(t *testing.T) {
	evaluator, err := cel.NewCELEvaluator()
	if err != nil {
		t.Fatalf("NewCELEvaluator: %v", err)
	}
	tests := []struct {
		name string
		expr string
		item cel.ItemContext
		want bool
	}{
		{"deferred row", `Item.Id != "" && Item.Status == "deferred"`, cel.ItemContext{Id: "mitto-1", Status: "deferred"}, true},
		{"open row is not deferred", `Item.Id != "" && Item.Status == "deferred"`, cel.ItemContext{Id: "mitto-1", Status: "open"}, false},
		{"open needs-human row", `Item.Id != "" && Item.Status != "closed" && "needs-human" in Item.Labels`, cel.ItemContext{Id: "mitto-1", Status: "open", Labels: []string{"needs-human"}}, true},
		{"unlabelled row", `Item.Id != "" && Item.Status != "closed" && "needs-human" in Item.Labels`, cel.ItemContext{Id: "mitto-1", Status: "open"}, false},
		{"closed needs-human row", `Item.Id != "" && Item.Status != "closed" && "needs-human" in Item.Labels`, cel.ItemContext{Id: "mitto-1", Status: "closed", Labels: []string{"needs-human"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := evaluator.Compile(tt.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.expr, err)
			}
			got, err := evaluator.Evaluate(compiled, &cel.PromptEnabledContext{Item: tt.item})
			if err != nil {
				t.Fatalf("Evaluate(%q): %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestBuiltinStateDiagnosisRendersForMenuAndLinkedConversation(t *testing.T) {
	contexts := []struct {
		name   string
		ctx    *cel.PromptEnabledContext
		target string
	}{
		{
			name: "beads menu argument",
			ctx: &cel.PromptEnabledContext{
				Session: cel.SessionContext{ID: "session-menu"},
				Args:    map[string]string{"IssueID": "mitto-menu"},
			},
			target: "mitto-menu",
		},
		{
			name: "linked conversation",
			ctx: &cel.PromptEnabledContext{
				Session: cel.SessionContext{ID: "session-linked", HasBeadsIssue: true, BeadsIssue: "mitto-linked"},
			},
			target: "mitto-linked",
		},
	}

	for _, promptName := range []string{"Why is deferred?", "Why needs human?"} {
		for _, tc := range contexts {
			t.Run(promptName+"/"+tc.name, func(t *testing.T) {
				out := renderBuiltinPromptWithFragments(t, promptName, tc.ctx)
				for _, hallmark := range []string{
					"The target bead is `" + tc.target + "`",
					"bd comments " + tc.target,
					"Read comments chronologically",
					"**Recorded reason**",
					"**What is needed now**",
					"one concrete question or action request",
					"mitto_ui_options(self_id:",
					"The diagnosis is read-only until the user answers",
				} {
					if !strings.Contains(out, hallmark) {
						t.Errorf("rendered output missing hallmark %q", hallmark)
					}
				}
			})
		}
	}
}

func TestBuiltinStateDiagnosisWithoutTargetUsesSafePlaceholder(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Why is deferred?", &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "session-no-target"},
	})
	for _, hallmark := range []string{
		"No bead was linked or supplied",
		`mitto_ui_form(self_id: "session-no-target")`,
		"bd comments <bead-id-returned-by-user>",
	} {
		if !strings.Contains(out, hallmark) {
			t.Errorf("rendered no-target output missing hallmark %q", hallmark)
		}
	}
}
