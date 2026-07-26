package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// renderSupportPrompt is a small helper: it loads the builtin fragment
// registry, resolves the named prompt from the builtin corpus, and renders
// its body against the given PromptEnabledContext. All support/ fragment
// smoke tests below share this bootstrap so each test focuses on the
// hallmark assertions unique to that fragment.
func renderSupportPrompt(t *testing.T, name string, ctx *cel.PromptEnabledContext) string {
	t.Helper()
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
	for _, p := range list {
		if p.Name == name {
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, err := RenderPromptTemplate(name, p.Content, ctx, funcs)
			if err != nil {
				t.Fatalf("render %q: %v", name, err)
			}
			return out
		}
	}
	t.Fatalf("prompt %q not found in builtin corpus", name)
	return ""
}

// TestNoTextUIFragmentRenders is a smoke test for _shared/no-text-ui: it
// renders every consuming support prompt and asserts hallmark substrings
// unique to the fragment body appear in the output, confirming the
// `{{ template "_shared/no-text-ui" . }}` call actually inlined the block.
func TestNoTextUIFragmentRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	hallmarks := []string{
		"NEVER use text-based interaction prompts.",
		"NEVER ask the user to type a number, command, or keyword",
		"ALWAYS use `mitto_ui_options` for choices, `mitto_ui_textbox` for review/editing",
	}
	consumers := []string{
		"Support: check status",
		"Support: gather more information",
		"Support: housekeeping",
		"Support: investigate",
		"Support: reply to user",
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, ctx)
		for _, hallmark := range hallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing no-text-ui hallmark %q", name, hallmark)
			}
		}
	}
}

// TestBeadMarkdownConventionFragmentRenders is a smoke test for
// _shared/beads/markdown-convention: it renders every consuming support
// prompt and asserts hallmark substrings unique to the fragment body.
func TestBeadMarkdownConventionFragmentRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	hallmarks := []string{
		"Always write in GitHub-flavored Markdown.",
		"pipe via stdin (`printf '%s' \"$c\" | bd comment <id> --stdin`)",
	}
	consumers := []string{
		"Support: check status",
		"Support: continue conversation",
		"Support: gather more information",
		"Support: housekeeping",
		"Support: investigate",
		"Support: reply to user",
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, ctx)
		for _, hallmark := range hallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing beads/markdown-convention hallmark %q", name, hallmark)
			}
		}
	}
}

// TestReplyFenceConventionFragmentRenders is a smoke test for
// _shared/reply-fence-convention: the caller passes a noun (e.g. "reply"
// or "clarifying message") as the fragment argument, and the fragment
// substitutes it into three sentences. This test asserts both that the
// stable fence markers appear and that the caller-supplied noun round-trips.
func TestReplyFenceConventionFragmentRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	stable := []string{
		"--- BEGIN REPLY ---",
		"--- END REPLY ---",
		"MUST be enclosed in a clearly delimited block",
	}
	cases := []struct {
		prompt string
		noun   string
	}{
		{"Support: reply to user", "reply"},
		{"Support: gather more information", "clarifying message"},
		{"Support: investigate", "reply"},
		{"Support: continue conversation", "reply"},
	}
	for _, tc := range cases {
		out := renderSupportPrompt(t, tc.prompt, ctx)
		for _, hallmark := range stable {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing reply-fence hallmark %q", tc.prompt, hallmark)
			}
		}
		// Confirm the parameterised noun substituted correctly (appears in
		// the "proposed <noun> to the user" subject clause).
		if !strings.Contains(out, "proposed "+tc.noun+" to the user") {
			t.Errorf("prompt %q: fragment did not substitute noun %q into subject clause", tc.prompt, tc.noun)
		}
	}
}

