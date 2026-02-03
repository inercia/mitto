package session

import (
	"testing"
	"time"
)

func TestDecodeEventData(t *testing.T) {
	event := Event{
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"message": "Hello"},
	}

	decoded, err := DecodeEventData(event)
	if err != nil {
		t.Fatalf("DecodeEventData failed: %v", err)
	}

	data, ok := decoded.(UserPromptData)
	if !ok {
		t.Fatalf("Expected UserPromptData, got %T", decoded)
	}
	if data.Message != "Hello" {
		t.Errorf("Message = %q, want %q", data.Message, "Hello")
	}
}

func TestBuildConversationHistory(t *testing.T) {
	// Create test events
	events := []Event{
		{Type: EventTypeSessionStart, Data: SessionStartData{SessionID: "test"}},
		{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Hello, how are you?"}},
		{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "I'm doing well, thank you!"}},
		{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Can you help me with code?"}},
		{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Of course! What do you need help with?"}},
		{Type: EventTypeToolCall, Data: ToolCallData{Title: "Read file", Status: "completed"}},
		{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Fix the bug"}},
		{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "I've fixed the bug."}},
	}

	// Test with all turns
	history := BuildConversationHistory(events, 10)
	if history == "" {
		t.Error("BuildConversationHistory returned empty string")
	}

	// Should contain the header
	if !contains(history, "[CONVERSATION HISTORY") {
		t.Error("History should contain header")
	}

	// Should contain user messages
	if !contains(history, "Hello, how are you?") {
		t.Error("History should contain first user message")
	}
	if !contains(history, "Can you help me with code?") {
		t.Error("History should contain second user message")
	}

	// Should contain agent messages
	if !contains(history, "I'm doing well") {
		t.Error("History should contain first agent message")
	}

	// Should contain footer
	if !contains(history, "[END OF HISTORY") {
		t.Error("History should contain footer")
	}

	// Test with limited turns
	limitedHistory := BuildConversationHistory(events, 2)
	// Should only have last 2 turns
	if contains(limitedHistory, "Hello, how are you?") {
		t.Error("Limited history should not contain first turn")
	}
	if !contains(limitedHistory, "Fix the bug") {
		t.Error("Limited history should contain last turn")
	}

	// Test with empty events
	emptyHistory := BuildConversationHistory([]Event{}, 5)
	if emptyHistory != "" {
		t.Error("Empty events should return empty history")
	}
}

func TestGetLastAgentMessage(t *testing.T) {
	tests := []struct {
		name     string
		events   []Event
		expected string
	}{
		{
			name:     "empty events",
			events:   []Event{},
			expected: "",
		},
		{
			name: "no user prompt",
			events: []Event{
				{Type: EventTypeSessionStart, Data: SessionStartData{SessionID: "test"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Orphan message"}},
			},
			expected: "",
		},
		{
			name: "single turn",
			events: []Event{
				{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Hello"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Hi there!"}},
			},
			expected: "Hi there!",
		},
		{
			name: "multiple turns returns last",
			events: []Event{
				{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "First question"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "First answer"}},
				{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Second question"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Second answer"}},
			},
			expected: "Second answer",
		},
		{
			name: "multiple agent messages after last user prompt",
			events: []Event{
				{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Do something"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Part 1. "}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Part 2. "}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Part 3."}},
			},
			expected: "Part 1. Part 2. Part 3.",
		},
		{
			name: "user prompt with no agent response yet",
			events: []Event{
				{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Old question"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Old answer"}},
				{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "New question"}},
			},
			expected: "",
		},
		{
			name: "ignores tool calls between messages",
			events: []Event{
				{Type: EventTypeUserPrompt, Data: UserPromptData{Message: "Fix the bug"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "I'll fix it. "}},
				{Type: EventTypeToolCall, Data: ToolCallData{Title: "read file"}},
				{Type: EventTypeAgentMessage, Data: AgentMessageData{Text: "Done!"}},
			},
			expected: "I'll fix it. Done!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetLastAgentMessage(tt.events)
			if result != tt.expected {
				t.Errorf("GetLastAgentMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
