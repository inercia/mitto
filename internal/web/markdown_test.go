package web

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMarkdownBuffer_BasicWrite(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	buffer.Write(1, "Hello, world!\n\n")
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	if !strings.Contains(html, "Hello, world!") {
		t.Errorf("expected HTML to contain 'Hello, world!', got %q", html)
	}

	if !strings.Contains(html, "<p>") {
		t.Errorf("expected HTML to contain <p> tag, got %q", html)
	}
}

func TestMarkdownBuffer_CodeBlock(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	buffer.Write(1, "```go\nfunc main() {}\n```\n")
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	if !strings.Contains(html, "<pre") || !strings.Contains(html, "<code") {
		t.Errorf("expected HTML to contain code block tags, got %q", html)
	}
}

func TestMarkdownBuffer_ParagraphBreakFlush(t *testing.T) {
	flushCount := 0
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		flushCount++
		mu.Unlock()
	})

	// Double newline should trigger flush
	buffer.Write(1, "First paragraph.\n\n")

	// Give time for flush
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := flushCount
	mu.Unlock()

	if count < 1 {
		t.Errorf("expected at least 1 flush after paragraph break, got %d", count)
	}

	buffer.Close()
}

func TestMarkdownBuffer_MaxBufferSize(t *testing.T) {
	flushCount := 0
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		flushCount++
		mu.Unlock()
	})

	// Write more than maxBufferSize (4096) bytes with newlines
	// Each line is 100 chars + newline, so 50 lines = 5050 bytes
	for i := 0; i < 50; i++ {
		buffer.Write(int64(i+1), strings.Repeat("x", 100)+"\n")
	}

	mu.Lock()
	count := flushCount
	mu.Unlock()

	if count < 1 {
		t.Errorf("expected flush when buffer exceeds max size, got %d flushes", count)
	}

	buffer.Close()
}

func TestMarkdownBuffer_FlushOnTimeout(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a complete line without triggering immediate flush conditions
	buffer.Write(1, "partial content\n")

	// Wait for timeout flush (default 200ms)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	html := result.String()
	mu.Unlock()

	if !strings.Contains(html, "partial content") {
		t.Errorf("expected timeout flush to include content, got %q", html)
	}

	buffer.Close()
}

func TestMarkdownBuffer_Close(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	buffer.Write(1, "final content")
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	if !strings.Contains(html, "final content") {
		t.Errorf("expected Close to flush remaining content, got %q", html)
	}
}

func TestMarkdownBuffer_Reset(t *testing.T) {
	flushCount := 0

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		flushCount++
	})

	buffer.Write(1, "some content")
	buffer.Reset()
	buffer.Close()

	// Reset should clear buffer without flushing, Close on empty buffer shouldn't flush
	if flushCount != 0 {
		t.Errorf("expected no flushes after Reset, got %d", flushCount)
	}
}

func TestMarkdownBuffer_CodeBlockDetection(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write opening fence - should not flush yet
	buffer.Write(1, "```python\n")
	buffer.Write(2, "print('hello')\n")
	// Write closing fence - should trigger flush
	buffer.Write(3, "```\n")

	// Give time for processing
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := len(results)
	mu.Unlock()

	if count < 1 {
		t.Errorf("expected flush after code block end, got %d flushes", count)
	}

	buffer.Close()
}

func TestMarkdownBuffer_NestedCodeBlocks(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Markdown with code block containing backticks in content
	buffer.Write(1, "```\nsome `inline` code\n```\n")
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	if !strings.Contains(html, "inline") {
		t.Errorf("expected code block content to be preserved, got %q", html)
	}
}

func TestMarkdownBuffer_EmptyFlush(t *testing.T) {
	flushCount := 0

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		flushCount++
	})

	// Flush on empty buffer should not call callback
	buffer.Flush()

	if flushCount != 0 {
		t.Errorf("expected no flush callback for empty buffer, got %d", flushCount)
	}

	buffer.Close()
}