// TestPriorityRubricFragmentRenders is a smoke test for
// _shared/support/priority-rubric: renders every consuming per-bead
// support prompt and asserts hallmark substrings unique to the fragment
// (the three-bullet P1/P2/P3 list + the `bd update --priority` trailer).
func TestPriorityRubricFragmentRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	hallmarks := []string{
		"P1 (HIGH)",
		"P2 (MEDIUM)",
		"P3 (LOW)",
		"Apply with `bd update <id> --priority <n>`",
		"**[PRIORITY · <time>]** P<old> → P<new>",
	}
	consumers := []string{
		"Support: check status",
		"Support: continue conversation",
		"Support: gather more information",
		"Support: investigate",
		"Support: reply to user",
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, ctx)
		for _, hallmark := range hallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing priority-rubric hallmark %q", name, hallmark)
			}
		}
	}
}

// TestTargetBeadPickerFragmentRenders is a smoke test for
// _shared/support/target-bead-picker. It renders each consumer twice —
// once WITH a linked bead (so the "launched from bead" branch fires)
// and once WITHOUT (so the picker branch fires) — and asserts each
// branch's hallmark substrings appear.
func TestTargetBeadPickerFragmentRenders(t *testing.T) {
	consumers := []string{
		"Support: check status",
		"Support: gather more information",
		"Support: investigate",
		"Support: reply to user",
	}
	// Linked-bead branch.
	linkedCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", BeadsIssue: "mitto-42", HasMessages: true},
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, linkedCtx)
		if !strings.Contains(out, "launched from bead **`mitto-42`**") {
			t.Errorf("prompt %q: linked-bead branch missing the 'launched from bead' hallmark", name)
		}
	}
	// Picker branch.
	pickerCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, pickerCtx)
		if !strings.Contains(out, "`bd list -l support-question --status open,in_progress --all`") {
			t.Errorf("prompt %q: picker branch missing the `bd list` command hallmark", name)
		}
	}
}

// TestChannelFragmentReadRenders is a smoke test for
// _shared/support/channel-fragment-read. Verifies the stable
// per-fragment path convention hallmark appears in every consumer
// (mitto-da9.1 introduced the fragment; mitto-da9.2 migrated the
// remaining consumers off the monolithic channel-playbook-read).
func TestChannelFragmentReadRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	// Hallmarks unique to the channel-fragment-read fragment body:
	// the per-fragment path convention and the "authoritative fragment
	// guide" phrasing (as opposed to the monolithic "authoritative guide"
	// of the deprecated channel-playbook-read).
	hallmarks := []string{
		"Read the channel playbook fragment",
		"`.mitto/support/<channel-id>/",
		"authoritative",
	}
	consumers := []string{
		"Support: check status",
		"Support: gather more information",
		"Support: investigate",
		"Support: reply to user",
		"Support: watch channel",
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, ctx)
		for _, hallmark := range hallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing channel-fragment-read hallmark %q", name, hallmark)
			}
		}
	}
	// Owner-ask branch hallmark: the three owner prompts must render the
	// "this prompt owns creating it" text for at least one fragment.
	ownerAskHallmark := "this prompt owns creating it"
	owners := []string{
		"Support: watch channel",
		"Support: investigate",
		"Support: reply to user",
	}
	for _, name := range owners {
		out := renderSupportPrompt(t, name, ctx)
		if !strings.Contains(out, ownerAskHallmark) {
			t.Errorf("prompt %q: rendered output missing owner-ask hallmark %q", name, ownerAskHallmark)
		}
	}
}

