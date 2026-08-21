package prompts

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/cel"
)

// TestStartWorkingOnReady_RespectsInFlightClaimProtocol pins the mitto-kpgs
// contract: the "Start working on ready" prompt
// (config/prompts/builtin/beads/work.prompt.yaml) must participate in the
// mitto-ial `in-flight` claim protocol like every L2 loop-driver does, so it
// cannot steal a bead an active driver already holds and cannot leave ghost
// claims behind on close.
//
// Before the fix, the rendered prompt contains none of these hallmarks —
// Step 1 scans with a bare `bd ready`/`bd list` (no `--exclude-label
// in-flight`), Step 3 claims with a bare `bd update <id> --claim` (no
// `claimed_by` read), and Step 10 closes with no claim-clear — so this test
// fails. That failure IS the reproduction (see the mitto-kpgs investigation
// comment for the full analysis). The fix phase adds the three edit points
// below and turns this test green.
func TestStartWorkingOnReady_RespectsInFlightClaimProtocol(t *testing.T) {
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

	const promptName = "Start working on ready"
	var body string
	for _, p := range list {
		if p.Name == promptName {
			body = p.Content
			break
		}
	}
	if body == "" {
		t.Fatalf("prompt %q not found in builtin corpus", promptName)
	}

	ctx := &cel.PromptEnabledContext{
		Session: cel.SessionContext{ID: "sess-1", Name: "N", HasMessages: true},
	}
	funcs := cel.BuildTemplateFuncMap(ctx)
	out, err := RenderPromptTemplate(promptName, body, ctx, funcs)
	if err != nil {
		t.Fatalf("render %q: %v", promptName, err)
	}

	// A. Step 1 scan must exclude beads a driver already holds. Both the
	// primary `bd ready` scan and the `bd list` fallback need the exclusion —
	// a bead in-flight has status=open (claim-entry.tmpl keeps it open on
	// purpose), so only the label exclusion catches it.
	if !strings.Contains(out, "bd ready --exclude-label in-flight") {
		t.Errorf("%q: Step 1 `bd ready` scan is missing `--exclude-label in-flight` — "+
			"beads held by an active driver (status stays open per mitto-ial) are still "+
			"offered to the user (mitto-kpgs)", promptName)
	}
	if !strings.Contains(out, "bd list --status open --exclude-label in-flight") {
		t.Errorf("%q: Step 1 `bd list` fallback is missing `--exclude-label in-flight` "+
			"(mitto-kpgs)", promptName)
	}

	// B. Step 3 claim must read the existing claim before writing, so a
	// bead claimed between the scan and the user's (possibly free-text)
	// selection is not silently clobbered. Ground the hallmark in the exact
	// jq lookup claim-entry.tmpl already uses.
	if !strings.Contains(out, "metadata.claimed_by") {
		t.Errorf("%q: Step 3 claim step does not read `metadata.claimed_by` before "+
			"`bd update --claim` — it can steal a bead an active driver holds "+
			"(mitto-kpgs)", promptName)
	}

	// C. Step 10 close must clear the passive claim before `bd close`, or a
	// bead this prompt claimed (once B is fixed) leaves a ghost claim in the
	// archive (mitto-ial invariant).
	if !strings.Contains(out, "--remove-label in-flight") ||
		!strings.Contains(out, "--unset-metadata claimed_by") {
		t.Errorf("%q: Step 10 does not clear the `in-flight` label + `claimed_*` "+
			"metadata before `bd close` — ghost claims survive into the archive "+
			"(mitto-ial, mitto-kpgs)", promptName)
	}
}
