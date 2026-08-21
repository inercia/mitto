package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/slackcatalog"
	"github.com/inercia/mitto/internal/web/middleware"
)

type handlerOAuthSlack struct {
	exchanges int
}

func (*handlerOAuthSlack) ValidateApp(context.Context, string) (string, error) { return "A123", nil }
func (*handlerOAuthSlack) ValidateInstallation(context.Context, string) (slackcatalog.InstallationIdentity, error) {
	return slackcatalog.InstallationIdentity{}, slackcatalog.ErrOAuthRequired
}
func (*handlerOAuthSlack) ListChannels(context.Context, string, string, int) (slackcatalog.ChannelPage, error) {
	return slackcatalog.ChannelPage{}, nil
}
func (s *handlerOAuthSlack) ExchangeOAuth(_ context.Context, clientID, secret, code, redirectURI string) (slackcatalog.OAuthIdentity, error) {
	s.exchanges++
	if clientID != "123.456" || secret != "write-only-client-secret" || code != "write-only-code" ||
		redirectURI != "https://mitto.example/mitto/api/slack/oauth/callback" {
		return slackcatalog.OAuthIdentity{}, slackcatalog.ErrInvalid
	}
	return slackcatalog.OAuthIdentity{InstallationIdentity: slackcatalog.InstallationIdentity{
		CredentialKind: slackcatalog.CredentialKindUser, SlackAppID: "A123", TeamID: "T123", TeamName: "Example", UserID: "U123",
	}, AccessToken: "write-only-user-token"}, nil
}
func (*handlerOAuthSlack) RevalidateOAuthInstallation(context.Context, string, string) (slackcatalog.InstallationIdentity, error) {
	return slackcatalog.InstallationIdentity{}, slackcatalog.ErrInvalid
}

func newHandlerOAuthFixture(t *testing.T) (*Handlers, *slackcatalog.Service, *handlerCredentials, *handlerOAuthSlack, string) {
	t.Helper()
	credentials := &handlerCredentials{values: make(map[secrets.CredentialRef]string)}
	provider := &handlerOAuthSlack{}
	service := slackcatalog.NewService(slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")), credentials, provider, nil)
	app, err := service.CreateApp(context.Background(), "App", "write-only-app-token")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Web.Hooks.ExternalAddress = "https://mitto.example"
	return New(Deps{SlackCatalog: service, MittoConfig: cfg, APIPrefix: "/mitto"}), service, credentials, provider, app.ID
}

func TestSlackOAuthHandlersCompleteValueFreeFlow(t *testing.T) {
	h, _, credentials, provider, appID := newHandlerOAuthFixture(t)

	configResponse := httptest.NewRecorder()
	h.HandleSlackOAuthConfig(configResponse, httptest.NewRequest(http.MethodGet, "/api/slack/oauth/config", nil))
	if configResponse.Code != http.StatusOK || !strings.Contains(configResponse.Body.String(), "https://mitto.example/mitto/api/slack/oauth/callback") {
		t.Fatalf("OAuth config status=%d body=%s", configResponse.Code, configResponse.Body.String())
	}

	configure := newLocalSlackRequest(http.MethodPut, "/api/slack/apps/"+appID+"/oauth-client",
		`{"client_id":"123.456","client_secret":"write-only-client-secret"}`)
	configure.SetPathValue("appId", appID)
	configureResponse := httptest.NewRecorder()
	h.HandleSlackOAuthClient(configureResponse, configure)
	if configureResponse.Code != http.StatusOK || strings.Contains(configureResponse.Body.String(), "write-only-client-secret") {
		t.Fatalf("OAuth client status=%d body=%s", configureResponse.Code, configureResponse.Body.String())
	}

	startRequest := newLocalSlackRequest(http.MethodPost, "/api/slack/apps/"+appID+"/oauth/start",
		`{"name":"Workspace","team_id":"T123"}`)
	startRequest.SetPathValue("appId", appID)
	startResponse := httptest.NewRecorder()
	h.HandleSlackOAuthCreateStart(startResponse, startRequest)
	var start slackcatalog.OAuthStart
	if err := json.Unmarshal(startResponse.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	if startResponse.Code != http.StatusOK || start.FlowID == "" || state == "" {
		t.Fatalf("OAuth start status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}

	callback := httptest.NewRequest(http.MethodGet, "/mitto/api/slack/oauth/callback?state="+url.QueryEscape(state)+"&code=write-only-code", nil)
	callbackResponse := httptest.NewRecorder()
	h.HandleSlackOAuthCallback(callbackResponse, callback)
	if callbackResponse.Code != http.StatusOK || !strings.Contains(callbackResponse.Body.String(), "authorization complete") {
		t.Fatalf("OAuth callback status=%d body=%s", callbackResponse.Code, callbackResponse.Body.String())
	}
	for _, secret := range []string{state, "write-only-code", "write-only-client-secret", "write-only-user-token"} {
		if strings.Contains(callbackResponse.Body.String(), secret) {
			t.Fatalf("callback exposed sensitive value %q", secret)
		}
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/slack/oauth/flows/"+start.FlowID, nil)
	statusRequest.SetPathValue("flowId", start.FlowID)
	statusResponse := httptest.NewRecorder()
	h.HandleSlackOAuthStatus(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("OAuth status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	for _, secret := range []string{state, "write-only-code", "write-only-client-secret", "write-only-user-token"} {
		if strings.Contains(statusResponse.Body.String(), secret) {
			t.Fatalf("status exposed sensitive value %q", secret)
		}
	}
	if provider.exchanges != 1 || len(credentials.values) != 3 {
		t.Fatalf("exchanges=%d stored credentials=%d", provider.exchanges, len(credentials.values))
	}
}

type unreadOAuthBody struct{ read bool }

func (b *unreadOAuthBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*unreadOAuthBody) Close() error               { return nil }

func TestSlackOAuthCredentialWritesRejectExternalBeforeReadingBody(t *testing.T) {
	h, _, _, _, appID := newHandlerOAuthFixture(t)
	body := &unreadOAuthBody{}
	request := httptest.NewRequest(http.MethodPut, "/api/slack/apps/"+appID+"/oauth-client", nil)
	request.Body = body
	request.Host = "mitto.example"
	request.SetPathValue("appId", appID)
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeyExternalConnection, true))
	response := httptest.NewRecorder()
	h.HandleSlackOAuthClient(response, request)
	if response.Code != http.StatusForbidden || body.read {
		t.Fatalf("status=%d body_read=%v response=%s", response.Code, body.read, response.Body.String())
	}
}

func TestSlackOAuthConfigRequiresHTTPSExternalAddress(t *testing.T) {
	for _, address := range []string{"", "http://mitto.example", "https://user@mitto.example", "https://mitto.example?token=value"} {
		cfg := &config.Config{}
		cfg.Web.Hooks.ExternalAddress = address
		h := New(Deps{MittoConfig: cfg, APIPrefix: "/mitto"})
		response := httptest.NewRecorder()
		h.HandleSlackOAuthConfig(response, httptest.NewRequest(http.MethodGet, "/api/slack/oauth/config", nil))
		if strings.Contains(response.Body.String(), `"available":true`) || strings.Contains(response.Body.String(), "token=value") {
			t.Fatalf("address %q unexpectedly available: %s", address, response.Body.String())
		}
	}
}
