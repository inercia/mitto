package conversation

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// newAgentHandoffTestSession builds a minimal store-backed BackgroundSession
// (no shared process/RPC involved — buildPromptWithHistory never issues one)
// with pendingFrom preset on the persisted metadata, and seeds nTurns
// user-prompt/agent-message turns into its event log via a real Recorder.
func newAgentHandoffTestSession(t *testing.T, pendingFrom string, nTurns int) (*BackgroundSession, *session.Store) {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const sessionID = "handoff-session"
	recorder := session.NewRecorderWithID(store, sessionID)
	if err := recorder.Start("agent-a", tmpDir, ""); err != nil {
		t.Fatalf("recorder.Start failed: %v", err)
	}
	if pendingFrom != "" {
		if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
			m.PendingAgentHandoffFrom = pendingFrom
		}); err != nil {
			t.Fatalf("UpdateMetadata failed: %v", err)
		}
	}
	for i := 0; i < nTurns; i++ {
		if err := recorder.RecordUserPrompt(fmtTurnLabel("question", i)); err != nil {
			t.Fatalf("RecordUserPrompt failed: %v", err)
		}
		if err := recorder.RecordAgentMessage(fmtTurnLabel("answer", i), fmtTurnLabel("answer", i)); err != nil {
			t.Fatalf("RecordAgentMessage failed: %v", err)
		}
	}

	bs := &BackgroundSession{persistedID: sessionID, store: store}
	return bs, store
}

func fmtTurnLabel(prefix string, i int) string {
	return prefix + "-" + string(rune('a'+i))
}

func TestConsumePendingAgentHandoff_ReadsAndClears(t *testing.T) {
	bs, store := newAgentHandoffTestSession(t, "agent-a", 1)

	previousAgent, ok := bs.consumePendingAgentHandoff()
	if !ok || previousAgent != "agent-a" {
		t.Fatalf("consumePendingAgentHandoff() = (%q, %v), want (\"agent-a\", true)", previousAgent, ok)
	}

	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.PendingAgentHandoffFrom != "" {
		t.Fatalf("PendingAgentHandoffFrom = %q, want cleared", meta.PendingAgentHandoffFrom)
	}
}

func TestConsumePendingAgentHandoff_NothingPending(t *testing.T) {
	bs, _ := newAgentHandoffTestSession(t, "", 1)

	if _, ok := bs.consumePendingAgentHandoff(); ok {
		t.Fatal("consumePendingAgentHandoff() = ok=true, want false when nothing was pending")
	}
}

func TestConsumePendingAgentHandoff_RunsOnlyOnce(t *testing.T) {
	bs, _ := newAgentHandoffTestSession(t, "agent-a", 1)

	if _, ok := bs.consumePendingAgentHandoff(); !ok {
		t.Fatal("first consumePendingAgentHandoff() should report ok=true")
	}
	if _, ok := bs.consumePendingAgentHandoff(); ok {
		t.Fatal("second consumePendingAgentHandoff() should report ok=false (already consumed)")
	}
}

// TestBuildPromptWithHistory_AgentHandoff_UsesLargerBudgetAndPreamble pins the
// mitto-f7yo.5 acceptance criterion: the first history-injecting prompt after
// a move uses the larger agentHandoffMaxTurns/agentHandoffMaxChars budget and
// names the previous agent in a preamble, instead of the normal 5-turn/no
// preamble path.
func TestBuildPromptWithHistory_AgentHandoff_UsesLargerBudgetAndPreamble(t *testing.T) {
	// 8 turns: more than the normal 5-turn cap, well under agentHandoffMaxTurns (20).
	bs, store := newAgentHandoffTestSession(t, "agent-a", 8)

	result := bs.buildPromptWithHistory("new message")

	if !strings.Contains(result, "previously handled by agent \"agent-a\"") {
		t.Errorf("expected handoff preamble naming agent-a, got: %q", result)
	}
	// Turn 0 ("question-a") would be excluded by the normal 5-turn cap but
	// must survive under the larger 20-turn handoff budget.
	if !strings.Contains(result, "question-a") {
		t.Errorf("expected the larger budget to retain the oldest of 8 turns, got: %q", result)
	}
	if !strings.Contains(result, "new message") {
		t.Errorf("expected the original message to be preserved, got: %q", result)
	}

	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.PendingAgentHandoffFrom != "" {
		t.Fatalf("PendingAgentHandoffFrom = %q, want cleared after use", meta.PendingAgentHandoffFrom)
	}
}

// TestBuildPromptWithHistory_NormalResume_Unchanged pins acceptance criterion
// #5: a normal resume (nothing pending) must be byte-identical to
// session.BuildConversationHistory(events, 5) + message -- no preamble, no
// larger budget.
func TestBuildPromptWithHistory_NormalResume_Unchanged(t *testing.T) {
	bs, _ := newAgentHandoffTestSession(t, "", 8)

	got := bs.buildPromptWithHistory("new message")

	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	want := session.BuildConversationHistory(events, 5) + "new message"
	if got != want {
		t.Errorf("normal resume changed:\ngot=  %q\nwant= %q", got, want)
	}
	if strings.Contains(got, "previously handled by agent") {
		t.Error("normal resume must not include the handoff preamble")
	}
}

// TestBuildPromptWithHistory_AgentHandoff_ConsumedOnce verifies a second call
// (e.g. a later prompt on the same BackgroundSession, or after a restart if a
// new instance re-reads the persisted metadata) no longer sees the preamble
// once the pending flag has been consumed.
func TestBuildPromptWithHistory_AgentHandoff_ConsumedOnce(t *testing.T) {
	bs, _ := newAgentHandoffTestSession(t, "agent-a", 2)

	first := bs.buildPromptWithHistory("first prompt")
	if !strings.Contains(first, "previously handled by agent \"agent-a\"") {
		t.Fatalf("first call should include the handoff preamble, got: %q", first)
	}

	second := bs.buildPromptWithHistory("second prompt")
	if strings.Contains(second, "previously handled by agent") {
		t.Errorf("second call must not repeat the handoff preamble, got: %q", second)
	}
}

// TestPendingAgentHandoff_SurvivesFreshContextSkip pins acceptance criterion
// #4: a FreshContext loop run computes shouldInjectHistory=false (existing
// gate in PromptWithMeta, unchanged here) and so never calls
// buildPromptWithHistory / consumePendingAgentHandoff at all for that prompt.
// The pending flag must therefore be left untouched -- not silently cleared
// -- so a LATER, non-FreshContext prompt still receives the handoff
// preamble/larger budget instead of silently losing it.
func TestPendingAgentHandoff_SurvivesFreshContextSkip(t *testing.T) {
	bs, store := newAgentHandoffTestSession(t, "agent-a", 2)

	// A FreshContext prompt happens here in production: shouldInjectHistory
	// is false, so buildPromptWithHistory is simply never invoked — nothing
	// to call here, this IS the simulation.

	meta, err := store.GetMetadata(bs.persistedID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.PendingAgentHandoffFrom != "agent-a" {
		t.Fatalf("PendingAgentHandoffFrom should survive an uncalled (FreshContext) prompt untouched, got %q", meta.PendingAgentHandoffFrom)
	}

	// A later, non-FreshContext prompt DOES call buildPromptWithHistory and
	// must still see (and then consume) the pending handoff.
	result := bs.buildPromptWithHistory("later prompt")
	if !strings.Contains(result, "previously handled by agent \"agent-a\"") {
		t.Errorf("expected handoff preamble to still apply on the later call, got: %q", result)
	}
}
