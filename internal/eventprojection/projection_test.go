package eventprojection

import (
	"context"
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
)

type seqCounter struct{ n int64 }

func (c *seqCounter) GetNextSeq() int64 {
	c.n++
	return c.n
}

type captureSink struct{ events []ProjectedEvent }

func (s *captureSink) Emit(ev ProjectedEvent) { s.events = append(s.events, ev) }

func newTestProjector(t *testing.T) (*Projector, *captureSink) {
	t.Helper()
	sink := &captureSink{}
	p, err := NewProjector(SourceID{Backend: "fake", Provider: "p1", ProviderSession: "s1"}, &seqCounter{}, sink, NewMemoryCheckpointStore(), nil)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	return p, sink
}

func textEvent(kind agentbackend.EventKind, origin agentbackend.Origin, text, cursor string) agentbackend.Event {
	return agentbackend.Event{
		Kind:           kind,
		Origin:         origin,
		Content:        []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: text}}},
		UpstreamCursor: cursor,
	}
}

func TestProjector_CoalescesMessageChunksAcrossBoundary(t *testing.T) {
	p, sink := newTestProjector(t)

	if err := p.Ingest(textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "hello ", "")); err != nil {
		t.Fatalf("Ingest chunk 1: %v", err)
	}
	if err := p.Ingest(textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "world", "")); err != nil {
		t.Fatalf("Ingest chunk 2: %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected no commits before a boundary, got %d", len(sink.events))
	}

	// A tool call is itself a boundary: it must flush the coalesced message first.
	if err := p.Ingest(agentbackend.Event{Kind: agentbackend.EventToolCall, Origin: agentbackend.OriginLocal, ToolCall: &agentbackend.ToolCallPayload{ID: "tc1", Title: "Read"}}); err != nil {
		t.Fatalf("Ingest tool call: %v", err)
	}

	if len(sink.events) != 2 {
		t.Fatalf("expected 2 committed events (coalesced message + tool call), got %d: %+v", len(sink.events), sink.events)
	}
	msg := sink.events[0]
	if msg.Kind != agentbackend.EventAgentMessage || len(msg.Content) != 1 || msg.Content[0].Text.Text != "hello world" {
		t.Fatalf("unexpected coalesced message: %+v", msg)
	}
	if msg.Seq != 1 {
		t.Fatalf("coalesced message Seq = %d, want 1 (allocated once at commit)", msg.Seq)
	}
	tc := sink.events[1]
	if tc.Kind != agentbackend.EventToolCall || tc.ToolCall == nil || tc.ToolCall.ID != "tc1" {
		t.Fatalf("unexpected tool call event: %+v", tc)
	}
}

func TestProjector_FlushCommitsTrailingBufferedMessage(t *testing.T) {
	p, sink := newTestProjector(t)
	if err := p.Ingest(textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "trailing", "")); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected nothing committed before Flush, got %d", len(sink.events))
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(sink.events) != 1 || sink.events[0].Content[0].Text.Text != "trailing" {
		t.Fatalf("unexpected events after Flush: %+v", sink.events)
	}
}

func TestProjector_ToolCallAndPlanPayloadsPassThrough(t *testing.T) {
	p, sink := newTestProjector(t)

	toolEv := agentbackend.Event{Kind: agentbackend.EventToolCall, Origin: agentbackend.OriginLocal, ToolCall: &agentbackend.ToolCallPayload{ID: "tc1", Title: "Read file", Status: "in_progress"}}
	if err := p.Ingest(toolEv); err != nil {
		t.Fatalf("Ingest tool call: %v", err)
	}
	planEv := agentbackend.Event{Kind: agentbackend.EventPlan, Origin: agentbackend.OriginLocal, Plan: &agentbackend.PlanPayload{Entries: []agentbackend.PlanEntry{{Content: "step", Priority: "high", Status: "pending"}}}}
	if err := p.Ingest(planEv); err != nil {
		t.Fatalf("Ingest plan: %v", err)
	}

	if len(sink.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(sink.events))
	}
	if sink.events[0].ToolCall == nil || sink.events[0].ToolCall.Title != "Read file" {
		t.Fatalf("unexpected tool call payload: %+v", sink.events[0].ToolCall)
	}
	if sink.events[1].Plan == nil || len(sink.events[1].Plan.Entries) != 1 || sink.events[1].Plan.Entries[0].Content != "step" {
		t.Fatalf("unexpected plan payload: %+v", sink.events[1].Plan)
	}
}

