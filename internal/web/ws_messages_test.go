package web

import (
	"context"
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/session"
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

	// First chunk creates new event with seq=1
	seq1, isNew1 := buf.AppendAgentMessage(1, "Hello, ")
	if !isNew1 || seq1 != 1 {
		t.Errorf("First append: seq=%d, isNew=%v, want seq=1, isNew=true", seq1, isNew1)
	}

	// Second chunk appends to existing event, returns same seq
	seq2, isNew2 := buf.AppendAgentMessage(2, "World!")
	if isNew2 || seq2 != 1 {
		t.Errorf("Second append: seq=%d, isNew=%v, want seq=1, isNew=false", seq2, isNew2)
	}

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

	// First chunk creates new event with seq=1
	seq1, isNew1 := buf.AppendAgentThought(1, "Thinking... ")
	if !isNew1 || seq1 != 1 {
		t.Errorf("First append: seq=%d, isNew=%v, want seq=1, isNew=true", seq1, isNew1)
	}

	// Second chunk appends to existing event, returns same seq
	seq2, isNew2 := buf.AppendAgentThought(2, "Done!")
	if isNew2 || seq2 != 1 {
		t.Errorf("Second append: seq=%d, isNew=%v, want seq=1, isNew=false", seq2, isNew2)
	}

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
	// Each event gets a unique seq
	buf.AppendAgentMessage(1, "Let me help... ")
	buf.AppendToolCall(2, "tool-1", "Read file", "running")
	buf.AppendAgentMessage(3, "I found... ")
	buf.AppendToolCall(4, "tool-2", "Edit file", "running")
	buf.AppendAgentMessage(5, "Done!")

	// Should have 5 separate events (not concatenated because interleaved)
	if buf.Len() != 5 {
		t.Errorf("Len = %d, want 5", buf.Len())
	}

	events := buf.Events()

	// Verify order and seq
	if events[0].Type != BufferedEventAgentMessage || events[0].Seq != 1 {
		t.Errorf("events[0].Type = %v, Seq = %d, want AgentMessage, 1", events[0].Type, events[0].Seq)
	}
	if events[1].Type != BufferedEventToolCall || events[1].Seq != 2 {
		t.Errorf("events[1].Type = %v, Seq = %d, want ToolCall, 2", events[1].Type, events[1].Seq)
	}
	if events[2].Type != BufferedEventAgentMessage || events[2].Seq != 3 {
		t.Errorf("events[2].Type = %v, Seq = %d, want AgentMessage, 3", events[2].Type, events[2].Seq)
	}
	if events[3].Type != BufferedEventToolCall || events[3].Seq != 4 {
		t.Errorf("events[3].Type = %v, Seq = %d, want ToolCall, 4", events[3].Type, events[3].Seq)
	}
	if events[4].Type != BufferedEventAgentMessage || events[4].Seq != 5 {
		t.Errorf("events[4].Type = %v, Seq = %d, want AgentMessage, 5", events[4].Type, events[4].Seq)
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

	buf.AppendAgentMessage(1, "Hello")
	buf.AppendToolCall(2, "tool-1", "Test", "done")

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

	buf.AppendAgentMessage(1, "Hello")

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

	buf.AppendAgentMessage(1, "Part 1. ")
	buf.AppendToolCall(2, "tool-1", "Test", "done")
	buf.AppendAgentMessage(3, "Part 2. ")
	buf.AppendAgentThought(4, "Thinking...")
	buf.AppendAgentMessage(5, "Part 3.")

	// GetAgentMessage should concatenate all agent messages
	result := buf.GetAgentMessage()
	if result != "Part 1. Part 2. Part 3." {
		t.Errorf("GetAgentMessage = %q, want %q", result, "Part 1. Part 2. Part 3.")
	}
}

func TestEventBuffer_GetAgentThought_Interleaved(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentThought(1, "Thought 1. ")
	buf.AppendAgentMessage(2, "Message")
	buf.AppendAgentThought(3, "Thought 2.")

	// GetAgentThought should concatenate all thoughts
	result := buf.GetAgentThought()
	if result != "Thought 1. Thought 2." {
		t.Errorf("GetAgentThought = %q, want %q", result, "Thought 1. Thought 2.")
	}
}

func TestEventBuffer_ToolCallUpdate(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendToolCall(1, "tool-1", "Read file", "running")
	status := "completed"
	buf.AppendToolCallUpdate(2, "tool-1", &status)

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

	buf.AppendAgentThought(1, "Thinking...")
	buf.AppendAgentMessage(2, "Hello")
	buf.AppendToolCall(3, "tool-1", "Read", "running")
	status := "done"
	buf.AppendToolCallUpdate(4, "tool-1", &status)
	buf.AppendPlan(5)
	buf.AppendFileRead(6, "/path/to/file", 100)
	buf.AppendFileWrite(7, "/path/to/output", 200)

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

// replayTestObserver implements SessionObserver for testing ReplayTo.
// It tracks all event types with full details.
type replayTestObserver struct {
	agentMessages []string
	agentThoughts []string
	toolCalls     []struct{ id, title, status string }
	toolUpdates   []struct {
		id     string
		status *string
	}
	planCalls int
	fileReads []struct {
		path string
		size int
	}
	fileWrites []struct {
		path string
		size int
	}
}

func (m *replayTestObserver) OnAgentMessage(_ int64, html string) {
	m.agentMessages = append(m.agentMessages, html)
}
func (m *replayTestObserver) OnAgentThought(_ int64, text string) {
	m.agentThoughts = append(m.agentThoughts, text)
}
func (m *replayTestObserver) OnToolCall(_ int64, id, title, status string) {
	m.toolCalls = append(m.toolCalls, struct{ id, title, status string }{id, title, status})
}
func (m *replayTestObserver) OnToolUpdate(_ int64, id string, status *string) {
	m.toolUpdates = append(m.toolUpdates, struct {
		id     string
		status *string
	}{id, status})
}
func (m *replayTestObserver) OnPlan(_ int64) { m.planCalls++ }
func (m *replayTestObserver) OnFileRead(_ int64, path string, size int) {
	m.fileReads = append(m.fileReads, struct {
		path string
		size int
	}{path, size})
}
func (m *replayTestObserver) OnFileWrite(_ int64, path string, size int) {
	m.fileWrites = append(m.fileWrites, struct {
		path string
		size int
	}{path, size})
}
func (m *replayTestObserver) OnPermission(_ context.Context, _ acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}
func (m *replayTestObserver) OnPromptComplete(_ int)                           {}
func (m *replayTestObserver) OnActionButtons(_ []ActionButton)                 {}
func (m *replayTestObserver) OnUserPrompt(_ int64, _, _, _ string, _ []string) {}
func (m *replayTestObserver) OnError(_ string)                                 {}
func (m *replayTestObserver) OnQueueUpdated(_ int, _, _ string)                {}
func (m *replayTestObserver) OnQueueReordered(_ []session.QueuedMessage)       {}
func (m *replayTestObserver) OnQueueMessageSending(_ string)                   {}
func (m *replayTestObserver) OnQueueMessageSent(_ string)                      {}

func TestBufferedEvent_ReplayTo(t *testing.T) {
	observer := &replayTestObserver{}

	// Test each event type with seq numbers
	events := []BufferedEvent{
		{Type: BufferedEventAgentThought, Seq: 1, Data: &AgentThoughtData{Text: "thinking"}},
		{Type: BufferedEventAgentMessage, Seq: 2, Data: &AgentMessageData{HTML: "<p>hello</p>"}},
		{Type: BufferedEventToolCall, Seq: 3, Data: &ToolCallData{ID: "t1", Title: "Read", Status: "running"}},
		{Type: BufferedEventToolCallUpdate, Seq: 4, Data: &ToolCallUpdateData{ID: "t1", Status: ptr("done")}},
		{Type: BufferedEventPlan, Seq: 5, Data: &PlanData{}},
		{Type: BufferedEventFileRead, Seq: 6, Data: &FileOperationData{Path: "/a.txt", Size: 100}},
		{Type: BufferedEventFileWrite, Seq: 7, Data: &FileOperationData{Path: "/b.txt", Size: 200}},
	}

	for _, e := range events {
		e.ReplayTo(observer)
	}

	if len(observer.agentThoughts) != 1 || observer.agentThoughts[0] != "thinking" {
		t.Errorf("agentThoughts = %v, want [thinking]", observer.agentThoughts)
	}
	if len(observer.agentMessages) != 1 || observer.agentMessages[0] != "<p>hello</p>" {
		t.Errorf("agentMessages = %v, want [<p>hello</p>]", observer.agentMessages)
	}
	if len(observer.toolCalls) != 1 {
		t.Errorf("toolCalls = %v, want 1 call", observer.toolCalls)
	}
	if len(observer.toolUpdates) != 1 {
		t.Errorf("toolUpdates = %v, want 1 update", observer.toolUpdates)
	}
	if observer.planCalls != 1 {
		t.Errorf("planCalls = %d, want 1", observer.planCalls)
	}
	if len(observer.fileReads) != 1 || observer.fileReads[0].path != "/a.txt" {
		t.Errorf("fileReads = %v, want [{/a.txt 100}]", observer.fileReads)
	}
	if len(observer.fileWrites) != 1 || observer.fileWrites[0].path != "/b.txt" {
		t.Errorf("fileWrites = %v, want [{/b.txt 200}]", observer.fileWrites)
	}
}

func TestBufferedEvent_ReplayTo_EmptyData(t *testing.T) {
	observer := &replayTestObserver{}

	// Empty text should not trigger callback
	event := BufferedEvent{Type: BufferedEventAgentThought, Data: &AgentThoughtData{Text: ""}}
	event.ReplayTo(observer)

	if len(observer.agentThoughts) != 0 {
		t.Errorf("agentThoughts = %v, want empty", observer.agentThoughts)
	}

	// Empty HTML should not trigger callback
	event = BufferedEvent{Type: BufferedEventAgentMessage, Data: &AgentMessageData{HTML: ""}}
	event.ReplayTo(observer)

	if len(observer.agentMessages) != 0 {
		t.Errorf("agentMessages = %v, want empty", observer.agentMessages)
	}
}

// mockPersister implements EventPersister for testing PersistTo.
type mockPersister struct {
	agentMessages []string
	agentThoughts []string
	toolCalls     []struct{ id, title, status, kind string }
	toolUpdates   []struct {
		id     string
		status *string
	}
	planCalls int
	fileReads []struct {
		path string
		size int
	}
	fileWrites []struct {
		path string
		size int
	}
	returnErr error
}

func (m *mockPersister) RecordAgentMessage(html string) error {
	m.agentMessages = append(m.agentMessages, html)
	return m.returnErr
}
func (m *mockPersister) RecordAgentThought(text string) error {
	m.agentThoughts = append(m.agentThoughts, text)
	return m.returnErr
}
func (m *mockPersister) RecordToolCall(id, title, status, kind string, _, _ any) error {
	m.toolCalls = append(m.toolCalls, struct{ id, title, status, kind string }{id, title, status, kind})
	return m.returnErr
}
func (m *mockPersister) RecordToolCallUpdate(id string, status, _ *string) error {
	m.toolUpdates = append(m.toolUpdates, struct {
		id     string
		status *string
	}{id, status})
	return m.returnErr
}
func (m *mockPersister) RecordPlan(_ []session.PlanEntry) error {
	m.planCalls++
	return m.returnErr
}
func (m *mockPersister) RecordFileRead(path string, size int) error {
	m.fileReads = append(m.fileReads, struct {
		path string
		size int
	}{path, size})
	return m.returnErr
}
func (m *mockPersister) RecordFileWrite(path string, size int) error {
	m.fileWrites = append(m.fileWrites, struct {
		path string
		size int
	}{path, size})
	return m.returnErr
}

func TestBufferedEvent_PersistTo(t *testing.T) {
	persister := &mockPersister{}

	events := []BufferedEvent{
		{Type: BufferedEventAgentThought, Data: &AgentThoughtData{Text: "thinking"}},
		{Type: BufferedEventAgentMessage, Data: &AgentMessageData{HTML: "<p>hello</p>"}},
		{Type: BufferedEventToolCall, Data: &ToolCallData{ID: "t1", Title: "Read", Status: "running"}},
		{Type: BufferedEventToolCallUpdate, Data: &ToolCallUpdateData{ID: "t1", Status: ptr("done")}},
		{Type: BufferedEventPlan, Data: &PlanData{}},
		{Type: BufferedEventFileRead, Data: &FileOperationData{Path: "/a.txt", Size: 100}},
		{Type: BufferedEventFileWrite, Data: &FileOperationData{Path: "/b.txt", Size: 200}},
	}

	for _, e := range events {
		if err := e.PersistTo(persister); err != nil {
			t.Errorf("PersistTo returned error: %v", err)
		}
	}

	if len(persister.agentThoughts) != 1 || persister.agentThoughts[0] != "thinking" {
		t.Errorf("agentThoughts = %v, want [thinking]", persister.agentThoughts)
	}
	if len(persister.agentMessages) != 1 || persister.agentMessages[0] != "<p>hello</p>" {
		t.Errorf("agentMessages = %v, want [<p>hello</p>]", persister.agentMessages)
	}
	if len(persister.toolCalls) != 1 {
		t.Errorf("toolCalls = %v, want 1 call", persister.toolCalls)
	}
	if len(persister.toolUpdates) != 1 {
		t.Errorf("toolUpdates = %v, want 1 update", persister.toolUpdates)
	}
	if persister.planCalls != 1 {
		t.Errorf("planCalls = %d, want 1", persister.planCalls)
	}
	if len(persister.fileReads) != 1 || persister.fileReads[0].path != "/a.txt" {
		t.Errorf("fileReads = %v, want [{/a.txt 100}]", persister.fileReads)
	}
	if len(persister.fileWrites) != 1 || persister.fileWrites[0].path != "/b.txt" {
		t.Errorf("fileWrites = %v, want [{/b.txt 200}]", persister.fileWrites)
	}
}

func TestBufferedEvent_PersistTo_ReturnsError(t *testing.T) {
	persister := &mockPersister{returnErr: errors.New("persist error")}

	event := BufferedEvent{Type: BufferedEventAgentMessage, Data: &AgentMessageData{HTML: "test"}}
	err := event.PersistTo(persister)

	if err == nil {
		t.Error("PersistTo should return error")
	}
	if err.Error() != "persist error" {
		t.Errorf("error = %v, want 'persist error'", err)
	}
}

func TestEventBuffer_SeqCoalescing(t *testing.T) {
	// Test that consecutive agent messages share the same seq (coalescing)
	buf := NewEventBuffer()

	// First chunk gets seq=1, creates new event
	seq1, isNew1 := buf.AppendAgentMessage(1, "Hello ")
	if !isNew1 {
		t.Error("First chunk should create new event")
	}
	if seq1 != 1 {
		t.Errorf("First chunk seq = %d, want 1", seq1)
	}

	// Second chunk gets seq=2, but appends to existing event, returns seq=1
	seq2, isNew2 := buf.AppendAgentMessage(2, "world!")
	if isNew2 {
		t.Error("Second chunk should append to existing event")
	}
	if seq2 != 1 {
		t.Errorf("Second chunk seq = %d, want 1 (coalesced)", seq2)
	}

	// Verify only one event with seq=1
	events := buf.Events()
	if len(events) != 1 {
		t.Errorf("Events count = %d, want 1", len(events))
	}
	if events[0].Seq != 1 {
		t.Errorf("Event seq = %d, want 1", events[0].Seq)
	}

	// Verify content is concatenated
	data := events[0].Data.(*AgentMessageData)
	if data.HTML != "Hello world!" {
		t.Errorf("HTML = %q, want %q", data.HTML, "Hello world!")
	}
}

func TestEventBuffer_SeqPreservedOnInterleave(t *testing.T) {
	// Test that interleaved events preserve their individual seq numbers
	buf := NewEventBuffer()

	// Message with seq=1
	buf.AppendAgentMessage(1, "Starting...")

	// Tool call with seq=2
	buf.AppendToolCall(2, "tool-1", "Read file", "running")

	// New message with seq=3 (not coalesced because tool call in between)
	seq3, isNew3 := buf.AppendAgentMessage(3, "Found it!")
	if !isNew3 {
		t.Error("Message after tool call should create new event")
	}
	if seq3 != 3 {
		t.Errorf("Third event seq = %d, want 3", seq3)
	}

	events := buf.Events()
	if len(events) != 3 {
		t.Errorf("Events count = %d, want 3", len(events))
	}

	// Verify seq numbers are preserved
	expectedSeqs := []int64{1, 2, 3}
	for i, e := range events {
		if e.Seq != expectedSeqs[i] {
			t.Errorf("events[%d].Seq = %d, want %d", i, e.Seq, expectedSeqs[i])
		}
	}
}

func TestEventBuffer_LastSeq(t *testing.T) {
	buf := NewEventBuffer()

	// Empty buffer should return 0
	if buf.LastSeq() != 0 {
		t.Errorf("Empty buffer LastSeq = %d, want 0", buf.LastSeq())
	}

	buf.AppendAgentMessage(5, "Hello")
	if buf.LastSeq() != 5 {
		t.Errorf("After first event LastSeq = %d, want 5", buf.LastSeq())
	}

	buf.AppendToolCall(10, "tool-1", "Test", "done")
	if buf.LastSeq() != 10 {
		t.Errorf("After second event LastSeq = %d, want 10", buf.LastSeq())
	}
}

// =============================================================================
// Edge Case Tests for Event Buffer
// =============================================================================

// TestEventBuffer_OutOfOrderSeqPreserved tests that events added out of order
// preserve their original sequence numbers.
func TestEventBuffer_OutOfOrderSeqPreserved(t *testing.T) {
	buf := NewEventBuffer()

	// Add events out of order (simulating markdown buffering scenario)
	buf.AppendToolCall(3, "tool-1", "Read file", "running")
	buf.AppendAgentMessage(1, "Let me read that file")
	buf.AppendToolCallUpdate(4, "tool-1", ptr("completed"))
	buf.AppendAgentMessage(2, " for you.")

	events := buf.Events()

	// Events should be in insertion order, not seq order
	// (sorting happens during persistence, not in buffer)
	if len(events) != 4 {
		t.Fatalf("Expected 4 events, got %d", len(events))
	}

	// Verify seq numbers are preserved
	expectedSeqs := []int64{3, 1, 4, 2}
	for i, e := range events {
		if e.Seq != expectedSeqs[i] {
			t.Errorf("events[%d].Seq = %d, want %d", i, e.Seq, expectedSeqs[i])
		}
	}
}

// TestEventBuffer_CoalescingPreservesFirstSeq tests that when agent messages
// are coalesced, the first chunk's seq is preserved.
func TestEventBuffer_CoalescingPreservesFirstSeq(t *testing.T) {
	buf := NewEventBuffer()

	// First chunk with seq=5
	seq1, isNew1 := buf.AppendAgentMessage(5, "Hello ")
	if !isNew1 || seq1 != 5 {
		t.Errorf("First chunk: seq=%d, isNew=%v, want seq=5, isNew=true", seq1, isNew1)
	}

	// Second chunk with seq=6 should coalesce and return seq=5
	seq2, isNew2 := buf.AppendAgentMessage(6, "World")
	if isNew2 || seq2 != 5 {
		t.Errorf("Second chunk: seq=%d, isNew=%v, want seq=5, isNew=false", seq2, isNew2)
	}

	// Third chunk with seq=7 should also coalesce
	seq3, isNew3 := buf.AppendAgentMessage(7, "!")
	if isNew3 || seq3 != 5 {
		t.Errorf("Third chunk: seq=%d, isNew=%v, want seq=5, isNew=false", seq3, isNew3)
	}

	// Buffer should have only one event with seq=5
	events := buf.Events()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}
	if events[0].Seq != 5 {
		t.Errorf("Coalesced event seq = %d, want 5", events[0].Seq)
	}

	// Content should be concatenated
	if data, ok := events[0].Data.(*AgentMessageData); ok {
		if data.HTML != "Hello World!" {
			t.Errorf("Coalesced content = %q, want %q", data.HTML, "Hello World!")
		}
	}
}

// TestEventBuffer_FlushClearsBuffer tests that Flush returns all events and clears the buffer.
func TestEventBuffer_FlushClearsBuffer(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentMessage(1, "Hello")
	buf.AppendToolCall(2, "tool-1", "Test", "done")

	// Flush should return events
	events := buf.Flush()
	if len(events) != 2 {
		t.Errorf("Flush returned %d events, want 2", len(events))
	}

	// Buffer should be empty after flush
	if !buf.IsEmpty() {
		t.Error("Buffer should be empty after Flush")
	}
	if buf.Len() != 0 {
		t.Errorf("Buffer Len = %d after Flush, want 0", buf.Len())
	}

	// Second flush should return empty
	events2 := buf.Flush()
	if len(events2) != 0 {
		t.Errorf("Second Flush returned %d events, want 0", len(events2))
	}
}

// TestEventBuffer_EventsDoesNotClearBuffer tests that Events() returns a copy
// without modifying the buffer.
func TestEventBuffer_EventsDoesNotClearBuffer(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentMessage(1, "Hello")

	// Get events
	events1 := buf.Events()
	if len(events1) != 1 {
		t.Fatalf("First Events() returned %d events, want 1", len(events1))
	}

	// Buffer should still have the event
	if buf.IsEmpty() {
		t.Error("Buffer should not be empty after Events()")
	}

	// Get events again
	events2 := buf.Events()
	if len(events2) != 1 {
		t.Errorf("Second Events() returned %d events, want 1", len(events2))
	}
}

// TestEventBuffer_ReplayToObserver tests that ReplayTo correctly sends events to an observer.
func TestEventBuffer_ReplayToObserver(t *testing.T) {
	buf := NewEventBuffer()

	buf.AppendAgentThought(1, "Thinking...")
	buf.AppendAgentMessage(2, "<p>Hello</p>")
	buf.AppendToolCall(3, "tool-1", "Read file", "running")

	// Create a mock observer to capture replayed events
	observer := &testReplayObserver{}

	events := buf.Events()
	for _, e := range events {
		e.ReplayTo(observer)
	}

	// Verify observer received all events
	if len(observer.thoughts) != 1 || observer.thoughts[0] != "Thinking..." {
		t.Errorf("Observer thoughts = %v, want [Thinking...]", observer.thoughts)
	}
	if len(observer.messages) != 1 || observer.messages[0] != "<p>Hello</p>" {
		t.Errorf("Observer messages = %v, want [<p>Hello</p>]", observer.messages)
	}
	if len(observer.toolCalls) != 1 || observer.toolCalls[0] != "tool-1" {
		t.Errorf("Observer toolCalls = %v, want [tool-1]", observer.toolCalls)
	}
}

// testReplayObserver is a minimal observer for testing ReplayTo.
type testReplayObserver struct {
	thoughts  []string
	messages  []string
	toolCalls []string
}

func (o *testReplayObserver) OnAgentThought(seq int64, text string) {
	o.thoughts = append(o.thoughts, text)
}

func (o *testReplayObserver) OnAgentMessage(seq int64, html string) {
	o.messages = append(o.messages, html)
}

func (o *testReplayObserver) OnToolCall(seq int64, id, title, status string) {
	o.toolCalls = append(o.toolCalls, id)
}

func (o *testReplayObserver) OnToolUpdate(seq int64, id string, status *string) {}
func (o *testReplayObserver) OnPlan(seq int64)                                  {}
func (o *testReplayObserver) OnFileWrite(seq int64, path string, size int)      {}
func (o *testReplayObserver) OnFileRead(seq int64, path string, size int)       {}
func (o *testReplayObserver) OnPromptComplete(eventCount int)                   {}
func (o *testReplayObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs []string) {
}
func (o *testReplayObserver) OnError(message string) {}
func (o *testReplayObserver) OnQueueUpdated(queueLength int, action string, messageID string) {
}
func (o *testReplayObserver) OnQueueMessageSending(messageID string)            {}
func (o *testReplayObserver) OnQueueMessageSent(messageID string)               {}
func (o *testReplayObserver) OnQueueReordered(messages []session.QueuedMessage) {}
func (o *testReplayObserver) OnActionButtons(buttons []ActionButton)            {}
func (o *testReplayObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

func ptr(s string) *string { return &s }