func TestMarkdownBuffer_ConcurrentWrites(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Concurrent writes should not panic
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			buffer.Write(int64(n+1), "chunk ")
		}(i)
	}
	wg.Wait()

	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// Should have some content (exact count may vary due to flushing)
	if len(html) == 0 {
		t.Error("expected some HTML output from concurrent writes")
	}
}

func TestMarkdownBuffer_CodeBlockNoTimeoutFlush(t *testing.T) {
	// This test verifies that timeout-based flushing does NOT happen
	// while inside a code block, which would cause broken rendering.
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write opening fence
	buffer.Write(1, "```go\n")
	buffer.Write(2, "func main() {\n")

	// Wait longer than the timeout (200ms)
	time.Sleep(350 * time.Millisecond)

	mu.Lock()
	countBeforeClose := len(results)
	mu.Unlock()

	// Should NOT have flushed yet because we're still inside the code block
	if countBeforeClose > 0 {
		t.Errorf("expected no flush while inside code block, got %d flushes", countBeforeClose)
	}

	// Now close the code block
	buffer.Write(3, "}\n```\n")

	// Give time for the flush
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	countAfterClose := len(results)
	mu.Unlock()

	// Should have flushed now that code block is closed
	if countAfterClose < 1 {
		t.Errorf("expected flush after code block close, got %d flushes", countAfterClose)
	}

	buffer.Close()

	// Verify the HTML contains the complete code block
	mu.Lock()
	var fullHTML strings.Builder
	for _, r := range results {
		fullHTML.WriteString(r)
	}
	html := fullHTML.String()
	mu.Unlock()

	// The content should be in a single <pre> block, not split across multiple
	if !strings.Contains(html, "main") {
		t.Errorf("expected code block content to be preserved, got %q", html)
	}
	if !strings.Contains(html, "<pre") {
		t.Errorf("expected code block to be properly rendered, got %q", html)
	}
	// Count the number of <pre> tags - should be exactly 1 (not split)
	preCount := strings.Count(html, "<pre")
	if preCount != 1 {
		t.Errorf("expected exactly 1 <pre> tag (complete code block), got %d", preCount)
	}
}

// TestMarkdownBuffer_CodeBlockInactivityTimeout tests that the inactivity timeout
// (2 seconds) does NOT flush content while inside a code block.
func TestMarkdownBuffer_CodeBlockInactivityTimeout(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write opening fence and some code
	buffer.Write(1, "```go\n")
	buffer.Write(2, "func main() {\n")
	buffer.Write(3, "    fmt.Println(\"Hello\")\n")

	// Wait longer than the inactivity timeout (2 seconds)
	time.Sleep(2500 * time.Millisecond)

	mu.Lock()
	countBeforeClose := len(results)
	mu.Unlock()

	// Should NOT have flushed yet because we're still inside the code block
	if countBeforeClose > 0 {
		t.Errorf("expected no flush while inside code block (even after inactivity timeout), got %d flushes", countBeforeClose)
	}

	// Now close the code block
	buffer.Write(4, "}\n```\n")

	// Give time for the flush
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	countAfterClose := len(results)
	mu.Unlock()

	// Should have flushed now that code block is closed
	if countAfterClose < 1 {
		t.Errorf("expected flush after code block close, got %d flushes", countAfterClose)
	}

	buffer.Close()

	// Verify the HTML contains the complete code block
	mu.Lock()
	var fullHTML strings.Builder
	for _, r := range results {
		fullHTML.WriteString(r)
	}
	html := fullHTML.String()
	mu.Unlock()

	// The content should be in a single <pre> block, not split across multiple
	if !strings.Contains(html, "main") {
		t.Errorf("expected code block content to be preserved, got %q", html)
	}
	if !strings.Contains(html, "<pre") {
		t.Errorf("expected code block to be properly rendered, got %q", html)
	}
	// Count the number of <pre> tags - should be exactly 1 (not split)
	preCount := strings.Count(html, "<pre")
	if preCount != 1 {
		t.Errorf("expected exactly 1 <pre> tag (complete code block), got %d", preCount)
	}
}