func TestProjector_FirstCursorBearingEventIsSnapshotThenLive(t *testing.T) {
	p, sink := newTestProjector(t)

	if err := p.Ingest(textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "first", "cur-1")); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := p.Ingest(textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "second", "cur-2")); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(sink.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(sink.events))
	}
	if sink.events[0].Phase != PhaseSnapshot {
		t.Fatalf("first cursor-bearing commit Phase = %v, want %v", sink.events[0].Phase, PhaseSnapshot)
	}
	if sink.events[1].Phase != PhaseLive {
		t.Fatalf("second commit Phase = %v, want %v", sink.events[1].Phase, PhaseLive)
	}
}

func TestProjector_ReplayReusesOriginalSeq(t *testing.T) {
	p, sink := newTestProjector(t)

	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "hello", "cur-1")
	if err := p.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
	originalSeq := sink.events[0].Seq
	if sink.events[0].Phase != PhaseSnapshot {
		t.Fatalf("unexpected phase: %v", sink.events[0].Phase)
	}

	// Re-deliver the identical upstream event (simulating a reconnect replay).
	if err := p.Ingest(ev); err != nil {
		t.Fatalf("Ingest (replay): %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush (replay): %v", err)
	}

	if len(sink.events) != 2 {
		t.Fatalf("expected replay to be re-emitted (with original seq), got %d events", len(sink.events))
	}
	replay := sink.events[1]
	if replay.Phase != PhaseReplay {
		t.Fatalf("replay Phase = %v, want %v", replay.Phase, PhaseReplay)
	}
	if replay.Seq != originalSeq {
		t.Fatalf("replay Seq = %d, want original Seq %d (native identity stable across reconnect)", replay.Seq, originalSeq)
	}
}

func TestProjector_ExternalUpdateWithoutCorrelationIsSuppressedButProjected(t *testing.T) {
	p, sink := newTestProjector(t)

	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginRemote, "external", "cur-1")
	if err := p.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("expected the external event to be projected exactly once, got %d", len(sink.events))
	}
	if !sink.events[0].SuppressLocalAutomation {
		t.Fatalf("expected SuppressLocalAutomation=true for an unresolvable OriginRemote event")
	}
}

func TestProjector_ConfirmedLocalEchoIsNotReprojected(t *testing.T) {
	p, sink := newTestProjector(t)
	p.Correlation().Link("local-prompt-1", "cur-echo")

	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginRemote, "echo of local work", "cur-echo")
	if err := p.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(sink.events) != 0 {
		t.Fatalf("expected a confirmed local echo to NOT be projected as a new event, got %d: %+v", len(sink.events), sink.events)
	}
	if p.Correlation().Pending() != 0 {
		t.Fatalf("expected the correlation link to be consumed (Forget) once resolved")
	}
}

func TestProjector_ResetEpochClearsDedupState(t *testing.T) {
	p, sink := newTestProjector(t)

	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "hello", "cur-1")
	if err := p.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if sink.events[0].Phase != PhaseSnapshot {
		t.Fatalf("unexpected initial phase: %v", sink.events[0].Phase)
	}

	if err := p.ResetEpoch(); err != nil {
		t.Fatalf("ResetEpoch: %v", err)
	}
	if p.cp.Epoch != 1 {
		t.Fatalf("Epoch = %d, want 1", p.cp.Epoch)
	}

	// Same cursor identity, after an epoch reset, must be treated as fresh
	// (PhaseSnapshot again) rather than as a replay.
	if err := p.Ingest(ev); err != nil {
		t.Fatalf("Ingest (post-reset): %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush (post-reset): %v", err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(sink.events))
	}
	if sink.events[1].Phase != PhaseSnapshot {
		t.Fatalf("post-reset Phase = %v, want %v", sink.events[1].Phase, PhaseSnapshot)
	}
}

