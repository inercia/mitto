package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/cel"
)

// TestPilotPRCommentsFragment (mitto-g61.8) locks the first real fragment
// migration: the "Address review comments on PR #<number>" spawn payload
// used to be duplicated inline in babysit-my-prs and babysit-this-pr, and
// now lives once in config/prompts/builtin/github/pr-comments.tmpl,
// consumed via `{{ template "github/pr-comments" . }}`.
//
// Assertions:
//
//	(a) Both consumer prompts still parse via ParsePromptFile.
//	(b) Rendered against a canned PromptEnabledContext, each consumer's
//	    output contains the canonical spawn-payload string verbatim.
//	(c) pr-comments.tmpl does NOT appear in LoadPromptsFromDir results
//	    (fragments must remain hidden from menus/actions/shortcuts —
//	    mitto-g61.6 general guarantee, pinned here for this fragment).
//	(d) The fragment file is CO-LOCATED with its consumers under
//	    builtin/github/ (bead acceptance criterion: fragments live NEXT
//	    to the prompts that use them — no separate `fragments/` tree).
//	(e) The fragment does NOT appear in PromptsToWebPrompts output
//	    (the projection actually consumed by the UI menu / actions /
//	    shortcuts pipeline — stronger than the LoadPromptsFromDir
//	    check because it locks the observable menu vocabulary).
func TestPilotPRCommentsFragment(t *testing.T) {
	const builtinDir = "../../config/prompts/builtin"

	// (d) Co-location: the fragment file must live directly under
	// builtin/github/, not under a separate fragments/ tree. This is an
	// explicit bead acceptance criterion ("Fragment file is CO-LOCATED
	// with its consumers"), pinned so a future refactor that relocates
	// pilot fragments into fragments/ fails loudly here.
	fragPath := filepath.Join(builtinDir, "github", "pr-comments.tmpl")
	if st, err := os.Stat(fragPath); err != nil || st.IsDir() {
		t.Fatalf("fragment file must exist as a regular file at %s (co-location acceptance criterion): stat=%v isDir=%v", fragPath, err, st != nil && st.IsDir())
	}
	// And there must NOT be a sibling fragments/ subtree that could host
	// migrated fragments — the pilot's contract is co-location.
	if _, err := os.Stat(filepath.Join(builtinDir, "fragments")); err == nil {
		t.Fatalf("unexpected builtin/fragments/ directory found: co-located layout is the pilot's contract (see bead mitto-g61.8 acceptance)")
	}

	// Install the on-disk fragment registry on the process-wide singleton
	// so RenderPromptTemplate can resolve `{{ template "github/pr-comments" . }}`.
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, loadErrs, err := LoadFragmentsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir(builtin): %v", err)
	}
	if len(loadErrs) != 0 {
		t.Fatalf("LoadFragmentsFromDir(builtin) per-file errors: %+v", loadErrs)
	}
	if _, ok := reg.Get("github/pr-comments"); !ok {
		t.Fatalf("fragment registry missing github/pr-comments; got names=%v", reg.Names())
	}
	SetCurrentFragments(reg)

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1"},
		Args:    map[string]string{"Pr": "42", "Repository": ""},
	}

	// A distinctive substring from the canonical spawn payload. Anchoring on
	// this line is enough to prove the fragment was expanded inline (it does
	// not appear anywhere else in either consumer's body).
	const canonicalMarker = `title: "Address review comments on PR #<number>: <title>",`

	consumers := []string{
		"github/babysit-my-prs.prompt.yaml",
		"github/babysit-this-pr.prompt.yaml",
	}
	for _, rel := range consumers {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(builtinDir, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("prompt file not found at %s: %v", path, err)
			}
			// (a) Still parses.
			p, err := ParsePromptFile(rel, data, time.Now())
			if err != nil {
				t.Fatalf("ParsePromptFile(%s): %v", rel, err)
			}
			// (b) Rendered body contains the canonical marker exactly once,
			//     proving the fragment reference expanded inline.
			funcs := cel.BuildTemplateFuncMap(ctx)
			out, rerr := RenderPromptTemplate(p.Name, p.Content, ctx, funcs)
			if rerr != nil {
				t.Fatalf("RenderPromptTemplate(%s): %v", rel, rerr)
			}
			if got := strings.Count(out, canonicalMarker); got != 1 {
				t.Errorf("%s: canonical spawn-payload marker count = %d, want 1", rel, got)
			}
			// The Session.ID substitution must have happened INSIDE the
			// fragment body (self_id: "{{ .Session.ID }}" in the fragment),
			// so the rendered output must carry the concrete session id.
			if !strings.Contains(out, `self_id: "sess-1"`) {
				t.Errorf("%s: rendered output missing self_id substitution", rel)
			}
			// The block-canonical closing line must also be present.
			if !strings.Contains(out, `acp_server: <prefer "coding" or "fast" tagged server>)`) {
				t.Errorf("%s: rendered output missing canonical acp_server closing line", rel)
			}
		})
	}

	// (c) The fragment file must NOT be loadable as a prompt (LoadPromptsFromDir
	// filters strictly on .prompt.yaml). Assert that no returned prompt has
	// the fragment's derived name or file path.
	loadedPrompts, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	for _, p := range loadedPrompts {
		if strings.HasSuffix(p.Path, "github/pr-comments.tmpl") ||
			p.Name == "pr-comments" ||
			p.Name == "github/pr-comments" {
			t.Errorf("fragment leaked into prompt loader: name=%q path=%q", p.Name, p.Path)
		}
	}

	// (e) The fragment must also be absent from PromptsToWebPrompts output —
	// this is the projection the UI menu / actions / shortcuts pipeline
	// actually consumes. LoadPromptsFromDir already filters .tmpl out; this
	// second assertion locks the observable menu vocabulary directly so a
	// future refactor that shovels fragments into the web layer through a
	// different route (e.g. bypassing LoadPromptsFromDir) still fails here.
	webPrompts := PromptsToWebPrompts(loadedPrompts)
	for _, wp := range webPrompts {
		if wp.Name == "pr-comments" || wp.Name == "github/pr-comments" {
			t.Errorf("fragment leaked into UI menu vocabulary: WebPrompt.Name=%q", wp.Name)
		}
	}
}
