// Package web provides the web interface for Mitto.
package web

import (
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/conversion"
)

const (
	// defaultFlushTimeout is the default timeout for flushing buffered content.
	defaultFlushTimeout = 200 * time.Millisecond
	// maxBufferSize is the maximum buffer size before forcing a flush.
	maxBufferSize = 4096
	// maxCodeBlockBufferSize is the absolute maximum buffer size, even inside code blocks.
	// This prevents unbounded memory growth if the closing ``` is missing.
	maxCodeBlockBufferSize = 65536 // 64KB
)

// MarkdownBuffer accumulates streaming text and converts to HTML intelligently.
// It buffers chunks until semantic boundaries (lines, code blocks, paragraphs)
// are detected, then converts to HTML and sends via callback.
type MarkdownBuffer struct {
	mu           sync.Mutex
	buffer       strings.Builder
	converter    *conversion.Converter
	onFlush      func(html string)
	flushTimer   *time.Timer
	flushTimeout time.Duration
	inCodeBlock  bool
	inList       bool // Track if we're inside a list
	inTable      bool // Track if we're inside a table
}

// MarkdownBufferConfig holds configuration for creating a MarkdownBuffer.
type MarkdownBufferConfig struct {
	// OnFlush is called when HTML content is ready to be sent.
	OnFlush func(html string)
	// FileLinksConfig configures file path detection and linking.
	// If nil, file linking is disabled.
	FileLinksConfig *conversion.FileLinkerConfig
}

// NewMarkdownBuffer creates a new streaming Markdown buffer.
func NewMarkdownBuffer(onFlush func(html string)) *MarkdownBuffer {
	return &MarkdownBuffer{
		converter:    conversion.DefaultConverter(),
		onFlush:      onFlush,
		flushTimeout: defaultFlushTimeout,
	}
}

// NewMarkdownBufferWithConfig creates a new streaming Markdown buffer with configuration.
func NewMarkdownBufferWithConfig(cfg MarkdownBufferConfig) *MarkdownBuffer {
	opts := []conversion.Option{
		conversion.WithHighlighting("monokai"),
		conversion.WithSanitization(conversion.CreateSanitizer()),
	}

	// Add file linking if configured
	if cfg.FileLinksConfig != nil {
		opts = append(opts, conversion.WithFileLinks(*cfg.FileLinksConfig))
	}

	return &MarkdownBuffer{
		converter:    conversion.NewConverter(opts...),
		onFlush:      cfg.OnFlush,
		flushTimeout: defaultFlushTimeout,
	}
}

// Write adds a chunk of text to the buffer and triggers smart flushing.
func (mb *MarkdownBuffer) Write(chunk string) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	// Cancel any pending timeout flush
	if mb.flushTimer != nil {
		mb.flushTimer.Stop()
	}

	// Process chunk character by character for code block detection
	for _, char := range chunk {
		mb.buffer.WriteRune(char)

		// Check for code block boundaries on newline
		if char == '\n' {
			line := mb.getLastLine()
			if conversion.IsCodeBlockStart(line) {
				mb.inCodeBlock = !mb.inCodeBlock
				if !mb.inCodeBlock {
					// End of code block - flush the complete block
					mb.flushLocked()
					continue
				}
			}

			// If not in code block, check for flush conditions
			if !mb.inCodeBlock {
				content := mb.buffer.String()

				// Track list state: entering a list when we see a list item
				if conversion.IsListItem(line) {
					mb.inList = true
				}

				// Track table state: entering a table when we see a table row
				if conversion.IsTableRow(line) {
					mb.inTable = true
				} else if mb.inTable && strings.TrimSpace(line) == "" {
					// Empty line ends a table
					mb.inTable = false
				}

				// Check for paragraph break (double newline)
				if strings.HasSuffix(content, "\n\n") {
					// Double newline ends a list and table
					mb.inList = false
					mb.inTable = false
					mb.flushLocked()
					continue
				}

				// Don't flush mid-list, mid-table, or with unmatched formatting
				// Only flush if buffer is getting large AND we're not in a list/table AND formatting is complete
				if mb.buffer.Len() > maxBufferSize/2 && !mb.inList && !mb.inTable && !conversion.HasUnmatchedInlineFormatting(mb.buffer.String()) {
					mb.flushLocked()
					continue
				}
			}
		}
	}

	// Force flush if buffer exceeds max size (but only if safe to flush)
	// Also force flush if buffer exceeds absolute maximum to prevent unbounded growth
	content := mb.buffer.String()
	safeToFlush := mb.endsWithCompleteLine() && !conversion.HasUnmatchedInlineFormatting(content)
	if mb.buffer.Len() >= maxBufferSize && !mb.inCodeBlock && !mb.inList && !mb.inTable && safeToFlush {
		mb.flushLocked()
		return
	}
	if mb.buffer.Len() >= maxCodeBlockBufferSize && safeToFlush {
		mb.flushLocked()
		return
	}

	// Set timeout for eventual flush (but only if safe to flush)
	mb.flushTimer = time.AfterFunc(mb.flushTimeout, func() {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		// Don't flush if we're in the middle of a code block, list, table, incomplete line, or unmatched formatting
		content := mb.buffer.String()
		safeToFlush := mb.endsWithCompleteLine() && !conversion.HasUnmatchedInlineFormatting(content)
		if mb.inCodeBlock || mb.inList || mb.inTable || !safeToFlush {
			return
		}
		mb.flushLocked()
	})
}

