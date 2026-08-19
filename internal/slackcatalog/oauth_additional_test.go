package slackcatalog

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/secrets"
)

const testOAuthRedirectURI = "https://mitto.example/mitto/api/slack/oauth/callback"

func startTestOAuth(t *testing.T, service *Service, request OAuthStartRequest) (OAuthStart, string) {
	t.Helper()
	request.RedirectURI = testOAuthRedirectURI
	start, err := service.StartOAuth(request)
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" {
		t.Fatalf("authorization URL has no state: %q", start.AuthorizationURL)
	}
	return start, state
}

func TestOAuthStateExpiresAndCancellationConsumesState(t *testing.T) {
	service, _, _, provider, _ := newTestService()
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	service.oauthTTL = time.Minute
	app, err := service.CreateApp(context.Background(), "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConfigureOAuthClient(app.ID, "123.456", "oauth-secret"); err != nil {
		t.Fatal(err)
	}

	start, expiredState := startTestOAuth(t, service, OAuthStartRequest{AppID: app.ID, Name: "Expired"})
	now = start.ExpiresAt
	status, err := service.OAuthStatus(start.FlowID)
	if err != nil || status.Status != "failed" || status.Error != "expired" {
		t.Fatalf("expired OAuthStatus() = %#v, %v", status, err)
	}
	if _, err = service.CompleteOAuth(context.Background(), expiredState, "oauth-code", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired CompleteOAuth() error = %v", err)
	}
	if provider.oauthCalls != 0 {
		t.Fatalf("expired state reached provider: calls=%d", provider.oauthCalls)
	}

	start, cancelledState := startTestOAuth(t, service, OAuthStartRequest{AppID: app.ID, Name: "Cancelled"})
	status, err = service.CompleteOAuth(context.Background(), cancelledState, "", "access_denied")
	if !errors.Is(err, ErrInvalid) || status.Status != "failed" || status.Error != "authorization_cancelled" {
		t.Fatalf("cancelled CompleteOAuth() = %#v, %v", status, err)
	}
	if _, err = service.CompleteOAuth(context.Background(), cancelledState, "oauth-code", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replayed cancellation error = %v", err)
	}
	stored, err := service.OAuthStatus(start.FlowID)
	if err != nil || stored.Error != "authorization_cancelled" {
		t.Fatalf("cancelled OAuthStatus() = %#v, %v", stored, err)
	}
}

func TestOAuthReplacementRollsBackCredentialAndMetadataOnSaveFailure(t *testing.T) {
	service, store, credentials, provider, _ := newTestService()
	provider.oauthIdentity = OAuthIdentity{InstallationIdentity: InstallationIdentity{
		CredentialKind: CredentialKindUser, SlackAppID: "A111", TeamID: "T111", TeamName: "One", UserID: "U444",
	}, AccessToken: "oauth-user-token"}
	ctx := context.Background()
	app, err := service.CreateApp(ctx, "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	installation, err := service.CreateInstallation(ctx, app.ID, "Workspace", "T111", "bot-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConfigureOAuthClient(app.ID, "123.456", "oauth-secret"); err != nil {
		t.Fatal(err)
	}

	_, state := startTestOAuth(t, service, OAuthStartRequest{AppID: app.ID, InstallationID: installation.ID})
	status, err := service.CompleteOAuth(ctx, state, "oauth-code", "")
	if err != nil || status.InstallationID != installation.ID {
		t.Fatalf("replacement CompleteOAuth() = %#v, %v", status, err)
	}
	ref := secrets.SlackInstallationCredential(installation.ID, InstallationTokenCredential)
	if got, _ := credentials.Resolve(ref); got != "oauth-user-token" {
		t.Fatalf("replacement credential = %q", got)
	}

	provider.oauthIdentity.UserID = "U555"
	provider.oauthIdentity.AccessToken = "oauth-rejected-token"
	_, state = startTestOAuth(t, service, OAuthStartRequest{AppID: app.ID, InstallationID: installation.ID})
	store.saveErr = errors.New("disk full")
	if _, err = service.CompleteOAuth(ctx, state, "oauth-code", ""); err == nil {
		t.Fatal("CompleteOAuth() succeeded with failing catalog store")
	}
	if got, _ := credentials.Resolve(ref); got != "oauth-user-token" {
		t.Fatalf("rollback restored credential %q", got)
	}
	if got := store.doc.Installations[0]; got.UserID != "U444" || !got.OAuthAuthorized || got.CredentialKind != CredentialKindUser {
		t.Fatalf("rollback changed installation metadata: %#v", got)
	}
}

type oauthRevalidationProvider struct {
	identity      OAuthIdentity
	revalidateErr error
}

func (*oauthRevalidationProvider) ValidateApp(context.Context, string) (string, error) {
	return "A111", nil
}
func (*oauthRevalidationProvider) ValidateInstallation(context.Context, string) (InstallationIdentity, error) {
	return InstallationIdentity{}, ErrOAuthRequired
}
func (*oauthRevalidationProvider) ListChannels(context.Context, string, string, int) (ChannelPage, error) {
	return ChannelPage{}, nil
}
func (p *oauthRevalidationProvider) ExchangeOAuth(context.Context, string, string, string, string) (OAuthIdentity, error) {
	return p.identity, nil
}
func (p *oauthRevalidationProvider) RevalidateOAuthInstallation(context.Context, string, string) (InstallationIdentity, error) {
	if p.revalidateErr != nil {
		return InstallationIdentity{}, p.revalidateErr
	}
	return p.identity.InstallationIdentity, nil
}

func TestOAuthAuthorizedUserRevalidationFailsClosedAfterRevocation(t *testing.T) {
	store := newMemoryStore()
	credentials := newMemoryCredentials()
	provider := &oauthRevalidationProvider{identity: OAuthIdentity{InstallationIdentity: InstallationIdentity{
		CredentialKind: CredentialKindUser, SlackAppID: "A111", TeamID: "T111", TeamName: "One", UserID: "U444",
	}, AccessToken: "oauth-user-token"}}
	service := NewService(store, credentials, provider, nil)
	ctx := context.Background()
	app, err := service.CreateApp(ctx, "App", "app-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ConfigureOAuthClient(app.ID, "123.456", "oauth-secret"); err != nil {
		t.Fatal(err)
	}
	_, state := startTestOAuth(t, service, OAuthStartRequest{AppID: app.ID, Name: "Workspace"})
	status, err := service.CompleteOAuth(ctx, state, "oauth-code", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ValidateInstallation(ctx, status.InstallationID); err != nil {
		t.Fatalf("OAuth revalidation failed: %v", err)
	}
	provider.revalidateErr = ErrInvalid
	if _, err = service.ValidateInstallation(ctx, status.InstallationID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("revoked OAuth revalidation error = %v", err)
	}
	if got := store.doc.Installations[0]; got.UserID != "U444" || !got.OAuthAuthorized {
		t.Fatalf("failed revalidation changed metadata: %#v", got)
	}
}
