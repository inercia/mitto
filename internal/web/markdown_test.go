package web

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMarkdownBuffer_BasicWrite(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	buffer.Write("Hello, world!\n\n")
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	buffer.Write("```go\nfunc main() {}\n```\n")
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		flushCount++
		mu.Unlock()
	})

	// Double newline should trigger flush
	buffer.Write("First paragraph.\n\n")

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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		flushCount++
		mu.Unlock()
	})

	// Write more than maxBufferSize (4096) bytes with newlines
	// Each line is 100 chars + newline, so 50 lines = 5050 bytes
	for i := 0; i < 50; i++ {
		buffer.Write(strings.Repeat("x", 100) + "\n")
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a complete line without triggering immediate flush conditions
	buffer.Write("partial content\n")

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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	buffer.Write("final content")
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

	buffer := NewMarkdownBuffer(func(html string) {
		flushCount++
	})

	buffer.Write("some content")
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write opening fence - should not flush yet
	buffer.Write("```python\n")
	buffer.Write("print('hello')\n")
	// Write closing fence - should trigger flush
	buffer.Write("```\n")

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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Markdown with code block containing backticks in content
	buffer.Write("```\nsome `inline` code\n```\n")
	buffer.Close()

	mu.Lock()
	html := result.String()
	mu.Unlock()

	if !strings.Contains(html, "inline") {
		t.Errorf("expected code block content to be preserved, got %q", html)
	}
}

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<script>alert('xss')</script>", "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;"},
		{"a & b", "a &amp; b"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{"normal text", "normal text"},
		{"<>&\"'", "&lt;&gt;&amp;&quot;&#39;"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := escapeHTML(tt.input)
			if result != tt.expected {
				t.Errorf("escapeHTML(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMarkdownBuffer_EmptyFlush(t *testing.T) {
	flushCount := 0

	buffer := NewMarkdownBuffer(func(html string) {
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

	buffer := NewMarkdownBuffer(func(html string) {
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
			buffer.Write("chunk ")
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write opening fence
	buffer.Write("```go\n")
	buffer.Write("func main() {\n")

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
	buffer.Write("}\n```\n")

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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a numbered list - should not be split mid-list
	buffer.Write("Here are the changes:\n\n")
	buffer.Write("1. First item\n")
	buffer.Write("2. Second item\n")
	buffer.Write("3. Third item\n")
	buffer.Write("\n") // End of list (double newline)
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write an unordered list
	buffer.Write("Items:\n\n")
	buffer.Write("- Apple\n")
	buffer.Write("- Banana\n")
	buffer.Write("- Cherry\n")
	buffer.Write("\n") // End of list
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

func TestMarkdownBuffer_TableNoSplit(t *testing.T) {
	var result strings.Builder
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a markdown table - should not be split mid-table
	buffer.Write("Here is a table:\n\n")
	buffer.Write("| Category | Example Services |\n")
	buffer.Write("|----------|------------------|\n")
	buffer.Write("| Category A | Service 1, Service 2 |\n")
	buffer.Write("| Category B | Service 3, Service 4 |\n")
	buffer.Write("\n") // End of table (blank line)
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write a larger table that might exceed buffer thresholds
	buffer.Write("| What Happened | Example Services | Description |\n")
	buffer.Write("|---------------|------------------|-------------|\n")
	for i := 0; i < 20; i++ {
		buffer.Write("| Row " + string(rune('A'+i)) + " | Service ABC, Service DEF, Service GHI | Long description text here |\n")
	}
	buffer.Write("\n") // End of table
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Write table rows with pauses that would normally trigger timeout flush
	buffer.Write("| Header 1 | Header 2 |\n")
	time.Sleep(100 * time.Millisecond) // Would trigger flush if not in table
	buffer.Write("|----------|----------|\n")
	time.Sleep(100 * time.Millisecond)
	buffer.Write("| Cell 1 | Cell 2 |\n")
	time.Sleep(100 * time.Millisecond)
	buffer.Write("| Cell 3 | Cell 4 |\n")
	buffer.Write("\n") // End of table

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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write bold text on a single line that could be split mid-bold
	buffer.Write("Here is some **bold text that is important")
	time.Sleep(100 * time.Millisecond) // Would trigger flush if not detecting unmatched **
	buffer.Write("** and more text\n")
	buffer.Write("\n") // End of paragraph
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

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		result.WriteString(html)
		mu.Unlock()
	})

	// Write inline code that might be split
	buffer.Write("Use the `some_function")
	time.Sleep(100 * time.Millisecond) // Would trigger flush if not detecting unmatched `
	buffer.Write("_name` method\n")
	buffer.Write("\n") // End of paragraph
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

func TestHasUnmatchedInlineFormatting(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"no formatting", "hello world", false},
		{"matched bold", "**bold**", false},
		{"unmatched bold", "**bold", true},
		{"matched code", "`code`", false},
		{"unmatched code", "`code", true},
		{"multiple matched", "**bold** and `code`", false},
		{"one unmatched bold", "**bold** and **more", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasUnmatchedInlineFormatting(tt.input)
			if result != tt.expected {
				t.Errorf("hasUnmatchedInlineFormatting(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
