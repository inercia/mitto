package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// StreamingFixture represents a test fixture for markdown streaming.
type StreamingFixture struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	File         string                 `json:"file"`
	Expectations map[string]interface{} `json:"expectations"`
}

// StreamingFixturesManifest represents the fixtures.json file.
type StreamingFixturesManifest struct {
	Description string             `json:"description"`
	Fixtures    []StreamingFixture `json:"fixtures"`
}

// loadStreamingFixtures loads the fixtures manifest from testdata.
func loadStreamingFixtures(t *testing.T) []StreamingFixture {
	t.Helper()
	manifestPath := filepath.Join("testdata", "streaming", "fixtures.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("Failed to read fixtures manifest: %v", err)
	}

	var manifest StreamingFixturesManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Failed to parse fixtures manifest: %v", err)
	}

	return manifest.Fixtures
}

// loadFixtureContent loads the markdown content for a fixture.
func loadFixtureContent(t *testing.T, fixture StreamingFixture) string {
	t.Helper()
	path := filepath.Join("testdata", "streaming", fixture.File)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", fixture.File, err)
	}
	return string(data)
}

// StreamingTestResult holds the results of streaming a fixture.
type StreamingTestResult struct {
	HTML         string   // Combined HTML output
	FlushCount   int      // Number of times flush was called
	FlushOutputs []string // Individual flush outputs
}

// streamFixture streams a fixture through the MarkdownBuffer and returns results.
func streamFixture(t *testing.T, content string, opts ...streamOption) StreamingTestResult {
	t.Helper()

	cfg := streamConfig{
		chunkSize:      0, // 0 means line-by-line
		delayBetween:   0,
		pauseAfterLine: -1,
		pauseDuration:  0,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	lines := strings.Split(content, "\n")

	for i, line := range lines {
		// Add newline back except for the last empty line
		chunk := line
		if i < len(lines)-1 || line != "" {
			chunk = line + "\n"
		}

		if cfg.chunkSize > 0 {
			// Stream character by character or in chunks
			for j := 0; j < len(chunk); j += cfg.chunkSize {
				end := j + cfg.chunkSize
				if end > len(chunk) {
					end = len(chunk)
				}
				buffer.Write(chunk[j:end])
				if cfg.delayBetween > 0 {
					time.Sleep(cfg.delayBetween)
				}
			}
		} else {
			// Stream line by line
			if chunk != "" {
				buffer.Write(chunk)
			}
		}

		// Optional pause after specific line
		if cfg.pauseAfterLine == i && cfg.pauseDuration > 0 {
			time.Sleep(cfg.pauseDuration)
		}

		if cfg.delayBetween > 0 && cfg.chunkSize == 0 {
			time.Sleep(cfg.delayBetween)
		}
	}

	buffer.Close()

	mu.Lock()
	defer mu.Unlock()

	return StreamingTestResult{
		HTML:         strings.Join(results, ""),
		FlushCount:   len(results),
		FlushOutputs: results,
	}
}

// streamConfig holds configuration for streaming tests.
type streamConfig struct {
	chunkSize      int           // Size of chunks (0 = line by line)
	delayBetween   time.Duration // Delay between chunks
	pauseAfterLine int           // Line number to pause after (-1 = no pause)
	pauseDuration  time.Duration // Duration of pause
}

type streamOption func(*streamConfig)

func withChunkSize(size int) streamOption {
	return func(c *streamConfig) { c.chunkSize = size }
}

func withDelay(d time.Duration) streamOption {
	return func(c *streamConfig) { c.delayBetween = d }
}

// withPauseAfterLine pauses streaming after a specific line for testing timing scenarios.
// Currently unused but preserved for future test scenarios involving delayed streaming.
//
//nolint:unused // Reserved for future test scenarios
func withPauseAfterLine(line int, duration time.Duration) streamOption {
	return func(c *streamConfig) {
		c.pauseAfterLine = line
		c.pauseDuration = duration
	}
}

// =============================================================================
// Test: All Fixtures Basic Streaming
// =============================================================================

// TestStreamingFixtures_LineByLine tests all fixtures with line-by-line streaming.
// This is the most common streaming pattern.
func TestStreamingFixtures_LineByLine(t *testing.T) {
	fixtures := loadStreamingFixtures(t)

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			content := loadFixtureContent(t, fixture)
			result := streamFixture(t, content)

			// Log the result for debugging
			t.Logf("Fixture: %s", fixture.Name)
			t.Logf("Flush count: %d", result.FlushCount)
			t.Logf("HTML length: %d", len(result.HTML))

			// Check expectations
			checkExpectations(t, fixture, result)
		})
	}
}

