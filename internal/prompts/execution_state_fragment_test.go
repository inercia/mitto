package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

func TestExecutionStateFragmentsRender(t *testing.T) {
	installBuiltinFragmentsForTest(t)

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)

	t.Run("read", func(t *testing.T) {
		out, err := RenderPromptTemplateWithFragments(
			"execution-state-read",
			`{{ template "beads-issues/shared/execution-state-read" "mitto-abc" }}`,
			ctx, funcs, CurrentFragments(),
		)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		for _, want := range []string{
			"bd show mitto-abc --long --json",
			`jq -r '.[0].notes // ""'`,
			`/<!-- mitto:execution-state:v1 -->/`,
			`/<!-- \/mitto:execution-state -->/`,
			"Read recent comments only",
			"byte-for-byte",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("read fragment missing %q\n%s", want, out)
			}
		}
	})

	t.Run("write", func(t *testing.T) {
		out, err := RenderPromptTemplateWithFragments(
			"execution-state-write",
			`{{ template "beads-issues/shared/execution-state-write" "mitto-abc" }}`,
			ctx, funcs, CurrentFragments(),
		)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		for _, want := range []string{
			`id="mitto-abc"`,
			`before=$(bd show "$id" --long --json`,
			`after=$(bd show "$id" --long --json`,
			`[ "$after" = "$before" ] ||`,
			"notes changed; state not saved",
			`bd update "$id" --notes`,
			"<!-- mitto:execution-state:v1 -->",
			"<!-- /mitto:execution-state -->",
			"never use `bd update --append-notes`",
			"preserve all other notes",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("write fragment missing %q\n%s", want, out)
			}
		}
		if strings.Contains(out, `bd update "mitto-abc" --append-notes`) {
			t.Errorf("write fragment must not append notes:\n%s", out)
		}
	})
}
