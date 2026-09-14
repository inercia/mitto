package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestLoopProcessing_ConcurrencyGate_RequiresBeadsIssueProvenance_Mitto_e1x
// reproduces mitto-e1x: the §B/§C concurrency gate counts a peer as an
// "active worker" using a blunt title-prefix heuristic (`Fix `/`Implement `/
// `Post-task: `) with no provenance check. A non-child, non-loop, idle human
// conversation whose title merely starts with `Fix ` (e.g. "Fix mermaid
// rendering") is indistinguishable from a genuine spawned worker and
// occupies the single worker slot forever, starving the loop.
//
// Genuine §B/§C/5H workers are ALWAYS spawned with `beads_issue` set (the
// `mitto_conversation_new` calls a few lines below each gate all pass
// `beads_issue: "<id>"`), so the rendered `Workspace peers:` block (backed
// by `.Workspace.Peers.All` / FormatPeers, see
// loop_processing_workspace_peers_render_test.go) already emits a literal
// `{<bd-id>}` suffix for every genuine worker and omits it for a bead-less
// human peer. The fix is to make the §B and §C gate prose require that
// suffix — not just the title prefix — before counting a peer against the
// concurrency cap.
//
// This test pins the exact required wording (documented on mitto-e1x's
// Reproduction comment) so the fix phase's prompt edit is verifiable:
// both the §B and §C gate paragraphs must state that a peer counts only
// when it ALSO carries a `{<bd-id>}` beads_issue suffix in the pre-rendered
// `Workspace peers:` block — a bare title-prefix match is insufficient.
//
// Fails today (pre-fix): neither section contains this qualifier, only the
// title-prefix rule.
func TestLoopProcessing_ConcurrencyGate_RequiresBeadsIssueProvenance_Mitto_e1x(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
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
				PromptingCount: 0,
				IdleCount:      2,
				All: []cel.PeerInfo{
					// Genuine spawned worker: title matches AND carries a
					// beads_issue → renders with a `{mitto-abc}` suffix.
					{
						ID:         "peer-1",
						Name:       "Fix mitto-abc",
						ACPServer:  "Auggie (Sonnet)",
						BeadsIssue: "mitto-abc",
					},
					// False positive from mitto-e1x's live repro: idle,
					// human-created, non-child conversation whose title
					// happens to start with "Fix " but has NO beads_issue —
					// must NOT count against the concurrency cap.
					{
						ID:         "peer-2",
						Name:       "Fix mermaid rendering",
						ACPServer:  "Auggie (Sonnet)",
						BeadsIssue: "",
					},
				},
			},
		},
	}
	out := renderBuiltinPromptWithFragments(t, "Loop processing tasks", ctx)

	// Extract the §B section body (between the §B and §C headings).
	bStart := strings.Index(out, "## §B — Fix ONE bug")
	cStart := strings.Index(out, "## §C — Implement ONE feature")
	if bStart < 0 || cStart < 0 || cStart <= bStart {
		t.Fatalf("could not locate §B/§C sections in rendered body")
	}
	sectionB := out[bStart:cStart]

	const requiredPhrase = "carries a `{<bd-id>}` beads_issue suffix"

	if !strings.Contains(sectionB, requiredPhrase) {
		t.Errorf("mitto-e1x: §B concurrency gate must require %q (provenance check) "+
			"in addition to the title-prefix match — bare title-prefix counting "+
			"false-positives on idle human peers whose title happens to start with "+
			"`Fix `/`Implement `/`Post-task: ` (live repro: peer-2 'Fix mermaid rendering', "+
			"no beads_issue). §B section:\n%s", requiredPhrase, sectionB)
	}

	// §C section runs from its heading to the next top-level heading/hr.
	sectionCEnd := strings.Index(out[cStart:], "\n  ---")
	var sectionC string
	if sectionCEnd < 0 {
		sectionC = out[cStart:]
	} else {
		sectionC = out[cStart : cStart+sectionCEnd]
	}
	if !strings.Contains(sectionC, requiredPhrase) {
		t.Errorf("mitto-e1x: §C concurrency gate must require %q (provenance check), "+
			"same rule as §B. §C section:\n%s", requiredPhrase, sectionC)
	}
}
