package eventprojection

import (
	"context"
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
