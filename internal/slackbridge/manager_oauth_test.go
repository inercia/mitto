package slackbridge

import (
	"context"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackcatalog"
)

type oauthManagerCredentials struct {
	mu     sync.Mutex
	values map[secrets.CredentialRef]string
}

func (c *oauthManagerCredentials) Put(ref secrets.CredentialRef, value string) error {
	c.mu.Lock()
	c.values[ref] = value
	c.mu.Unlock()
	return nil
}

func (c *oauthManagerCredentials) Resolve(ref secrets.CredentialRef) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[ref]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return value, nil
}

func (c *oauthManagerCredentials) Status(ref secrets.CredentialRef) (secrets.CredentialStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.values[ref]
	return secrets.CredentialStatus{Configured: ok}, nil
}

func (c *oauthManagerCredentials) Delete(ref secrets.CredentialRef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.values[ref]; !ok {
		return secrets.ErrNotFound
	}
	delete(c.values, ref)
	return nil
}

type oauthManagerSlack struct{}

func (oauthManagerSlack) ValidateApp(context.Context, string) (string, error) { return "A123", nil }

func (oauthManagerSlack) ValidateInstallation(_ context.Context, token string) (slackcatalog.InstallationIdentity, error) {
	if token != "bot-install-token" {
		return slackcatalog.InstallationIdentity{}, slackcatalog.ErrOAuthRequired
	}
	return slackcatalog.InstallationIdentity{CredentialKind: slackcatalog.CredentialKindBot,
		SlackAppID: "A123", TeamID: "T123", TeamName: "Example", BotID: "B123", BotUserID: "UBOT"}, nil
}

func (oauthManagerSlack) ListChannels(context.Context, string, string, int) (slackcatalog.ChannelPage, error) {
	return slackcatalog.ChannelPage{}, nil
}

func (oauthManagerSlack) ExchangeOAuth(context.Context, string, string, string, string) (slackcatalog.OAuthIdentity, error) {
	return slackcatalog.OAuthIdentity{InstallationIdentity: slackcatalog.InstallationIdentity{
		CredentialKind: slackcatalog.CredentialKindUser, SlackAppID: "A123", TeamID: "T123", TeamName: "Example", UserID: "UDELEGATED",
	}, AccessToken: "oauth-user-token"}, nil
}

func (oauthManagerSlack) RevalidateOAuthInstallation(context.Context, string, string) (slackcatalog.InstallationIdentity, error) {
	return slackcatalog.InstallationIdentity{CredentialKind: slackcatalog.CredentialKindUser,
		SlackAppID: "A123", TeamID: "T123", TeamName: "Example", UserID: "UDELEGATED"}, nil
}

func TestManagerOAuthReplacementReconcilesRealTimeSocketAuthorization(t *testing.T) {
	credentials := &oauthManagerCredentials{values: make(map[secrets.CredentialRef]string)}
	catalog := slackcatalog.NewService(slackcatalog.NewFileStore(filepath.Join(t.TempDir(), "catalog.json")),
		credentials, oauthManagerSlack{}, nil)
	ctx := context.Background()
	app, err := catalog.CreateApp(ctx, "App", "app-socket-token")
	if err != nil {
		t.Fatal(err)
	}
	installation, err := catalog.CreateInstallation(ctx, app.ID, "Workspace", "T123", "bot-install-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.ConfigureOAuthClient(app.ID, "123.456", "oauth-client-secret"); err != nil {
		t.Fatal(err)
	}

	store := newManagerStore(t)
	addSlackLoop(t, store, "oauth-target", true, false, session.SlackSubscription{
		InstallationID: installation.ID, ChannelID: "private", EventMode: session.SlackEventModeAnyHumanMessage,
	})
	runner, sources := &managerRunner{}, &sourceHarness{}
	manager := NewManager(store, catalog, credentials, runner, nil)
	manager.factory, manager.settle = sources.factory, -1
	t.Cleanup(manager.Close)
	if err = manager.Start(); err != nil {
		t.Fatal(err)
	}
	waitForManager(t, "OAuth app Socket Mode worker", func() bool {
		created, active, _, _, _ := sources.snapshot()
		return created == 1 && active == 1
	})
	_, _, _, tokens, activeSources := sources.snapshot()
	if len(tokens) != 1 || tokens[0] != "app-socket-token" {
		t.Fatalf("Socket Mode app-token resolutions = %v", tokens)
	}

	activeSources[0].events <- Event{EventID: "bot-before-oauth", TeamID: "T123", ChannelID: "private",
		AuthorID: "human", Kind: "message", AuthorizationScopeKnown: true,
		Authorizations: []EventAuthorization{{UserID: "UBOT", IsBot: true}}}
	waitForManager(t, "bot authorization dispatch before replacement", func() bool { return len(runner.snapshot()) == 1 })

	var observerCalled bool
	var reconcileErr error
	catalog.SetChangeObserver(func(change slackcatalog.Change) {
		if change.InstallationID != "" {
			observerCalled = true
			reconcileErr = manager.ReconcileAll()
		}
	})
	start, err := catalog.StartOAuth(slackcatalog.OAuthStartRequest{AppID: app.ID, InstallationID: installation.ID,
		RedirectURI: "https://mitto.example/mitto/api/slack/oauth/callback"})
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.CompleteOAuth(ctx, authorizeURL.Query().Get("state"), "oauth-code", ""); err != nil {
		t.Fatal(err)
	}
	if !observerCalled || reconcileErr != nil {
		t.Fatalf("OAuth catalog observer called=%v reconcile_error=%v", observerCalled, reconcileErr)
	}
	updated, err := catalog.GetInstallation(installation.ID)
	if err != nil || updated.CredentialKind != slackcatalog.CredentialKindUser || updated.UserID != "UDELEGATED" || !updated.OAuthAuthorized {
		t.Fatalf("OAuth installation = %#v, %v", updated, err)
	}

	activeSources[0].events <- Event{EventID: "user-after-oauth", TeamID: "T123", ChannelID: "private",
		AuthorID: "human", Kind: "message", AuthorizationScopeKnown: true,
		Authorizations: []EventAuthorization{{UserID: "UDELEGATED"}}}
	activeSources[0].events <- Event{EventID: "old-bot-after-oauth", TeamID: "T123", ChannelID: "private",
		AuthorID: "human", Kind: "message", AuthorizationScopeKnown: true,
		Authorizations: []EventAuthorization{{UserID: "UBOT", IsBot: true}}}
	waitForManager(t, "delegated-user onSlack dispatch", func() bool { return len(runner.snapshot()) == 2 })
	time.Sleep(30 * time.Millisecond)
	calls := runner.snapshot()
	if len(calls) != 2 || calls[1].sessionID != "oauth-target" || calls[1].event.EventID != "user-after-oauth" ||
		calls[1].event.InstallationID != installation.ID {
		t.Fatalf("onSlack dispatches after OAuth = %#v", calls)
	}
	if created, active, maxActive, _, _ := sources.snapshot(); created != 1 || active != 1 || maxActive != 1 {
		t.Fatalf("Socket Mode workers after OAuth: created=%d active=%d max_active=%d", created, active, maxActive)
	}
}
