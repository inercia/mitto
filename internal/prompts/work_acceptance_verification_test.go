package prompts

import (
	"os"
	"strings"
	"testing"
)

func TestBuiltinAcceptanceVerificationSchema(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	list, err := LoadPromptsFromDir("../../config/prompts/builtin")
	if err != nil {
		t.Fatal(err)
	}
	var found *PromptFile
	for _, p := range list {
		if p.Name == "Acceptance verification phase" {
			if found != nil {
				t.Fatal("duplicate acceptance verification prompt name")
			}
			found = p
		}
	}
	if found == nil {
		t.Fatal("acceptance verification phase missing from builtin corpus")
	}
	if found.Menus != "internal" || found.Group != "Tasks" {
		t.Errorf("unexpected verification menu/group: %q/%q", found.Menus, found.Group)
	}
	if found.Target != nil || found.Singleton || found.Loop != nil {
		t.Fatal("independent verifier must always create a fresh, non-looping conversation")
	}
	if len(found.PreferredModels) != 1 || found.PreferredModels[0].ModelTag != "Reasoning" {
		t.Errorf("verifier model preferences = %+v, want Reasoning", found.PreferredModels)
	}
	want := map[string]string{
		"IssueID": "beadsId", "AttemptID": "text", "ExpectedState": "text", "SnapshotRecipe": "text",
	}
	if len(found.Parameters) != len(want) {
		t.Fatalf("parameters = %+v, want exact verification input contract", found.Parameters)
	}
	for _, param := range found.Parameters {
		if want[param.Name] != param.Type {
			t.Errorf("unexpected verification parameter: %+v", param)
		}
	}
}

func TestBuiltinStartWorkIndependentAcceptanceClosure(t *testing.T) {
	for _, child := range []bool{false, true} {
		for _, commit := range []string{"true", "false"} {
			ctx := linkedWorkRegressionContext()
			ctx.Session.IsChild, ctx.Session.ParentID = child, "parent-coordinator"
			ctx.Args["ClosePolicy"], ctx.Args["Commit"] = "Verify and close", commit
			_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
			normalized := strings.Join(strings.Fields(body), " ")
			for _, contract := range []string{
				`prompt_name: "Acceptance verification phase"`,
				`model_tag: "Reasoning"`,
				`"IssueID": "<effective-id>", "AttemptID": "<attempt-id>", "ExpectedState": "<state>", "SnapshotRecipe":`,
				`task_id: "<attempt-id>", timeout_seconds: 600`,
				"fixed`/`verified` alone never authorizes closure",
				"one bounded verification attempt, not a repair loop",
				"actual designated child's report with matching issue ID, attempt ID and state",
				"EVERY criterion PASS with concrete evidence",
				"Reload the durable `Acceptance verification:` report",
				"synthetic `stuck`, `not_running`",
				"children remain open, or human/external sign-off is required",
				"make no commits after the snapshot",
				"untracked paths AND file contents",
				"recapture the state",
				"deferral/human gates",
				"not other comments that can change requirements/evidence",
				"Any change/unknown invalidates this attempt",
				"Only an unchanged PASS permits:",
				"Check command success and reload status",
				"Never push or close another ticket",
				"do not delete a live verifier",
				"Children hand acceptance coordination back to this parent",
				"A handoff is NOT a PASS",
				"Otherwise keep it and report the incomplete outcome",
			} {
				if !strings.Contains(normalized, contract) {
					t.Errorf("child=%v Commit=%s: missing acceptance contract %q", child, commit, contract)
				}
			}
			wantOwner := "session-baseline"
			if child {
				wantOwner = "parent-coordinator"
				if !strings.Contains(body, `conversation_id: "parent-coordinator"`) ||
					!strings.Contains(normalized, "not for this child to self-verify") ||
					!strings.Contains(normalized, "not a request to run Start work") {
					t.Error("child invocation must hand the explicit target procedure to its parent")
				}
			} else if !strings.Contains(normalized, "Execute the coordinator section below in this conversation") {
				t.Error("top-level invocation must own fresh verification")
			}
			if !strings.Contains(body, "Use `"+wantOwner+"` as your `self_id`") {
				t.Error("wrong closure coordinator identity")
			}
			if !strings.Contains(body, "Commit="+commit+" policy remains in force") {
				t.Error("delegated coordinator must preserve the worker's commit policy")
			}
			if strings.Contains(body, `question: "Close completed`) ||
				strings.Contains(body, "The task is complete. Offer to tidy up") ||
				strings.Contains(body, "Clean up any child conversations that have reported back") ||
				strings.Contains(body, "Invalid ClosePolicy") {
				t.Error("acceptance mode must not inherit Ask or unconditional cleanup instructions")
			}
			if commit == "true" && strings.Index(body, "commit attributable leftovers") > strings.Index(body, "## Independent acceptance closure") {
				t.Error("commits must precede acceptance capture")
			}
		}
	}
	for _, policy := range []string{"", "Ask", "Leave open"} {
		ctx := linkedWorkRegressionContext()
		ctx.Args["ClosePolicy"] = policy
		_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
		if strings.Contains(body, "Acceptance closure coordinator") || strings.Contains(body, "Acceptance verification phase") {
			t.Errorf("policy %q must not launch independent acceptance verification", policy)
		}
	}
	ctx := linkedWorkRegressionContext()
	ctx.Args = map[string]string{"ClosePolicy": "Verify and close"}
	ctx.Session.BeadsIssue, ctx.Session.HasBeadsIssue = "", false
	_, inline := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	if strings.Contains(inline, "Acceptance closure coordinator") || strings.Contains(inline, "ticket's acceptance PASS") {
		t.Error("no-target inline work must not require a ticket acceptance phase")
	}
}

