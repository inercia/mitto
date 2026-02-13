package web

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// EventFixture represents a test fixture loaded from a JSONL file.
type EventFixture struct {
	Name   string
	Events []session.Event
}

// EventTestResult holds the results of replaying an event fixture.
type EventTestResult struct {
	Flushes      []FlushResult // All flush outputs
	ToolCalls    []ToolCallResult
	TotalHTML    string
	FlushCount   int
	ToolCount    int
	ReplayTimeMs int64 // Actual time taken to replay
}

// FlushResult represents a single flush from the markdown buffer.
type FlushResult struct {
	Seq       int64
	HTML      string
	Timestamp time.Time
}

// ToolCallResult represents a tool call event.
type ToolCallResult struct {
	Seq        int64
	ToolCallID string
	Title      string
	Status     string
	Timestamp  time.Time
}

// loadEventFixture loads a JSONL fixture file.
func loadEventFixture(t *testing.T, name string) EventFixture {
	t.Helper()
	path := filepath.Join("testdata", "streaming", "events", name+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Failed to open fixture %s: %v", name, err)
	}
	defer f.Close()

	var events []session.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event session.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Failed to parse event in %s: %v\nLine: %s", name, err, line)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Failed to read fixture %s: %v", name, err)
	}

	return EventFixture{Name: name, Events: events}
}

// ReplayConfig configures how events are replayed.
type ReplayConfig struct {
	// SpeedFactor scales the time between events (1.0 = real time, 0.0 = instant)
	SpeedFactor float64
	// MaxDelay caps the maximum delay between events (0 = no cap)
	MaxDelay time.Duration
}

// replayEventFixture replays events through the markdown buffer with timing.
func replayEventFixture(t *testing.T, fixture EventFixture, cfg ReplayConfig) EventTestResult {
	t.Helper()

	var flushes []FlushResult
	var toolCalls []ToolCallResult
	var mu sync.Mutex
	var seqCounter int64

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		seqCounter++
		flushes = append(flushes, FlushResult{
			Seq:       seqCounter,
			HTML:      html,
			Timestamp: time.Now(),
		})
		mu.Unlock()
	})

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
		switch event.Type {
		case session.EventTypeAgentMessage:
			data, err := session.DecodeEventData(event)
			if err != nil {
				t.Logf("Warning: failed to decode agent_message: %v", err)
				continue
			}
			if msgData, ok := data.(session.AgentMessageData); ok {
				buffer.Write(msgData.Text)
			}

		case session.EventTypeToolCall:
			data, err := session.DecodeEventData(event)
			if err != nil {
				t.Logf("Warning: failed to decode tool_call: %v", err)
				continue
			}
			if tcData, ok := data.(session.ToolCallData); ok {
				mu.Lock()
				toolCalls = append(toolCalls, ToolCallResult{
					Seq:        event.Seq,
					ToolCallID: tcData.ToolCallID,
					Title:      tcData.Title,
					Status:     tcData.Status,
					Timestamp:  time.Now(),
				})
				mu.Unlock()
			}

		case session.EventTypeToolCallUpdate:
			// Tool call updates don't affect markdown buffer directly
			// but we could track them if needed
		}
	}

	// Simulate prompt completion - this is what happens in real usage
	// when the agent finishes responding. We use Flush() not Close()
	// because Close() is only called when the session itself ends.
	buffer.Flush()

	mu.Lock()
	defer mu.Unlock()

	// Combine all HTML
	var htmlParts []string
	for _, f := range flushes {
		htmlParts = append(htmlParts, f.HTML)
	}

	return EventTestResult{
		Flushes:      flushes,
		ToolCalls:    toolCalls,
		TotalHTML:    strings.Join(htmlParts, ""),
		FlushCount:   len(flushes),
		ToolCount:    len(toolCalls),
		ReplayTimeMs: time.Since(startTime).Milliseconds(),
	}
}

// =============================================================================
// Event-Based Tests
// =============================================================================

// TestEventReplay_ListSplitApostrophe tests the bug where a list was split
// at an apostrophe due to the inactivity timeout firing mid-list.
func TestEventReplay_ListSplitApostrophe(t *testing.T) {
	fixture := loadEventFixture(t, "list_split_apostrophe")

	// Replay with real timing (this will take ~3 seconds due to the 2.5s pause)
	result := replayEventFixture(t, fixture, ReplayConfig{
		SpeedFactor: 1.0,
		MaxDelay:    3 * time.Second,
	})

	t.Logf("Replay took %dms, %d flushes", result.ReplayTimeMs, result.FlushCount)
	for i, f := range result.Flushes {
		preview := f.HTML
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		t.Logf("Flush %d (seq=%d): %q", i, f.Seq, preview)
	}

	// Key assertion: the list should NOT be split
	// All list items should be in the same flush
	listFlushCount := 0
	for _, f := range result.Flushes {
		if strings.Contains(f.HTML, "<li>") {
			listFlushCount++
		}
	}
	if listFlushCount > 1 {
		t.Errorf("List was split across %d flushes - should be in single flush", listFlushCount)
	}

	// Check that both list items are present
	if !strings.Contains(result.TotalHTML, "There") {
		t.Error("Missing list item 1 content")
	}
	if !strings.Contains(result.TotalHTML, "continuation hasn") {
		t.Error("Missing list item 2 content")
	}

	// Check list item count
	liCount := strings.Count(result.TotalHTML, "<li>")
	if liCount != 2 {
		t.Errorf("Expected 2 <li> tags, got %d", liCount)
	}
}

