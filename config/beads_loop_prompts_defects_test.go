package config

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
	promptspkg "github.com/inercia/mitto/internal/prompts"
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
		mergedOrch    = "beads-issues/loop-processing.prompt.yaml"
		bugDriver     = "beads-issues/loop-fixing-bug.prompt.yaml"
		featDriver    = "beads-issues/loop-implementing-feature.prompt.yaml"
		mentionDriver = "beads-issues/mention-driver.prompt.yaml"
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
		bugDriver     = "beads-issues/loop-fixing-bug.prompt.yaml"
		featDriver    = "beads-issues/loop-implementing-feature.prompt.yaml"
		mentionDriver = "beads-issues/mention-driver.prompt.yaml"
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

// TestBeadsLoopPrompts_Defects_mitto9mk_WorkspaceScopedConcurrencyGate is the
// regression guard for mitto-9mk's workspace-scoped concurrency invariant:
// the L1 orchestrator's "1 active driver" gate in §B / §C, and the
// "Skip beads with active conversations" guideline, must both be scoped to
// the whole workspace — not just to the session's OWN children via
// `{{ .Children.MCPText }}`. When two `Loop processing tasks` sessions run in
// the same workspace, per-caller scoping lets both spawn a Fix/Implement
// driver on the same pass, breaking the workspace-wide "at most one active
// driver" invariant.
//
// mitto-9mk originally enforced this by requiring a runtime
// `mitto_conversation_list(workspace: ...)` MCP call in the gate paragraphs.
// mitto-5vu replaced that runtime round-trip with a strictly-fresher
// template-time snapshot: `.Workspace.Peers.*` (populated by the server at
// prompt-dispatch time, scoped to non-archived siblings sharing this
// session's `(WorkingDir, ACPServer)` — i.e. the workspace boundary). The
// invariant is unchanged; only the enforcement primitive moved.
//
// This test accepts EITHER enforcement primitive so a future refactor away
// from `.Workspace.Peers` back to a runtime call — or forward to some third
// primitive — stays green as long as the paragraph is workspace-scoped and
// not per-caller. It fails loudly if the gate reverts to `.Children.MCPText`
// or drops both workspace-scoped primitives entirely.
func TestBeadsLoopPrompts_Defects_mitto9mk_WorkspaceScopedConcurrencyGate(t *testing.T) {
	const mergedOrch = "beads-issues/loop-processing.prompt.yaml"

	body, err := fs.ReadFile(BuiltinPromptsFS, BuiltinPromptsDir+"/"+mergedOrch)
	if err != nil {
		t.Fatalf("read embedded prompt %s: %v", mergedOrch, err)
	}
	src := string(body)

	// Slice out each concurrency-gate paragraph by its section heading and the
	// next `## ` heading, so we assert on the specific paragraph rather than
	// the whole file (which legitimately references `.Children.MCPText` in the
	// §A dedup and Step 2P reap paragraphs).
	sliceSection := func(marker string) string {
		start := strings.Index(src, marker)
		if start < 0 {
			t.Fatalf("%s: section marker %q not found — test needs updating to match the current prompt body", mergedOrch, marker)
		}
		rest := src[start:]
		end := strings.Index(rest[len(marker):], "\n  ## ")
		if end < 0 {
			return rest
		}
		return rest[:len(marker)+end]
	}

	sectionB := sliceSection("## §B — Fix ONE bug")
	sectionC := sliceSection("## §C — Implement ONE feature")

	// isWorkspaceScoped returns true iff the paragraph enforces the gate via
	// a workspace-scoped primitive:
	//   - `.Workspace.Peers` — template-time snapshot (mitto-5vu, preferred).
	//   - `mitto_conversation_list(... workspace: ...)` — runtime enumeration
	//     (mitto-9mk original). Accepted for backward compat so a swap of
	//     primitives stays green.
	isWorkspaceScoped := func(section string) bool {
		if strings.Contains(section, ".Workspace.Peers") {
			return true
		}
		if strings.Contains(section, "mitto_conversation_list") &&
			strings.Contains(section, ".Workspace.UUID") {
			return true
		}
		return false
	}

	// Gate 1: §B concurrency-gate paragraph must NOT count via `.Children.MCPText`
	// (per-caller scope defeats the workspace-wide 1-driver invariant).
	if strings.Contains(sectionB, ".Children.MCPText") {
		t.Errorf("[mitto-9mk §B concurrency-gate is per-caller] §B still counts active drivers via `.Children.MCPText`; that field only lists children spawned by THIS orchestrator session. Two parallel `Loop processing tasks` sessions in the same workspace both see count=0 and both spawn — violating the workspace-wide `1 active driver` invariant. Use `.Workspace.Peers` (template-time snapshot, mitto-5vu) or `mitto_conversation_list(workspace: \"{{ .Workspace.UUID }}\", ...)` (runtime enumeration, mitto-9mk)")
	}

	// Gate 2: §C concurrency-gate paragraph must NOT count via `.Children.MCPText`.
	if strings.Contains(sectionC, ".Children.MCPText") {
		t.Errorf("[mitto-9mk §C concurrency-gate is per-caller] §C still counts active drivers via `.Children.MCPText`; same failure mode as §B — two parallel orchestrator sessions both spawn simultaneously. Use `.Workspace.Peers` or `mitto_conversation_list` with a workspace filter")
	}

	// Gate 3: §B / §C concurrency-gate paragraphs MUST reference a
	// workspace-scoped primitive (either `.Workspace.Peers` or
	// `mitto_conversation_list(workspace: ...)`), otherwise the gate is still
	// per-session even if it doesn't mention `.Children.MCPText`.
	if !isWorkspaceScoped(sectionB) {
		t.Errorf("[mitto-9mk §B concurrency-gate not workspace-scoped] §B does not reference either `.Workspace.Peers` (mitto-5vu template-time snapshot) or `mitto_conversation_list` with a `workspace:` filter (mitto-9mk runtime enumeration). Without a workspace-scoped primitive the gate cannot see peer orchestrator sessions' drivers, and two parallel `Loop processing tasks` sessions each see count=0 and both spawn")
	}
	if !isWorkspaceScoped(sectionC) {
		t.Errorf("[mitto-9mk §C concurrency-gate not workspace-scoped] §C does not reference either `.Workspace.Peers` or `mitto_conversation_list` with a `workspace:` filter — the `1 active driver` invariant cannot be enforced across parallel orchestrator sessions")
	}

	// Gate 4: the "Skip beads with active conversations" guard (later in the
	// Guidelines section) must also be workspace-scoped, otherwise two
	// orchestrators can each dispatch the SAME bead to a Fix/Implement driver
	// on the same pass. Locate the paragraph by its bold header.
	// After the mitto-9mk fix the bullet is renamed to
	// "**Skip beads with active conversations (workspace-scoped).**"; accept
	// either form so this assertion also protects against a regression that
	// silently reverts the header to the per-caller wording.
	skipMarker := "**Skip beads with active conversations (workspace-scoped).**"
	if !strings.Contains(src, skipMarker) {
		skipMarker = "**Skip beads with active conversations.**"
	}
	skipStart := strings.Index(src, skipMarker)
	if skipStart < 0 {
		t.Fatalf("%s: skip-active-beads marker %q not found — test needs updating", mergedOrch, skipMarker)
	}
	// Paragraph runs until the next blank line + list bullet.
	skipRest := src[skipStart:]
	skipEnd := strings.Index(skipRest, "\n  - **")
	if skipEnd < 0 {
		skipEnd = len(skipRest)
	}
	skipPara := skipRest[:skipEnd]

	// The paragraph MUST reference a workspace-scoped primitive. It may
	// additionally reference `.Children.MCPText` as a first-pass filter (the
	// mitto-5vu wording unions the session's own children with the workspace
	// peer snapshot: "either this session's {{ .Children.MCPText }} OR
	// .Workspace.Peers.All") — that's fine as long as a workspace-scoped
	// primitive is also present so peer orchestrators' children are visible.
	if !isWorkspaceScoped(skipPara) {
		t.Errorf("[mitto-9mk skip-active-beads is per-caller] the `Skip beads with active conversations` guideline does not reference either `.Workspace.Peers` (mitto-5vu) or `mitto_conversation_list(workspace: ...)` (mitto-9mk); a peer orchestrator's active child on the same bead is invisible, so both sessions can dispatch the same bead in one pass. Consult `.Workspace.Peers.All` or `mitto_conversation_list(workspace: \"{{ .Workspace.UUID }}\", archived: false)` for the workspace-wide active-bead set")
	}
}

