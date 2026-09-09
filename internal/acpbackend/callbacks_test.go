package acpbackend

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

func TestBuildCallbacks_OnSessionUpdatePublishes(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}

	var received *agentbackend.Event
	sub, err := c.Subscribe(context.Background(), ref, func(ev agentbackend.Event) { received = &ev })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	cbs := c.buildCallbacks(ref)
	notif := acp.SessionNotification{
		SessionId: "upstream-1",
		Update:    acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("hi")}},
	}
	if err := cbs.OnSessionUpdate(context.Background(), notif); err != nil {
		t.Fatalf("OnSessionUpdate: %v", err)
	}
	if received == nil {
		t.Fatal("expected the subscriber to receive a translated event")
	}
	if received.Kind != agentbackend.EventAgentMessage {
		t.Errorf("Kind = %q, want agent_message", received.Kind)
	}
}

func TestBuildCallbacks_OnSessionUpdate_UntranslatableIsSilentlyDropped(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	ref := agentbackend.SessionRef{ConversationID: "conv-1"}
	called := false
	sub, _ := c.Subscribe(context.Background(), ref, func(agentbackend.Event) { called = true })
	defer sub.Close()

	cbs := c.buildCallbacks(ref)
	if err := cbs.OnSessionUpdate(context.Background(), acp.SessionNotification{Update: acp.SessionUpdate{}}); err != nil {
		t.Fatalf("OnSessionUpdate must not error on an untranslatable update: %v", err)
	}
	if called {
		t.Error("expected no delivery for an update kind with no neutral analogue")
	}
}

func TestBuildCallbacks_TerminalHandlersAlwaysUnsupported(t *testing.T) {
	c := NewConnection(newFakeSharedProcess(), "acp", "/work", nil)
	cbs := c.buildCallbacks(agentbackend.SessionRef{ConversationID: "conv-1"})

	_, err := cbs.OnCreateTerminal(context.Background(), acp.CreateTerminalRequest{})
	var unsupported *agentbackend.UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Feature != agentbackend.FeatureTerminals {
		t.Fatalf("OnCreateTerminal: expected *UnsupportedError{Feature: terminals}, got %v", err)
	}

	if _, err := cbs.OnTerminalOutput(context.Background(), acp.TerminalOutputRequest{}); !errors.As(err, &unsupported) {
		t.Errorf("OnTerminalOutput: expected *UnsupportedError, got %v", err)
	}
	if _, err := cbs.OnReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{}); !errors.As(err, &unsupported) {
		t.Errorf("OnReleaseTerminal: expected *UnsupportedError, got %v", err)
	}
	if _, err := cbs.OnWaitForTerminalExit(context.Background(), acp.WaitForTerminalExitRequest{}); !errors.As(err, &unsupported) {
		t.Errorf("OnWaitForTerminalExit: expected *UnsupportedError, got %v", err)
	}
	if _, err := cbs.OnKillTerminal(context.Background(), acp.KillTerminalRequest{}); !errors.As(err, &unsupported) {
		t.Errorf("OnKillTerminal: expected *UnsupportedError, got %v", err)
	}
}
