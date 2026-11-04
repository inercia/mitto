package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/inercia/mitto/internal/cel"
)

const (
	startWorkLinkedLeafBaselineBytes = 20373
	startWorkLinkedLeafBudgetBytes   = 14000
)

func renderBuiltinPromptForWorkRegression(t *testing.T, file string, ctx *cel.PromptEnabledContext) (string, string) {
	t.Helper()
	installBuiltinFragmentsForTest(t)

	const builtinDir = "../../config/prompts/builtin"
	data, err := os.ReadFile(filepath.Join(builtinDir, file))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", file, err)
	}
	prompt, err := ParsePromptFile(file, data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile(%s): %v", file, err)
	}
	out, err := RenderPromptTemplateWithFragments(prompt.Name, prompt.Content, ctx, cel.BuildTemplateFuncMap(ctx), CurrentFragments())
	if err != nil {
		t.Fatalf("RenderPromptTemplateWithFragments(%s): %v", file, err)
	}
	return prompt.Name, out
}

func linkedWorkRegressionContext() *cel.PromptEnabledContext {
	return &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "session-baseline",
			BeadsIssue:    "mitto-baseline",
			HasBeadsIssue: true,
		},
		Args: map[string]string{"IssueID": "mitto-baseline"},
	}
}

func TestBuiltinStartWorkExpandedLinkedLeafSizeBudget(t *testing.T) {
	_, rendered := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", linkedWorkRegressionContext())
	gotBytes := len([]byte(rendered))
	if gotBytes > startWorkLinkedLeafBudgetBytes {
		t.Fatalf("expanded linked-leaf Start work is %d UTF-8 bytes; budget is %d", gotBytes, startWorkLinkedLeafBudgetBytes)
	}
	// The shared diagnostic lifecycle adds authority/retry safeguards. Keep the
	// existing 14 KB cap and require at least 30% reduction from the old driver.
	if gotBytes*100 > startWorkLinkedLeafBaselineBytes*70 {
		t.Fatalf("expanded linked-leaf Start work is %d UTF-8 bytes; must be at least 30%% smaller than baseline %d", gotBytes, startWorkLinkedLeafBaselineBytes)
	}
	t.Logf("expanded linked-leaf Start work: %d UTF-8 bytes, %d Unicode characters (baseline %d bytes; reduction %.1f%%)", gotBytes, utf8.RuneCountInString(rendered), startWorkLinkedLeafBaselineBytes, 100*(1-float64(gotBytes)/startWorkLinkedLeafBaselineBytes))
}

func TestBuiltinStartWorkRetainedFreshAndRepeatedDispatchesRemainSelfContained(t *testing.T) {
	retained := linkedWorkRegressionContext()
	retained.Session.IsLoop = true
	retained.Iteration = cel.IterationContext{Number: 1, Max: 30, IsLoop: true, IsUninterrupted: true}
	_, retainedOutput := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", retained)

	// A reset/fresh scheduled run has no uninterrupted-context marker. Start work
	// intentionally renders the same complete router instead of assuming prior
	// history still contains its safety instructions.
	fresh := linkedWorkRegressionContext()
	fresh.Session.IsLoop = true
	fresh.Iteration = cel.IterationContext{Number: 0, Max: 30, IsLoop: true, IsFirst: true}
	_, freshOutput := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", fresh)
	if retainedOutput != freshOutput {
		t.Fatal("Start work rendered different instructions for retained and fresh scheduled contexts")
	}

	_, repeatedOutput := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", retained)
	if repeatedOutput != retainedOutput {
		t.Fatal("repeated Start work dispatch omitted or changed the self-contained router")
	}
	for _, contract := range []string{
		"bd show mitto-baseline --long --json",
		"bd dep tree mitto-baseline",
		"bd show mitto-baseline --children --json",
		"bd comments mitto-baseline",
		"stop before linking/claiming/dispatching",
		"mitto_ui_options",
		"**On timeout** (`timed_out: true`): the user did not answer",
		`An explicit decline, "Not now" or free-text feedback is an answer, not a timeout`,
		"cancellation is not a timeout",
		"On successful approved/timed-out dispatch, send the selected named phase",
		"queue named **Start work** with that same ID",
	} {
		if !strings.Contains(strings.Join(strings.Fields(retainedOutput), " "), strings.Join(strings.Fields(contract), " ")) {
			t.Errorf("rendered router missing required repeat-safe contract %q", contract)
		}
	}
}

func TestBuiltinStartWorkFeatureSampleProgressionAndMultiPhasePayload(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	_, driver := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	const route = "| feature/task/chore | `verified`; else `tested`; else `implemented`; else `planned`; else `implementation-refined`; else | Done; Feature — review phase; Feature — test phase; Feature — implement phase; Feature — plan phase (label-only); Feature — plan phase |"
	normalizedDriver := strings.Join(strings.Fields(driver), " ")
	if !strings.Contains(normalizedDriver, strings.Join(strings.Fields(route), " ")) {
		t.Fatal("rendered Start work route no longer encodes the expected feature phase precedence")
	}

	// This fixed progression exercises the same durable label sequence that
	// Start work reads again on each dispatch: empty -> planned -> implemented
	// -> tested -> verified. Phase prompt bodies are unchanged by this change;
	// rendering them here makes their contribution to the sample total visible.
	phaseFiles := []string{
		"beads-issues/feature-phase-plan.prompt.yaml",
		"beads-issues/feature-phase-implement.prompt.yaml",
		"beads-issues/feature-phase-test.prompt.yaml",
		"beads-issues/feature-phase-review.prompt.yaml",
	}
	var phaseBytes int
	for _, file := range phaseFiles {
		_, rendered := renderBuiltinPromptForWorkRegression(t, file, linkedWorkRegressionContext())
		if !strings.Contains(rendered, "bd show mitto-baseline --long --json") {
			t.Errorf("phase %s must reload durable bead state", file)
		}
		if !strings.Contains(rendered, "bd comments mitto-baseline --json | jq '.[-10:]'") {
			t.Errorf("phase %s must reload recent comments", file)
		}
		phaseBytes += len([]byte(rendered))
		t.Logf("expanded unchanged phase %s: %d UTF-8 bytes", filepath.Base(file), len([]byte(rendered)))
	}

	const driverRuns = 4
	baselineTotal := driverRuns*startWorkLinkedLeafBaselineBytes + phaseBytes
	revisedTotal := driverRuns*len([]byte(driver)) + phaseBytes
	t.Logf("four-dispatch sample aggregate (driver + plan/implement/test/review): baseline %d bytes; revised %d bytes; unchanged phase payloads %d bytes", baselineTotal, revisedTotal, phaseBytes)
	if revisedTotal >= baselineTotal {
		t.Fatal("revised multi-phase sample should reduce payload versus the captured driver baseline")
	}
	for _, contract := range []string{
		"Log `Start work: dispatched <phase prompt name>; next phase after it adds its label.` on the effective bead",
		"Start work: dispatched <phase prompt name>; next phase after it adds its label.",
		"Only an explicit **Yes, close it**",
		"Never queue if blocked, declined, dispatch failed, or terminal",
	} {
		if !strings.Contains(normalizedDriver, strings.Join(strings.Fields(contract), " ")) {
			t.Errorf("rendered sample missing durable-state/safety contract %q", contract)
		}
	}
}