// TestEventReplay_CodeBlockWithTool tests that tool calls during a code block
// don't cause the code block to be split.
func TestEventReplay_CodeBlockWithTool(t *testing.T) {
	fixture := loadEventFixture(t, "code_block_with_tool")

	result := replayEventFixture(t, fixture, ReplayConfig{
		SpeedFactor: 1.0,
	})

	t.Logf("Replay took %dms, %d flushes, %d tool calls",
		result.ReplayTimeMs, result.FlushCount, result.ToolCount)

	// Code block should not be split
	preCount := strings.Count(result.TotalHTML, "<pre")
	if preCount != 1 {
		t.Errorf("Expected 1 <pre> tag, got %d - code block was split", preCount)
		for i, f := range result.Flushes {
			t.Logf("Flush %d: %s", i, f.HTML[:min(100, len(f.HTML))])
		}
	}

	// Tool call should have been captured
	if result.ToolCount != 1 {
		t.Errorf("Expected 1 tool call, got %d", result.ToolCount)
	}
}

// TestEventReplay_TableSlowRows tests that tables with slow row delivery
// are not split by timeouts.
func TestEventReplay_TableSlowRows(t *testing.T) {
	fixture := loadEventFixture(t, "table_slow_rows")

	result := replayEventFixture(t, fixture, ReplayConfig{
		SpeedFactor: 1.0,
		MaxDelay:    500 * time.Millisecond, // Cap delays for faster test
	})

	t.Logf("Replay took %dms, %d flushes", result.ReplayTimeMs, result.FlushCount)

	// Table should not be split
	tableCount := strings.Count(result.TotalHTML, "<table")
	if tableCount != 1 {
		t.Errorf("Expected 1 <table> tag, got %d - table was split", tableCount)
		for i, f := range result.Flushes {
			t.Logf("Flush %d: %s", i, f.HTML[:min(100, len(f.HTML))])
		}
	}

	// All rows should be present
	trCount := strings.Count(result.TotalHTML, "<tr>")
	if trCount < 4 { // header + 3 data rows
		t.Errorf("Expected at least 4 <tr> tags, got %d", trCount)
	}
}

// TestEventReplay_CodeBlockLongPause tests that code blocks survive the
// inactivity timeout (2.5s pause in the middle).
func TestEventReplay_CodeBlockLongPause(t *testing.T) {
	fixture := loadEventFixture(t, "code_block_long_pause")

	result := replayEventFixture(t, fixture, ReplayConfig{
		SpeedFactor: 1.0,
		MaxDelay:    3 * time.Second,
	})

	t.Logf("Replay took %dms, %d flushes", result.ReplayTimeMs, result.FlushCount)

	// Code block should not be split despite 2.5s pause
	preCount := strings.Count(result.TotalHTML, "<pre")
	if preCount != 1 {
		t.Errorf("Expected 1 <pre> tag, got %d - code block was split by inactivity timeout", preCount)
		for i, f := range result.Flushes {
			t.Logf("Flush %d (seq=%d): %s", i, f.Seq, f.HTML[:min(100, len(f.HTML))])
		}
	}

	// All code content should be present
	if !strings.Contains(result.TotalHTML, "After 2.5s pause") {
		t.Error("Missing code content after pause")
	}
}

// TestEventReplay_NaturalFlush tests that content is flushed naturally when
// structures complete, without needing an explicit Flush() call.
func TestEventReplay_NaturalFlush(t *testing.T) {
	var flushes []FlushResult
	var mu sync.Mutex
	var seqCounter int64

	buffer := NewMarkdownBuffer(func(html string) {
		mu.Lock()
		seqCounter++
		flushes = append(flushes, FlushResult{
			Seq:       seqCounter,
			HTML:      html,
			Timestamp: time.Now(),
		})
		mu.Unlock()
	})

	// Send a complete list that ends with a blank line
	// This should flush naturally without needing Flush()
	chunks := []string{
		"Here's a list:\n",
		"\n",
		"1. First item\n",
		"2. Second item\n",
		"\n", // Blank line ends the list
		"After the list.\n",
		"\n", // Paragraph break
	}

	for _, chunk := range chunks {
		buffer.Write(chunk)
		time.Sleep(50 * time.Millisecond) // Small delay
	}

	// Wait for soft timeout to flush
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	flushCount := len(flushes)
	var totalHTML string
	for _, f := range flushes {
		totalHTML += f.HTML
	}
	mu.Unlock()

	// Content should have been flushed naturally (list ended with blank line)
	if flushCount == 0 {
		t.Error("Expected content to be flushed naturally when list ended")
	}

	// Check that list is present
	if !strings.Contains(totalHTML, "<li>") {
		t.Error("Expected list items in output")
	}

	t.Logf("Natural flush: %d flushes, %d bytes HTML", flushCount, len(totalHTML))
}

// TestEventReplay_InstantReplay tests that fixtures work with instant replay
// (no delays) for fast unit testing.
func TestEventReplay_InstantReplay(t *testing.T) {
	fixtures := []string{
		"list_split_apostrophe",
		"code_block_with_tool",
		"table_slow_rows",
		"code_block_long_pause",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			fixture := loadEventFixture(t, name)

			// Instant replay (no delays)
			result := replayEventFixture(t, fixture, ReplayConfig{
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