func TestBuiltinAcceptanceVerifierRequiresExactInputsAndEvidence(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Session.IsChild, ctx.Session.ParentID = true, "coordinator"
	ctx.Args = map[string]string{
		"IssueID": "mitto-acceptance-target", "AttemptID": "acceptance-attempt",
		"ExpectedState":  `{"workspace":"/workspace","head":"abc","ticket_sha256":"123","tree_sha256":"456"}`,
		"SnapshotRecipe": "safe snapshot recipe",
	}
	_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/acceptance-verification.prompt.yaml", ctx)
	for _, contract := range []string{
		"bd show mitto-acceptance-target --long --json",
		"bd comments mitto-acceptance-target",
		"Use the explicit IssueID above, never a different linked ticket",
		"Enumerate EVERY explicit acceptance criterion",
		"An empty checklist can never PASS",
		"A disproved criterion => FAIL",
		"unverifiable criterion => UNKNOWN",
		"PASS requires ALL criteria PASS",
		"Do not fix files, format, stage, commit, push",
		"Only append the report comment below",
		"unsafe or unreproducible",
		"untracked paths AND contents",
		"any content/requirements/hierarchy change => UNKNOWN",
		`"attempt_id": "acceptance-attempt"`,
		`"issue_id": "mitto-acceptance-target"`,
		`task_id: "acceptance-attempt", status: "completed"`,
		"Acceptance verification: <JSON report>",
		"If recording fails, do not report PASS",
		"details <=16000 bytes",
		"never omit criteria to fit",
		"Do not close the ticket or self-delete",
	} {
		if !strings.Contains(body, contract) {
			t.Errorf("missing verifier contract %q", contract)
		}
	}
	if strings.Contains(body, "bd show mitto-baseline") {
		t.Error("verifier must not substitute its linked ticket for the explicit target")
	}
	if strings.Index(body, "Append `Acceptance verification:") > strings.Index(body, "Then call `mitto_children_tasks_report") {
		t.Error("durable acceptance report must precede the report transport")
	}
	for _, missing := range []string{"IssueID", "AttemptID", "ExpectedState", "SnapshotRecipe", "child"} {
		t.Run("missing-"+missing, func(t *testing.T) {
			saved := ctx.Args[missing]
			delete(ctx.Args, missing)
			ctx.Session.IsChild = missing != "child"
			_, invalid := renderBuiltinPromptForWorkRegression(t, "beads-issues/acceptance-verification.prompt.yaml", ctx)
			ctx.Args[missing] = saved
			ctx.Session.IsChild = true
			if !strings.Contains(invalid, "report UNKNOWN with the missing input") ||
				strings.Contains(invalid, "bd show ") || strings.Contains(invalid, `"verdict":`) {
				t.Error("missing inputs must not render verification or a successful report")
			}
		})
	}
}

func TestBuiltinStartWorkAcceptanceCallerArguments(t *testing.T) {
	ctx := linkedWorkRegressionContext()
	ctx.Args["Commit"], ctx.Args["ClosePolicy"] = "false", "Verify and close"
	_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	for _, id := range []string{"<effective-id>", "<child-id>"} {
		if !strings.Contains(body, `{ "IssueID": "`+id+`", "Commit": "false", "ClosePolicy": "Verify and close" }`) {
			t.Errorf("Start work caller dropped ownership arguments for %s", id)
		}
	}
	// The generic post-task hook already forwards every picked prompt argument;
	// unlike Start work's parallel workers it runs after the task was closed.
	data, err := os.ReadFile("../../config/prompts/builtin/beads-issues/shared/loop-processing-post-task-hook.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{`ArgsMap "PostIterationPrompt_Args"`, `range $k, $v := $hookArgs`, `prompt_name: "{{ .Args.PostIterationPrompt }}"`} {
		if !strings.Contains(string(data), contract) {
			t.Errorf("generic caller must preserve picked prompt arguments: %q", contract)
		}
	}
	// Keep the opt-in route bounded without relaxing the existing default budget.
	if len(body) > 20000 {
		t.Fatalf("acceptance-mode router is %d bytes; budget is 20000", len(body))
	}
	t.Logf("acceptance-mode router: %d bytes", len(body))

}
