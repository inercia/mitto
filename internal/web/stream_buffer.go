// Package web provides the web interface for Mitto.
package web

import (
	"sync"

	"github.com/inercia/mitto/internal/conversion"
)

// StreamEventType represents the type of a buffered stream event.
type StreamEventType int

const (
	StreamEventAgentMessage StreamEventType = iota
	StreamEventAgentThought
	StreamEventToolCall
	StreamEventToolUpdate
	StreamEventPlan
)

// StreamEvent represents a buffered event in the stream.
// Note: Seq is NOT stored here - it's assigned at emit time by SeqProvider.
type StreamEvent struct {
	Type        StreamEventType
	HTML        string      // For AgentMessage
	Text        string      // For AgentThought
	ToolID      string      // For ToolCall/ToolUpdate
	Title       string      // For ToolCall
	Status      *string     // For ToolCall/ToolUpdate (nil means no status)
	PlanEntries []PlanEntry // For Plan
}

// StreamBufferCallbacks holds callbacks for emitting events.
type StreamBufferCallbacks struct {
	OnAgentMessage func(seq int64, html string)
	OnAgentThought func(seq int64, text string)
	OnToolCall     func(seq int64, id, title, status string)
	OnToolUpdate   func(seq int64, id string, status *string)
	OnPlan         func(seq int64, entries []PlanEntry)
}

// StreamBufferConfig holds configuration for creating a StreamBuffer.
type StreamBufferConfig struct {
	Callbacks       StreamBufferCallbacks
	FileLinksConfig *conversion.FileLinkerConfig
	// SeqProvider provides sequence numbers for event ordering.
	// Seq is assigned at emit time (not receive time) to ensure contiguous numbers.
	SeqProvider SeqProvider
}

// StreamBuffer buffers all streaming events and emits them in correct order.
// It wraps MarkdownBuffer for markdown content and buffers non-markdown events
// (tool calls, thoughts, etc.) when we're in the middle of a markdown block
// (list, table, code block). Events are emitted in sequence order once the
// markdown block completes.
//
// Sequence numbers are assigned at emit time (not receive time) to ensure
// contiguous numbers without gaps from coalesced chunks.
//
// This ensures that a tool call arriving mid-list doesn't break the list rendering.
// Instead, the tool call is buffered and emitted after the list completes.
type StreamBuffer struct {
	mu            sync.Mutex
	mdBuffer      *MarkdownBuffer
	pendingEvents []StreamEvent // Events waiting to be emitted after markdown flush
	callbacks     StreamBufferCallbacks
	seqProvider   SeqProvider
}

// NewStreamBuffer creates a new stream buffer with the given configuration.
func NewStreamBuffer(cfg StreamBufferConfig) *StreamBuffer {
	sb := &StreamBuffer{
		callbacks:     cfg.Callbacks,
		pendingEvents: make([]StreamEvent, 0, 8),
		seqProvider:   cfg.SeqProvider,
	}

	// Create markdown buffer that notifies us on flush
	sb.mdBuffer = NewMarkdownBufferWithConfig(MarkdownBufferConfig{
		OnFlush: func(html string) {
			sb.onMarkdownFlush(html)
		},
		FileLinksConfig: cfg.FileLinksConfig,
	})

	return sb
}

// getNextSeq returns the next sequence number from the provider.
// Returns 0 if no provider is configured (for backward compatibility in tests).
func (sb *StreamBuffer) getNextSeq() int64 {
	if sb.seqProvider == nil {
		return 0
	}
	return sb.seqProvider.GetNextSeq()
}

// WriteMarkdown adds a markdown chunk to the buffer.
// Note: No seq is passed - seq is assigned at emit time.
func (sb *StreamBuffer) WriteMarkdown(chunk string) {
	// Don't hold lock while calling mdBuffer - it may trigger onMarkdownFlush
	sb.mdBuffer.Write(chunk)

	// After writing, check if we're no longer in a block and have pending events
	inBlock := sb.mdBuffer.InBlock()
	if !inBlock {
		sb.mu.Lock()
		if len(sb.pendingEvents) > 0 {
			// Copy events to emit outside lock
			eventsToEmit := make([]StreamEvent, len(sb.pendingEvents))
			copy(eventsToEmit, sb.pendingEvents)
			sb.pendingEvents = sb.pendingEvents[:0]
			sb.mu.Unlock()
			sb.emitEvents(eventsToEmit)
			return
		}
		sb.mu.Unlock()
	}
}

