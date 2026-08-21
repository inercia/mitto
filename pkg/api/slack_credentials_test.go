package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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

func TestCreateSlackInstallationSurfacesOAuthRequiredSafeMessage(t *testing.T) {
	const canary = "write-only-user-token-missing-app-id"
	server := newFakeServer(t)
	server.On(http.MethodPost, "/mitto/api/slack/apps/app-1/installations").RespondJSON(http.StatusConflict,
		`{"error":{"code":"conflict","message":"Slack did not return the app identity needed to safely bind this delegated-user credential. Manual delegated-user setup is unavailable until Slack OAuth provenance is supported; use a bot token instead."}}`)

	installation, err := server.Client().CreateSlackInstallation("app-1", CreateSlackInstallationRequest{
		Name: "Team", TeamID: "T123", Token: canary,
	})
	if installation != nil {
		t.Fatalf("installation = %#v, want nil on rejection", installation)
	}
	if err == nil {
		t.Fatal("CreateSlackInstallation() succeeded, want an error")
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want it to satisfy errors.Is(err, ErrConflict)", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != "conflict" {
		t.Fatalf("apiErr = %#v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "OAuth") || !strings.Contains(apiErr.Message, "delegated-user") {
		t.Fatalf("apiErr.Message = %q, want an actionable OAuth/delegated-user explanation", apiErr.Message)
	}
	if strings.Contains(apiErr.Error(), canary) || strings.Contains(apiErr.Message, canary) {
		t.Fatalf("apiErr leaked candidate credential: %v", apiErr)
	}
}

func TestRemoveSlackAppReferences(t *testing.T) {
	server := newFakeServer(t)
	server.On(http.MethodDelete, "/mitto/api/slack/apps/app-1/references").RespondJSON(http.StatusOK,
		`{"removed":[{"session_id":"session-1","name":"Watcher"}],"preview":{"installation_ids":["install-1"],"references":[]}}`)

	result, err := server.Client().RemoveSlackAppReferences("app-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || result.Removed[0].Name != "Watcher" || len(result.Preview.References) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRemoveSlackInstallationReferences(t *testing.T) {
	server := newFakeServer(t)
	server.On(http.MethodDelete, "/mitto/api/slack/installations/install-1/references").RespondJSON(http.StatusOK,
		`{"removed":[{"session_id":"session-1","name":"Watcher"}],"preview":{"installation_ids":["install-1"],"references":[]}}`)

	result, err := server.Client().RemoveSlackInstallationReferences("install-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || result.Removed[0].SessionID != "session-1" || len(result.Preview.References) != 0 {
		t.Fatalf("result = %#v", result)
	}
}
