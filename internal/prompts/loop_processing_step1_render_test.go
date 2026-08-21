package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

// TestLoopProcessingStep1_PromptsPredicate verifies the mitto-s1w refactor of
// loop-processing.prompt.yaml Step 1: with `Loop fixing bug` and `Loop
// implementing feature` registered in the workspace prompt view (via
// `.Prompts.EnabledNames`), Step 1 renders no disable-messages; with those
// names absent from the view, Step 1 emits the per-class disable-messages.
// Pins the .Prompts.Enabled predicate wiring at the consumer level.
func TestLoopProcessingStep1_PromptsPredicate(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	step1Excerpt := func(out string) string {
		i := strings.Index(out, "## Step 1")
		if i < 0 {
			return ""
		}
		rel := strings.Index(out[i:], "## Step 2P")
		if rel < 0 {
			return out[i:]
		}
		return out[i : i+rel]
	}

	// Case A: both driver prompts registered + enabled — Step 1 should NOT
	// emit a disable-message for either class.
	ctxOK := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "true", "WorkOnFeatures": "true"},
		Prompts: cel.PromptsContext{
			Names:        []string{"Loop fixing bug", "Loop implementing feature"},
			EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
		},
	}
	funcs := cel.BuildTemplateFuncMap(ctxOK)
	out, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctxOK, funcs)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate (both enabled): %v", rerr)
	}
	step1 := step1Excerpt(out)
	if strings.Contains(step1, "§B disabled this pass") {
		t.Errorf("both drivers enabled: Step 1 should not disable §B; got:\n%s", step1)
	}
	if strings.Contains(step1, "§C disabled this pass") {
		t.Errorf("both drivers enabled: Step 1 should not disable §C; got:\n%s", step1)
	}
	// The mitto_prompt_get(...) tool-call form must be gone entirely (the
	// bare word "mitto_prompt_get" may still appear in narrative text).
	if strings.Contains(step1, "mitto_prompt_get(") {
		t.Errorf("Step 1 still invokes mitto_prompt_get(...) after refactor:\n%s", step1)
	}

	// Case B: neither driver prompt is registered (zero-value .Prompts) —
	// Step 1 should emit both disable-messages.
	ctxMissing := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "true", "WorkOnFeatures": "true"},
		// Prompts left zero-valued -> .Prompts.Enabled returns false.
	}
	funcs2 := cel.BuildTemplateFuncMap(ctxMissing)
	out2, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctxMissing, funcs2)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate (both missing): %v", rerr)
	}
	step1Missing := step1Excerpt(out2)
	if !strings.Contains(step1Missing, "§B disabled this pass") {
		t.Errorf("missing drivers: Step 1 should disable §B; got:\n%s", step1Missing)
	}
	if !strings.Contains(step1Missing, "§C disabled this pass") {
		t.Errorf("missing drivers: Step 1 should disable §C; got:\n%s", step1Missing)
	}
}