func TestProjector_CheckpointPersistsAcrossProjectorRestart(t *testing.T) {
	store := NewMemoryCheckpointStore()
	src := SourceID{Backend: "fake", Provider: "p1", ProviderSession: "s1"}

	sink1 := &captureSink{}
	p1, err := NewProjector(src, &seqCounter{}, sink1, store, nil)
	if err != nil {
		t.Fatalf("NewProjector 1: %v", err)
	}
	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "hello", "cur-1")
	if err := p1.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p1.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	originalSeq := sink1.events[0].Seq

	// Simulate a process restart: a brand-new Projector for the same source,
	// backed by the same durable store, loads the persisted Checkpoint.
	sink2 := &captureSink{}
	p2, err := NewProjector(src, &seqCounter{}, sink2, store, nil)
	if err != nil {
		t.Fatalf("NewProjector 2: %v", err)
	}
	if err := p2.Ingest(ev); err != nil {
		t.Fatalf("Ingest (post-restart replay): %v", err)
	}
	if err := p2.Flush(); err != nil {
		t.Fatalf("Flush (post-restart replay): %v", err)
	}

	if len(sink2.events) != 1 {
		t.Fatalf("expected 1 event on the new Projector, got %d", len(sink2.events))
	}
	if sink2.events[0].Phase != PhaseReplay {
		t.Fatalf("Phase = %v, want %v (durable checkpoint must survive restart)", sink2.events[0].Phase, PhaseReplay)
	}
	if sink2.events[0].Seq != originalSeq {
		t.Fatalf("Seq = %d, want original Seq %d", sink2.events[0].Seq, originalSeq)
	}
}

