package slackcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	pagesByToken  map[string]map[string]ChannelPage
	channelReqs   []channelRequest
	channelCalls  int
}

type channelRequest struct {
	token  string
	cursor string
	limit  int
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
func (f *fakeSlackProvider) ListChannels(_ context.Context, token, cursor string, limit int) (ChannelPage, error) {
	f.channelCalls++
	f.channelReqs = append(f.channelReqs, channelRequest{token: token, cursor: cursor, limit: limit})
	if pages, ok := f.pagesByToken[token]; ok {
		return cloneChannelPage(pages[cursor]), nil
	}
	return cloneChannelPage(f.pages[cursor]), nil
}

type fakeReferences struct{ refs []Reference }

func (f *fakeReferences) FindSlackReferences(context.Context, string, []string) ([]Reference, error) {
	return append([]Reference(nil), f.refs...), nil
}

type blockingSlackProvider struct {
	*fakeSlackProvider
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingSlackProvider) ListChannels(ctx context.Context, token, cursor string, limit int) (ChannelPage, error) {
	p.once.Do(func() {
		close(p.started)
		<-p.release
	})
	return p.fakeSlackProvider.ListChannels(ctx, token, cursor, limit)
}

func newTestService() (*Service, *memoryStore, *memoryCredentials, *fakeSlackProvider, *fakeReferences) {
	store := newMemoryStore()
	credentials := newMemoryCredentials()
	provider := &fakeSlackProvider{
		apps: map[string]string{"app-one": "A111", "app-two": "A222"},
		installations: map[string]InstallationIdentity{
			"bot-one":   {SlackAppID: "A111", TeamID: "T111", TeamName: "One", BotID: "B111", BotUserID: "U111"},
			"bot-new":   {CredentialKind: CredentialKindBot, SlackAppID: "A111", TeamID: "T111", TeamName: "One", BotID: "B333", BotUserID: "U333"},
			"bot-two":   {SlackAppID: "A111", TeamID: "T222", TeamName: "Two", BotID: "B222", BotUserID: "U222"},
			"bot-other": {SlackAppID: "A222", TeamID: "T999", TeamName: "Other", BotID: "B999", BotUserID: "U999"},
			"user-one":  {CredentialKind: CredentialKindUser, SlackAppID: "A111", TeamID: "T111", TeamName: "One", UserID: "U444"},
		},
		pages: map[string]ChannelPage{
			"":     {Channels: []Channel{{ID: "C111", Name: "general", IsMember: true}}, NextCursor: "next"},
			"next": {Channels: []Channel{{ID: "G222", Name: "private-ops", IsPrivate: true, IsMember: true}}},
		},
	}
	references := &fakeReferences{}
	service := NewService(store, credentials, provider, references)
	return service, store, credentials, provider, references
}

func TestInstallationCredentialKindCanSwitchOnlyOnExplicitReplacement(t *testing.T) {
	service, store, credentials, provider, _ := newTestService()
	ctx := context.Background()
	app, err := service.CreateApp(ctx, "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	installation, err := service.CreateInstallation(ctx, app.ID, "Workspace", "T111", "bot-one")
	if err != nil || installation.CredentialKind != CredentialKindBot {
		t.Fatalf("CreateInstallation() = %#v, %v", installation, err)
	}

	userView, err := service.ReplaceInstallationToken(ctx, installation.ID, "user-one")
	if err != nil || userView.CredentialKind != CredentialKindUser || userView.UserID != "U444" ||
		userView.BotID != "" || userView.BotUserID != "" {
		t.Fatalf("user replacement = %#v, %v", userView, err)
	}
	if got, _ := credentials.Resolve(secrets.SlackInstallationCredential(installation.ID, InstallationTokenCredential)); got != "user-one" {
		t.Fatalf("stored user credential = %q", got)
	}

	provider.installations["user-one"] = provider.installations["bot-new"]
	if _, err := service.ValidateInstallation(ctx, installation.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("revalidation kind change error = %v", err)
	}
	if got := store.doc.Installations[0]; got.CredentialKind != CredentialKindUser || got.UserID != "U444" || got.BotID != "" {
		t.Fatalf("failed revalidation changed metadata: %#v", got)
	}

	botView, err := service.ReplaceInstallationToken(ctx, installation.ID, "bot-new")
	if err != nil || botView.CredentialKind != CredentialKindBot || botView.BotID != "B333" || botView.UserID != "" {
		t.Fatalf("bot replacement = %#v, %v", botView, err)
	}
}

func TestDelegatedUserIdentityMismatchAndSaveFailurePreserveBotInstallation(t *testing.T) {
	service, store, credentials, provider, _ := newTestService()
	ctx := context.Background()
	app, err := service.CreateApp(ctx, "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	provider.installations["user-wrong-app"] = InstallationIdentity{
		CredentialKind: CredentialKindUser, SlackAppID: "A222", TeamID: "T111", UserID: "U555",
	}
	provider.installations["user-wrong-team"] = InstallationIdentity{
		CredentialKind: CredentialKindUser, SlackAppID: "A111", TeamID: "T999", UserID: "U555",
	}
	for _, token := range []string{"user-wrong-app", "user-wrong-team"} {
		if _, err := service.CreateInstallation(ctx, app.ID, "Mismatch", "T111", token); !errors.Is(err, ErrConflict) {
			t.Fatalf("CreateInstallation(%q) error = %v", token, err)
		}
	}
	if len(store.doc.Installations) != 0 || len(credentials.values) != 1 {
		t.Fatalf("identity mismatch mutated state: document=%#v credentials=%d", store.doc, len(credentials.values))
	}

	installation, err := service.CreateInstallation(ctx, app.ID, "Workspace", "T111", "bot-one")
	if err != nil {
		t.Fatal(err)
	}
	store.saveErr = errors.New("disk full")
	if _, err := service.ReplaceInstallationToken(ctx, installation.ID, "user-one"); err == nil {
		t.Fatal("ReplaceInstallationToken succeeded with failing catalog store")
	}
	ref := secrets.SlackInstallationCredential(installation.ID, InstallationTokenCredential)
	if got, _ := credentials.Resolve(ref); got != "bot-one" {
		t.Fatalf("rollback restored credential %q, want bot-one", got)
	}
	if got := store.doc.Installations[0]; got.CredentialKind != CredentialKindBot || got.BotID != "B111" || got.UserID != "" {
		t.Fatalf("rollback changed installation metadata: %#v", got)
	}
}

func TestFileStoreDefaultsLegacyInstallationKindToBotAndRejectsUnknownKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slack_integrations.json")
	legacy := `{"version":1,"apps":[],"installations":[{"id":"legacy","app_id":"app","name":"Legacy","team_id":"T111"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := NewFileStore(path).Load()
	if err != nil || len(doc.Installations) != 1 || doc.Installations[0].CredentialKind != CredentialKindBot {
		t.Fatalf("legacy Load() = %#v, %v", doc, err)
	}
	unknown := strings.Replace(legacy, `"team_id":"T111"`, `"credential_kind":"admin","team_id":"T111"`, 1)
	if err := os.WriteFile(path, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path).Load(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind Load() error = %v", err)
	}
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

func TestCatalogChangeObserverRunsOnlyAfterSuccessfulCommitAndOutsideLock(t *testing.T) {
	service, store, _, provider, _ := newTestService()
	app, err := service.CreateApp(context.Background(), "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan Change, 1)
	service.SetChangeObserver(func(change Change) {
		// Calling back into the service proves the observer is outside s.mu.
		if _, err := service.GetApp(change.AppID); err != nil {
			t.Errorf("GetApp from observer: %v", err)
		}
		changes <- change
	})
	provider.apps["wrong-app"] = "A222"
	if _, err := service.ReplaceAppToken(context.Background(), app.ID, "wrong-app"); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched replacement error = %v", err)
	}
	select {
	case change := <-changes:
		t.Fatalf("failed transaction notified observer: %#v", change)
	default:
	}
	provider.apps["replacement"] = app.SlackAppID
	if _, err := service.ReplaceAppToken(context.Background(), app.ID, "replacement"); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-changes:
		if change.AppID != app.ID || !change.Credential || change.InstallationID != "" {
			t.Fatalf("change = %#v", change)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for post-commit observer")
	}
	if store.doc.Apps[0].ID != app.ID {
		t.Fatal("observer ran before document commit")
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
	first.Channels[0].Name = "mutated by caller"
	cached, err := service.Channels(ctx, installation.ID, "", 25)
	if err != nil || provider.channelCalls != 1 || cached.Channels[0].Name != "general" || !cached.Channels[0].IsMember {
		t.Fatalf("cache calls = %d, err=%v", provider.channelCalls, err)
	}
	second, err := service.Channels(ctx, installation.ID, "next", 25)
	if err != nil || len(second.Channels) != 1 || second.Channels[0].ID != "G222" ||
		!second.Channels[0].IsPrivate || !second.Channels[0].IsMember {
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

func TestChannelsUseModeCredentialAndCachePerInstallation(t *testing.T) {
	service, _, _, provider, _ := newTestService()
	provider.installations["user-three"] = InstallationIdentity{
		CredentialKind: CredentialKindUser, SlackAppID: "A111", TeamID: "T333", TeamName: "Three", UserID: "U444",
	}
	provider.pagesByToken = map[string]map[string]ChannelPage{
		"bot-one": {
			"":         {Channels: []Channel{{ID: "C-BOT", Name: "bot-public", IsMember: true}}, NextCursor: "bot-next"},
			"bot-next": {Channels: []Channel{{ID: "G-BOT", Name: "bot-private", IsPrivate: true, IsMember: true}}},
		},
		"user-three": {
			"":          {Channels: []Channel{{ID: "G-USER", Name: "user-private", IsPrivate: true, IsMember: true}}, NextCursor: "user-next"},
			"user-next": {Channels: []Channel{{ID: "C-USER", Name: "user-public", IsMember: true}}},
		},
	}
	ctx := context.Background()
	app, err := service.CreateApp(ctx, "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	bot, err := service.CreateInstallation(ctx, app.ID, "Bot workspace", "T111", "bot-one")
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.CreateInstallation(ctx, app.ID, "User workspace", "T333", "user-three")
	if err != nil {
		t.Fatal(err)
	}

	botFirst, err := service.Channels(ctx, bot.ID, "", 25)
	if err != nil || len(botFirst.Channels) != 1 || botFirst.Channels[0].ID != "C-BOT" ||
		botFirst.Channels[0].Name != "bot-public" || botFirst.Channels[0].IsPrivate ||
		!botFirst.Channels[0].IsMember || botFirst.NextCursor != "bot-next" {
		t.Fatalf("bot first page = %#v, %v", botFirst, err)
	}
	userFirst, err := service.Channels(ctx, user.ID, "", 25)
	if err != nil || len(userFirst.Channels) != 1 || userFirst.Channels[0].ID != "G-USER" ||
		userFirst.Channels[0].Name != "user-private" || !userFirst.Channels[0].IsPrivate ||
		!userFirst.Channels[0].IsMember || userFirst.NextCursor != "user-next" {
		t.Fatalf("user first page = %#v, %v", userFirst, err)
	}
	if _, err := service.Channels(ctx, bot.ID, "", 25); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Channels(ctx, user.ID, "", 25); err != nil {
		t.Fatal(err)
	}
	if provider.channelCalls != 2 {
		t.Fatalf("cached first pages made %d provider calls, want 2", provider.channelCalls)
	}
	botSecond, err := service.Channels(ctx, bot.ID, "bot-next", 25)
	if err != nil || len(botSecond.Channels) != 1 || botSecond.Channels[0].ID != "G-BOT" ||
		!botSecond.Channels[0].IsPrivate || !botSecond.Channels[0].IsMember {
		t.Fatalf("bot second page = %#v, %v", botSecond, err)
	}
	userSecond, err := service.Channels(ctx, user.ID, "user-next", 25)
	if err != nil || len(userSecond.Channels) != 1 || userSecond.Channels[0].ID != "C-USER" ||
		userSecond.Channels[0].IsPrivate || !userSecond.Channels[0].IsMember {
		t.Fatalf("user second page = %#v, %v", userSecond, err)
	}
	if _, err := service.Channels(ctx, bot.ID, "", 50); err != nil {
		t.Fatal(err)
	}
	wantRequests := []channelRequest{
		{token: "bot-one", limit: 25},
		{token: "user-three", limit: 25},
		{token: "bot-one", cursor: "bot-next", limit: 25},
		{token: "user-three", cursor: "user-next", limit: 25},
		{token: "bot-one", limit: 50},
	}
	if fmt.Sprint(provider.channelReqs) != fmt.Sprint(wantRequests) {
		t.Fatalf("channel requests = %#v, want %#v", provider.channelReqs, wantRequests)
	}
	encoded, err := json.Marshal([]ChannelPage{botFirst, userFirst, botSecond, userSecond})
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"bot-one", "user-three"} {
		if strings.Contains(string(encoded), credential) {
			t.Fatalf("channel pages leaked installation credential %q: %s", credential, encoded)
		}
	}
}

func TestChannelRequestCannotRepopulateCacheAfterTokenReplacement(t *testing.T) {
	service, _, _, provider, _ := newTestService()
	blocking := &blockingSlackProvider{
		fakeSlackProvider: provider,
		started:           make(chan struct{}),
		release:           make(chan struct{}),
	}
	service.slack = blocking
	ctx := context.Background()
	app, err := service.CreateApp(ctx, "App", "app-one")
	if err != nil {
		t.Fatal(err)
	}
	installation, err := service.CreateInstallation(ctx, app.ID, "Workspace", "", "bot-one")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := service.Channels(ctx, installation.ID, "", 25)
		done <- err
	}()
	<-blocking.started
	provider.installations["bot-new"] = provider.installations["bot-one"]
	if _, err := service.ReplaceInstallationToken(ctx, installation.ID, "bot-new"); err != nil {
		t.Fatal(err)
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Channels(ctx, installation.ID, "", 25); err != nil {
		t.Fatal(err)
	}
	if provider.channelCalls != 2 {
		t.Fatalf("channel calls = %d, want refetch after token replacement", provider.channelCalls)
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
