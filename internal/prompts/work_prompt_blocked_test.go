package prompts

import (
	"os"
	"strings"
	"testing"
)

func TestBuiltinStartWorkExplainsBlockedBeadAndQueuesApprovedContinuation(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"

	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, fragErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(%s): %v", builtinDir, err)
	}
	if len(fragErrs) != 0 {
		t.Fatalf("builtin fragment load errors: %+v", fragErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(%s): %v", builtinDir, err)
	}

	var body string
	for _, prompt := range list {
		if prompt.Name == "Start work" {
			body = prompt.Content
			break
		}
	}
	if body == "" {
		t.Fatal("builtin corpus is missing the Start work prompt")
	}

	for _, hallmark := range []string{
		"## Live state and blockers",
		"bd comments {{ $target }}",
		"stop before linking/claiming/dispatching",
		`template "beads-issues/shared/blocked-diagnosis"`,
		`template "beads-issues/shared/resolve-blocked-dispatch"`,
		"Recheck blockers. Evidence/authority required for stale-state fixes",
		"or scoped prerequisites; else ask",
		"Preserve dependencies/holds/scope/claims",
		"Blocked: stop before linking/claiming/dispatching",
		"Resolved: reload/resume",
		"blocked handoff above",
		"## Route exactly one phase from the live labels",
		"Before dispatch, propose it with `mitto_ui_options`",
		"60-second timeout",
		`{{ template "_shared/timeout-default-continue" . }}`,
		"queue named **Start work** with that same ID",
		"queue if blocked, declined, dispatch failed, or terminal",
	} {
		if !strings.Contains(body, hallmark) {
			t.Errorf("Start work prompt missing blocked-work hallmark %q", hallmark)
		}
	}
	if strings.Contains(body, "never clear blockers") || strings.Contains(body, "wait for actual resolution") {
		t.Error("Start work must not impose a blanket blocker-clearing prohibition or passive wait")
	}

	guard := strings.Index(body, "## Live state and blockers")
	claim := strings.Index(body, "bd update <effective-id> --claim")
	if guard < 0 || claim < 0 || guard >= claim {
		t.Errorf("blocked-work guard must appear before claim step: guard=%d claim=%d", guard, claim)
	}

	proposal := strings.Index(body, "Before dispatch, propose it with `mitto_ui_options`")
	phaseDispatch := strings.Index(body, "On successful approved/timed-out dispatch, send the selected named phase")
	continuation := strings.Index(body, "then queue named **Start work**")
	if proposal < 0 || phaseDispatch < 0 || continuation < 0 || proposal >= phaseDispatch || phaseDispatch >= continuation {
		t.Errorf("next-phase approval, phase dispatch, and continuation must appear in order: proposal=%d dispatch=%d continuation=%d", proposal, phaseDispatch, continuation)
	}
}

func TestRoutineGatesUseTimeoutDefaultContinueFragment(t *testing.T) {
	for _, f := range []string{"beads-issues/work", "child/continue-new", "child/continue-existing", "child/create-minions"} {
		b, err := os.ReadFile("../../config/prompts/builtin/" + f + ".prompt.yaml")
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(b), `_shared/timeout-default-continue`) {
			t.Errorf("%s must reference the timeout-default-continue fragment", f)
		}
		if !strings.Contains(string(b), "timeout_seconds: 60") && !strings.Contains(string(b), "60-second timeout") {
			t.Errorf("%s must set routine UI option timeout_seconds to 60", f)
		}
		if strings.Contains(string(b), "do **not** dispatch or queue anything") {
			t.Errorf("%s still has the old abort-on-timeout rule", f)
		}
	}
}

func TestBuiltinStartWorkOwnershipPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, commit, close, submit string
		ask                         bool
	}{
		{"omitted", "", "", "Commit", true},
		{"standalone", "true", "Ask", "Commit", true},
		{"caller-owned", "false", "Leave open", "None", false},
		{"phase-commits", "true", "Leave open", "Commit", false},
		{"uncommitted-close", "false", "Ask", "None", true},
		{"verified-commits", "true", "Close if validated", "Commit", false},
		{"verified-uncommitted", "false", "Close if validated", "None", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := linkedWorkRegressionContext()
			ctx.Args["Commit"], ctx.Args["ClosePolicy"] = tc.commit, tc.close
			_, body := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
			closePolicy := tc.close
			if closePolicy == "" {
				closePolicy = "Ask"
			}
			commit := "true"
			if tc.commit == "false" {
				commit = "false"
			}
			for _, contract := range []string{
				`"SubmitStrategy": "` + tc.submit + `"`,
				`{ "IssueID": "<effective-id>", "Commit": "` + commit + `", "ClosePolicy": "` + closePolicy + `" }`,
				`{ "IssueID": "<child-id>", "Commit": "` + commit + `", "ClosePolicy": "` + closePolicy + `" }`,
				"mirror these arguments into `loop_arguments`",
				"Never close the parent from",
				"unverified hierarchy",
				"Never push automatically",
			} {
				if !strings.Contains(body, contract) {
					t.Errorf("missing ownership contract %q", contract)
				}
			}
			question := `question: "Close completed <effective-id>?"`
			if strings.Contains(body, question) != tc.ask {
				t.Errorf("close question present = %v, want %v", strings.Contains(body, question), tc.ask)
			}
			if tc.ask {
				for _, contract := range []string{"timeout_seconds: 60", "Only an explicit **Yes, close it**", "Timeout (`timed_out: true`), cancellation, decline, free text, or tool errors leave it open"} {
					if !strings.Contains(body, contract) {
						t.Errorf("missing close approval contract %q", contract)
					}
				}
			} else if tc.close == "Leave open" && !strings.Contains(body, "Do not ask about closure or run `bd close`") {
				t.Error("caller-owned closure must explicitly leave the ticket open")
			}
			if tc.commit == "false" && strings.Contains(body, "commit attributable leftovers") {
				t.Error("Commit=false must not instruct finalization to commit leftovers")
			}
			if tc.commit != "false" && !strings.Contains(body, "commit attributable leftovers") {
				t.Error("Commit=true must finalize uncommitted task work")
			}
			ctx.Session.BeadsIssue = ""
			ctx.Session.HasBeadsIssue = false
			delete(ctx.Args, "IssueID")
			_, inline := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
			if strings.Contains(inline, question) || strings.Contains(inline, "bd close") {
				t.Error("inline work must not attempt ticket closure")
			}
			if strings.Contains(inline, "After checks pass, commit task changes.") != (tc.commit != "false") {
				t.Error("inline work must honor Commit")
			}
		})
	}
	ctx := linkedWorkRegressionContext()
	ctx.Args["ClosePolicy"] = "invalid"
	_, invalid := renderBuiltinPromptForWorkRegression(t, "beads-issues/work.prompt.yaml", ctx)
	if !strings.Contains(invalid, "Invalid ClosePolicy: report and stop.") || strings.Contains(invalid, `question: "Close completed`) {
		t.Error("invalid closure policy must stop, not request closure")
	}
}

func TestBuiltinStartWorkOwnershipParameterSchema(t *testing.T) {
	installBuiltinFragmentsForTest(t)
	list, err := LoadPromptsFromDir("../../config/prompts/builtin")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Name != "Start work" {
			continue
		}
		found := 0
		for _, param := range p.Parameters {
			switch param.Name {
			case "Commit":
				found++
				if param.Type != "boolean" || param.Default != "true" {
					t.Errorf("unexpected Commit schema: %+v", param)
				}
			case "ClosePolicy":
				found++
				if param.Type != "text" || param.Default != "Ask" || strings.Join(param.Options, ",") != "Ask,Leave open,Close if validated" {
					t.Errorf("unexpected ClosePolicy schema: %+v", param)
				}
			}
		}
		if found != 2 {
			t.Fatalf("ownership parameter count = %d, want 2", found)
		}
		return
	}
	t.Fatal("Start work not found")
}
