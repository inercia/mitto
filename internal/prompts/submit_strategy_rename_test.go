package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

// submitStrategyFiles is the 9-file propagation surface of mitto-cwz.3 (hard
// rename of .Args.Commit -> .Args.SubmitStrategy). loop-processing is the L1
// orchestrator (gets the picker); the other 8 are dispatch targets (plain text).
var submitStrategyFiles = []string{
	"beads-issues/loop-processing.prompt.yaml",
	"beads-issues/loop-implementing-feature.prompt.yaml",
	"beads-issues/loop-fixing-bug.prompt.yaml",
	"beads-issues/loop-until-complete.prompt.yaml",
	"beads-issues/mention-driver.prompt.yaml",
	"beads-issues/feature-phase-implement.prompt.yaml",
	"beads-issues/feature-phase-test.prompt.yaml",
	"beads-issues/feature-phase-review.prompt.yaml",
	"beads-issues/fix-phase-fix.prompt.yaml",
}

// TestSubmitStrategy_ParameterDeclarations pins the mitto-cwz.3 parameter-shape
// acceptance criteria across all 9 in-scope files:
//   - the old boolean/text "Commit" parameter is gone (hard rename, no back-compat)
//   - every file declares "SubmitStrategy" as type "text"
//   - only the L1 orchestrator (loop-processing) declares it as a picker
//     (options + default) — dispatch targets must NOT, or mitto-cwz.1 would
//     always-collect a dialog on an internal-menu prompt
func TestSubmitStrategy_ParameterDeclarations(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	for _, rel := range submitStrategyFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(builtinDir, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			prompt, err := ParsePromptFile(rel, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", rel, err)
			}

			var submit, commit *PromptParameter
			for i := range prompt.Parameters {
				switch prompt.Parameters[i].Name {
				case "SubmitStrategy":
					submit = &prompt.Parameters[i]
				case "Commit":
					commit = &prompt.Parameters[i]
				}
			}
			if commit != nil {
				t.Errorf("%s: found leftover 'Commit' parameter — hard rename requires it be gone entirely", rel)
			}
			if submit == nil {
				t.Fatalf("%s: missing 'SubmitStrategy' parameter", rel)
			}
			if submit.Type != "text" {
				t.Errorf("%s: SubmitStrategy.Type = %q, want \"text\"", rel, submit.Type)
			}

			isPicker := rel == "beads-issues/loop-processing.prompt.yaml"
			if isPicker {
				want := []string{"Commit", "Pull Request", "None"}
				if strings.Join(submit.Options, ",") != strings.Join(want, ",") {
					t.Errorf("%s: SubmitStrategy.Options = %v, want %v", rel, submit.Options, want)
				}
				if submit.Default != "Commit" {
					t.Errorf("%s: SubmitStrategy.Default = %q, want \"Commit\"", rel, submit.Default)
				}
			} else {
				if len(submit.Options) != 0 {
					t.Errorf("%s: dispatch-target prompt must NOT declare Options (mitto-cwz.1 always-dialogs text+options); got %v", rel, submit.Options)
				}
				if submit.Default != "" {
					t.Errorf("%s: dispatch-target prompt must NOT declare a Default; got %q", rel, submit.Default)
				}
			}
		})
	}
}

