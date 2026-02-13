package web

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

// ACPReplayEvent represents an ACP event with timing information.
// This mirrors the structure of events received from the ACP server,
// with timestamps for realistic replay.
type ACPReplayEvent struct {
	// Timestamp when this event was received (for timing simulation)
	Timestamp time.Time `json:"timestamp"`

	// Event type: "agent_message", "tool_call", "tool_update", "thought", "plan"
	Type string `json:"type"`

	// Data contains the event-specific payload
	Data json.RawMessage `json:"data"`
}

// ACPReplayFixture contains a sequence of ACP events to replay.
type ACPReplayFixture struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Events      []ACPReplayEvent `json:"events"`
}

// ACPReplayResult holds the results of replaying an ACP fixture.
type ACPReplayResult struct {
	HTMLOutputs  []HTMLOutput
	ToolCalls    []ToolCallOutput
	Thoughts     []ThoughtOutput
	TotalHTML    string
	FlushCount   int
	ReplayTimeMs int64
}

// HTMLOutput represents a single HTML flush from the WebClient.
type HTMLOutput struct {
	Seq       int64
	HTML      string
	Timestamp time.Time
}

// ToolCallOutput represents a tool call event.
type ToolCallOutput struct {
	Seq       int64
	ID        string
	Title     string
	Status    string
	Timestamp time.Time
}

// ThoughtOutput represents a thought event.
type ThoughtOutput struct {
	Seq       int64
	Text      string
	Timestamp time.Time
}

// ACPReplayConfig configures how events are replayed.
type ACPReplayConfig struct {
	// SpeedFactor scales the time between events (1.0 = real time, 0.0 = instant)
	SpeedFactor float64
	// MaxDelay caps the maximum delay between events (0 = no cap)
	MaxDelay time.Duration
}

// loadACPFixture loads a JSONL fixture file containing ACP events.
func loadACPFixture(t *testing.T, name string) ACPReplayFixture {
	t.Helper()
	path := filepath.Join("testdata", "acp", name+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Failed to open fixture %s: %v", name, err)
	}
	defer f.Close()

	fixture := ACPReplayFixture{Name: name}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var event ACPReplayEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Failed to parse event in %s: %v\nLine: %s", name, err, line)
		}
		fixture.Events = append(fixture.Events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Failed to read fixture %s: %v", name, err)
	}

	return fixture
}

