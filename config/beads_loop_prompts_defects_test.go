package config

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestBeadsLoopPrompts_Defects_mitto6am is the failing reproduction test for
// mitto-6am: the "Loop implementing features" / "Loop implementing feature"
// prompt family shares the same three structural defects as its bug-fix
// siblings (mitto-dj9, mitto-i5k, mitto-fko), plus a fourth Step 3f fan-out
// vector unique to the features driver.
//
// This test asserts the ABSENCE of the four defective patterns in the
// embedded builtin prompt YAMLs. While the defects are still present it
// fails; when the fix phase rewrites the prompts (prompt_name-based spawns,
// unconditional Done branch, recently-closed-parents filter, named worker
// prompt for Step 3f) it will flip to green.
func TestBeadsLoopPrompts_Defects_mitto6am(t *testing.T) {
	const (
		bugsOrch   = "beads-issue-loop-fixing-bugs.prompt.yaml"
		bugDriver  = "beads-issue-loop-fixing-bug.prompt.yaml"
		featsOrch  = "beads-issue-loop-implementing-features.prompt.yaml"
		featDriver = "beads-issue-loop-implementing-feature.prompt.yaml"
	)

	load := func(name string) string {
		b, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+name)
		if err != nil {
			t.Fatalf("read embedded prompt %s: %v", name, err)
		}
		return string(b)
	}

	// Defect 1 — placeholder short-circuit vector (mitto-dj9).
	// Orchestrator Step 4 spawns children via `initial_prompt: <driverBody>` +
	// `loop_prompt: <driverBody>` instead of `prompt_name:` + `arguments:`.
	// Fix: replace with `prompt_name: "Loop fixing bug" | "Loop implementing feature"`.
	placeholderInitial := regexp.MustCompile(`initial_prompt:\s*<driverBody>`)
	placeholderLoop := regexp.MustCompile(`loop_prompt:\s*<driverBody>`)
	for _, name := range []string{bugsOrch, featsOrch} {
		body := load(name)
		if placeholderInitial.MatchString(body) {
			t.Errorf("[defect-1 placeholder-vector, mitto-dj9] %s still contains `initial_prompt: <driverBody>` at Step 4; expected `prompt_name:` + `arguments:` so the server expands the driver body from the named template", name)
		}
		if placeholderLoop.MatchString(body) {
			t.Errorf("[defect-1 placeholder-vector, mitto-dj9] %s still contains `loop_prompt: <driverBody>` at Step 4; expected `prompt_name:` + `arguments:` so the server expands the driver body from the named template", name)
		}
	}

	// Defect 2 — soft-gated Done branch (mitto-i5k).
	// Per-item driver Done branch marks `bd close` as "optional but
	// recommended", inviting the LLM to skip past `loop_enabled: false`.
	// Fix: unconditional `bd close` + unconditional `loop_enabled: false`,
	// evaluated as the very first branch against a mandatory fresh
	// `bd show --json`.
	softClose := regexp.MustCompile(`bd close[^\n]*# optional but recommended`)
	for _, name := range []string{bugDriver, featDriver} {
		body := load(name)
		if softClose.MatchString(body) {
			t.Errorf("[defect-2 soft-gated-done, mitto-i5k] %s Done branch still marks `bd close` as `# optional but recommended`; expected unconditional close + unconditional loop_enabled:false as the first branch of Step 3", name)
		}
	}

	// Defect 3 — subtask spawn after parent closes (mitto-fko).
	// Orchestrator Step 2 enumerates via `bd ready` (falling back to
	// `bd list --status open`) with no filter for entries whose
	// `parent-child` dependency's parent was closed within the current
	// outer run. The prompt should mention this filter explicitly so the
	// LLM applies it.
	//
	// Signal the fix has been applied by requiring EITHER a mention of
	// `parent-child` OR of `recently-closed` in the orchestrator body.
	// This is a lint-style check on prompt text, not on behaviour: the
	// fix must instruct the LLM to exclude recently-closed parents'
	// children.
	parentChild := regexp.MustCompile(`parent-child`)
	recentlyClosed := regexp.MustCompile(`recently[- ]closed`)
	for _, name := range []string{bugsOrch, featsOrch} {
		body := load(name)
		if !parentChild.MatchString(body) && !recentlyClosed.MatchString(body) {
			t.Errorf("[defect-3 subtask-spawn, mitto-fko] %s Step 2 has no filter language for `parent-child` deps or `recently-closed` parents; expected instructions to exclude beads whose parent bead was closed within the current outer run", name)
		}
	}

	// Defect 4 — Step 3f grand-child fan-out with inline free-text worker
	// prompts (features driver only, worse than the bug family because it
	// spawns from *inside* an already-scheduled loop driver).
	// The current text tells the LLM to synthesize a "fully self-contained
	// worker prompt" inline; the fix registers that worker body as a named
	// workspace prompt and spawns it via `prompt_name:` + `arguments:`.
	body := load(featDriver)
	if strings.Contains(body, "self-contained worker prompt") {
		t.Errorf("[defect-4 step3f-inline-worker, mitto-6am-unique] %s Step 3f still tells the driver LLM to seed a `self-contained worker prompt` inline; expected `mitto_conversation_new(..., prompt_name: \"<worker>\", arguments: {...})` so the grand-child body is expanded server-side and cannot short-circuit to a placeholder", featDriver)
		_ = body
	}
}

