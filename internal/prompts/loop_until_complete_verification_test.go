package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

func TestLoopUntilCompleteClosePolicySchema(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", "beads-issues/loop-until-complete.prompt.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-until-complete.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, parameter := range prompt.Parameters {
		if parameter.Name == "ClosePolicy" {
			if parameter.Type != "text" || parameter.Default != "Ask" || parameter.Required == nil || *parameter.Required || strings.Join(parameter.Options, ",") != "Ask,Leave open,Verify and close" {
				t.Errorf("ClosePolicy = %#v, want optional Start-work-compatible selector", parameter)
			}
			return
		}
	}
	t.Error("ClosePolicy parameter missing")
}

func TestLoopUntilCompleteSubmitStrategySchema(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	data, err := os.ReadFile(filepath.Join("../../config/prompts/builtin", "beads-issues/loop-until-complete.prompt.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := ParsePromptFile("beads-issues/loop-until-complete.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, parameter := range prompt.Parameters {
		if parameter.Name == "SubmitStrategy" {
			if parameter.Type != "text" || parameter.Default != "Commit" || strings.Join(parameter.Options, ",") != "Commit,Pull Request,None" {
				t.Errorf("SubmitStrategy = %#v, want optional Commit/Pull Request/None selector defaulting to Commit", parameter)
			}
			if parameter.Required != nil && *parameter.Required {
				t.Errorf("SubmitStrategy required = true, want optional")
			}
			return
		}
	}
	t.Error("SubmitStrategy parameter missing")
}

func TestLoopUntilCompleteClosePolicyRendering(t *testing.T) {
	render := func(policy, submitStrategy, linkedIssue, issueArg string, continuation bool) string {
		t.Helper()
		ctx := linkedWorkRegressionContext()
		ctx.Session.BeadsIssue = linkedIssue
		ctx.Session.HasBeadsIssue = linkedIssue != ""
		ctx.Args["IssueID"] = issueArg
		if policy != "" {
			ctx.Args["ClosePolicy"] = policy
		}
		if submitStrategy != "" {
			ctx.Args["SubmitStrategy"] = submitStrategy
		}
		if continuation {
			ctx.Session.IsLoop = true
			ctx.Iteration = cel.IterationContext{Number: 2, Max: 20, IsLoop: true, IsUninterrupted: true}
		}
		_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-until-complete.prompt.yaml", ctx)
		return strings.Join(strings.Fields(body), " ")
	}

	for _, tc := range []struct {
		name, policy string
		verifier     bool
	}{
		{name: "missing defaults Ask", policy: "", verifier: false},
		{name: "Ask", policy: "Ask", verifier: false},
		{name: "Leave open", policy: "Leave open", verifier: false},
		{name: "Verify and close", policy: "Verify and close", verifier: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, continuation := range []bool{false, true} {
				body := strings.ReplaceAll(render(tc.policy, "", "mitto-epic", "mitto-ignored", continuation), "**", "")
				if !strings.Contains(body, "2. Select the next bounded phase") {
					t.Error("universal item 2 missing or blank")
				}
				if got := strings.Contains(body, "Acceptance verification phase"); got != tc.verifier {
					t.Errorf("continuation=%v verifier rendered=%v, want %v", continuation, got, tc.verifier)
				}
				if tc.verifier {
					for _, want := range []string{
						"<effective-id> := <target>",
						"You own verification and closure of <target> only",
						"fresh sibling owned by this orchestrator, never a grandchild",
						"runnable verification work, not a deferral",
						"do not retry the same unchanged attempt",
						"read the latest `Acceptance verification:` report and use its failed criteria and evidence as the repair spec",
						"Do not switch to `Ask` merely because verification failed",
						"On FAIL, record a concise durable `Iterate run: verification failed because <failed criteria and reasons>` comment",
						"The next run reads that report and dispatches one bounded repair scoped to the failed criteria",
						"only a later run verifies the repaired deliverable again",
						"the repair increment is exactly its failed criteria",
						"The Investigation Report remains context, not a substitute for the failed criteria",
						"supersedes its older pending entry",
						"not the independent acceptance checks this verifier is about to perform",
						"still-running verifier awaiting reconciliation",
						"one new child per run",
					} {
						if !strings.Contains(body, want) {
							t.Errorf("missing verification contract %q", want)
						}
					}
				} else if strings.Contains(body, "fresh sibling owned") {
					t.Error("non-verification policies must not render a verifier")
				}
			}
		})
	}

	t.Run("Ask requires exact bounded consent", func(t *testing.T) {
		body := render("Ask", "", "mitto-linked", "", true)
		for _, want := range []string{
			"mitto_ui_options(self_id: \"session-baseline\"", "question: \"Acceptance criteria are complete. Close <target>?\"",
			"\"label\":\"Yes\"", "\"label\":\"No\"", "allow_free_text: false", "timeout_seconds: 60",
			"`selected:\"Yes\"` and `index:0`, with `timed_out` **absent or false**", "`free_text` **absent or empty**",
			"matching label at another index", "is not consent",
			"Do not ask again or rerun implementation for this unchanged target",
			"additional narrow exception", "After every response** — before closing *or* recording a completed-but-open result",
			"reload the ticket, acceptance criteria, children, blockers, active claims, and workspace evidence", "compare all of it to the pre-approval snapshot",
			"do **not** write a completed skip marker", "reevaluation required; ClosePolicy=Ask", "supersedes any older completed entry",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("Ask contract missing %q", want)
			}
		}
	})
	t.Run("Ask distinguishes unchanged decline from stale and closure failures", func(t *testing.T) {
		body := render("Ask", "", "mitto-linked", "", false)
		for _, want := range []string{
			"an explicit No, timeout, or cancellation is not consent but may record `Iterate run: completed; ClosePolicy=Ask",
			"If the close command fails or closure cannot be confirmed, do **not** write a completed marker",
			"reason=close command failed or unconfirmed", "next phase=inspect closure failure", "do not automatically re-prompt",
			"For a malformed response or tool error, record reevaluation required (not completed)",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("Ask outcome contract missing %q", want)
			}
		}
		for _, invalid := range []string{"title: \"Close completed", "message: \"Acceptance criteria", "\"value\":\"yes\"", "\"value\":\"no\""} {
			if strings.Contains(body, invalid) {
				t.Errorf("Ask options must not render invalid API argument %q", invalid)
			}
		}
	})
	t.Run("scheduled initial Ask permits only the closure exception", func(t *testing.T) {
		ctx := linkedWorkRegressionContext()
		ctx.Session.BeadsIssue = "mitto-linked"
		ctx.Session.HasBeadsIssue = true
		ctx.Session.IsLoop = true
		ctx.Iteration = cel.IterationContext{Number: 1, Max: 20, IsLoop: true}
		ctx.Args["ClosePolicy"] = "Ask"
		_, rendered := renderBuiltinPromptForWorkRegression(t, "beads-issues/loop-until-complete.prompt.yaml", ctx)
		body := strings.Join(strings.Fields(rendered), " ")
		for _, want := range []string{
			"one narrow final-closure exception", "bounded explicit Yes/No `mitto_ui_options` approval",
			"never a work or design decision", "never ask the user for work decisions",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("scheduled-initial Ask contract missing %q", want)
			}
		}
	})
	t.Run("Leave open has no approval or close action", func(t *testing.T) {
		body := render("Leave open", "", "mitto-linked", "", false)
		if strings.Contains(body, "mitto_ui_options(") || strings.Contains(body, "bd close <target>") {
			t.Error("Leave open must not render approval or closure")
		}
		for _, want := range []string{"closure outcome=left open by policy", "exclude this unchanged completed unit from runnable work", "Re-evaluate only after changed requirements"} {
			if !strings.Contains(body, want) {
				t.Errorf("Leave open contract missing %q", want)
			}
		}
	})
	t.Run("invalid policy stops without bead mutation", func(t *testing.T) {
		body := render("Bogus", "", "mitto-linked", "", false)
		for _, want := range []string{"ClosePolicy=Bogus", "report the invalid value", "do not claim, comment, defer, dispatch, verify, close"} {
			if !strings.Contains(body, want) {
				t.Errorf("invalid-policy contract missing %q", want)
			}
		}
	})
	t.Run("stop condition skips completed-open units without completing epics", func(t *testing.T) {
		body := render("Leave open", "", "mitto-linked", "", false)
		for _, want := range []string{
			"completed-but-open unchanged unit under its", "A completed-but-open child is skipped, but its open epic parent is **not** thereby done",
			"continue the epic with other runnable children and stop only when none remain",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("stop-condition contract missing %q", want)
			}
		}
	})

	t.Run("linked issue takes precedence over IssueID", func(t *testing.T) {
		body := render("Verify and close", "Commit", "mitto-linked", "mitto-arg", false)
		if !strings.Contains(body, "target bead** for this run is `mitto-linked`") || strings.Contains(body, "target bead** for this run is `mitto-arg`") {
			t.Error("linked target must win over IssueID")
		}
	})
	t.Run("IssueID is fallback when unlinked", func(t *testing.T) {
		body := render("Verify and close", "Commit", "", "mitto-arg", false)
		if !strings.Contains(body, "target bead** for this run is `mitto-arg`") {
			t.Error("IssueID fallback target missing")
		}
	})
	for _, tc := range []struct{ strategy, want string }{
		{strategy: "None", want: "caller's Commit=false policy remains in force"},
		{strategy: "Commit", want: "caller's Commit=true policy remains in force"},
		{strategy: "", want: "caller's Commit=true policy remains in force"},
	} {
		t.Run("SubmitStrategy "+tc.strategy, func(t *testing.T) {
			if body := render("Verify and close", tc.strategy, "mitto-linked", "", false); !strings.Contains(body, tc.want) {
				t.Errorf("missing %q", tc.want)
			}
		})
	}
}