// replayACPFixture replays ACP events through the WebClient with timing.
func replayACPFixture(t *testing.T, fixture ACPReplayFixture, cfg ACPReplayConfig) ACPReplayResult {
	t.Helper()

	var htmlOutputs []HTMLOutput
	var toolCalls []ToolCallOutput
	var thoughts []ThoughtOutput
	var mu sync.Mutex

	seqCounter := int64(0)
	client := NewWebClient(WebClientConfig{
		SeqProvider: &testSeqProvider{counter: &seqCounter},
		OnAgentMessage: func(seq int64, html string) {
			mu.Lock()
			htmlOutputs = append(htmlOutputs, HTMLOutput{
				Seq:       seq,
				HTML:      html,
				Timestamp: time.Now(),
			})
			mu.Unlock()
		},
		OnToolCall: func(seq int64, id, title, status string) {
			mu.Lock()
			toolCalls = append(toolCalls, ToolCallOutput{
				Seq:       seq,
				ID:        id,
				Title:     title,
				Status:    status,
				Timestamp: time.Now(),
			})
			mu.Unlock()
		},
		OnAgentThought: func(seq int64, text string) {
			mu.Lock()
			thoughts = append(thoughts, ThoughtOutput{
				Seq:       seq,
				Text:      text,
				Timestamp: time.Now(),
			})
			mu.Unlock()
		},
	})
	defer client.Close()

	ctx := context.Background()
	startTime := time.Now()
	var lastEventTime time.Time

	for i, event := range fixture.Events {
		// Calculate delay from previous event
		if i > 0 && !lastEventTime.IsZero() && cfg.SpeedFactor > 0 {
			delay := event.Timestamp.Sub(lastEventTime)
			scaledDelay := time.Duration(float64(delay) * cfg.SpeedFactor)
			if cfg.MaxDelay > 0 && scaledDelay > cfg.MaxDelay {
				scaledDelay = cfg.MaxDelay
			}
			if scaledDelay > 0 {
				time.Sleep(scaledDelay)
			}
		}
		lastEventTime = event.Timestamp

		// Process event based on type
		var err error
		switch event.Type {
		case "agent_message":
			var data struct {
				HTML string `json:"html"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Logf("Warning: failed to decode agent_message data: %v", err)
				continue
			}
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
						Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: data.HTML}},
					},
				},
			})

		case "tool_call":
			var data struct {
				ToolCallID string `json:"tool_call_id"`
				Title      string `json:"title"`
				Status     string `json:"status"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Logf("Warning: failed to decode tool_call data: %v", err)
				continue
			}
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					ToolCall: &acp.SessionUpdateToolCall{
						ToolCallId: acp.ToolCallId(data.ToolCallID),
						Title:      data.Title,
						Status:     acp.ToolCallStatus(data.Status),
					},
				},
			})

		case "tool_update":
			var data struct {
				ToolCallID string  `json:"tool_call_id"`
				Status     *string `json:"status,omitempty"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Logf("Warning: failed to decode tool_update data: %v", err)
				continue
			}
			var status *acp.ToolCallStatus
			if data.Status != nil {
				s := acp.ToolCallStatus(*data.Status)
				status = &s
			}
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					ToolCallUpdate: &acp.SessionToolCallUpdate{
						ToolCallId: acp.ToolCallId(data.ToolCallID),
						Status:     status,
					},
				},
			})

		case "thought":
			var data struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Logf("Warning: failed to decode thought data: %v", err)
				continue
			}
			err = client.SessionUpdate(ctx, acp.SessionNotification{
				Update: acp.SessionUpdate{
					AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
						Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: data.Text}},
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
			t.Logf("Warning: SessionUpdate failed for event %d (%s): %v", i, event.Type, err)
		}
	}

	// Simulate prompt completion - flush remaining content
	client.FlushMarkdown()

	mu.Lock()
	defer mu.Unlock()

	// Combine all HTML
	var htmlParts []string
	for _, h := range htmlOutputs {
		htmlParts = append(htmlParts, h.HTML)
	}

	return ACPReplayResult{
		HTMLOutputs:  htmlOutputs,
		ToolCalls:    toolCalls,
		Thoughts:     thoughts,
		TotalHTML:    strings.Join(htmlParts, ""),
		FlushCount:   len(htmlOutputs),
		ReplayTimeMs: time.Since(startTime).Milliseconds(),
	}
}

// =============================================================================
// ACP Replay Tests
// =============================================================================

// TestACPReplay_ListWithPause tests that a list is not split when there's a
// long pause (>2s) in the middle of streaming.
func TestACPReplay_ListWithPause(t *testing.T) {
	fixture := loadACPFixture(t, "list_with_pause")

	// Replay with real timing
	result := replayACPFixture(t, fixture, ACPReplayConfig{
		SpeedFactor: 1.0,
		MaxDelay:    3 * time.Second,
	})

	t.Logf("Replay took %dms, %d flushes", result.ReplayTimeMs, result.FlushCount)
	for i, h := range result.HTMLOutputs {
		preview := h.HTML
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		t.Logf("Flush %d (seq=%d): %q", i, h.Seq, preview)
	}

	// Key assertion: the list should NOT be split
	listFlushCount := 0
	for _, h := range result.HTMLOutputs {
		if strings.Contains(h.HTML, "<li>") {
			listFlushCount++
		}
	}
	if listFlushCount > 1 {
		t.Errorf("List was split across %d flushes - should be in single flush", listFlushCount)
	}

	// Check list item count
	liCount := strings.Count(result.TotalHTML, "<li>")
	if liCount != 2 {
		t.Errorf("Expected 2 <li> tags, got %d", liCount)
	}
}

// TestACPReplay_CodeBlockWithToolCall tests that a code block is not split
// when a tool call arrives in the middle.
func TestACPReplay_CodeBlockWithToolCall(t *testing.T) {
	fixture := loadACPFixture(t, "code_block_with_tool")

	result := replayACPFixture(t, fixture, ACPReplayConfig{
		SpeedFactor: 1.0,
	})

	t.Logf("Replay took %dms, %d flushes, %d tool calls",
		result.ReplayTimeMs, result.FlushCount, len(result.ToolCalls))

	// Code block should not be split
	preCount := strings.Count(result.TotalHTML, "<pre")
	if preCount != 1 {
		t.Errorf("Expected 1 <pre> tag, got %d - code block was split", preCount)
		for i, h := range result.HTMLOutputs {
			t.Logf("Flush %d: %s", i, h.HTML[:min(100, len(h.HTML))])
		}
	}

	// Tool call should have been captured
	if len(result.ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(result.ToolCalls))
	}
}

// TestACPReplay_TableWithSlowRows tests that a table is not split when rows
// arrive slowly.
func TestACPReplay_TableWithSlowRows(t *testing.T) {
	fixture := loadACPFixture(t, "table_slow_rows")

	result := replayACPFixture(t, fixture, ACPReplayConfig{
		SpeedFactor: 1.0,
		MaxDelay:    500 * time.Millisecond, // Cap delays for faster test
	})

	t.Logf("Replay took %dms, %d flushes", result.ReplayTimeMs, result.FlushCount)

	// Table should not be split
	tableCount := strings.Count(result.TotalHTML, "<table")
	if tableCount != 1 {
		t.Errorf("Expected 1 <table> tag, got %d - table was split", tableCount)
	}

	// All rows should be present
	trCount := strings.Count(result.TotalHTML, "<tr>")
	if trCount < 4 { // header + 3 data rows
		t.Errorf("Expected at least 4 <tr> tags, got %d", trCount)
	}
}

// TestACPReplay_ComplexResponse tests a complex response with multiple
// tool calls, code blocks, lists, and tables.
func TestACPReplay_ComplexResponse(t *testing.T) {
	fixture := loadACPFixture(t, "complex_response")

	result := replayACPFixture(t, fixture, ACPReplayConfig{
		SpeedFactor: 1.0,
	})

	t.Logf("Replay took %dms, %d flushes, %d tool calls",
		result.ReplayTimeMs, result.FlushCount, len(result.ToolCalls))

	// Should have 2 tool calls
	if len(result.ToolCalls) != 2 {
		t.Errorf("Expected 2 tool calls, got %d", len(result.ToolCalls))
	}

	// Should have exactly 1 code block (not split)
	preCount := strings.Count(result.TotalHTML, "<pre")
	if preCount != 1 {
		t.Errorf("Expected 1 <pre> tag, got %d - code block was split", preCount)
	}

	// Should have exactly 1 ordered list (not split)
	olCount := strings.Count(result.TotalHTML, "<ol>")
	if olCount != 1 {
		t.Errorf("Expected 1 <ol> tag, got %d - list was split", olCount)
	}

	// Should have exactly 1 table (not split)
	tableCount := strings.Count(result.TotalHTML, "<table")
	if tableCount != 1 {
		t.Errorf("Expected 1 <table> tag, got %d - table was split", tableCount)
	}

	// Should have 3 list items
	liCount := strings.Count(result.TotalHTML, "<li>")
	if liCount != 3 {
		t.Errorf("Expected 3 <li> tags, got %d", liCount)
	}

	// Log the HTML for debugging
	t.Logf("Total HTML (%d bytes):\n%s", len(result.TotalHTML), result.TotalHTML)
}

// TestACPReplay_InstantReplay tests that fixtures work with instant replay
// (no delays) for fast unit testing.
func TestACPReplay_InstantReplay(t *testing.T) {
	fixtures := []string{
		"list_with_pause",
		"code_block_with_tool",
		"table_slow_rows",
		"complex_response",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			fixture := loadACPFixture(t, name)

			// Instant replay (no delays)
			result := replayACPFixture(t, fixture, ACPReplayConfig{
				SpeedFactor: 0, // Instant
			})

			// Basic sanity checks
			if result.TotalHTML == "" {
				t.Error("No HTML output")
			}
			if result.FlushCount == 0 {
				t.Error("No flushes occurred")
			}

			t.Logf("Instant replay: %d events -> %d flushes, %d bytes HTML",
				len(fixture.Events), result.FlushCount, len(result.TotalHTML))
		})
	}
}
