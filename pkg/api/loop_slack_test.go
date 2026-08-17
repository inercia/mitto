package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestClientSetLoopOnSlackRoundTrip(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPut, "/mitto/api/sessions/sess-slack/loop").RespondJSON(http.StatusOK,
		`{"prompt":"inspect","enabled":true,"trigger":"onSlack","triggers":["onSlack"],"slack_subscriptions":[{"installation_id":"install-1","channel_id":"channel-1","event_mode":"anyHumanMessage","thread_policy":"any"}]}`)

	cfg, err := f.Client().SetLoop("sess-slack", SetLoopRequest{
		Prompt: "inspect", Enabled: true, Triggers: []string{"onSlack"},
		SlackSubscriptions: []SlackSubscription{{InstallationID: "install-1", ChannelID: "channel-1"}},
	})
	if err != nil {
		t.Fatalf("SetLoop() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(f.LastRequest().Body, &body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	subs, _ := body["slack_subscriptions"].([]any)
	if len(subs) != 1 {
		t.Fatalf("request slack_subscriptions = %#v", body["slack_subscriptions"])
	}
	if cfg.Trigger != "onSlack" || len(cfg.SlackSubscriptions) != 1 || cfg.SlackSubscriptions[0].InstallationID != "install-1" {
		t.Errorf("LoopConfig = %#v", cfg)
	}
}

func TestClientPatchLoopOnSlackExplicitEmptyList(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPatch, "/mitto/api/sessions/sess-slack/loop").RespondJSON(http.StatusOK,
		`{"prompt":"inspect","enabled":false,"trigger":"onSlack","triggers":["onSlack"]}`)
	empty := []SlackSubscription{}
	if _, err := f.Client().PatchLoop("sess-slack", LoopPatchRequest{SlackSubscriptions: &empty}); err != nil {
		t.Fatalf("PatchLoop() error = %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(f.LastRequest().Body, &body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	raw, ok := body["slack_subscriptions"]
	if !ok || string(raw) != "[]" {
		t.Errorf("slack_subscriptions JSON = %s (present=%v), want []", raw, ok)
	}
}
