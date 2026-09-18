package conversation

// client_projection.go interposes an eventprojection.Projector into the
// production ACP streaming entry point (WebClient.SessionUpdate), as a
// transparent pass-through today (mitto-mx9.2). StreamBuffer remains the
// sole allocator of Mitto's observable seq — the Projector's own
// SeqAllocator (noopSeqAllocator below) is a private, discarded counter used
// only for the Projector's internal replay/dedup bookkeeping, which stays
// inert for ACP because ACP never emits an UpstreamCursor (see
// translateACPUpdateToNeutral). See docs/devel/agent-backend-architecture.md.

import (
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/eventprojection"
)

// translateACPUpdateToNeutral mirrors internal/acpbackend/events.go's
// translateSessionUpdate, duplicated (not imported) because acpbackend
// imports internal/conversation (for SharedProcess) — importing acpbackend
// here would create a cycle. Same precedent as
// acpLeaseContentBlocksToACP/acpLeaseContentBlocksToNeutral in
// backend_provider_acp.go. Only handles the update kinds this file routes
// through the projector (message/thought/tool-call/tool-call-update/plan);
// AvailableCommandsUpdate/CurrentModeUpdate/UsageUpdate keep their existing
// direct-callback paths in WebClient.SessionUpdate and are not translated
// here. Origin is always OriginLocal, matching acpbackend's own invariant:
// Mitto is the sole client driving each ACP session.
func translateACPUpdateToNeutral(u acp.SessionUpdate) (agentbackend.Event, bool) {
	base := agentbackend.Event{Origin: agentbackend.OriginLocal, Time: time.Now()}
	switch {
	case u.AgentMessageChunk != nil:
		base.Kind = agentbackend.EventAgentMessage
		base.Content = acpLeaseContentBlocksToNeutral([]acp.ContentBlock{u.AgentMessageChunk.Content})
		return base, true
	case u.AgentThoughtChunk != nil:
		base.Kind = agentbackend.EventAgentThought
		base.Content = acpLeaseContentBlocksToNeutral([]acp.ContentBlock{u.AgentThoughtChunk.Content})
		return base, true
	case u.ToolCall != nil:
		base.Kind = agentbackend.EventToolCall
		base.ToolCall = &agentbackend.ToolCallPayload{
			ID:     string(u.ToolCall.ToolCallId),
			Title:  u.ToolCall.Title,
			Status: string(u.ToolCall.Status),
			Kind:   string(u.ToolCall.Kind),
		}
		return base, true
	case u.ToolCallUpdate != nil:
		base.Kind = agentbackend.EventToolCall
		p := &agentbackend.ToolCallPayload{ID: string(u.ToolCallUpdate.ToolCallId), Update: true}
		if u.ToolCallUpdate.Title != nil {
			p.Title = *u.ToolCallUpdate.Title
		}
		if u.ToolCallUpdate.Status != nil {
			p.Status = string(*u.ToolCallUpdate.Status)
		}
		if u.ToolCallUpdate.Kind != nil {
			p.Kind = string(*u.ToolCallUpdate.Kind)
		}
		base.ToolCall = p
		return base, true
	case u.Plan != nil:
		base.Kind = agentbackend.EventPlan
		entries := make([]agentbackend.PlanEntry, len(u.Plan.Entries))
		for i, e := range u.Plan.Entries {
			entries[i] = agentbackend.PlanEntry{
				Content:  e.Content,
				Priority: string(e.Priority),
				Status:   string(e.Status),
			}
		}
		base.Plan = &agentbackend.PlanPayload{Entries: entries}
		return base, true
	default:
		return agentbackend.Event{}, false
	}
}

// noopSeqAllocator satisfies eventprojection.SeqAllocator without
// participating in Mitto's real sequence numbering. NewProjector requires a
// non-nil SeqAllocator, but StreamBuffer (via its own SeqProvider) remains
// the sole allocator of the observable Mitto seq — see
// streamBufferProjectionSink.Emit, which never reads ProjectedEvent.Seq.
type noopSeqAllocator struct{ n int64 }

// GetNextSeq implements eventprojection.SeqAllocator.
func (a *noopSeqAllocator) GetNextSeq() int64 {
	a.n++
	return a.n
}

// streamBufferProjectionSink adapts eventprojection.ProjectedEvent values
// into the same StreamBuffer calls WebClient.SessionUpdate made directly
// before this seam existed, preserving seq/ACK/replay semantics
// byte-for-byte: sb allocates the real Mitto seq at emit time via its own
// SeqProvider, so ev.Seq (the Projector's private, discarded counter) is
// never consulted here.
type streamBufferProjectionSink struct {
	sb *StreamBuffer
}

var _ eventprojection.ProjectionSink = (*streamBufferProjectionSink)(nil)

// Emit implements eventprojection.ProjectionSink.
func (s *streamBufferProjectionSink) Emit(ev eventprojection.ProjectedEvent) {
	switch ev.Kind {
	case agentbackend.EventAgentMessage:
		for _, c := range ev.Content {
			if c.Text != nil {
				s.sb.WriteMarkdown(c.Text.Text)
			}
		}
	case agentbackend.EventAgentThought:
		for _, c := range ev.Content {
			if c.Text != nil {
				s.sb.AddThought(c.Text.Text)
			}
		}
	case agentbackend.EventToolCall:
		if ev.ToolCall == nil {
			return
		}
		if ev.ToolCall.Update {
			var status *string
			if ev.ToolCall.Status != "" {
				st := ev.ToolCall.Status
				status = &st
			}
			s.sb.AddToolUpdate(ev.ToolCall.ID, status)
		} else {
			status := ev.ToolCall.Status
			s.sb.AddToolCall(ev.ToolCall.ID, ev.ToolCall.Title, &status)
		}
	case agentbackend.EventPlan:
		if ev.Plan == nil {
			return
		}
		entries := make([]PlanEntry, len(ev.Plan.Entries))
		for i, e := range ev.Plan.Entries {
			entries[i] = PlanEntry{Content: e.Content, Priority: e.Priority, Status: e.Status}
		}
		s.sb.AddPlan(entries)
	}
}

// newACPProjector constructs an eventprojection.Projector wired to sb via
// streamBufferProjectionSink, for the ACP integration (mitto-mx9.2). Uses an
// in-memory checkpoint store: ACP never emits an UpstreamCursor (see
// translateACPUpdateToNeutral), so no checkpoint is ever actually persisted
// — the durable, session-sidecar-backed store in
// internal/eventprojection/eventprojectionsession is reserved for a future
// backend that does emit one (e.g. AHP).
func newACPProjector(src eventprojection.SourceID, sb *StreamBuffer) (*eventprojection.Projector, error) {
	sink := &streamBufferProjectionSink{sb: sb}
	return eventprojection.NewProjector(src, &noopSeqAllocator{}, sink, eventprojection.NewMemoryCheckpointStore(), nil)
}