func TestMarkdownBuffer_ListNoSplit(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a numbered list - should not be split mid-list
	buffer.Write(1, "Here are the changes:\n\n")
	buffer.Write(2, "1. First item\n")
	buffer.Write(3, "2. Second item\n")
	buffer.Write(4, "3. Third item\n")
	buffer.Write(5, "\n") // End of list (double newline)
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// Should have exactly one <ol> tag (list not split)
	olCount := strings.Count(html, "<ol>")
	if olCount != 1 {
		t.Errorf("expected exactly 1 <ol> tag (complete list), got %d in: %s", olCount, html)
	}

	// All list items should be present
	if !strings.Contains(html, "First item") {
		t.Errorf("expected 'First item' in output, got %q", html)
	}
	if !strings.Contains(html, "Second item") {
		t.Errorf("expected 'Second item' in output, got %q", html)
	}
	if !strings.Contains(html, "Third item") {
		t.Errorf("expected 'Third item' in output, got %q", html)
	}
}

func TestMarkdownBuffer_UnorderedListNoSplit(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write an unordered list
	buffer.Write(1, "Items:\n\n")
	buffer.Write(2, "- Apple\n")
	buffer.Write(3, "- Banana\n")
	buffer.Write(4, "- Cherry\n")
	buffer.Write(5, "\n") // End of list
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// Should have exactly one <ul> tag
	ulCount := strings.Count(html, "<ul>")
	if ulCount != 1 {
		t.Errorf("expected exactly 1 <ul> tag (complete list), got %d in: %s", ulCount, html)
	}
}

// TestMarkdownBuffer_NumberedListWithBlankLines tests the specific pattern that was
// causing issues: numbered lists with blank lines between items and nested sub-lists.
// This pattern is common in AI agent responses.
// Fixture extracted from session 20260208-212021-e4e71f40.
func TestMarkdownBuffer_NumberedListWithBlankLines(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// This is the exact pattern that was causing issues:
	// Numbered list items with nested bullet points and blank lines between them
	markdown := `The codebase now has:

1. **Robust sequence number handling**:
   - Backend assigns seq at receive time
   - Frontend updates lastSeenSeq immediately

2. **Improved reliability**:
   - Periodic persistence during long responses
   - Exponential backoff prevents thundering herd

3. **Comprehensive documentation**:
   - Formal sequence number contract
   - Updated rules files

4. **Strong test coverage**:
   - 33 new JavaScript unit tests
   - 18 new Go unit tests

Some text after the list.
`

	// Simulate streaming by writing chunks
	for i, char := range markdown {
		buffer.Write(int64(i+1), string(char))
	}
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	t.Logf("HTML output:\n%s", html)

	// Should have exactly one <ol> tag - the list should NOT be split
	olCount := strings.Count(html, "<ol>")
	if olCount != 1 {
		t.Errorf("expected exactly 1 <ol> tag (list should not be split), got %d", olCount)
	}

	// All list items should be present
	for _, item := range []string{"Robust sequence", "Improved reliability", "Comprehensive documentation", "Strong test coverage"} {
		if !strings.Contains(html, item) {
			t.Errorf("expected %q in output", item)
		}
	}

	// The nested bullet points should be present
	for _, item := range []string{"Backend assigns", "Periodic persistence", "Formal sequence", "33 new JavaScript"} {
		if !strings.Contains(html, item) {
			t.Errorf("expected nested item %q in output", item)
		}
	}
}

// TestMarkdownBuffer_NumberedListWithBlankLines_Streaming tests the same pattern
// but with more realistic streaming chunks (line by line instead of char by char).
func TestMarkdownBuffer_NumberedListWithBlankLines_Streaming(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Simulate streaming line by line (more realistic for ACP)
	lines := []string{
		"The codebase now has:\n",
		"\n",
		"1. **First feature**:\n",
		"   - Sub item 1\n",
		"   - Sub item 2\n",
		"\n",
		"2. **Second feature**:\n",
		"   - Sub item 3\n",
		"   - Sub item 4\n",
		"\n",
		"Some text after.\n",
	}

	for i, line := range lines {
		buffer.Write(int64(i+1), line)
	}
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// Should have exactly one <ol> tag
	olCount := strings.Count(html, "<ol>")
	if olCount != 1 {
		t.Errorf("expected exactly 1 <ol> tag (list should not be split), got %d\nHTML:\n%s", olCount, html)
	}

	// Both main items should be present
	if !strings.Contains(html, "First feature") {
		t.Error("expected 'First feature' in output")
	}
	if !strings.Contains(html, "Second feature") {
		t.Error("expected 'Second feature' in output")
	}
}