func TestProjector_FakeHostIntegration(t *testing.T) {
	h := agentbackend.NewFakeHost("p1")
	if err := h.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	sink := &captureSink{}
	proj, err := NewProjector(SourceIDFromSession("fake", sess.Ref()), &seqCounter{}, sink, NewMemoryCheckpointStore(), nil)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}

	sub, err := h.Subscribe(context.Background(), sess.Ref(), func(ev agentbackend.Event) {
		if err := proj.Ingest(ev); err != nil {
			t.Errorf("Ingest: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	if _, err := h.Prompt(context.Background(), sess.Ref(), []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: "hi"}}}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := proj.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(sink.events) != 1 || sink.events[0].Content[0].Text.Text != "hi" {
		t.Fatalf("unexpected projected events from FakeHost source: %+v", sink.events)
	}

	// ResumeSession publishes a Reconnected lifecycle event carrying a
	// non-empty UpstreamCursor gap signal (agentbackend's documented
	// sequence-gap contract) — the projector must accept and commit it.
	if _, err := h.ResumeSession(context.Background(), sess.Ref()); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if len(sink.events) != 2 || sink.events[1].Kind != agentbackend.EventLifecycle || sink.events[1].Lifecycle != agentbackend.LifecycleReconnected {
		t.Fatalf("expected a reconnect lifecycle event to be projected, got %+v", sink.events)
	}
}

func TestProjector_SeqIsMittoOwnedNeverDerivedFromUpstreamCursor(t *testing.T) {
	p, sink := newTestProjector(t)

	// Use a cursor value that LOOKS like it could be mistaken for a Mitto
	// seq (a large numeric string) to prove Seq is always allocated by the
	// injected SeqAllocator and never derived from/equal to the opaque,
	// possibly host-wide UpstreamCursor value.
	const suspiciousCursor = "999999"
	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "hi", suspiciousCursor)
	if err := p.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
	if got := sink.events[0].Seq; got != 1 {
		t.Fatalf("Seq = %d, want 1 (Mitto-owned, independent of the upstream cursor value %q)", got, suspiciousCursor)
	}
	if sink.events[0].UpstreamIdentity != suspiciousCursor {
		t.Fatalf("UpstreamIdentity should still record the opaque cursor for dedup purposes: got %q", sink.events[0].UpstreamIdentity)
	}
}

func TestProjector_OutOfOrderCursorArrivalsHandledByIdentityNotOrdering(t *testing.T) {
	p, sink := newTestProjector(t)

	// Cursors are opaque, backend-assigned markers with no assumed
	// ordering/comparability (doc.go) -- delivering "cur-2" before "cur-1"
	// must not corrupt dedup state or crash.
	ev2 := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "second", "cur-2")
	ev1 := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "first", "cur-1")

	if err := p.Ingest(ev2); err != nil {
		t.Fatalf("Ingest ev2: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := p.Ingest(ev1); err != nil {
		t.Fatalf("Ingest ev1: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(sink.events) != 2 {
		t.Fatalf("expected both out-of-order events projected, got %d", len(sink.events))
	}
	if sink.events[0].UpstreamIdentity != "cur-2" || sink.events[1].UpstreamIdentity != "cur-1" {
		t.Fatalf("unexpected identities: %+v", sink.events)
	}
	replaySeq := sink.events[0].Seq

	// Re-deliver "cur-2" (the first one ever seen) a second time, out of
	// order relative to "cur-1"'s single delivery: must still be
	// recognized as a replay carrying its OWN original seq, regardless of
	// arrival order.
	if err := p.Ingest(ev2); err != nil {
		t.Fatalf("Ingest ev2 (replay): %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(sink.events) != 3 {
		t.Fatalf("expected the replay to be re-emitted, got %d events", len(sink.events))
	}
	if sink.events[2].Phase != PhaseReplay || sink.events[2].Seq != replaySeq {
		t.Fatalf("replay of cur-2 = %+v, want Phase=replay Seq=%d", sink.events[2], replaySeq)
	}
}

func TestProjector_IndependentSourceIDsDoNotShareDedupState(t *testing.T) {
	// One durable store shared by two independent, interleaved upstream
	// host sessions (e.g. one host process serving many chats) must never
	// cross-contaminate each session's dedup/replay state.
	store := NewMemoryCheckpointStore()
	srcA := SourceID{Backend: "fake", Provider: "p1", ProviderSession: "sessA"}
	srcB := SourceID{Backend: "fake", Provider: "p1", ProviderSession: "sessB"}

	sinkA := &captureSink{}
	pA, err := NewProjector(srcA, &seqCounter{}, sinkA, store, nil)
	if err != nil {
		t.Fatalf("NewProjector A: %v", err)
	}
	sinkB := &captureSink{}
	pB, err := NewProjector(srcB, &seqCounter{}, sinkB, store, nil)
	if err != nil {
		t.Fatalf("NewProjector B: %v", err)
	}

	// Both sessions happen to reuse the identical UpstreamCursor value.
	evA := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "from A", "cur-1")
	evB := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "from B", "cur-1")

	if err := pA.Ingest(evA); err != nil {
		t.Fatalf("Ingest A: %v", err)
	}
	if err := pB.Ingest(evB); err != nil {
		t.Fatalf("Ingest B: %v", err)
	}
	if err := pA.Flush(); err != nil {
		t.Fatalf("Flush A: %v", err)
	}
	if err := pB.Flush(); err != nil {
		t.Fatalf("Flush B: %v", err)
	}

	if len(sinkA.events) != 1 || sinkA.events[0].Content[0].Text.Text != "from A" {
		t.Fatalf("sink A unexpected: %+v", sinkA.events)
	}
	if len(sinkB.events) != 1 || sinkB.events[0].Content[0].Text.Text != "from B" {
		t.Fatalf("sink B unexpected: %+v", sinkB.events)
	}
	if sinkA.events[0].Phase != PhaseSnapshot || sinkB.events[0].Phase != PhaseSnapshot {
		t.Fatalf("both independent sources must independently see their own first commit as PhaseSnapshot: A=%v B=%v", sinkA.events[0].Phase, sinkB.events[0].Phase)
	}

	// Re-arrival on A alone must replay only A's identity, leaving B
	// untouched.
	if err := pA.Ingest(evA); err != nil {
		t.Fatalf("Ingest A (replay): %v", err)
	}
	if err := pA.Flush(); err != nil {
		t.Fatalf("Flush A: %v", err)
	}
	if len(sinkA.events) != 2 || sinkA.events[1].Phase != PhaseReplay {
		t.Fatalf("expected A's replay: %+v", sinkA.events)
	}
	if len(sinkB.events) != 1 {
		t.Fatalf("B must be unaffected by A's replay, got %d events", len(sinkB.events))
	}
}

// neverPersistsCheckpointStore models a CheckpointStore whose Save() is lost
// on every call (e.g. a process crash between the sink write and the
// checkpoint persist -- Window A in doc.go / the Plan's crash-consistency
// design). Load always reports no checkpoint, as if no Save ever landed.
type neverPersistsCheckpointStore struct{}

func (neverPersistsCheckpointStore) Load(SourceID) (*Checkpoint, error) {
	return nil, ErrCheckpointNotFound
}
func (neverPersistsCheckpointStore) Save(*Checkpoint) error { return nil }