// TestSlackToolsFragmentRenders is a smoke test for _shared/support/slack-tools.
// Asserts the stable "match by capability" preamble appears in every
// consumer, plus the two optional trailers (NoPosting, ReadMetadata)
// fire only when the caller opts in.
func TestSlackToolsFragmentRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	stable := "Match Slack MCP tools **by capability, not exact name**."
	consumers := []string{
		"Support: check status",
		"Support: continue conversation",
		"Support: gather more information",
		"Support: housekeeping",
		"Support: investigate",
		"Support: reply to user",
		"Support: watch channel",
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, ctx)
		if !strings.Contains(out, stable) {
			t.Errorf("prompt %q: rendered output missing slack-tools stable preamble", name)
		}
	}
	// Read-only prompts must render the "No posting tool" trailer.
	readOnly := []string{
		"Support: check status",
		"Support: housekeeping",
		"Support: investigate",
		"Support: watch channel",
	}
	for _, name := range readOnly {
		out := renderSupportPrompt(t, name, ctx)
		if !strings.Contains(out, "No posting tool is needed") {
			t.Errorf("prompt %q: missing NoPosting trailer", name)
		}
	}
	// Prompts that DO post must NOT render the NoPosting trailer.
	posts := []string{
		"Support: continue conversation",
		"Support: gather more information",
		"Support: reply to user",
	}
	for _, name := range posts {
		out := renderSupportPrompt(t, name, ctx)
		if strings.Contains(out, "No posting tool is needed") {
			t.Errorf("prompt %q: unexpectedly rendered NoPosting trailer", name)
		}
	}
}

// TestWhatsNextMappingFragmentRenders is a smoke test for
// _shared/support/whats-next-mapping. Asserts the six-row core table
// appears in both consumers and the closing-state rows appear only in
// watch-channel (IncludeClosed=true).
func TestWhatsNextMappingFragmentRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	coreRows := []string{
		"| `triaged` | Needs investigation |",
		"| `gathering-info` | Investigation in progress |",
		"| `need-info` | Needs more info from user |",
		"| `drafting` | Ready to reply to user |",
		"| `awaiting-customer` | Waiting on customer reply |",
		"| `awaiting-us` | Our turn — needs a response |",
	}
	for _, name := range []string{"Support: housekeeping", "Support: watch channel"} {
		out := renderSupportPrompt(t, name, ctx)
		for _, row := range coreRows {
			if !strings.Contains(out, row) {
				t.Errorf("prompt %q: rendered output missing whats-next-mapping row %q", name, row)
			}
		}
	}
	// Only watch-channel should have the two closing-state rows.
	wc := renderSupportPrompt(t, "Support: watch channel", ctx)
	for _, row := range []string{"| `resolved` | Resolved (closing) |", "| `stale` | Stale — auto-closing |"} {
		if !strings.Contains(wc, row) {
			t.Errorf("Support: watch channel missing closing-state row %q", row)
		}
	}
	hk := renderSupportPrompt(t, "Support: housekeeping", ctx)
	for _, row := range []string{"| `resolved` | Resolved (closing) |", "| `stale` | Stale — auto-closing |"} {
		if strings.Contains(hk, row) {
			t.Errorf("Support: housekeeping unexpectedly contains closing-state row %q", row)
		}
	}
}

// TestAskFragmentTemplatesRender is a smoke test for the six new
// `_shared/support/ask-*.tmpl` templates introduced in mitto-da9.2.
// It renders the three owner prompts and asserts that each ask
// template's "Ask + write `<fragment>.md`" hallmark is present in
// the correct owner (and only that owner) — protecting the owner
// mapping from silent drift.
func TestAskFragmentTemplatesRender(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	// Owner mapping: (owner prompt name) -> list of fragment names it owns.
	owners := map[string][]string{
		"Support: watch channel": {"scope"},
		"Support: investigate":   {"investigation", "resources", "escalation", "redirects"},
		"Support: reply to user": {"tone"},
	}
	// Fragments not owned by a given owner must NOT appear as owner-ask
	// hallmarks in that owner's render — the ask template is emitted only
	// by its owning prompt.
	allFragments := []string{"scope", "tone", "investigation", "resources", "escalation", "redirects"}
	for ownerName, owned := range owners {
		out := renderSupportPrompt(t, ownerName, ctx)
		ownedSet := make(map[string]bool, len(owned))
		for _, f := range owned {
			ownedSet[f] = true
			hallmark := "**Ask + write `" + f + ".md`."
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing ask-%s hallmark %q", ownerName, f, hallmark)
			}
			// Each ask template must instruct writing under the stable
			// per-fragment path.
			pathHallmark := ".mitto/support/<channel-id>/" + f + ".md"
			if !strings.Contains(out, pathHallmark) {
				t.Errorf("prompt %q: rendered output missing per-fragment path hallmark %q", ownerName, pathHallmark)
			}
		}
		for _, f := range allFragments {
			if ownedSet[f] {
				continue
			}
			hallmark := "**Ask + write `" + f + ".md`."
			if strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: unexpectedly renders ask-%s hallmark %q (not its owner)", ownerName, f, hallmark)
			}
		}
	}
	// Non-owner support prompts must not emit ANY ask hallmark.
	nonOwners := []string{
		"Support: check status",
		"Support: continue conversation",
		"Support: gather more information",
		"Support: housekeeping",
	}
	for _, name := range nonOwners {
		out := renderSupportPrompt(t, name, ctx)
		for _, f := range allFragments {
			hallmark := "**Ask + write `" + f + ".md`."
			if strings.Contains(out, hallmark) {
				t.Errorf("non-owner prompt %q: unexpectedly renders ask-%s hallmark %q", name, f, hallmark)
			}
		}
	}
}

