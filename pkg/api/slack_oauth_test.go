package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSlackOAuthSDKContractsAreValueFreeAndEscaped(t *testing.T) {
	server := newFakeServer(t)
	server.On(http.MethodGet, "/mitto/api/slack/oauth/config").RespondJSON(http.StatusOK,
		`{"available":true,"redirect_uri":"https://mitto.example/mitto/api/slack/oauth/callback"}`)
	server.On(http.MethodPut, "/mitto/api/slack/apps/app 1/oauth-client").RespondJSON(http.StatusOK,
		`{"id":"app 1","oauth_client_id":"123.456","oauth_client_secret_configured":true}`)
	server.On(http.MethodPost, "/mitto/api/slack/apps/app 1/oauth/start").RespondJSON(http.StatusOK,
		`{"flow_id":"flow-create","authorization_url":"https://slack.example/authorize","expires_at":"2026-08-19T20:10:00Z"}`)
	server.On(http.MethodPost, "/mitto/api/slack/installations/inst 1/oauth/start").RespondJSON(http.StatusOK,
		`{"flow_id":"flow-replace","authorization_url":"https://slack.example/authorize","expires_at":"2026-08-19T20:10:00Z"}`)
	server.On(http.MethodGet, "/mitto/api/slack/oauth/flows/flow 1").RespondJSON(http.StatusOK,
		`{"flow_id":"flow 1","status":"failed","error":"expired","message":"Start again.","expires_at":"2026-08-19T20:10:00Z"}`)

	client := server.Client()
	config, err := client.GetSlackOAuthConfig()
	if err != nil || !config.Available || !strings.HasSuffix(config.RedirectURI, "/api/slack/oauth/callback") {
		t.Fatalf("GetSlackOAuthConfig() = %#v, %v", config, err)
	}
	app, err := client.ConfigureSlackOAuthClient("app 1", ConfigureSlackOAuthClientRequest{
		ClientID: "123.456", ClientSecret: "write-only-client-secret",
	})
	if err != nil || !app.OAuthClientSecretConfigured || app.OAuthClientID != "123.456" {
		t.Fatalf("ConfigureSlackOAuthClient() = %#v, %v", app, err)
	}
	if _, err = client.StartSlackOAuthInstallation("app 1", StartSlackOAuthRequest{Name: "Workspace", TeamID: "T123"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.StartSlackOAuthReplacement("inst 1"); err != nil {
		t.Fatal(err)
	}
	status, err := client.GetSlackOAuthFlowStatus("flow 1")
	if err != nil || status.Error != "expired" || status.Status != "failed" {
		t.Fatalf("GetSlackOAuthFlowStatus() = %#v, %v", status, err)
	}

	requests := server.Requests()
	if len(requests) != 5 {
		t.Fatalf("requests = %d, want 5", len(requests))
	}
	var configured map[string]string
	if err := json.Unmarshal(requests[1].Body, &configured); err != nil {
		t.Fatal(err)
	}
	if configured["client_id"] != "123.456" || configured["client_secret"] != "write-only-client-secret" {
		t.Fatalf("OAuth client request = %#v", configured)
	}
	if string(requests[2].Body) != `{"name":"Workspace","team_id":"T123"}` || string(requests[3].Body) != `{}` {
		t.Fatalf("OAuth start bodies = %q, %q", requests[2].Body, requests[3].Body)
	}
	for _, response := range []any{config, app, status} {
		encoded, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(encoded), "write-only-client-secret") {
			t.Fatalf("SDK response exposed client secret: %s", encoded)
		}
	}
}