// TestLoopProcessingStep1_PassedVerdict_Mitto3gx pins mitto-3gx: on the happy
// path (each class's driver prompt registered+enabled) Step 1 must render an
// explicit affirmative "**PASSED**" verdict per class, and when a class flag
// is off, an explicit "not applicable this pass" verdict — never an empty
// section. Before the mitto-3gx fix, Step 1 rendered NOTHING on the happy
// path (TestLoopProcessingStep1_PromptsPredicate above only asserts the
// ABSENCE of the disable-messages, which is why this silent-success
// regression went uncaught), which reads as "nothing was validated" and
// invites agents to hand-roll a re-verification via mitto_prompt_get /
// mitto_prompt_list / grep — the exact regression reported in session
// 20260803-204301-e307e7f9 (workspace cgw-managed-tools).
func TestLoopProcessingStep1_PassedVerdict_Mitto3gx(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	builtinDir := "../../config/prompts/builtin"
	path := filepath.Join(builtinDir, "beads-issues/loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	step1Excerpt := func(out string) string {
		i := strings.Index(out, "## Step 1")
		if i < 0 {
			return ""
		}
		rel := strings.Index(out[i:], "## Step 2P")
		if rel < 0 {
			return out[i:]
		}
		return out[i : i+rel]
	}

	// Case A: both driver prompts registered + enabled — Step 1 must render
	// an explicit **PASSED** verdict for each class, not silence.
	ctxOK := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "true", "WorkOnFeatures": "true"},
		Prompts: cel.PromptsContext{
			Names:        []string{"Loop fixing bug", "Loop implementing feature"},
			EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
		},
	}
	funcs := cel.BuildTemplateFuncMap(ctxOK)
	out, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctxOK, funcs)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate (both enabled): %v", rerr)
	}
	step1 := step1Excerpt(out)
	if !strings.Contains(step1, "§B driver check: **PASSED**") {
		t.Errorf("both drivers enabled: Step 1 must render an explicit §B PASSED verdict (not silence); got:\n%s", step1)
	}
	if !strings.Contains(step1, "§C driver check: **PASSED**") {
		t.Errorf("both drivers enabled: Step 1 must render an explicit §C PASSED verdict (not silence); got:\n%s", step1)
	}

	// Case B: both class flags off — Step 1 must render an explicit "not
	// applicable this pass" verdict per class, not silence.
	ctxOff := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "false", "WorkOnFeatures": "false"},
	}
	funcs2 := cel.BuildTemplateFuncMap(ctxOff)
	out2, rerr := RenderPromptTemplate("beads-issue-loop-processing", body, ctxOff, funcs2)
	if rerr != nil {
		t.Fatalf("RenderPromptTemplate (both flags off): %v", rerr)
	}
	step1Off := step1Excerpt(out2)
	if !strings.Contains(step1Off, "§B not applicable this pass (`FixBugs=false`)") {
		t.Errorf("FixBugs=false: Step 1 must render an explicit §B not-applicable verdict; got:\n%s", step1Off)
	}
	if !strings.Contains(step1Off, "§C not applicable this pass (`WorkOnFeatures=false`)") {
		t.Errorf("WorkOnFeatures=false: Step 1 must render an explicit §C not-applicable verdict; got:\n%s", step1Off)
	}
}