func TestMarkdownBuffer_TableNoSplit(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a markdown table - should not be split mid-table
	buffer.Write(1, "Here is a table:\n\n")
	buffer.Write(2, "| Category | Example Services |\n")
	buffer.Write(3, "|----------|------------------|\n")
	buffer.Write(4, "| Category A | Service 1, Service 2 |\n")
	buffer.Write(5, "| Category B | Service 3, Service 4 |\n")
	buffer.Write(6, "\n") // End of table (blank line)
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// Should have exactly one <table> tag (table not split)
	tableCount := strings.Count(html, "<table>")
	if tableCount != 1 {
		t.Errorf("expected exactly 1 <table> tag (complete table), got %d in: %s", tableCount, html)
	}

	// All table cells should be present
	if !strings.Contains(html, "Category A") {
		t.Errorf("expected 'Category A' in output, got %q", html)
	}
	if !strings.Contains(html, "Category B") {
		t.Errorf("expected 'Category B' in output, got %q", html)
	}
	if !strings.Contains(html, "Service 1") {
		t.Errorf("expected 'Service 1' in output, got %q", html)
	}
}

func TestMarkdownBuffer_LargeTableNoSplit(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a larger table that might exceed buffer thresholds
	buffer.Write(1, "| What Happened | Example Services | Description |\n")
	buffer.Write(2, "|---------------|------------------|-------------|\n")
	for i := 0; i < 20; i++ {
		buffer.Write(int64(i+3), "| Row "+string(rune('A'+i))+" | Service ABC, Service DEF, Service GHI | Long description text here |\n")
	}
	buffer.Write(23, "\n") // End of table
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// Should have exactly one <table> tag even for large tables
	tableCount := strings.Count(html, "<table>")
	if tableCount != 1 {
		t.Errorf("expected exactly 1 <table> tag (complete table), got %d", tableCount)
	}
}

func TestMarkdownBuffer_TableWithTimeoutNoSplit(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write table rows with pauses that would normally trigger timeout flush
	buffer.Write(1, "| Header 1 | Header 2 |\n")
	time.Sleep(100 * time.Millisecond) // Would trigger flush if not in table
	buffer.Write(2, "|----------|----------|\n")
	time.Sleep(100 * time.Millisecond)
	buffer.Write(3, "| Cell 1 | Cell 2 |\n")
	time.Sleep(100 * time.Millisecond)
	buffer.Write(4, "| Cell 3 | Cell 4 |\n")
	buffer.Write(5, "\n") // End of table

	// Wait a bit for potential timeout flush
	time.Sleep(300 * time.Millisecond)
	buffer.Close()

	mu.Lock()
	defer mu.Unlock()

	// Concatenate all results
	var fullHTML strings.Builder
	for _, r := range results {
		fullHTML.WriteString(r)
	}
	html := fullHTML.String()

	// The content should be in a single <table> block, not split across multiple
	tableCount := strings.Count(html, "<table>")
	if tableCount != 1 {
		t.Errorf("expected exactly 1 <table> tag (table not split), got %d in: %s", tableCount, html)
	}
}

func TestMarkdownBuffer_BoldTextNoSplit(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write bold text on a single line that could be split mid-bold
	buffer.Write(1, "Here is some **bold text that is important")
	time.Sleep(100 * time.Millisecond) // Would trigger flush if not detecting unmatched **
	buffer.Write(2, "** and more text\n")
	buffer.Write(3, "\n") // End of paragraph
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// The bold text should be properly rendered (not showing literal **)
	// If split incorrectly, we'd see "**" in the output
	if strings.Contains(html, "**") {
		t.Errorf("expected bold markers to be converted to HTML, but found literal ** in: %s", html)
	}

	// Should contain <strong> tag for bold text
	if !strings.Contains(html, "<strong>") {
		t.Errorf("expected <strong> tag for bold text, got: %s", html)
	}
}

