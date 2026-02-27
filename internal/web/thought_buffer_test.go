package web

import (
	"sync"
	"testing"
	"time"
)

func TestThoughtBuffer_CoalescesChunks(t *testing.T) {
	var flushed []string
	var mu sync.Mutex

	tb := NewThoughtBuffer(ThoughtBufferConfig{
		OnFlush: func(text string) {
			mu.Lock()
			flushed = append(flushed, text)
			mu.Unlock()
		},
	})
	defer tb.Close()

	// Write multiple chunks rapidly
	tb.Write("Hello ")
	tb.Write("world ")
	tb.Write("from ")
	tb.Write("thoughts!")

	// Wait for flush timeout
	time.Sleep(thoughtFlushTimeout + 50*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(flushed) != 1 {
		t.Errorf("expected 1 flush, got %d", len(flushed))
	}
	if flushed[0] != "Hello world from thoughts!" {
		t.Errorf("expected coalesced text, got %q", flushed[0])
	}
}

func TestThoughtBuffer_ForceFlush(t *testing.T) {
	var flushed []string

	tb := NewThoughtBuffer(ThoughtBufferConfig{
		OnFlush: func(text string) {
			flushed = append(flushed, text)
		},
	})
	defer tb.Close()

	// Write chunks
	tb.Write("Part 1 ")
	tb.Write("Part 2")

	// Force flush immediately
	tb.ForceFlush()

	if len(flushed) != 1 {
		t.Errorf("expected 1 flush after ForceFlush, got %d", len(flushed))
	}
	if flushed[0] != "Part 1 Part 2" {
		t.Errorf("expected coalesced text, got %q", flushed[0])
	}
}

func TestThoughtBuffer_EmptyChunksIgnored(t *testing.T) {
	var flushed []string

	tb := NewThoughtBuffer(ThoughtBufferConfig{
		OnFlush: func(text string) {
			flushed = append(flushed, text)
		},
	})
	defer tb.Close()

	// Write empty chunks
	tb.Write("")
	tb.Write("")

	// Force flush
	tb.ForceFlush()

	// Should not have flushed anything
	if len(flushed) != 0 {
		t.Errorf("expected 0 flushes for empty chunks, got %d", len(flushed))
	}
}

func TestThoughtBuffer_MultipleFlushes(t *testing.T) {
	var flushed []string
	var mu sync.Mutex

	tb := NewThoughtBuffer(ThoughtBufferConfig{
		OnFlush: func(text string) {
			mu.Lock()
			flushed = append(flushed, text)
			mu.Unlock()
		},
	})
	defer tb.Close()

	// First batch
	tb.Write("First ")
	tb.Write("thought")
	time.Sleep(thoughtFlushTimeout + 50*time.Millisecond)

	// Second batch
	tb.Write("Second ")
	tb.Write("thought")
	time.Sleep(thoughtFlushTimeout + 50*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(flushed) != 2 {
		t.Errorf("expected 2 flushes, got %d", len(flushed))
	}
	if flushed[0] != "First thought" {
		t.Errorf("expected first coalesced text, got %q", flushed[0])
	}
	if flushed[1] != "Second thought" {
		t.Errorf("expected second coalesced text, got %q", flushed[1])
	}
}

func TestThoughtBuffer_Close(t *testing.T) {
	var flushed []string

	tb := NewThoughtBuffer(ThoughtBufferConfig{
		OnFlush: func(text string) {
			flushed = append(flushed, text)
		},
	})

	// Write without waiting for timeout
	tb.Write("Pending content")

	// Close should flush
	tb.Close()

	if len(flushed) != 1 {
		t.Errorf("expected 1 flush on Close, got %d", len(flushed))
	}
	if flushed[0] != "Pending content" {
		t.Errorf("expected content to be flushed, got %q", flushed[0])
	}
}