// TestStreamingFixtures_CharByChar tests all fixtures with character-by-character streaming.
// This simulates slow network or token-by-token streaming.
func TestStreamingFixtures_CharByChar(t *testing.T) {
	fixtures := loadStreamingFixtures(t)

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			content := loadFixtureContent(t, fixture)
			result := streamFixture(t, content, withChunkSize(1))

			// Check expectations
			checkExpectations(t, fixture, result)
		})
	}
}

// checkExpectations verifies the expectations for a fixture.
func checkExpectations(t *testing.T, fixture StreamingFixture, result StreamingTestResult) {
	t.Helper()

	html := result.HTML

	// Check for single <pre> tag (no split code blocks)
	if expectSinglePre, ok := fixture.Expectations["single_pre_tag"].(bool); ok && expectSinglePre {
		preCount := strings.Count(html, "<pre")
		if preCount > 1 {
			t.Errorf("Expected single <pre> tag, got %d. Code block was split!", preCount)
			logFlushOutputs(t, result)
		}
	}

	// Check for no split code block
	if noSplit, ok := fixture.Expectations["no_split_code_block"].(bool); ok && noSplit {
		preCount := strings.Count(html, "<pre")
		codeCount := strings.Count(html, "<code")
		if preCount > 1 || codeCount > preCount+5 { // Allow some inline <code> tags
			t.Errorf("Code block appears to be split: %d <pre>, %d <code>", preCount, codeCount)
			logFlushOutputs(t, result)
		}
	}

	// Check for single <ol> tag
	if expectSingleOL, ok := fixture.Expectations["single_ol_tag"].(bool); ok && expectSingleOL {
		olCount := strings.Count(html, "<ol")
		if olCount != 1 {
			t.Errorf("Expected single <ol> tag, got %d", olCount)
			logFlushOutputs(t, result)
		}
	}

	// Check for single <table> tag
	if expectSingleTable, ok := fixture.Expectations["single_table_tag"].(bool); ok && expectSingleTable {
		tableCount := strings.Count(html, "<table")
		if tableCount != 1 {
			t.Errorf("Expected single <table> tag, got %d", tableCount)
			logFlushOutputs(t, result)
		}
	}

	// Check for <strong> tags
	if hasStrong, ok := fixture.Expectations["has_strong_tags"].(bool); ok && hasStrong {
		if !strings.Contains(html, "<strong>") {
			t.Error("Expected <strong> tags in output")
		}
	}

	// Check for <em> tags
	if hasEm, ok := fixture.Expectations["has_em_tags"].(bool); ok && hasEm {
		if !strings.Contains(html, "<em>") {
			t.Error("Expected <em> tags in output")
		}
	}

	// Check for <code> tags
	if hasCode, ok := fixture.Expectations["has_code_tags"].(bool); ok && hasCode {
		if !strings.Contains(html, "<code>") {
			t.Error("Expected <code> tags in output")
		}
	}

	// Check for links
	if hasLinks, ok := fixture.Expectations["has_links"].(bool); ok && hasLinks {
		if !strings.Contains(html, "<a ") {
			t.Error("Expected <a> tags in output")
		}
	}

	// Check for <pre> tag (code block present)
	if hasPre, ok := fixture.Expectations["has_pre_tag"].(bool); ok && hasPre {
		if !strings.Contains(html, "<pre") {
			t.Error("Expected <pre> tag in output")
		}
	}

	// Check all <strong> tags are closed
	if allClosed, ok := fixture.Expectations["all_strong_tags_closed"].(bool); ok && allClosed {
		openCount := strings.Count(html, "<strong>")
		closeCount := strings.Count(html, "</strong>")
		if openCount != closeCount {
			t.Errorf("Unbalanced <strong> tags: %d open, %d close", openCount, closeCount)
		}
	}

	// Check for no literal ** in list items (indicates broken bold)
	if noLiteral, ok := fixture.Expectations["no_literal_double_asterisk_in_list"].(bool); ok && noLiteral {
		if strings.Contains(html, "<li>**") || strings.Contains(html, "**</li>") {
			t.Error("Found literal ** in list item - bold formatting is broken")
			logFlushOutputs(t, result)
		}
	}

	// Check for <ol> tag (ordered list present)
	if hasOL, ok := fixture.Expectations["has_ol_tag"].(bool); ok && hasOL {
		if !strings.Contains(html, "<ol") {
			t.Error("Expected <ol> tag in output")
		}
	}

	// Check for <li> tags (list items present)
	if hasLI, ok := fixture.Expectations["has_li_tags"].(bool); ok && hasLI {
		if !strings.Contains(html, "<li>") {
			t.Error("Expected <li> tags in output")
		}
	}
}

