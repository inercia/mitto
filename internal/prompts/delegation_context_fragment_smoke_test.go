package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestDelegationContextFragmentRenders is a smoke test for the
// _shared/delegation-context fragment: renders each of the 6 code/ci
// consumers in BOTH branches (no children, and children present) and
// asserts the fragment's hallmark strings render — session ID line,
// ACP catalog placeholder text, and the optional "Existing children:"
// heading (must appear only when .Children.AllText is non-empty).
func TestDelegationContextFragmentRenders(t *testing.T) {
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
		"Cleanup Code", "Optimize", "Refactor", "Simplify",
		"Fix CI", "Fix errors",
	}

	// no-children branch — Session.ID + ACP catalog render; "Existing
	// children:" must NOT appear.
	for _, name := range consumers {
		body, ok := byName[name]
		if !ok {
			t.Errorf("prompt %q not found", name)
			continue
		}
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "sess-42", Name: "N", HasMessages: true},
			ACP: cel.ACPContext{Available: []cel.ACPServerInfo{
				{Name: "auggie", Type: "auggie"},
				{Name: "claude-code", Type: "claude-code"},
			}},
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q (no children): %v", name, err)
			continue
		}
		if !strings.Contains(out, "**Session context for delegation:**") {
			t.Errorf("%q (no children): missing 'Session context for delegation:' heading", name)
		}
		if !strings.Contains(out, "Your session ID is `sess-42`") {
			t.Errorf("%q (no children): missing session ID line", name)
		}
		if !strings.Contains(out, "auggie") || !strings.Contains(out, "claude-code") {
			t.Errorf("%q (no children): missing ACP catalog entries", name)
		}
		if strings.Contains(out, "Existing children:") {
			t.Errorf("%q (no children): 'Existing children:' leaked when Children.All was empty", name)
		}
	}

	// children-present branch — "Existing children:" heading + child
	// content must both render.
	for _, name := range consumers {
		body := byName[name]
		ctx := &cel.PromptEnabledContext{
			Session: cel.SessionContext{ID: "sess-42", Name: "N", HasMessages: true},
			ACP: cel.ACPContext{Available: []cel.ACPServerInfo{
				{Name: "auggie", Type: "auggie"},
			}},
			Children: cel.ChildrenContext{
				All: []cel.ChildInfo{
					{ID: "child-1", Name: "worker", ACPServer: "auggie", Origin: "auto"},
				},
				Count: 1, Exists: true,
			},
		}
		funcs := cel.BuildTemplateFuncMap(ctx)
		out, err := RenderPromptTemplate(name, body, ctx, funcs)
		if err != nil {
			t.Errorf("render %q (children): %v", name, err)
			continue
		}
		if !strings.Contains(out, "Existing children:") {
			t.Errorf("%q (children): missing 'Existing children:' heading when Children.All was set", name)
		}
		if !strings.Contains(out, "child-1") {
			t.Errorf("%q (children): missing child-1 identifier in rendered output", name)
		}
	}
}
