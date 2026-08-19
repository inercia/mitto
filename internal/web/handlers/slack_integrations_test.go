package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/slackbridge"
	"github.com/inercia/mitto/internal/slackcatalog"
	"github.com/inercia/mitto/internal/web/middleware"
)

type handlerCredentials struct {
	values map[secrets.CredentialRef]string
}

func (c *handlerCredentials) Put(ref secrets.CredentialRef, value string) error {
	c.values[ref] = value
	return nil
}
func (c *handlerCredentials) Resolve(ref secrets.CredentialRef) (string, error) {
	value, ok := c.values[ref]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return value, nil
}
func (c *handlerCredentials) Status(ref secrets.CredentialRef) (secrets.CredentialStatus, error) {
	_, ok := c.values[ref]
	return secrets.CredentialStatus{Configured: ok}, nil
}
func (c *handlerCredentials) Delete(ref secrets.CredentialRef) error {
	if _, ok := c.values[ref]; !ok {
		return secrets.ErrNotFound
	}
	delete(c.values, ref)
	return nil
}

type handlerSlack struct{}

func (handlerSlack) ValidateApp(_ context.Context, token string) (string, error) {
	if token != "write-only-token" {
		return "", slackcatalog.ErrUnavailable
	}
	return "A123", nil
}
func (handlerSlack) ValidateInstallation(context.Context, string) (slackcatalog.InstallationIdentity, error) {
	return slackcatalog.InstallationIdentity{}, slackcatalog.ErrUnavailable
}
func (handlerSlack) ListChannels(context.Context, string, string, int) (slackcatalog.ChannelPage, error) {
	return slackcatalog.ChannelPage{}, nil
}

type handlerUserSlack struct{}

func (handlerUserSlack) ValidateApp(context.Context, string) (string, error) { return "A123", nil }
func (handlerUserSlack) ValidateInstallation(_ context.Context, token string) (slackcatalog.InstallationIdentity, error) {
	if token != "write-only-user-token" {
		return slackcatalog.InstallationIdentity{}, slackcatalog.ErrInvalid
	}
	return slackcatalog.InstallationIdentity{CredentialKind: slackcatalog.CredentialKindUser,
		SlackAppID: "A123", TeamID: "T123", TeamName: "Example", UserID: "U123"}, nil
}
func (handlerUserSlack) ListChannels(context.Context, string, string, int) (slackcatalog.ChannelPage, error) {
	return slackcatalog.ChannelPage{}, nil
}

func newSlackHandlers(t *testing.T) *Handlers {
	t.Helper()
	service := slackcatalog.NewService(
		slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		&handlerCredentials{values: make(map[secrets.CredentialRef]string)},
		handlerSlack{}, nil,
	)
	return New(Deps{SlackCatalog: service})
}

func newLocalSlackRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Host = "127.0.0.1"
	return request
}

func TestSlackInstallationCreateRequestCredentialCompatibility(t *testing.T) {
	for _, test := range []struct {
		name    string
		request slackInstallationCreateRequest
		want    string
		wantErr bool
	}{
		{"generic", slackInstallationCreateRequest{Token: "user-token"}, "user-token", false},
		{"legacy", slackInstallationCreateRequest{BotToken: "bot-token"}, "bot-token", false},
		{"matching", slackInstallationCreateRequest{Token: "same", BotToken: "same"}, "same", false},
		{"conflicting", slackInstallationCreateRequest{Token: "one", BotToken: "two"}, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.request.credential()
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("credential() = %q, %v", got, err)
			}
		})
	}
}

func TestSlackInstallationRESTReturnsOnlyDelegatedUserMetadata(t *testing.T) {
	credentials := &handlerCredentials{values: make(map[secrets.CredentialRef]string)}
	service := slackcatalog.NewService(slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		credentials, handlerUserSlack{}, nil)
	app, err := service.CreateApp(context.Background(), "App", "write-only-app-token")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{SlackCatalog: service})
	request := newLocalSlackRequest(http.MethodPost, "/api/slack/apps/"+app.ID+"/installations",
		`{"name":"Team","team_id":"T123","token":"write-only-user-token"}`)
	request.SetPathValue("appId", app.ID)
	response := httptest.NewRecorder()
	h.HandleSlackInstallationCreate(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["credential_kind"] != "user" || body["user_id"] != "U123" || body["team_id"] != "T123" {
		t.Fatalf("response metadata = %#v", body)
	}
	for _, forbidden := range []string{"token", "bot_token", "bot_id", "bot_user_id"} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("response exposed %q: %#v", forbidden, body)
		}
	}
	if strings.Contains(response.Body.String(), "write-only-user-token") {
		t.Fatalf("response leaked credential: %s", response.Body.String())
	}
}

