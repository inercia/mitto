package processors

import (
	"os"
	"strings"
	"testing"
)

// TestBeadsReadyTasksProcessor_ExcludeInFlightLabel is a smoke test for the
// builtin `beads-ready-tasks` processor (mitto-kvq): it loads the YAML file
// directly, verifies it parses into a valid processor definition, and pins
// that the injected reminder text (a) instructs agents to run
// `bd ready --exclude-label in-flight` instead of a bare `bd ready`, and (b)
// explains why the filter matters (in-flight is an automated-driver claim,
// omitting the filter risks a collision). A regression that reverts the
// reminder to the bare `bd ready` form is caught here.
func TestBeadsReadyTasksProcessor_ExcludeInFlightLabel(t *testing.T) {
	const path = "../../config/processors/builtin/beads-ready-tasks.yaml"

	loader := NewLoader("../../config/processors/builtin", nil)
	proc, err := loader.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if proc == nil {
		t.Fatalf("LoadFile(%q): nil processor (file may be empty)", path)
	}

	if proc.Name != "beads-ready-tasks" {
		t.Errorf("processor name = %q, want %q", proc.Name, "beads-ready-tasks")
	}
	if !proc.IsTextMode() {
		t.Errorf("processor must be text-mode (Text set, no Command/Prompt); got Text=%q Command=%q Prompt=%q",
			proc.Text, proc.Command, proc.Prompt)
	}

	// Hallmarks the reminder text MUST contain (mitto-kvq companion to mitto-ial):
	// the explicit filtered command, the explanation of what the label means,
	// and the collision-risk warning so an operator understands the "why".
	hallmarks := []string{
		"bd ready --exclude-label in-flight",
		"in-flight",
		"automated drivers",
		"colliding with an active driver",
	}
	for _, h := range hallmarks {
		if !strings.Contains(proc.Text, h) {
			t.Errorf("beads-ready-tasks reminder text missing hallmark %q\n---\ntext:\n%s\n---", h, proc.Text)
		}
	}

	// Anti-regression: the bare `bd ready` recommendation (no --exclude-label
	// flag) must NOT appear in the reminder body — that was the pre-mitto-kvq
	// form and reintroducing it silently routes a human straight into a
	// driver-held bead. We look for the substring "run `bd ready`" (backtick-
	// fenced, matching the prose voice) followed immediately by whitespace or
	// end-of-line rather than the ` --exclude-label` continuation.
	if strings.Contains(proc.Text, "run `bd ready` ") || strings.Contains(proc.Text, "run `bd ready`\n") {
		t.Errorf("beads-ready-tasks reminder still contains the pre-mitto-kvq bare \"run `bd ready`\" form; the --exclude-label in-flight filter must be included in the recommended command\n---\ntext:\n%s\n---", proc.Text)
	}
}

// TestAgentsMD_DocumentsInFlightLabelExclusion pins the AGENTS.md side of the
// mitto-kvq contract: the repo's human-facing agent instructions must (a)
// show the `--exclude-label in-flight` form in the Quick Reference and (b)
// carry a dedicated section that documents the `in-flight` label as
// automated-driver-only and meant to be excluded from human bd queries. The
// auto-managed `<!-- BEGIN BEADS INTEGRATION -->` block is off-limits to
// hand edits, so this test scopes its assertions to the pre-BEGIN portion of
// the file where the new documentation lives.
func TestAgentsMD_DocumentsInFlightLabelExclusion(t *testing.T) {
	const path = "../../AGENTS.md"

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	content := string(raw)

	// Scope: only inspect the human-editable prefix before the auto-managed
	// BEADS INTEGRATION block; that block is tool-regenerated (hash header)
	// and hand-editing it is explicitly forbidden.
	const beginMarker = "<!-- BEGIN BEADS INTEGRATION"
	prefix := content
	if idx := strings.Index(content, beginMarker); idx >= 0 {
		prefix = content[:idx]
	}

	// Quick Reference must recommend the filtered command form.
	if !strings.Contains(prefix, "bd ready --exclude-label in-flight") {
		t.Errorf("AGENTS.md pre-BEGIN section missing `bd ready --exclude-label in-flight` in the Quick Reference (mitto-kvq)")
	}

	// A dedicated subsection must explain the automated-driver-only nature
	// of the `in-flight` label and point at the L1 orchestrator source.
	hallmarks := []string{
		"Automated-driver `in-flight` label",
		"automated-driver-only",
		"excluded from human queries",
		"mitto-ial",
	}
	for _, h := range hallmarks {
		if !strings.Contains(prefix, h) {
			t.Errorf("AGENTS.md pre-BEGIN section missing hallmark %q (mitto-kvq)", h)
		}
	}
}