// TestBeadsLoopPrompts_mitto4vr_BeadsStateWait is the regression guard for
// mitto-4vr: the L1 orchestrator's §B and §C wait blocks must wait on the
// bead's terminal state (`closed`) via `mitto_conversation_wait(what:
// "beads_issues_reached_state", ...)` — landed by mitto-7rw — rather than on
// the child agent's yield via `mitto_children_tasks_wait`. The two are not
// equivalent ("child agent stopped talking" ≠ "bead closed"): the child may
// yield mid-turn, hit its context budget, crash, or be archived without a
// terminal `bd` action, so the bead-state wait is strictly more correct AND
// activates the orchestrator ~1 per real terminal event instead of ~1 per
// child turn.
//
// §A is deliberately left alone — its success criterion IS "child agent
// stopped talking" (one-shot mention drivers that don't necessarily touch a
// bead's status), so it keeps `mitto_children_tasks_wait`. This test also
// asserts §A did not accidentally get rewritten by a §B/§C-wide edit. The
// assertions run against rendered prompt bodies because MaxParallelWorkers
// intentionally changes §B/§C wait semantics at dispatch time.
func TestBeadsLoopPrompts_mitto4vr_BeadsStateWait(t *testing.T) {
	const mergedOrch = "beads-issues/loop-processing.prompt.yaml"

	const builtinDir = "prompts/builtin"
	fragments, loadErrs, err := promptspkg.LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("load builtin fragments: %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("load builtin fragments: %+v", loadErrs)
	}
	prompt, err := promptspkg.LoadPromptFileWithFragments(builtinDir, mergedOrch, fragments)
	if err != nil {
		t.Fatalf("load builtin prompt %s: %v", mergedOrch, err)
	}

	render := func(t *testing.T, args map[string]string) string {
		t.Helper()
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "orchestrator-1"},
			Args:    args,
			Prompts: cel.PromptsContext{
				Names:        []string{"Loop fixing bug", "Loop implementing feature"},
				EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
			},
		}
		out, renderErr := promptspkg.RenderPromptTemplateWithFragments(
			prompt.Name, prompt.Content, ctx, cel.BuildTemplateFuncMap(ctx), fragments,
		)
		if renderErr != nil {
			t.Fatalf("render builtin prompt %s: %v", mergedOrch, renderErr)
		}
		return out
	}

	// Slice out each wait block by its `### §X wait, log, archive` heading and
	// the next `### ` / `## ` / `---` boundary so the assertions target only
	// the wait paragraph. Reuses the same slicing shape as the mitto-9mk test.
	sliceWait := func(t *testing.T, src, marker string) string {
		t.Helper()
		start := strings.Index(src, marker)
		if start < 0 {
			t.Fatalf("%s: wait-block marker %q not found — test needs updating", mergedOrch, marker)
		}
		rest := src[start:]
		// Stop at the next section heading or horizontal rule; the wait block
		// always ends before one of these appears.
		enders := []string{"\n### ", "\n## ", "\n---"}
		end := len(rest)
		for _, e := range enders {
			if i := strings.Index(rest[len(marker):], e); i >= 0 && len(marker)+i < end {
				end = len(marker) + i
			}
		}
		return rest[:end]
	}

	modes := []struct {
		name             string
		args             map[string]string
		wantBeadsIssues  string
		wantBeadsMatch   string
		wrongBeadsIssues string
		wrongBeadsMatch  string
	}{
		{
			name:             "default serial render",
			args:             map[string]string{"FixBugs": "true", "WorkOnFeatures": "true"},
			wantBeadsIssues:  `beads_issues: ["<id>"]`,
			wantBeadsMatch:   `beads_match: "all"`,
			wrongBeadsIssues: `beads_issues: ["<ids…>"]`,
			wrongBeadsMatch:  `beads_match: "any"`,
		},
		{
			name: "raised parallel cap render",
			args: map[string]string{
				"FixBugs":            "true",
				"WorkOnFeatures":     "true",
				"MaxParallelWorkers": "2",
			},
			wantBeadsIssues:  `beads_issues: ["<ids…>"]`,
			wantBeadsMatch:   `beads_match: "any"`,
			wrongBeadsIssues: `beads_issues: ["<id>"]`,
			wrongBeadsMatch:  `beads_match: "all"`,
		},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			src := render(t, mode.args)
			waitA := sliceWait(t, src, "### §A wait, verify, archive")
			waitB := sliceWait(t, src, "### §B wait, log, archive")
			waitC := sliceWait(t, src, "### §C wait, log, archive")

			// §A remains a child-yield wait in every render mode.
			for _, want := range []string{"mitto_children_tasks_wait", "timeout_seconds: 1800"} {
				if !strings.Contains(waitA, want) {
					t.Errorf("[mitto-4vr §A leave-alone regression] §A wait block missing %q", want)
				}
			}
			if strings.Contains(waitA, "beads_issues_reached_state") {
				t.Error("[mitto-4vr §A leave-alone regression] §A must wait for child-agent yield, not bead state")
			}

			// §B and §C independently retain the bead-state wait, timeout,
			// bookkeeping prose, archive action, and correct bead identifiers.
			for _, tc := range []struct {
				section string
				block   string
			}{
				{"§B", waitB},
				{"§C", waitC},
			} {
				for _, want := range []string{
					`what: "beads_issues_reached_state"`,
					`beads_target_state: "closed"`,
					mode.wantBeadsIssues,
					mode.wantBeadsMatch,
					"timeout_seconds: 14400",
					`Orchestrator: 4h bead-state wait timed out; leaving child running.`,
					"mitto_conversation_archive",
				} {
					if !strings.Contains(tc.block, want) {
						t.Errorf("[mitto-4vr %s rendered semantics] wait block missing %q", tc.section, want)
					}
				}
				for _, wrong := range []string{
					"mitto_children_tasks_wait",
					`beads_issues: ["<child-id>"]`,
					mode.wrongBeadsIssues,
					mode.wrongBeadsMatch,
				} {
					if strings.Contains(tc.block, wrong) {
						t.Errorf("[mitto-4vr %s rendered semantics] wait block unexpectedly contains %q", tc.section, wrong)
					}
				}
			}

			// Rendered negative space: exactly §B and §C use the bead-state wait.
			if got, want := strings.Count(src, "beads_issues_reached_state"), 2; got != want {
				t.Errorf("[mitto-4vr token leak] rendered prompt contains %d bead-state wait tokens; want %d", got, want)
			}
		})
	}
}
