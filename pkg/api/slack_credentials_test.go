package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSlackInstallationCredentialKindAndGenericTokenContract(t *testing.T) {
	server := newFakeServer(t)
	server.On(http.MethodPost, "/mitto/api/slack/apps/app-1/installations").RespondJSON(http.StatusCreated,
		`{"id":"install-1","app_id":"app-1","name":"Team","credential_kind":"user","team_id":"T123","user_id":"U123","token_configured":true}`)

	installation, err := server.Client().CreateSlackInstallation("app-1", CreateSlackInstallationRequest{
		Name: "Team", TeamID: "T123", Token: "write-only-user-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if installation.CredentialKind != "user" || installation.UserID != "U123" || installation.BotID != "" {
		t.Fatalf("installation = %#v", installation)
	}
	var body map[string]any
	if err := json.Unmarshal(server.LastRequest().Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["token"] != "write-only-user-token" {
		t.Fatalf("request body = %#v", body)
	}
	if _, exists := body["bot_token"]; exists {
		t.Fatalf("generic request unexpectedly included bot_token: %#v", body)
	}
}
