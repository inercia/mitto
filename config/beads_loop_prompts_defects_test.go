package config

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestBeadsLoopPrompts_Defects_mitto6am is the regression guard for the four
// structural defects originally tracked as mitto-dj9, mitto-i5k, mitto-fko,
// and the unique mitto-6am Step 3f fan-out vector. The three list-level
// orchestrators (bugs-plural / features-plural / addressing-comments) have
// since been consolidated into a single "Loop processing beads" prompt
// (beads-issue-loop-processing.prompt.yaml), so the anti-defect assertions
// that originally targeted the plural orchestrators now target the merged
// prompt. The per-item drivers (bug / feature, singular) still exist and are
// still checked for defects 2 and 4.
func TestBeadsLoopPrompts_Defects_mitto6am(t *testing.T) {
	const (
		mergedOrch    = "beads-issue-loop-processing.prompt.yaml"
		bugDriver     = "beads-issue-loop-fixing-bug.prompt.yaml"
		featDriver    = "beads-issue-loop-implementing-feature.prompt.yaml"
		mentionDriver = "beads-issue-mention-driver.prompt.yaml"
	)

	load := func(name string) string {
		b, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+name)
		if err != nil {
			t.Fatalf("read embedded prompt %s: %v", name, err)
		}
		return string(b)
	}

	// Defect 1 — placeholder short-circuit vector (mitto-dj9). Merged
	// orchestrator §B/§C must spawn nested drivers via `prompt_name:` +
	// `arguments:`, not via `initial_prompt: <driverBody>` /
	// `loop_prompt: <driverBody>` placeholders.
	placeholderInitial := regexp.MustCompile(`initial_prompt:\s*<driverBody>`)
	placeholderLoop := regexp.MustCompile(`loop_prompt:\s*<driverBody>`)
	{
		body := load(mergedOrch)
		if placeholderInitial.MatchString(body) {
			t.Errorf("[defect-1 placeholder-vector, mitto-dj9] %s still contains `initial_prompt: <driverBody>`; expected `prompt_name:` + `arguments:` so the server expands the driver body from the named template", mergedOrch)
		}
		if placeholderLoop.MatchString(body) {
			t.Errorf("[defect-1 placeholder-vector, mitto-dj9] %s still contains `loop_prompt: <driverBody>`; expected `prompt_name:` + `arguments:` so the server expands the driver body from the named template", mergedOrch)
		}
	}

	// Defect 2 — soft-gated Done branch (mitto-i5k). Per-item driver Done
	// branch must not mark `bd close` as "optional but recommended".
	softClose := regexp.MustCompile(`bd close[^\n]*# optional but recommended`)
	for _, name := range []string{bugDriver, featDriver} {
		body := load(name)
		if softClose.MatchString(body) {
			t.Errorf("[defect-2 soft-gated-done, mitto-i5k] %s Done branch still marks `bd close` as `# optional but recommended`; expected unconditional close + unconditional loop_enabled:false as the first branch of Step 3", name)
		}
	}

	// Defect 3 — subtask spawn after parent closes (mitto-fko). The merged
	// orchestrator's Step 2 must mention the parent-child / recently-closed
	// filter so the LLM excludes beads whose parent was closed within the
	// current outer run.
	parentChild := regexp.MustCompile(`parent-child`)
	recentlyClosed := regexp.MustCompile(`recently[- ]closed`)
	{
		body := load(mergedOrch)
		if !parentChild.MatchString(body) && !recentlyClosed.MatchString(body) {
			t.Errorf("[defect-3 subtask-spawn, mitto-fko] %s Step 2 has no filter language for `parent-child` deps or `recently-closed` parents; expected instructions to exclude beads whose parent bead was closed within the current outer run", mergedOrch)
		}
	}

	// Defect 4 — Step 3f grand-child fan-out with inline free-text worker
	// prompts (features driver only). The driver must not tell the LLM to
	// synthesize a "self-contained worker prompt" inline.
	body := load(featDriver)
	if strings.Contains(body, "self-contained worker prompt") {
		t.Errorf("[defect-4 step3f-inline-worker, mitto-6am-unique] %s Step 3f still tells the driver LLM to seed a `self-contained worker prompt` inline; expected `mitto_conversation_new(..., prompt_name: \"<worker>\", arguments: {...})` so the grand-child body is expanded server-side and cannot short-circuit to a placeholder", featDriver)
		_ = body
	}

	// Defect 5 (mitto-91wk) — mention driver must also honor all three
	// structural anti-patterns. §A in the merged orchestrator spawns the
	// mention router; the router itself is a phase-based driver just like
	// the bug/feature drivers, so it inherits the same guards.
	{
		// dj9: mention-driver's phase dispatches must use `prompt_name:`
		// (server-expanded named body) and must NOT ship a free-text
		// `prompt:` alongside — the free text short-circuits the name.
		mentionBody := load(mentionDriver)
		// Assert that dispatches do reference the four phase prompt names —
		// if they don't, the driver is broken.
		for _, phaseName := range []string{
			"Mention — investigate phase",
			"Mention — plan phase",
			"Mention — implement phase",
			"Mention — answer phase",
		} {
			if !strings.Contains(mentionBody, phaseName) {
				t.Errorf("[defect-5 mention-driver-missing-phase, mitto-91wk] %s does not reference phase prompt %q — driver cannot dispatch its state machine", mentionDriver, phaseName)
			}
		}
		// Assert §A spawn uses prompt_name (not initial_prompt) for the
		// mention driver seed, matching the §B/§C pattern.
		orchBody := load(mergedOrch)
		if !strings.Contains(orchBody, `prompt_name: "Mention — driver"`) {
			t.Errorf("[defect-5 §A placeholder-vector, mitto-91wk] %s §A does not spawn via `prompt_name: \"Mention — driver\"`; expected server-expanded named body", mergedOrch)
		}
		if !strings.Contains(orchBody, `loop_prompt_name: "Mention — driver"`) {
			t.Errorf("[defect-5 §A placeholder-vector, mitto-91wk] %s §A does not set `loop_prompt_name: \"Mention — driver\"`; expected the router to re-fire via named prompt", mergedOrch)
		}
	}

	// Defect 6 (mitto-91wk) — mention driver must not synthesize inline
	// free-text worker prompts (mitto-6am vector applied to the mention
	// router). All phase dispatches must go through named prompts.
	{
		mentionBody := load(mentionDriver)
		if strings.Contains(mentionBody, "self-contained worker prompt") {
			t.Errorf("[defect-6 mention-inline-worker, mitto-91wk/mitto-6am] %s tells the driver LLM to seed a `self-contained worker prompt` inline; expected `mitto_conversation_send_prompt(..., prompt_name: \"<phase>\", ...)` so the phase body is server-expanded", mentionDriver)
		}
		// The router must never do phase-label writes inline — those are the
		// phase prompts' job. It is allowed to *describe* the labels in
		// branching prose, but must not contain a literal `bd update ...
		// --add-label mention-{investigated,planned,implemented,answered}`
		// command line inside a shell block.
		forbiddenLabelWrites := regexp.MustCompile(`bd update[^\n]*--add-label mention-(investigated|planned|implemented|answered)`)
		if forbiddenLabelWrites.MatchString(mentionBody) {
			t.Errorf("[defect-6 mention-inline-label-write, mitto-91wk] %s writes a phase-scoped `--add-label mention-*` command inline; expected the phase prompts to own their own label writes", mentionDriver)
		}
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
		bugDriver     = "beads-issue-loop-fixing-bug.prompt.yaml"
		featDriver    = "beads-issue-loop-implementing-feature.prompt.yaml"
		mentionDriver = "beads-issue-mention-driver.prompt.yaml"
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
		// mitto-91wk: mention driver applies the same branch-order rule.
		// Its terminal branch is "Step 3f: Finalize (handled inline)" and
		// must be enumerated BEFORE the first-stage "dispatch Investigate"
		// / "dispatch Answer" branches. Both markers are text that appears
		// only inside the Step 3 branch enumeration (not the later section
		// headings), so the first-occurrence semantics of strings.Index
		// pin the assertion to the enumeration ordering.
		{mentionDriver, "Step 3a (classify)", "Step 3f: Finalize (handled inline)"},
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
