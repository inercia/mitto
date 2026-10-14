package prompts

import (
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
		"## Step 1a — Stop and explain if the work is blocked",
		"bd comments {{ $target }}",
		"**do not claim the bead and do not continue to Steps 2–4**",
		`template "beads-issues/shared/unblock-guidance"`,
		"Stop after this handoff and wait for the user",
		"children**, follow Step 1a's blocked-work explanation",
		"## Step 3 — Pick and propose the next phase from the live labels",
		"**Propose and confirm the selected next phase.**",
		"mitto_ui_options(self_id: \"{{ .Session.ID }}\", question: \"Proceed to <phase>?\"",
		"If `mitto_ui_options` returns `timed_out: true`",
		"say **\"continue\"**",
		"prompt_name: \"Start work\"",
		"Queue this only after the user approved and the phase prompt was dispatched",
		"Do not queue a follow-up after",
		"a timeout, a declined proposal, blocked work",
	} {
		if !strings.Contains(body, hallmark) {
			t.Errorf("Start work prompt missing blocked-work hallmark %q", hallmark)
		}
	}

	guard := strings.Index(body, "## Step 1a — Stop and explain if the work is blocked")
	claim := strings.Index(body, "## Step 2 — Claim the bead")
	if guard < 0 || claim < 0 || guard >= claim {
		t.Errorf("blocked-work guard must appear before claim step: guard=%d claim=%d", guard, claim)
	}

	proposal := strings.Index(body, "**Propose and confirm the selected next phase.**")
	phaseDispatch := strings.Index(body, `prompt_name: "<phase prompt name above>"`)
	continuation := strings.Index(body, `prompt_name: "Start work"`)
	if proposal < 0 || phaseDispatch < 0 || continuation < 0 || proposal >= phaseDispatch || phaseDispatch >= continuation {
		t.Errorf("next-phase approval, phase dispatch, and continuation must appear in order: proposal=%d dispatch=%d continuation=%d", proposal, phaseDispatch, continuation)
	}
}
