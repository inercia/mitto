package web

import (
	"testing"
	"time"
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

func TestAgentMessageBuffer_Write(t *testing.T) {
	buf := &agentMessageBuffer{}

	buf.Write("Hello, ")
	buf.Write("World!")

	result := buf.Flush()
	if result != "Hello, World!" {
		t.Errorf("Flush = %q, want %q", result, "Hello, World!")
	}
}

func TestAgentMessageBuffer_Flush_Empty(t *testing.T) {
	buf := &agentMessageBuffer{}

	result := buf.Flush()
	if result != "" {
		t.Errorf("Flush = %q, want empty", result)
	}
}

func TestAgentMessageBuffer_Flush_ResetsBuffer(t *testing.T) {
	buf := &agentMessageBuffer{}

	buf.Write("First")
	buf.Flush()

	buf.Write("Second")
	result := buf.Flush()

	if result != "Second" {
		t.Errorf("Flush = %q, want %q", result, "Second")
	}
}

func TestAgentMessageBuffer_Flush_UpdatesLastFlush(t *testing.T) {
	buf := &agentMessageBuffer{}

	before := time.Now()
	buf.Flush()
	after := time.Now()

	if buf.lastFlush.Before(before) || buf.lastFlush.After(after) {
		t.Errorf("lastFlush = %v, should be between %v and %v", buf.lastFlush, before, after)
	}
}

func TestAgentMessageBuffer_MultipleWrites(t *testing.T) {
	buf := &agentMessageBuffer{}

	// Simulate streaming chunks
	chunks := []string{"The ", "quick ", "brown ", "fox ", "jumps."}
	for _, chunk := range chunks {
		buf.Write(chunk)
	}

	result := buf.Flush()
	expected := "The quick brown fox jumps."
	if result != expected {
		t.Errorf("Flush = %q, want %q", result, expected)
	}
}
