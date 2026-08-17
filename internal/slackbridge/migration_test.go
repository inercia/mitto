package slackbridge

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackcatalog"
)

type migrationCredentials struct {
	values map[secrets.CredentialRef]string
}

func (c *migrationCredentials) Put(ref secrets.CredentialRef, value string) error {
	c.values[ref] = value
	return nil
}
func (c *migrationCredentials) Resolve(ref secrets.CredentialRef) (string, error) {
	value, ok := c.values[ref]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return value, nil
}
func (c *migrationCredentials) Status(ref secrets.CredentialRef) (secrets.CredentialStatus, error) {
	_, ok := c.values[ref]
	return secrets.CredentialStatus{Configured: ok}, nil
}
func (c *migrationCredentials) Delete(ref secrets.CredentialRef) error {
	if _, ok := c.values[ref]; !ok {
		return secrets.ErrNotFound
	}
	delete(c.values, ref)
	return nil
}

type migrationSlack struct {
	apps          map[string]string
	installations map[string]slackcatalog.InstallationIdentity
}

func (s migrationSlack) ValidateApp(_ context.Context, token string) (string, error) {
	id, ok := s.apps[token]
	if !ok {
		return "", slackcatalog.ErrUnavailable
	}
	return id, nil
}
func (s migrationSlack) ValidateInstallation(_ context.Context, token string) (slackcatalog.InstallationIdentity, error) {
	id, ok := s.installations[token]
	if !ok {
		return slackcatalog.InstallationIdentity{}, slackcatalog.ErrUnavailable
	}
	return id, nil
}
func (migrationSlack) ListPublicChannels(context.Context, string, string, int) (slackcatalog.ChannelPage, error) {
	return slackcatalog.ChannelPage{}, nil
}

type migrationLegacy struct {
	active bool
	events *[]string
}

func (l *migrationLegacy) Active() bool { return l.active }
func (l *migrationLegacy) Start() error {
	l.active = true
	*l.events = append(*l.events, "start")
	return nil
}
func (l *migrationLegacy) Stop() bool {
	if !l.active {
		return false
	}
	l.active = false
	*l.events = append(*l.events, "stop")
	return true
}

type reconcileFunc func(string) error

func (f reconcileFunc) ReconcileSession(id string) error { return f(id) }

type migrationFixture struct {
	store       *session.Store
	catalog     *slackcatalog.Service
	credentials *migrationCredentials
	provider    migrationSlack
	cfg         Config
}

