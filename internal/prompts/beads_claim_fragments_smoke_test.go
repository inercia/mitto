package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestClaimEntryFragmentRenders is a smoke test for the
// beads-issues/shared/claim-entry fragment (mitto-ial): loads the builtin
// fragment registry, renders each L2 loop-driver consumer that invokes the
// fragment, and asserts that the hallmark substrings from the fragment body
// appear in the rendered output — with the target bead ID and this session
// ID substituted correctly. A regression that silently drops the fragment,
// breaks the dict parameterization, or drops any of the three metadata
// stamps / the foreign-claim defer branch is caught here.
func TestClaimEntryFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// L2 loop-drivers that MUST inline the claim-entry fragment after Step 2.
	consumers := []string{
		"Loop fixing bug",
		"Loop implementing feature",
		"Mention — driver",
	}

	linkedCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-42", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "Commit": "true", "MentionTS": "2026-07-25T00:00:00Z"},
	}
	linkedFuncs := cel.BuildTemplateFuncMap(linkedCtx)

	// Hallmarks unique to claim-entry — every consumer must render them, with
	// the target bead + session ID substituted from the dict parameters.
	hallmarks := []string{
		"## Step 0 — Claim + first heartbeat (crash-recovery guarantee)",
		"claim_owner=$(bd show mitto-abc --json | jq -r '.[0].metadata.claimed_by // empty')",
		// Fresh-claim branch: label add + all three metadata keys stamped.
		"bd update mitto-abc --add-label in-flight",
		`--set-metadata "claimed_by=sess-42"`,
		`--set-metadata "claimed_at=$now"`,
		`--set-metadata "claim_heartbeat_at=$now"`,
		// Re-entry branch: heartbeat-only refresh.
		`elif [ "$claim_owner" = "sess-42" ]; then`,
		`bd update mitto-abc --set-metadata "claim_heartbeat_at=$now"`,
		// Foreign-claim branch: comment + fall through to Step 4.
		`bd comment mitto-abc "Driver sess-42: bead already claimed by $claim_owner; deferring to peer."`,
		"jump to **Step 4 (Blocked → Defer + Handoff)**",
	}
	for _, name := range consumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", name)
			continue
		}
		out, err := RenderPromptTemplate(name, body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Errorf("render %q: %v", name, err)
			continue
		}
		for _, h := range hallmarks {
			if !strings.Contains(out, h) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — claim-entry did not inline or dict parameterization failed", name, h)
			}
		}
	}
}

// TestClaimClearFragmentRenders is a smoke test for the
// beads-issues/shared/claim-clear fragment (mitto-ial): renders each L2 loop-
// driver's Done branch and asserts the three-line clear block (remove
// `in-flight` label, unset all three claim metadata keys) appears with the
// target bead substituted. A regression that drops the fragment or leaves
// a ghost claim on a closed bead is caught here.
func TestClaimClearFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	consumers := []string{
		"Loop fixing bug",
		"Loop implementing feature",
		"Mention — driver",
	}

	linkedCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-42", Name: "N", HasMessages: true, BeadsIssue: "mitto-abc", HasBeadsIssue: true},
		Args:    map[string]string{"IssueID": "mitto-abc", "Commit": "true", "MentionTS": "2026-07-25T00:00:00Z"},
	}
	linkedFuncs := cel.BuildTemplateFuncMap(linkedCtx)

	// Hallmarks unique to claim-clear. Each substring must appear with the
	// target bead substituted, and the comment cross-referencing mitto-ial
	// must be present so grep-and-audit still finds the release site.
	hallmarks := []string{
		"Clear the passive claim before closing (see mitto-ial",
		"bd update mitto-abc --remove-label in-flight",
		"--unset-metadata claimed_by",
		"--unset-metadata claimed_at",
		"--unset-metadata claim_heartbeat_at",
	}
	for _, name := range consumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found in builtin corpus", name)
			continue
		}
		out, err := RenderPromptTemplate(name, body, linkedCtx, linkedFuncs)
		if err != nil {
			t.Errorf("render %q: %v", name, err)
			continue
		}
		for _, h := range hallmarks {
			if !strings.Contains(out, h) {
				t.Errorf("prompt %q: rendered output missing hallmark %q — claim-clear did not inline before bd close", name, h)
			}
		}
	}
}

// TestClaimHeartbeatFragmentRenders directly exercises the
// beads-issues/shared/claim-heartbeat fragment (mitto-ial) via a synthetic
// caller. The fragment is intentionally NOT invoked by any current driver
// today — claim-entry's `claimed_by == self` re-entry branch covers the
// per-iteration heartbeat — but the fragment is kept as a named companion
// for future callers and lifecycle-symmetry with claim-clear. This test
// pins its contract: the target substitutes into a bare `bd update
// --set-metadata claim_heartbeat_at=...` line.
func TestClaimHeartbeatFragmentRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	// Synthetic caller that only invokes claim-heartbeat with a linked bead.
	// This bypasses the "no live consumer" gap so any regression in the
	// fragment itself is still caught.
	body := `{{ template "beads-issues/shared/claim-heartbeat" (dict "Target" "mitto-abc") }}`
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-42", Name: "N", HasMessages: true},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	out, err := RenderPromptTemplate("claim-heartbeat-smoke", body, ctx, funcs)
	if err != nil {
		t.Fatalf("render synthetic caller: %v", err)
	}
	for _, h := range []string{
		"Refresh the claim heartbeat",
		`bd update mitto-abc --set-metadata "claim_heartbeat_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"`,
	} {
		if !strings.Contains(out, h) {
			t.Errorf("claim-heartbeat: rendered output missing hallmark %q", h)
		}
	}

	// Empty Target must skip the whole block — no `bd update` leaks with a
	// literal blank id.
	bodyEmpty := `{{ template "beads-issues/shared/claim-heartbeat" (dict "Target" "") }}`
	outEmpty, err := RenderPromptTemplate("claim-heartbeat-empty-smoke", bodyEmpty, ctx, funcs)
	if err != nil {
		t.Fatalf("render synthetic caller (empty Target): %v", err)
	}
	if strings.Contains(outEmpty, "bd update") {
		t.Errorf("claim-heartbeat: empty Target must skip the block, got: %q", outEmpty)
	}
}

