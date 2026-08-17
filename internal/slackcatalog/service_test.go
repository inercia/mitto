package slackcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/secrets"
)

type memoryStore struct {
	doc     document
	saveErr error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{doc: document{Version: DocumentVersion, Apps: []AppProfile{}, Installations: []Installation{}}}
}

func cloneDocument(doc document) document {
	clone := doc
	clone.Apps = append([]AppProfile(nil), doc.Apps...)
	clone.Installations = append([]Installation(nil), doc.Installations...)
	return clone
}

func (s *memoryStore) Load() (document, error) { return cloneDocument(s.doc), nil }
func (s *memoryStore) Save(doc document) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.doc = cloneDocument(doc)
	return nil
}

type memoryCredentials struct {
	values map[secrets.CredentialRef]string
}

func newMemoryCredentials() *memoryCredentials {
	return &memoryCredentials{values: make(map[secrets.CredentialRef]string)}
}

func (c *memoryCredentials) Put(ref secrets.CredentialRef, value string) error {
	c.values[ref] = value
	return nil
}
func (c *memoryCredentials) Resolve(ref secrets.CredentialRef) (string, error) {
	value, ok := c.values[ref]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return value, nil
}
func (c *memoryCredentials) Status(ref secrets.CredentialRef) (secrets.CredentialStatus, error) {
	_, ok := c.values[ref]
	return secrets.CredentialStatus{Configured: ok}, nil
}
func (c *memoryCredentials) Delete(ref secrets.CredentialRef) error {
	if _, ok := c.values[ref]; !ok {
		return secrets.ErrNotFound
	}
	delete(c.values, ref)
	return nil
}

type fakeSlackProvider struct {
	apps          map[string]string
	installations map[string]InstallationIdentity
	pages         map[string]ChannelPage
	channelCalls  int
}

func (f *fakeSlackProvider) ValidateApp(_ context.Context, token string) (string, error) {
	id, ok := f.apps[token]
	if !ok {
		return "", ErrUnavailable
	}
	return id, nil
}
func (f *fakeSlackProvider) ValidateInstallation(_ context.Context, token string) (InstallationIdentity, error) {
	identity, ok := f.installations[token]
	if !ok {
		return InstallationIdentity{}, ErrUnavailable
	}
	return identity, nil
}
func (f *fakeSlackProvider) ListPublicChannels(_ context.Context, _ string, cursor string, _ int) (ChannelPage, error) {
	f.channelCalls++
	return cloneChannelPage(f.pages[cursor]), nil
}

type fakeReferences struct{ refs []Reference }

func (f *fakeReferences) FindSlackReferences(context.Context, string, []string) ([]Reference, error) {
	return append([]Reference(nil), f.refs...), nil
}

func newTestService() (*Service, *memoryStore, *memoryCredentials, *fakeSlackProvider, *fakeReferences) {
	store := newMemoryStore()
	credentials := newMemoryCredentials()
	provider := &fakeSlackProvider{
		apps: map[string]string{"app-one": "A111", "app-two": "A222"},
		installations: map[string]InstallationIdentity{
			"bot-one":   {SlackAppID: "A111", TeamID: "T111", TeamName: "One", BotID: "B111", BotUserID: "U111"},
			"bot-two":   {SlackAppID: "A111", TeamID: "T222", TeamName: "Two", BotID: "B222", BotUserID: "U222"},
			"bot-other": {SlackAppID: "A222", TeamID: "T999", TeamName: "Other", BotID: "B999", BotUserID: "U999"},
		},
		pages: map[string]ChannelPage{
			"":     {Channels: []Channel{{ID: "C111", Name: "general"}}, NextCursor: "next"},
			"next": {Channels: []Channel{{ID: "C222", Name: "random"}}},
		},
	}
	references := &fakeReferences{}
	service := NewService(store, credentials, provider, references)
	return service, store, credentials, provider, references
}

func TestServiceLifecycleAndSecretNonDisclosure(t *testing.T) {
	service, store, credentials, _, _ := newTestService()
	ctx := context.Background()
	app, err := service.CreateApp(ctx, " App One ", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateApp(ctx, "App Two", "app-two"); err != nil {
		t.Fatal(err)
	}
	first, err := service.CreateInstallation(ctx, app.ID, "Workspace One", "T111", "bot-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInstallation(ctx, app.ID, "Workspace Two", "", "bot-two"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInstallation(ctx, app.ID, "Duplicate", "", "bot-one"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate workspace error = %v", err)
	}
	apps, err := service.ListApps()
	if err != nil || len(apps) != 2 || !apps[0].TokenConfigured {
		t.Fatalf("ListApps() = %#v, %v", apps, err)
	}
	installations, err := service.ListInstallations(app.ID)
	if err != nil || len(installations) != 2 || !installations[0].TokenConfigured {
		t.Fatalf("ListInstallations() = %#v, %v", installations, err)
	}
	if got, _ := credentials.Resolve(secrets.SlackInstallationCredential(first.ID, BotTokenCredential)); got != "bot-one" {
		t.Fatalf("stored credential = %q", got)
	}
	encoded, err := json.Marshal(struct {
		Document document           `json:"document"`
		Apps     []AppView          `json:"apps"`
		Installs []InstallationView `json:"installations"`
	}{store.doc, apps, installations})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"app-one", "app-two", "bot-one", "bot-two"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("serialized catalog leaked credential %q: %s", secret, encoded)
		}
	}
	if renamed, err := service.RenameInstallation(first.ID, "Renamed"); err != nil || renamed.Name != "Renamed" {
		t.Fatalf("RenameInstallation() = %#v, %v", renamed, err)
	}
}

