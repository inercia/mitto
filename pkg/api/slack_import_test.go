package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSlackEnvironmentImportSDKContract(t *testing.T) {
	server := newFakeServer(t)
	server.On(http.MethodGet, "/mitto/api/slack/environment-import").RespondJSON(http.StatusOK,
		`{"present":true,"complete":true,"team_id":"T123","channel_id":"C123","target_session_id":"session-1","active":true,"shadowed":false}`)
	server.On(http.MethodPost, "/mitto/api/slack/environment-import").RespondJSON(http.StatusOK,
		`{"app_id":"app-1","installation_id":"inst-1","app_created":false,"installation_created":false,"subscription_created":true,"environment_stopped":true,"managed_active":true}`)
	client := server.Client()
	status, err := client.GetSlackEnvironmentStatus()
	if err != nil || !status.Present || status.TeamID != "T123" || status.Shadowed {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	result, err := client.ImportSlackPoC(ImportSlackPoCRequest{AppID: "app-1", InstallationID: "inst-1"})
	if err != nil || result.AppID != "app-1" || !result.SubscriptionCreated || !result.EnvironmentStopped {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	requests := server.Requests()
	if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPost {
		t.Fatalf("requests=%#v", requests)
	}
	var body map[string]any
	if err := json.Unmarshal(requests[1].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["app_id"] != "app-1" || body["installation_id"] != "inst-1" {
		t.Fatalf("body=%#v", body)
	}
	for key := range body {
		if strings.Contains(key, "token") {
			t.Fatalf("request unexpectedly contains credential field %q", key)
		}
	}
}
