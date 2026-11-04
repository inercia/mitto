package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinStartWorkCloseIfValidatedSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", "beads-issues/work.prompt.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)

	for _, want := range []string{
		"options: [Ask, Leave open, Close if validated]",
		"close immediately when the ticket already has its terminal validation label",
		"verified",
		"fixed",
		"bd close <effective-id>",
		"Validated (",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("work prompt missing %q", want)
		}
	}

	for _, banned := range []string{
		"Verify and close",
		"acceptance-close",
		"Acceptance verification phase",
		"verification pending",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("work prompt still contains retired verifier text %q", banned)
		}
	}
}

func TestBuiltinStartWorkCloseIfValidatedRendering(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Args["ClosePolicy"] = "Close if validated"
	_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)

	for _, want := range []string{
		"ClosePolicy=Close if validated",
		"bd close",
		"Validated (",
		"verified",
		"fixed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Close if validated rendering missing %q", want)
		}
	}
	for _, banned := range []string{
		"acceptance-close",
		"Acceptance verification phase",
		"Close completed",
		"Acceptance criteria are complete",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("Close if validated rendering still contains %q", banned)
		}
	}
}

func TestBuiltinStartWorkAskStillUsesApproval(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Args["ClosePolicy"] = "Ask"
	_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	if !strings.Contains(out, "mitto_ui_options") {
		t.Error("Ask rendering must still request bounded approval")
	}
	if strings.Contains(out, "Validated (") {
		t.Error("Ask rendering must not use the automatic validated close")
	}
}

func TestBuiltinStartWorkLeaveOpenStillLeavesOpen(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Args["ClosePolicy"] = "Leave open"
	_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	if !strings.Contains(out, "Do not ask about closure") {
		t.Error("Leave open rendering must not ask for closure")
	}
	if strings.Contains(out, "Validated (") {
		t.Error("Leave open rendering must not close automatically")
	}
}