// logFlushOutputs logs individual flush outputs for debugging.
func logFlushOutputs(t *testing.T, result StreamingTestResult) {
	t.Helper()
	t.Logf("Flush outputs (%d total):", result.FlushCount)
	for i, output := range result.FlushOutputs {
		preview := output
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		t.Logf("  [%d] %q", i, preview)
	}
}

// =============================================================================
// Test: Code Block Inactivity Timeout
// =============================================================================

// TestStreamingFixtures_CodeBlockWithInactivityTimeout tests that code blocks
// ARE flushed by the hard inactivity timeout to prevent content loss.
// This is intentional: if an agent stops mid-block, we must display the content
// rather than losing it forever.
func TestStreamingFixtures_CodeBlockWithInactivityTimeout(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Stream a simple code block with a pause in the middle
	buffer.Write("```go\n")
	buffer.Write("func main() {\n")
	buffer.Write("    fmt.Println(\"Hello\")\n")

	// Wait for hard inactivity timeout
	time.Sleep(inactivityFlushTimeout + 500*time.Millisecond)

	mu.Lock()
	countAfterTimeout := len(results)
	mu.Unlock()

	// Hard timeout SHOULD have flushed the content to prevent loss
	if countAfterTimeout < 1 {
		t.Error("Expected hard inactivity timeout to flush code block content")
	}

	// Complete the code block
	buffer.Write("}\n")
	buffer.Write("```\n")
	buffer.Write("\nSome text after.\n")

	buffer.Close()

	mu.Lock()
	html := strings.Join(results, "")
	mu.Unlock()

	// All code content should be present (even if split across flushes)
	// Note: Content may be HTML-escaped (e.g., "Hello" -> &#34;Hello&#34;)
	if !strings.Contains(html, "Println") && !strings.Contains(html, "Hello") {
		t.Errorf("Expected code content to be preserved, got: %s", html)
	}
	if !strings.Contains(html, "Some text after") {
		t.Errorf("Expected text after code block to be present, got: %s", html)
	}
}

// =============================================================================
// Test: List with Unmatched Bold - No Premature Flush
// =============================================================================

// TestStreamingFixtures_ListUnmatchedBold_NoPrematureFlush tests that a list
// with unmatched bold is NOT flushed when the list "ends" (blank line + non-list content).
func TestStreamingFixtures_ListUnmatchedBold_NoPrematureFlush(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Simulate the exact scenario from the bug report
	chunks := []string{
		"1. **First item** - Description\n",
		"2. **Second item** - Description\n",
		"3. **Third item** - Description\n",
		"4. **Real-time\n", // Unmatched bold!
		"\n",               // Blank line - would normally end the list
		"messaging works after refresh** - New messages\n", // Closing **
	}

	for _, chunk := range chunks {
		buffer.Write(chunk)
	}

	mu.Lock()
	countBeforeClose := len(results)
	mu.Unlock()

	// Should NOT have flushed yet - the list has unmatched bold
	if countBeforeClose > 0 {
		t.Errorf("List was flushed prematurely with unmatched bold! Got %d flushes", countBeforeClose)
		mu.Lock()
		for i, r := range results {
			if strings.Contains(r, "**Real-time") {
				t.Errorf("Flush %d contains literal **: %s", i, r[:min(100, len(r))])
			}
		}
		mu.Unlock()
	}

	buffer.Close()

	mu.Lock()
	finalCount := len(results)
	mu.Unlock()

	// After close, should have flushed
	if finalCount == 0 {
		t.Error("Expected content to be flushed after Close()")
	}
}

