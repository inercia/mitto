package prompts

import (
	"strings"
	"testing"
)

// These scenario contracts verify rendered instructions, not model compliance.
// The executable state-write recipe is exercised separately.
func TestResolveBlockedRecoveryScenarioContracts(t *testing.T) {
	scenarios := []struct {
		name string
		want []string
	}{
		{"running-child-without-report", []string{"Reconcile get/history/reports first", "Running/unconfirmed completion: pending, not unknown", "Idle/timeout is not evidence"}},
		{"terminal-child-without-report", []string{"confirmed terminal failure/disappearance or terminal missing/malformed report", "matching task/target report", "not wait wrapper"}},
		{"first-attempt-versus-unreadable-snapshot", []string{"No prior input: first attempt", "comparison failure: unknown", "Same input: no new diagnosis"}},
		{"infrastructure-recovery", []string{"one explicit caller-authorized retry after evidenced recovery", "no pending child; persist retry used before spawning", "Continue alone is not consent"}},
		{"blocked-ticket-needs-research", []string{"consume before ordinary readiness on next run", "Blocked: scoped read-only research only", "caller approval/authority", "no human/security/access hold or competing worker", "retain blockers/claims", "model_tag: \"Smart\"", "initial_prompt", "Save child/task IDs before waiting"}},
		{"producer-owner-needs-handoff", []string{"one authorized sourced handoff", "delivery is not acceptance, never replace", "proposed|delivered|accepted|completed", "no duplicates"}},
		{"proposal-needs-new-authority", []string{"Missing authority/new scope: seek approval/external owner", "never silently execute", "Preserve phase/closure approval"}},
	}
	for _, file := range []string{"beads-issues/work.prompt.yaml", "misc/continue.prompt.yaml", "beads-issues/loop-until-complete.prompt.yaml"} {
		t.Run(file, func(t *testing.T) {
			ctx := linkedWorkRegressionContext()
			_, out := renderBuiltinPromptForWorkRegression(t, file, ctx)
			for _, scenario := range scenarios {
				t.Run(scenario.name, func(t *testing.T) {
					for _, want := range scenario.want {
						if !strings.Contains(out, want) {
							t.Errorf("missing lifecycle guard %q", want)
						}
					}
				})
			}
		})
	}
}

func TestResolveBlockedActiveOwnerIsNotGlobalStop(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	_, phase := renderBuiltinPromptForWorkRegression(t, "beads-issues/resolve-blocked.prompt.yaml", ctx)
	for _, want := range []string{"Active ownership alone does not prove", "per candidate", "read-only-research | owner-handoff | approval", "disposition starts proposed"} {
		if !strings.Contains(phase, want) {
			t.Errorf("diagnosis missing %q", want)
		}
	}
	for _, file := range []string{"beads-issues/work.prompt.yaml", "misc/continue.prompt.yaml", "beads-issues/loop-until-complete.prompt.yaml"} {
		_, out := renderBuiltinPromptForWorkRegression(t, file, ctx)
		if !strings.Contains(out, "per candidate") {
			t.Errorf("%s must not stop whole epic for one confirmed wait", file)
		}
		if strings.Contains(out, "or another active worker is\n  not uncertainty") {
			t.Errorf("%s retains unconditional ownership short circuit", file)
		}
	}
}
