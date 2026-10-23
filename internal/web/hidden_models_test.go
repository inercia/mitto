package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

type hiddenPolicyBroadcast struct {
	messageType string
	payload     map[string]interface{}
}

type hiddenPolicyEvents struct {
	broadcasts []hiddenPolicyBroadcast
}

func (e *hiddenPolicyEvents) Broadcast(messageType string, data interface{}) {
	payload, _ := data.(map[string]interface{})
	e.broadcasts = append(e.broadcasts, hiddenPolicyBroadcast{messageType: messageType, payload: payload})
}

func (*hiddenPolicyEvents) ClientCount() int { return 1 }

func hiddenReq(t *testing.T, body string) *ConfigSaveRequest {
	t.Helper()
	var req ConfigSaveRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

func hiddenServer(rules ...string) *Server {
	return &Server{config: Config{MittoConfig: &config.Config{
		ACPServers: []config.ACPServer{{Name: "old", Command: "c", HiddenModels: rules}},
	}}}
}

func TestValidateConfigRequest_HiddenModelsInvalid(t *testing.T) {
	req := hiddenReq(t, `{"workspaces":[{"working_dir":"/tmp","acp_server":"a"}],"acp_servers":[{"name":"a","command":"c","hidden_models":["ok","("]}]}`)
	err := (&Server{}).validateConfigRequest(req)
	if err == nil || err.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %v", err)
	}
	if err.Details["server"] != "a" || err.Details["rule"] != "(" || err.Details["field"] != "acp_servers[0].hidden_models[1]" {
		t.Errorf("details = %v", err.Details)
	}
}

func TestBuildNewSettings_HiddenModels(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"omitted preserves", `{"acp_servers":[{"name":"old","command":"c"}]}`, []string{"x"}},
		{"rename preserves", `{"acp_servers":[{"name":"new","command":"c"}],"server_renames":{"old":"new"}}`, []string{"x"}},
		{"explicit empty clears", `{"acp_servers":[{"name":"old","command":"c","hidden_models":[]}]}`, nil},
		{"replace drops blanks", `{"acp_servers":[{"name":"old","command":"c","hidden_models":["a"," ","b "]}]}`, []string{"a", "b "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := hiddenServer("x").buildNewSettings(hiddenReq(t, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			got := s.ACPServers[0].HiddenModels
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestValidateAndPrepareSaveConfig_InvalidHiddenModelRule_NoMutation(t *testing.T) {
	t.Setenv("MITTO_DIR", t.TempDir())
	s := hiddenServer("x")
	req := hiddenReq(t, `{"workspaces":[{"working_dir":"/tmp","acp_server":"old"}],"acp_servers":[{"name":"old","command":"c","hidden_models":["("]}]}`)
	rec := httptest.NewRecorder()
	if s.validateAndPrepareSaveConfig(rec, req) {
		t.Fatal("expected rejection")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Error.Details
	if d["server"] != "old" || d["rule"] != "(" || d["field"] != "acp_servers[0].hidden_models[0]" {
		t.Errorf("details = %v (body %s)", d, rec.Body.String())
	}
	if got := s.config.MittoConfig.ACPServers[0].HiddenModels; !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("runtime mutated: %v", got)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("MITTO_DIR"), "settings.json")); err == nil {
		t.Error("settings.json written")
	}
}

func TestHandleSaveConfig_HiddenModelsRefreshesAttachedViewAndInvalidSavePreservesIt(t *testing.T) {
	s, _ := modelShortcutServer(t)
	s.sessionManager.SetMittoConfig(s.config.MittoConfig)
	policies := s.sessionManager.HiddenModelPolicies()
	initial := policies.Snapshot()
	attached := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID: "attached", ACPServer: "test-server", MittoConfig: s.config.MittoConfig, HiddenModelPolicies: policies,
		AgentModels:    &conversation.SessionModelState{CurrentModelId: "alpha", AvailableModels: []conversation.ModelInfo{{ModelId: "alpha", Name: "Alpha"}, {ModelId: "beta", Name: "Beta"}}},
		RawAgentModels: &conversation.SessionModelState{CurrentModelId: "alpha", AvailableModels: []conversation.ModelInfo{{ModelId: "alpha", Name: "Alpha"}, {ModelId: "beta", Name: "Beta"}}},
		ConfigOptions:  []conversation.SessionConfigOption{{ID: "model", Category: conversation.ConfigOptionCategoryModel, Type: conversation.ConfigOptionTypeSelect, CurrentValue: "alpha", Options: []conversation.SessionConfigOptionValue{{Value: "alpha", Name: "Alpha"}, {Value: "beta", Name: "Beta"}}}},
	})
	s.sessionManager.AddSessionForTest(attached)
	events := &hiddenPolicyEvents{}
	s.sessionManager.SetEventsManager(events)
	w := httptest.NewRecorder()
	s.handleSaveConfig(w, modelShortcutRequest(`,"acp_servers":[{"name":"test-server","command":"test-cmd","hidden_models":["^beta$"]}]`))
	if w.Code != http.StatusOK {
		t.Fatalf("validated save status = %d, body = %s", w.Code, w.Body.String())
	}
	accepted := policies.Snapshot()
	if accepted == initial || !accepted.Policy("test-server").Hides("beta", "Beta") {
		t.Fatal("validated save did not publish the attached session policy")
	}
	assertAttachedPolicyPayload(t, attached, events, 1)
	events.broadcasts = nil
	w = httptest.NewRecorder()
	s.handleSaveConfig(w, modelShortcutRequest(`,"acp_servers":[{"name":"test-server","command":"test-cmd","hidden_models":["("]}]`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid save status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if policies.Snapshot() != accepted || !accepted.Policy("test-server").Hides("beta", "Beta") {
		t.Fatal("invalid save replaced published policy")
	}
	if len(events.broadcasts) != 0 {
		t.Fatalf("invalid save published %d attached-view updates", len(events.broadcasts))
	}
	assertAttachedPolicyPayload(t, attached, nil, 1)
}

func assertAttachedPolicyPayload(t *testing.T, attached *conversation.BackgroundSession, events *hiddenPolicyEvents, wantModels int) {
	t.Helper()
	if models := attached.AgentModels(); models == nil || len(models.AvailableModels) != wantModels || models.AvailableModels[0].ModelId != "alpha" {
		t.Fatalf("attached selectable models = %+v, want only alpha", models)
	}
	options := attached.ConfigOptions()
	if len(options) != 1 || len(options[0].Options) != wantModels || options[0].Options[0].Value != "alpha" {
		t.Fatalf("attached reconnect options = %+v, want only alpha", options)
	}
	if events == nil {
		return
	}
	if len(events.broadcasts) != 1 || events.broadcasts[0].messageType != conversation.WSMsgTypeConfigOptionChanged {
		t.Fatalf("attached updates = %+v", events.broadcasts)
	}
	payloadOptions, ok := events.broadcasts[0].payload["config_options"].([]conversation.SessionConfigOption)
	if !ok || len(payloadOptions) != 1 || len(payloadOptions[0].Options) != wantModels || payloadOptions[0].Options[0].Value != "alpha" {
		t.Fatalf("attached update payload = %+v", events.broadcasts[0].payload)
	}
}