func TestMarkdownBuffer_InlineCodeNoSplit(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write inline code that might be split
	buffer.Write(1, "Use the `some_function")
	time.Sleep(100 * time.Millisecond) // Would trigger flush if not detecting unmatched `
	buffer.Write(2, "_name` method\n")
	buffer.Write(3, "\n") // End of paragraph
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	// Should contain <code> tag for inline code
	if !strings.Contains(html, "<code>") {
		t.Errorf("expected <code> tag for inline code, got: %s", html)
	}

	// The backticks should be converted, not shown literally
	// Note: We check for isolated backticks, not those in code blocks
	if strings.Contains(html, "`some_function") || strings.Contains(html, "_name`") {
		t.Errorf("expected backticks to be converted to HTML, but found literal backticks in: %s", html)
	}
}

func TestMarkdownBuffer_SafeFlush_InTable(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write table header
	buffer.Write(1, "| File | Issue | Severity |\n")

	// Try SafeFlush while in table - should NOT flush
	flushed := buffer.SafeFlush()
	if flushed {
		t.Error("SafeFlush should return false while in table")
	}

	mu.Lock()
	if len(results) > 0 {
		t.Error("SafeFlush should not have flushed while in table")
	}
	mu.Unlock()

	// Write separator and data
	buffer.Write(2, "|------|-------|----------|\n")
	buffer.Write(3, "| auth.go | Missing check | High |\n")

	// Still in table (no empty line yet)
	flushed = buffer.SafeFlush()
	if flushed {
		t.Error("SafeFlush should return false while still in table")
	}

	// End table with empty line - this triggers auto-flush due to double newline
	buffer.Write(4, "\n")

	mu.Lock()
	defer mu.Unlock()

	// The auto-flush should have happened
	if len(results) == 0 {
		t.Error("Expected at least one flush result from auto-flush on double newline")
		return
	}

	html := strings.Join(results, "")
	if !strings.Contains(html, "<table>") {
		t.Errorf("Expected table HTML, got: %s", html)
	}
}

func TestMarkdownBuffer_SafeFlush_InCodeBlock(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write code block start
	buffer.Write(1, "```go\n")
	buffer.Write(2, "func main() {\n")

	// Try SafeFlush while in code block - should NOT flush
	flushed := buffer.SafeFlush()
	if flushed {
		t.Error("SafeFlush should return false while in code block")
	}

	mu.Lock()
	if len(results) > 0 {
		t.Error("SafeFlush should not have flushed while in code block")
	}
	mu.Unlock()

	// Write code block end - this triggers auto-flush
	buffer.Write(3, "}\n")
	buffer.Write(4, "```\n")

	mu.Lock()
	defer mu.Unlock()

	// The auto-flush should have happened after code block end
	if len(results) == 0 {
		t.Error("Expected at least one flush result after code block end")
		return
	}

	html := strings.Join(results, "")
	if !strings.Contains(html, "<code>") && !strings.Contains(html, "<pre>") {
		t.Errorf("Expected code HTML, got: %s", html)
	}
}

func TestMarkdownBuffer_SafeFlush_InList(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write list items
	buffer.Write(1, "- Item 1\n")

	// Try SafeFlush while in list - should NOT flush
	flushed := buffer.SafeFlush()
	if flushed {
		t.Error("SafeFlush should return false while in list")
	}

	mu.Lock()
	if len(results) > 0 {
		t.Error("SafeFlush should not have flushed while in list")
	}
	mu.Unlock()

	// Add more items
	buffer.Write(2, "- Item 2\n")

	// Still in list
	flushed = buffer.SafeFlush()
	if flushed {
		t.Error("SafeFlush should return false while still in list")
	}

	// Empty line after list - should NOT flush yet (might be between list items)
	buffer.Write(3, "\n")

	mu.Lock()
	if len(results) > 0 {
		t.Error("Should not flush on blank line in list - might be between items")
	}
	mu.Unlock()

	// Non-list content after blank line ends the list and triggers flush
	buffer.Write(4, "Some paragraph text.\n")

	mu.Lock()
	defer mu.Unlock()

	// The auto-flush should have happened now
	if len(results) == 0 {
		t.Error("Expected at least one flush result after list ends with non-list content")
		return
	}

	html := strings.Join(results, "")
	if !strings.Contains(html, "<li>") || !strings.Contains(html, "<ul>") {
		t.Errorf("Expected list HTML, got: %s", html)
	}
}