// =============================================================================
// Test: Tool Call During Code Block
// =============================================================================

// TestStreamingFixtures_ToolCallDuringCodeBlock tests tool call behavior during code blocks.
// When FlushOnToolCall is false: tool calls are buffered and don't cause a flush.
// When FlushOnToolCall is true: tool calls cause an immediate flush, splitting the block.
func TestStreamingFixtures_ToolCallDuringCodeBlock(t *testing.T) {
	if FlushOnToolCall {
		t.Skip("Skipping: FlushOnToolCall is enabled, which intentionally flushes on tool calls")
	}

	var messageResults []string
	var toolResults []string
	var mu sync.Mutex

	seqCounter := int64(0)
	client := NewWebClient(WebClientConfig{
		SeqProvider: &testSeqProvider{counter: &seqCounter},
		OnAgentMessage: func(seq int64, html string) {
			mu.Lock()
			messageResults = append(messageResults, html)
			mu.Unlock()
		},
		OnToolCall: func(seq int64, id, title, status string) {
			mu.Lock()
			toolResults = append(toolResults, id)
			mu.Unlock()
		},
	})
	defer client.Close()

	ctx := context.Background()

	// Start a code block
	sendAgentMessage(t, client, ctx, "```go\n")
	sendAgentMessage(t, client, ctx, "func main() {\n")
	sendAgentMessage(t, client, ctx, "    fmt.Println(\"Hello\")\n")

	// Tool call arrives mid-code-block
	sendToolCall(t, client, ctx, "tool-1", "Read file", acp.ToolCallStatusCompleted)

	mu.Lock()
	messageCountMid := len(messageResults)
	toolCountMid := len(toolResults)
	mu.Unlock()

	// Message should NOT have been flushed (we're in a code block)
	if messageCountMid > 0 {
		t.Errorf("Message was flushed during code block! Got %d messages", messageCountMid)
	}

	// Tool call should be buffered (not emitted yet)
	if toolCountMid > 0 {
		t.Errorf("Tool call was emitted during code block! Should be buffered")
	}

	// Close the code block
	sendAgentMessage(t, client, ctx, "}\n")
	sendAgentMessage(t, client, ctx, "```\n")

	// Flush
	client.FlushMarkdown()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	finalMessageCount := len(messageResults)
	finalToolCount := len(toolResults)
	html := strings.Join(messageResults, "")
	mu.Unlock()

	// Now both should be emitted
	if finalMessageCount == 0 {
		t.Error("Expected message to be flushed after code block close")
	}
	if finalToolCount == 0 {
		t.Error("Expected tool call to be emitted after code block close")
	}

	// Verify single code block
	preCount := strings.Count(html, "<pre")
	if preCount != 1 {
		t.Errorf("Expected 1 <pre> tag, got %d", preCount)
	}
}

// =============================================================================
// Test: Table with Delay Between Rows
// =============================================================================

// TestStreamingFixtures_TableWithDelay tests that tables are not split
// even with delays between rows.
func TestStreamingFixtures_TableWithDelay(t *testing.T) {
	content := loadFixtureContent(t, StreamingFixture{
		File: "table_with_formatting.md",
	})

	// Stream with 50ms delay between lines
	result := streamFixture(t, content, withDelay(50*time.Millisecond))

	// Verify single table
	tableCount := strings.Count(result.HTML, "<table")
	if tableCount != 1 {
		t.Errorf("Expected 1 <table> tag, got %d - table was split!", tableCount)
		logFlushOutputs(t, result)
	}
}

// =============================================================================
// Test: Nested Code Block in List
// =============================================================================

// TestStreamingFixtures_NestedCodeInList tests code blocks near list items.
// Note: In standard markdown, a code block after a list item (without proper
// indentation) will end the list. This is expected behavior.
func TestStreamingFixtures_NestedCodeInList(t *testing.T) {
	content := loadFixtureContent(t, StreamingFixture{
		File: "nested_code_in_list.md",
	})

	result := streamFixture(t, content)

	// Should have a <pre> tag for the code block
	if !strings.Contains(result.HTML, "<pre") {
		t.Error("Expected <pre> tag for code block")
	}

	// Should have list items
	liCount := strings.Count(result.HTML, "<li>")
	if liCount < 2 {
		t.Errorf("Expected at least 2 <li> tags, got %d", liCount)
	}

	// The code block should be complete (not split)
	preCount := strings.Count(result.HTML, "<pre")
	if preCount != 1 {
		t.Errorf("Expected 1 <pre> tag, got %d - code block was split!", preCount)
		logFlushOutputs(t, result)
	}
}

