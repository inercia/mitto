package web

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

// ACPEvent represents a simulated ACP event for testing.
// This mirrors the structure of events received from the ACP server.
type ACPEvent struct {
	Type string // "text", "tool_call", "tool_update", "thought", "plan"
	Text string // For text events
	// Tool call fields
	ToolID     string
	ToolTitle  string
	ToolStatus acp.ToolCallStatus
}

// StreamingTestCase defines a test scenario with a sequence of ACP events
// and expected HTML outputs.
type StreamingTestCase struct {
	Name           string
	Events         []ACPEvent
	ExpectedHTML   []string // Expected HTML fragments (in order)
	ExpectedOLTags int      // Expected number of <ol> tags in combined output
	ExpectedULTags int      // Expected number of <ul> tags in combined output
	Description    string   // Human-readable description of what this tests
}

// runStreamingTest executes a streaming test case.
func runStreamingTest(t *testing.T, tc StreamingTestCase) {
	t.Helper()

	var htmlOutputs []string
	var toolCalls []string
	var mu sync.Mutex

	seqCounter := int64(0)
	client := NewWebClient(WebClientConfig{
		SeqProvider: &testSeqProvider{counter: &seqCounter},
		OnAgentMessage: func(seq int64, html string) {
			mu.Lock()
			htmlOutputs = append(htmlOutputs, html)
			mu.Unlock()
		},
		OnToolCall: func(seq int64, id, title, status string) {
			mu.Lock()
			toolCalls = append(toolCalls, id)
			mu.Unlock()
		},
	})
	defer client.Close()

	ctx := context.Background()

	// Process each event
	for _, event := range tc.Events {
		var err error
		switch event.Type {
		case "text":
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
						Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: event.Text}},
					},
				},
			})
		case "tool_call":
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					ToolCall: &acp.SessionUpdateToolCall{
						ToolCallId: acp.ToolCallId(event.ToolID),
						Title:      event.ToolTitle,
						Status:     event.ToolStatus,
					},
				},
			})
		case "thought":
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
						Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: event.Text}},
					},
				},
			})
		case "plan":
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					Plan: &acp.SessionUpdatePlan{},
				},
			})
		}
		if err != nil {
			t.Fatalf("SessionUpdate failed for event %+v: %v", event, err)
		}
	}

	// Flush any remaining content
	client.FlushMarkdown()

	mu.Lock()
	defer mu.Unlock()

	// Combine all HTML outputs
	combinedHTML := strings.Join(htmlOutputs, "")

	// Check expected HTML fragments
	for _, expected := range tc.ExpectedHTML {
		if !strings.Contains(combinedHTML, expected) {
			t.Errorf("expected HTML to contain %q\nGot:\n%s", expected, combinedHTML)
		}
	}

	// Check <ol> tag count
	if tc.ExpectedOLTags > 0 {
		olCount := strings.Count(combinedHTML, "<ol>")
		if olCount != tc.ExpectedOLTags {
			t.Errorf("expected %d <ol> tag(s), got %d\nHTML:\n%s", tc.ExpectedOLTags, olCount, combinedHTML)
		}
	}

	// Check <ul> tag count
	if tc.ExpectedULTags > 0 {
		ulCount := strings.Count(combinedHTML, "<ul>")
		if ulCount != tc.ExpectedULTags {
			t.Errorf("expected %d <ul> tag(s), got %d\nHTML:\n%s", tc.ExpectedULTags, ulCount, combinedHTML)
		}
	}
}

