package prompts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

// TestSessionContextFragment (mitto-g61.9) locks the second real fragment
// migration: the "## Session Context" preamble that appeared inline in 64
// builtin prompts is extracted once into config/prompts/builtin/_shared/
// session-context.tmpl and consumed via
// `{{ template "_shared/session-context" . }}`.
//
// Unlike the pilot fragment (github/shared/pr-comments — scoped under
// builtin/github/shared/), the session-context fragment is cross-topic: it is used by
// prompts under beads-issues/, beads/, ci/, code/, docs/, github/, jira/,
// loop/, and support/, so the Implementation deliberately placed it under
// a top-level `_shared/` prefix. This test pins that layout.
//
// Assertions:
//
//	(a) Fragment file exists at builtin/_shared/session-context.tmpl.
//	(b) On-disk fragment registry loads it under name "_shared/session-context".
//	(c) The fragment is HIDDEN from LoadPromptsFromDir results (no leak into
//	    the prompt loader — same guarantee as the pilot).
//	(d) The fragment is HIDDEN from PromptsToWebPrompts output (no leak
//	    into the UI menu / actions / shortcuts vocabulary).
//	(e) No .prompt.yaml under builtin/ still contains an inline
//	    "## Session Context" heading (structural regression guard).
//	(f) A representative floor of consumers (>= 63) references the fragment
//	    via `{{ template "_shared/session-context" . }}`.
//	(g) Every discovered consumer renders — with the shared registry
//	    installed — to output that contains the fully-expanded canonical
//	    block exactly once with the concrete Session.ID substituted in.
//	(h) All 64 consumers produce byte-identical fragment expansions,
//	    proving the migration is uniform (this is the render-stability
//	    acceptance criterion: same input Session context → identical
//	    Session-Context bytes across every consumer).
func TestSessionContextFragment(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"

	// (a) Fragment must exist as a regular file at the top-level _shared/
	// prefix — cross-topic reuse justifies the non-co-located layout.
	fragPath := filepath.Join(builtinDir, "_shared", "session-context.tmpl")
	if st, err := os.Stat(fragPath); err != nil || st.IsDir() {
		t.Fatalf("fragment file must exist as a regular file at %s: stat=%v isDir=%v", fragPath, err, st != nil && st.IsDir())
	}

	// Install the on-disk fragment registry on the process-wide singleton
	// so RenderPromptTemplate can resolve `{{ template "_shared/session-context" . }}`.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	// (b) Registered under the expected name.
	if _, ok := reg.Get("_shared/session-context"); !ok {
		t.Fatalf("fragment registry missing _shared/session-context; got names=%v", reg.Names())
	}
	SetCurrentFragments(reg)

	// (c) The fragment is filtered out of LoadPromptsFromDir (must not be
	// treated as a prompt).
	loadedPrompts, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	for _, p := range loadedPrompts {
		if strings.HasSuffix(p.Path, "_shared/session-context.tmpl") ||
			p.Name == "session-context" ||
			p.Name == "_shared/session-context" {
			t.Errorf("fragment leaked into prompt loader: name=%q path=%q", p.Name, p.Path)
		}
	}

	// (d) The fragment is filtered out of PromptsToWebPrompts (must not
	// appear in the UI menu / actions / shortcuts vocabulary).
	webPrompts := PromptsToWebPrompts(loadedPrompts)
	for _, wp := range webPrompts {
		if wp.Name == "session-context" || wp.Name == "_shared/session-context" {
			t.Errorf("fragment leaked into UI menu vocabulary: WebPrompt.Name=%q", wp.Name)
		}
	}

	// (e) Structural regression guard: no .prompt.yaml file may still carry
	// the inline preamble heading — if one does, the migration has drifted
	// and this fragment no longer captures every consumer.
	//
	// A caller may legitimately embed the literal substring "## Session
	// Context" in prose (e.g. as documentation of what the fragment emits),
	// so this guard anchors on the exact 2-line block that the migration
	// removed: the heading followed on the next non-empty line by the
	// "Your session ID is `{{ .Session.ID }}`" sentence. Any prompt still
	// carrying that exact inline pair is an un-migrated consumer.
	var stragglers []string
	if err := filepath.Walk(builtinDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".prompt.yaml") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		text := string(body)
		// Look for the inline preamble heading with the immediately-following
		// session-id sentence within a small window (allowing for blank line +
		// leading indentation). The migration replaced this exact pair with a
		// single fragment-ref line, so any remaining hit is un-migrated.
		const heading = "## Session Context"
		const sentence = "Your session ID is `{{ .Session.ID }}`"
		if idx := strings.Index(text, heading); idx >= 0 {
			window := text[idx:]
			if len(window) > 400 {
				window = window[:400]
			}
			if strings.Contains(window, sentence) {
				rel, _ := filepath.Rel(builtinDir, path)
				stragglers = append(stragglers, rel)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", builtinDir, err)
	}
	if len(stragglers) > 0 {
		sort.Strings(stragglers)
		t.Errorf("un-migrated consumers still carry inline '## Session Context' + session-id sentence:\n  %s", strings.Join(stragglers, "\n  "))
	}

	// (f) Discover consumers dynamically: any .prompt.yaml that references
	// the fragment. Floor at the Implementation-committed count (64).
	var consumers []string
	if err := filepath.Walk(builtinDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".prompt.yaml") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(body), `template "_shared/session-context"`) {
			rel, _ := filepath.Rel(builtinDir, path)
			consumers = append(consumers, rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", builtinDir, err)
	}
	sort.Strings(consumers)
	// The Implementation comment on mitto-g61.9 records 64 consumers landed
	// in commit 65a2a923; use that as a lower-bound floor so accidentally
	// deleting the fragment reference from many consumers fails the test,
	// while still permitting future additions.
	const consumerFloor = 63
	if len(consumers) < consumerFloor {
		t.Errorf("session-context fragment has too few consumers: got %d, want >= %d\nconsumers=%v", len(consumers), consumerFloor, consumers)
	}

	// The canonical expansion the fragment must emit when Session.ID is set.
	// Note the leading whitespace is stripped by the fragment's `{{- ... -}}`
	// action; the heading + sentence pair is what the rendered output must
	// contain verbatim, once, per consumer.
	const canonicalHeading = "## Session Context"
	const canonicalSentence = "Your session ID is `sess-fragtest` — use this as `self_id` for all `mitto_*` MCP tool calls."

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{
			ID:            "sess-fragtest",
			Name:          "Fragment Test",
			BeadsIssue:    "mitto-g61.9",
			HasBeadsIssue: true,
		},
		// A generous Args map so consumers that gate content on IssueID /
		// Condition / Pr / Repository etc. still render their prose.
		Args: map[string]string{
			"IssueID":       "mitto-g61.9",
			"Condition":     "all tests pass",
			"Pr":            "42",
			"Repository":    "",
			"ParentIssueID": "mitto-g61",
			"Scope":         "test",
			"OwnedPaths":    "",
		},
	}

	// (g) + (h): render every consumer and check that the fragment expansion
	// text is byte-identical across all of them.
	for _, rel := range consumers {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(builtinDir, rel)
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("read %s: %v", path, rerr)
			}
			p, perr := ParsePromptFile(rel, data, time.Now())
			if perr != nil {
				t.Fatalf("ParsePromptFile(%s): %v", rel, perr)
			}
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, rerr := RenderPromptTemplate(p.Name, p.Content, ctx, funcs)
			if rerr != nil {
				t.Fatalf("RenderPromptTemplate(%s): %v", rel, rerr)
			}
			// Heading + sentence must both be present at least once.
			if !strings.Contains(out, canonicalHeading) {
				t.Errorf("%s: rendered output missing canonical heading %q", rel, canonicalHeading)
			}
			if !strings.Contains(out, canonicalSentence) {
				t.Errorf("%s: rendered output missing canonical sentence %q", rel, canonicalSentence)
			}
			// Session.ID substitution: the concrete id must appear in
			// the rendered body (proves the fragment resolved the
			// {{ .Session.ID }} variable rather than emitting an empty).
			if !strings.Contains(out, "`sess-fragtest`") {
				t.Errorf("%s: rendered output missing concrete Session.ID substitution", rel)
			}
		})
	}

	// (h) Uniformity: extract the canonical expansion window (heading +
	// blank line + sentence) from each consumer's rendered output and
	// require them all to be byte-identical. Any drift here means a
	// consumer's copy of the fragment resolved to different text — which
	// should be impossible with a single shared registry entry, and if it
	// ever happens is a regression worth failing on.
	//
	// This subtest runs sequentially after the per-consumer subtests
	// (t.Run parents wait for children) so the fragment expansion is
	// already validated per-consumer before uniformity is checked.
	t.Run("uniform-expansion-across-consumers", func(t *testing.T) {
		var canonical string
		var canonicalFrom string
		for _, rel := range consumers {
			path := filepath.Join(builtinDir, rel)
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("read %s: %v", path, rerr)
			}
			p, perr := ParsePromptFile(rel, data, time.Now())
			if perr != nil {
				t.Fatalf("ParsePromptFile(%s): %v", rel, perr)
			}
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, rerr := RenderPromptTemplate(p.Name, p.Content, ctx, funcs)
			if rerr != nil {
				t.Fatalf("RenderPromptTemplate(%s): %v", rel, rerr)
			}
			// Slice the block from the heading up to (and including) the
			// canonical sentence — the "window" all consumers must share.
			headIdx := strings.Index(out, canonicalHeading)
			if headIdx < 0 {
				t.Fatalf("%s: no canonical heading in rendered output", rel)
			}
			endIdx := strings.Index(out[headIdx:], canonicalSentence)
			if endIdx < 0 {
				t.Fatalf("%s: canonical sentence not found after heading", rel)
			}
			endIdx += headIdx + len(canonicalSentence)
			window := out[headIdx:endIdx]
			if canonical == "" {
				canonical = window
				canonicalFrom = rel
				continue
			}
			if window != canonical {
				t.Errorf("session-context expansion drift in %s:\n---- canonical (from %s) ----\n%q\n---- got ----\n%q\n----",
					rel, canonicalFrom, canonical, window)
			}
		}
	})
}
