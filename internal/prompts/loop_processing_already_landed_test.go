package prompts

import (
	"os/exec"
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

// TestLoopProcessing_TerminalSweepRejectsPreReopenLabel reproduces the
// residual mitto-2bbw path. Step 2P must not close a reopened bead merely
// because its terminal label survived from the previous closed episode.
func TestLoopProcessing_TerminalSweepRejectsPreReopenLabel(t *testing.T) {
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
		t.Fatal("rendered Step 2P terminal-label sweep not found")
	}
	end := strings.Index(out[start:], "### Reap epics whose children are all closed")
	if end < 0 {
		t.Fatal("rendered Step 2P terminal-label sweep not found")
	}
	section := out[start : start+end]

	for _, want := range []string{
		`while IFS=$'\t' read -r id terminal_label`,
		`bd history "$id" --json`,
		`bd show "$id" --json --include-comments`,
		"most recent transition into the current non-closed episode",
		"most recent terminal phase comment",
		`"fixed": "Fix [tier:"`,
		`"verified": "Review [tier:"`,
		"strictly newer than the current episode",
		"datetime.fromisoformat",
		`current_terminal_labels["$id"]="$terminal_label"`,
		`bd update "$id" --remove-label "$terminal_label"`,
		`[ "${current_terminal_labels[$id]:-}" = "fixed" ] || continue`,
		`[ "${current_terminal_labels[$id]:-}" = "verified" ] || continue`,
		"leave the bead actionable",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Step 2P terminal sweep missing reopen guard %q", want)
		}
	}

	historyAt := strings.Index(section, `bd history "$id" --json`)
	closeAt := strings.Index(section, `bd close "$id"`)
	if historyAt < 0 || closeAt < 0 || historyAt > closeAt {
		t.Error("Step 2P must validate terminal-label history before closing the bead")
	}
	if strings.Contains(section, `issue(e).get("labels")`) {
		t.Error("bd history snapshots do not carry labels; the probe must use live comments as terminal evidence")
	}
}

func TestLoopProcessing_TerminalSweepProbeUsesRealHistoryShape(t *testing.T) {
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
		t.Fatal("rendered Step 2P terminal-label sweep not found")
	}
	end := strings.Index(out[start:], "### Reap epics whose children are all closed")
	if end < 0 {
		t.Fatal("rendered Step 2P terminal-label sweep end not found")
	}
	section := out[start : start+end]
	probeStart := strings.Index(section, "python3 -c '\n")
	if probeStart < 0 {
		t.Fatal("terminal-label Python probe not found")
	}
	probeStart += len("python3 -c '\n")
	argAt := strings.Index(section[probeStart:], `"$terminal_label" 2>/dev/null`)
	if argAt < 0 {
		t.Fatal("terminal-label Python probe end not found")
	}
	probePrefix := section[probeStart : probeStart+argAt]
	probeEnd := strings.LastIndex(probePrefix, "\n")
	if probeEnd < 0 {
		t.Fatal("terminal-label Python probe closing line not found")
	}
	probe := probePrefix[:probeEnd]

	fixtures := []struct {
		name    string
		comment string
		want    string
	}{
		{name: "pre-reopen fix comment is stale", comment: "2026-08-18T08:59:00Z", want: "stale"},
		{name: "current-episode fix comment is current", comment: "2026-08-18T10:01:00Z", want: "current"},
	}
	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			payload := `{"history":[` +
				`{"CommitDate":"2026-08-18T10:00:00Z","Issue":{"status":"open"}},` +
				`{"CommitDate":"2026-08-18T09:00:00Z","Issue":{"status":"closed"}}],` +
				`"current":[{"status":"open","labels":["fixed"],"comments":[` +
				`{"created_at":"` + tc.comment + `","text":"Fix [tier: Coding]: evidence"}]}]}`
			cmd := exec.Command("python3", "-c", probe, "fixed")
			cmd.Stdin = strings.NewReader(payload)
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("terminal-label probe failed: %v\n%s", err, got)
			}
			if strings.TrimSpace(string(got)) != tc.want {
				t.Fatalf("terminal-label probe = %q, want %q", strings.TrimSpace(string(got)), tc.want)
			}
		})
	}
}
