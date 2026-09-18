package processors

import (
	"context"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// newCloseGateTestManager builds a Manager with a single close-phase,
// command-mode processor gated by the given enabledWhen expression. Uses
// the "true" command (always exits 0) so the recorded outcome is
// deterministic once the gate itself passes.
func newCloseGateTestManager(name, enabledWhen string) *Manager {
	m := NewManager("", nil)
	m.processors = []*Processor{
		{
			Name:        name,
			When:        WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
			EnabledWhen: enabledWhen,
			Command:     "true",
			Output:      OutputDiscard,
		},
	}
	return m
}

// TestApplyOnClose_PromptsIsEnabledGate_RouterEnabledSuppressesLegacy verifies
// mitto-3od.3's wiring end-to-end: with CloseProcessorInput.PromptsSnapshotFn
// populated (mirrors SessionManager.closePromptsSnapshot) and the
// knowledge-router prompt reported enabled, a legacy processor's
// `!Prompts.IsEnabled("knowledge-router")` enabledWhen clause evaluates to
// false and the processor is skipped with SkipReasonEnabledWhen — not run
// alongside the router.
func TestApplyOnClose_PromptsIsEnabledGate_RouterEnabledSuppressesLegacy(t *testing.T) {
	sessionID := "sess-gate-router-enabled"
	store := newCloseRouterApplyTestStore(t, sessionID)
	m := newCloseGateTestManager("legacy-memory", `!Prompts.IsEnabled("knowledge-router")`)

	var runs []ProcessorRun
	m.SetRunRecorder(func(run ProcessorRun) { runs = append(runs, run) })

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     sessionID,
		SessionStore:  store,
		ArchiveReason: "manual",
		PromptsSnapshotFn: func() *config.PromptsSnapshot {
			return &config.PromptsSnapshot{
				Names:        []string{"knowledge-router"},
				EnabledNames: []string{"knowledge-router"},
			}
		},
	})

	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want exactly 1", runs)
	}
	if runs[0].Outcome != "skipped" || runs[0].SkipReason != string(SkipReasonEnabledWhen) {
		t.Errorf("router enabled: run = %+v, want Outcome=skipped SkipReason=%s", runs[0], SkipReasonEnabledWhen)
	}
}

// TestApplyOnClose_PromptsIsEnabledGate_RouterDisabledLegacyFires verifies the
// converse: when the knowledge-router prompt is NOT in EnabledNames, the
// legacy processor's negated gate evaluates to true and it still runs.
func TestApplyOnClose_PromptsIsEnabledGate_RouterDisabledLegacyFires(t *testing.T) {
	sessionID := "sess-gate-router-disabled"
	store := newCloseRouterApplyTestStore(t, sessionID)
	m := newCloseGateTestManager("legacy-memory", `!Prompts.IsEnabled("knowledge-router")`)

	var runs []ProcessorRun
	m.SetRunRecorder(func(run ProcessorRun) { runs = append(runs, run) })

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     sessionID,
		SessionStore:  store,
		ArchiveReason: "manual",
		PromptsSnapshotFn: func() *config.PromptsSnapshot {
			return &config.PromptsSnapshot{Names: []string{"knowledge-router"}, EnabledNames: nil}
		},
	})

	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want exactly 1", runs)
	}
	if runs[0].Outcome != "ok" {
		t.Errorf("router disabled: run = %+v, want Outcome=ok", runs[0])
	}
}