// AddThought adds a thought event to the buffer.
// If we're in a markdown block (list/table/code), the thought is buffered until the block completes.
// Otherwise, any pending markdown is flushed and the thought is emitted immediately.
// Note: No seq is passed - seq is assigned at emit time.
func (sb *StreamBuffer) AddThought(text string) {
	// Check if we're in a block
	inBlock := sb.mdBuffer.InBlock()

	if inBlock {
		// Buffer the thought - it will be emitted after the markdown block completes
		sb.mu.Lock()
		sb.pendingEvents = append(sb.pendingEvents, StreamEvent{
			Type: StreamEventAgentThought,
			Text: text,
		})
		sb.mu.Unlock()
		return
	}

	// Not in a block - force flush any pending markdown first
	// (use Flush, not SafeFlush, to ensure content is emitted before the thought)
	sb.mdBuffer.Flush()

	// Emit the thought immediately with seq assigned now
	if sb.callbacks.OnAgentThought != nil {
		seq := sb.getNextSeq()
		sb.callbacks.OnAgentThought(seq, text)
	}
}

// AddToolCall adds a tool call event to the buffer.
// If we're in a markdown block (list/table/code), the tool call is buffered until the block completes.
// Otherwise, any pending markdown is flushed and the tool call is emitted immediately.
// Note: No seq is passed - seq is assigned at emit time.
//
// EXPERIMENTAL: If FlushOnToolCall is enabled, the markdown buffer is force-flushed
// when a tool call arrives, even if we're in a block. This ensures content is visible
// before tool output appears, at the cost of potentially splitting blocks.
func (sb *StreamBuffer) AddToolCall(id, title string, status *string) {
	// EXPERIMENTAL: Force flush on tool call if enabled
	if FlushOnToolCall {
		// Force flush any pending markdown before processing the tool call
		sb.mdBuffer.Flush()

		// Emit any pending events that were buffered
		sb.emitPendingEvents()

		// Emit the tool call immediately
		if sb.callbacks.OnToolCall != nil {
			seq := sb.getNextSeq()
			s := ""
			if status != nil {
				s = *status
			}
			sb.callbacks.OnToolCall(seq, id, title, s)
		}
		return
	}

	// Standard behavior: check if we're in a block
	inBlock := sb.mdBuffer.InBlock()

	if inBlock {
		// Buffer the tool call - it will be emitted after the markdown block completes
		sb.mu.Lock()
		sb.pendingEvents = append(sb.pendingEvents, StreamEvent{
			Type:   StreamEventToolCall,
			ToolID: id,
			Title:  title,
			Status: status,
		})
		sb.mu.Unlock()
		return
	}

	// Not in a block - force flush any pending markdown first
	// (use Flush, not SafeFlush, to ensure content is emitted before the tool call)
	sb.mdBuffer.Flush()

	// Emit the tool call immediately with seq assigned now
	if sb.callbacks.OnToolCall != nil {
		seq := sb.getNextSeq()
		s := ""
		if status != nil {
			s = *status
		}
		sb.callbacks.OnToolCall(seq, id, title, s)
	}
}

// AddToolUpdate adds a tool update event to the buffer.
// If we're in a markdown block (list/table/code), the update is buffered until the block completes.
// Otherwise, any pending markdown is flushed and the update is emitted immediately.
// Note: No seq is passed - seq is assigned at emit time.
func (sb *StreamBuffer) AddToolUpdate(id string, status *string) {
	// Check if we're in a block
	inBlock := sb.mdBuffer.InBlock()

	if inBlock {
		// Buffer the tool update
		sb.mu.Lock()
		sb.pendingEvents = append(sb.pendingEvents, StreamEvent{
			Type:   StreamEventToolUpdate,
			ToolID: id,
			Status: status,
		})
		sb.mu.Unlock()
		return
	}

	// Not in a block - force flush any pending markdown first
	sb.mdBuffer.Flush()

	// Emit the tool update immediately with seq assigned now
	if sb.callbacks.OnToolUpdate != nil {
		seq := sb.getNextSeq()
		sb.callbacks.OnToolUpdate(seq, id, status)
	}
}

