package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestHandleSessionLoopOnSlackRoundTripAndPatch(t *testing.T) {
	store, h := newLoopStore(t)
	const sid = "test-onslack-roundtrip"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got := putLoopForTest(t, h, sid, LoopPromptRequest{
		Prompt: "inspect", Enabled: true, Triggers: []session.LoopTrigger{session.TriggerOnSlack},
		SlackSubscriptions: []session.SlackSubscription{
			{InstallationID: "z", ChannelID: "c2", EventMode: session.SlackEventModeAppMention},
			{InstallationID: "a", ChannelID: "c1"},
		},
	})
	// SA1019: intentionally pin the legacy singular Trigger compatibility field.
	//nolint:staticcheck
	if got.Trigger != session.TriggerOnSlack || len(got.SlackSubscriptions) != 2 || got.SlackSubscriptions[0].InstallationID != "a" {
		t.Fatalf("PUT response = triggers %v, subscriptions %#v", got.Triggers, got.SlackSubscriptions)
	}
	if got.SlackSubscriptions[0].EventMode != session.SlackEventModeAnyHumanMessage || got.SlackSubscriptions[0].ThreadPolicy != session.SlackThreadPolicyAny {
		t.Errorf("PUT did not apply safe defaults: %#v", got.SlackSubscriptions[0])
	}

	replacement := []session.SlackSubscription{{InstallationID: "b", ChannelID: "c3", ThreadPolicy: session.SlackThreadPolicyRootOnly}}
	raw, _ := json.Marshal(LoopPromptPatchRequest{SlackSubscriptions: &replacement})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", w.Code, w.Body.String())
	}
	stored, _ := store.Loop(sid).Get()
	if len(stored.SlackSubscriptions) != 1 || stored.SlackSubscriptions[0].InstallationID != "b" || stored.SlackSubscriptions[0].EventMode != session.SlackEventModeAnyHumanMessage {
		t.Errorf("PATCH replacement = %#v", stored.SlackSubscriptions)
	}

	disabled := false
	empty := []session.SlackSubscription{}
	raw, _ = json.Marshal(LoopPromptPatchRequest{Enabled: &disabled, SlackSubscriptions: &empty})
	req = httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/loop", bytes.NewReader(raw))
	w = httptest.NewRecorder()
	h.HandleSessionLoop(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH clear status = %d, body=%s", w.Code, w.Body.String())
	}
	stored, _ = store.Loop(sid).Get()
	if len(stored.SlackSubscriptions) != 0 || stored.Enabled {
		t.Errorf("PATCH clear result = enabled %v, subscriptions %#v", stored.Enabled, stored.SlackSubscriptions)
	}
}

func TestHandleSessionLoopOnSlackValidationErrorsAreBadRequests(t *testing.T) {
	store, h := newLoopStore(t)
	const sid = "test-onslack-invalid"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	cases := []LoopPromptRequest{
		{Prompt: "inspect", Enabled: true, Triggers: []session.LoopTrigger{session.TriggerOnSlack}},
		{Prompt: "inspect", Enabled: true, Triggers: []session.LoopTrigger{session.TriggerOnSlack}, SlackSubscriptions: []session.SlackSubscription{{InstallationID: "i", ChannelID: "c", EventMode: "bots"}}},
	}
	for i, body := range cases {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sid+"/loop", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		h.HandleSessionLoop(w, req, sid, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d status = %d, want 400; body=%s", i, w.Code, w.Body.String())
		}
	}
}