// TestLoopProcessingNotifications_MittoDnx pins mitto-dnx: the supervisor
// prompt loop-processing.prompt.yaml must emit workspace-scoped
// notifications on three milestone events, and only on those events.
//
// Acceptance criteria (from the bead):
//
//   - Closing a §B (bug) or §C (feature) bead surfaces a workspace toast
//     within one loop pass — expressed by a `mitto_workspace_ui_notify(...)`
//     call inside each of the two `Bead closed (happy path)` branches with
//     `style: "success"`, a `Task completed` title, and the bead's id
//     carried in `beads_issue:` for click-through (plumbing landed via
//     mitto-9yz).
//   - Addressing a @mitto mention surfaces a workspace toast, clickable —
//     expressed by a `mitto_workspace_ui_notify(...)` call inside the §A
//     `Driver done` branch with `style: "info"`, an `@mitto mention
//     addressed` title, and `beads_issue:` for click-through.
//   - No toasts during phase transitions — expressed by an explicit
//     `Milestone toasts only` bullet in the Guidelines section that names
//     the three legitimate fire sites and forbids the illegitimate ones
//     (phase transitions, dispatches, timeouts, failures).
//
// Rendering is not required — the toast calls are static text (workspace
// UUID + session id come from Go-template context and stay templated in
// the raw body). This test asserts on the parsed prompt body so drift
// (a moved/removed notify call, a flipped style, a stripped beads_issue,
// or a dropped Guidelines bullet) is caught structurally.
func TestLoopProcessingNotifications_MittoDnx(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	path := filepath.Join("..", "..", "config", "prompts", "builtin", "beads-issues/loop-processing.prompt.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("prompt file not found at %s: %v", path, err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-processing.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile: %v", err)
	}
	body := prompt.Content

	// Section slicer: return the body between two headings. `to` may be
	// empty to slice from `from` to end of body.
	section := func(from, to string) string {
		i := strings.Index(body, from)
		if i < 0 {
			return ""
		}
		rest := body[i:]
		if to == "" {
			return rest
		}
		j := strings.Index(rest, to)
		if j < 0 {
			return rest
		}
		return rest[:j]
	}

	// §A — @mitto mention addressed (Driver done branch of the §A wait).
	// The §A wait is bounded by "### §A wait, verify, archive" above and
	// "## §B" below (the section header for the next branch).
	sectionA := section("### §A wait, verify, archive", "{{ if ne .Args.FixBugs")
	if sectionA == "" {
		t.Fatal("§A wait section not found in prompt body")
	}
	if !strings.Contains(sectionA, "mitto_workspace_ui_notify(") {
		t.Errorf("§A wait: expected mitto_workspace_ui_notify(...) call in Driver-done branch; not found")
	}
	if !strings.Contains(sectionA, `title: "@mitto mention addressed"`) {
		t.Errorf("§A wait: expected title `\"@mitto mention addressed\"` in notify call; not found")
	}
	if !strings.Contains(sectionA, `style: "info"`) {
		t.Errorf("§A wait: expected style: \"info\" in notify call; not found")
	}
	if !strings.Contains(sectionA, `beads_issue: "<id>"`) {
		t.Errorf("§A wait: expected beads_issue: \"<id>\" in notify call for click-through (mitto-9yz); not found")
	}

	// §B — Bug close (Bead-closed branch of the §B wait).
	sectionB := section("### §B wait, log, archive", "{{ if ne .Args.WorkOnFeatures")
	if sectionB == "" {
		t.Fatal("§B wait section not found in prompt body")
	}
	if !strings.Contains(sectionB, "mitto_workspace_ui_notify(") {
		t.Errorf("§B wait: expected mitto_workspace_ui_notify(...) call in Bead-closed branch; not found")
	}
	if !strings.Contains(sectionB, `title: "Task completed"`) {
		t.Errorf("§B wait: expected title `\"Task completed\"` in notify call; not found")
	}
	if !strings.Contains(sectionB, `style: "success"`) {
		t.Errorf("§B wait: expected style: \"success\" in notify call; not found")
	}
	if !strings.Contains(sectionB, `beads_issue: "<id>"`) {
		t.Errorf("§B wait: expected beads_issue: \"<id>\" in notify call for click-through; not found")
	}

	// §C — Feature close (Bead-closed branch of the §C wait). The §C wait
	// runs until Step 6 / the ---{{- end }} that closes the WorkOnFeatures
	// gate; the closest stable boundary is "## Step 6".
	sectionC := section("### §C wait, log, archive", "## Step 6")
	if sectionC == "" {
		t.Fatal("§C wait section not found in prompt body")
	}
	if !strings.Contains(sectionC, "mitto_workspace_ui_notify(") {
		t.Errorf("§C wait: expected mitto_workspace_ui_notify(...) call in Bead-closed branch; not found")
	}
	if !strings.Contains(sectionC, `title: "Task completed"`) {
		t.Errorf("§C wait: expected title `\"Task completed\"` in notify call; not found")
	}
	if !strings.Contains(sectionC, `style: "success"`) {
		t.Errorf("§C wait: expected style: \"success\" in notify call; not found")
	}
	if !strings.Contains(sectionC, `beads_issue: "<id>"`) {
		t.Errorf("§C wait: expected beads_issue: \"<id>\" in notify call for click-through; not found")
	}

	// Guidelines — "Milestone toasts only" bullet must be present, must
	// name the three legitimate fire sites, and must forbid the anti-
	// pattern fire sites (phase transitions, dispatches, timeouts,
	// failures). This is the volume guardrail that prevents the LLM from
	// interpreting §B/§C phase transitions or spawn-side events as
	// notify-worthy.
	guidelines := section("## Guidelines", "")
	if guidelines == "" {
		t.Fatal("Guidelines section not found in prompt body")
	}
	if !strings.Contains(guidelines, "Milestone toasts only") {
		t.Errorf("Guidelines: expected `Milestone toasts only` bullet; not found")
	}
	forbidden := []string{"phase transitions", "dispatches", "timeouts", "failures"}
	for _, phrase := range forbidden {
		if !strings.Contains(guidelines, phrase) {
			t.Errorf("Guidelines `Milestone toasts only` bullet: expected forbidden fire site %q named; not found", phrase)
		}
	}

	// Volume guardrail: the raw body should never carry `sound: true`,
	// `sticky: true`, or `native: true` on a notify call — loops fire
	// frequently and audible/persistent spam is the anti-pattern the
	// Guidelines bullet explicitly forbids. Guard against them showing
	// up in any notify site (§A/§B/§C or anywhere else).
	for _, banned := range []string{"sound: true", "sticky: true", "native: true"} {
		if strings.Contains(body, banned) {
			t.Errorf("prompt body: %q must not appear on any notify call (spam anti-pattern)", banned)
		}
	}
}
