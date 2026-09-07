package acpbackend

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestConnection_Subscribe_ScopedDelivery(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var received []agentbackend.Event
	sub, err := c.Subscribe(context.Background(), ref, func(ev agentbackend.Event) { received = append(received, ev) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	c.publish(agentbackend.Event{Session: ref, Kind: agentbackend.EventAgentMessage})
	c.publish(agentbackend.Event{Session: agentbackend.SessionRef{ConversationID: "conv-other"}, Kind: agentbackend.EventAgentMessage})

	if len(received) != 1 {
		t.Fatalf("expected exactly 1 scoped event, got %d: %+v", len(received), received)
	}
}

func TestConnection_Subscribe_HostWideReceivesEverything(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	var received []agentbackend.Event
	sub, err := c.Subscribe(context.Background(), agentbackend.SessionRef{}, func(ev agentbackend.Event) { received = append(received, ev) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	c.publish(agentbackend.Event{Session: agentbackend.SessionRef{ConversationID: "conv-a"}})
	c.publish(agentbackend.Event{Session: agentbackend.SessionRef{ConversationID: "conv-b"}})
	if len(received) != 2 {
		t.Fatalf("expected host-wide subscriber to see both events, got %d", len(received))
	}
}

func TestSubscription_Close_StopsDelivery(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	n := 0
	sub, _ := c.Subscribe(context.Background(), ref, func(agentbackend.Event) { n++ })

	c.publish(agentbackend.Event{Session: ref})
	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close must be idempotent.
	if err := sub.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	c.publish(agentbackend.Event{Session: ref})
	if n != 1 {
		t.Fatalf("expected exactly 1 delivery before Close, got %d", n)
	}
}

func TestTranslateSessionUpdate_AgentMessageChunk(t *testing.T) {
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	u := acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("hi")}}
	ev, ok := translateSessionUpdate(ref, u)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ev.Kind != agentbackend.EventAgentMessage || ev.Origin != agentbackend.OriginLocal {
		t.Errorf("unexpected event: %+v", ev)
	}
	if len(ev.Content) != 1 || ev.Content[0].Text.Text != "hi" {
		t.Errorf("unexpected content: %+v", ev.Content)
	}
}

func TestTranslateSessionUpdate_AgentThoughtChunk(t *testing.T) {
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	u := acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock("thinking")}}
	ev, ok := translateSessionUpdate(ref, u)
	if !ok || ev.Kind != agentbackend.EventAgentThought {
		t.Fatalf("unexpected result: ok=%v ev=%+v", ok, ev)
	}
}

func TestTranslateSessionUpdate_CurrentModeUpdate(t *testing.T) {
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	u := acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{CurrentModeId: "code"}}
	ev, ok := translateSessionUpdate(ref, u)
	if !ok || ev.Kind != agentbackend.EventModeChange {
		t.Fatalf("unexpected result: ok=%v ev=%+v", ok, ev)
	}
	if ev.Modes == nil || ev.Modes.CurrentModeID != "code" {
		t.Errorf("unexpected Modes: %+v", ev.Modes)
	}
}

func TestTranslateSessionUpdate_UnknownKindNotOK(t *testing.T) {
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	// A SessionUpdate with no recognized field set (e.g. UsageUpdate, deferred).
	if _, ok := translateSessionUpdate(ref, acp.SessionUpdate{}); ok {
		t.Fatal("expected ok=false for an update kind with no neutral analogue yet")
	}
}