// AddPlan adds a plan event to the buffer.
// If we're in a markdown block (list/table/code), the plan is buffered until the block completes.
// Otherwise, any pending markdown is flushed and the plan is emitted immediately.
// Note: No seq is passed - seq is assigned at emit time.
func (sb *StreamBuffer) AddPlan(entries []PlanEntry) {
	// Check if we're in a block
	inBlock := sb.mdBuffer.InBlock()

	if inBlock {
		// Buffer the plan
		sb.mu.Lock()
		sb.pendingEvents = append(sb.pendingEvents, StreamEvent{
			Type:        StreamEventPlan,
			PlanEntries: entries,
		})
		sb.mu.Unlock()
		return
	}

	// Not in a block - force flush any pending markdown first
	sb.mdBuffer.Flush()

	// Emit the plan immediately with seq assigned now
	if sb.callbacks.OnPlan != nil {
		seq := sb.getNextSeq()
		sb.callbacks.OnPlan(seq, entries)
	}
}

// Flush forces a flush of all buffered content and events.
// This should be called when the agent finishes responding.
func (sb *StreamBuffer) Flush() {
	// Don't hold lock while calling mdBuffer.Flush() because it will
	// call onMarkdownFlush.
	sb.mdBuffer.Flush()

	// After markdown flush, emit any remaining pending events
	sb.emitPendingEvents()
}

// Close stops the buffer and releases resources.
func (sb *StreamBuffer) Close() {
	// Don't hold lock while calling mdBuffer.Close() because it will
	// call Flush() which calls onMarkdownFlush.
	sb.mdBuffer.Close()
}

// onMarkdownFlush is called when the markdown buffer flushes content.
// NOTE: This is called from within mdBuffer.flushLocked(), so we CANNOT call
// any mdBuffer methods that acquire locks (like InBlock()).
// Pending events are NOT emitted here - they're emitted when:
// 1. Flush() is called explicitly (end of response)
// 2. A non-markdown event arrives and we're not in a block
func (sb *StreamBuffer) onMarkdownFlush(html string) {
	// Emit the markdown content with seq assigned now
	if sb.callbacks.OnAgentMessage != nil && html != "" {
		seq := sb.getNextSeq()
		sb.callbacks.OnAgentMessage(seq, html)
	}
	// Note: We don't emit pending events here because we can't safely check
	// if we're still in a block (would cause deadlock). Pending events will
	// be emitted when Flush() is called or when the next non-markdown event
	// arrives and we're not in a block.
}

// emitPendingEvents emits any pending events that were buffered.
// Must be called WITHOUT lock held.
func (sb *StreamBuffer) emitPendingEvents() {
	sb.mu.Lock()
	if len(sb.pendingEvents) == 0 {
		sb.mu.Unlock()
		return
	}
	// Copy events to emit outside lock
	eventsToEmit := make([]StreamEvent, len(sb.pendingEvents))
	copy(eventsToEmit, sb.pendingEvents)
	sb.pendingEvents = sb.pendingEvents[:0]
	sb.mu.Unlock()

	// Emit events outside lock
	sb.emitEvents(eventsToEmit)
}

// emitEvents emits a list of events via callbacks.
// Seq is assigned at emit time for each event.
// Must be called WITHOUT lock held.
func (sb *StreamBuffer) emitEvents(events []StreamEvent) {
	for _, event := range events {
		switch event.Type {
		case StreamEventAgentMessage:
			if sb.callbacks.OnAgentMessage != nil {
				seq := sb.getNextSeq()
				sb.callbacks.OnAgentMessage(seq, event.HTML)
			}
		case StreamEventAgentThought:
			if sb.callbacks.OnAgentThought != nil {
				seq := sb.getNextSeq()
				sb.callbacks.OnAgentThought(seq, event.Text)
			}
		case StreamEventToolCall:
			if sb.callbacks.OnToolCall != nil {
				seq := sb.getNextSeq()
				s := ""
				if event.Status != nil {
					s = *event.Status
				}
				sb.callbacks.OnToolCall(seq, event.ToolID, event.Title, s)
			}
		case StreamEventToolUpdate:
			if sb.callbacks.OnToolUpdate != nil {
				seq := sb.getNextSeq()
				sb.callbacks.OnToolUpdate(seq, event.ToolID, event.Status)
			}
		case StreamEventPlan:
			if sb.callbacks.OnPlan != nil {
				seq := sb.getNextSeq()
				sb.callbacks.OnPlan(seq, event.PlanEntries)
			}
		}
	}
}