func newMigrationFixture(t *testing.T, enabled bool) migrationFixture {
	t.Helper()
	dir := t.TempDir()
	store, err := session.NewStore(dir + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	const sid = "migration-session"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := store.Loop(sid).Set(&session.LoopPrompt{Prompt: "preserve me", Enabled: enabled,
		Triggers: []session.LoopTrigger{session.TriggerOnCompletion}, DelaySeconds: 60}); err != nil {
		t.Fatal(err)
	}
	credentials := &migrationCredentials{values: map[secrets.CredentialRef]string{}}
	provider := migrationSlack{apps: map[string]string{"env-app": "A123", "old-app": "A123"},
		installations: map[string]slackcatalog.InstallationIdentity{
			"env-bot": {SlackAppID: "A123", TeamID: "T123", TeamName: "Team", BotID: "B123", BotUserID: "U123"},
			"old-bot": {SlackAppID: "A123", TeamID: "T123", TeamName: "Team", BotID: "B123", BotUserID: "U123"},
		}}
	catalog := slackcatalog.NewService(slackcatalog.NewFileStore(dir+"/catalog.json"), credentials, provider, nil)
	return migrationFixture{store: store, catalog: catalog, credentials: credentials, provider: provider,
		cfg: Config{AppToken: "env-app", BotToken: "env-bot", TeamID: "T123", ChannelID: "C123", TargetSessionID: sid}}
}

func TestEnvironmentMigrationSuccessIsOrderedIdempotentAndValueFree(t *testing.T) {
	f := newMigrationFixture(t, true)
	events := []string{}
	legacy := &migrationLegacy{active: true, events: &events}
	reconciler := reconcileFunc(func(id string) error {
		events = append(events, "reconcile")
		if legacy.active {
			t.Fatal("managed reconcile ran before legacy listener stopped")
		}
		loop, err := f.store.Loop(id).Get()
		if err != nil || !loop.IsOnSlack() {
			t.Fatalf("loop at reconcile = %#v, %v", loop, err)
		}
		return nil
	})
	migration := NewEnvironmentMigration(f.cfg, EnvironmentStatus{Present: true, Complete: true}, f.store, f.catalog, legacy, reconciler)
	result, err := migration.Import(context.Background(), EnvironmentImportRequest{AppName: "Imported", InstallationName: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"stop", "reconcile"}) || !result.AppCreated || !result.InstallationCreated || !result.SubscriptionCreated || !result.ManagedActive {
		t.Fatalf("events=%v result=%#v", events, result)
	}
	loop, _ := f.store.Loop(f.cfg.TargetSessionID).Get()
	if loop.Prompt != "preserve me" || !loop.IsOnCompletion() || len(loop.SlackSubscriptions) != 1 || loop.SlackSubscriptions[0].EventMode != session.SlackEventModeAnyHumanMessage {
		t.Fatalf("migrated loop = %#v", loop)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), f.cfg.AppToken) || strings.Contains(string(encoded), f.cfg.BotToken) {
		t.Fatalf("result leaked secrets: %s", encoded)
	}
	second, err := migration.Import(context.Background(), EnvironmentImportRequest{AppName: "Ignored", InstallationName: "Ignored"})
	if err != nil || second.AppCreated || second.InstallationCreated || second.SubscriptionCreated {
		t.Fatalf("second import = %#v, %v", second, err)
	}
	loop, _ = f.store.Loop(f.cfg.TargetSessionID).Get()
	if len(loop.SlackSubscriptions) != 1 {
		t.Fatalf("duplicate subscriptions: %#v", loop.SlackSubscriptions)
	}
	status, err := migration.Status()
	if err != nil || !status.Shadowed || status.Active {
		t.Fatalf("status = %#v, %v", status, err)
	}
}

func TestEnvironmentMigrationReconcileFailureRestoresEverythingAndLegacy(t *testing.T) {
	f := newMigrationFixture(t, true)
	ctx := context.Background()
	app, _ := f.catalog.CreateApp(ctx, "Existing", "old-app")
	installation, _ := f.catalog.CreateInstallation(ctx, app.ID, "Existing Team", "T123", "old-bot")
	priorLoop, _ := f.store.Loop(f.cfg.TargetSessionID).Get()
	priorApps, _ := f.catalog.ListApps()
	priorInstalls, _ := f.catalog.ListInstallations(app.ID)
	events := []string{}
	legacy := &migrationLegacy{active: true, events: &events}
	migration := NewEnvironmentMigration(f.cfg, EnvironmentStatus{Present: true, Complete: true}, f.store, f.catalog, legacy,
		reconcileFunc(func(string) error { events = append(events, "reconcile"); return errors.New("reconcile failed") }))
	_, err := migration.Import(ctx, EnvironmentImportRequest{AppID: app.ID, InstallationID: installation.ID})
	if err == nil {
		t.Fatal("Import succeeded despite reconcile failure")
	}
	if !reflect.DeepEqual(events, []string{"stop", "reconcile", "start"}) || !legacy.active {
		t.Fatalf("handoff events=%v active=%v", events, legacy.active)
	}
	loop, _ := f.store.Loop(f.cfg.TargetSessionID).Get()
	if !reflect.DeepEqual(loop, priorLoop) {
		t.Fatalf("loop rollback = %#v, want %#v", loop, priorLoop)
	}
	apps, _ := f.catalog.ListApps()
	installs, _ := f.catalog.ListInstallations(app.ID)
	if !reflect.DeepEqual(apps, priorApps) || !reflect.DeepEqual(installs, priorInstalls) {
		t.Fatalf("catalog rollback differs")
	}
	if got, _ := f.credentials.Resolve(secrets.SlackAppCredential(app.ID, slackcatalog.AppTokenCredential)); got != "old-app" {
		t.Fatalf("app credential = %q", got)
	}
	if got, _ := f.credentials.Resolve(secrets.SlackInstallationCredential(installation.ID, slackcatalog.BotTokenCredential)); got != "old-bot" {
		t.Fatalf("bot credential = %q", got)
	}
}