func TestIdentityMismatchPreservesWorkingCredentials(t *testing.T) {
	service, _, credentials, provider, _ := newTestService()
	ctx := context.Background()
	app, err := service.CreateApp(ctx, "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	provider.apps["wrong-app"] = "A222"
	if _, err := service.ReplaceAppToken(ctx, app.ID, "wrong-app"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ReplaceAppToken mismatch error = %v", err)
	}
	appRef := secrets.SlackAppCredential(app.ID, AppTokenCredential)
	if got, _ := credentials.Resolve(appRef); got != "app-one" {
		t.Fatalf("app credential changed to %q", got)
	}
	if _, err := service.ReplaceAppToken(ctx, app.ID, "failed-validation"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ReplaceAppToken validation error = %v", err)
	}
	if got, _ := credentials.Resolve(appRef); got != "app-one" {
		t.Fatalf("failed validation changed app credential to %q", got)
	}
	installation, err := service.CreateInstallation(ctx, app.ID, "Workspace", "", "bot-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceInstallationToken(ctx, installation.ID, "bot-other"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ReplaceInstallationToken mismatch error = %v", err)
	}
	installRef := secrets.SlackInstallationCredential(installation.ID, BotTokenCredential)
	if got, _ := credentials.Resolve(installRef); got != "bot-one" {
		t.Fatalf("installation credential changed to %q", got)
	}
	if _, err := service.GetApp("not-a-uuid"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed ID error = %v", err)
	}
}

func TestCatalogSaveFailureRollsBackCredential(t *testing.T) {
	service, store, credentials, provider, _ := newTestService()
	store.saveErr = errors.New("disk full")
	if _, err := service.CreateApp(context.Background(), "App", "app-one"); err == nil {
		t.Fatal("CreateApp succeeded with failing catalog store")
	}
	if len(credentials.values) != 0 || len(store.doc.Apps) != 0 {
		t.Fatalf("failed create left state: credentials=%v document=%#v", credentials.values, store.doc)
	}

	store.saveErr = nil
	app, err := service.CreateApp(context.Background(), "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	provider.apps["replacement"] = "A111"
	store.saveErr = errors.New("disk full")
	if _, err := service.ReplaceAppToken(context.Background(), app.ID, "replacement"); err == nil {
		t.Fatal("ReplaceAppToken succeeded with failing catalog store")
	}
	ref := secrets.SlackAppCredential(app.ID, AppTokenCredential)
	if got, _ := credentials.Resolve(ref); got != "app-one" {
		t.Fatalf("rollback restored %q, want original", got)
	}
}

func TestReferenceBlockedDeletionAndChannelCache(t *testing.T) {
	service, _, credentials, provider, references := newTestService()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	app, _ := service.CreateApp(ctx, "App", "app-one")
	installation, _ := service.CreateInstallation(ctx, app.ID, "Workspace", "", "bot-one")

	first, err := service.Channels(ctx, installation.ID, "", 25)
	if err != nil || first.NextCursor != "next" {
		t.Fatalf("Channels first page = %#v, %v", first, err)
	}
	if _, err := service.Channels(ctx, installation.ID, "", 25); err != nil || provider.channelCalls != 1 {
		t.Fatalf("cache calls = %d, err=%v", provider.channelCalls, err)
	}
	second, err := service.Channels(ctx, installation.ID, "next", 25)
	if err != nil || len(second.Channels) != 1 || second.Channels[0].ID != "C222" {
		t.Fatalf("Channels second page = %#v, %v", second, err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := service.Channels(ctx, installation.ID, "", 25); err != nil || provider.channelCalls != 3 {
		t.Fatalf("expired cache calls = %d, err=%v", provider.channelCalls, err)
	}
	provider.installations["bot-new"] = provider.installations["bot-one"]
	if _, err := service.ReplaceInstallationToken(ctx, installation.ID, "bot-new"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Channels(ctx, installation.ID, "", 25); err != nil || provider.channelCalls != 4 {
		t.Fatalf("token invalidation calls = %d, err=%v", provider.channelCalls, err)
	}
	if _, err := service.Channels(ctx, installation.ID, "", MaxChannelPageSize+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid pagination error = %v", err)
	}

	references.refs = []Reference{{SessionID: "session-1", Name: "Watcher"}}
	preview, err := service.PrepareDeleteApp(ctx, app.ID)
	if err != nil || len(preview.InstallationIDs) != 1 || len(preview.References) != 1 {
		t.Fatalf("PrepareDeleteApp() = %#v, %v", preview, err)
	}
	if err := service.DeleteInstallation(ctx, installation.ID); !errors.Is(err, ErrReferenced) {
		t.Fatalf("DeleteInstallation reference error = %v", err)
	}
	ref := secrets.SlackInstallationCredential(installation.ID, BotTokenCredential)
	if got, _ := credentials.Resolve(ref); got != "bot-new" {
		t.Fatalf("blocked deletion changed credential to %q", got)
	}
	references.refs = nil
	if err := service.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Resolve(ref); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("cascaded credential remains: %v", err)
	}
}

func TestFileStoreUsesPrivateModeAndContainsNoTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slack_integrations.json")
	store := NewFileStore(path)
	service := NewService(store, newMemoryCredentials(), &fakeSlackProvider{
		apps: map[string]string{"secret-app-token": "A111"}, installations: map[string]InstallationIdentity{}, pages: map[string]ChannelPage{},
	}, nil)
	if _, err := service.CreateApp(context.Background(), "App", "secret-app-token"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-app-token") {
		t.Fatalf("catalog contains token: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("catalog mode = %o, want 600", info.Mode().Perm())
	}
}
