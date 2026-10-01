package prompts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This file pins the outcome of an investigation into whether the loop-intended
// builtin prompts under config/prompts/builtin/jira/ (and the triage-bugs prompt,
// which actually lives at config/prompts/builtin/beads/triage-bugs.prompt.yaml —
// NOT beads-issues/) could be split into a Cheap outer loop (polling/classification/
// dispatch) that spawns a child on model_tag "Coding" for the heavy work, mirroring
// the precedent at config/prompts/builtin/github/babysit-*.prompt.yaml +
// config/prompts/builtin/github/shared/spawn-rules.tmpl.
//
// Conclusion: NONE of the jira loop prompts qualify for that split today.
//
//   - jira/work.prompt.yaml: interactive (multiple mitto_ui_options checkpoints),
//     and its existing per-item delegation already picks a target `acp_server`
//     directly (preferring a faster/cheaper agent vs. a slower/more-capable one)
//     rather than spawning via model_tag. It has no preferredModels field at all.
//   - jira/status-all-inprogress.prompt.yaml and jira/status-one-inprogress.prompt.yaml:
//     single-pass, read-only fact-check/report prompts ("No code changes, no JIRA
//     updates, no further work."). Spawning a child per ticket would add overhead
//     without saving meaningful work, and a single coherent pass keeps judgment
//     consistent across tickets. Both already declare preferredModels: [Coding].
//   - jira/sync-tasks.prompt.yaml: a dense, stateful two-way JIRA<->beads
//     reconciliation pipeline. Its stale-ticket reconciliation step depends on the
//     full live ticket set having been processed earlier in the SAME run, and
//     concurrent bd writes from spawned children would risk database races.
//     Already declares preferredModels: [Coding].
//   - jira/decompose.prompt.yaml and the non-loop jira prompts (new-ticket,
//     pull-issue, pull-to-new, push-issue, push-new): menu-dispatched, not
//     loop-intended (no loop: block), already preferredModels: [Coding].
//   - beads/triage-bugs.prompt.yaml: already converted ahead of this task —
//     preferredModels: [Cheap, Coding], runs entirely inline (no spawns).
//
// These subtests pin the current (already-correct, or deliberately-unchanged)
// state as a regression guard, so a future edit that "simplifies" one of these
// prompts to a bare Cheap tag (breaking its correctness-critical same-run
// semantics) or drops triage-bugs's existing Cheap/Coding split is caught by CI.

func TestBuiltinJiraTriageBugsLoopPromptsNotSplit(t *testing.T) {
	installBuiltinFragmentsForTest(t)

	t.Run("beads/triage-bugs.prompt.yaml already Cheap+Coding", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", "beads/triage-bugs.prompt.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		prompt, err := ParsePromptFile("beads/triage-bugs.prompt.yaml", data, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		wantTags := []string{"Cheap", "Coding"}
		if len(prompt.PreferredModels) != len(wantTags) {
			t.Fatalf("preferred models = %+v, want tags %v", prompt.PreferredModels, wantTags)
		}
		for i, tag := range wantTags {
			if prompt.PreferredModels[i].ModelTag != tag {
				t.Errorf("preferred model %d = %+v, want %s", i, prompt.PreferredModels[i], tag)
			}
		}
	})

	// These jira loop prompts intentionally keep a single Coding preference
	// (not split into a Cheap outer + spawned Coding child) — see rationale above.
	codingOnlyFiles := []string{
		"jira/status-all-inprogress.prompt.yaml",
		"jira/status-one-inprogress.prompt.yaml",
		"jira/sync-tasks.prompt.yaml",
	}
	for _, file := range codingOnlyFiles {
		t.Run(file+" stays Coding-only (not split)", func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", file))
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := ParsePromptFile(file, data, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(prompt.PreferredModels) != 1 || prompt.PreferredModels[0].ModelTag != "Coding" {
				t.Errorf("preferred models = %+v, want only Coding", prompt.PreferredModels)
			}
		})
	}

	t.Run("jira/work.prompt.yaml has no preferredModels (uses acp_server-based delegation)", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", "jira/work.prompt.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		prompt, err := ParsePromptFile("jira/work.prompt.yaml", data, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(prompt.PreferredModels) != 0 {
			t.Errorf("preferred models = %+v, want none", prompt.PreferredModels)
		}
	})
}
