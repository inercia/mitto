package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestLoopProcessing_ChangesParametersAndPolicy(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("../../config/prompts/builtin", "beads-issues/loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}

	params := make(map[string]PromptParameter, len(prompt.Parameters))
	for _, param := range prompt.Parameters {
		params[param.Name] = param
	}
	for name, want := range map[string]struct{ typ, group, def string }{
		"SubmitStrategy": {"text", "Changes", "Commit"},
		"FromBranch":     {"text", "Changes", "main"},
		"GroupEpics":     {"boolean", "Changes", "true"},
	} {
		got, ok := params[name]
		if !ok {
			t.Fatalf("missing parameter %s", name)
		}
		if got.Type != want.typ || got.Group != want.group || got.Default != want.def {
			t.Errorf("%s = type %q, group %q, default %q; want %q, %q, %q",
				name, got.Type, got.Group, got.Default, want.typ, want.group, want.def)
		}
	}

	render := func(args map[string]string) string {
		return renderBuiltinPromptWithFragments(t, "Loop processing tasks", &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "orch-1"},
			Args:    args,
			Prompts: cel.PromptsContext{
				Names:        []string{"Loop fixing bug", "Loop implementing feature"},
				EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
			},
		})
	}

	grouped := render(map[string]string{"SubmitStrategy": "Pull Request"})
	for _, want := range []string{
		"created from `main`",
		"never branch from the current HEAD",
		"**one epic PR** only when the last open child finishes",
	} {
		if !strings.Contains(grouped, want) {
			t.Errorf("default grouped policy missing %q", want)
		}
	}

	ungrouped := render(map[string]string{
		"SubmitStrategy": "Pull Request",
		"FromBranch":     "release/2.x",
		"GroupEpics":     "false",
	})
	for _, want := range []string{
		"created from `release/2.x`",
		"Epic grouping is disabled",
		"each epic child, gets a fresh branch",
	} {
		if !strings.Contains(ungrouped, want) {
			t.Errorf("ungrouped policy missing %q", want)
		}
	}

	for _, anchor := range []string{
		`prompt_name: "Mention — driver",`,
		`prompt_name: "Loop fixing bug",`,
		`prompt_name: "Loop implementing feature",`,
	} {
		start := strings.Index(ungrouped, anchor)
		if start < 0 {
			t.Fatalf("spawn anchor %q not found", anchor)
		}
		block := ungrouped[start:]
		if end := strings.Index(block, "\n  )\n"); end >= 0 {
			block = block[:end]
		}
		loopArgs := strings.Index(block, "loop_arguments:")
		if loopArgs < 0 {
			t.Fatalf("spawn %q missing loop_arguments", anchor)
		}
		for _, side := range []string{block[:loopArgs], block[loopArgs:]} {
			for _, want := range []string{`"FromBranch": "release/2.x"`, `"GroupEpics": "false"`} {
				if !strings.Contains(side, want) {
					t.Errorf("spawn %q does not mirror %s", anchor, want)
				}
			}
		}
	}
}

func TestLoopProcessing_PRBranchStartsBeforeTaskWork(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "driver-1", BeadsIssue: "mitto-child", HasBeadsIssue: true},
		Args: map[string]string{
			"SubmitStrategy": "Pull Request",
			"FromBranch":     "release/2.x",
			"GroupEpics":     "true",
		},
	}
	out := renderBuiltinPromptWithFragments(t, "Loop implementing feature", ctx)
	branch := strings.Index(out, "Never create the new branch from bare `HEAD`")
	work := strings.Index(out, "## Step 3 — Branch on the live labels")
	if branch < 0 || work < 0 || branch > work {
		t.Fatalf("branch setup must render before phase work: branch=%d work=%d", branch, work)
	}
	for _, want := range []string{
		`base_branch="release/2.x"`,
		`branch_target="$parent"`,
		`branch_type="epic"`,
		`select(.id != $id and .status != "closed")`,
		"one\nPR for the whole epic rather than one per child",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered PR workflow missing %q", want)
		}
	}
}