func TestProjector_CrashWindowA_UnpersistedCheckpointCanDuplicateProjectionButPreservesIdentity(t *testing.T) {
	store := neverPersistsCheckpointStore{}
	src := SourceID{Backend: "fake", Provider: "p1", ProviderSession: "s1"}
	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "hello", "cur-1")

	// First "process lifetime": the event is committed and handed to the
	// sink (the durable event write succeeds), but the checkpoint save
	// that should follow it is lost -- simulating a crash landing exactly
	// in Window A (event persisted, checkpoint not advanced).
	sink1 := &captureSink{}
	p1, err := NewProjector(src, &seqCounter{}, sink1, store, nil)
	if err != nil {
		t.Fatalf("NewProjector 1: %v", err)
	}
	if err := p1.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p1.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(sink1.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink1.events))
	}
	firstIdentity := sink1.events[0].UpstreamIdentity

	// "Restart": a fresh Projector loads from the store, which never
	// durably recorded the identity. Re-arrival of the SAME upstream event
	// is therefore NOT recognized as a replay. This is the documented
	// (doc.go) non-guarantee: reconciliation gives at-most-once PROJECTION
	// only once the checkpoint save has actually landed -- it is never a
	// promise of exactly-once SIDE-EFFECT EXECUTION by whatever consumes
	// the sink's output.
	sink2 := &captureSink{}
	p2, err := NewProjector(src, &seqCounter{}, sink2, store, nil)
	if err != nil {
		t.Fatalf("NewProjector 2: %v", err)
	}
	if err := p2.Ingest(ev); err != nil {
		t.Fatalf("Ingest (post-crash re-arrival): %v", err)
	}
	if err := p2.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if len(sink2.events) != 1 {
		t.Fatalf("expected the re-arrival to be projected again (the documented Window A risk), got %d", len(sink2.events))
	}
	if sink2.events[0].Phase == PhaseReplay {
		t.Fatalf("did not expect PhaseReplay: an unpersisted checkpoint cannot recognize the replay (this IS the Window A gap)")
	}
	// The one thing that MUST still hold even inside this crash window:
	// both emissions carry the SAME UpstreamIdentity, so a sink/consumer
	// that needs exactly-once side-effect execution can dedup on it.
	if firstIdentity == "" || sink2.events[0].UpstreamIdentity != firstIdentity {
		t.Fatalf("UpstreamIdentity must remain stable across the crash window so downstream consumers can dedup: got %q, want %q", sink2.events[0].UpstreamIdentity, firstIdentity)
	}
}

// countingCheckpointStore wraps a MemoryCheckpointStore to count Save calls,
// for asserting that a failed sink write never lets the checkpoint advance.
type countingCheckpointStore struct {
	*MemoryCheckpointStore
	saves int
}

func (s *countingCheckpointStore) Save(cp *Checkpoint) error {
	s.saves++
	return s.MemoryCheckpointStore.Save(cp)
}

// panicSink simulates a sink whose durable write fails catastrophically
// (e.g. a crash mid-write), to prove Window B (checkpoint advanced without
// the event ever being durably written) cannot happen: the crash-consistency
// ordering in projection.go calls the sink BEFORE saving the checkpoint, so
// a sink failure must prevent the checkpoint save from ever running.
type panicSink struct{}

func (panicSink) Emit(ProjectedEvent) { panic("simulated sink failure") }

func TestProjector_CrashWindowB_SinkFailureNeverAdvancesCheckpoint(t *testing.T) {
	store := &countingCheckpointStore{MemoryCheckpointStore: NewMemoryCheckpointStore()}
	src := SourceID{Backend: "fake", Provider: "p1", ProviderSession: "s1"}
	p, err := NewProjector(src, &seqCounter{}, panicSink{}, store, nil)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	ev := textEvent(agentbackend.EventAgentMessage, agentbackend.OriginLocal, "hello", "cur-1")

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected the sink failure to propagate out of Ingest/Flush")
			}
		}()
		if err := p.Ingest(ev); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		_ = p.Flush()
	}()

	if store.saves != 0 {
		t.Fatalf("checkpoint must never be advanced when the event write fails: got %d Save call(s), want 0 (Window B is impossible by ordering)", store.saves)
	}
	if _, err := store.Load(src); !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("checkpoint store must still report no checkpoint after the failed write: err = %v", err)
	}
}