// TestLoopProcessingReaperRenders is a smoke test for the L1 orchestrator's
// Step 2R (cross-signal reaper) + Step 2P terminal-label pre-close claim
// clear + Step 6 Reaper counter (mitto-ial). Renders the `Loop processing
// tasks` prompt and asserts the reaper's decision-table hallmarks, the
// staleness-cutoff shell probe, the cross-signal invocation with the session
// ID threaded, the REAP/SKIP branches, the Step 2P pre-close claim-clear
// inlined into the terminal-label sweep, and the Reaper counter in Step 6.
// A regression that drops any of these guarantees is caught here.
func TestLoopProcessingReaperRenders(t *testing.T) {
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	builtinDir := "../../config/prompts/builtin"
	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	body, ok := byName["Loop processing tasks"]
	if !ok {
		t.Fatalf("prompt \"Loop processing tasks\" not found in builtin corpus")
	}
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-orchestrator", Name: "N", HasMessages: true},
		Args:    map[string]string{},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	out, err := RenderPromptTemplate("Loop processing tasks", body, ctx, funcs)
	if err != nil {
		t.Fatalf("render \"Loop processing tasks\": %v", err)
	}

	// Step 2R heading + cross-signal decision table (all 4 rows must appear).
	reaperHallmarks := []string{
		"## Step 2R — Reap stale in-flight claims (cross-signal reaper)",
		"Run AFTER Step 2P (children/beads already reaped) and BEFORE Step 2Z",
		"| not-found / archived / !is_running   | REAP (`conversation_gone`)",
		"| is_prompting == true                 | SKIP (`still_prompting`",
		"| updated_at > now − 30 min            | SKIP-with-warn (`wedged_but_ticking`)",
		"| else                                 | REAP (`silent_conversation`)",
		// Enumeration query: uses both --label in-flight and
		// --has-metadata-key claim_heartbeat_at, filters by staleness in jq.
		"bd list --status open --label in-flight --has-metadata-key claim_heartbeat_at --json",
		"select(.metadata.claim_heartbeat_at < $cutoff)",
		// Cross-signal invocation threads the session ID.
		`mitto_conversation_get(self_id: "sess-orchestrator", conversation_id: "<claimer>")`,
		// REAP branch: release the claim (label + all three metadata keys).
		"bd update <id> --remove-label in-flight",
		"--unset-metadata claimed_by --unset-metadata claimed_at",
		"--unset-metadata claim_heartbeat_at",
		// Step 6 counter line for this pass.
		"Log one-line count for Step 6: `Reaper: released=<n> skipped=<n>`.",
	}
	for _, h := range reaperHallmarks {
		if !strings.Contains(out, h) {
			t.Errorf("Loop processing tasks: missing Step 2R hallmark %q", h)
		}
	}

	// Step 2P terminal-label sweep must run the claim-clear (remove label +
	// unset all three metadata keys) INLINE before bd close, so archived
	// beads never carry ghost claims. Both the bug and feature branches
	// share the same clear-block; assert both `bd close "$id"` sites are
	// preceded by the clear (approximated by the presence of the clear
	// snippet + the sweep's outer marker).
	sweepHallmarks := []string{
		"### Close terminal-label beads",
		// The clear block appears inside the sweep loop (target is the shell
		// variable $id in this sweep, so we match on the flag surface only).
		`bd update "$id" --remove-label in-flight`,
		"--unset-metadata claimed_by --unset-metadata claimed_at",
		"--unset-metadata claim_heartbeat_at",
		// Same-line self-doc so the intent is discoverable via grep.
		"mitto-ial: ghost claims must not survive into archives",
	}
	for _, h := range sweepHallmarks {
		if !strings.Contains(out, h) {
			t.Errorf("Loop processing tasks: missing Step 2P terminal-label claim-clear hallmark %q", h)
		}
	}

	// Step 6 end-of-pass notification message must publish the Reaper
	// counter so operators can observe the reaper's activity per pass.
	step6Hallmarks := []string{
		"Reaper: released=<n> skipped=<n> (from Step 2R",
	}
	for _, h := range step6Hallmarks {
		if !strings.Contains(out, h) {
			t.Errorf("Loop processing tasks: missing Step 6 Reaper counter hallmark %q", h)
		}
	}

	// §B / §C must exclude beads carrying the `in-flight` label as the
	// primary collision guard (belt-and-suspenders with the peer-match).
	if !strings.Contains(out, "in-flight") {
		t.Errorf("Loop processing tasks: rendered output does not mention the in-flight label anywhere — §B/§C exclusion vanished")
	}
}
