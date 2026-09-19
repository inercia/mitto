package processors

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

// newRerunTestManager builds a Manager preloaded with procs and a run
// recorder that appends every ProcessorRun to the returned slice, so
// behavioral rerun-cadence tests can assert exactly what fired and why.
func newRerunTestManager(procs ...*Processor) (*Manager, *[]ProcessorRun) {
	m := NewManager("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.processors = procs
	runs := &[]ProcessorRun{}
	m.SetRunRecorder(func(r ProcessorRun) { *runs = append(*runs, r) })
	return m, runs
}

// TestRerunCadence_TokenOnlyProcessor_DoesNotRefireOnTimeOrMessagesAlone
// pins the mitto-8n5 engine-level contract behind the YAML retune: a
// match:first processor configured with ONLY afterTokens (the shape every
// immutable-guidance builtin processor now uses) must NOT refire purely
// because wall-clock time elapsed or many messages were sent — only actual
// token consumption crossing the threshold may trigger a rerun.
func TestRerunCadence_TokenOnlyProcessor_DoesNotRefireOnTimeOrMessagesAlone(t *testing.T) {
	proc := &Processor{
		Name:   "immutable-guidance",
		Text:   "static guidance",
		Mutate: config.ProcessorMutateAppend,
		When: WhenConfig{
			On:    PhaseUserPrompt,
			Match: MatchFirst,
			Rerun: &RerunConfig{AfterTokens: 1000},
		},
	}
	m, runs := newRerunTestManager(proc)

	// First message: fires naturally as an initial run.
	if _, err := m.Apply(context.Background(), &ProcessorInput{Message: "hi", IsFirstMessage: true}); err != nil {
		t.Fatalf("Apply (first message): %v", err)
	}
	if len(*runs) != 1 || (*runs)[0].RunKind != RunKindInitial {
		t.Fatalf("expected exactly 1 initial run after the first message, got %+v", *runs)
	}
	*runs = nil

	// Simulate 25 subsequent non-first turns (exceeds the legacy
	// afterSentMsgs:20 default) with the tracked last-run time pushed 45
	// minutes into the past (exceeds the legacy afterTime:30m default), but
	// keep accumulated tokens under the afterTokens:1000 threshold
	// throughout. Neither dimension may trigger a rerun now that time/msg
	// triggers have been removed from this processor's config.
	m.rerunState[proc.Name].lastRunTime = time.Now().Add(-45 * time.Minute)
	for i := 0; i < 25; i++ {
		if _, err := m.Apply(context.Background(), &ProcessorInput{Message: "turn", IsFirstMessage: false}); err != nil {
			t.Fatalf("Apply (turn %d): %v", i, err)
		}
		m.AccumulateTokenUsage(10) // 25*10 = 250, well under the 1000 threshold
	}
	// Every one of those 25 turns is expected to be recorded as "skipped"
	// (match:first, not the first message, no rerun override due) — filter
	// down to actually-applied ("ok") runs, which must be zero.
	if applied := okRuns(*runs); len(applied) != 0 {
		t.Fatalf("expected zero applied reruns from time/message elapsed alone (mitto-8n5), got %d: %+v", len(applied), applied)
	}
	*runs = nil

	// Now cross the token threshold: the NEXT Apply call must refire, and
	// only with RerunReasonTokens — never Time or Msgs, since those
	// triggers are absent from this processor's rerun config.
	m.AccumulateTokenUsage(1000)
	if _, err := m.Apply(context.Background(), &ProcessorInput{Message: "threshold-crossing turn", IsFirstMessage: false}); err != nil {
		t.Fatalf("Apply (threshold-crossing turn): %v", err)
	}
	applied := okRuns(*runs)
	if len(applied) != 1 {
		t.Fatalf("expected exactly 1 applied run once tokens crossed the threshold, got %d: %+v", len(applied), *runs)
	}
	if applied[0].RunKind != RunKindRerun || applied[0].RerunReason != string(RerunReasonTokens) {
		t.Errorf("rerun record = %+v, want RunKind=%q RerunReason=%q", applied[0], RunKindRerun, RerunReasonTokens)
	}
}

// okRuns filters a ProcessorRun slice down to entries with Outcome == "ok"
// (i.e. the processor actually applied, as opposed to being skipped).
func okRuns(runs []ProcessorRun) []ProcessorRun {
	var out []ProcessorRun
	for _, r := range runs {
		if r.Outcome == "ok" {
			out = append(out, r)
		}
	}
	return out
}

// TestRerunCadence_VerifiedFreshContext_RefiresRegardlessOfTokenBudget pins
// the "verified fresh context" half of the mitto-8n5 acceptance criteria: a
// genuine context reset (modeled here as IsFirstMessage=true again, e.g.
// after a session resume/compaction upstream) must refire the processor
// even though its token budget is nowhere near exhausted — no time/message
// workaround is needed for this case because the pipeline's own
// first-message handling already covers it.
func TestRerunCadence_VerifiedFreshContext_RefiresRegardlessOfTokenBudget(t *testing.T) {
	proc := &Processor{
		Name:   "immutable-guidance",
		Text:   "static guidance",
		Mutate: config.ProcessorMutateAppend,
		When: WhenConfig{
			On:    PhaseUserPrompt,
			Match: MatchFirst,
			Rerun: &RerunConfig{AfterTokens: 1000000}, // never crossed in this test
		},
	}
	m, runs := newRerunTestManager(proc)

	if _, err := m.Apply(context.Background(), &ProcessorInput{Message: "hi", IsFirstMessage: true}); err != nil {
		t.Fatalf("Apply (initial first message): %v", err)
	}
	if len(*runs) != 1 || (*runs)[0].RunKind != RunKindInitial {
		t.Fatalf("expected 1 initial run after the first message, got %+v", *runs)
	}
	*runs = nil

	// A resumed/compacted session re-sends IsFirstMessage=true on its next
	// dispatch (the mitto-cq4 machinery upstream of this package decides
	// that signal); simulate it directly here.
	if _, err := m.Apply(context.Background(), &ProcessorInput{Message: "resumed", IsFirstMessage: true}); err != nil {
		t.Fatalf("Apply (resumed first message): %v", err)
	}
	if len(*runs) != 1 || (*runs)[0].RunKind != RunKindInitial {
		t.Fatalf("expected the resumed first message to refire as an initial run (fresh-context path), got %+v", *runs)
	}
}
