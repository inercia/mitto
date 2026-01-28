// Package web provides the web interface for Mitto.
package web

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
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
	md           goldmark.Markdown
	onFlush      func(html string)
	flushTimer   *time.Timer
	flushTimeout time.Duration
	inCodeBlock  bool
	inList       bool // Track if we're inside a list
	inTable      bool // Track if we're inside a table
}

// NewMarkdownBuffer creates a new streaming Markdown buffer.
func NewMarkdownBuffer(onFlush func(html string)) *MarkdownBuffer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM, // GitHub Flavored Markdown
			highlighting.NewHighlighting(
				highlighting.WithStyle("monokai"),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithXHTML(),
			html.WithUnsafe(), // Allow raw HTML in markdown
		),
	)

	return &MarkdownBuffer{
		md:           md,
		onFlush:      onFlush,
		flushTimeout: defaultFlushTimeout,
	}
}

// codeBlockPattern matches opening/closing code fences.
var codeBlockPattern = regexp.MustCompile("^```")

// listItemPattern matches list item lines (ordered or unordered).
// Matches: "1. ", "2. ", "- ", "* ", "+ " with optional leading whitespace
var listItemPattern = regexp.MustCompile(`^\s*(\d+\.\s+|[-*+]\s+)`)

// tableRowPattern matches table row lines (lines starting with |, with optional leading whitespace).
// Also matches table separator rows like |---|---|
var tableRowPattern = regexp.MustCompile(`^\s*\|`)

// hasUnmatchedInlineFormatting checks if the content has unmatched inline formatting markers.
// This includes **, *, _, and ` markers that would be broken if we flush mid-content.
func hasUnmatchedInlineFormatting(content string) bool {
	// Count occurrences of formatting markers
	// For **, we need to count pairs
	boldCount := strings.Count(content, "**")
	if boldCount%2 != 0 {
		return true
	}

	// For inline code, count backticks (but not code blocks which start with ```)
	// We need to be careful: ``` is a code block, `` is escaped backtick, ` is inline code
	// Simple approach: count single backticks that aren't part of ```
	inlineCodeCount := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '`' {
			// Check if this is part of a code fence (```)
			if i+2 < len(content) && content[i+1] == '`' && content[i+2] == '`' {
				// Skip the entire ``` sequence
				i += 2
				continue
			}
			// Check if this is a double backtick (``)
			if i+1 < len(content) && content[i+1] == '`' {
				// Skip the `` sequence
				i++
				continue
			}
			inlineCodeCount++
		}
	}
	if inlineCodeCount%2 != 0 {
		return true
	}

	return false
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
			if codeBlockPattern.MatchString(line) {
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
				if listItemPattern.MatchString(line) {
					mb.inList = true
				}

				// Track table state: entering a table when we see a table row
				if tableRowPattern.MatchString(line) {
					mb.inTable = true
				} else if mb.inTable && strings.TrimSpace(line) == "" {
					// Empty line or non-table line ends a table
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
				if mb.buffer.Len() > maxBufferSize/2 && !mb.inList && !mb.inTable && !hasUnmatchedInlineFormatting(mb.buffer.String()) {
					mb.flushLocked()
					continue
				}
			}
		}
	}

	// Force flush if buffer exceeds max size (but only if safe to flush)
	// Also force flush if buffer exceeds absolute maximum to prevent unbounded growth
	content := mb.buffer.String()
	safeToFlush := mb.endsWithCompleteLine() && !hasUnmatchedInlineFormatting(content)
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
		safeToFlush := mb.endsWithCompleteLine() && !hasUnmatchedInlineFormatting(content)
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
func (mb *MarkdownBuffer) Flush() {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.flushLocked()
}

// flushLocked performs the flush (must be called with lock held).
func (mb *MarkdownBuffer) flushLocked() {
	if mb.buffer.Len() == 0 {
		return
	}

	content := mb.buffer.String()
	mb.buffer.Reset()

	// Convert to HTML
	var htmlBuf bytes.Buffer
	if err := mb.md.Convert([]byte(content), &htmlBuf); err != nil {
		// On error, send as escaped text
		if mb.onFlush != nil {
			mb.onFlush("<pre>" + escapeHTML(content) + "</pre>")
		}
		return
	}

	htmlStr := htmlBuf.String()
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

// escapeHTML escapes special HTML characters.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
