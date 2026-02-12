// Package web provides the web interface for Mitto.
package web

import (
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/conversion"
	"github.com/inercia/mitto/internal/logging"
)

const (
	// defaultFlushTimeout is the default timeout for flushing buffered content.
	// This is the "soft" timeout that respects block boundaries.
	defaultFlushTimeout = 200 * time.Millisecond
	// inactivityFlushTimeout is the timeout for forcing a flush regardless of block state.
	// This ensures content is displayed even if the agent stops mid-block.
	// Set to 2 seconds to give the agent time to complete blocks normally.
	inactivityFlushTimeout = 2 * time.Second
	// maxBufferSize is the maximum buffer size before forcing a flush.
	maxBufferSize = 4096
	// maxCodeBlockBufferSize is the absolute maximum buffer size, even inside code blocks.
	// This prevents unbounded memory growth if the closing ``` is missing.
	maxCodeBlockBufferSize = 65536 // 64KB
)

// MarkdownBuffer accumulates streaming text and converts to HTML intelligently.
// It buffers chunks until semantic boundaries (lines, code blocks, paragraphs)
// are detected, then converts to HTML and sends via callback.
//
// Sequence numbers (seq) are tracked through the buffer to maintain correct
// event ordering. When content is buffered, the seq from the first chunk is
// preserved and passed to the onFlush callback when the content is flushed.
type MarkdownBuffer struct {
	mu              sync.Mutex
	buffer          strings.Builder
	converter       *conversion.Converter
	onFlush         func(seq int64, html string)
	flushTimer      *time.Timer // Soft timeout (respects block boundaries)
	inactivityTimer *time.Timer // Hard timeout (forces flush regardless of state)
	flushTimeout    time.Duration
	inCodeBlock     bool
	inList          bool  // Track if we're inside a list
	inTable         bool  // Track if we're inside a table
	pendingSeq      int64 // Seq for buffered content (from first chunk)
	sawBlankLine    bool  // Track if we saw a blank line (potential list/paragraph end)
}

// MarkdownBufferConfig holds configuration for creating a MarkdownBuffer.
type MarkdownBufferConfig struct {
	// OnFlush is called when HTML content is ready to be sent.
	// The seq parameter is the sequence number from when the content was received.
	OnFlush func(seq int64, html string)
	// FileLinksConfig configures file path detection and linking.
	// If nil, file linking is disabled.
	FileLinksConfig *conversion.FileLinkerConfig
}

