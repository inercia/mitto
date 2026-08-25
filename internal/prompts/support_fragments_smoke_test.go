package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// installFakeBdForRender writes a fake `bd` shell script to a fresh temp dir
// (unique per call) and prepends it to PATH for the test's lifetime. It ignores
// its arguments and always emits `stdout`. Mirrors the internal/cel unexported
// helper of the same purpose so integration renders in this package can
// exercise the `BeadMetadata` fallback path (mitto-09k) against the real
// builtin corpus without requiring a working `bd` in the host PATH.
func installFakeBdForRender(t *testing.T, stdout string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncat <<'MITTO_BD_EOF'\n%s\nMITTO_BD_EOF\nexit 0\n", stdout)
	bdPath := filepath.Join(dir, "bd")
	if err := os.WriteFile(bdPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}

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
// beads-issues/shared/markdown-convention: it renders every consuming support
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
// support/shared/priority-rubric: renders every consuming per-bead
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
// support/shared/target-bead-picker. It renders each consumer twice —
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
// support/shared/channel-fragment-read (mitto-eyf: render-time inlining).
// The fragment has three branches: (a) inline when the channel is known and
// the file exists on disk, (b) MISSING when the channel is known but the file
// does not exist, (c) runtime-read fallback when the caller did not supply a
// channel id at render time. This test covers branches (b) and (c); the
// inline branch (a) has its own dedicated test with a temp-workspace file.
func TestChannelFragmentReadRenders(t *testing.T) {
	// Rendering with NO SlackChannelID (and no linked bead) forces the
	// runtime-read fallback branch — hallmarks unique to that branch include
	// the parenthetical marker and the "<channel-id>" placeholder path
	// convention which no other branch emits.
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	fallbackHallmarks := []string{
		"fragment (runtime read fallback)",
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
		for _, hallmark := range fallbackHallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: rendered output missing channel-fragment-read fallback hallmark %q", name, hallmark)
			}
		}
	}
	// Every former owner now renders the shared read-only policy instead of
	// asking to create a missing fragment.
	formerOwners := []string{
		"Support: watch channel",
		"Support: investigate",
		"Support: reply to user",
	}
	for _, name := range formerOwners {
		out := renderSupportPrompt(t, name, ctx)
		if !strings.Contains(out, "read-only by default") || !strings.Contains(out, "explicitly and directly requires") {
			t.Errorf("prompt %q: rendered output missing shared read-only policy", name)
		}
		if strings.Contains(out, "this prompt owns creating it") {
			t.Errorf("prompt %q: rendered obsolete owner-write instruction", name)
		}
	}

	// MISSING branch (channel known, file absent): render `Support: watch
	// channel` with an explicit SlackChannelID and no fragment files in the
	// temp workspace. The template emits the "fragment MISSING" marker
	// scoped to the concrete channel id, proving branch (b) fires.
	tmpDir := t.TempDir()
	missingCtx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		Args:      map[string]string{"SlackChannelID": "C0MISSING1"},
	}
	missOut := renderSupportPrompt(t, "Support: watch channel", missingCtx)
	missingHallmarks := []string{
		"fragment MISSING",
		"`.mitto/support/C0MISSING1/",
	}
	for _, hallmark := range missingHallmarks {
		if !strings.Contains(missOut, hallmark) {
			t.Errorf("watch-channel (channel set, no file): rendered output missing hallmark %q", hallmark)
		}
	}
	// The runtime-read fallback branch must NOT fire when a channel is set.
	if strings.Contains(missOut, "fragment (runtime read fallback)") {
		t.Errorf("watch-channel (channel set): unexpectedly rendered runtime-read fallback branch")
	}
}

