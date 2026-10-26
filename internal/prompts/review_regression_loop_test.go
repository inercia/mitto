package prompts

import (
	"strings"
	"testing"
)

// TestBuiltinFeatureReviewRegressesInsteadOfLooping locks the fix for the
// `tested` -> Review -> `tested` infinite loop: a review that cannot verify
// must leave the bead in a state the router can advance from.
func TestBuiltinFeatureReviewRegressesInsteadOfLooping(t *testing.T) {
	_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/feature-phase-review.prompt.yaml", linkedWorkRegressionContext())
	normalized := strings.Join(strings.Fields(body), " ")

	for _, contract := range []string{
		"Never stop with the labels unchanged",
		"an unchanged exit is an infinite review loop, not a retry",
		"bd update mitto-baseline --remove-label tested",
		"bd update mitto-baseline --remove-label tested --remove-label implemented",
		"the next run dispatches the Test phase",
		"the next run dispatches the Implement phase",
		"Never remove `planned`; it is the floor of this state machine",
		"Always leave a decision",
		"Never end on the unchanged `tested` state",
	} {
		if !strings.Contains(normalized, strings.Join(strings.Fields(contract), " ")) {
			t.Errorf("review phase missing regression contract %q", contract)
		}
	}
	if strings.Contains(normalized, "re-dispatch this same review phase") {
		t.Error("review phase must not tell the driver to re-dispatch the same unchanged review")
	}
}

func TestBuiltinFeatureRoutersRefuseUnchangedPhaseRedispatch(t *testing.T) {
	_, work := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", linkedWorkRegressionContext())
	workNormalized := strings.Join(strings.Fields(work), " ")
	for _, contract := range []string{
		"`tested` routes to Review only once per state",
		"Never re-dispatch against unchanged labels",
		"if the latest `Review:` comment says NOT VERIFIED while `tested` remains",
		"remove `tested` and dispatch Test/Implement",
		"require a failed review to regress labels",
	} {
		if !strings.Contains(workNormalized, strings.Join(strings.Fields(contract), " ")) {
			t.Errorf("Start work router missing no-redispatch contract %q", contract)
		}
	}

	_, loop := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-implementing-feature.prompt.yaml", linkedWorkRegressionContext())
	loopNormalized := strings.Join(strings.Fields(loop), " ")
	for _, contract := range []string{
		"regresses the labels",
		"Never dispatch Review while `tested` is unchanged",
		"stall, not a retry",
	} {
		if !strings.Contains(loopNormalized, strings.Join(strings.Fields(contract), " ")) {
			t.Errorf("Loop implementing feature missing no-redispatch contract %q", contract)
		}
	}
}

func TestBuiltinRegressionPhasesReadReviewFindings(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"beads-issues/feature-phase-test.prompt.yaml", "latest NOT VERIFIED `Review:` comment"},
		{"beads-issues/feature-phase-implement.prompt.yaml", "latest NOT VERIFIED `Review:` comment"},
	} {
		_, body := renderBuiltinPromptForWorkRegression(t, tc.file, linkedWorkRegressionContext())
		if !strings.Contains(strings.Join(strings.Fields(body), " "), tc.want) {
			t.Errorf("%s must consume the review gap findings on re-entry", tc.file)
		}
	}
}
