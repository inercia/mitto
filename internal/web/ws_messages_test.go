package web

import (
	"testing"
)

func TestParseMessage_Valid(t *testing.T) {
	data := []byte(`{"type":"prompt","data":{"message":"hello"}}`)

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	if msg.Type != "prompt" {
		t.Errorf("Type = %q, want %q", msg.Type, "prompt")
	}
	if msg.Data == nil {
		t.Error("Data should not be nil")
	}
}

func TestParseMessage_Invalid(t *testing.T) {
	data := []byte(`{invalid json}`)

	_, err := ParseMessage(data)
	if err == nil {
		t.Error("ParseMessage should fail for invalid JSON")
	}
}

func TestParseMessage_EmptyType(t *testing.T) {
	data := []byte(`{"type":""}`)

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}

	if msg.Type != "" {
		t.Errorf("Type = %q, want empty", msg.Type)
	}
}

// =============================================================================
// EventBuffer Tests
// =============================================================================

func TestEventBuffer_NewEventBuffer(t *testing.T) {
	buf := NewEventBuffer()
	if buf == nil {
		t.Fatal("NewEventBuffer returned nil")
	}
	if buf.Len() != 0 {
		t.Errorf("Len = %d, want 0", buf.Len())
	}
	if !buf.IsEmpty() {
		t.Error("IsEmpty should return true for new buffer")
	}
}

func TestEventBuffer_AppendAgentMessage(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentMessage("Hello, ")
	buf.AppendAgentMessage("World!")

	// Consecutive agent messages should be concatenated
	if buf.Len() != 1 {
		t.Errorf("Len = %d, want 1 (messages should be concatenated)", buf.Len())
	}

	result := buf.GetAgentMessage()
	if result != "Hello, World!" {
		t.Errorf("GetAgentMessage = %q, want %q", result, "Hello, World!")
	}
}

func TestEventBuffer_AppendAgentThought(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentThought("Thinking... ")
	buf.AppendAgentThought("Done!")

	// Consecutive thoughts should be concatenated
	if buf.Len() != 1 {
		t.Errorf("Len = %d, want 1 (thoughts should be concatenated)", buf.Len())
	}

	result := buf.GetAgentThought()
	if result != "Thinking... Done!" {
		t.Errorf("GetAgentThought = %q, want %q", result, "Thinking... Done!")
	}
}

func TestEventBuffer_InterleavedEvents(t *testing.T) {
	buf := NewEventBuffer()

	// Simulate interleaved streaming: message, tool, message, tool, message
	buf.AppendAgentMessage("Let me help... ")
	buf.AppendToolCall("tool-1", "Read file", "running")
	buf.AppendAgentMessage("I found... ")
	buf.AppendToolCall("tool-2", "Edit file", "running")
	buf.AppendAgentMessage("Done!")

	// Should have 5 separate events (not concatenated because interleaved)
	if buf.Len() != 5 {
		t.Errorf("Len = %d, want 5", buf.Len())
	}

	events := buf.Events()

	// Verify order
	if events[0].Type != BufferedEventAgentMessage {
		t.Errorf("events[0].Type = %v, want AgentMessage", events[0].Type)
	}
	if events[1].Type != BufferedEventToolCall {
		t.Errorf("events[1].Type = %v, want ToolCall", events[1].Type)
	}
	if events[2].Type != BufferedEventAgentMessage {
		t.Errorf("events[2].Type = %v, want AgentMessage", events[2].Type)
	}
	if events[3].Type != BufferedEventToolCall {
		t.Errorf("events[3].Type = %v, want ToolCall", events[3].Type)
	}
	if events[4].Type != BufferedEventAgentMessage {
		t.Errorf("events[4].Type = %v, want AgentMessage", events[4].Type)
	}

	// Verify tool call data
	if tc, ok := events[1].Data.(*ToolCallData); ok {
		if tc.ID != "tool-1" {
			t.Errorf("ToolCall ID = %q, want %q", tc.ID, "tool-1")
		}
	} else {
		t.Error("events[1].Data is not ToolCallData")
	}
}

