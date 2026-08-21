package prompts

import (
	"os"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestSupportFragmentRenderDump is a diagnostic test intended for humans
// eyeballing whitespace and inlining after a support/* fragment refactor.
// It's gated behind the env var MITTO_DUMP_SUPPORT_RENDER=1 to avoid
// cluttering normal `go test -v` output. To run it:
//
//	MITTO_DUMP_SUPPORT_RENDER=1 go test ./internal/prompts \
//	    -run TestSupportFragmentRenderDump -v
func TestSupportFragmentRenderDump(t *testing.T) {
	if os.Getenv("MITTO_DUMP_SUPPORT_RENDER") == "" {
		t.Skip("dump test — set MITTO_DUMP_SUPPORT_RENDER=1 to enable")
	}
	prev := CurrentFragments()
	t.Cleanup(func() { SetCurrentFragments(prev) })

	reg, _, err := LoadFragmentsFromDir("../../config/prompts/builtin")
	if err != nil {
		t.Fatalf("LoadFragmentsFromDir: %v", err)
	}
	SetCurrentFragments(reg)
	list, err := LoadPromptsFromDir("../../config/prompts/builtin")
	if err != nil {
		t.Fatalf("LoadPromptsFromDir: %v", err)
	}
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	targets := map[string]bool{
		"Support: check status":  true,
		"Support: reply to user": true,
	}
	for _, p := range list {
		if !targets[p.Name] {
			continue
		}
		out, err := RenderPromptTemplate(p.Name, p.Content, ctx, funcs)
		if err != nil {
			t.Errorf("%s: %v", p.Name, err)
			continue
		}
		// Extract just the CRITICAL + Bead-writing region.
		start := strings.Index(out, "## CRITICAL:")
		end := strings.Index(out, "## Slack tools")
		if start < 0 || end < 0 || end <= start {
			t.Errorf("%s: markers not found", p.Name)
			continue
		}
		t.Logf("========== %s ==========\n%s\n========== END ==========", p.Name, out[start:end])
	}
}
