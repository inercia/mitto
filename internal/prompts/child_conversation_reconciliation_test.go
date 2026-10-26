package prompts

import (
	"strings"
	"testing"
)

// A child conversation that finishes a turn without reporting (`agent_idle`)
// used to be left untouched: it had neither "reported back" nor was "still
// working", so the cleanup guidance fell through and the coordinator deferred
// the ticket claiming the child "never reported" — even when the child's final
// messages already carried the report. The cleanup fragment and the acceptance
// coordinator must force an explicit reconciliation instead of deferral.
func TestBuiltinUnreportedChildConversationsAreReconciledBeforeDeferral(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	_, ask := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	normalized := strings.Join(strings.Fields(ask), " ")
	for _, contract := range []string{
		"Leave any child that is still working, or that you did not spawn, untouched",
		"A child that is idle without a report is unresolved, not finished",
		"check `mitto_conversation_get`",
		"never claim it never reported without reading them",
		"`mitto_conversation_send_prompt` a concrete query",
		"`mitto_children_tasks_wait` it; act on the answer",
		"Never defer on `agent_idle`/timeout alone",
	} {
		if !strings.Contains(normalized, contract) {
			t.Errorf("Start work cleanup guidance missing reconciliation contract %q", contract)
		}
	}

	verifyCtx := linkedWorkRegressionContext()
	verifyCtx.Args["ClosePolicy"] = "Verify and close"
	verifyCtx.Args["EffectiveID"] = "mitto-baseline"
	_, verify := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", verifyCtx)
	normalizedVerify := strings.Join(strings.Fields(verify), " ")
	for _, contract := range []string{
		"bead children remain open",
		"A Mitto child conversation that has not reported is not an open bead child by itself",
		"reconcile it (`mitto_conversation_get`, `mitto_conversation_history`",
	} {
		if !strings.Contains(normalizedVerify, contract) {
			t.Errorf("acceptance coordinator missing bead-child/reconciliation boundary %q", contract)
		}
	}
}
