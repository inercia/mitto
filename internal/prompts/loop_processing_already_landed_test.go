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
		"most recent transition into the current open episode",
		"strictly newer than that transition",
		"If either timestamp or ordering cannot be established, leave the bead actionable",
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