// TestSubmitStrategy_DefaultOnGateSafety pins the core safety invariant from
// the mitto-cwz.3 plan: the commit gate is default-on (`ne $submit "None"`),
// never positive-match. An in-flight child re-firing across the rename with
// Args.SubmitStrategy unset (missingkey=zero => "") must still commit — never
// silently degrade to no-commit (the mitto-rtdr failure mode). Exercised
// against feature-phase-implement.prompt.yaml, the phase that also wires the
// Pull-Request-only ensure-bead-branch fragment.
func TestSubmitStrategy_DefaultOnGateSafety(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	const rel = "beads-issues/feature-phase-implement.prompt.yaml"
	data, err := os.ReadFile(filepath.Join(builtinDir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	prompt, err := ParsePromptFile(rel, data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile(%s): %v", rel, err)
	}

	render := func(args map[string]string) string {
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "sess-1", BeadsIssue: "mitto-abc", HasBeadsIssue: true},
			Args:    args,
		}
		out, rerr := RenderPromptTemplate(prompt.Name, prompt.Content, ctx, cel.BuildTemplateFuncMap(ctx))
		if rerr != nil {
			t.Fatalf("RenderPromptTemplate(args=%v): %v", args, rerr)
		}
		return out
	}

	const commitHallmark = "commit-after-implement is enabled"
	const noCommitHallmark = "Do **not** commit"
	const branchHallmark = "Ensure the branch exists and is checked out"

	t.Run("missing SubmitStrategy degrades to commit", func(t *testing.T) {
		out := render(map[string]string{"IssueID": "mitto-abc"})
		if !strings.Contains(out, commitHallmark) {
			t.Errorf("missing SubmitStrategy must default-on to Commit; hallmark %q not found in:\n%s", commitHallmark, out)
		}
		if strings.Contains(out, noCommitHallmark) {
			t.Errorf("missing SubmitStrategy must NOT render the no-commit branch; found %q", noCommitHallmark)
		}
		if strings.Contains(out, branchHallmark) {
			t.Errorf("missing SubmitStrategy must NOT invoke ensure-bead-branch (Pull-Request-only); found %q", branchHallmark)
		}
	})

	t.Run(`SubmitStrategy "None" disables commit`, func(t *testing.T) {
		out := render(map[string]string{"IssueID": "mitto-abc", "SubmitStrategy": "None"})
		if !strings.Contains(out, noCommitHallmark) {
			t.Errorf("SubmitStrategy=None must render the no-commit branch; hallmark %q not found in:\n%s", noCommitHallmark, out)
		}
		if strings.Contains(out, commitHallmark) {
			t.Errorf("SubmitStrategy=None must not render the commit-enabled hallmark")
		}
	})

	t.Run(`SubmitStrategy "Pull Request" commits AND branches`, func(t *testing.T) {
		out := render(map[string]string{"IssueID": "mitto-abc", "SubmitStrategy": "Pull Request"})
		if !strings.Contains(out, branchHallmark) {
			t.Errorf("SubmitStrategy=Pull Request must invoke ensure-bead-branch; hallmark %q not found in:\n%s", branchHallmark, out)
		}
		if strings.Contains(out, noCommitHallmark) {
			t.Errorf("SubmitStrategy=Pull Request must still commit (not render the no-commit branch)")
		}
	})
}

// TestSubmitStrategy_FragmentWiringCallSites pins the exact fan-out of the two
// mitto-cwz.2 git fragments across the 9-file surface, per the plan: exactly
// the two first-committing phases invoke ensure-bead-branch, and exactly the
// three driver Done/finalize branches invoke push-and-open-pr — never zero
// (PR strategy silently does nothing) and never duplicated (PR opened twice).
func TestSubmitStrategy_FragmentWiringCallSites(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"

	wantBranch := map[string]bool{
		"beads-issues/feature-phase-implement.prompt.yaml": true,
		"beads-issues/fix-phase-fix.prompt.yaml":           true,
	}
	wantPR := map[string]bool{
		"beads-issues/loop-implementing-feature.prompt.yaml": true,
		"beads-issues/loop-fixing-bug.prompt.yaml":           true,
		"beads-issues/mention-driver.prompt.yaml":            true,
	}

	for _, rel := range submitStrategyFiles {
		data, err := os.ReadFile(filepath.Join(builtinDir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		content := string(data)
		hasBranch := strings.Contains(content, `template "beads-issues/shared/ensure-bead-branch"`)
		if hasBranch != wantBranch[rel] {
			t.Errorf("%s: ensure-bead-branch invocation present=%v, want %v", rel, hasBranch, wantBranch[rel])
		}
		hasPR := strings.Contains(content, `template "beads-issues/shared/push-and-open-pr"`)
		if hasPR != wantPR[rel] {
			t.Errorf("%s: push-and-open-pr invocation present=%v, want %v", rel, hasPR, wantPR[rel])
		}
	}
}