func TestEnvironmentStatusTreatsPausedManagedSubscriptionAsShadowing(t *testing.T) {
	f := newMigrationFixture(t, false)
	app, _ := f.catalog.CreateApp(context.Background(), "App", "env-app")
	installation, _ := f.catalog.CreateInstallation(context.Background(), app.ID, "Team", "T123", "env-bot")
	loop, _ := f.store.Loop(f.cfg.TargetSessionID).Get()
	loop.Triggers = append(loop.EffectiveTriggers(), session.TriggerOnSlack)
	loop.SlackSubscriptions = []session.SlackSubscription{{InstallationID: installation.ID, ChannelID: "C123",
		EventMode: session.SlackEventModeAnyHumanMessage, ThreadPolicy: session.SlackThreadPolicyAny}}
	if err := f.store.Loop(f.cfg.TargetSessionID).Set(loop); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	legacy := &migrationLegacy{events: &events}
	migration := NewEnvironmentMigration(f.cfg, EnvironmentStatus{Present: true, Complete: true}, f.store, f.catalog, legacy, reconcileFunc(func(string) error { return nil }))
	status, err := migration.Status()
	if err != nil || !status.Shadowed || status.Active {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestEnvironmentMigrationLifecycleReconcileHandsOffWithoutOverlap(t *testing.T) {
	f := newMigrationFixture(t, true)
	app, _ := f.catalog.CreateApp(context.Background(), "App", "env-app")
	installation, _ := f.catalog.CreateInstallation(context.Background(), app.ID, "Team", "T123", "env-bot")
	loop, _ := f.store.Loop(f.cfg.TargetSessionID).Get()
	loop.Triggers = append(loop.EffectiveTriggers(), session.TriggerOnSlack)
	loop.SlackSubscriptions = []session.SlackSubscription{{InstallationID: installation.ID, ChannelID: "C123",
		EventMode: session.SlackEventModeAnyHumanMessage, ThreadPolicy: session.SlackThreadPolicyAny}}
	if err := f.store.Loop(f.cfg.TargetSessionID).Set(loop); err != nil {
		t.Fatal(err)
	}

	events := []string{}
	legacy := &migrationLegacy{active: true, events: &events}
	migration := NewEnvironmentMigration(f.cfg, EnvironmentStatus{Present: true, Complete: true}, f.store, f.catalog, legacy,
		reconcileFunc(func(string) error {
			events = append(events, "reconcile")
			if legacy.active {
				t.Fatal("managed lifecycle reconcile ran before legacy listener stopped")
			}
			return nil
		}))
	if err := migration.ReconcileSession(f.cfg.TargetSessionID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"stop", "reconcile"}) {
		t.Fatalf("handoff events = %v", events)
	}

	loop.Triggers = []session.LoopTrigger{session.TriggerOnCompletion}
	if err := f.store.Loop(f.cfg.TargetSessionID).Set(loop); err != nil {
		t.Fatal(err)
	}
	if err := migration.ReconcileSession(f.cfg.TargetSessionID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"stop", "reconcile", "reconcile", "start"}) {
		t.Fatalf("fallback events = %v", events)
	}
}
