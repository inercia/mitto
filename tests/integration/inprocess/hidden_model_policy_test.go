//go:build integration

package inprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// TestHiddenModelPolicySaveAndDispatch uses the real config endpoint and ACP
// subprocess. The RPC log is the oracle for the model actually used on a turn.
func TestHiddenModelPolicySaveAndDispatch(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "models.json")
	order := filepath.Join(dir, "order.log")
	attempts := filepath.Join(dir, "attempts.jsonl")
	const catalog = `{"initial_model":"terra-private","options":[{"value":"terra-private","name":"Terra"},{"value":"coding-safe","name":"Coding Safe"},{"value":"general-safe","name":"General Safe"}]}`
	if err := os.WriteFile(fixture, []byte(catalog), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOCK_MODEL_FIXTURE_FILE", fixture)
	t.Setenv("MOCK_RPC_ORDER_FILE", order)
	t.Setenv("MOCK_MODEL_ATTEMPT_FILE", attempts)
	ts := SetupTestServer(t, func(c *web.Config) {
		if binary := os.Getenv("INPROCESS_MOCK_ACP_BINARY"); binary != "" {
			c.ACPCommand = binary
			c.MittoConfig.ACPServers[0].Command = binary
		}
	})
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "hidden-policy"})
	if err != nil {
		t.Fatal(err)
	}
	var bs *conversation.BackgroundSession
	waitFor(t, 15*time.Second, func() bool { bs = ts.Server.GetSessionManager().GetSession(sess.SessionID); return bs != nil }, "session created")
	prompt := func() {
		t.Helper()
		done := make(chan error, 1)
		if err := bs.PromptWithMeta("hello", conversation.PromptMeta{OnComplete: func(err error) { done <- err }}); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("prompt timeout")
		}
	}
	prompt()
	if bs.AgentModels() == nil || len(bs.AgentModels().AvailableModels) != 3 {
		t.Fatalf("initial catalog: %+v", bs.AgentModels())
	}
	wsURL := "ws" + strings.TrimPrefix(ts.HTTPServer.URL, "http") + "/mitto/api/events"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := ws.ReadJSON(&message); err != nil || message.Type != "connected" {
		t.Fatalf("events connection: %v %s", err, message.Type)
	}
	getConfig := func() map[string]any {
		t.Helper()
		resp, err := http.Get(ts.HTTPServer.URL + "/mitto/api/config")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var cfg map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	save := func(rules []string) (int, string) {
		t.Helper()
		cfg := getConfig()
		servers := cfg["acp_servers"].([]any)
		servers[0].(map[string]any)["hidden_models"] = rules
		body := map[string]any{}
		for _, key := range []string{"workspaces", "acp_servers", "prompts", "ui", "models", "session", "conversations", "permissions"} {
			if value, ok := cfg[key]; ok {
				body[key] = value
			}
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(ts.HTTPServer.URL+"/mitto/api/config", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		result, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(result)
	}
	if status, body := save([]string{"terra"}); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	for {
		if err := ws.ReadJSON(&message); err != nil {
			t.Fatalf("policy update over websocket: %v", err)
		}
		if message.Type != "config_option_changed" {
			continue
		}
		data := message.Data
		var payload struct {
			ConfigOptions []conversation.SessionConfigOption `json:"config_options"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, option := range payload.ConfigOptions {
			if option.Category == "model" {
				found = true
				for _, value := range option.Options {
					if value.Value == "terra-private" {
						t.Fatalf("hidden selectable WS option: %s", data)
					}
				}
			}
		}
		if !found {
			t.Fatalf("missing model option in WS update: %s", data)
		}
		break
	}
	for _, model := range bs.AgentModels().AvailableModels {
		if model.ModelId == "terra-private" {
			t.Fatalf("hidden model still selectable: %+v", bs.AgentModels())
		}
	}
	if status, body := save([]string{"("}); status != http.StatusBadRequest {
		t.Fatalf("invalid save: %d %s", status, body)
	}
	if err := bs.SetConfigOption(context.Background(), "model", "terra-private"); err == nil {
		t.Fatal("stale hidden selection accepted")
	}
	beforePrompt, err := os.ReadFile(attempts)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if bytes.Contains(beforePrompt, []byte(`"model":"terra-private"`)) {
		t.Fatalf("stale hidden selection reached agent: %s", beforePrompt)
	}
	prompt()
	current := bs.AgentModels().CurrentModelId
	if current == "terra-private" || current == "" {
		t.Fatalf("unsafe current model %q", current)
	}
	lines := readRPCOrder(t, order)
	var models []string
	for _, line := range lines {
		if model, ok := strings.CutPrefix(line, "prompt_model\t"+bs.GetACPID()+"\t"); ok {
			models = append(models, model)
		}
	}
	if len(models) != 2 || models[0] != "terra-private" || models[1] == "terra-private" {
		t.Fatalf("wire models: %v", models)
	}
	if status, body := save([]string{}); status != http.StatusOK {
		t.Fatalf("unhide: %d %s", status, body)
	}
	if len(bs.AgentModels().AvailableModels) != 3 {
		t.Fatalf("catalog after unhide: %+v", bs.AgentModels())
	}
	if got := bs.AgentModels().CurrentModelId; got != current {
		t.Fatalf("unhide switched model: %s -> %s", current, got)
	}
}
