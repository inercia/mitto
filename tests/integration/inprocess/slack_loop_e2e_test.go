//go:build integration

package inprocess

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackbridge"
	"github.com/inercia/mitto/internal/slackcatalog"
	"github.com/inercia/mitto/pkg/api"
)

type slackE2ECatalog map[string]slackcatalog.InstallationView

func (c slackE2ECatalog) GetInstallation(id string) (slackcatalog.InstallationView, error) {
	installation, ok := c[id]
	if !ok {
		return slackcatalog.InstallationView{}, slackcatalog.ErrNotFound
	}
	return installation, nil
}

type slackE2ECredentials struct {
	mu    sync.Mutex
	token string
}

func (c *slackE2ECredentials) Resolve(secrets.CredentialRef) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, nil
}

func (c *slackE2ECredentials) set(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

type slackE2ESources struct {
	mu     sync.Mutex
	source *slackbridge.FakeSource
	tokens []string
}

func (s *slackE2ESources) factory(_ string, token string) (slackbridge.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, token)
	return s.source, nil
}

func (s *slackE2ESources) snapshotTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.tokens...)
}

func TestSlackLoopProductionPathE2E(t *testing.T) {
	ts := SetupTestServer(t)
	catalog := slackE2ECatalog{
		"install-a": {Installation: slackcatalog.Installation{ID: "install-a", AppID: "app-shared", TeamID: "team-a"}, TokenConfigured: true},
		"install-b": {Installation: slackcatalog.Installation{ID: "install-b", AppID: "app-shared", TeamID: "team-b"}, TokenConfigured: true},
	}
	credentials := &slackE2ECredentials{token: "fake-token-v1"}
	longText := strings.Repeat("external", 700)
	privateText := strings.Repeat("private", 700)
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})}
	sources := &slackE2ESources{source: &slackbridge.FakeSource{Runs: []slackbridge.FakeRun{
		{Wait: gates[0], Events: []slackbridge.Event{
			{EventID: "event-a", TeamID: "team-a", ChannelID: "channel-a", AuthorID: "human-a", Kind: "message", Text: longText},
			{EventID: "event-private", TeamID: "team-a", ChannelID: "private-a", AuthorID: "human-private", Kind: "message", Text: privateText},
			{EventID: "event-b", TeamID: "team-b", ChannelID: "channel-b", AuthorID: "human-b", Kind: "message", Text: "workspace B"},
			{EventID: "event-miss", TeamID: "team-a", ChannelID: "channel-b", AuthorID: "human", Kind: "message", Text: "must not dispatch"},
			{EventID: "event-a", TeamID: "team-a", ChannelID: "channel-a", AuthorID: "human-a", Kind: "message", Text: longText},
		}},
		{Wait: gates[1], Events: []slackbridge.Event{{EventID: "event-paused", TeamID: "team-a", ChannelID: "channel-a", AuthorID: "human", Kind: "message"}}},
		{Wait: gates[2], Events: []slackbridge.Event{{EventID: "event-resumed", TeamID: "team-a", ChannelID: "channel-a", AuthorID: "human", Kind: "message"}}},
		{Wait: gates[3], Events: []slackbridge.Event{{EventID: "event-rotated", TeamID: "team-a", ChannelID: "channel-a", AuthorID: "human", Kind: "message"}}, Err: errors.New("forced disconnect")},
		{Wait: gates[4], Events: []slackbridge.Event{{EventID: "event-reconnected", TeamID: "team-a", ChannelID: "channel-a", AuthorID: "human", Kind: "message"}}},
		{Wait: gates[5], Events: []slackbridge.Event{{EventID: "event-restart", TeamID: "team-b", ChannelID: "channel-b", AuthorID: "human", Kind: "message"}}},
	}}}

	a1 := createSlackLoopSession(t, ts, "A mixed", true, []string{"onSlack", "schedule"}, "install-a", "channel-a")
	a2 := createSlackLoopSession(t, ts, "A fanout", true, []string{"onSlack"}, "install-a", "channel-a")
	b := createSlackLoopSession(t, ts, "B target", true, []string{"onSlack"}, "install-b", "channel-b")
	private := createSlackLoopSession(t, ts, "private target", true, []string{"onSlack"}, "install-a", "private-a")
	paused := createSlackLoopSession(t, ts, "paused", false, []string{"onSlack"}, "install-a", "channel-a")

	manager := slackbridge.NewManager(ts.Store, catalog, credentials, ts.Server.LoopRunner(), nil)
	manager.SetSourceFactory(sources.factory)
	if err := manager.Start(); err != nil {
		t.Fatalf("start Slack manager: %v", err)
	}
	t.Cleanup(manager.Close)

	close(gates[0])
	waitSlackCounts(t, ts, map[string]int{a1: 1, a2: 1, b: 1, private: 1, paused: 0})
	assertSlackPromptBounded(t, ts, a1, "event-a", "install-a", "channel-a", longText)
	assertSlackPromptBounded(t, ts, private, "event-private", "install-a", "private-a", privateText)
	waitLoopSessionIdle(t, ts, a1)
	waitLoopSessionIdle(t, ts, a2)
	waitLoopSessionIdle(t, ts, b)
	waitLoopSessionIdle(t, ts, private)

	setSlackLoopEnabled(t, ts, a2, false)
	if err := manager.ReconcileSession(a2); err != nil {
		t.Fatal(err)
	}
	close(gates[1])
	waitSlackCounts(t, ts, map[string]int{a1: 2, a2: 1})
	waitLoopSessionIdle(t, ts, a1)

	setSlackLoopEnabled(t, ts, a2, true)
	if err := manager.ReconcileSession(a2); err != nil {
		t.Fatal(err)
	}
	close(gates[2])
	waitSlackCounts(t, ts, map[string]int{a1: 3, a2: 2})
	waitLoopSessionIdle(t, ts, a1)
	waitLoopSessionIdle(t, ts, a2)

	credentials.set("fake-token-v2")
	manager.RestartApp("app-shared")
	close(gates[3])
	waitSlackCounts(t, ts, map[string]int{a1: 4, a2: 3})
	waitLoopSessionIdle(t, ts, a1)
	waitLoopSessionIdle(t, ts, a2)
	// The next FakeRun is consumed by the manager's bounded reconnect loop.
	close(gates[4])
	waitSlackCounts(t, ts, map[string]int{a1: 5, a2: 4})
	if got := sources.snapshotTokens(); len(got) < 5 || got[len(got)-1] != "fake-token-v2" {
		t.Fatalf("credential restart/reconnect token history = %v", got)
	}
	waitLoopSessionIdle(t, ts, a1)
	waitLoopSessionIdle(t, ts, a2)

	complete := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, b, api.SessionCallbacks{OnPromptComplete: func(int) { complete <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := ws.SendPrompt("slow response"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		bs := ts.Server.GetSessionManager().GetSession(b)
		return bs != nil && bs.IsPrompting()
	}, "busy Slack recipient")
	close(gates[5])
	waitFor(t, 5*time.Second, func() bool {
		for _, status := range manager.Status() {
			if status.AppID == "app-shared" && status.PendingCount == 1 {
				return true
			}
		}
		return false
	}, "durable Slack contention backlog")
	manager.Close()
	select {
	case <-complete:
	case <-ctx.Done():
		t.Fatal("slow prompt did not complete")
	}
	waitLoopSessionIdle(t, ts, b)

	recoveredSources := &slackE2ESources{source: &slackbridge.FakeSource{}}
	recovered := slackbridge.NewManager(ts.Store, catalog, credentials, ts.Server.LoopRunner(), nil)
	recovered.SetSourceFactory(recoveredSources.factory)
	defer recovered.Close()
	if err := recovered.Start(); err != nil {
		t.Fatalf("restart Slack manager: %v", err)
	}
	waitSlackCounts(t, ts, map[string]int{b: 2})
}

