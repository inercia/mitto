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
		`template "beads-issues/shared/unblock-guidance"`,
		"wait for actual resolution",
		"If none are workable, use the blocked handoff above",
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

func TestBuiltinStartWorkLeavesTerminalClosureToOrchestrator(t *testing.T) {
	b, err := os.ReadFile("../../config/prompts/builtin/beads-issues/work.prompt.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	start := strings.Index(body, "  ## Done\n")
	if start < 0 {
		t.Fatal("cannot locate the completed-ticket branch")
	}
	end := strings.Index(body[start:], "  {{- else }}")
	if end <= 0 {
		t.Fatal("cannot locate the completed-ticket branch")
	}
	done := body[start : start+end]
	for _, hallmark := range []string{"`fixed`/`verified` is terminal", "report delivered work from phase comments", "**Do not close the bead**", "the orchestrator closes terminal-label beads", "Never close the parent from"} {
		if !strings.Contains(done, hallmark) {
			t.Errorf("terminal branch missing orchestrator-closure contract %q", hallmark)
		}
	}
	if strings.Contains(done, "mitto_ui_options") || strings.Contains(done, "bd close") {
		t.Error("terminal branch must leave closure to the orchestrator, without asking or closing directly")
	}
}