func TestAcceptanceCloseTerminalLabelDefaultsAndLoopOverride(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	ctx := linkedWorkRegressionContext()
	defaultBody, err := RenderPromptTemplateWithFragments("default-acceptance-close", `{{ template "beads-issues/shared/acceptance-close" . }}`, ctx, cel.BuildTemplateFuncMap(ctx), CurrentFragments())
	if err != nil {
		t.Fatalf("render default fragment: %v", err)
	}
	if !strings.Contains(defaultBody, "Confirm the terminal label") || !strings.Contains(defaultBody, "and completion labels") {
		t.Error("default acceptance-close invocation must preserve terminal-label gates")
	}
	override := `{{ $args := dict "Commit" "false" "RequireTerminalLabel" "false" "EffectiveID" "mitto-child" }}{{ $ctx := dict "Session" .Session "Args" $args }}{{ template "beads-issues/shared/acceptance-close" $ctx }}`
	overrideBody, err := RenderPromptTemplateWithFragments("loop-acceptance-close", override, ctx, cel.BuildTemplateFuncMap(ctx), CurrentFragments())
	if err != nil {
		t.Fatalf("render loop override: %v", err)
	}
	if strings.Contains(overrideBody, "Confirm the terminal label") || strings.Contains(overrideBody, "and completion labels") || !strings.Contains(overrideBody, "mitto-child only") {
		t.Error("loop override must scope the effective child and opt out of terminal-label gates only")
	}
}
