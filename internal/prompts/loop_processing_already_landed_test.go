package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestLoopProcessing_AlreadyLandedRequiresPostReopenCommit reproduces
// mitto-2bbw. A historical resolving commit must not self-heal a bead whose
// current open episode began after that commit.
func TestLoopProcessing_AlreadyLandedRequiresPostReopenCommit(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks",
		&cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "orch-1"},
			Args: map[string]string{
				"FixBugs":        "true",
				"WorkOnFeatures": "true",
			},
			Prompts: cel.PromptsContext{
				Names:        []string{"Loop fixing bug", "Loop implementing feature"},
				EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
			},
		})

	start := strings.Index(out, "**Already-landed fix detection.**")
	if start < 0 {
		t.Fatal("rendered Already-landed fix detection section not found")
	}
	end := strings.Index(out[start:], "Keep the similarity check")
	if end < 0 {
		t.Fatal("rendered Already-landed fix detection section end not found")
	}
	section := out[start : start+end]

	for _, want := range []string{
		"bd history <id> --json",
		"Only beads that have never been closed are eligible for this shortcut",
		"If history contains any prior closed episode, leave the bead actionable",
		"A newer commit on a reopened bead still completes through the normal driver",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("already-landed detection missing temporal guard %q", want)
		}
	}

	historyAt := strings.Index(section, "bd history <id> --json")
	selfHealAt := strings.Index(section, "exclude AND self-heal")
	if historyAt < 0 || selfHealAt < 0 || historyAt > selfHealAt {
		t.Error("current-open history check must precede the self-heal action")
	}
}

// TestLoopProcessing_TerminalSweepIsMachineryComputed pins that Step 2P's
// terminal-label classification (mitto-2bbw reopen guard) runs in the prompt
// machinery (TerminalBeads template func) rather than as agent-run shell or
// Python. The history/comment verdict logic itself is covered by
// internal/cel TestTerminalBeads_*.
func TestLoopProcessing_TerminalSweepIsMachineryComputed(t *testing.T) {
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks",
		&cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "orch-1"},
			Args: map[string]string{
				"FixBugs":        "true",
				"WorkOnFeatures": "true",
			},
			Prompts: cel.PromptsContext{
				Names:        []string{"Loop fixing bug", "Loop implementing feature"},
				EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
			},
		})

	start := strings.Index(out, "## Step 2P — Prune stale children")
	if start < 0 {
		t.Fatal("rendered Step 2P not found")
	}
	end := strings.Index(out[start:], "### Reap epics whose children are all closed")
	if end < 0 {
		t.Fatal("rendered Step 2P terminal-label sweep end not found")
	}
	section := out[start : start+end]

	for _, want := range []string{
		"### Reject terminal labels carried through a reopen",
		"`fake-1` (`fixed`, non-closed children: 0)",
		`bd comment fake-1 "Orchestrator: terminal label=fixed; closing."`,
		"--set-metadata \"work_started_at=2026-01-01T00:00:00Z\"",
		"bd close fake-1",
		"leave the bead actionable",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Step 2P terminal sweep missing %q", want)
		}
	}
	for _, banned := range []string{"python3", "jq ", "declare -A current_terminal_labels"} {
		if strings.Contains(section, banned) {
			t.Errorf("Step 2P terminal sweep still contains agent-run script %q", banned)
		}
	}
}
