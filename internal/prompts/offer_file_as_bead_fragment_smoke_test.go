package prompts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestOfferFileAsBeadFragmentRenders is a smoke test for the
// _shared/offer-file-as-bead fragment: renders each of its four consuming
// prompts in both branches (beads present → fragment prose emitted, beads
// absent → fragment fully empty) and asserts the surrounding caller text is
// preserved so an accidental delete of one of the caller's own approval
// options is caught.
//
// The fragment is gated on `and (CommandExists "bd") (DirExists ".beads")`.
// DirExists is controlled deterministically via Workspace.Folder — a temp
// dir with (or without) a .beads/ subdir. CommandExists("bd") relies on the
// real PATH: `bd` is a hard project requirement (see AGENTS.md) so the
// present-branch assumes it is installed; the absent-branch relies on the
// `and` short-circuit (no .beads → false regardless of `bd`) and therefore
// does NOT depend on bd's presence.
func TestOfferFileAsBeadFragmentRenders(t *testing.T) {
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

	// The fragment must be registered under this exact key — used by all
	// four callers via {{ template "_shared/offer-file-as-bead" . }}.
	if _, ok := reg.Get("_shared/offer-file-as-bead"); !ok {
		t.Fatalf("fragment %q not registered", "_shared/offer-file-as-bead")
	}

	list, err := LoadPromptsFromDir(builtinDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir(builtin): %v", err)
	}
	byName := map[string]string{}
	for _, p := range list {
		byName[p.Name] = p.Content
	}

	// The four consumers wired to the fragment. `nearbyMarker` is a stable
	// substring from the caller's own text near the fragment call — used
	// to prove the caller still renders when the fragment expands to
	// nothing.
	type consumer struct{ name, nearbyMarker string }
	consumers := []consumer{
		{"Refactor", "### 4. Execute"},
		{"Optimize", "### 4. Execute"},
		{"Cleanup Code", "### 4. Execute"},
		{"Simplify", "### 4. Execute"},
	}

	// Hallmark substring from the fragment's rendered prose. Present iff
	// both gates hold.
	const fragmentHallmark = "**Optional: file this as a beads issue (or epic) instead of executing now.**"

	// ─── Present branch: workspace has .beads/ ────────────────────────
	presentDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(presentDir, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}

	// If `bd` is not on PATH, the fragment's `and` clause is false even
	// with .beads present, so the "present branch" assertions would spurious
	// -fail. Skip them in that unlikely case rather than pretend to test.
	bdOnPath := commandExistsForTest("bd")

	for _, c := range consumers {
		body, ok := byName[c.name]
		if !ok {
			t.Errorf("prompt %q not found", c.name)
			continue
		}
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{
				ID: "sess-1", Name: "N", HasMessages: true,
			},
			Workspace: cel.WorkspaceContext{Folder: presentDir},
			Args:      map[string]string{},
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(c.name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q (present): %v", c.name, err)
			continue
		}
		if !strings.Contains(out, c.nearbyMarker) {
			t.Errorf("%q (present): missing caller-owned marker %q", c.name, c.nearbyMarker)
		}
		if bdOnPath {
			if !strings.Contains(out, fragmentHallmark) {
				t.Errorf("%q (present): missing fragment hallmark %q", c.name, fragmentHallmark)
			}
			// Safety-rail lines are load-bearing — they turn the fragment
			// from "helpful hint" into a bounded policy.
			for _, rail := range []string{
				"Do **NOT** run `bd close`",
				"Do **NOT** claim the issue",
			} {
				if !strings.Contains(out, rail) {
					t.Errorf("%q (present): missing safety rail %q", c.name, rail)
				}
			}
		} else {
			t.Logf("%q (present): `bd` not on PATH, skipping fragment-body assertions", c.name)
		}
	}

	// ─── Absent branch: workspace has no .beads/ ──────────────────────
	absentDir := t.TempDir() // fresh temp dir; no .beads
	for _, c := range consumers {
		body := byName[c.name]
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{
				ID: "sess-1", Name: "N", HasMessages: true,
			},
			Workspace: cel.WorkspaceContext{Folder: absentDir},
			Args:      map[string]string{},
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(c.name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q (absent): %v", c.name, err)
			continue
		}
		// Caller-owned text must survive the fragment collapsing to
		// empty — proves callers don't accidentally require fragment
		// output to close their own sections.
		if !strings.Contains(out, c.nearbyMarker) {
			t.Errorf("%q (absent): missing caller-owned marker %q", c.name, c.nearbyMarker)
		}
		// Fragment prose must NOT leak when .beads is missing.
		if strings.Contains(out, fragmentHallmark) {
			t.Errorf("%q (absent): fragment prose leaked when .beads was missing", c.name)
		}
	}
}

// commandExistsForTest mirrors internal/cel.commandExists without importing
// an unexported symbol; kept private to this test file.
func commandExistsForTest(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