func TestSlackLoopDelegatedAuthorizationE2E(t *testing.T) {
	ts := SetupTestServer(t)
	emit := make(chan struct{})
	catalog := slackE2ECatalog{
		"bot-install": {Installation: slackcatalog.Installation{ID: "bot-install", AppID: "app", CredentialKind: slackcatalog.CredentialKindBot,
			TeamID: "team", BotUserID: "UBOT"}, TokenConfigured: true},
		"user-install": {Installation: slackcatalog.Installation{ID: "user-install", AppID: "app", CredentialKind: slackcatalog.CredentialKindUser,
			TeamID: "team", UserID: "UDELEGATED"}, TokenConfigured: true},
	}
	source := &slackbridge.FakeSource{Runs: []slackbridge.FakeRun{{Wait: emit, Events: []slackbridge.Event{
		{EventID: "event-both", TeamID: "team", ChannelID: "private", AuthorID: "human", Kind: "message", Text: "both",
			AuthorizationScopeKnown: true, Authorizations: []slackbridge.EventAuthorization{{UserID: "UBOT", IsBot: true}, {UserID: "UDELEGATED"}}},
		{EventID: "event-user", TeamID: "team", ChannelID: "private", AuthorID: "human", Kind: "message", Text: "user",
			AuthorizationScopeKnown: true, Authorizations: []slackbridge.EventAuthorization{{UserID: "UDELEGATED"}}},
		{EventID: "event-revoked", TeamID: "team", ChannelID: "private", AuthorID: "human", Kind: "message", Text: "must-not-persist",
			AuthorizationScopeKnown: true, Authorizations: []slackbridge.EventAuthorization{}},
	}}}}
	sources := &slackE2ESources{source: source}
	bot := createSlackLoopSession(t, ts, "bot authorization", true, []string{"onSlack"}, "bot-install", "private")
	user := createSlackLoopSession(t, ts, "user authorization", true, []string{"onSlack"}, "user-install", "private")
	dual := createSlackLoopSessionWithSubscriptions(t, ts, "deduplicated authorization", true, []string{"onSlack"}, []api.SlackSubscription{
		{InstallationID: "bot-install", ChannelID: "private", EventMode: "anyHumanMessage", ThreadPolicy: "any"},
		{InstallationID: "user-install", ChannelID: "private", EventMode: "anyHumanMessage", ThreadPolicy: "any"},
	})
	manager := slackbridge.NewManager(ts.Store, catalog, &slackE2ECredentials{token: "fake-app-token"}, ts.Server.LoopRunner(), nil)
	manager.SetSourceFactory(sources.factory)
	if err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	close(emit)
	waitSlackCounts(t, ts, map[string]int{bot: 1, user: 1, dual: 1})
	for _, sessionID := range []string{bot, user, dual} {
		waitLoopSessionIdle(t, ts, sessionID)
	}
	assertSlackPromptEventCounts(t, ts, bot, map[string]int{"event-both": 1, "event-user": 0, "event-revoked": 0})
	assertSlackPromptEventCounts(t, ts, user, map[string]int{"event-both": 1, "event-user": 1, "event-revoked": 0})
	assertSlackPromptEventCounts(t, ts, dual, map[string]int{"event-both": 1, "event-user": 1, "event-revoked": 0})
}

