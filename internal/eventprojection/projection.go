package eventprojection

import (
	"errors"
	"sync"

	"github.com/inercia/mitto/internal/agentbackend"
)

// pendingContent buffers consecutive same-kind, same-origin content chunks
// (EventAgentMessage/EventAgentThought) so they commit as one logical unit,
// mirroring MarkdownBuffer's boundary role but content-agnostic: coalescing
// is driven purely by (Kind, Origin) continuity, never by markdown/HTML
// structure.
type pendingContent struct {
	active bool
	kind   agentbackend.EventKind
	origin agentbackend.Origin
	blocks []agentbackend.ContentBlock
	cursor string
}

// Projector consumes agentbackend.Event values for one upstream source (see
// SourceID) and emits neutral, sequence-numbered ProjectedEvents to a Sink.
// See doc.go for the full contract (coalescing, phases, replay dedup,
// prompt correlation, crash-consistency ordering).
//
// A Projector is NOT safe for concurrent calls to Ingest/Flush from
// multiple goroutines for the SAME instance — agentbackend's
// EventDelivery.Subscribe already documents in-order, single-callback
// delivery per subscription, so a single Projector should be fed from a
// single subscription. Independent Projectors for independent sources may
// be used concurrently.
type Projector struct {
	src   SourceID
	seq   SeqAllocator
	sink  ProjectionSink
	store CheckpointStore
	corr  *PromptCorrelation

	mu      sync.Mutex
	cp      *Checkpoint
	pending pendingContent
}

// NewProjector constructs a Projector for src, loading (or initializing) its
// Checkpoint from store. If corr is nil, a fresh PromptCorrelation is used
// (equivalent to "no local prompts tracked yet").
func NewProjector(src SourceID, seq SeqAllocator, sink ProjectionSink, store CheckpointStore, corr *PromptCorrelation) (*Projector, error) {
	if seq == nil {
		return nil, errors.New("eventprojection: NewProjector requires a non-nil SeqAllocator")
	}
	if store == nil {
		return nil, errors.New("eventprojection: NewProjector requires a non-nil CheckpointStore")
	}
	cp, err := store.Load(src)
	if err != nil {
		if errors.Is(err, ErrCheckpointNotFound) {
			cp = newCheckpoint(src)
		} else {
			return nil, err
		}
	}
	if cp.IdentitySeq == nil {
		cp.IdentitySeq = make(map[string]int64)
	}
	if corr == nil {
		corr = NewPromptCorrelation()
	}
	return &Projector{src: src, seq: seq, sink: sink, store: store, corr: corr, cp: cp}, nil
}

// Correlation returns the Projector's PromptCorrelation, so callers can Link
// a locally-issued prompt before/while feeding events.
func (p *Projector) Correlation() *PromptCorrelation { return p.corr }

// Ingest processes one upstream event. Content-bearing chunks
// (EventAgentMessage/EventAgentThought) are buffered until a logical
// boundary (a differing Kind/Origin, or an explicit Flush); every other
// event kind is itself a boundary and is committed immediately (after first
// flushing any pending buffered content).
func (p *Projector) Ingest(ev agentbackend.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ev.Kind == agentbackend.EventAgentMessage || ev.Kind == agentbackend.EventAgentThought {
		if p.pending.active && (p.pending.kind != ev.Kind || p.pending.origin != ev.Origin) {
			if err := p.flushPendingLocked(); err != nil {
				return err
			}
		}
		p.pending.active = true
		p.pending.kind = ev.Kind
		p.pending.origin = ev.Origin
		p.pending.blocks = mergeContent(p.pending.blocks, ev.Content)
		if ev.UpstreamCursor != "" {
			p.pending.cursor = ev.UpstreamCursor
		}
		return nil
	}

	if err := p.flushPendingLocked(); err != nil {
		return err
	}
	return p.commitDiscreteLocked(ev)
}

// Flush commits any buffered (coalesced) content as a ProjectedEvent. Call
// this at logical turn boundaries (e.g. once a prompt outcome is received)
// even when no further event arrives to trigger it implicitly, so the final
// in-flight message of a turn is not left uncommitted.
func (p *Projector) Flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flushPendingLocked()
}

// ResetEpoch bumps the checkpoint's epoch and clears all dedup/identity
// state, forcing the next cursor-bearing event to be treated as a fresh
// PhaseSnapshot. Callers invoke this when they have independent knowledge
// that the upstream cursor space was invalidated (e.g. the backend reports
// its history was purged/rotated) — a Projector has no generic way to infer
// this from an opaque cursor string alone.
func (p *Projector) ResetEpoch() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cp.Epoch++
	p.cp.LastCursor = ""
	p.cp.Committed = nil
	p.cp.IdentitySeq = make(map[string]int64)
	return p.saveCheckpointLocked()
}

