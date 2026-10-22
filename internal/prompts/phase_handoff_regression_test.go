package prompts

import (
	"strings"
	"testing"
)

func TestBuiltinRoutersPreserveRestrictedRolesAndEvidenceHandoffs(t *testing.T) {
	for _, file := range []string{"beads-issues/work.prompt.yaml", "beads-issues/loop-until-complete.prompt.yaml"} {
		ctx := linkedWorkRegressionContext()
		ctx.Session.IsChild = true
		_, body := renderBuiltinPromptForWorkRegression(t, file, ctx)
		normalized := strings.Join(strings.Fields(body), " ")
		for _, contract := range []string{"target, permissions and stop condition", "Report the mismatch and stop"} {
			if !strings.Contains(strings.ToLower(normalized), strings.ToLower(contract)) {
				t.Errorf("%s missing restricted-role boundary %q", file, contract)
			}
		}
		if strings.Index(normalized, "target, permissions and stop condition") > strings.Index(normalized, "bd update") {
			t.Errorf("%s must check role before mutating ticket state", file)
		}
	}
	ctx := linkedWorkRegressionContext()
	_, router := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	for _, contract := range []string{`"AdditionalInstructions": "<handoff>"`, "user clarifications verbatim with source", "every phase dispatch"} {
		if !strings.Contains(router, contract) {
			t.Errorf("Start work missing sourced phase handoff %q", contract)
		}
	}
	ctx.Args["SubmitStrategy"] = "Pull Request"
	_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-until-complete.prompt.yaml", ctx)
	for _, contract := range []string{
		"local commits only here", "evidence handoff", "every** authoritative acceptance criterion",
		"do not waive it or push to satisfy it", "warning otherwise",
	} {
		if !strings.Contains(body, contract) {
			t.Errorf("increment loop missing bounded phase contract %q", contract)
		}
	}
	if strings.Contains(body, `style: "success"`) {
		t.Error("loop stop must not unconditionally report success")
	}
}

func TestBuiltinFeatureImplementationPreservesUncommittedHandoff(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Args["SubmitStrategy"] = "None"
	_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/feature-phase-implement.prompt.yaml", ctx)
	normalized := strings.Join(strings.Fields(body), " ")
	for _, contract := range []string{
		"a new turn does not itself discard uncommitted files",
		"Honor `SubmitStrategy` rather than committing",
		"Test, review, acceptance closure and any authorized PR publication belong to their own steps",
	} {
		if !strings.Contains(normalized, contract) {
			t.Errorf("missing implementation handoff contract %q", contract)
		}
	}
	if strings.Contains(body, "would be lost between iterations") {
		t.Error("implementation must not invent uncommitted data loss as a reason to commit")
	}
}