func TestChannelToneDefaultFallback(t *testing.T) {
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	consumers := []string{
		"Support: check status",
		"Support: continue conversation",
		"Support: gather more information",
		"Support: investigate",
		"Support: reply to user",
		"Support: watch channel",
	}
	hallmarks := []string{
		"Use this default tone guidance:",
		"**Keep responses concise and brief:**",
		"Prioritize short, focused answers",
		"**Show appropriate uncertainty:**",
		`❌ "Great question! This is definitely a XXX issue.`,
	}
	for _, name := range consumers {
		out := renderSupportPrompt(t, name, ctx)
		for _, hallmark := range hallmarks {
			if !strings.Contains(out, hallmark) {
				t.Errorf("prompt %q: missing default-tone hallmark %q", name, hallmark)
			}
		}
	}

	// A real tone.md remains authoritative and suppresses the fallback.
	// `Support: reply to user` derives the channel from the linked bead's
	// `metadata.slack_channel` (no SlackChannelID param), so wire the custom
	// channel through a fake bead rather than an explicit arg.
	channel := "C0CUSTOMTONE"
	installFakeBdForRender(t, `[{"id":"mitto-1","status":"open","labels":["support-question"],"metadata":{"slack_channel":"`+channel+`"}}]`)
	tmpDir := t.TempDir()
	fragmentDir := filepath.Join(tmpDir, ".mitto", "support", channel)
	if err := os.MkdirAll(fragmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fragmentDir, "tone.md"), []byte("CUSTOM-TONE-SENTINEL"), 0o644); err != nil {
		t.Fatal(err)
	}
	customCtx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		Args:      map[string]string{"IssueID": "mitto-1"},
	}
	out := renderSupportPrompt(t, "Support: reply to user", customCtx)
	if !strings.Contains(out, "CUSTOM-TONE-SENTINEL") {
		t.Error("custom tone.md was not inlined")
	}
	if strings.Contains(out, "Use this default tone guidance:") {
		t.Error("default tone rendered even though tone.md exists")
	}
}

// TestChannelFragmentReadInlinesRenderTime (mitto-eyf) is the acceptance test
// for the render-time inlining branch of `support/shared/channel-fragment-read`.
// It writes a fragment file to a temp workspace, renders `Support: watch
// channel` with an explicit SlackChannelID, and asserts:
//
//  1. The rendered body contains the "embedded from `<path>`" marker unique
//     to the inline branch.
//  2. The `<!-- BEGIN <path> -->` / `<!-- END <path> -->` framing markers
//     bracket the inlined content.
//  3. The exact bytes written to disk appear verbatim between the markers —
//     proving `{{ ReadFile }}` inlined the file at render time (no runtime
//     tool call needed).
//  4. Neither the MISSING nor runtime-read-fallback branches fire.
func TestChannelFragmentReadInlinesRenderTime(t *testing.T) {
	tmpDir := t.TempDir()
	channel := "C0INLINE01"
	fragmentDir := filepath.Join(tmpDir, ".mitto", "support", channel)
	if err := os.MkdirAll(fragmentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", fragmentDir, err)
	}
	// Scope fragment is the one owned by `Support: watch channel`, so it is
	// guaranteed to be referenced in that prompt's rendered body.
	scopeBody := "SCOPE-SENTINEL-XYZ: this channel triages Kubernetes questions only.\nOut of scope: billing, HR, marketing.\n"
	scopePath := filepath.Join(fragmentDir, "scope.md")
	if err := os.WriteFile(scopePath, []byte(scopeBody), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", scopePath, err)
	}

	ctx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		Args:      map[string]string{"SlackChannelID": channel},
	}
	out := renderSupportPrompt(t, "Support: watch channel", ctx)

	relPath := ".mitto/support/" + channel + "/scope.md"
	embeddedMarker := "embedded from `" + relPath + "`"
	if !strings.Contains(out, embeddedMarker) {
		t.Errorf("inline branch: rendered output missing %q — ReadFile did not inline the fragment", embeddedMarker)
	}
	beginMarker := "<!-- BEGIN " + relPath + " -->"
	endMarker := "<!-- END " + relPath + " -->"
	if !strings.Contains(out, beginMarker) {
		t.Errorf("inline branch: rendered output missing BEGIN marker %q", beginMarker)
	}
	if !strings.Contains(out, endMarker) {
		t.Errorf("inline branch: rendered output missing END marker %q", endMarker)
	}
	// The file body must appear verbatim in the render — this is the whole
	// point of mitto-eyf: auditable, deterministic inlining.
	if !strings.Contains(out, "SCOPE-SENTINEL-XYZ: this channel triages Kubernetes questions only.") {
		t.Errorf("inline branch: rendered output missing verbatim fragment body — ReadFile did not embed the file contents")
	}
	// The other two branches must not fire when the file exists.
	if strings.Contains(out, "fragment MISSING") {
		// Note: other fragments (tone.md) may still emit MISSING for this
		// channel. Guard only the scope-fragment MISSING marker to avoid
		// false positives from unrelated fragments in the same render.
		if strings.Contains(out, "`scope` fragment MISSING") {
			t.Errorf("inline branch: unexpectedly rendered MISSING for scope.md when file exists")
		}
	}
	if strings.Contains(out, "`scope` fragment (runtime read fallback)") {
		t.Errorf("inline branch: unexpectedly rendered runtime-read fallback for scope.md when channel is set")
	}
}

