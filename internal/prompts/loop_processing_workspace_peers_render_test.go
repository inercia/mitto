package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestLoopProcessing_WorkspacePeers_Mitto5vu pins the mitto-5vu optimization:
// loop-processing.prompt.yaml must no longer dispatch `mitto_conversation_list`
// for the §B/§C concurrency gate or the §2A/2B/2C beads-issue collision
// exclusion. Both must instead consult the pre-rendered `Workspace peers:`
// block backed by the `.Workspace.Peers.*` template accessor (mitto-4d6),
// which is populated at prompt-dispatch time.
//
// Acceptance criteria (from the bead, restated):
//
//   - `mitto_conversation_list_mitto` no longer appears in the rendered
//     "Loop processing tasks" body for the concurrency and collision checks.
//   - The pre-rendered `Workspace peers:` preamble carries `.Workspace.Peers.AllText`
//     (fallback via `Default "(none)" ...`) and `.Workspace.Peers.PromptingCount`
//     / `.Workspace.Peers.Count`.
//   - When peers are present, `AllText` renders each entry as
//     `id (name) [acp] {bd-id}` so a substring check for `{<bd-id>}` remains
//     the formal beads_issue collision needle (mirrors FormatChildren shape).
//   - When peers are empty, the `Default "(none)"` fallback lands.
//   - The Guidelines "Skip beads with active conversations" bullet cites
//     `.Workspace.Peers.All` instead of the removed MCP call.
//
// Rationale: this replaces one MCP round-trip per loop iteration (10-50/day
// per workspace) with a template-time snapshot that is strictly fresher —
// the bug this test would catch is a drift back to `mitto_conversation_list`
// or a stripped `Workspace peers:` preamble.
func TestLoopProcessing_WorkspacePeers_Mitto5vu(t *testing.T) {
	// Case A — peers present. Two peers: one carrying an active-driver
	// title/beads_issue, one carrying an unrelated title. Exercises the
	// formal-match needle (`{<bd-id>}`) inside AllText.
	ctxWithPeers := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "true", "WorkOnFeatures": "true"},
		Prompts: cel.PromptsContext{
			Names:        []string{"Loop fixing bug", "Loop implementing feature"},
			EnabledNames: []string{"Loop fixing bug", "Loop implementing feature"},
		},
		Workspace: cel.WorkspaceContext{
			UUID: "ws-uuid",
			Peers: cel.PeersContext{
				Count:          2,
				Exists:         true,
				PromptingCount: 1,
				IdleCount:      1,
				All: []cel.PeerInfo{
					{
						ID:          "peer-1",
						Name:        "Fix bug mitto-abc",
						ACPServer:   "Auggie (Sonnet)",
						IsPrompting: true,
						BeadsIssue:  "mitto-abc",
					},
					{
						ID:         "peer-2",
						Name:       "Unrelated user session",
						ACPServer:  "Auggie (Opus)",
						BeadsIssue: "",
					},
				},
			},
		},
	}
	outWithPeers := renderBuiltinPromptWithFragments(t, "Loop processing tasks", ctxWithPeers)

	// Acceptance 1: the removed MCP call must NOT reappear in the rendered
	// body. Guard against drift back to `mitto_conversation_list(...)` in
	// any form — the bare word is fine in narrative text, the tool-call
	// form is not.
	if strings.Contains(outWithPeers, "mitto_conversation_list(") {
		t.Errorf("rendered body still invokes mitto_conversation_list(...) — mitto-5vu regression:\n%s",
			firstOccurrence(outWithPeers, "mitto_conversation_list(", 200))
	}

	// Acceptance 2: the pre-rendered `Workspace peers:` block must appear
	// at the top of the body and must carry the AllText/PromptingCount/Count
	// substitutions.
	if !strings.Contains(outWithPeers, "Workspace peers:") {
		t.Fatalf("rendered body missing `Workspace peers:` preamble (mitto-5vu)")
	}
	// FormatPeers shape: `id (name) [acp] {bd-id}` — the `{mitto-abc}` suffix
	// is the formal beads_issue collision needle.
	if !strings.Contains(outWithPeers, "peer-1 (Fix bug mitto-abc) [Auggie (Sonnet)] {mitto-abc}") {
		t.Errorf("rendered `Workspace peers:` block missing FormatPeers-shaped entry for peer-1:\n%s",
			sliceAfter(outWithPeers, "Workspace peers:", 300))
	}
	// PromptingCount/Count must land as literal integers.
	if !strings.Contains(outWithPeers, "Active peer drivers: 1 / 2") {
		t.Errorf("rendered preamble missing `Active peer drivers: 1 / 2`:\n%s",
			sliceAfter(outWithPeers, "Active peer drivers:", 120))
	}

	// Acceptance 3: the Guidelines "Skip beads with active conversations"
	// bullet must cite `.Workspace.Peers.All`, not the removed MCP call.
	guidelinesIdx := strings.Index(outWithPeers, "## Guidelines")
	if guidelinesIdx < 0 {
		t.Fatal("Guidelines section not found in rendered body")
	}
	guidelines := outWithPeers[guidelinesIdx:]
	if !strings.Contains(guidelines, "Skip beads with active conversations") {
		t.Fatal("Guidelines missing `Skip beads with active conversations` bullet")
	}
	if !strings.Contains(guidelines, ".Workspace.Peers.All") {
		t.Errorf("Guidelines `Skip beads with active conversations` bullet must cite `.Workspace.Peers.All` (mitto-5vu); got:\n%s",
			sliceAfter(guidelines, "Skip beads with active conversations", 500))
	}

	// Case B — no peers. The `Default "(none)"` fallback must land so the
	// preamble stays human-readable instead of degenerating to empty
	// backticks (regression guard for mitto-znv on the new preamble line).
	ctxNoPeers := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "orch-1"},
		Args:    map[string]string{"Commit": "true", "FixBugs": "true", "WorkOnFeatures": "true"},
	}
	outNoPeers := renderBuiltinPromptWithFragments(t, "Loop processing tasks", ctxNoPeers)
	if !strings.Contains(outNoPeers, "Workspace peers: `(none)`") {
		t.Errorf("empty peers: expected `Workspace peers: `(none)`` fallback; got:\n%s",
			sliceAfter(outNoPeers, "Workspace peers:", 80))
	}
	if !strings.Contains(outNoPeers, "Active peer drivers: 0 / 0") {
		t.Errorf("empty peers: expected `Active peer drivers: 0 / 0`; got:\n%s",
			sliceAfter(outNoPeers, "Active peer drivers:", 80))
	}
}

// firstOccurrence returns up to `n` bytes of `s` starting at the first
// occurrence of `needle`, or the empty string if not found.
func firstOccurrence(s, needle string, n int) string {
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	end := i + n
	if end > len(s) {
		end = len(s)
	}
	return s[i:end]
}

// sliceAfter returns up to `n` bytes of `s` starting at the first
// occurrence of `after`, or the empty string if not found.
func sliceAfter(s, after string, n int) string { return firstOccurrence(s, after, n) }
