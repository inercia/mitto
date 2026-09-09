package eventprojection

import "github.com/inercia/mitto/internal/agentbackend"

// SeqAllocator issues monotonically increasing, Mitto-owned sequence
// numbers. Mirrors conversation.SeqProvider's contract (GetNextSeq) so a
// Projector can be driven by the same allocation discipline without this
// package importing internal/conversation.
type SeqAllocator interface {
	// GetNextSeq returns the next sequence number and increments the counter.
	GetNextSeq() int64
}

// Phase distinguishes where in a source's connection lifecycle a
// ProjectedEvent was produced.
type Phase string

const (
	// PhaseSnapshot marks the first logical event ever committed for a
	// fresh Checkpoint (no prior committed identity).
	PhaseSnapshot Phase = "snapshot"
	// PhaseReplay marks a logical event whose upstream identity was already
	// committed before; it carries its ORIGINAL Mitto seq, not a new one.
	PhaseReplay Phase = "replay"
	// PhaseLive marks an ordinary, first-time-seen logical event.
	PhaseLive Phase = "live"
)

// ProjectedEvent is the neutral, sequence-numbered event a Projector emits
// once a logical unit (a coalesced message/thought, a tool call, a plan
// update, ...) is committed.
type ProjectedEvent struct {
	// Seq is the Mitto-owned sequence number, allocated at commit time.
	// Never derived from UpstreamCursor.
	Seq  int64
	Kind agentbackend.EventKind
	// Phase reports whether this is an initial snapshot, a replay of a
	// previously-committed event, or an ordinary live event.
	Phase Phase
	// UpstreamIdentity is the opaque identity this logical event was
	// deduplicated under (empty when the source event carried no
	// UpstreamCursor and so could not participate in dedup/replay).
	UpstreamIdentity string

	Content      []agentbackend.ContentBlock
	Lifecycle    agentbackend.LifecycleState
	Capabilities agentbackend.Capabilities
	Models       *agentbackend.ModelState
	Modes        *agentbackend.ModeState
	ToolCall     *agentbackend.ToolCallPayload
	Plan         *agentbackend.PlanPayload

	// SuppressLocalAutomation is set for genuinely externally-originated
	// events (Origin=OriginRemote with no resolvable local-prompt
	// correlation) so downstream processors/loops know not to treat this
	// projection as a signal to run local automation side effects. Always
	// false for OriginLocal events and for confirmed echoes of local work
	// (which are not projected as new events at all — see correlation.go).
	SuppressLocalAutomation bool
}

// ProjectionSink receives committed ProjectedEvents, in order, for one
// source.
type ProjectionSink interface {
	Emit(ev ProjectedEvent)
}

// SinkFunc adapts a plain function to ProjectionSink.
type SinkFunc func(ProjectedEvent)

// Emit implements ProjectionSink.
func (f SinkFunc) Emit(ev ProjectedEvent) { f(ev) }
