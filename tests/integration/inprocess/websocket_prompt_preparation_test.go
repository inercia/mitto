//go:build integration

package inprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// Exercise the actual WS reader and PromptWithMeta, not a mock dispatch helper.
// The existing mock's model delay holds preparation BEFORE prompt persistence.
func TestWebSocketPromptPreparationDoesNotBlockReadPump(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", stop), func(t *testing.T) {
			t.Setenv("MOCK_SET_MODEL_DELAY_MS", "3000")
			ts := SetupTestServer(t, func(c *web.Config) {
				c.MittoConfig.Models = []config.ModelProfile{{
					Name: "WS preflight",
					Criteria: &config.ACPServerConstraint{
						Pattern: "opus", MatchMode: "contains",
					},
				}}
			})
			promptsDir := filepath.Join(ts.TempDir, "workspace", ".mitto", "prompts")
			if err := os.MkdirAll(promptsDir, 0755); err != nil {
				t.Fatal(err)
			}
			const prompt = `name: WS preflight
preferredModels:
  - modelName: WS preflight
prompt: 'WS_PREFLIGHT {{ .Session.ModelName }}'
`
			if err := os.WriteFile(filepath.Join(promptsDir, "preflight.prompt.yaml"), []byte(prompt), 0644); err != nil {
				t.Fatal(err)
			}
			sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "WS preflight test"})
			if err != nil {
				t.Fatal(err)
			}
			defer ts.Client.DeleteSession(sess.SessionID)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			url := fmt.Sprintf("ws://%s/mitto/api/sessions/%s/ws", ts.HTTPServer.Listener.Addr(), sess.SessionID)
			conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			waitForWSMessageType(t, conn, "connected", 5*time.Second)
			writeWSJSON(t, conn, map[string]any{"type": "load_events", "data": map[string]any{"limit": 10}})
			waitForWSMessageType(t, conn, "events_loaded", 5*time.Second)

			// Warm the deferred handshake/catalog without a model preference first.
			writeWSJSON(t, conn, map[string]any{"type": "prompt", "data": map[string]any{"message": "warm up"}})
			waitForWSMessageType(t, conn, "prompt_complete", 10*time.Second)
			bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
			if bs == nil {
				t.Fatal("missing live background session")
			}
			waitFor(t, 5*time.Second, func() bool { return !bs.IsPrompting() }, "warmup idle")

			const promptID = "ws-preflight-prompt"
			writeWSJSON(t, conn, map[string]any{"type": "prompt", "data": map[string]any{
				"prompt_name": "WS preflight", "prompt_id": promptID,
			}})
			waitFor(t, time.Second, bs.IsPrompting, "preparation reserved")
			writeWSJSON(t, conn, map[string]any{"type": "keepalive", "data": map[string]any{"client_time": 123}})
			// A blocked reader times out here; an invented early ACK also fails.
			ack := readPreflightWSUntil(t, conn, "keepalive_ack", time.Second)
			var keepalive struct {
				IsPrompting bool `json:"is_prompting"`
			}
			if err := json.Unmarshal(ack.Data, &keepalive); err != nil || !keepalive.IsPrompting {
				t.Fatalf("keepalive must report preparing as busy: %s (%v)", ack.Data, err)
			}
			if seq := preflightPromptSeq(t, ts, sess.SessionID, promptID); seq != 0 {
				t.Fatalf("prompt persisted during model preparation: seq=%d", seq)
			}

			if stop {
				writeWSJSON(t, conn, map[string]any{"type": "cancel"})
				msg := readPreflightWSUntil(t, conn, "error", 2*time.Second)
				var failure struct {
					PromptID string `json:"prompt_id"`
					Message  string `json:"message"`
				}
				if err := json.Unmarshal(msg.Data, &failure); err != nil {
					t.Fatal(err)
				}
				if failure.PromptID != promptID || !strings.Contains(failure.Message, "canceled") {
					t.Fatalf("expected prompt-scoped cancellation, got %s", msg.Data)
				}
				if seq := preflightPromptSeq(t, ts, sess.SessionID, promptID); seq != 0 {
					t.Fatalf("cancelled preparation was persisted: seq=%d", seq)
				}
				return
			}

			msg := readPreflightWSUntil(t, conn, "user_prompt", 10*time.Second)
			var delivered struct {
				PromptID string `json:"prompt_id"`
				Seq      int64  `json:"seq"`
				Message  string `json:"message"`
			}
			if err := json.Unmarshal(msg.Data, &delivered); err != nil {
				t.Fatal(err)
			}
			if delivered.PromptID != promptID || delivered.Seq == 0 || !strings.Contains(delivered.Message, "Opus") {
				t.Fatalf("expected resolved model and durable prompt identity: %s", msg.Data)
			}
			if seq := preflightPromptSeq(t, ts, sess.SessionID, promptID); seq != delivered.Seq {
				t.Fatalf("ACK seq=%d, persisted seq=%d", delivered.Seq, seq)
			}
		})
	}
}

func readPreflightWSUntil(t *testing.T, conn *websocket.Conn, want string, timeout time.Duration) wsTestMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	for {
		var msg wsTestMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("waiting for %s during preparation: %v", want, err)
		}
		if msg.Type == want {
			return msg
		}
		if msg.Type == "user_prompt" || msg.Type == "prompt_received" || msg.Type == "error" {
			t.Fatalf("unexpected %s while waiting for %s: %s", msg.Type, want, msg.Data)
		}
	}
}

func preflightPromptSeq(t *testing.T, ts *TestServer, sessionID, promptID string) int64 {
	t.Helper()
	events, err := ts.Store.ReadEvents(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		data, ok := event.Data.(map[string]interface{})
		if event.Type == "user_prompt" && ok && data["prompt_id"] == promptID {
			return event.Seq
		}
	}
	return 0
}
