package processors

import (
	"os"
	"strings"
	"testing"
)

// TestBeadsTrackTasksProcessor_ExcludeInFlightLabel pins the peer
// `beads-track-tasks` processor: it also injects a system-notes reminder
// recommending `bd ready`, so the mitto-kvq contract (human-facing bd
// reminders must show `--exclude-label in-flight`) applies here too.
// This processor also subsumes the former `beads-ready-tasks` processor, so it
// pins the explanation of why the filter matters (in-flight is an
// automated-driver claim; omitting the filter risks a collision).
func TestBeadsTrackTasksProcessor_ExcludeInFlightLabel(t *testing.T) {
	const path = "../../config/processors/builtin/beads-track-tasks.yaml"

	loader := NewLoader("../../config/processors/builtin", nil)
	proc, err := loader.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}
	if proc == nil {
		t.Fatalf("LoadFile(%q): nil processor (file may be empty)", path)
	}

	if proc.Name != "beads-track-tasks" {
		t.Errorf("processor name = %q, want %q", proc.Name, "beads-track-tasks")
	}
	if !proc.IsTextMode() {
		t.Errorf("processor must be text-mode; got Text=%q Command=%q Prompt=%q",
			proc.Text, proc.Command, proc.Prompt)
	}

	for _, h := range []string{
		"bd ready --exclude-label in-flight",
		"in-flight",
		"automated drivers",
		"colliding with an active driver",
	} {
		if !strings.Contains(proc.Text, h) {
			t.Errorf("beads-track-tasks reminder missing hallmark %q (mitto-kvq)\n---\ntext:\n%s\n---", h, proc.Text)
		}
	}

	// Anti-regression: bare `bd ready` recommendation must not reappear in
	// the reminder body. Same voice check as the peer test.
	if strings.Contains(proc.Text, "Run `bd ready` ") || strings.Contains(proc.Text, "Run `bd ready`\n") ||
		strings.Contains(proc.Text, "run `bd ready` ") || strings.Contains(proc.Text, "run `bd ready`\n") {
		t.Errorf("beads-track-tasks reminder still contains the pre-mitto-kvq bare `bd ready` form (mitto-kvq)\n---\ntext:\n%s\n---", proc.Text)
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