func createSlackLoopSession(t *testing.T, ts *TestServer, name string, enabled bool, triggers []string, installationID, channelID string) string {
	return createSlackLoopSessionWithSubscriptions(t, ts, name, enabled, triggers, []api.SlackSubscription{{
		InstallationID: installationID, ChannelID: channelID, EventMode: "anyHumanMessage", ThreadPolicy: "any",
	}})
}

func createSlackLoopSessionWithSubscriptions(t *testing.T, ts *TestServer, name string, enabled bool, triggers []string, subscriptions []api.SlackSubscription) string {
	t.Helper()
	created, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(created.SessionID) })
	_, err = ts.Client.SetLoop(created.SessionID, api.SetLoopRequest{
		Prompt:   "{{ range .Trigger.OnSlack.Events }}event={{ .EventID }} install={{ .InstallationID }} channel={{ .ChannelID }} text={{ .Text }}{{ end }}",
		Triggers: triggers, Frequency: api.LoopFrequency{Value: 1, Unit: "days"}, Enabled: enabled,
		SlackSubscriptions: subscriptions,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created.SessionID
}

func assertSlackPromptEventCounts(t *testing.T, ts *TestServer, sessionID string, expected map[string]int) {
	t.Helper()
	events, err := ts.Store.ReadEvents(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var prompts strings.Builder
	for _, event := range events {
		if event.Type == session.EventTypeUserPrompt {
			data, _ := json.Marshal(event.Data)
			prompts.Write(data)
		}
	}
	for eventID, want := range expected {
		if got := strings.Count(prompts.String(), eventID); got != want {
			t.Fatalf("session %s event %s count=%d want=%d prompts=%s", sessionID, eventID, got, want, prompts.String())
		}
	}
}

func setSlackLoopEnabled(t *testing.T, ts *TestServer, sessionID string, enabled bool) {
	t.Helper()
	if _, err := ts.Client.PatchLoop(sessionID, api.LoopPatchRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
}

func waitSlackCounts(t *testing.T, ts *TestServer, counts map[string]int) {
	t.Helper()
	waitFor(t, 12*time.Second, func() bool {
		for id, want := range counts {
			loop, err := ts.Client.GetLoop(id)
			if err != nil || loop.IterationCount != want {
				return false
			}
		}
		return true
	}, "Slack loop iteration counts")
}

func assertSlackPromptBounded(t *testing.T, ts *TestServer, sessionID, eventID, installationID, channelID, original string) {
	t.Helper()
	events, err := ts.Store.ReadEvents(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != session.EventTypeUserPrompt {
			continue
		}
		data, _ := json.Marshal(event.Data)
		text := string(data)
		if !strings.Contains(text, eventID) {
			continue
		}
		for _, want := range []string{installationID, channelID, "SLACK_UNTRUSTED_START", "SLACK_UNTRUSTED_END"} {
			if !strings.Contains(text, want) {
				t.Fatalf("Slack prompt missing %q: %s", want, text)
			}
		}
		if strings.Contains(text, original) || len(text) > 6000 {
			t.Fatalf("untrusted Slack prompt was not bounded: bytes=%d", len(text))
		}
		return
	}
	t.Fatal("Slack user_prompt event not found")
}