func TestEventBuffer_Flush(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentMessage("Hello")
	buf.AppendToolCall("tool-1", "Test", "done")

	events := buf.Flush()
	if len(events) != 2 {
		t.Errorf("Flush returned %d events, want 2", len(events))
	}

	// Buffer should be empty after flush
	if !buf.IsEmpty() {
		t.Error("Buffer should be empty after Flush")
	}
	if buf.Len() != 0 {
		t.Errorf("Len after Flush = %d, want 0", buf.Len())
	}
}

func TestEventBuffer_Events_ReturnsCopy(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentMessage("Hello")

	events1 := buf.Events()
	events2 := buf.Events()

	// Modifying one should not affect the other
	if len(events1) != len(events2) {
		t.Error("Events should return consistent results")
	}

	// Buffer should still have the event
	if buf.Len() != 1 {
		t.Errorf("Len = %d, want 1 (Events should not modify buffer)", buf.Len())
	}
}

func TestEventBuffer_GetAgentMessage_Interleaved(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentMessage("Part 1. ")
	buf.AppendToolCall("tool-1", "Test", "done")
	buf.AppendAgentMessage("Part 2. ")
	buf.AppendAgentThought("Thinking...")
	buf.AppendAgentMessage("Part 3.")

	// GetAgentMessage should concatenate all agent messages
	result := buf.GetAgentMessage()
	if result != "Part 1. Part 2. Part 3." {
		t.Errorf("GetAgentMessage = %q, want %q", result, "Part 1. Part 2. Part 3.")
	}
}

func TestEventBuffer_GetAgentThought_Interleaved(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentThought("Thought 1. ")
	buf.AppendAgentMessage("Message")
	buf.AppendAgentThought("Thought 2.")

	// GetAgentThought should concatenate all thoughts
	result := buf.GetAgentThought()
	if result != "Thought 1. Thought 2." {
		t.Errorf("GetAgentThought = %q, want %q", result, "Thought 1. Thought 2.")
	}
}

func TestEventBuffer_ToolCallUpdate(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendToolCall("tool-1", "Read file", "running")
	status := "completed"
	buf.AppendToolCallUpdate("tool-1", &status)

	if buf.Len() != 2 {
		t.Errorf("Len = %d, want 2", buf.Len())
	}

	events := buf.Events()
	if events[1].Type != BufferedEventToolCallUpdate {
		t.Errorf("events[1].Type = %v, want ToolCallUpdate", events[1].Type)
	}
}

func TestEventBuffer_AllEventTypes(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentThought("Thinking...")
	buf.AppendAgentMessage("Hello")
	buf.AppendToolCall("tool-1", "Read", "running")
	status := "done"
	buf.AppendToolCallUpdate("tool-1", &status)
	buf.AppendPlan()
	buf.AppendFileRead("/path/to/file", 100)
	buf.AppendFileWrite("/path/to/output", 200)

	if buf.Len() != 7 {
		t.Errorf("Len = %d, want 7", buf.Len())
	}

	events := buf.Events()
	expectedTypes := []BufferedEventType{
		BufferedEventAgentThought,
		BufferedEventAgentMessage,
		BufferedEventToolCall,
		BufferedEventToolCallUpdate,
		BufferedEventPlan,
		BufferedEventFileRead,
		BufferedEventFileWrite,
	}

	for i, expected := range expectedTypes {
		if events[i].Type != expected {
			t.Errorf("events[%d].Type = %v, want %v", i, events[i].Type, expected)
		}
	}
}

func TestEventBuffer_Append(t *testing.T) {
	buf := NewEventBuffer()

	// Test direct Append method
	event := BufferedEvent{
		Type: BufferedEventToolCall,
		Data: &ToolCallData{
			ID:    "tool-1",
			Title: "test_tool",
		},
	}

	buf.Append(event)

	if buf.Len() != 1 {
		t.Errorf("Len = %d, want 1", buf.Len())
	}

	events := buf.Events()
	if events[0].Type != BufferedEventToolCall {
		t.Errorf("Type = %v, want %v", events[0].Type, BufferedEventToolCall)
	}
}