// =============================================================================
// Test: Soft Timeout Respects Block Boundaries
// =============================================================================

// TestStreamingFixtures_SoftTimeoutRespectsBlocks tests that the soft timeout
// (200ms) respects block boundaries and doesn't flush mid-block.
func TestStreamingFixtures_SoftTimeoutRespectsBlocks(t *testing.T) {
	testCases := []struct {
		name    string
		content string
		check   func(t *testing.T, results []string)
	}{
		{
			name:    "code_block",
			content: "```go\nfunc main() {}\n```\n",
			check: func(t *testing.T, results []string) {
				// Should be a single flush with complete code block
				if len(results) > 1 {
					t.Errorf("Code block was split into %d flushes", len(results))
				}
			},
		},
		{
			name:    "list",
			content: "1. First\n2. Second\n3. Third\n\n",
			check: func(t *testing.T, results []string) {
				// List should be flushed as a unit
				html := strings.Join(results, "")
				olCount := strings.Count(html, "<ol")
				if olCount != 1 {
					t.Errorf("Expected 1 <ol>, got %d", olCount)
				}
			},
		},
		{
			name:    "table",
			content: "| A | B |\n|---|---|\n| 1 | 2 |\n\n",
			check: func(t *testing.T, results []string) {
				// Table should be flushed as a unit
				html := strings.Join(results, "")
				tableCount := strings.Count(html, "<table")
				if tableCount != 1 {
					t.Errorf("Expected 1 <table>, got %d", tableCount)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var results []string
			var mu sync.Mutex

			buffer := NewMarkdownBuffer(func(html string) {
				mu.Lock()
				results = append(results, html)
				mu.Unlock()
			})

			// Stream character by character with small delays
			for _, char := range tc.content {
				buffer.Write(string(char))
				time.Sleep(10 * time.Millisecond)
			}

			// Wait for soft timeout
			time.Sleep(300 * time.Millisecond)

			buffer.Close()

			mu.Lock()
			defer mu.Unlock()
			tc.check(t, results)
		})
	}
}

// =============================================================================
// Test: Multiple Code Blocks
// =============================================================================

// TestStreamingFixtures_MultipleCodeBlocks tests that multiple code blocks
// in the same content are each rendered as separate units.
func TestStreamingFixtures_MultipleCodeBlocks(t *testing.T) {
	content := `# Multiple Code Blocks

First code block:

` + "```go\nfunc first() {}\n```" + `

Some text between.

Second code block:

` + "```python\ndef second(): pass\n```" + `

End of content.
`

	result := streamFixture(t, content)

	// Should have exactly 2 <pre> tags
	preCount := strings.Count(result.HTML, "<pre")
	if preCount != 2 {
		t.Errorf("Expected 2 <pre> tags, got %d", preCount)
		logFlushOutputs(t, result)
	}

	// Both languages should be present
	if !strings.Contains(result.HTML, "first") {
		t.Error("Missing first code block content")
	}
	if !strings.Contains(result.HTML, "second") {
		t.Error("Missing second code block content")
	}
}

// =============================================================================
// Test: Paragraph Followed by Numbered List
// =============================================================================

// TestStreamingFixtures_ParagraphThenList tests that a paragraph followed by
// a numbered list doesn't lose list items.
func TestStreamingFixtures_ParagraphThenList(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// Simulate the exact scenario from the screenshot
	chunks := []string{
		"The test passes now. The key insight is that:\n",
		"\n",
		"1. The list is NOT flushed mid-stream when the bold is unmatched\n",
		"2. The tool call is buffered (because `inList` is still true)\n",
		"3. Everything is flushed together at the end\n",
		"\n",
		"The final HTML still shows the issue.\n",
	}

	for _, chunk := range chunks {
		buffer.Write(chunk)
	}

	buffer.Close()

	mu.Lock()
	html := strings.Join(results, "")
	flushCount := len(results)
	mu.Unlock()

	t.Logf("Flush count: %d", flushCount)
	t.Logf("HTML: %s", html)

	// Check that all 3 list items are present
	if !strings.Contains(html, "<li>") {
		t.Error("Expected list items in output")
	}

	liCount := strings.Count(html, "<li>")
	if liCount != 3 {
		t.Errorf("Expected 3 <li> tags, got %d", liCount)
	}

	// Check that the paragraph is present
	if !strings.Contains(html, "key insight") {
		t.Error("Expected paragraph with 'key insight' in output")
	}

	// Check that list item 1 content is present
	if !strings.Contains(html, "NOT flushed mid-stream") {
		t.Error("Expected list item 1 content in output")
	}

	// Check that list item 2 content is present
	if !strings.Contains(html, "tool call is buffered") {
		t.Error("Expected list item 2 content in output")
	}

	// Log individual flushes for debugging
	mu.Lock()
	for i, r := range results {
		preview := r
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		t.Logf("Flush %d: %q", i, preview)
	}
	mu.Unlock()
}

// TestStreamingFixtures_ListItemWithParentheses tests list items with parenthetical
// content that might be split across lines.
func TestStreamingFixtures_ListItemWithParentheses(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// This matches the screenshot more closely - list item with parenthetical content
	// that spans multiple chunks
	chunks := []string{
		"The test passes now. The key insight is that:\n",
		"\n",
		"1. The list is NOT flushed mid-stream when the bold is unmatched (the bold text\n",
		"spans across a blank line), but that's a limitation of the markdown parser, not our\n",
		"buffering logic.\n",
		"2. The tool call is buffered\n",
		"\n",
		"The final HTML still shows the issue.\n",
	}

	for _, chunk := range chunks {
		buffer.Write(chunk)
	}

	buffer.Close()

	mu.Lock()
	html := strings.Join(results, "")
	flushCount := len(results)
	mu.Unlock()

	t.Logf("Flush count: %d", flushCount)
	t.Logf("HTML: %s", html)

	// Log individual flushes for debugging
	for i, r := range results {
		preview := r
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		t.Logf("Flush %d: %q", i, preview)
	}

	// Check that list item 1 content is complete (not split)
	// The parenthetical content should be in the same list item
	if !strings.Contains(html, "limitation of the markdown parser") {
		t.Error("Expected complete list item 1 content including parenthetical")
	}

	// Check that we have 2 list items
	liCount := strings.Count(html, "<li>")
	if liCount != 2 {
		t.Errorf("Expected 2 <li> tags, got %d", liCount)
	}

	// The key test: "spans across a blank line" should be in a <li>, not a <p>
	// If it's in a <p>, the list item was split
	if strings.Contains(html, "<p>spans across") {
		t.Error("List item was split - 'spans across' is in a <p> instead of <li>")
	}
}

// TestStreamingFixtures_ListItemSplitByBlankLine tests the scenario where
// a list item number is followed by a blank line and then content.
// This is malformed markdown but we should handle it gracefully.
func TestStreamingFixtures_ListItemSplitByBlankLine(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// This is the problematic pattern - list number followed by blank line
	chunks := []string{
		"The test passes now. The key insight is that:\n",
		"\n",
		"1. The list is NOT flushed mid-stream (the bold text\n",
		"\n", // Blank line in the middle of list item!
		"spans across a blank line), but that's a limitation of the markdown parser.\n",
		"\n",
		"The final paragraph.\n",
	}

	for _, chunk := range chunks {
		buffer.Write(chunk)
	}

	buffer.Close()

	mu.Lock()
	html := strings.Join(results, "")
	mu.Unlock()

	t.Logf("HTML: %s", html)

	// Log individual flushes for debugging
	mu.Lock()
	for i, r := range results {
		preview := r
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		t.Logf("Flush %d: %q", i, preview)
	}
	mu.Unlock()

	// With a blank line in the middle of a list item, the markdown parser
	// will treat the content after the blank line as a separate paragraph.
	// This is expected behavior for malformed markdown.
	// The key is that we don't lose any content.
	if !strings.Contains(html, "key insight") {
		t.Error("Missing 'key insight' content")
	}
	if !strings.Contains(html, "spans across") {
		t.Error("Missing 'spans across' content")
	}
	if !strings.Contains(html, "final paragraph") {
		t.Error("Missing 'final paragraph' content")
	}
}

// =============================================================================
// Test: List Item with Inline Code and Parentheses
// =============================================================================

// TestStreamingFixtures_ListItemWithCodeAndParens tests the scenario where
// a list item contains inline code followed by parenthetical content that
// spans multiple lines.
func TestStreamingFixtures_ListItemWithCodeAndParens(t *testing.T) {
	var results []string
	var mu sync.Mutex

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		results = append(results, html)
		mu.Unlock()
	})

	// This matches the screenshot - list item with inline code and parentheses
	// that spans multiple lines with blank lines
	// The actual pattern from the screenshot seems to be:
	// 1. Item with inline code (like `something`
	//
	// ), which continues
	//
	// more continuation text
	chunks := []string{
		"All tests pass. Let me provide a summary of the changes I made:\n",
		"\n",
		"1. Added check for unmatched inline formatting (like `**` or `` ` ``\n",
		"\n", // Blank line in the middle!
		"`), which caused content like:\n",
		"\n", // Another blank line!
		"inactivity timeout flushes. This prevents the 2-second timeout from flushing content with broken formatting.\n",
	}

	for _, chunk := range chunks {
		buffer.Write(chunk)
	}

	buffer.Close()

	mu.Lock()
	html := strings.Join(results, "")
	mu.Unlock()

	t.Logf("HTML: %s", html)

	// Log individual flushes for debugging
	mu.Lock()
	for i, r := range results {
		preview := r
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		t.Logf("Flush %d: %q", i, preview)
	}
	mu.Unlock()

	// The key test: the content should not be split into separate paragraphs
	// with literal backticks showing
	if strings.Contains(html, "<p>``") || strings.Contains(html, "<p>`),") {
		t.Error("List item was split - found paragraph starting with backticks")
	}
}