type trackingSlackBody struct {
	reader *strings.Reader
	read   bool
}

func newTrackingSlackBody(value string) *trackingSlackBody {
	return &trackingSlackBody{reader: strings.NewReader(value)}
}

func (b *trackingSlackBody) Read(p []byte) (int, error) {
	b.read = true
	return b.reader.Read(p)
}

func (*trackingSlackBody) Close() error { return nil }

var _ io.ReadCloser = (*trackingSlackBody)(nil)

func TestSlackHandlersCreateListAndNeverReturnToken(t *testing.T) {
	h := newSlackHandlers(t)
	request := newLocalSlackRequest(http.MethodPost, "/api/slack/apps",
		`{"name":"Production","app_token":"write-only-token"}`)
	response := httptest.NewRecorder()
	h.HandleSlackAppCreate(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "write-only-token") || !strings.Contains(response.Body.String(), `"token_configured":true`) {
		t.Fatalf("unsafe create response: %s", response.Body.String())
	}

	response = httptest.NewRecorder()
	h.HandleSlackAppsList(response, httptest.NewRequest(http.MethodGet, "/api/slack/apps", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "write-only-token") {
		t.Fatalf("unsafe list response: status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"slack_app_id":"A123"`) {
		t.Fatalf("list response missing derived identity: %s", response.Body.String())
	}
}

func TestSlackHandlersCanonicalErrors(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		h := New(Deps{})
		response := httptest.NewRecorder()
		h.HandleSlackAppsList(response, httptest.NewRequest(http.MethodGet, "/api/slack/apps", nil))
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" {
			t.Fatalf("status=%d retry=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"code":"unavailable"`) {
			t.Fatalf("body=%s", response.Body.String())
		}
	})

	t.Run("bad request", func(t *testing.T) {
		h := newSlackHandlers(t)
		response := httptest.NewRecorder()
		h.HandleSlackAppCreate(response, newLocalSlackRequest(http.MethodPost, "/api/slack/apps", `{"name":`))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("conflict", func(t *testing.T) {
		h := newSlackHandlers(t)
		body := `{"name":"One","app_token":"write-only-token"}`
		first := httptest.NewRecorder()
		h.HandleSlackAppCreate(first, newLocalSlackRequest(http.MethodPost, "/api/slack/apps", body))
		second := httptest.NewRecorder()
		h.HandleSlackAppCreate(second, newLocalSlackRequest(http.MethodPost, "/api/slack/apps", body))
		if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), `"code":"conflict"`) {
			t.Fatalf("status=%d body=%s", second.Code, second.Body.String())
		}
	})

	t.Run("channel limit", func(t *testing.T) {
		h := newSlackHandlers(t)
		request := httptest.NewRequest(http.MethodGet, "/api/slack/installations/id/channels?limit=bad", nil)
		response := httptest.NewRecorder()
		h.HandleSlackInstallationChannels(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestSlackCredentialWritesRejectExternalRequestsBeforeReadingBody(t *testing.T) {
	h := newSlackHandlers(t)
	tests := []struct {
		name   string
		method string
		path   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{"create app", http.MethodPost, "/api/slack/apps", h.HandleSlackAppCreate},
		{"replace app token", http.MethodPut, "/api/slack/apps/app/token", h.HandleSlackAppToken},
		{"create installation", http.MethodPost, "/api/slack/apps/app/installations", h.HandleSlackInstallationCreate},
		{"replace installation token", http.MethodPut, "/api/slack/installations/install/token", h.HandleSlackInstallationToken},
		{"import environment", http.MethodPost, "/api/slack/environment-import", h.HandleSlackEnvironmentImport},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := newTrackingSlackBody(`{"token":"canary-slack-token"}`)
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Host = "127.0.0.1"
			request.Body = body
			request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeyExternalConnection, true))
			response := httptest.NewRecorder()

			test.handle(response, request)

			if response.Code != http.StatusForbidden || body.read {
				t.Fatalf("status=%d body_read=%v response=%s", response.Code, body.read, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "canary-slack-token") || !strings.Contains(response.Body.String(), `"code":"forbidden"`) {
				t.Fatalf("unsafe external response: %s", response.Body.String())
			}
		})
	}
}

func TestSlackEnvironmentStatusIsValueFree(t *testing.T) {
	const canary = "xapp-secret-never-return"
	migration := slackbridge.NewEnvironmentMigration(
		slackbridge.Config{AppToken: canary, BotToken: "xoxb-secret-never-return"},
		slackbridge.EnvironmentStatus{Present: true, Complete: false, MissingVariables: []string{slackbridge.EnvTeamID}},
		nil, nil, nil, nil,
	)
	h := New(Deps{SlackEnvironment: migration})
	response := httptest.NewRecorder()
	h.HandleSlackEnvironmentStatus(response, httptest.NewRequest(http.MethodGet, "/api/slack/environment-import", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), slackbridge.EnvTeamID) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), canary) || strings.Contains(response.Body.String(), "xoxb-secret") {
		t.Fatalf("status leaked environment credential: %s", response.Body.String())
	}
}

