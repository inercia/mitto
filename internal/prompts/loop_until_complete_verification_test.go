package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func TestLoopUntilCompleteCloseIfValidatedSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", "beads-issues/loop-until-complete.prompt.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	for _, want := range []string{
		"options: [Ask, Leave open, Close if validated]",
		"verified",
		"fixed",
		"no verifier",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("loop prompt missing %q", want)
		}
	}
	for _, bad := range []string{
		"Verify and close",
		"acceptance-close",
		"Acceptance verification phase",
		"verification pending",
	} {
		if strings.Contains(raw, bad) {
			t.Errorf("loop prompt still contains %q", bad)
		}
	}
}

func TestLoopUntilCompleteCloseIfValidatedRendering(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID: "sess-1", Name: "N", HasMessages: true,
			BeadsIssue: "mitto-abc", HasBeadsIssue: true,
		},
		Args: map[string]string{
			"IssueID":        "mitto-abc",
			"ClosePolicy":    "Close if validated",
			"SubmitStrategy": "Commit",
		},
	}
	_, out := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-until-complete.prompt.yaml", ctx)
	for _, want := range []string{
		"ClosePolicy=Close if validated",
		"Close if validated",
		"bd close <target>",
		"Validated (",
		"verified",
		"fixed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered loop prompt missing %q", want)
		}
	}
	for _, bad := range []string{
		"acceptance-close",
		"Acceptance verification phase",
		"verification pending",
		"Verify and close",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("rendered loop prompt still contains %q", bad)
		}
	}
}

func TestLoopUntilCompleteAskAndLeaveOpenRemain(t *testing.T) {
	ask := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "ClosePolicy": "Ask"},
	}
	_, askOut := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-until-complete.prompt.yaml", ask)
	if !strings.Contains(askOut, "mitto_ui_options") {
		t.Error("Ask policy must keep the bounded approval UI")
	}

	leave := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "ClosePolicy": "Leave open"},
	}
	_, leaveOut := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-until-complete.prompt.yaml", leave)
	if !strings.Contains(leaveOut, "left open by policy") {
		t.Error("Leave open policy must keep its no-close outcome")
	}
}