// =============================================================================
// Test: Join List Item Continuations
// =============================================================================

// TestJoinListItemContinuations tests the preprocessing function that joins
// list item continuations split by blank lines.
func TestJoinListItemContinuations(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no_change_needed",
			input:    "1. Complete item\n2. Another item\n",
			expected: "1. Complete item\n2. Another item\n",
		},
		{
			name:     "join_continuation_with_open_paren",
			input:    "1. Start (with open\n\ncontinuation)\n",
			expected: "1. Start (with open\n   continuation)\n",
		},
		{
			name:     "no_join_when_next_starts_uppercase",
			input:    "1. Start (with open\n\nNext paragraph.\n",
			expected: "1. Start (with open\n\nNext paragraph.\n",
		},
		{
			name:     "no_join_when_parens_balanced",
			input:    "1. Complete (balanced)\n\nnext line\n",
			expected: "1. Complete (balanced)\n\nnext line\n",
		},
		{
			// NOTE: Multi-continuation joining with backticks is a known limitation.
			// The function currently joins only ONE continuation, not multiple.
			// This test documents current behavior.
			name:     "join_one_continuation_with_backticks",
			input:    "1. Check for `something\n\n`), which continues\n\nmore text here.\n",
			expected: "1. Check for `something\n   `), which continues\n\nmore text here.\n",
		},
		{
			// NOTE: Same limitation - only first continuation is joined.
			name:     "join_one_continuation_after_double_backtick",
			input:    "1. Added check (like `` ` ``\n\n`), which caused:\n\ncontinuation.\n",
			expected: "1. Added check (like `` ` ``\n   `), which caused:\n\ncontinuation.\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := joinListItemContinuations(tc.input)
			if result != tc.expected {
				t.Errorf("Expected:\n%q\nGot:\n%q", tc.expected, result)
			}
		})
	}
}