// TestAskFragmentTimeouts is a regression guard for mitto-da9.2's
// silent-loop-safety contract:
//
//   - `Support: watch channel` runs in a scheduled loop and MUST use a
//     short timeout (60s) for its ask so the run does not block for
//     minutes waiting on a user who is not there.
//   - `Support: investigate` and `Support: reply to user` are interactive
//     owners; they use the interactive 300s convention.
//
// If a future edit accidentally flips watch-channel to 300s (or an
// interactive owner to 60s), this test catches it.
func TestAskFragmentTimeouts(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	cases := []struct {
		owner    string
		fragment string
		timeout  string // the exact `timeout_seconds: <N>` value expected
	}{
		{"Support: watch channel", "scope", "60"},
		{"Support: investigate", "investigation", "300"},
		{"Support: investigate", "resources", "300"},
		{"Support: investigate", "escalation", "300"},
		{"Support: investigate", "redirects", "300"},
		{"Support: reply to user", "tone", "300"},
	}
	// One render per owner is enough; cache to avoid redundant work.
	renders := map[string]string{}
	for _, tc := range cases {
		out, ok := renders[tc.owner]
		if !ok {
			out = renderSupportPrompt(t, tc.owner, ctx)
			renders[tc.owner] = out
		}
		// Find the ask block for this fragment, then look for the timeout
		// within a short window that follows it (the templates render the
		// timeout on the same line as the title).
		anchor := "**Ask + write `" + tc.fragment + ".md`."
		idx := strings.Index(out, anchor)
		if idx < 0 {
			t.Errorf("prompt %q: missing ask block for fragment %q (anchor %q)", tc.owner, tc.fragment, anchor)
			continue
		}
		end := idx + 400
		if end > len(out) {
			end = len(out)
		}
		block := out[idx:end]
		want := "timeout_seconds: " + tc.timeout
		if !strings.Contains(block, want) {
			t.Errorf("prompt %q, fragment %q: ask block missing expected timeout %q\nblock: %s", tc.owner, tc.fragment, want, block)
		}
	}
}

