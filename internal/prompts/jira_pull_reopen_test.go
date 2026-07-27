package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestJiraPullAutoReopenHallmarks pins the auto-reopen contract for mitto-5yh:
// when the "JIRA: pull issue" prompt renders, its Step 2 must request the JIRA
// changelog (so `changelog.histories` is available), a Step 6.5 must parse those
// histories to derive T_reopen, and Step 7.5 must include an auto-reopen branch
// that fires `bd reopen` with the "Auto-reopened: JIRA ... reopened at ... after
// local close at ..." reason when T_reopen > T_close.
//
// Before the fix lands, none of these hallmarks are in the rendered prompt body
// so this test fails — that failure IS the reproduction. The fix phase makes
// the three edits described in the mitto-5yh investigation comment and turns
// this test green.
func TestJiraPullAutoReopenHallmarks(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}

	// The MCP branch of Step 2 (which carries the expand="renderedFields,changelog"
	// hallmark) is CEL-gated on `Tools.Names.exists(n, n.matches('(?i)jira'))`,
	// so the test must present a JIRA-named tool in the Tools context, otherwise
	// only the CLI else branch renders and the hallmark check on the MCP wording
	// silently misses.
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "s-test",
			Name:          "Test",
			BeadsIssue:    "mitto-abc",
			HasBeadsIssue: true,
		},
		Args:  map[string]string{"IssueID": "mitto-abc"},
		Tools: cel.NewReachableToolsContext([]string{"jira_get_issue_jira"}),
	}

	var body string
	for _, p := range list {
		if p.Name == "JIRA: pull issue" {
			body = p.Content
			break
		}
	}
	if body == "" {
		t.Fatalf("prompt %q not found in builtin corpus", "JIRA: pull issue")
	}

	funcs := cel.BuildTemplateFuncMap(ctx)
	out, err := RenderPromptTemplate("JIRA: pull issue", body, ctx, funcs)
	if err != nil {
		t.Fatalf("render %q: %v", "JIRA: pull issue", err)
	}

	// Each hallmark corresponds to one of the three edit points described in the
	// mitto-5yh investigation comment. All three must appear in the rendered
	// prompt body once the fix is in place.
	hallmarks := []struct {
		needle string
		reason string
	}{
		{
			needle: `expand="renderedFields,changelog"`,
			reason: "Step 2 must request expand=\"renderedFields,changelog\" so the JIRA changelog is available for the auto-reopen decision (mitto-5yh)",
		},
		{
			needle: "changelog.histories",
			reason: "Step 6.5 must parse changelog.histories to find the latest status transition into a non-terminal status (mitto-5yh)",
		},
		{
			needle: "Auto-reopened: JIRA",
			reason: "Step 7.5 must include an auto-reopen branch that calls `bd reopen` with an \"Auto-reopened: JIRA ...\" reason when T_reopen > T_close (mitto-5yh)",
		},
	}
	for _, h := range hallmarks {
		if !strings.Contains(out, h.needle) {
			t.Errorf("rendered %q missing hallmark %q — %s", "JIRA: pull issue", h.needle, h.reason)
		}
	}
}
