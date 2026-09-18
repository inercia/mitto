package processors

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// beadsGuidanceStandingFile is a minimal standing agent-instructions body
// carrying both markers HasStandingBeadsGuidance() requires (mitto-2kw).
const beadsGuidanceStandingFile = "Run `bd ready --exclude-label in-flight` to find work.\n" +
	"Use `bd remember` for persistent knowledge.\n"

// writeAgentsMD writes a standing AGENTS.md at dir's root with content.
func writeAgentsMD(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestBeadsTrackTasksProcessor_ShrinksWhenStandingGuidancePresent renders the
// real beads-track-tasks.yaml body against a workspace that DOES carry a
// standing AGENTS.md with both Beads-guidance markers, and one that does not,
// asserting (mitto-2kw acceptance criteria):
//   - the runtime-resolved BeadsDatabaseMode block is present in BOTH renders
//     (the one thing no static file can supply must never be dropped);
//   - the multi-bullet reminder (bd ready / bd update --claim / bd remember /
//     bd prime) is dropped ONLY when the standing file is present;
//   - the shrunk render is measurably smaller than the full render (the
//     "measured first-message token cost is reduced" acceptance criterion).
func TestBeadsTrackTasksProcessor_ShrinksWhenStandingGuidancePresent(t *testing.T) {
	const path = "../../config/processors/builtin/beads-track-tasks.yaml"
	loader := NewLoader("../../config/processors/builtin", nil)
	proc, err := loader.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}

	render := func(workingDir string) string {
		t.Helper()
		ctx := BuildCELContext(&ProcessorInput{WorkingDir: workingDir, DatabaseMode: config.BeadsDatabaseModeShared})
		got, err := config.RenderPromptTemplate(proc.Name, proc.Text, ctx, config.BuildTemplateFuncMap(ctx))
		if err != nil {
			t.Fatalf("RenderPromptTemplate: %v", err)
		}
		return got
	}

	standingDir := t.TempDir()
	writeAgentsMD(t, standingDir, beadsGuidanceStandingFile)
	shrunk := render(standingDir)

	bareDir := t.TempDir()
	full := render(bareDir)

	// The runtime-resolved database-mode line has no static substitute and
	// must survive in both renders.
	for _, text := range []string{shrunk, full} {
		if !strings.Contains(text, "Database mode: shared.") {
			t.Errorf("render missing unconditional BeadsDatabaseMode block:\n%s", text)
		}
	}

	// Bullet reminder must be present in the fallback (no standing file) render.
	fallbackHallmarks := []string{
		"bd ready --exclude-label in-flight",
		"bd update <id> --claim",
		"bd remember",
		"bd prime",
	}
	for _, h := range fallbackHallmarks {
		if !strings.Contains(full, h) {
			t.Errorf("fallback (no standing file) render missing hallmark %q:\n%s", h, full)
		}
	}

	// Bullet reminder must be ABSENT once a standing file covers it.
	for _, h := range fallbackHallmarks {
		if strings.Contains(shrunk, h) {
			t.Errorf("shrunk (standing file present) render still contains hallmark %q; expected it dropped:\n%s", h, shrunk)
		}
	}

	// Shrunk render should reference the standing file instead. The prose
	// wraps ("standing\nagent-instructions file") so match on the
	// unambiguous "agent-instructions file" tail rather than the whole phrase.
	if !strings.Contains(shrunk, "agent-instructions file") {
		t.Errorf("shrunk render missing reference to the standing agent-instructions file:\n%s", shrunk)
	}

	// Byte-count reduction: shrunk render must be meaningfully smaller than
	// the full render (mitto-2kw AC: "measured first-message token cost is
	// reduced"). Use a generous 70% threshold so the assertion is robust to
	// minor prose rewording, while still catching a regression that stops
	// shrinking altogether.
	if len(shrunk) >= len(full) {
		t.Fatalf("shrunk render (%d bytes) is not smaller than full render (%d bytes)", len(shrunk), len(full))
	}
	if ratio := float64(len(shrunk)) / float64(len(full)); ratio > 0.7 {
		t.Errorf("shrunk render is only %.0f%% smaller than full render (shrunk=%d full=%d bytes), want a bigger reduction",
			(1-ratio)*100, len(shrunk), len(full))
	}
}

// TestBeadsReadyTasksProcessor_SuppressedWhenStandingGuidancePresent verifies
// the beads-ready-tasks.yaml enabledWhen gate (mitto-2kw): the processor is
// fully suppressed when a standing AGENTS.md already carries the guidance,
// still fires in a workspace without one (pre-existing DirExists/CommandExists
// gates unaffected), and stays suppressed when .beads/ is absent regardless of
// standing-guidance state.
func TestBeadsReadyTasksProcessor_SuppressedWhenStandingGuidancePresent(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed on PATH")
	}

	const path = "../../config/processors/builtin/beads-ready-tasks.yaml"
	loader := NewLoader("../../config/processors/builtin", nil)
	proc, err := loader.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%q): %v", path, err)
	}

	newBeadsDir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".beads"), 0755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("suppressed when standing guidance present", func(t *testing.T) {
		dir := newBeadsDir(t)
		writeAgentsMD(t, dir, beadsGuidanceStandingFile)
		if got := evaluateEnabledWhen(proc, &ProcessorInput{WorkingDir: dir}, nil); got {
			t.Errorf("evaluateEnabledWhen = true, want false (standing guidance present)")
		}
	})

	t.Run("fires when no standing guidance", func(t *testing.T) {
		dir := newBeadsDir(t)
		if got := evaluateEnabledWhen(proc, &ProcessorInput{WorkingDir: dir}, nil); !got {
			t.Errorf("evaluateEnabledWhen = false, want true (no standing guidance)")
		}
	})

	t.Run("still suppressed without .beads dir even absent standing guidance", func(t *testing.T) {
		dir := t.TempDir() // no .beads, no AGENTS.md
		if got := evaluateEnabledWhen(proc, &ProcessorInput{WorkingDir: dir}, nil); got {
			t.Errorf("evaluateEnabledWhen = true, want false (.beads dir absent)")
		}
	})
}
