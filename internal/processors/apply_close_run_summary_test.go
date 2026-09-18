package processors

import (
	"context"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// TestApplyOnClose_PersistsCloseRunSummary_AggregatesAcrossProcessors is the
// "close_summary unit tests over synthetic ProcessorRunData events" acceptance
// criterion for mitto-3od.3: it exercises a realistic multi-processor
// ApplyOnClose pipeline (one applied command-mode run, one enabledWhen-skipped
// run, one erroring command-mode run, and one collected prompt-mode run) and
// verifies the persisted close-run-summary.json sidecar entry aggregates
// exactly what the pipeline observed — not just that the sidecar's own
// read/write round-trips (covered separately in
// internal/session/close_run_summary_sidecar_test.go).
func TestApplyOnClose_PersistsCloseRunSummary_AggregatesAcrossProcessors(t *testing.T) {
	sessionID := "sess-summary-aggregate"
	store := newCloseRouterApplyTestStore(t, sessionID)

	m := NewManager("", nil)
	// A no-op PromptFunc is enough: dispatchPromptBatch fires the actual
	// completion asynchronously, but the summary entry for a collected
	// prompt-mode processor is recorded synchronously at collection time
	// (before dispatch), so the eventual async outcome is irrelevant here.
	m.SetPromptFunc(func(ctx context.Context, workspaceUUID, processorName, prompt string) error { return nil })

	m.processors = []*Processor{
		{
			Name:    "cmd-ok",
			When:    WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
			Command: "true",
			Output:  OutputDiscard,
		},
		{
			Name:        "cmd-skipped",
			When:        WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
			EnabledWhen: "false",
			Command:     "true",
			Output:      OutputDiscard,
		},
		{
			Name:    "cmd-errors",
			When:    WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
			Command: "false", // exits non-zero
			Output:  OutputDiscard,
		},
		{
			Name:   "prompt-ok",
			When:   WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
			Prompt: "Summarize this closed session.",
		},
	}

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:     sessionID,
		SessionStore:  store,
		ArchiveReason: "manual",
	})

	summary, err := session.ReadCloseRunSummary(store, sessionID)
	if err != nil {
		t.Fatalf("ReadCloseRunSummary: %v", err)
	}
	if len(summary.Runs) != 1 {
		t.Fatalf("Runs = %+v, want exactly 1 persisted run", summary.Runs)
	}
	run := summary.Runs[0]

	if run.ArchiveReason != "manual" {
		t.Errorf("ArchiveReason = %q, want %q", run.ArchiveReason, "manual")
	}
	if run.StartedAt == "" || run.CompletedAt == "" {
		t.Errorf("StartedAt/CompletedAt not set: %+v", run)
	}
	if run.TotalProcessors != 4 {
		t.Errorf("TotalProcessors = %d, want 4", run.TotalProcessors)
	}
	if run.Applied != 2 { // cmd-ok + prompt-ok (collected for dispatch counts as applied)
		t.Errorf("Applied = %d, want 2", run.Applied)
	}
	if run.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", run.Skipped)
	}
	if run.Errored != 1 {
		t.Errorf("Errored = %d, want 1", run.Errored)
	}
	if run.TotalEstTokensAuxiliary == 0 {
		t.Errorf("TotalEstTokensAuxiliary = 0, want > 0 from the collected prompt-mode processor")
	}
	if run.TotalEstTokensPrimary != 0 {
		t.Errorf("TotalEstTokensPrimary = %d, want 0 (no primary-target runs in this pipeline)", run.TotalEstTokensPrimary)
	}

	if len(run.Processors) != 4 {
		t.Fatalf("Processors = %+v, want 4 entries", run.Processors)
	}
	byName := map[string]session.CloseRunProcessorEntry{}
	for _, p := range run.Processors {
		byName[p.Name] = p
	}
	if got := byName["cmd-ok"]; got.Outcome != "ok" {
		t.Errorf("cmd-ok entry = %+v, want Outcome=ok", got)
	}
	if got := byName["cmd-skipped"]; got.Outcome != "skipped" || got.SkipReason != string(SkipReasonEnabledWhen) {
		t.Errorf("cmd-skipped entry = %+v, want Outcome=skipped SkipReason=%s", got, SkipReasonEnabledWhen)
	}
	if got := byName["cmd-errors"]; got.Outcome != "error" {
		t.Errorf("cmd-errors entry = %+v, want Outcome=error", got)
	}
	if got := byName["prompt-ok"]; got.Outcome != "ok" || got.Target != RunTargetAuxiliary || got.EstimatedTokens == 0 {
		t.Errorf("prompt-ok entry = %+v, want Outcome=ok Target=%s EstimatedTokens>0", got, RunTargetAuxiliary)
	}
}

// TestApplyOnClose_PersistsCloseRunSummary_AppendsAcrossMultipleCloses
// verifies the sidecar accumulates one entry per ApplyOnClose invocation
// rather than overwriting it, mirroring a session that is archived, and
// documents (in the unlikely event ApplyOnClose ran twice for it) reopened
// and re-closed.
func TestApplyOnClose_PersistsCloseRunSummary_AppendsAcrossMultipleCloses(t *testing.T) {
	sessionID := "sess-summary-append"
	store := newCloseRouterApplyTestStore(t, sessionID)

	m := NewManager("", nil)
	m.processors = []*Processor{
		{Name: "cmd-ok", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll}, Command: "true", Output: OutputDiscard},
	}

	for i := 0; i < 2; i++ {
		m.ApplyOnClose(context.Background(), CloseProcessorInput{
			SessionID:     sessionID,
			SessionStore:  store,
			ArchiveReason: "manual",
		})
	}

	summary, err := session.ReadCloseRunSummary(store, sessionID)
	if err != nil {
		t.Fatalf("ReadCloseRunSummary: %v", err)
	}
	if len(summary.Runs) != 2 {
		t.Fatalf("Runs = %+v, want 2 appended entries", summary.Runs)
	}
}
