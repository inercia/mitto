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
				// Check for paragraph break (double newline)
				if strings.HasSuffix(content, "\n\n") {
					mb.flushLocked()
					continue
				}
				// Flush if buffer is getting large
				if mb.buffer.Len() > maxBufferSize/2 {
					mb.flushLocked()
					continue
				}
			}
		}
	}

	// Force flush if buffer exceeds max size
	if mb.buffer.Len() >= maxBufferSize {
		mb.flushLocked()
		return
	}

	// Set timeout for eventual flush
	mb.flushTimer = time.AfterFunc(mb.flushTimeout, func() {
		mb.Flush()
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
