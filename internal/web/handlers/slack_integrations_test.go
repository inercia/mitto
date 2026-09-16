package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/session"
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

type handlerReferences struct {
	refs []slackcatalog.Reference

	// removeErr and partialRemoved simulate a remover that fails partway
	// through a batch: some sessions were already mutated (partialRemoved)
	// before the failure. Zero-valued, RemoveSlackReferences succeeds and
	// removes every ref (unchanged prior behavior).
	removeErr      error
	partialRemoved []slackcatalog.Reference
}

func (r *handlerReferences) FindSlackReferences(context.Context, string, []string) ([]slackcatalog.Reference, error) {
	return append([]slackcatalog.Reference(nil), r.refs...), nil
}

func (r *handlerReferences) RemoveSlackReferences(context.Context, string, []string) ([]slackcatalog.Reference, error) {
	if r.removeErr != nil {
		removed := append([]slackcatalog.Reference(nil), r.partialRemoved...)
		r.refs = nil
		return removed, r.removeErr
	}
	removed := append([]slackcatalog.Reference(nil), r.refs...)
	r.refs = nil
	return removed, nil
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

// handlerOAuthRequiredSlack models the production recurrence: a bot token
// validates normally, but any other token reproduces a standard Slack
// user-token auth.test response that omits app_id, which SlackClient
// classifies as slackcatalog.ErrOAuthRequired rather than a generic conflict.
type handlerOAuthRequiredSlack struct{}

func (handlerOAuthRequiredSlack) ValidateApp(context.Context, string) (string, error) {
	return "A123", nil
}
func (handlerOAuthRequiredSlack) ValidateInstallation(_ context.Context, token string) (slackcatalog.InstallationIdentity, error) {
	if token == "write-only-bot-token" {
		return slackcatalog.InstallationIdentity{CredentialKind: slackcatalog.CredentialKindBot,
			SlackAppID: "A123", TeamID: "T123", TeamName: "Example", BotID: "B123", BotUserID: "U123"}, nil
	}
	return slackcatalog.InstallationIdentity{}, slackcatalog.ErrOAuthRequired
}
func (handlerOAuthRequiredSlack) ListChannels(context.Context, string, string, int) (slackcatalog.ChannelPage, error) {
	return slackcatalog.ChannelPage{}, nil
}

type handlerChannelRequest struct {
	token  string
	cursor string
	limit  int
}

type handlerChannelSlack struct {
	identities map[string]slackcatalog.InstallationIdentity
	pages      map[string]slackcatalog.ChannelPage
	requests   []handlerChannelRequest
}

func (*handlerChannelSlack) ValidateApp(context.Context, string) (string, error) { return "A123", nil }
func (s *handlerChannelSlack) ValidateInstallation(_ context.Context, token string) (slackcatalog.InstallationIdentity, error) {
	identity, ok := s.identities[token]
	if !ok {
		return slackcatalog.InstallationIdentity{}, slackcatalog.ErrInvalid
	}
	return identity, nil
}
func (s *handlerChannelSlack) ListChannels(_ context.Context, token, cursor string, limit int) (slackcatalog.ChannelPage, error) {
	s.requests = append(s.requests, handlerChannelRequest{token: token, cursor: cursor, limit: limit})
	return s.pages[token], nil
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

func TestSlackInstallationCreateAndReplaceRejectOAuthRequiredWithoutMutation(t *testing.T) {
	const canary = "write-only-user-token-missing-app-id"
	credentials := &handlerCredentials{values: make(map[secrets.CredentialRef]string)}
	service := slackcatalog.NewService(slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		credentials, handlerOAuthRequiredSlack{}, nil)
	app, err := service.CreateApp(context.Background(), "App", "write-only-app-token")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{SlackCatalog: service})

	createRequest := newLocalSlackRequest(http.MethodPost, "/api/slack/apps/"+app.ID+"/installations",
		`{"name":"Team","team_id":"T123","token":"`+canary+`"}`)
	createRequest.SetPathValue("appId", app.ID)
	response := httptest.NewRecorder()
	h.HandleSlackInstallationCreate(response, createRequest)

	if response.Code != http.StatusConflict {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "conflict" {
		t.Fatalf("error envelope = %#v, body=%s", errObj, response.Body.String())
	}
	message, _ := errObj["message"].(string)
	if !strings.Contains(message, "OAuth") || !strings.Contains(message, "delegated-user") {
		t.Fatalf("message = %q, want an actionable OAuth/delegated-user explanation", message)
	}
	if strings.Contains(response.Body.String(), canary) {
		t.Fatalf("response leaked candidate credential: %s", response.Body.String())
	}

	// Create makes no catalog/vault mutation on rejection.
	installations, err := service.ListInstallations(app.ID)
	if err != nil || len(installations) != 0 {
		t.Fatalf("rejected create mutated catalog: installations=%#v err=%v", installations, err)
	}
	if len(credentials.values) != 1 { // only the app token from CreateApp above
		t.Fatalf("rejected create mutated vault: %d credentials stored", len(credentials.values))
	}

	// Compatibility: bot create still succeeds after the rejected attempt.
	botRequest := newLocalSlackRequest(http.MethodPost, "/api/slack/apps/"+app.ID+"/installations",
		`{"name":"Bot Team","team_id":"T123","token":"write-only-bot-token"}`)
	botRequest.SetPathValue("appId", app.ID)
	response = httptest.NewRecorder()
	h.HandleSlackInstallationCreate(response, botRequest)
	if response.Code != http.StatusCreated {
		t.Fatalf("bot create status=%d body=%s", response.Code, response.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	installationID, _ := created["id"].(string)
	if installationID == "" {
		t.Fatalf("bot create response missing id: %s", response.Body.String())
	}

	// Replacement preserves the prior bot installation on rejection.
	replaceRequest := newLocalSlackRequest(http.MethodPut, "/api/slack/installations/"+installationID+"/token",
		`{"token":"`+canary+`"}`)
	replaceRequest.SetPathValue("installationId", installationID)
	response = httptest.NewRecorder()
	h.HandleSlackInstallationToken(response, replaceRequest)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"conflict"`) {
		t.Fatalf("replace status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), canary) {
		t.Fatalf("replace response leaked candidate credential: %s", response.Body.String())
	}
	preserved, err := service.GetInstallation(installationID)
	if err != nil || preserved.CredentialKind != slackcatalog.CredentialKindBot || preserved.BotID != "B123" {
		t.Fatalf("rejected replacement changed installation: %#v, err=%v", preserved, err)
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

func TestSlackAppReferencesDeleteReturnsRefreshedPreview(t *testing.T) {
	references := &handlerReferences{refs: []slackcatalog.Reference{{SessionID: "session-1", Name: "Watcher"}}}
	service := slackcatalog.NewService(
		slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		&handlerCredentials{values: make(map[secrets.CredentialRef]string)},
		handlerSlack{}, references,
	)
	app, err := service.CreateApp(context.Background(), "App", "write-only-token")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{SlackCatalog: service})
	request := httptest.NewRequest(http.MethodDelete, "/api/slack/apps/"+app.ID+"/references", nil)
	request.SetPathValue("appId", app.ID)
	response := httptest.NewRecorder()

	h.HandleSlackAppReferencesDelete(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Watcher"`) ||
		!strings.Contains(response.Body.String(), `"references":[]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// TestSlackAppReferencesDeleteBroadcastsPartialSuccessOnError pins the
// mitto-5apf follow-up fix: when the remover fails partway through a batch,
// the handler still broadcasts loop_updated for every session that was
// actually mutated before returning the (safe, wrapped) error to the client.
func TestSlackAppReferencesDeleteBroadcastsPartialSuccessOnError(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(session.Metadata{SessionID: "session-1", ACPServer: "test", WorkingDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	retained := &session.LoopPrompt{Prompt: "inspect", Enabled: true, Triggers: []session.LoopTrigger{session.TriggerOnCompletion}}
	if err := store.Loop("session-1").Set(retained); err != nil {
		t.Fatal(err)
	}

	references := &handlerReferences{
		refs:           []slackcatalog.Reference{{SessionID: "session-1", Name: "Watcher"}, {SessionID: "session-2", Name: "Auditor"}},
		removeErr:      errors.New("disk error on session-2"),
		partialRemoved: []slackcatalog.Reference{{SessionID: "session-1", Name: "Watcher"}},
	}
	service := slackcatalog.NewService(
		slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		&handlerCredentials{values: make(map[secrets.CredentialRef]string)},
		handlerSlack{}, references,
	)
	app, err := service.CreateApp(context.Background(), "App", "write-only-token")
	if err != nil {
		t.Fatal(err)
	}

	var broadcastSessions []string
	var broadcastLoops []*session.LoopPrompt
	h := New(Deps{
		SlackCatalog: service,
		Store:        store,
		BroadcastLoopUpdated: func(sessionID string, loop *session.LoopPrompt) {
			broadcastSessions = append(broadcastSessions, sessionID)
			broadcastLoops = append(broadcastLoops, loop)
		},
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/slack/apps/"+app.ID+"/references", nil)
	request.SetPathValue("appId", app.ID)
	response := httptest.NewRecorder()

	h.HandleSlackAppReferencesDelete(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "disk error on session-2") {
		t.Fatalf("response leaked raw remover error: %s", response.Body.String())
	}
	if len(broadcastSessions) != 1 || broadcastSessions[0] != "session-1" {
		t.Fatalf("broadcast sessions = %#v, want exactly [session-1]", broadcastSessions)
	}
	if broadcastLoops[0] == nil || broadcastLoops[0].Prompt != "inspect" {
		t.Fatalf("broadcast loop = %#v, want the retained on-disk loop", broadcastLoops[0])
	}
}

// connectionsTestCredentials is a minimal slackbridge.CredentialResolver that
// always resolves the same app token, used only to let a FakeSource-backed
// worker start.
type connectionsTestCredentials struct{ token string }

func (c connectionsTestCredentials) Resolve(secrets.CredentialRef) (string, error) {
	return c.token, nil
}

// connectionsTestCatalog is a minimal slackbridge.Catalog resolving exactly
// one installation, used only to arm one onSlack subscription.
type connectionsTestCatalog map[string]slackcatalog.InstallationView

func (c connectionsTestCatalog) GetInstallation(id string) (slackcatalog.InstallationView, error) {
	installation, ok := c[id]
	if !ok {
		return slackcatalog.InstallationView{}, slackcatalog.ErrNotFound
	}
	return installation, nil
}

// connectionsTestRunner is a no-op slackbridge.ManagedLoopTriggerer: this test
// only exercises the connection-status snapshot, not event dispatch.
type connectionsTestRunner struct{}

func (connectionsTestRunner) TriggerNowWithSlackEvents(string, bool, session.LoopTrigger, []conversation.PromptSlackEvent) error {
	return nil
}

// connectionsTestSource is a slackbridge.Source that emits every queued event
// and then blocks until ctx is cancelled, unlike slackbridge.FakeSource (which
// returns immediately once its scripted events are exhausted). The manager
// treats a Run() return as a disconnect and immediately cycles back to
// "backoff", so a non-blocking source races the "connected" state this test
// asserts on. Blocking after the last event keeps the connection healthy so
// the status snapshot is deterministic.
type connectionsTestSource struct {
	events chan slackbridge.Event
}

func (s *connectionsTestSource) Run(ctx context.Context, emit func(slackbridge.Event)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt := <-s.events:
			emit(evt)
		}
	}
}

func waitForConnectionStatus(t *testing.T, manager *slackbridge.Manager, condition func([]slackbridge.ConnectionStatus) bool) []slackbridge.ConnectionStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if status := manager.Status(); condition(status) {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for connection status condition")
	return nil
}

// TestHandleSlackConnectionsReturnsCredentialFreeSnapshot pins the mitto-yn5
// acceptance criterion: GET /api/slack/connections surfaces the manager's
// live, credential-free ConnectionStatus snapshot (used by Settings > Slack
// to derive the "connected but 0 events received" delivery-health warning)
// and never leaks the app token used to establish the connection.
func TestHandleSlackConnectionsReturnsCredentialFreeSnapshot(t *testing.T) {
	const canary = "xapp-connections-test-canary-token"
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Create(session.Metadata{SessionID: "watcher", ACPServer: "test", WorkingDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	loop := &session.LoopPrompt{Prompt: "inspect", Enabled: true, Triggers: []session.LoopTrigger{session.TriggerOnSlack},
		SlackSubscriptions: []session.SlackSubscription{{InstallationID: "install-1", ChannelID: "channel-1"}}}
	if err := store.Loop("watcher").Set(loop); err != nil {
		t.Fatal(err)
	}

	catalog := connectionsTestCatalog{
		"install-1": {Installation: slackcatalog.Installation{ID: "install-1", AppID: "app-1",
			CredentialKind: slackcatalog.CredentialKindBot, TeamID: "team-1", BotID: "bot-1", BotUserID: "user-bot-1"}, TokenConfigured: true},
	}
	manager := slackbridge.NewManager(store, catalog, connectionsTestCredentials{token: canary}, connectionsTestRunner{}, nil)
	t.Cleanup(manager.Close)
	source := &connectionsTestSource{events: make(chan slackbridge.Event, 1)}
	manager.SetSourceFactory(func(string, string) (slackbridge.Source, error) { return source, nil })
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	source.events <- slackbridge.Event{EventID: "event-1", TeamID: "team-1", ChannelID: "channel-1", AuthorID: "human", Kind: "message"}

	waitForConnectionStatus(t, manager, func(status []slackbridge.ConnectionStatus) bool {
		return len(status) == 1 && status[0].State == "connected" && status[0].EventsAPIReceived == 1
	})

	h := New(Deps{SlackManager: manager})
	response := httptest.NewRecorder()
	h.HandleSlackConnections(response, httptest.NewRequest(http.MethodGet, "/api/slack/connections", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Connections []slackbridge.ConnectionStatus `json:"connections"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Connections) != 1 {
		t.Fatalf("connections = %#v", body.Connections)
	}
	got := body.Connections[0]
	if got.AppID != "app-1" || got.State != "connected" || got.SubscriptionCount != 1 || got.EventsAPIReceived != 1 {
		t.Fatalf("connection status = %#v", got)
	}
	if strings.Contains(response.Body.String(), canary) {
		t.Fatalf("response leaked app token: %s", response.Body.String())
	}
}

// TestHandleSlackConnectionsWithoutManagerReturnsEmptySnapshot pins the
// documented nil-SlackManager fallback (event delivery unavailable, e.g. the
// catalog path could not be resolved): the endpoint still returns 200 with an
// empty connections array rather than erroring the Settings > Slack tab.
func TestHandleSlackConnectionsWithoutManagerReturnsEmptySnapshot(t *testing.T) {
	h := New(Deps{})
	response := httptest.NewRecorder()
	h.HandleSlackConnections(response, httptest.NewRequest(http.MethodGet, "/api/slack/connections", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"connections":[]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
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

func TestSlackInstallationChannelsReturnsModeScopedPagesWithoutCredentials(t *testing.T) {
	const botToken = "xoxb-handler-secret"
	const userToken = "xoxp-handler-secret"
	provider := &handlerChannelSlack{
		identities: map[string]slackcatalog.InstallationIdentity{
			botToken:  {CredentialKind: slackcatalog.CredentialKindBot, SlackAppID: "A123", TeamID: "T111", BotID: "B111", BotUserID: "U111"},
			userToken: {CredentialKind: slackcatalog.CredentialKindUser, SlackAppID: "A123", TeamID: "T222", UserID: "U222"},
		},
		pages: map[string]slackcatalog.ChannelPage{
			botToken:  {Channels: []slackcatalog.Channel{{ID: "C-BOT", Name: "bot-public", IsMember: true}}, NextCursor: "bot-next"},
			userToken: {Channels: []slackcatalog.Channel{{ID: "G-USER", Name: "user-private", IsPrivate: true, IsMember: true}}, NextCursor: "user-next"},
		},
	}
	service := slackcatalog.NewService(
		slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		&handlerCredentials{values: make(map[secrets.CredentialRef]string)}, provider, nil,
	)
	app, err := service.CreateApp(context.Background(), "App", "write-only-app-token")
	if err != nil {
		t.Fatal(err)
	}
	bot, err := service.CreateInstallation(context.Background(), app.ID, "Bot", "T111", botToken)
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.CreateInstallation(context.Background(), app.ID, "User", "T222", userToken)
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{SlackCatalog: service})

	for _, test := range []struct {
		name        string
		installID   string
		wantID      string
		wantName    string
		wantPrivate bool
		wantMember  bool
		wantCursor  string
	}{
		{name: "bot", installID: bot.ID, wantID: "C-BOT", wantName: "bot-public", wantMember: true, wantCursor: "bot-next"},
		{name: "delegated user", installID: user.ID, wantID: "G-USER", wantName: "user-private", wantPrivate: true, wantMember: true, wantCursor: "user-next"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := "/api/slack/installations/" + test.installID + "/channels?cursor=cursor-in&limit=25"
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.SetPathValue("installationId", test.installID)
			response := httptest.NewRecorder()
			h.HandleSlackInstallationChannels(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var page slackcatalog.ChannelPage
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Channels) != 1 || page.Channels[0].ID != test.wantID ||
				page.Channels[0].Name != test.wantName || page.Channels[0].IsPrivate != test.wantPrivate ||
				page.Channels[0].IsMember != test.wantMember || page.NextCursor != test.wantCursor {
				t.Fatalf("channel page = %#v", page)
			}
			for _, credential := range []string{botToken, userToken} {
				if strings.Contains(response.Body.String(), credential) {
					t.Fatalf("response leaked installation credential: %s", response.Body.String())
				}
			}
		})
	}
	wantRequests := []handlerChannelRequest{
		{token: botToken, cursor: "cursor-in", limit: 25},
		{token: userToken, cursor: "cursor-in", limit: 25},
	}
	if fmt.Sprint(provider.requests) != fmt.Sprint(wantRequests) {
		t.Fatalf("provider requests = %#v, want %#v", provider.requests, wantRequests)
	}
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

func TestWriteSlackErrorMapsOAuthRequiredToActionableConflict(t *testing.T) {
	response := httptest.NewRecorder()
	writeSlackError(response, errors.Join(slackcatalog.ErrOAuthRequired, errors.New("auth.test omitted delegated-user app identity")))
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"conflict"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "OAuth") {
		t.Fatalf("missing actionable OAuth guidance: %s", response.Body.String())
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
		{"oauth required", errors.Join(slackcatalog.ErrOAuthRequired, errors.New(canary)), http.StatusConflict},
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
