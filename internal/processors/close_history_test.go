package processors

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestBuildCloseHistorySnapshot_BoundsAndFiltersEvents(t *testing.T) {
	events := make([]session.Event, 0, 57)
	for i := 1; i <= 55; i++ {
		events = append(events, session.Event{
			Seq: int64(i), Type: session.EventTypeUserPrompt,
			Data: session.UserPromptData{Message: "prompt"},
		})
	}
	events = append(events,
		session.Event{Seq: 56, Type: session.EventTypeToolCall, Data: session.ToolCallData{Title: "excluded"}},
		session.Event{Seq: 57, Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Markdown: strings.Repeat("é", 2000)}},
	)

	raw, err := BuildCloseHistorySnapshot("session-1", events)
	if err != nil {
		t.Fatalf("BuildCloseHistorySnapshot: %v", err)
	}
	var snapshot closeHistorySnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatalf("Unmarshal snapshot: %v", err)
	}
	if snapshot.TotalEvents != 57 || snapshot.FilteredEvents != 56 || snapshot.ReturnedEvents != closeHistoryMaxEvents {
		t.Fatalf("snapshot counts = total:%d filtered:%d returned:%d", snapshot.TotalEvents, snapshot.FilteredEvents, snapshot.ReturnedEvents)
	}
	if got := snapshot.Events[0].Seq; got != 7 {
		t.Fatalf("first retained seq = %d, want 7", got)
	}
	latest := snapshot.Events[len(snapshot.Events)-1]
	if latest.Seq != 57 || len(latest.Text) > closeHistoryMaxText || !strings.HasSuffix(latest.Text, "...") {
		t.Fatalf("latest bounded event = %#v (bytes=%d)", latest, len(latest.Text))
	}
	for _, event := range snapshot.Events {
		if event.Type == string(session.EventTypeToolCall) {
			t.Fatal("tool event leaked into close history snapshot")
		}
	}
}

func TestApplyOnClose_HistoryUnavailableDoesNotAcknowledgeAsNoOp(t *testing.T) {
	proc := &Processor{
		Name: "close-history-required", When: WhenConfig{On: PhaseConversationClosed, Match: MatchAll},
		Prompt: "analyze history",
	}
	m := NewManager("", nil)
	m.processors = []*Processor{proc}
	dispatches := 0
	m.SetPromptCompletionFunc(func(context.Context, string, string, string, string) (PromptCompletion, error) {
		dispatches++
		return PromptCompletion{SaveCount: 0, SaveCountKnown: true}, nil
	})

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID: "deleted-source", WorkspaceUUID: "workspace-1",
		HistorySnapshotError: "session not found",
	})
	if dispatches != 0 {
		t.Fatalf("history-unavailable close dispatched %d times and could be acknowledged as save_count=0", dispatches)
	}
}