func TestSlackCredentialWriteRejectsAuthenticatedCSRFAuthorizedExternalRequest(t *testing.T) {
	const canary = "canary-authenticated-external-token"
	h := newSlackHandlers(t)
	auth := middleware.NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer auth.Close()
	session, err := auth.CreateSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	csrf := middleware.NewCSRFManager()
	defer csrf.Close()
	csrfToken, err := csrf.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)

	body := newTrackingSlackBody(`{"name":"Production","app_token":"` + canary + `"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/slack/apps", nil)
	request.Host = "127.0.0.1"
	request.RemoteAddr = "203.0.113.12:12345"
	request.Body = body
	request.AddCookie(&http.Cookie{Name: "mitto_session", Value: session.Token})
	request.AddCookie(&http.Cookie{Name: "mitto_csrf", Value: csrfToken})
	request.Header.Set("X-CSRF-Token", csrfToken)
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeyExternalConnection, true))
	response := httptest.NewRecorder()

	csrf.CSRFMiddleware(auth.AuthMiddleware(http.HandlerFunc(h.HandleSlackAppCreate))).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || body.read {
		t.Fatalf("status=%d body_read=%v response=%s", response.Code, body.read, response.Body.String())
	}
	if strings.Contains(response.Body.String(), canary) {
		t.Fatalf("external response leaked canary: %s", response.Body.String())
	}
	if strings.Contains(logs.String(), canary) {
		t.Fatalf("external write logged canary: %s", logs.String())
	}
}

func TestSlackCredentialWritesRejectNonLocalhostBeforeReadingBody(t *testing.T) {
	h := newSlackHandlers(t)
	body := newTrackingSlackBody(`{"app_token":"canary-slack-token"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/slack/apps", nil)
	request.Host = "mitto.example"
	request.Body = body
	response := httptest.NewRecorder()

	h.HandleSlackAppCreate(response, request)

	if response.Code != http.StatusForbidden || body.read {
		t.Fatalf("status=%d body_read=%v response=%s", response.Code, body.read, response.Body.String())
	}
}

func TestSlackCredentialBodyErrorsAreBoundedAndValueFree(t *testing.T) {
	const canary = "canary-slack-secret-never-echo"
	h := newSlackHandlers(t)
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"malformed", `{"name":"Production","app_token":"` + canary, http.StatusBadRequest, "bad_request"},
		{"trailing value", `{"name":"Production","app_token":"write-only-token"} ` + canary, http.StatusBadRequest, "bad_request"},
		{"oversized", `{"name":"Production","app_token":"` + strings.Repeat("x", slackRequestBodyLimit) + canary + `"}`, http.StatusRequestEntityTooLarge, "too_large"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.HandleSlackAppCreate(response, newLocalSlackRequest(http.MethodPost, "/api/slack/apps", test.body))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), canary) {
				t.Fatalf("decoder response leaked canary: %s", response.Body.String())
			}
		})
	}
}

func TestWriteSlackErrorMapsReferencesToConflict(t *testing.T) {
	response := httptest.NewRecorder()
	writeSlackError(response, errors.Join(slackcatalog.ErrReferenced, errors.New("in use")))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"conflict"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWriteSlackErrorNeverEchoesWrappedValues(t *testing.T) {
	const canary = "canary-provider-error-secret"
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"invalid", errors.Join(slackcatalog.ErrInvalid, errors.New(canary)), http.StatusBadRequest},
		{"not found", errors.Join(slackcatalog.ErrNotFound, errors.New(canary)), http.StatusNotFound},
		{"referenced", errors.Join(slackcatalog.ErrReferenced, errors.New(canary)), http.StatusConflict},
		{"conflict", errors.Join(slackcatalog.ErrConflict, errors.New(canary)), http.StatusConflict},
		{"unavailable", errors.Join(slackcatalog.ErrUnavailable, errors.New(canary)), http.StatusServiceUnavailable},
		{"unexpected", errors.New(canary), http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeSlackError(response, test.err)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), canary) {
				t.Fatalf("public error leaked wrapped value: %s", response.Body.String())
			}
		})
	}
}
