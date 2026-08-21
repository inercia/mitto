package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestReadTemplateEndToEnd is the integration smoke test for the
// ReadTemplate FuncMap binding (mitto-hiy). It rides the same rendering path
// as a real prompt dispatch — internal/prompts.RenderPromptTemplate, which
// attaches the fragment registry and delegates to Go text/template — and
// asserts that ReadTemplate:
//
//   - reads a workspace-relative fixture file,
//   - sub-renders it as a template against the outer .Args + FuncMap, and
//   - surfaces the read step's fail-open semantics (missing file → "") end-
//     to-end, not just in the unit-level readTemplate() helper.
//
// The unit-level coverage in internal/cel/templatefuncs_readtemplate_test.go
// exercises readTemplate() directly; this smoke test pins the wiring so a
// future change that decouples the FuncMap binding from the rendering path
// (or drops it from BuildTemplateFuncMap) fails here loudly rather than in a
// downstream prompt dispatch.
func TestReadTemplateEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()

	// Fixture files: one variable-expanding fragment, one verbatim body.
	const fragBody = `Hello {{ Upper .Args.Name }} from {{ .Args.Channel }}!`
	if err := os.WriteFile(filepath.Join(tmpDir, "greet.md"), []byte(fragBody), 0644); err != nil {
		t.Fatalf("write greet.md: %v", err)
	}
	const verbatimBody = "verbatim fragment body\nno metacharacters\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "verbatim.md"), []byte(verbatimBody), 0644); err != nil {
		t.Fatalf("write verbatim.md: %v", err)
	}

	ctx := &cel.PromptEnabledContext{
		Workspace: cel.WorkspaceContext{Folder: tmpDir},
		Args:      map[string]string{"Name": "alice", "Channel": "beads"},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	// A synthetic prompt body that mirrors the intended user idiom:
	//   {{ ReadTemplate "path/to/fragment.md" . }}
	// The outer prompt threads .Args + folder via ctx; the included file must
	// see the same .Args expansion as if inlined.
	const promptBody = `--- variable ---
{{ ReadTemplate "greet.md" . }}
--- verbatim ---
{{ ReadTemplate "verbatim.md" . }}
--- missing (fail-open) ---
[{{ ReadTemplate "absent.md" . }}]
--- end ---`

	got, err := RenderPromptTemplate("readtemplate-smoke", promptBody, ctx, funcs)
	if err != nil {
		t.Fatalf("RenderPromptTemplate: %v", err)
	}

	// Variable-expanding fragment: .Args + FuncMap helper both applied.
	if !strings.Contains(got, "Hello ALICE from beads!") {
		t.Errorf("expected variable expansion in included fragment; got:\n%s", got)
	}
	// Verbatim fragment: fast path preserves the body byte-for-byte (minus
	// trailing newline handling that ReadFile does not add or strip).
	if !strings.Contains(got, "verbatim fragment body\nno metacharacters") {
		t.Errorf("expected verbatim fragment body; got:\n%s", got)
	}
	// Missing file: read step is fail-open, so the include renders as empty
	// and the surrounding brackets collapse to `[]`.
	if !strings.Contains(got, "--- missing (fail-open) ---\n[]\n") {
		t.Errorf("expected missing include to render as empty (fail-open); got:\n%s", got)
	}
}
