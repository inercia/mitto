package eventprojectionsession

import (
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/eventprojection"
	"github.com/inercia/mitto/internal/session"
)

func newTestSessionStore(t *testing.T) (*session.Store, string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	const sessionID = "checkpoint-session"
	if err := store.Create(session.Metadata{SessionID: sessionID, ACPServer: "test-server", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return store, sessionID
}

func TestStore_Load_FreshSessionReturnsCheckpointNotFound(t *testing.T) {
	sessions, sessionID := newTestSessionStore(t)
	s := New(sessions, sessionID)

	_, err := s.Load(eventprojection.SourceID{Backend: "acp", Provider: "auggie", ProviderSession: "sess-1"})
	if !errors.Is(err, eventprojection.ErrCheckpointNotFound) {
		t.Fatalf("Load on fresh session: err = %v, want ErrCheckpointNotFound", err)
	}
}

func TestStore_Load_MissingSessionPropagatesSessionNotFound(t *testing.T) {
	sessions, _ := newTestSessionStore(t)
	s := New(sessions, "does-not-exist")

	_, err := s.Load(eventprojection.SourceID{Backend: "acp", Provider: "auggie", ProviderSession: "sess-1"})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("Load on missing session: err = %v, want session.ErrSessionNotFound", err)
	}
}

func TestStore_SaveThenLoad_RoundTrips(t *testing.T) {
	sessions, sessionID := newTestSessionStore(t)
	s := New(sessions, sessionID)

	src := eventprojection.SourceID{Backend: "acp", Provider: "auggie", ProviderSession: "sess-1"}
	want := &eventprojection.Checkpoint{
		Source:      src,
		Epoch:       2,
		LastCursor:  "cur-42",
		Committed:   []string{"cur-1", "cur-2"},
		IdentitySeq: map[string]int64{"cur-1": 1, "cur-2": -1},
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load(src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Source != want.Source || got.Epoch != want.Epoch || got.LastCursor != want.LastCursor {
		t.Fatalf("round-tripped checkpoint mismatch: got %+v, want %+v", got, want)
	}
	if len(got.Committed) != 2 || got.Committed[0] != "cur-1" || got.Committed[1] != "cur-2" {
		t.Fatalf("Committed mismatch: %+v", got.Committed)
	}
	if got.IdentitySeq["cur-1"] != 1 || got.IdentitySeq["cur-2"] != -1 {
		t.Fatalf("IdentitySeq mismatch: %+v", got.IdentitySeq)
	}
}

// TestProjector_UsesSessionBackedStoreAcrossRestart exercises the adapter
// through eventprojection.NewProjector end-to-end (not just the DTO
// round-trip), proving the sibling package satisfies CheckpointStore.
func TestProjector_UsesSessionBackedStoreAcrossRestart(t *testing.T) {
	sessions, sessionID := newTestSessionStore(t)
	store := New(sessions, sessionID)
	src := eventprojection.SourceID{Backend: "acp", Provider: "auggie", ProviderSession: "sess-1"}

	type seqAllocator struct{ n int64 }
	seq := &seqAllocator{}
	getNext := func() int64 { seq.n++; return seq.n }

	var captured []eventprojection.ProjectedEvent
	sink := eventprojection.SinkFunc(func(ev eventprojection.ProjectedEvent) { captured = append(captured, ev) })

	p1, err := eventprojection.NewProjector(src, seqFunc(getNext), sink, store, nil)
	if err != nil {
		t.Fatalf("NewProjector 1: %v", err)
	}
	ev := agentbackend.Event{
		Kind:           agentbackend.EventAgentMessage,
		Origin:         agentbackend.OriginLocal,
		Content:        []agentbackend.ContentBlock{{Text: &agentbackend.TextBlock{Text: "hi"}}},
		UpstreamCursor: "cur-1",
	}
	if err := p1.Ingest(ev); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := p1.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 event, got %d", len(captured))
	}
	originalSeq := captured[0].Seq

	// New Projector, same session-backed store: must load the persisted
	// checkpoint and recognize the replay.
	p2, err := eventprojection.NewProjector(src, seqFunc(getNext), sink, store, nil)
	if err != nil {
		t.Fatalf("NewProjector 2: %v", err)
	}
	if err := p2.Ingest(ev); err != nil {
		t.Fatalf("Ingest (replay): %v", err)
	}
	if err := p2.Flush(); err != nil {
		t.Fatalf("Flush (replay): %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 events total, got %d", len(captured))
	}
	if captured[1].Phase != eventprojection.PhaseReplay || captured[1].Seq != originalSeq {
		t.Fatalf("replay event = %+v, want Phase=replay Seq=%d", captured[1], originalSeq)
	}
}

// seqFunc adapts a plain function to eventprojection.SeqAllocator.
type seqFunc func() int64

func (f seqFunc) GetNextSeq() int64 { return f() }