// TestMigrateMonolithFragmentRenders is a smoke test for
// _shared/support/migrate-monolith (mitto-da9.3). It renders the two
// host prompts that invoke the migration block — `Support: watch channel`
// (scope gate) and `Support: investigate` (Step 3) — and asserts the
// stable hallmarks unique to the migration body appear in each.
//
// Also asserts that non-host support prompts do NOT accidentally include
// the migration block (owner mapping guard).
func TestMigrateMonolithFragmentRenders(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	// Hallmarks unique to the migrate-monolith fragment body: the
	// sub-heading, the idempotence-check phrasing, the six-row heading→
	// fragment table (a sampling), and the move-to-archive command.
	hallmarks := []string{
		"### Monolithic-playbook auto-migration",
		"Idempotence check.",
		"No-monolith check.",
		"Split the monolith.",
		"| Scope of Investigation and Reply             | `scope.md`",
		"| Response Style and Tone                      | `tone.md`",
		"| Investigations: how to gather information    | `investigation.md`",
		"| Repos / docs / runbooks                      | `resources.md`",
		"| Escalation                                   | `escalation.md`",
		"_migrated_from_monolith.md",
		"_unclassified.md",
	}
	hosts := []string{
		"Support: watch channel",
		"Support: investigate",
	}
	for _, name := range hosts {
		out := renderSupportPrompt(t, name, ctx)
		for _, hallmark := range hallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing migrate-monolith hallmark %q", name, hallmark)
			}
		}
	}
	// Non-host support prompts must NOT include the migration block.
	nonHosts := []string{
		"Support: check status",
		"Support: continue conversation",
		"Support: gather more information",
		"Support: housekeeping",
		"Support: reply to user",
	}
	guard := "### Monolithic-playbook auto-migration"
	for _, name := range nonHosts {
		out := renderSupportPrompt(t, name, ctx)
		if strings.Contains(out, guard) {
			t.Errorf("non-host prompt %q: unexpectedly renders migrate-monolith block %q", name, guard)
		}
	}
}

// TestMigrateMonolithChannelSubstitution verifies that the `Channel` arg
// passed by the caller is spliced into the rendered paths, and the
// `ActiveBead` arg controls whether the block ends with a scoped
// `bd comment <id> ...` command (non-empty) or a generic "any tracked
// bead in this channel" fallback (empty).
func TestMigrateMonolithChannelSubstitution(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}

	// `Support: watch channel` renders with the literal Go-template
	// variable `$channel` substituted at render time via the prompt's
	// own `SlackChannelID` arg. Passing a concrete channel ID exercises
	// that substitution end-to-end.
	watchCtx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Args:    map[string]string{"SlackChannelID": "C0TESTMIG1"},
	}
	watchOut := renderSupportPrompt(t, "Support: watch channel", watchCtx)
	if !strings.Contains(watchOut, ".mitto/slack-support-C0TESTMIG1.md") {
		t.Errorf("watch-channel: expected substituted monolith path with C0TESTMIG1; got no match")
	}
	if !strings.Contains(watchOut, ".mitto/support/C0TESTMIG1/") {
		t.Errorf("watch-channel: expected substituted target dir with C0TESTMIG1; got no match")
	}
	// Empty ActiveBead branch: the generic fallback prose fires.
	if !strings.Contains(watchOut, "any tracked bead in this channel") {
		t.Errorf("watch-channel: expected empty-ActiveBead fallback prose; got no match")
	}

	// `Support: investigate` passes the literal `<channel-id>` and `<id>`
	// placeholder strings (the prompt does not know the channel/bead at
	// template-render time), so the rendered block should preserve them
	// verbatim in both the paths and the scoped bd comment command.
	invOut := renderSupportPrompt(t, "Support: investigate", ctx)
	if !strings.Contains(invOut, ".mitto/slack-support-<channel-id>.md") {
		t.Errorf("investigate: expected placeholder-substituted monolith path; got no match")
	}
	if !strings.Contains(invOut, ".mitto/support/<channel-id>/") {
		t.Errorf("investigate: expected placeholder-substituted target dir; got no match")
	}
	// Non-empty ActiveBead branch: the scoped bd comment command fires.
	if !strings.Contains(invOut, "bd comment <id> ") {
		t.Errorf("investigate: expected scoped `bd comment <id> ...` command from non-empty ActiveBead; got no match")
	}
	if strings.Contains(invOut, "any tracked bead in this channel") {
		t.Errorf("investigate: unexpectedly contains empty-ActiveBead fallback prose (ActiveBead is set)")
	}
}