// TestBeadsLoopPrompts_Defects_mitto_i5k_BranchOrder is the failing reproduction
// test for the residual mitto-i5k defect that commit 24cfbf0f did not close.
//
// Commit 24cfbf0f removed the "# optional but recommended" soft-gating on
// `bd close` in the per-issue driver Done branch (Defect 2 above). But the
// upstream branch table in Step 3 still enumerates the terminal Done bullet
// LAST — after all the intermediate-phase dispatch bullets. The Step 3d/3e
// heading is tagged "EVALUATE FIRST", but that hint lives only on the section
// heading (which itself appears physically last in the prompt body), not in
// the top-level enumeration the LLM parses to pick a branch. With labels
// `[researched, reproduced, fixed]` (or `[planned, implemented, tested,
// verified]` for the feature driver), an LLM enumerating a bullet list is
// prone to (i) match a middle bullet on partial precondition and skip the
// Done bullet, or (ii) decide "nothing new to do" and end the turn without
// invoking `mitto_conversation_update(loop_enabled: false)`; the loop's
// onCompletion trigger then re-fires the driver 30s later, giving the
// observed "loop keeps re-firing on a fully-fixed bead" symptom.
//
// The structural fix is to enumerate the terminal Done bullet FIRST in the
// Step 3 branch table so it is evaluated as an early-exit before any
// dispatch branch. This test asserts that ordering: the Done bullet must
// appear BEFORE the first-stage bullet in the Step 3 enumeration of each
// per-issue driver. It fails on the current YAMLs; the fix will flip it
// green.
func TestBeadsLoopPrompts_Defects_mitto_i5k_BranchOrder(t *testing.T) {
	const (
		bugDriver  = "beads-issue-loop-fixing-bug.prompt.yaml"
		featDriver = "beads-issue-loop-implementing-feature.prompt.yaml"
	)

	load := func(name string) string {
		b, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+name)
		if err != nil {
			t.Fatalf("read embedded prompt %s: %v", name, err)
		}
		return string(b)
	}

	cases := []struct {
		file         string
		firstStage   string // marker for the Step 3a "no state label yet" bullet
		terminalDone string // marker for the terminal "Done (handled inline)" bullet
	}{
		{bugDriver, "Step 3a: dispatch Investigate", "Step 3d: Done (handled inline)"},
		{featDriver, "Step 3a: dispatch Plan", "Step 3e: Done (handled inline)"},
	}

	for _, c := range cases {
		body := load(c.file)
		idxFirst := strings.Index(body, c.firstStage)
		idxDone := strings.Index(body, c.terminalDone)
		if idxFirst < 0 {
			t.Fatalf("%s: first-stage marker %q not found — test needs its markers updated to match the current prompt body", c.file, c.firstStage)
		}
		if idxDone < 0 {
			t.Fatalf("%s: terminal Done marker %q not found — test needs its markers updated to match the current prompt body", c.file, c.terminalDone)
		}
		if idxDone > idxFirst {
			t.Errorf("[mitto-i5k residual branch-order] %s Step 3 branch enumeration still lists the terminal Done bullet AFTER the first-stage bullet; expected the Done bullet to be enumerated FIRST so an LLM parsing the branch table evaluates the `fixed`/`verified`-present early-exit before any dispatch branch (got %q at byte %d, which is AFTER %q at byte %d)", c.file, c.terminalDone, idxDone, c.firstStage, idxFirst)
		}
	}
}
