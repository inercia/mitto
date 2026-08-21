//go:build integration

package inprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/pkg/api"
)

// TestLocalSessionWebSocketAcceptsPromptAboveExternalLimit reproduces mitto-7rer:
// localhost clients must accept prompts larger than the external 64 KiB limit.
func TestLocalSessionWebSocketAcceptsPromptAboveExternalLimit(t *testing.T) {
	ts := SetupTestServer(t)
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wsURL := fmt.Sprintf("ws://%s/mitto/api/sessions/%s/ws",
		ts.HTTPServer.Listener.Addr().String(), sess.SessionID)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	defer conn.Close()

	waitForWSMessageType(t, conn, "connected", 5*time.Second)
	writeWSJSON(t, conn, map[string]any{
		"type": "load_events",
		"data": map[string]any{"limit": 10},
	})
	waitForWSMessageType(t, conn, "events_loaded", 5*time.Second)

	const promptID = "mitto-7rer-oversized-local-prompt"
	writeWSJSON(t, conn, map[string]any{
		"type": "prompt",
		"data": map[string]any{
			"message":   strings.Repeat("x", 80*1024),
			"prompt_id": promptID,
		},
	})
	// The live-session path confirms persisted delivery with the OnUserPrompt
	// broadcast (offline queueing uses the separate prompt_received ACK).
	msg := waitForWSMessageType(t, conn, "user_prompt", 5*time.Second)
	var ack struct {
		PromptID string `json:"prompt_id"`
	}
	if err := json.Unmarshal(msg.Data, &ack); err != nil {
		t.Fatalf("decode user_prompt: %v", err)
	}
	if ack.PromptID != promptID {
		t.Fatalf("user_prompt prompt_id = %q, want %q", ack.PromptID, promptID)
	}
}

type wsTestMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func writeWSJSON(t *testing.T, conn *websocket.Conn, payload any) {
	t.Helper()
	if err := conn.WriteJSON(payload); err != nil {
		t.Fatalf("write WebSocket message: %v", err)
	}
}

func waitForWSMessageType(t *testing.T, conn *websocket.Conn, want string, timeout time.Duration) wsTestMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set WebSocket read deadline: %v", err)
	}
	for {
		var msg wsTestMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("waiting for WebSocket message %q: %v", want, err)
		}
		if msg.Type == want {
			return msg
		}
	}
}