// TestChannelPlaybookReadDeprecated is a regression guard for
// mitto-da9.3's deprecation of `_shared/support/channel-playbook-read`.
//
//   - The fragment body itself must render the DEPRECATED banner and the
//     drift-note instruction so any future caller emits a `[CONTEXT]` bead
//     comment naming the legacy template.
//   - No in-tree builtin prompt may call the deprecated fragment: zero
//     `{{ template "_shared/support/channel-playbook-read" ... }}` sites
//     across the builtin corpus.
func TestChannelPlaybookReadDeprecated(t *testing.T) {
	installBuiltinFragmentsForTest(t)

	// (a) Body-level assertions: render the fragment directly against a
	// minimal context and check the deprecation hallmarks.
	reg := CurrentFragments()
	body, ok := reg.Get("_shared/support/channel-playbook-read")
	if !ok {
		t.Fatalf("fragment %q not present in the loaded registry", "_shared/support/channel-playbook-read")
	}
	// Minimal wrapper template that invokes the fragment with a Purpose.
	wrapper := `{{ template "_shared/support/channel-playbook-read" (dict "Purpose" "smoke-test") }}`
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	out, err := RenderPromptTemplate("channel-playbook-read-deprecation", wrapper, ctx, funcs)
	if err != nil {
		t.Fatalf("render deprecated fragment wrapper: %v", err)
	}
	// Guard the fragment content is non-empty so `Get` did not silently
	// return an empty string.
	if strings.TrimSpace(body) == "" {
		t.Fatalf("deprecated fragment body is empty")
	}
	deprecationHallmarks := []string{
		"⚠ **DEPRECATED**",
		"channel-fragment-read",
		"still using legacy",
	}
	for _, hallmark := range deprecationHallmarks {
		if !strings.Contains(out, hallmark) {
			t.Errorf("rendered deprecated fragment missing hallmark %q; got:\n%s", hallmark, out)
		}
	}

	// (b) Zero-caller assertion: no builtin prompt may invoke the
	// deprecated fragment. Scan every builtin prompt's raw body for the
	// template call. Doc-comment mentions inside `{{- /* ... */ -}}` blocks
	// and prose references are OK — only actual invocations fail.
	builtinDir := "../../config/prompts/builtin"
	prompts, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Skipf("cannot load builtins from %s: %v", builtinDir, err)
	}
	invocation := `template "_shared/support/channel-playbook-read"`
	for _, p := range prompts {
		if strings.Contains(p.Content, invocation) {
			t.Errorf("builtin prompt %q still invokes the deprecated fragment %q — migrate the caller to _shared/support/channel-fragment-read", p.Name, "_shared/support/channel-playbook-read")
		}
	}
}

// TestContinueConversationOffMonolithicPath is a regression guard for
// mitto-da9.3's migration of `Support: continue conversation` off the
// legacy monolithic playbook path. The rendered body must reference the
// fragment layout (`.mitto/support/<channel-id>/`) but MUST NOT reference
// the legacy monolith path (`.mitto/slack-support-<channel-id>.md`) —
// this was the last active caller of the monolith and it now uses
// `channel-fragment-read` for scope/tone/investigation reads.
func TestContinueConversationOffMonolithicPath(t *testing.T) {
	// Render with a concrete channel ID so any leftover `$channel`-templated
	// reference to the legacy path resolves and fails the check below.
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Args:    map[string]string{"SlackChannelID": "C0TESTCC01"},
	}
	out := renderSupportPrompt(t, "Support: continue conversation", ctx)
	// Positive: at least one fragment path substituted with the arg.
	if !strings.Contains(out, ".mitto/support/C0TESTCC01/") {
		t.Errorf("continue-conversation: expected fragment path with substituted channel; got no match")
	}
	// Negative: zero references to the legacy monolithic path, either
	// literal or substituted.
	forbidden := []string{
		".mitto/slack-support-C0TESTCC01.md",
		".mitto/slack-support-<channel-id>.md",
	}
	for _, f := range forbidden {
		if strings.Contains(out, f) {
			t.Errorf("continue-conversation: forbidden legacy monolith path %q leaked into rendered body", f)
		}
	}
}