// TestChannelFragmentReadInlinesAtRenderTime (mitto-eyf) verifies the
// render-time-inline branch of the channel-fragment-read reader: when a
// caller supplies Args["SlackChannelID"] AND the corresponding fragment
// file exists in the workspace, the file's CONTENTS appear verbatim in the
// rendered prompt, NOT the runtime-read instructions.
//
// This is the architectural inversion introduced by mitto-eyf: the agent
// never reads the fragment at runtime — the template embeds it during
// prompt render.
func TestChannelFragmentReadInlinesAtRenderTime(t *testing.T) {
	// Set up a synthetic workspace with a `scope` fragment.
	tmpDir := t.TempDir()
	channel := "C0INLINE"
	fragDir := filepath.Join(tmpDir, ".mitto", "support", channel)
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		t.Fatal(err)
	}
	scopeBody := "SCOPE_MARKER: only triage CGW ingress/route53 questions here."
	toneBody := "TONE_MARKER: warm, concise, senior SRE voice."
	if err := os.WriteFile(filepath.Join(fragDir, "scope.md"), []byte(scopeBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fragDir, "tone.md"), []byte(toneBody), 0644); err != nil {
		t.Fatal(err)
	}

	// Render each of the two channel-scoped prompts (watch-channel and
	// continue-conversation) with SlackChannelID + workspace folder set so
	// $channel is populated at render time and ReadFile can resolve the
	// synthetic files.
	ctx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		Args: map[string]string{
			"SlackChannelID":    channel,
			"SlackWorkspaceURL": "https://example.slack.com",
		},
	}

	cases := []struct {
		prompt      string
		mustHave    []string
		mustNotHave []string
	}{
		{
			prompt: "Support: watch channel",
			mustHave: []string{
				"SCOPE_MARKER: only triage CGW ingress/route53 questions here.",
				"TONE_MARKER: warm, concise, senior SRE voice.",
				"embedded from `.mitto/support/" + channel + "/scope.md`",
			},
			mustNotHave: []string{
				"runtime read fallback",
			},
		},
		{
			prompt: "Support: continue conversation",
			mustHave: []string{
				"SCOPE_MARKER: only triage CGW ingress/route53 questions here.",
				"TONE_MARKER: warm, concise, senior SRE voice.",
			},
			mustNotHave: []string{
				"runtime read fallback",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.prompt, func(t *testing.T) {
			out := renderSupportPrompt(t, tc.prompt, ctx)
			for _, h := range tc.mustHave {
				if !strings.Contains(out, h) {
					t.Errorf("prompt %q: rendered output missing inline hallmark %q", tc.prompt, h)
				}
			}
			for _, h := range tc.mustNotHave {
				if strings.Contains(out, h) {
					t.Errorf("prompt %q: rendered output unexpectedly contains fallback hallmark %q (fragment should have been inlined)", tc.prompt, h)
				}
			}
		})
	}
}

// TestChannelFragmentReadOwnerAskWhenChannelKnownButFileMissing (mitto-eyf)
// verifies that when the channel IS known at render time but the fragment
// file is missing on disk, the owner-ask branch fires — NOT the runtime
// fallback. This ensures the auditable prompt tells the agent to create the
// file for a NEW channel instead of instructing a runtime read that would
// also fail.
func TestChannelFragmentReadOwnerAskWhenChannelKnownButFileMissing(t *testing.T) {
	tmpDir := t.TempDir() // no fragment files under it
	channel := "C0EMPTY"
	ctx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		Args: map[string]string{
			"SlackChannelID":    channel,
			"SlackWorkspaceURL": "https://example.slack.com",
		},
	}
	out := renderSupportPrompt(t, "Support: watch channel", ctx)
	// Must fire the missing-fragment branch, not the runtime-read fallback.
	if !strings.Contains(out, "fragment MISSING") {
		t.Errorf("rendered output missing the 'fragment MISSING' hallmark; got:\n%s", out[:min(len(out), 500)])
	}
	if strings.Contains(out, "runtime read fallback") {
		t.Errorf("rendered output unexpectedly contains 'runtime read fallback' — channel WAS known at render time, so this branch should not fire")
	}
}

