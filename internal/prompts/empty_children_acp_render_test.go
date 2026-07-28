package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestBuiltinPromptsRenderEmptyBackticksForEmptyChildrenAndACP reproduces the
// mitto-znv bug: builtin prompts that print `{{ .Children.MCPText }}` and
// `{{ .ACP.AvailableText }}` inside literal backticks render an empty pair of
// backticks (`Existing children: “ “) when the underlying slice is empty,
// leaving the orchestrator with no human-readable placeholder.
//
// Approach A from the bead: wrap the substitution in the existing
// `Default "(none)" ...` template helper so an empty list renders as
// `Existing children: `(none)“ instead of `Existing children: “ “.
//
// This test renders three of the six unguarded prompts identified in the
// mitto-znv audit against an empty PromptEnabledContext (no children, no ACP
// servers), then asserts that neither empty-backtick artifact appears in the
// output. It FAILS today because the prompts are still unguarded; it will pass
// once approach A lands.
func TestBuiltinPromptsRenderEmptyBackticksForEmptyChildrenAndACP(t *testing.T) {
	// Empty context — no children, no ACP servers, nothing to substitute.
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N"},
		Args:    map[string]string{},
	}

	// The two literal empty-backtick artifacts from the bead description.
	const (
		emptyChildrenArtifact = "Existing children: ``"
		emptyACPArtifact      = "Available ACP servers: ``"
	)

	// Spot-check a representative slice of the 6 `Existing children:` +
	// 19 `Available ACP servers:` unguarded prompts (see mitto-znv audit).
	// Both surfaces coexist in the beads-issues/loop-* prompts, so those
	// two exercise both empty-render paths in one shot; publish-post
	// exercises the children-only surface on a non-loop prompt.
	type row struct {
		promptName   string
		forbidNeedle []string
	}
	rows := []row{
		{"Loop fixing bug", []string{emptyChildrenArtifact, emptyACPArtifact}},
		{"Loop processing tasks", []string{emptyChildrenArtifact, emptyACPArtifact}},
		{"Blog: publish", []string{emptyChildrenArtifact}},
	}

	for _, r := range rows {
		out := renderBuiltinPromptWithFragments(t, r.promptName, ctx)
		for _, needle := range r.forbidNeedle {
			if strings.Contains(out, needle) {
				t.Errorf("prompt %q: rendered output contains empty-backtick artifact %q "+
					"(mitto-znv): the empty list should render as a human-readable "+
					`placeholder like "(none)" instead of an empty pair of backticks`,
					r.promptName, needle)
			}
		}
	}
}
