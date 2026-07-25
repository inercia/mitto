package prompts

import (
	"os"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestSupportPickerRenderDump is an ad-hoc dump test for eyeballing the
// rendered "Step 1: Identify the target bead" section after the
// target-bead-picker fragment refactor. Gated by MITTO_DUMP_SUPPORT_PICKER=1
// so it doesn't clutter normal `go test -v` output. Run it manually with:
//
//	MITTO_DUMP_SUPPORT_PICKER=1 go test ./internal/prompts \
//	    -run TestSupportPickerRenderDump -v
func TestSupportPickerRenderDump(t *testing.T) {
	if os.Getenv("MITTO_DUMP_SUPPORT_PICKER") == "" {
		t.Skip("dump test — set MITTO_DUMP_SUPPORT_PICKER=1 to enable")
	}
	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "s", Name: "N", HasMessages: true},
	}
	prompts := []string{
		"Support: check status",
		"Support: gather more information",
		"Support: reply to user",
		"Support: investigate",
		"Support: housekeeping",
		"Support: watch channel",
	}
	for _, name := range prompts {
		out := renderSupportPrompt(t, name, ctx)
		// Extract the what's-next mapping section (or the Slack tools + Step 1 sections if there is no what's-next).
		idx := strings.Index(out, "what's next")
		if idx < 0 {
			idx = strings.Index(out, "## Slack tools")
		}
		if idx < 0 {
			idx = strings.Index(out, "## Step 1: Identify the target bead")
		}
		if idx < 0 {
			t.Logf("%s: no anchor section found", name)
			continue
		}
		// Back up to start of line for readability.
		lineStart := strings.LastIndex(out[:idx], "\n") + 1
		tail := out[lineStart:]
		if end := strings.Index(tail, "\n\n## "); end >= 0 {
			tail = tail[:end+3]
		}
		t.Logf("========== %s ==========\n%s\n========== END ==========", name, tail)
	}
}