// TestMarkdownBuffer_ListWithUnmatchedBold tests that a list with unmatched bold
// formatting is NOT flushed when the list ends, to avoid rendering broken markdown.
// The key insight is that we should wait for more content that might close the bold.
func TestMarkdownBuffer_ListWithUnmatchedBold(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write list items with item 4 having unmatched bold
	buffer.Write(1, "1. **First item** - Description\n")
	buffer.Write(2, "2. **Second item** - Description\n")
	buffer.Write(3, "3. **Third item** - Description\n")
	buffer.Write(4, "4. **Real-time\n") // Unmatched bold!

	// Blank line - would normally end the list
	buffer.Write(5, "\n")

	// Non-list content - this triggers "list has ended" logic
	// But the closing ** is in this content!
	buffer.Write(6, "messaging works after refresh** - New messages\n")

	mu.Lock()
	// At this point, the list should NOT have been flushed because of unmatched **
	// The fix should prevent flushing until the ** is matched
	for _, html := range results {
		if strings.Contains(html, "**Real-time") {
			t.Errorf("List with unmatched ** should NOT have been flushed mid-stream. Got: %s", html)
		}
	}
	resultCount := len(results)
	mu.Unlock()

	// The content should still be buffered (not flushed yet)
	// because the list has unmatched formatting
	if resultCount > 0 {
		t.Logf("Note: %d flush(es) happened before Close(). This is expected if the soft timeout fired.", resultCount)
	}

	// Now close the buffer to force flush
	buffer.Close()

	mu.Lock()
	defer mu.Unlock()

	// After close, everything should be flushed
	html := strings.Join(results, "")
	t.Logf("Final HTML: %s", html)

	// The content should be flushed as a single unit
	// Since the markdown is malformed (bold spans across blank line),
	// the goldmark parser will treat them as separate blocks.
	// The key test is that we didn't flush the list SEPARATELY with broken formatting.
	// If we flushed correctly (all at once at Close), the list and paragraph
	// will be in the same flush result.
	if resultCount > 1 {
		t.Errorf("Expected at most 1 flush before Close() (from soft timeout), got %d", resultCount)
	}
}

func TestMarkdownBuffer_ToolCallDoesNotSplitTable(t *testing.T) {
	// Simulates the real scenario: table is streaming, then a tool call happens
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Simulate table streaming
	buffer.Write(1, "| File | Issue | Severity |\n")
	buffer.Write(2, "|------|-------|----------|\n")

	// Simulate tool call event - using SafeFlush (what WebClient does now)
	flushed := buffer.SafeFlush()
	if flushed {
		t.Error("SafeFlush during table should return false")
	}

	// Continue table
	buffer.Write(3, "| auth.go | Missing | High |\n")
	buffer.Write(4, "\n") // End of table

	// Final flush
	buffer.Flush()

	mu.Lock()
	defer mu.Unlock()

	// Concatenate all results
	html := strings.Join(results, "")

	// Should have exactly one <table> - not split
	tableCount := strings.Count(html, "<table>")
	if tableCount != 1 {
		t.Errorf("Expected exactly 1 <table> (not split), got %d in: %s", tableCount, html)
	}

	// Should not have raw pipes visible (would indicate broken table)
	if strings.Contains(html, "| File |") || strings.Contains(html, "|------|") {
		t.Errorf("Table was not rendered correctly - raw markdown visible: %s", html)
	}
}