// TestMarkdownBuffer_StreamingScenarios tests various streaming scenarios
// that simulate real ACP event sequences.
func TestMarkdownBuffer_StreamingScenarios(t *testing.T) {
	testCases := []StreamingTestCase{
		// ===========================================
		// BASIC LIST SCENARIOS
		// ===========================================
		{
			Name:        "simple_ordered_list",
			Description: "Basic ordered list without blank lines",
			Events: []ACPEvent{
				{Type: "text", Text: "Here are the steps:\n\n"},
				{Type: "text", Text: "1. First step\n"},
				{Type: "text", Text: "2. Second step\n"},
				{Type: "text", Text: "3. Third step\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Done.\n"},
			},
			ExpectedHTML:   []string{"First step", "Second step", "Third step", "Done"},
			ExpectedOLTags: 1,
		},
		{
			Name:        "ordered_list_with_blank_lines",
			Description: "Ordered list with blank lines between items (the bug we fixed)",
			Events: []ACPEvent{
				{Type: "text", Text: "The codebase now has:\n\n"},
				{Type: "text", Text: "1. **First feature**:\n"},
				{Type: "text", Text: "   - Sub item 1\n"},
				{Type: "text", Text: "   - Sub item 2\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "2. **Second feature**:\n"},
				{Type: "text", Text: "   - Sub item 3\n"},
				{Type: "text", Text: "   - Sub item 4\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Some text after.\n"},
			},
			ExpectedHTML:   []string{"First feature", "Second feature", "Sub item 1", "Sub item 4"},
			ExpectedOLTags: 1,
		},
		{
			Name:        "four_item_ordered_list_with_blank_lines",
			Description: "Four-item ordered list with blank lines (from real conversation)",
			Events: []ACPEvent{
				{Type: "text", Text: "The codebase now has:\n\n"},
				{Type: "text", Text: "1. **Robust sequence number handling**:\n"},
				{Type: "text", Text: "   - Backend assigns seq at receive time\n"},
				{Type: "text", Text: "   - Frontend updates lastSeenSeq immediately\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "2. **Improved reliability**:\n"},
				{Type: "text", Text: "   - Periodic persistence during long responses\n"},
				{Type: "text", Text: "   - Exponential backoff prevents thundering herd\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "3. **Comprehensive documentation**:\n"},
				{Type: "text", Text: "   - Formal sequence number contract\n"},
				{Type: "text", Text: "   - Updated rules files\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "4. **Strong test coverage**:\n"},
				{Type: "text", Text: "   - 33 new JavaScript unit tests\n"},
				{Type: "text", Text: "   - 18 new Go unit tests\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Some text after the list.\n"},
			},
			ExpectedHTML:   []string{"Robust sequence", "Improved reliability", "Comprehensive documentation", "Strong test coverage"},
			ExpectedOLTags: 1,
		},

		// ===========================================
		// TOOL CALL INTERRUPTION SCENARIOS
		// ===========================================
		{
			Name:        "tool_call_interrupts_list",
			Description: "Tool call arrives mid-list, should flush list first",
			Events: []ACPEvent{
				{Type: "text", Text: "Let me check these files:\n\n"},
				{Type: "text", Text: "1. First file\n"},
				{Type: "text", Text: "2. Second file\n"},
				{Type: "tool_call", ToolID: "tool-1", ToolTitle: "Read file", ToolStatus: acp.ToolCallStatusInProgress},
			},
			ExpectedHTML:   []string{"First file", "Second file"},
			ExpectedOLTags: 1,
		},
		{
			Name:        "tool_call_after_list_ends",
			Description: "Tool call after list properly ends",
			Events: []ACPEvent{
				{Type: "text", Text: "Here's what I'll do:\n\n"},
				{Type: "text", Text: "1. Read the file\n"},
				{Type: "text", Text: "2. Analyze it\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Let me start:\n\n"},
				{Type: "tool_call", ToolID: "tool-1", ToolTitle: "Read file", ToolStatus: acp.ToolCallStatusInProgress},
			},
			ExpectedHTML:   []string{"Read the file", "Analyze it", "Let me start"},
			ExpectedOLTags: 1,
		},

		// ===========================================
		// CODE BLOCK SCENARIOS
		// ===========================================
		{
			Name:        "code_block_basic",
			Description: "Basic code block should not be split",
			Events: []ACPEvent{
				{Type: "text", Text: "Here's the code:\n\n"},
				{Type: "text", Text: "```go\n"},
				{Type: "text", Text: "func main() {\n"},
				{Type: "text", Text: "    fmt.Println(\"Hello\")\n"},
				{Type: "text", Text: "}\n"},
				{Type: "text", Text: "```\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "That's it.\n"},
			},
			// Note: HTML entities are used (&#39; for ', &#34; for ")
			ExpectedHTML: []string{"<span>func</span>", "Hello", "<pre>"},
		},
		{
			Name:        "code_block_with_tool_call",
			Description: "Tool call after code block",
			Events: []ACPEvent{
				{Type: "text", Text: "I'll write this code:\n\n"},
				{Type: "text", Text: "```python\n"},
				{Type: "text", Text: "print('hello')\n"},
				{Type: "text", Text: "```\n"},
				{Type: "text", Text: "\n"},
				{Type: "tool_call", ToolID: "tool-1", ToolTitle: "Write file", ToolStatus: acp.ToolCallStatusInProgress},
			},
			ExpectedHTML: []string{"print", "hello"},
		},

		// ===========================================
		// TABLE SCENARIOS
		// ===========================================
		{
			Name:        "table_basic",
			Description: "Basic table should not be split",
			Events: []ACPEvent{
				{Type: "text", Text: "Here's the data:\n\n"},
				{Type: "text", Text: "| Name | Value |\n"},
				{Type: "text", Text: "|------|-------|\n"},
				{Type: "text", Text: "| Foo  | 1     |\n"},
				{Type: "text", Text: "| Bar  | 2     |\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "End of table.\n"},
			},
			ExpectedHTML: []string{"<table>", "Foo", "Bar", "End of table"},
		},
		{
			Name:        "table_with_tool_call",
			Description: "Tool call after table",
			Events: []ACPEvent{
				{Type: "text", Text: "| File | Status |\n"},
				{Type: "text", Text: "|------|--------|\n"},
				{Type: "text", Text: "| a.go | OK     |\n"},
				{Type: "text", Text: "\n"},
				{Type: "tool_call", ToolID: "tool-1", ToolTitle: "Read file", ToolStatus: acp.ToolCallStatusInProgress},
			},
			ExpectedHTML: []string{"<table>", "a.go", "OK"},
		},

		// ===========================================
		// EDGE CASES: INLINE FORMATTING
		// ===========================================
		{
			Name:        "bold_text_across_chunks",
			Description: "Bold text split across chunks should not break",
			Events: []ACPEvent{
				{Type: "text", Text: "This is **bold"},
				{Type: "text", Text: " text** here.\n\n"},
			},
			ExpectedHTML: []string{"<strong>bold text</strong>"},
		},
		{
			Name:        "inline_code_across_chunks",
			Description: "Inline code split across chunks",
			Events: []ACPEvent{
				{Type: "text", Text: "Run `go test"},
				{Type: "text", Text: " ./...` to test.\n\n"},
			},
			ExpectedHTML: []string{"<code>go test ./...</code>"},
		},

		// ===========================================
		// EDGE CASES: NESTED LISTS
		// ===========================================
		{
			Name:        "deeply_nested_list",
			Description: "Three levels of nesting",
			Events: []ACPEvent{
				{Type: "text", Text: "- Level 1\n"},
				{Type: "text", Text: "  - Level 2\n"},
				{Type: "text", Text: "    - Level 3\n"},
				{Type: "text", Text: "  - Back to 2\n"},
				{Type: "text", Text: "- Back to 1\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Done.\n"},
			},
			ExpectedHTML:   []string{"Level 1", "Level 2", "Level 3", "Back to 2", "Back to 1"},
			ExpectedULTags: 3, // One for each nesting level
		},
		{
			Name:        "mixed_list_types",
			Description: "Ordered list with unordered sub-lists",
			Events: []ACPEvent{
				{Type: "text", Text: "1. First\n"},
				{Type: "text", Text: "   - Sub A\n"},
				{Type: "text", Text: "   - Sub B\n"},
				{Type: "text", Text: "2. Second\n"},
				{Type: "text", Text: "   - Sub C\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "End.\n"},
			},
			ExpectedHTML:   []string{"First", "Sub A", "Sub B", "Second", "Sub C"},
			ExpectedOLTags: 1,
			ExpectedULTags: 2,
		},

		// ===========================================
		// EDGE CASES: MULTIPLE BLANK LINES
		// ===========================================
		{
			Name:        "multiple_blank_lines_between_items",
			Description: "Multiple blank lines between list items",
			Events: []ACPEvent{
				{Type: "text", Text: "1. First\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "2. Second\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Done.\n"},
			},
			ExpectedHTML:   []string{"First", "Second"},
			ExpectedOLTags: 1,
		},
		{
			Name:        "blank_line_at_start",
			Description: "Blank line before list starts",
			Events: []ACPEvent{
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "1. First\n"},
				{Type: "text", Text: "2. Second\n"},
				{Type: "text", Text: "\n"},
			},
			ExpectedHTML:   []string{"First", "Second"},
			ExpectedOLTags: 1,
		},

		// ===========================================
		// EDGE CASES: CHARACTER-BY-CHARACTER STREAMING
		// ===========================================
		{
			Name:        "char_by_char_list",
			Description: "List streamed character by character",
			Events: func() []ACPEvent {
				text := "1. A\n2. B\n\nEnd.\n"
				events := make([]ACPEvent, len(text))
				for i, c := range text {
					events[i] = ACPEvent{Type: "text", Text: string(c)}
				}
				return events
			}(),
			ExpectedHTML:   []string{"<li>A</li>", "<li>B</li>"},
			ExpectedOLTags: 1,
		},

		// ===========================================
		// EDGE CASES: EMPTY AND WHITESPACE
		// ===========================================
		{
			Name:        "empty_list_item",
			Description: "List with empty item",
			Events: []ACPEvent{
				{Type: "text", Text: "1. First\n"},
				{Type: "text", Text: "2. \n"},
				{Type: "text", Text: "3. Third\n"},
				{Type: "text", Text: "\n"},
			},
			ExpectedHTML:   []string{"First", "Third"},
			ExpectedOLTags: 1,
		},
		{
			Name:        "whitespace_only_chunks",
			Description: "Chunks with only whitespace",
			Events: []ACPEvent{
				{Type: "text", Text: "Hello\n"},
				{Type: "text", Text: "   "},
				{Type: "text", Text: "   "},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "World\n"},
			},
			ExpectedHTML: []string{"Hello", "World"},
		},

		// ===========================================
		// EDGE CASES: THOUGHT INTERRUPTION
		// ===========================================
		{
			Name:        "thought_interrupts_list",
			Description: "Thought event mid-list should be BUFFERED (not break the list)",
			Events: []ACPEvent{
				{Type: "text", Text: "1. First\n"},
				{Type: "text", Text: "2. Second\n"},
				{Type: "thought", Text: "thinking about next step..."},
				{Type: "text", Text: "3. Third\n"},
				{Type: "text", Text: "\n"},
			},
			ExpectedHTML: []string{"First", "Second", "Third"},
			// Note: With StreamBuffer, thoughts are buffered mid-list, so we get 1 <ol>
			ExpectedOLTags: 1,
		},

		// ===========================================
		// REAL-WORLD SCENARIO: PROGRESS REPORT
		// ===========================================
		{
			Name:        "progress_report_with_table_and_list",
			Description: "Complex progress report with table followed by list",
			Events: []ACPEvent{
				{Type: "text", Text: "## Progress\n\n"},
				{Type: "text", Text: "| Task | Status |\n"},
				{Type: "text", Text: "|------|--------|\n"},
				{Type: "text", Text: "| A    | Done   |\n"},
				{Type: "text", Text: "| B    | WIP    |\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Next steps:\n\n"},
				{Type: "text", Text: "1. Complete B\n"},
				{Type: "text", Text: "2. Start C\n"},
				{Type: "text", Text: "\n"},
				{Type: "text", Text: "Let me continue.\n"},
				{Type: "tool_call", ToolID: "tool-1", ToolTitle: "Edit file", ToolStatus: acp.ToolCallStatusInProgress},
			},
			ExpectedHTML:   []string{"<table>", "Done", "WIP", "Complete B", "Start C"},
			ExpectedOLTags: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runStreamingTest(t, tc)
		})
	}
}

// TestMarkdownBuffer_SequenceOrdering verifies that sequence numbers are
// correctly preserved through the markdown buffer.
func TestMarkdownBuffer_SequenceOrdering(t *testing.T) {
	testCases := []struct {
		name           string
		events         []ACPEvent
		expectedSeqMin int64 // Minimum expected seq in first HTML output
		expectedSeqMax int64 // Maximum expected seq in last HTML output
	}{
		{
			name: "simple_text_preserves_seq",
			events: []ACPEvent{
				{Type: "text", Text: "Hello\n\n"},
			},
			expectedSeqMin: 1,
			expectedSeqMax: 1,
		},
		{
			name: "buffered_list_uses_first_seq",
			events: []ACPEvent{
				{Type: "text", Text: "1. First\n"},  // seq 1
				{Type: "text", Text: "2. Second\n"}, // seq 2
				{Type: "text", Text: "\n"},          // seq 3
			},
			expectedSeqMin: 1, // Should use seq from first chunk
			expectedSeqMax: 1,
		},
		{
			name: "tool_call_gets_higher_seq",
			events: []ACPEvent{
				{Type: "text", Text: "Hello\n"}, // seq 1
				{Type: "tool_call", ToolID: "t1", ToolTitle: "Test", ToolStatus: acp.ToolCallStatusInProgress}, // seq 2
			},
			expectedSeqMin: 1,
			expectedSeqMax: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var seqs []int64
			var mu sync.Mutex

			seqCounter := int64(0)
			client := NewWebClient(WebClientConfig{
				SeqProvider: &testSeqProvider{counter: &seqCounter},
				OnAgentMessage: func(seq int64, html string) {
					mu.Lock()
					seqs = append(seqs, seq)
					mu.Unlock()
				},
				OnToolCall: func(seq int64, id, title, status string) {
					mu.Lock()
					seqs = append(seqs, seq)
					mu.Unlock()
				},
			})
			defer client.Close()

			ctx := context.Background()
			for _, event := range tc.events {
				var err error
				switch event.Type {
				case "text":
					err = client.SessionUpdate(ctx, acp.SessionNotification{
						Update: acp.SessionUpdate{
							AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
								Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: event.Text}},
							},
						},
					})
				case "tool_call":
					err = client.SessionUpdate(ctx, acp.SessionNotification{
						Update: acp.SessionUpdate{
							ToolCall: &acp.SessionUpdateToolCall{
								ToolCallId: acp.ToolCallId(event.ToolID),
								Title:      event.ToolTitle,
								Status:     event.ToolStatus,
							},
						},
					})
				}
				if err != nil {
					t.Fatalf("SessionUpdate failed: %v", err)
				}
			}

			client.FlushMarkdown()

			mu.Lock()
			defer mu.Unlock()

			if len(seqs) == 0 {
				t.Fatal("no events received")
			}

			if seqs[0] < tc.expectedSeqMin {
				t.Errorf("first seq = %d, want >= %d", seqs[0], tc.expectedSeqMin)
			}
			if seqs[len(seqs)-1] > tc.expectedSeqMax {
				t.Errorf("last seq = %d, want <= %d", seqs[len(seqs)-1], tc.expectedSeqMax)
			}

			// Verify seqs are monotonically increasing
			for i := 1; i < len(seqs); i++ {
				if seqs[i] < seqs[i-1] {
					t.Errorf("seq[%d]=%d < seq[%d]=%d, should be monotonically increasing",
						i, seqs[i], i-1, seqs[i-1])
				}
			}
		})
	}
}

// TestMarkdownBuffer_LargeContent tests handling of large content that exceeds buffer limits.
func TestMarkdownBuffer_LargeContent(t *testing.T) {
	t.Run("large_paragraph_flushes", func(t *testing.T) {
		var flushCount int
		var mu sync.Mutex

		buffer := NewMarkdownBuffer(func(html string) {
			mu.Lock()
			flushCount++
			mu.Unlock()
		})

		// Write a very large paragraph (> 4KB)
		largeText := strings.Repeat("x", 5000) + "\n\n"
		buffer.Write(largeText)
		buffer.Close()

		mu.Lock()
		defer mu.Unlock()

		if flushCount == 0 {
			t.Error("expected at least one flush for large content")
		}
	})

	t.Run("large_code_block_flushes_at_limit", func(t *testing.T) {
		var flushCount int
		var mu sync.Mutex

		buffer := NewMarkdownBuffer(func(html string) {
			mu.Lock()
			flushCount++
			mu.Unlock()
		})

		// Start a code block
		buffer.Write("```\n")
		// Write content that exceeds maxCodeBlockBufferSize (64KB)
		for i := 0; i < 70; i++ {
			buffer.Write(strings.Repeat("x", 1000) + "\n")
		}
		buffer.Close()

		mu.Lock()
		defer mu.Unlock()

		// Should have flushed at least once due to size limit
		if flushCount == 0 {
			t.Error("expected at least one flush for large code block")
		}
	})
}

// TestMarkdownBuffer_InactivityTimeout tests that structured content (lists, tables, code blocks)
// is NOT flushed by inactivity timeout to avoid splitting them mid-stream.
// The content will be flushed when Close() is called.
func TestMarkdownBuffer_InactivityTimeout(t *testing.T) {
	t.Run("list_flushed_after_hard_inactivity", func(t *testing.T) {
		// Lists SHOULD be flushed by the hard inactivity timeout (2s).
		// This ensures content is displayed even if the agent stops mid-list.
		// The soft timeout (200ms) still respects list boundaries.
		var outputs []string
		var mu sync.Mutex

		buffer := NewMarkdownBuffer(func(html string) {
			mu.Lock()
			outputs = append(outputs, html)
			mu.Unlock()
		})

		// Start a list but don't end it
		buffer.Write("Summary:\n\n")
		buffer.Write("1. First item\n")
		buffer.Write("2. Second item\n")
		// Agent stops responding here (no blank line to end list)

		// Wait for hard inactivity timeout
		time.Sleep(inactivityFlushTimeout + 500*time.Millisecond)

		mu.Lock()
		combined := strings.Join(outputs, "")
		mu.Unlock()

		// List content SHOULD have been flushed by hard timeout
		if !strings.Contains(combined, "First item") {
			t.Error("expected 'First item' to be flushed after hard inactivity timeout")
		}
		if !strings.Contains(combined, "Second item") {
			t.Error("expected 'Second item' to be flushed after hard inactivity timeout")
		}

		buffer.Close()
	})

	t.Run("code_block_flushed_after_hard_inactivity", func(t *testing.T) {
		// Code blocks SHOULD be flushed by the hard inactivity timeout.
		// This ensures content is displayed even if the agent stops mid-block.
		var outputs []string
		var mu sync.Mutex

		buffer := NewMarkdownBuffer(func(html string) {
			mu.Lock()
			outputs = append(outputs, html)
			mu.Unlock()
		})

		// Start a code block but don't close it
		buffer.Write("```python\n")
		buffer.Write("def hello():\n")
		buffer.Write("    print('world')\n")
		// Agent stops responding here (no closing ```)

		// Wait for hard inactivity timeout
		time.Sleep(inactivityFlushTimeout + 500*time.Millisecond)

		mu.Lock()
		combined := strings.Join(outputs, "")
		mu.Unlock()

		// Content SHOULD have been flushed by hard timeout
		if !strings.Contains(combined, "hello") {
			t.Error("expected 'hello' to be flushed after hard inactivity timeout")
		}

		buffer.Close()
	})

	t.Run("table_flushed_after_hard_inactivity", func(t *testing.T) {
		// Tables SHOULD be flushed by the hard inactivity timeout.
		// This ensures content is displayed even if the agent stops mid-table.
		var outputs []string
		var mu sync.Mutex

		buffer := NewMarkdownBuffer(func(html string) {
			mu.Lock()
			outputs = append(outputs, html)
			mu.Unlock()
		})

		// Start a table but don't end it
		buffer.Write("| A | B |\n")
		buffer.Write("|---|---|\n")
		buffer.Write("| 1 | 2 |\n")
		// Agent stops responding here (no blank line to end table)

		// Wait for hard inactivity timeout
		time.Sleep(inactivityFlushTimeout + 500*time.Millisecond)

		mu.Lock()
		combined := strings.Join(outputs, "")
		mu.Unlock()

		// Content SHOULD have been flushed by hard timeout
		if !strings.Contains(combined, "<table>") {
			t.Error("expected table to be flushed after hard inactivity timeout")
		}

		buffer.Close()
	})

	t.Run("normal_content_uses_soft_timeout", func(t *testing.T) {
		var outputs []string
		var mu sync.Mutex

		buffer := NewMarkdownBuffer(func(html string) {
			mu.Lock()
			outputs = append(outputs, html)
			mu.Unlock()
		})

		// Write normal content (not in a block)
		buffer.Write("Hello world\n\n")

		// Wait for soft timeout (200ms + buffer)
		time.Sleep(400 * time.Millisecond)

		mu.Lock()
		combined := strings.Join(outputs, "")
		mu.Unlock()

		// Content should have been flushed by soft timeout
		if !strings.Contains(combined, "Hello world") {
			t.Error("expected 'Hello world' to be flushed by soft timeout")
		}

		buffer.Close()
	})
}

// TestMarkdownBuffer_EdgeCases tests additional edge cases.
func TestMarkdownBuffer_EdgeCases(t *testing.T) {
	t.Run("empty_input", func(t *testing.T) {
		var flushCount int
		buffer := NewMarkdownBuffer(func(html string) {
			flushCount++
		})

		buffer.Write("")
		buffer.Close()

		if flushCount != 0 {
			t.Errorf("expected 0 flushes for empty input, got %d", flushCount)
		}
	})

	t.Run("only_newlines", func(t *testing.T) {
		var outputs []string
		buffer := NewMarkdownBuffer(func(html string) {
			outputs = append(outputs, html)
		})

		buffer.Write("\n\n\n\n")
		buffer.Close()

		// Should produce empty or minimal output
		combined := strings.Join(outputs, "")
		if strings.Contains(combined, "<p>") && !strings.Contains(combined, "</p>") {
			t.Error("unclosed paragraph tag")
		}
	})

	t.Run("unclosed_code_block", func(t *testing.T) {
		var outputs []string
		buffer := NewMarkdownBuffer(func(html string) {
			outputs = append(outputs, html)
		})

		buffer.Write("```\ncode without closing\n")
		buffer.Close()

		combined := strings.Join(outputs, "")
		if !strings.Contains(combined, "code without closing") {
			t.Error("unclosed code block content should still be output")
		}
	})

	t.Run("list_at_end_of_input", func(t *testing.T) {
		var outputs []string
		buffer := NewMarkdownBuffer(func(html string) {
			outputs = append(outputs, html)
		})

		// List without trailing blank line
		buffer.Write("1. First\n")
		buffer.Write("2. Second\n")
		buffer.Close()

		combined := strings.Join(outputs, "")
		if !strings.Contains(combined, "<ol>") {
			t.Error("expected <ol> tag")
		}
		if !strings.Contains(combined, "First") || !strings.Contains(combined, "Second") {
			t.Error("expected list items in output")
		}
	})

	t.Run("table_at_end_of_input", func(t *testing.T) {
		var outputs []string
		buffer := NewMarkdownBuffer(func(html string) {
			outputs = append(outputs, html)
		})

		// Table without trailing blank line
		buffer.Write("| A | B |\n")
		buffer.Write("|---|---|\n")
		buffer.Write("| 1 | 2 |\n")
		buffer.Close()

		combined := strings.Join(outputs, "")
		if !strings.Contains(combined, "<table>") {
			t.Error("expected <table> tag")
		}
	})

	t.Run("mixed_list_markers", func(t *testing.T) {
		var outputs []string
		buffer := NewMarkdownBuffer(func(html string) {
			outputs = append(outputs, html)
		})

		// Different unordered list markers
		buffer.Write("- Dash\n")
		buffer.Write("* Star\n")
		buffer.Write("+ Plus\n")
		buffer.Write("\n")
		buffer.Close()

		combined := strings.Join(outputs, "")
		// All should be treated as list items
		if !strings.Contains(combined, "Dash") {
			t.Error("expected 'Dash' in output")
		}
		if !strings.Contains(combined, "Star") {
			t.Error("expected 'Star' in output")
		}
		if !strings.Contains(combined, "Plus") {
			t.Error("expected 'Plus' in output")
		}
	})
}
