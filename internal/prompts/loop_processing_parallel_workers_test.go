package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestLoopProcessing_MaxParallelWorkers pins the MaxParallelWorkers ceiling on
// the "Loop processing tasks" orchestrator. The parameter raises the workspace
// worker cap, but a second worker may only start on a Step 3D disjointness
// proof — the default stays strictly serial, and "Pull Request" forces the cap
// back to 1 (per-bead branch switching races in the one shared working tree).
func TestLoopProcessing_MaxParallelWorkers(t *testing.T) {
	baseArgs := func(extra map[string]string) map[string]string {
		args := map[string]string{"FixBugs": "true", "WorkOnFeatures": "true"}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	render := func(t *testing.T, args map[string]string) string {
		t.Helper()
		return renderBuiltinPromptWithFragments(t, "Loop processing tasks",
			&cel.PromptEnabledContext{
				Session: cel.SessionContext{ID: "orch-1"},
				Args:    args,
				Prompts: cel.PromptsContext{
					Names:        []string{"Loop fixing bug", "Loop implementing feature"},
					EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
				},
			})
	}

	// Case A — default (parameter unset). Strictly serial: no Step 3D, and
	// every cap mention reads 1.
	t.Run("default is serial", func(t *testing.T) {
		out := render(t, baseArgs(nil))
		if strings.Contains(out, "Step 3D") {
			t.Errorf("default render must not contain the Step 3D parallel gate:\n%s",
				sliceAfter(out, "Step 3D", 300))
		}
		for _, want := range []string{
			"**Cap: 1 active worker per workspace**",
			"concurrency cap (1 active worker in workspace) reached; bug queued",
			"concurrency cap (1 active worker in workspace) reached; feature queued",
			"Serial execution prevents overlapping-file races",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("default render missing %q", want)
			}
		}
		// The wait must stay single-bead / match=all.
		if !strings.Contains(out, `beads_match: "all"`) || strings.Contains(out, `beads_match: "any"`) {
			t.Error("default render must wait with beads_match: \"all\" on a single bead")
		}
	})

	// Case B — cap raised. Step 3D appears, the cap literal propagates to
	// every gate, and the multi-bead wait switches to match=any.
	t.Run("raised cap arms the disjointness gate", func(t *testing.T) {
		out := render(t, baseArgs(map[string]string{"MaxParallelWorkers": "2"}))
		for _, want := range []string{
			"## Step 3D — Can a second worker start? (disjointness proof)",
			"**Cap: 2 active workers per workspace**",
			"ceiling, not a target",
			"concurrency cap (2 active workers in workspace) reached; bug queued",
			"concurrency cap (2 active workers in workspace) reached; feature queued",
			"Never parallelize on a hunch",
			`beads_match: "any"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("cap=2 render missing %q", want)
			}
		}
		// The proof must be evidence-based, not inferred: this is the whole
		// safety property of the feature.
		for _, want := range []string{
			"Recorded file surface on both sides",
			"Disjoint at directory granularity",
			"No shared build/config surface",
			"No dependency edge, no shared parent epic",
			"Do NOT infer a\n   surface by reading code",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("cap=2 render missing disjointness criterion %q", want)
			}
		}
		if strings.Contains(out, "Serial execution prevents overlapping-file races") {
			t.Error("cap=2 render must not claim serial execution")
		}
	})

	// Case C — "Pull Request" forces serial regardless of the requested cap:
	// per-bead branches are checked out in the ONE shared working tree.
	t.Run("pull request forces cap back to 1", func(t *testing.T) {
		out := render(t, baseArgs(map[string]string{
			"MaxParallelWorkers": "3",
			"SubmitStrategy":     "Pull Request",
		}))
		if strings.Contains(out, "Step 3D") {
			t.Error("SubmitStrategy=Pull Request must suppress the Step 3D parallel gate")
		}
		if !strings.Contains(out, "**Cap: 1 active worker per workspace**") {
			t.Errorf("SubmitStrategy=Pull Request must force the cap to 1; got:\n%s",
				sliceAfter(out, "Cap:", 200))
		}
	})

	// Case D — the post-task hook is exclusivity, not a slot: even with the
	// cap raised it requires a live worker count of exactly 0.
	t.Run("post-task hook stays exclusive", func(t *testing.T) {
		out := render(t, baseArgs(map[string]string{
			"MaxParallelWorkers":  "2",
			"PostIterationPrompt": "Run tests",
		}))
		if !strings.Contains(out, "requires a live worker count of exactly 0") {
			t.Errorf("cap=2 + hook: Step 5H must require a zero live worker count; got:\n%s",
				sliceAfter(out, "Exclusivity gate", 500))
		}
	})
}