func TestMarkdownBuffer_MermaidDiagram(t *testing.T) {
	// Test that mermaid code blocks are converted to <pre class="mermaid">
	// for frontend rendering by Mermaid.js
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(seq int64, html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a mermaid diagram block
	buffer.Write(1, "Here is a diagram:\n\n")
	buffer.Write(1, "```mermaid\n")
	buffer.Write(1, "graph TD\n")
	buffer.Write(1, "    A[Start] --> B{Decision}\n")
	buffer.Write(1, "    B -->|Yes| C[Action]\n")
	buffer.Write(1, "```\n\n")
	buffer.Write(1, "And some text after.\n")

	buffer.Close()

	html := result.String()
	t.Logf("HTML output:\n%s", html)

	// Check that the mermaid class is present
	if !strings.Contains(html, `class="mermaid"`) {
		t.Errorf("Expected class=\"mermaid\" attribute, got:\n%s", html)
	}

	// Check that raw markdown code fence is NOT visible
	if strings.Contains(html, "```") {
		t.Errorf("Raw markdown code fence should not be visible in HTML output:\n%s", html)
	}

	// Check that the word "mermaid" as language identifier is NOT visible as content
	// (it should only appear in class="mermaid", not as text content)
	if strings.Contains(html, ">mermaid<") {
		t.Errorf("Language identifier 'mermaid' should not appear as visible text:\n%s", html)
	}

	// Check that the diagram content is present
	if !strings.Contains(html, "graph TD") {
		t.Errorf("Expected diagram content 'graph TD' in output:\n%s", html)
	}
}

// TestMarkdownBuffer_StreamingFixtures tests streaming with fixtures from the conversion package.
// These fixtures represent real-world markdown patterns that caused issues in production.
func TestMarkdownBuffer_StreamingFixtures(t *testing.T) {
	// Test cases with fixture names and expected properties
	testCases := []struct {
		name           string
		fixture        string
		expectedOLTags int // Expected number of <ol> tags (0 means don't check)
		expectedULTags int // Expected number of <ul> tags (0 means don't check)
	}{
		{
			name:           "ordered_list_with_blank_lines",
			fixture:        "ordered_list_with_blank_lines",
			expectedOLTags: 1, // Should be a single <ol> with all items
		},
		{
			name:           "ordered_list_spaced",
			fixture:        "ordered_list_spaced",
			expectedOLTags: 1,
		},
		{
			name:           "nested_list",
			fixture:        "nested_list",
			expectedULTags: 3, // One outer <ul> and two nested <ul>
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Read the fixture markdown
			mdPath := "../conversion/testdata/" + tc.fixture + ".md"
			mdContent, err := os.ReadFile(mdPath)
			if err != nil {
				t.Fatalf("Failed to read fixture %s: %v", mdPath, err)
			}

			var result strings.Builder
			var mu sync.Mutex

			buffer := NewMarkdownBuffer(func(seq int64, html string) {
				mu.Lock()
				result.WriteString(html)
				mu.Unlock()
			})

			// Simulate streaming line by line
			lines := strings.Split(string(mdContent), "\n")
			for i, line := range lines {
				// Add newline back except for the last empty line
				if i < len(lines)-1 || line != "" {
					buffer.Write(int64(i+1), line+"\n")
				}
			}
			buffer.Close()

			mu.Lock()
			html := result.String()
			mu.Unlock()

			// Check expected <ol> tags
			if tc.expectedOLTags > 0 {
				olCount := strings.Count(html, "<ol>")
				if olCount != tc.expectedOLTags {
					t.Errorf("expected %d <ol> tag(s), got %d\nHTML:\n%s", tc.expectedOLTags, olCount, html)
				}
			}

			// Check expected <ul> tags
			if tc.expectedULTags > 0 {
				ulCount := strings.Count(html, "<ul>")
				if ulCount != tc.expectedULTags {
					t.Errorf("expected %d <ul> tag(s), got %d\nHTML:\n%s", tc.expectedULTags, ulCount, html)
				}
			}
		})
	}
}