// TestBootstrapGateFragmentRenders is a smoke test for
// _shared/support/bootstrap-gate. It exercises the three branches of the
// gate — no-channel skip, already-bootstrapped short-circuit, and
// first-run setup — as rendered by its only host (`Support: watch
// channel`), and asserts each branch's stable hallmarks appear (and the
// other branches' hallmarks do not).
func TestBootstrapGateFragmentRenders(t *testing.T) {
	t.Skip("bootstrap writes were removed by the channel-playbook read-only policy")
	// Branch A: channel unknown at render time -> skip note.
	t.Run("no_channel_skip", func(t *testing.T) {
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		}
		out := renderSupportPrompt(t, "Support: watch channel", ctx)
		mustHave := []string{
			"Bootstrap gate — skipped.",
			"No channel id supplied at render time",
		}
		for _, h := range mustHave {
			if !strings.Contains(out, h) {
				t.Errorf("no-channel branch missing hallmark %q", h)
			}
		}
		mustNotHave := []string{
			"Bootstrap gate — already bootstrapped.",
			"Bootstrap gate — first-run playbook setup",
		}
		for _, h := range mustNotHave {
			if strings.Contains(out, h) {
				t.Errorf("no-channel branch unexpectedly contains hallmark %q", h)
			}
		}
	})

	// Branch B: channel known, ALL three mandatory fragments present ->
	// idempotent short-circuit.
	t.Run("already_bootstrapped", func(t *testing.T) {
		tmpDir := t.TempDir()
		channel := "C0BOOTED"
		fragDir := filepath.Join(tmpDir, ".mitto", "support", channel)
		if err := os.MkdirAll(fragDir, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"scope.md", "investigation.md", "escalation.md"} {
			if err := os.WriteFile(filepath.Join(fragDir, name), []byte("body"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		ctx := &cel.PromptEnabledContext{
			Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
			Workspace: cel.WorkspaceContext{Folder: tmpDir},
			Args: map[string]string{
				"SlackChannelID":    channel,
				"SlackWorkspaceURL": "https://example.slack.com",
			},
		}
		out := renderSupportPrompt(t, "Support: watch channel", ctx)
		mustHave := []string{
			"Bootstrap gate — already bootstrapped.",
			"scope.md` ✓",
			"investigation.md` ✓",
			"escalation.md` ✓",
			"requested on demand by **Support: investigate**",
		}
		for _, h := range mustHave {
			if !strings.Contains(out, h) {
				t.Errorf("already-bootstrapped branch missing hallmark %q", h)
			}
		}
		// The bootstrap gate must not itself render the first-run heading
		// on an already-bootstrapped channel.
		if strings.Contains(out, "Bootstrap gate — first-run playbook setup") {
			t.Errorf("already-bootstrapped branch unexpectedly contains first-run heading")
		}
		// The bootstrap gate must not itself inline the mandatory owner-asks
		// on an already-bootstrapped channel. Scope this assertion to the
		// bootstrap-gate output slice (bounded by its two neighbouring
		// section headings in watch-channel.prompt.yaml) so the *per-iteration
		// scope gate*'s own owner-ask further down the prompt is not
		// mistaken for a bootstrap-gate leak.
		start := strings.Index(out, "## Channel bootstrap gate")
		end := strings.Index(out, "## Channel scope gate")
		if start < 0 || end < 0 || end <= start {
			t.Fatalf("could not locate bootstrap-gate slice in rendered output (start=%d end=%d)", start, end)
		}
		bootstrapSlice := out[start:end]
		for _, h := range []string{
			"Ask + write `scope.md`.",
			"Ask + write `investigation.md`.",
			"Ask + write `escalation.md`.",
		} {
			if strings.Contains(bootstrapSlice, h) {
				t.Errorf("already-bootstrapped branch: bootstrap-gate slice unexpectedly contains %q", h)
			}
		}
	})

	// Branch C: channel known, NO fragments present -> first-run setup
	// with all three mandatory owner-asks emitted and the optional opt-in
	// prompt structure present.
	t.Run("first_run_setup", func(t *testing.T) {
		tmpDir := t.TempDir() // no fragments under it
		channel := "C0FRESH"
		ctx := &cel.PromptEnabledContext{
			Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
			Workspace: cel.WorkspaceContext{Folder: tmpDir},
			Args: map[string]string{
				"SlackChannelID":    channel,
				"SlackWorkspaceURL": "https://example.slack.com",
			},
		}
		out := renderSupportPrompt(t, "Support: watch channel", ctx)
		mustHave := []string{
			"Bootstrap gate — first-run playbook setup for channel `C0FRESH`",
			"Phase 1 — Mandatory fragments",
			"Phase 2 — Optional fragments",
			// All three mandatory owner-asks are inlined:
			"Ask + write `scope.md`.",
			"Ask + write `investigation.md`.",
			"Ask + write `escalation.md`.",
			// Optional opt-in structure present:
			"Add optional playbook fragments now?",
			"Add `tone.md` only",
			"Add `resources.md` only",
			// redirects.md is called out as intentionally deferred:
			"`redirects.md`** is intentionally **not** offered here.",
			// Optional owner-asks inlined so the agent has them ready:
			"Ask + write `tone.md`.",
			"Ask + write `resources.md`.",
		}
		for _, h := range mustHave {
			if !strings.Contains(out, h) {
				t.Errorf("first-run branch missing hallmark %q", h)
			}
		}
		// redirects owner-ask must NOT be inlined by the bootstrap gate
		// (it stays on-demand under the investigate prompt).
		if strings.Contains(out, "Ask + write `redirects.md`.") {
			// It IS emitted elsewhere in watch-channel's render (nowhere —
			// watch-channel does not own redirects), but the guard is worth
			// keeping so a future refactor cannot silently smuggle it in.
			t.Errorf("first-run branch unexpectedly inlines redirects owner-ask (must remain on-demand under Support: investigate)")
		}
	})
}

// TestSlackToolsFragmentRenders is a smoke test for support/shared/slack-tools.
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
// support/shared/whats-next-mapping. Asserts the six-row core table
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
// `support/shared/ask-*.tmpl` templates introduced in mitto-da9.2.
// It renders the three owner prompts and asserts that each ask
// template's "Ask + write `<fragment>.md`" hallmark is present in
// the correct owner (and only that owner) — protecting the owner
// mapping from silent drift.
func TestAskFragmentTemplatesRender(t *testing.T) {
	t.Skip("owner ask/write paths were removed by the channel-playbook read-only policy")
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
	t.Skip("owner ask/write paths were removed by the channel-playbook read-only policy")
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
// support/shared/migrate-monolith (mitto-da9.3). It renders the two
// host prompts that invoke the migration block — `Support: watch channel`
// (scope gate) and `Support: investigate` (Step 3) — and asserts the
// stable hallmarks unique to the migration body appear in each.
//
// Also asserts that non-host support prompts do NOT accidentally include
// the migration block (owner mapping guard).
func TestMigrateMonolithFragmentRenders(t *testing.T) {
	t.Skip("automatic migration was removed by the channel-playbook read-only policy")
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
	t.Skip("automatic migration was removed by the channel-playbook read-only policy")
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

// TestChannelPlaybookReadRemoved is a regression guard for the removal
// of `support/shared/channel-playbook-read` (mitto-5cx). The fragment
// must not exist in the loaded registry, and no in-tree builtin prompt
// may call it.
func TestChannelPlaybookReadRemoved(t *testing.T) {
	installBuiltinFragmentsForTest(t)

	// (a) Registry assertion: the fragment must be gone.
	reg := CurrentFragments()
	if _, ok := reg.Get("support/shared/channel-playbook-read"); ok {
		t.Fatalf("fragment %q is still present in the loaded registry — it was removed in mitto-5cx", "support/shared/channel-playbook-read")
	}

	// (b) Zero-caller assertion: no builtin prompt may invoke the removed
	// fragment. Scan every builtin prompt's raw body for the template call.
	// Doc-comment mentions inside `{{- /* ... */ -}}` blocks and prose
	// references are OK — only actual invocations fail.
	builtinDir := "../../config/prompts/builtin"
	prompts, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Skipf("cannot load builtins from %s: %v", builtinDir, err)
	}
	invocation := `template "support/shared/channel-playbook-read"`
	for _, p := range prompts {
		if strings.Contains(p.Content, invocation) {
			t.Errorf("builtin prompt %q still invokes the removed fragment %q — use support/shared/channel-fragment-read instead", p.Name, "support/shared/channel-playbook-read")
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

// TestBeadMetadataFallbackResolvesChannel_UIInvoked (mitto-09k) is the
// acceptance-criteria integration test for the BeadMetadata render-time
// fallback. It exercises the three UI-invoked support prompts
// (`Support: check status`, `Support: reply to user`,
// `Support: gather more information`) exactly the way the Beads issue menu
// dispatches them: only `IssueID` is passed, `SlackChannelID` is intentionally
// omitted. The prompt bodies must fall back to `BeadMetadata (Arg "IssueID" "")
// "slack_channel"` and hand `channel-fragment-read.tmpl` a non-empty channel
// value — so the fragment reader emits the fragment-scoped branch (either the
// inlined body when the file exists, or the owner-ask "fragment MISSING"
// branch when it does not) and never the "runtime read fallback" branch.
//
// Setup:
//   - Fake `bd show <id> --json` returns a bead JSON carrying
//     `metadata.slack_channel = C0DERIVED`.
//   - No fragment files are written under `.mitto/support/C0DERIVED/` — this
//     drives the reader into the "fragment MISSING" branch, whose hallmark
//     ("`C0DERIVED`") pins the derived channel value into the render.
//
// Positive assertions (per acceptance criterion #3 on the bead):
//   - Rendered output contains the derived channel id, proving the derivation
//     value actually flowed into `channel-fragment-read.tmpl`.
//   - Rendered output does NOT contain "runtime read fallback" — the branch
//     that fires when `$channel` is empty at render time.
func TestBeadMetadataFallbackResolvesChannel_UIInvoked(t *testing.T) {
	installFakeBdForRender(t, `[{"id":"mitto-1","status":"open","labels":["support-question"],"metadata":{"slack_channel":"C0DERIVED"}}]`)
	tmpDir := t.TempDir()

	ctx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		// UI-invoked shape: only IssueID is passed; SlackChannelID is empty.
		Args: map[string]string{"IssueID": "mitto-1"},
	}
	consumers := []string{
		"Support: check status",
		"Support: reply to user",
		"Support: gather more information",
	}
	for _, name := range consumers {
		t.Run(name, func(t *testing.T) {
			out := renderSupportPrompt(t, name, ctx)
			// The derived channel id must appear in the render — proving the
			// $channel value flowed into channel-fragment-read.tmpl's paths
			// or messages (either "embedded from `.mitto/support/C0DERIVED/…`"
			// or the "fragment MISSING (`.mitto/support/C0DERIVED/…`)" branch).
			if !strings.Contains(out, "C0DERIVED") {
				t.Errorf("derived channel id C0DERIVED not present in render — BeadMetadata fallback did not fire")
			}
			// The runtime-read fallback branch must NOT fire: $channel is
			// non-empty at render time thanks to BeadMetadata.
			if strings.Contains(out, "runtime read fallback") {
				t.Errorf("render unexpectedly contains 'runtime read fallback' — BeadMetadata fallback did not produce a non-empty $channel")
			}
		})
	}
}

// TestBeadMetadataFallbackResolvesChannel_SessionBead covers the OTHER
// UI-invoked launch shape: the prompt is started from the conversation menu
// (or the beads-issue menu on a session already linked to a bead), so the bead
// arrives on `.Session.BeadsIssue` and NOT as an `IssueID` argument.
//
// `support/shared/target-bead-picker` already resolves the target from either
// source, but the `$channel` derivation stanza at the top of each prompt used
// to read `Arg "IssueID"` only — so this launch shape derived an empty channel
// and every channel fragment (including `post-<phase>.md`) silently degraded
// to the runtime-read fallback instead of being inlined.
func TestBeadMetadataFallbackResolvesChannel_SessionBead(t *testing.T) {
	installFakeBdForRender(t, `[{"id":"mitto-1","status":"open","labels":["support-question"],"metadata":{"slack_channel":"C0DERIVED"}}]`)
	tmpDir := t.TempDir()

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID: "s", Name: "N", HasMessages: true,
			HasBeadsIssue: true, BeadsIssue: "mitto-1",
		},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		// Conversation-menu shape: no IssueID argument at all.
		Args: map[string]string{},
	}
	consumers := []string{
		"Support: check status",
		"Support: reply to user",
		"Support: gather more information",
		"Support: investigate",
	}
	for _, name := range consumers {
		t.Run(name, func(t *testing.T) {
			out := renderSupportPrompt(t, name, ctx)
			if !strings.Contains(out, "C0DERIVED") {
				t.Errorf("derived channel id C0DERIVED not present in render — the session-linked bead did not feed BeadMetadata")
			}
			if strings.Contains(out, "runtime read fallback") {
				t.Errorf("render unexpectedly contains 'runtime read fallback' — $channel was empty despite a session-linked bead")
			}
		})
	}
}

// TestBeadMetadataFallbackLegacyBeadStillReadsAtRuntime (mitto-09k) is the
// zero-regression guard for acceptance criterion #4: a bead WITHOUT a
// `metadata.slack_channel` value (older beads, or beads created outside the
// support pipeline) must still work — the three UI-invoked support prompts
// must fall through to `channel-fragment-read.tmpl`'s "runtime read fallback"
// branch exactly as they did before this change.
//
// Setup:
//   - Fake `bd show <id> --json` returns a bead JSON with NO metadata field
//     at all. `BeadMetadata(...)` returns "" (fail-open), so `$channel`
//     remains empty at render time.
//
// Assertions:
//   - Rendered output contains "runtime read fallback" — the legacy branch
//     is still reachable.
//   - Rendered output does NOT mention the derived channel token used in the
//     sibling positive test (`C0DERIVED`), proving the negative case is
//     genuinely negative and the tests are not aliasing each other via cache.
func TestBeadMetadataFallbackLegacyBeadStillReadsAtRuntime(t *testing.T) {
	installFakeBdForRender(t, `[{"id":"mitto-legacy","status":"open","labels":["support-question"]}]`)
	tmpDir := t.TempDir()

	ctx := &cel.PromptEnabledContext{
		Session:   cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		Args:      map[string]string{"IssueID": "mitto-legacy"},
	}
	consumers := []string{
		"Support: check status",
		"Support: reply to user",
		"Support: gather more information",
	}
	for _, name := range consumers {
		t.Run(name, func(t *testing.T) {
			out := renderSupportPrompt(t, name, ctx)
			if !strings.Contains(out, "runtime read fallback") {
				t.Errorf("legacy bead without metadata: render missing 'runtime read fallback' hallmark — the legacy branch was unreachable, indicating a regression")
			}
			if strings.Contains(out, "C0DERIVED") {
				t.Errorf("legacy bead render unexpectedly contains 'C0DERIVED' — tests are aliasing via cache or fake-bd is leaking across cases")
			}
		})
	}
}

// TestBeadMetadataFallbackNotWiredIntoLoopSpawnedPrompts (mitto-09k) is a
// scope-creep guard: `continue-conversation` is driven only by its loop parent
// and always receives `SlackChannelID` explicitly, so it must NOT carry the
// bead-derived channel fallback. Uses a raw source scan (not render) so we can
// pinpoint the exact prompt bodies, independent of fragment composition.
//
// `investigate` was originally in this guarded set for the same reason, but it
// also appears in the `beadsIssues` / `conversation` menus, so it now carries
// the fallback like the other UI-invoked support prompts.
func TestBeadMetadataFallbackNotWiredIntoLoopSpawnedPrompts(t *testing.T) {
	builtinDir := "../../config/prompts/builtin"
	reg, _, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })
	SetCurrentFragments(reg)

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	fallbackStanza := `BeadMetadata $bead "slack_channel"`
	scopeGuarded := map[string]bool{
		"Support: continue conversation": true,
	}
	seen := map[string]bool{}
	for _, p := range list {
		if !scopeGuarded[p.Name] {
			continue
		}
		seen[p.Name] = true
		if strings.Contains(p.Content, fallbackStanza) {
			t.Errorf("prompt %q: unexpectedly contains the BeadMetadata fallback stanza — this loop-spawned prompt always receives SlackChannelID from its spawner", p.Name)
		}
	}
	for name := range scopeGuarded {
		if !seen[name] {
			t.Errorf("guarded prompt %q was never loaded — the guard is vacuous", name)
		}
	}
}