// NewMarkdownBuffer creates a new streaming Markdown buffer.
// Deprecated: Use NewMarkdownBufferWithConfig instead for seq support.
func NewMarkdownBuffer(onFlush func(seq int64, html string)) *MarkdownBuffer {
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
// The seq parameter is the sequence number assigned when this content was received
// from ACP. If the buffer is empty, this seq becomes the pendingSeq for the buffered
// content. If content is already buffered, the original seq is preserved (first wins).
func (mb *MarkdownBuffer) Write(seq int64, chunk string) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	// Cancel any pending timeout flush
	if mb.flushTimer != nil {
		mb.flushTimer.Stop()
	}

	// Track seq for buffered content (first chunk's seq wins)
	if mb.buffer.Len() == 0 {
		mb.pendingSeq = seq
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
				isBlankLine := strings.TrimSpace(line) == ""
				isListItem := conversion.IsListItem(line)

				// Handle list continuation after blank line
				// If we saw a blank line and now see a list item, the list continues
				if mb.sawBlankLine && isListItem {
					// List continues after blank line - don't flush
					mb.sawBlankLine = false
					// Re-enter list state since we're continuing the list
					mb.inList = true
					// Don't continue - we still need to process this line normally
					// to track list state for nested items
				}

				// If we saw a blank line and now see non-list content, the list has ended
				if mb.sawBlankLine && !isBlankLine && !isListItem && mb.inList {
					// List has ended - flush the entire list as one unit
					// The buffer currently contains: list + blank line + new content
					// We need to extract and flush just the list part

					// Find where the new content starts (after the last \n\n)
					// We search in content excluding just the current line (not line+1)
					// because the \n\n might be right before the current line
					searchEnd := len(content) - len(line)
					if searchEnd < 0 {
						searchEnd = 0
					}
					lastDoubleNewline := strings.LastIndex(content[:searchEnd], "\n\n")
					if lastDoubleNewline >= 0 {
						// Flush everything up to and including the blank line
						listContent := content[:lastDoubleNewline+2]
						remainingContent := content[lastDoubleNewline+2:]

						// Check if the list content has unmatched formatting
						// If so, don't flush yet - wait for more content
						hasUnmatched := conversion.HasUnmatchedInlineFormatting(listContent)
						if hasUnmatched {
							// Don't flush - the list has unmatched formatting
							// Keep sawBlankLine true so we continue waiting
							continue
						}

						// Reset buffer and write list content, then flush
						mb.buffer.Reset()
						mb.buffer.WriteString(listContent)
						mb.inList = false
						mb.sawBlankLine = false
						savedSeq := mb.pendingSeq
						mb.flushLocked()

						// Re-add the remaining content with a new seq
						mb.buffer.WriteString(remainingContent)
						// Preserve the seq from the remaining content's first line
						// Since we don't track per-line seq, use the original seq
						if mb.buffer.Len() > 0 && mb.pendingSeq == 0 {
							mb.pendingSeq = savedSeq
						}
					} else {
						// Fallback: check for unmatched formatting before flushing
						if conversion.HasUnmatchedInlineFormatting(content) {
							// Don't flush - content has unmatched formatting
							continue
						}
						mb.inList = false
						mb.sawBlankLine = false
						mb.flushLocked()
					}
					continue
				}

				// Track list state: entering a list when we see a list item
				if isListItem {
					mb.inList = true
					mb.sawBlankLine = false
				}

				// Track table state: entering a table when we see a table row
				if conversion.IsTableRow(line) {
					mb.inTable = true
				} else if mb.inTable && isBlankLine {
					// Empty line ends a table
					mb.inTable = false
				}

				// Check for paragraph break (double newline)
				if strings.HasSuffix(content, "\n\n") {
					if mb.inList {
						// In a list context, blank line might be between list items
						// Don't flush yet - wait to see if next line is a list item
						mb.sawBlankLine = true
						continue
					}
					// Not in a list - double newline ends the paragraph
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

	// Set soft timeout for eventual flush (respects block boundaries)
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

	// Set hard inactivity timeout that forces flush regardless of block state.
	// This ensures content is displayed even if the agent stops mid-block.
	// Only set if not already set (we don't want to keep resetting it).
	if mb.inactivityTimer == nil {
		mb.inactivityTimer = time.AfterFunc(inactivityFlushTimeout, func() {
			mb.mu.Lock()
			defer mb.mu.Unlock()
			// Force flush if there's any content, but respect block boundaries
			// and unmatched formatting to avoid rendering broken markdown
			if mb.buffer.Len() > 0 {
				// Don't flush if we're in a code block, list, or table - this would split the block
				if mb.inCodeBlock || mb.inList || mb.inTable {
					return
				}
				content := mb.buffer.String()
				// Don't flush if we have unmatched inline formatting - this would
				// render broken markdown like "**Real-time" without the closing "**"
				if conversion.HasUnmatchedInlineFormatting(content) {
					return
				}
				mb.flushLocked()
			}
		})
	}
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
	log := logging.Web()

	if mb.buffer.Len() == 0 {
		log.Debug("markdown_buffer_flush_empty",
			"pending_seq", mb.pendingSeq,
			"in_code_block", mb.inCodeBlock,
			"in_list", mb.inList,
			"in_table", mb.inTable)
		return
	}

	// Reset inactivity timer since we're flushing
	if mb.inactivityTimer != nil {
		mb.inactivityTimer.Stop()
		mb.inactivityTimer = nil
	}

	content := mb.buffer.String()

	// Preprocess: Join list item continuations that were split by blank lines
	// This handles malformed markdown where a list item is split like:
	//   1. Start of item (with open paren
	//
	//   continuation with close paren)
	content = joinListItemContinuations(content)

	contentLen := len(content)
	seq := mb.pendingSeq // Capture seq before reset
	mb.buffer.Reset()
	mb.pendingSeq = 0

	// Check for unmatched formatting before converting
	hasUnmatched := conversion.HasUnmatchedInlineFormatting(content)

	// Convert to HTML using the converter (which handles sanitization)
	htmlStr := mb.converter.ConvertToSafeHTML(content)
	htmlLen := len(htmlStr)

	// Log flush for debugging message content issues
	if hasUnmatched {
		// Log when flushing content with unmatched formatting
		preview := content
		if len(preview) > 200 {
			preview = content[:100] + "..." + content[contentLen-100:]
		}
		log.Debug("markdown_buffer_flush_unmatched_formatting",
			"seq", seq,
			"content_len", contentLen,
			"html_len", htmlLen,
			"in_code_block", mb.inCodeBlock,
			"in_list", mb.inList,
			"in_table", mb.inTable,
			"content_preview", preview)
	} else if htmlLen > 1000 {
		// Large content - log with preview
		preview := htmlStr
		if len(preview) > 200 {
			preview = htmlStr[:100] + "..." + htmlStr[htmlLen-100:]
		}
		log.Debug("markdown_buffer_flush_large",
			"seq", seq,
			"content_len", contentLen,
			"html_len", htmlLen,
			"in_code_block", mb.inCodeBlock,
			"in_list", mb.inList,
			"in_table", mb.inTable,
			"preview", preview)
	} else {
		log.Debug("markdown_buffer_flush",
			"seq", seq,
			"content_len", contentLen,
			"html_len", htmlLen)
	}

	if htmlStr != "" && mb.onFlush != nil {
		log.Debug("markdown_buffer_flush_callback",
			"seq", seq,
			"html_len", htmlLen,
			"html_preview", truncateForLog(htmlStr, 100))
		mb.onFlush(seq, htmlStr)
	} else if htmlStr == "" {
		log.Debug("markdown_buffer_flush_empty_html",
			"seq", seq,
			"content_len", contentLen,
			"content_preview", truncateForLog(content, 100))
	}
}

// truncateForLog truncates a string for logging purposes.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Close flushes any remaining content and cleans up.
func (mb *MarkdownBuffer) Close() {
	mb.mu.Lock()
	if mb.flushTimer != nil {
		mb.flushTimer.Stop()
	}
	if mb.inactivityTimer != nil {
		mb.inactivityTimer.Stop()
		mb.inactivityTimer = nil
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
	if mb.inactivityTimer != nil {
		mb.inactivityTimer.Stop()
		mb.inactivityTimer = nil
	}
	mb.buffer.Reset()
	mb.inCodeBlock = false
	mb.inList = false
	mb.inTable = false
	mb.sawBlankLine = false
	mb.pendingSeq = 0
}

// InBlock returns true if the buffer is currently in the middle of a
// structured markdown block (code block, list, or table) that shouldn't
// be interrupted by non-markdown events.
func (mb *MarkdownBuffer) InBlock() bool {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.inCodeBlock || mb.inList || mb.inTable
}

// HasPendingContent returns true if there is buffered content waiting to be flushed.
func (mb *MarkdownBuffer) HasPendingContent() bool {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.buffer.Len() > 0
}

// joinListItemContinuations preprocesses markdown to join list item continuations
// that were split by blank lines. This handles malformed markdown where a list item
// is split like:
//
//  1. Start of item (with open paren
//
//     continuation with close paren)
//
// Or with inline code:
//
//  1. Check for `something
//
//     `), which continues here
//
// Or with multiple continuations:
//
//  1. Start (with open
//
//     continuation
//
//     more continuation
//
// The function detects this pattern by looking for:
// 1. A list item line with unmatched parentheses OR unmatched backticks
// 2. Followed by a blank line
// 3. Followed by a line that looks like a continuation (lowercase, backtick, closing paren, etc.)
//
// It joins these by indenting the continuation to be part of the list item.
// It also handles multiple consecutive continuations.
func joinListItemContinuations(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 {
		return content
	}

	result := make([]string, 0, len(lines))
	i := 0

	for i < len(lines) {
		line := lines[i]

		// Check if this is a list item with unmatched formatting
		if conversion.IsListItem(line) {
			// Collect all the content that belongs to this list item
			listItemLines := []string{line}
			j := i + 1

			// Keep looking for continuations
			for j < len(lines) {
				// Check if current accumulated content has unmatched formatting
				accumulated := strings.Join(listItemLines, "\n")
				openParens := strings.Count(accumulated, "(")
				closeParens := strings.Count(accumulated, ")")
				backticks := countInlineBackticks(accumulated)

				hasUnmatchedParens := openParens > closeParens
				hasUnmatchedBackticks := backticks%2 != 0

				// If no unmatched formatting, stop looking for continuations
				if !hasUnmatchedParens && !hasUnmatchedBackticks {
					break
				}

				// Check for blank line followed by continuation
				if j+1 < len(lines) {
					currentLine := lines[j]
					nextLine := lines[j+1]

					if strings.TrimSpace(currentLine) == "" && len(nextLine) > 0 {
						if looksLikeContinuation(nextLine) {
							// Add the continuation with indentation
							listItemLines = append(listItemLines, "   "+nextLine)
							j += 2
							continue
						}
					}
				}

				// No more continuations found
				break
			}

			// Add all collected lines
			result = append(result, listItemLines...)
			i = j
			continue
		}

		result = append(result, line)
		i++
	}

	return strings.Join(result, "\n")
}

// countInlineBackticks counts backticks that are not part of code fences.
func countInlineBackticks(s string) int {
	count := 0
	i := 0
	for i < len(s) {
		if s[i] == '`' {
			// Check if this is part of a code fence (```)
			if i+2 < len(s) && s[i+1] == '`' && s[i+2] == '`' {
				i += 3
				continue
			}
			count++
		}
		i++
	}
	return count
}

// looksLikeContinuation checks if a line looks like a continuation of a previous
// sentence or inline code, rather than a new paragraph.
func looksLikeContinuation(line string) bool {
	if len(line) == 0 {
		return false
	}

	firstChar := rune(line[0])

	// Lowercase letter - likely continuation of sentence
	if firstChar >= 'a' && firstChar <= 'z' {
		return true
	}

	// Backtick - likely continuation of inline code
	if firstChar == '`' {
		return true
	}

	// Closing punctuation - likely continuation
	if firstChar == ')' || firstChar == ']' || firstChar == '}' {
		return true
	}

	// Comma, period, semicolon, colon after space - likely continuation
	if firstChar == ',' || firstChar == ';' {
		return true
	}

	return false
}
