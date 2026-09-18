package processors

import (
	"context"
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