// TestApplyOnClose_PromptsIsEnabledGate_NilSnapshotFnFailsClosedToRunning
// documents the safety default when CloseProcessorInput.PromptsSnapshotFn is
// not wired (e.g. SessionManager.promptsCache unset): Prompts.IsEnabled(...)
// fails closed (false), so a legacy processor's negated gate
// (`!Prompts.IsEnabled(...)`) evaluates to true and the processor still
// runs — never silently suppressed by a missing wire-up.
func TestApplyOnClose_PromptsIsEnabledGate_NilSnapshotFnFailsClosedToRunning(t *testing.T) {
	sessionID := "sess-gate-nil-fn"
	store := newCloseRouterApplyTestStore(t, sessionID)
	m := newCloseGateTestManager("legacy-memory", `!Prompts.IsEnabled("knowledge-router")`)

	var runs []ProcessorRun
	m.SetRunRecorder(func(run ProcessorRun) { runs = append(runs, run) })

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     sessionID,
		SessionStore:  store,
		ArchiveReason: "manual",
		// PromptsSnapshotFn intentionally left nil.
	})

	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want exactly 1", runs)
	}
	if runs[0].Outcome != "ok" {
		t.Errorf("nil PromptsSnapshotFn: run = %+v, want Outcome=ok (fail-closed gate still runs the processor)", runs[0])
	}
}

// gatedLegacyCloseProcessorFiles lists the four builtin close-phase
// processors mitto-3od.3 gated behind the knowledge-router. Kept in sync
// with the Implementation comment on mitto-3od.3; deliberately excludes
// curate-memories-on-close (out of scope per mitto-3od's bead description).
var gatedLegacyCloseProcessorFiles = []string{
	"../../config/processors/builtin/memorize-preferences.yaml",
	"../../config/processors/builtin/extract-memories-on-close.yaml",
	"../../config/processors/builtin/auggie-update-rules.yaml",
	"../../config/processors/builtin/claude-update-memory.yaml",
}

// TestGatedLegacyCloseProcessorYAML_ParsesAndCarriesRouterGate is the "yaml
// validation for the gated processors (still parse...)" acceptance
// criterion: each file must still load successfully through the real YAML
// loader (not just compile as Go structs) and its enabledWhen must carry the
// new suppression clause verbatim.
func TestGatedLegacyCloseProcessorYAML_ParsesAndCarriesRouterGate(t *testing.T) {
	loader := NewLoader("../../config/processors/builtin", nil)
	const wantGate = `!Prompts.IsEnabled("knowledge-router")`

	for _, path := range gatedLegacyCloseProcessorFiles {
		t.Run(path, func(t *testing.T) {
			proc, err := loader.LoadFile(path)
			if err != nil {
				t.Fatalf("LoadFile(%s): %v", path, err)
			}
			if proc.When.On != PhaseConversationClosed {
				t.Errorf("When.On = %q, want %q", proc.When.On, PhaseConversationClosed)
			}
			if !strings.Contains(proc.EnabledWhen, wantGate) {
				t.Errorf("EnabledWhen = %q, want it to contain %q", proc.EnabledWhen, wantGate)
			}
		})
	}
}

// TestGatedLegacyCloseProcessorYAML_EnabledWhenUnchangedWhenRouterDisabled
// verifies the "enabledWhen unchanged when router disabled" half of the
// acceptance criterion: loads memorize-preferences.yaml's REAL enabledWhen
// (`!Session.IsLoop && !Prompts.IsEnabled("knowledge-router")`) from disk and
// checks it still evaluates exactly as its pre-gate form
// (`!Session.IsLoop`) would once the router is reported disabled — i.e. the
// new clause is a pure conjunction that changes behavior only when the
// router is enabled, never regressing the pre-existing IsLoop condition.
func TestGatedLegacyCloseProcessorYAML_EnabledWhenUnchangedWhenRouterDisabled(t *testing.T) {
	loader := NewLoader("../../config/processors/builtin", nil)
	proc, err := loader.LoadFile("../../config/processors/builtin/memorize-preferences.yaml")
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	routerDisabled := func() *config.PromptsSnapshot {
		return &config.PromptsSnapshot{Names: []string{"knowledge-router"}, EnabledNames: nil}
	}

	for _, isLoop := range []bool{false, true} {
		input := &ProcessorInput{IsLoop: isLoop, PromptsSnapshotFn: routerDisabled}
		got := evaluateEnabledWhen(proc, input, nil)
		want := !isLoop // the pre-gate expression's only condition
		if got != want {
			t.Errorf("IsLoop=%v, router disabled: evaluateEnabledWhen = %v, want %v (pre-gate behavior)", isLoop, got, want)
		}
	}
}