func (p *Projector) flushPendingLocked() error {
	if !p.pending.active {
		return nil
	}
	ev := agentbackend.Event{
		Kind:           p.pending.kind,
		Origin:         p.pending.origin,
		Content:        p.pending.blocks,
		UpstreamCursor: p.pending.cursor,
	}
	p.pending = pendingContent{}
	return p.commitDiscreteLocked(ev)
}

// identityFor reports the upstream identity an event participates in
// dedup/replay under. Only UpstreamCursor-bearing events get one: it is the
// one thing an upstream backend guarantees is a stable, backend-assigned
// position marker (see agentbackend.Event.UpstreamCursor). Events without a
// cursor are always treated as new/live and never dedup'd — a safe default
// that never silently drops or duplicates-detects incorrectly.
func identityFor(ev agentbackend.Event) (id string, ok bool) {
	if ev.UpstreamCursor != "" {
		return ev.UpstreamCursor, true
	}
	return "", false
}

// classifyOrigin decides whether ev should be projected at all, and if so
// whether it must carry SuppressLocalAutomation. See doc.go for the
// authoritative statement of this rule.
func (p *Projector) classifyOrigin(ev agentbackend.Event, id string, hasID bool) (project, suppress bool) {
	if ev.Origin != agentbackend.OriginRemote {
		return true, false
	}
	if hasID {
		if _, linked := p.corr.Resolve(id); linked {
			p.corr.Forget(id)
			return false, false
		}
	}
	return true, true
}

func (p *Projector) commitDiscreteLocked(ev agentbackend.Event) error {
	id, hasID := identityFor(ev)

	if hasID {
		if seq, known := p.cp.IdentitySeq[id]; known {
			if seq >= 0 {
				p.emit(p.toProjectedEvent(ev, seq, PhaseReplay, id, false))
			}
			p.cp.LastCursor = ev.UpstreamCursor
			return p.saveCheckpointLocked()
		}
	}

	project, suppress := p.classifyOrigin(ev, id, hasID)
	if !project {
		if hasID {
			p.cp.recordIdentity(id, -1)
			p.cp.LastCursor = ev.UpstreamCursor
			return p.saveCheckpointLocked()
		}
		return nil
	}

	phase := PhaseLive
	if hasID && len(p.cp.IdentitySeq) == 0 {
		phase = PhaseSnapshot
	}
	seq := p.seq.GetNextSeq()
	p.emit(p.toProjectedEvent(ev, seq, phase, id, suppress))
	if hasID {
		p.cp.recordIdentity(id, seq)
		p.cp.LastCursor = ev.UpstreamCursor
		return p.saveCheckpointLocked()
	}
	return nil
}

func (p *Projector) toProjectedEvent(ev agentbackend.Event, seq int64, phase Phase, id string, suppress bool) ProjectedEvent {
	return ProjectedEvent{
		Seq:                     seq,
		Kind:                    ev.Kind,
		Phase:                   phase,
		UpstreamIdentity:        id,
		Content:                 ev.Content,
		Lifecycle:               ev.Lifecycle,
		Capabilities:            ev.Capabilities,
		Models:                  ev.Models,
		Modes:                   ev.Modes,
		ToolCall:                ev.ToolCall,
		Plan:                    ev.Plan,
		SuppressLocalAutomation: suppress,
	}
}

func (p *Projector) emit(pe ProjectedEvent) {
	if p.sink != nil {
		// Crash-consistency ordering: the sink's durable write (e.g. an
		// events.jsonl append) happens here, BEFORE the checkpoint is
		// advanced/saved by the caller. See doc.go.
		p.sink.Emit(pe)
	}
}

func (p *Projector) saveCheckpointLocked() error {
	if p.store == nil {
		return nil
	}
	return p.store.Save(p.cp)
}

// mergeContent appends add's blocks to existing, concatenating adjacent
// TextBlocks (content-agnostic: no markdown/HTML structure is consulted)
// and preserving non-text blocks as discrete entries.
func mergeContent(existing []agentbackend.ContentBlock, add []agentbackend.ContentBlock) []agentbackend.ContentBlock {
	for _, b := range add {
		if b.Text != nil && len(existing) > 0 && existing[len(existing)-1].Text != nil {
			merged := existing[len(existing)-1].Text.Text + b.Text.Text
			existing[len(existing)-1] = agentbackend.ContentBlock{Text: &agentbackend.TextBlock{Text: merged}}
			continue
		}
		existing = append(existing, b)
	}
	return existing
}
