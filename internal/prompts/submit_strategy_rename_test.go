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

// TestSubmitStrategy_StrategyMatrixAcrossPhases covers the three SubmitStrategy
// values ("Commit", "Pull Request", "None") across every file that actually
// consumes the strategy at render time, closing the gap left by
// TestSubmitStrategy_DefaultOnGateSafety (which only exercises
// feature-phase-implement.prompt.yaml).
//
// The roles are NOT uniform across the 9-file surface (verified by grep over
// the builtin prompt suite):
//   - safe-commit (git/shared/safe-commit) callers: the four phase prompts
//     that make their own commit (feature-phase-implement/test/review,
//     fix-phase-fix) plus mention-driver's inline finalize commit.
//   - ensure-bead-branch callers: only the two FIRST-committing phases
//     (feature-phase-implement, fix-phase-fix) -- Pull-Request-only.
//   - push-and-open-pr callers: only the three driver Done/finalize branches
//     (loop-implementing-feature, loop-fixing-bug, mention-driver) --
//     Pull-Request-only, called exactly once per bead lifecycle.
//
// loop-processing (mirrors only, does no commit work itself) and
// loop-until-complete (has its own bespoke `ne .Args.SubmitStrategy "None"`
// gate, no safe-commit/ensure-bead-branch/push-and-open-pr wiring) are
// intentionally excluded from this matrix -- they are covered by
// TestLoopProcessingSpawns_MirrorArgumentsIntoLoopArguments and existing
// loop-until-complete-specific tests respectively.
//
// For every (file, strategy) pair this asserts, on the rendered output:
//   - the safe-commit hallmark (a `git commit -m "<type>(<scope>): ... (refs
//     <target>)"` line) is present iff strategy != "None" AND the file is a
//     safe-commit caller;
//   - the ensure-bead-branch hallmark is present iff strategy == "Pull Request"
//     AND the file is a branch caller;
//   - the push-and-open-pr hallmark occurs exactly once iff strategy ==
//     "Pull Request" AND the file is a PR caller, and zero times otherwise --
//     pins the bead's "exactly one push-and-open-pr occurrence in the whole
//     rendered driver" acceptance criterion (a duplicated invocation would
//     silently open two PRs for the same bead);
//   - the per-strategy PhaseSuffix/suffix copy matches the strategy, so a
//     mis-set $pr/$commit pair cannot pass silently.
func TestSubmitStrategy_StrategyMatrixAcrossPhases(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"

	const target = "mitto-abc"
	safeCommitHallmark := `(refs ` + target + `)`
	const branchHallmark = "must be **stable across phases**"
	const prHallmark = "existing_url=$(gh pr view"

	type file struct {
		rel           string
		safeCommit    bool
		branch        bool
		pr            bool
		suffixNone    string
		suffixCommit  string
		suffixPR      string
		fallbackToArg bool // whether IssueID must be passed explicitly (no Session.BeadsIssue)
	}
	files := []file{
		{
			rel:          "beads-issues/feature-phase-implement.prompt.yaml",
			safeCommit:   true,
			branch:       true,
			suffixNone:   "commit-after-implement is disabled",
			suffixCommit: "commit-after-implement is enabled",
			suffixPR:     "committed on a per-bead branch",
		},
		{
			rel:          "beads-issues/feature-phase-test.prompt.yaml",
			safeCommit:   true,
			suffixNone:   "commit-after-test is disabled",
			suffixCommit: "commit-after-test is enabled",
			suffixPR:     "committed on the bead's existing feature branch",
		},
		{
			rel:          "beads-issues/feature-phase-review.prompt.yaml",
			safeCommit:   true,
			suffixNone:   "commit-after-review is disabled",
			suffixCommit: "commit-after-review is enabled",
			suffixPR:     "committed on the bead's existing feature branch",
		},
		{
			rel:          "beads-issues/fix-phase-fix.prompt.yaml",
			safeCommit:   true,
			branch:       true,
			suffixNone:   "commit-after-fix is disabled",
			suffixCommit: "commit-after-fix is enabled",
			suffixPR:     "committed on a per-bead branch",
		},
		{
			rel:          "beads-issues/loop-implementing-feature.prompt.yaml",
			pr:           true,
			suffixNone:   "", // driver has no PhaseSuffix; skip suffix assertions
			suffixCommit: "",
			suffixPR:     "",
		},
		{
			rel:          "beads-issues/loop-fixing-bug.prompt.yaml",
			pr:           true,
			suffixNone:   "",
			suffixCommit: "",
			suffixPR:     "",
		},
		{
			rel:          "beads-issues/mention-driver.prompt.yaml",
			safeCommit:   true,
			pr:           true,
			suffixNone:   "Submit strategy is **None**",
			suffixCommit: "Submit strategy is **Commit**",
			suffixPR:     "Submit strategy is **Pull Request**",
		},
	}

	strategies := []struct {
		value       string
		wantSuffix  func(f file) string
		wantCommit  bool // safe-commit hallmark expected (if f.safeCommit)
		wantPR      bool // ensure-bead-branch / push-and-open-pr expected (if f.branch / f.pr)
		otherSuffix []string
	}{
		{
			value:      "None",
			wantSuffix: func(f file) string { return f.suffixNone },
			wantCommit: false,
			wantPR:     false,
		},
		{
			value:      "Commit",
			wantSuffix: func(f file) string { return f.suffixCommit },
			wantCommit: true,
			wantPR:     false,
		},
		{
			value:      "Pull Request",
			wantSuffix: func(f file) string { return f.suffixPR },
			wantCommit: true,
			wantPR:     true,
		},
	}

	for _, f := range files {
		f := f
		t.Run(f.rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(builtinDir, f.rel))
			if err != nil {
				t.Fatalf("read %s: %v", f.rel, err)
			}
			prompt, err := ParsePromptFile(f.rel, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", f.rel, err)
			}

			for _, strat := range strategies {
				strat := strat
				t.Run(strat.value, func(t *testing.T) {
					ctx := &cel.PromptEnabledContext{
						Session: cel.SessionContext{ID: "sess-1", BeadsIssue: target, HasBeadsIssue: true},
						Args:    map[string]string{"IssueID": target, "SubmitStrategy": strat.value},
					}
					out, rerr := RenderPromptTemplate(prompt.Name, prompt.Content, ctx, cel.BuildTemplateFuncMap(ctx))
					if rerr != nil {
						t.Fatalf("RenderPromptTemplate(%s, SubmitStrategy=%s): %v", f.rel, strat.value, rerr)
					}

					if f.safeCommit {
						gotCommit := strings.Contains(out, safeCommitHallmark)
						if gotCommit != strat.wantCommit {
							t.Errorf("SubmitStrategy=%s: safe-commit hallmark present=%v, want %v; output:\n%s",
								strat.value, gotCommit, strat.wantCommit, out)
						}
					}
					if f.branch {
						gotBranch := strings.Contains(out, branchHallmark)
						wantBranch := strat.wantPR
						if gotBranch != wantBranch {
							t.Errorf("SubmitStrategy=%s: ensure-bead-branch hallmark present=%v, want %v; output:\n%s",
								strat.value, gotBranch, wantBranch, out)
						}
					}
					if f.pr {
						// Exactly-once invariant (bead acceptance criteria): "Pull
						// Request" must call push-and-open-pr exactly ONCE in the
						// whole rendered driver -- a duplicated invocation would
						// silently open two PRs for the same bead. "None"/"Commit"
						// must call it zero times. strings.Count (not Contains)
						// so a regression that renders the fragment twice fails.
						gotPRCount := strings.Count(out, prHallmark)
						wantPRCount := 0
						if strat.wantPR {
							wantPRCount = 1
						}
						if gotPRCount != wantPRCount {
							t.Errorf("SubmitStrategy=%s: push-and-open-pr hallmark occurrences=%d, want %d (exactly-once invariant); output:\n%s",
								strat.value, gotPRCount, wantPRCount, out)
						}
					}

					wantSuffix := strat.wantSuffix(f)
					if wantSuffix != "" && !strings.Contains(out, wantSuffix) {
						t.Errorf("SubmitStrategy=%s: expected suffix copy %q not found; output:\n%s",
							strat.value, wantSuffix, out)
					}
				})
			}
		})
	}
}
