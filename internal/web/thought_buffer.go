// Package web provides the web interface for Mitto.
package web

import (
	"strings"
	"sync"
	"time"
)

const (
	// thoughtFlushTimeout is the timeout for flushing buffered thought content.
	// Claude Code sends thoughts in bursts with ~50ms gaps between chunks, but
	// there can be longer pauses (100-200ms) between bursts of thinking.
	// We use 500ms to coalesce multiple bursts into a single thought block,
	// providing a better UX with unified thought messages instead of fragments.
	// The timeout is reset on each new chunk, so rapid streaming still works well.
	thoughtFlushTimeout = 500 * time.Millisecond

	// thoughtMaxBufferSize is the maximum thought buffer size before forcing a flush.
	// This prevents unbounded memory growth for very long thoughts.
	thoughtMaxBufferSize = 8192
)

// ThoughtBuffer accumulates streaming thought chunks and emits them as unified blocks.
// This reduces the number of WebSocket messages and sequence numbers used for thoughts.
//
// The buffer flushes when:
// - The flush timeout expires (500ms after last write)
// - ForceFlush() is called (when another event type arrives)
// - The buffer size exceeds the maximum
// - Close() is called
//
// Sequence numbers are assigned at emit time by the caller.
type ThoughtBuffer struct {
	mu              sync.Mutex
	buffer          strings.Builder
	onFlush         func(text string)
	flushTimer      *time.Timer
	timerGeneration uint64 // Generation counter to detect stale timer callbacks
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

	// Cancel any pending timeout flush and increment generation
	if tb.flushTimer != nil {
		tb.flushTimer.Stop()
	}
	tb.timerGeneration++

	tb.buffer.WriteString(chunk)

	// Check if buffer is too large
	if tb.buffer.Len() >= thoughtMaxBufferSize {
		tb.flushLocked()
		return
	}

	// Schedule flush timeout with generation check to prevent stale timer races
	currentGen := tb.timerGeneration
	tb.flushTimer = time.AfterFunc(thoughtFlushTimeout, func() {
		tb.mu.Lock()
		defer tb.mu.Unlock()
		// Only flush if this timer is still the active one (generation matches)
		if tb.timerGeneration == currentGen {
			tb.flushLocked()
		}
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

// flushLocked extracts buffered content and schedules callback outside lock.
// Must be called with lock held. Releases lock before calling callback to prevent deadlocks.
func (tb *ThoughtBuffer) flushLocked() {
	if tb.flushTimer != nil {
		tb.flushTimer.Stop()
		tb.flushTimer = nil
	}

	if tb.buffer.Len() == 0 {
		return
	}

	// Copy content and callback reference before releasing lock
	content := tb.buffer.String()
	tb.buffer.Reset()
	callback := tb.onFlush

	// Release lock before calling callback to prevent deadlocks.
	// The callback might acquire other locks or call back into ThoughtBuffer.
	tb.mu.Unlock()
	defer tb.mu.Lock() // Re-acquire lock for deferred unlock in caller

	if callback != nil {
		callback(content)
	}
}