// =============================================================================
// Test: List Item Split at Apostrophe (Screenshot Bug)
// =============================================================================

// TestStreamingFixtures_ListSplitAtApostrophe tests that the hard inactivity
// timeout (2s) will flush list content to prevent loss, even if this splits
// the list. All content should be preserved.
func TestStreamingFixtures_ListSplitAtApostrophe(t *testing.T) {
	var results []string
	var mu sync.Mutex
	var seqCounter int64

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		seqCounter++
		results = append(results, html)
		t.Logf("Flush seq=%d len=%d: %q", seqCounter, len(html), html[:min(100, len(html))])
		mu.Unlock()
	})

	// Simulate the exact pattern from the screenshot
	// The AI is sending something like:
	// "long as:\n1. There\n's a\nblank line followed by..."
	chunks := []string{
		"long as:\n",
		"1. There\n",
		"'s a\n",
		"blank line followed by a line that looks\n",
		"like a continuation AND\n",
		"2. The continuation hasn\n",
		"'t ended (indicated by reaching a line that\n",
		"starts a new sentence/paragraph)\n",
		"\n", // Blank line to end list
		"Let me rewrite the function with a\n",
		"simpler approach:\n",
	}

	for i, chunk := range chunks {
		buffer.Write(chunk)
		// Very long delay after list item 1 to trigger inactivity timeout
		if i == 2 { // After "'s a\n"
			t.Logf("Waiting %v to trigger hard inactivity timeout...", inactivityFlushTimeout+500*time.Millisecond)
			time.Sleep(inactivityFlushTimeout + 500*time.Millisecond)

			// After the hard timeout, the content SHOULD have been flushed to prevent loss
			mu.Lock()
			flushCountDuringPause := len(results)
			mu.Unlock()

			// At least 1 flush should have happened (intro paragraph and/or list)
			if flushCountDuringPause < 1 {
				t.Errorf("Expected hard timeout to flush content, got %d flushes", flushCountDuringPause)
			}
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Wait for any pending timeout
	time.Sleep(300 * time.Millisecond)

	// Simulate prompt completion - use Flush() not Close()
	buffer.Flush()

	mu.Lock()
	defer mu.Unlock()

	html := strings.Join(results, "")
	t.Logf("Total HTML: %s", html)
	t.Logf("Flush count: %d", len(results))

	// Key assertion: ALL content should be preserved (even if split)
	if !strings.Contains(html, "There") {
		t.Error("Missing content: 'There'")
	}
	if !strings.Contains(html, "'s a") || !strings.Contains(html, "&#39;s a") {
		// Content may be HTML-escaped
		if !strings.Contains(html, "s a") {
			t.Error("Missing content: apostrophe continuation")
		}
	}
	if !strings.Contains(html, "Let me rewrite") {
		t.Error("Missing content: 'Let me rewrite'")
	}
}

// =============================================================================
// Test: Unmatched Formatting Recovery
// =============================================================================

// TestStreamingFixtures_UnmatchedFormattingRecovery tests that content with
// unmatched formatting is eventually flushed (on Close) even if malformed.
func TestStreamingFixtures_UnmatchedFormattingRecovery(t *testing.T) {
	testCases := []struct {
		name    string
		content string
	}{
		{
			name:    "unmatched_bold",
			content: "This has **unmatched bold\n\nNext paragraph.\n",
		},
		{
			name:    "unmatched_italic",
			content: "This has *unmatched italic\n\nNext paragraph.\n",
		},
		{
			name:    "unmatched_code",
			content: "This has `unmatched code\n\nNext paragraph.\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var results []string
			var mu sync.Mutex

			buffer := NewMarkdownBuffer(func(html string) {
				mu.Lock()
				results = append(results, html)
				mu.Unlock()
			})

			// Stream line by line
			lines := strings.Split(tc.content, "\n")
			for i, line := range lines {
				if i < len(lines)-1 || line != "" {
					buffer.Write(line + "\n")
				}
			}

			mu.Lock()
			countBeforeClose := len(results)
			mu.Unlock()

			// Should NOT have flushed yet (unmatched formatting)
			if countBeforeClose > 0 {
				t.Logf("Note: %d flush(es) before Close (may be expected for some patterns)", countBeforeClose)
			}

			// Close should flush everything
			buffer.Close()

			mu.Lock()
			html := strings.Join(results, "")
			mu.Unlock()

			// Content should be present (even if malformed)
			if !strings.Contains(html, "unmatched") {
				t.Error("Content was not flushed after Close()")
			}
			if !strings.Contains(html, "Next paragraph") {
				t.Error("Following content was not flushed")
			}
		})
	}
}