// getLastLine returns the last line in the buffer (after last \n or from start).
func (mb *MarkdownBuffer) getLastLine() string {
	content := mb.buffer.String()
	if len(content) == 0 {
		return ""
	}
	// Find the second-to-last newline (since content ends with \n)
	lastNewline := strings.LastIndex(content[:len(content)-1], "\n")
	if lastNewline == -1 {
		return content
	}
	return content[lastNewline+1:]
}

// endsWithCompleteLine returns true if the buffer ends with a newline.
// This ensures we don't flush mid-line, which would break inline formatting.
func (mb *MarkdownBuffer) endsWithCompleteLine() bool {
	content := mb.buffer.String()
	return len(content) > 0 && content[len(content)-1] == '\n'
}

// Flush converts buffered content to HTML and sends it.
// This is a "force flush" that ignores table/list/code block state.
// Use SafeFlush() for event interleaving that respects markdown boundaries.
func (mb *MarkdownBuffer) Flush() {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.flushLocked()
}

// SafeFlush flushes buffered content only if it's safe to do so.
// It will NOT flush if we're in the middle of a table, list, or code block
// to avoid rendering incomplete markdown structures.
// Returns true if content was flushed, false if flush was skipped.
func (mb *MarkdownBuffer) SafeFlush() bool {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	// Don't flush if we're in the middle of a structured block
	if mb.inCodeBlock || mb.inList || mb.inTable {
		return false
	}

	// Don't flush incomplete lines or unmatched formatting
	content := mb.buffer.String()
	if !mb.endsWithCompleteLine() || conversion.HasUnmatchedInlineFormatting(content) {
		return false
	}

	mb.flushLocked()
	return true
}

// flushLocked performs the flush (must be called with lock held).
func (mb *MarkdownBuffer) flushLocked() {
	if mb.buffer.Len() == 0 {
		return
	}

	content := mb.buffer.String()
	mb.buffer.Reset()

	// Convert to HTML using the converter (which handles sanitization)
	htmlStr := mb.converter.ConvertToSafeHTML(content)
	if htmlStr != "" && mb.onFlush != nil {
		mb.onFlush(htmlStr)
	}
}

// Close flushes any remaining content and cleans up.
func (mb *MarkdownBuffer) Close() {
	mb.mu.Lock()
	if mb.flushTimer != nil {
		mb.flushTimer.Stop()
	}
	mb.mu.Unlock()
	mb.Flush()
}

// Reset clears the buffer without flushing.
func (mb *MarkdownBuffer) Reset() {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	if mb.flushTimer != nil {
		mb.flushTimer.Stop()
	}
	mb.buffer.Reset()
	mb.inCodeBlock = false
	mb.inList = false
	mb.inTable = false
}
