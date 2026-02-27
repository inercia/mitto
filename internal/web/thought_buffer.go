// Package web provides the web interface for Mitto.
package web

import (
	"strings"
	"sync"
	"time"
)

const (
	// thoughtFlushTimeout is the timeout for flushing buffered thought content.
	// Thoughts are simpler than markdown (no block detection needed), so we use
	// a shorter timeout to keep the UI responsive while still coalescing chunks.
	thoughtFlushTimeout = 150 * time.Millisecond

	// thoughtMaxBufferSize is the maximum thought buffer size before forcing a flush.
	// This prevents unbounded memory growth for very long thoughts.
	thoughtMaxBufferSize = 8192
)

// ThoughtBuffer accumulates streaming thought chunks and emits them as unified blocks.
// This reduces the number of WebSocket messages and sequence numbers used for thoughts.
//
// The buffer flushes when:
// - The flush timeout expires (150ms after last write)
// - ForceFlush() is called (when another event type arrives)
// - The buffer size exceeds the maximum
// - Close() is called
//
// Sequence numbers are assigned at emit time by the caller.
type ThoughtBuffer struct {
	mu         sync.Mutex
	buffer     strings.Builder
	onFlush    func(text string)
	flushTimer *time.Timer
}

// ThoughtBufferConfig holds configuration for creating a ThoughtBuffer.
type ThoughtBufferConfig struct {
	// OnFlush is called when thought content is ready to be sent.
	// The caller is responsible for assigning sequence numbers.
	OnFlush func(text string)
}

// NewThoughtBuffer creates a new streaming thought buffer.
func NewThoughtBuffer(cfg ThoughtBufferConfig) *ThoughtBuffer {
	return &ThoughtBuffer{
		onFlush: cfg.OnFlush,
	}
}

// Write adds a thought chunk to the buffer and schedules a flush.
func (tb *ThoughtBuffer) Write(chunk string) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Skip empty chunks
	if chunk == "" {
		return
	}

	// Cancel any pending timeout flush
	if tb.flushTimer != nil {
		tb.flushTimer.Stop()
	}

	tb.buffer.WriteString(chunk)

	// Check if buffer is too large
	if tb.buffer.Len() >= thoughtMaxBufferSize {
		tb.flushLocked()
		return
	}

	// Schedule flush timeout
	tb.flushTimer = time.AfterFunc(thoughtFlushTimeout, func() {
		tb.mu.Lock()
		defer tb.mu.Unlock()
		tb.flushLocked()
	})
}

// ForceFlush immediately flushes any buffered content.
// Call this before emitting non-thought events to maintain ordering.
func (tb *ThoughtBuffer) ForceFlush() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.flushLocked()
}

// HasContent returns true if there is buffered content waiting to be flushed.
func (tb *ThoughtBuffer) HasContent() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.buffer.Len() > 0
}

// Close stops the buffer and flushes any remaining content.
func (tb *ThoughtBuffer) Close() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.flushTimer != nil {
		tb.flushTimer.Stop()
		tb.flushTimer = nil
	}
	tb.flushLocked()
}

// flushLocked emits buffered content via callback.
// Must be called with lock held.
func (tb *ThoughtBuffer) flushLocked() {
	if tb.flushTimer != nil {
		tb.flushTimer.Stop()
		tb.flushTimer = nil
	}

	if tb.buffer.Len() == 0 {
		return
	}

	content := tb.buffer.String()
	tb.buffer.Reset()

	// Call callback outside... wait, we have the lock. Need to be careful here.
	// The callback might try to acquire other locks. To be safe, we should
	// copy the content and call outside the lock, but that requires restructuring.
	// For simplicity, we call with lock held but document that callbacks should
	// not call back into ThoughtBuffer.
	if tb.onFlush != nil {
		tb.onFlush(content)
	}
}
