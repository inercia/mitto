package slackcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/secrets"
)

func pocImportRequest() PoCImportRequest {
	return PoCImportRequest{AppName: "Imported App", InstallationName: "Imported Team",
		ExpectedTeamID: "T111", AppToken: "app-one", BotToken: "bot-one"}
}

func TestPoCImportTransactionCreatesIdempotentlyWithoutSerializingSecrets(t *testing.T) {
	service, store, credentials, _, _ := newTestService()
	tx, err := service.PreparePoCImport(context.Background(), pocImportRequest())
	if err != nil {
		t.Fatal(err)
	}
	result := tx.Result()
	if !result.AppCreated || !result.InstallationCreated {
		t.Fatalf("first result = %#v", result)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx.Finalize()
	if len(store.doc.Apps) != 1 || len(store.doc.Installations) != 1 {
		t.Fatalf("document = %#v", store.doc)
	}
	if got, _ := credentials.Resolve(secrets.SlackAppCredential(result.AppID, AppTokenCredential)); got != "app-one" {
		t.Fatalf("app credential = %q", got)
	}

	second, err := service.PreparePoCImport(context.Background(), pocImportRequest())
	if err != nil {
		t.Fatal(err)
	}
	secondResult := second.Result()
	if secondResult.AppCreated || secondResult.InstallationCreated || secondResult.AppID != result.AppID || secondResult.InstallationID != result.InstallationID {
		t.Fatalf("idempotent result = %#v, first = %#v", secondResult, result)
	}
	if err := second.Commit(); err != nil {
		t.Fatal(err)
	}
	second.Finalize()
	if len(store.doc.Apps) != 1 || len(store.doc.Installations) != 1 {
		t.Fatalf("idempotent import duplicated records: %#v", store.doc)
	}
	encoded, _ := json.Marshal(secondResult)
	for _, secret := range []string{"app-one", "bot-one"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("result leaked %q: %s", secret, encoded)
		}
	}
}

func TestPoCImportTransactionSelectedRecordsRollbackExactly(t *testing.T) {
	service, store, credentials, provider, _ := newTestService()
	ctx := context.Background()
	app, _ := service.CreateApp(ctx, "Existing App", "app-one")
	installation, _ := service.CreateInstallation(ctx, app.ID, "Existing Team", "T111", "bot-one")
	provider.apps["replacement-app"] = "A111"
	provider.installations["replacement-bot"] = provider.installations["bot-one"]
	prior := cloneDocument(store.doc)

	tx, err := service.PreparePoCImport(ctx, PoCImportRequest{AppID: app.ID, InstallationID: installation.ID,
		ExpectedTeamID: "T111", AppToken: "replacement-app", BotToken: "replacement-bot"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.doc, prior) {
		t.Fatalf("rollback document = %#v, want %#v", store.doc, prior)
	}
	if got, _ := credentials.Resolve(secrets.SlackAppCredential(app.ID, AppTokenCredential)); got != "app-one" {
		t.Fatalf("app rollback = %q", got)
	}
	if got, _ := credentials.Resolve(secrets.SlackInstallationCredential(installation.ID, BotTokenCredential)); got != "bot-one" {
		t.Fatalf("bot rollback = %q", got)
	}
}

func TestPoCImportValidationAndCommitFailuresLeaveStateUnchanged(t *testing.T) {
	service, store, credentials, _, _ := newTestService()
	request := pocImportRequest()
	request.ExpectedTeamID = "T999"
	if _, err := service.PreparePoCImport(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("identity mismatch error = %v", err)
	}
	if len(store.doc.Apps) != 0 || len(credentials.values) != 0 {
		t.Fatalf("validation failure mutated state: %#v %#v", store.doc, credentials.values)
	}
	tx, err := service.PreparePoCImport(context.Background(), pocImportRequest())
	if err != nil {
		t.Fatal(err)
	}
	store.saveErr = errors.New("disk full")
	if err := tx.Commit(); err == nil {
		t.Fatal("Commit succeeded with failing store")
	}
	if len(store.doc.Apps) != 0 || len(credentials.values) != 0 {
		t.Fatalf("commit failure mutated state: %#v %#v", store.doc, credentials.values)
	}
}

func TestPoCImportCommitRejectsConcurrentCatalogChange(t *testing.T) {
	service, _, credentials, provider, _ := newTestService()
	ctx := context.Background()
	app, _ := service.CreateApp(ctx, "Existing App", "app-one")
	installation, _ := service.CreateInstallation(ctx, app.ID, "Existing Team", "T111", "bot-one")
	provider.apps["replacement-app"] = "A111"
	provider.installations["replacement-bot"] = provider.installations["bot-one"]
	tx, err := service.PreparePoCImport(ctx, PoCImportRequest{AppID: app.ID, InstallationID: installation.ID,
		ExpectedTeamID: "T111", AppToken: "replacement-app", BotToken: "replacement-bot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RenameApp(app.ID, "Concurrent rename"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); !errors.Is(err, ErrConflict) {
		t.Fatalf("Commit error = %v, want conflict", err)
	}
	got, _ := service.GetApp(app.ID)
	if got.Name != "Concurrent rename" {
		t.Fatalf("concurrent catalog update was overwritten: %#v", got)
	}
	if value, _ := credentials.Resolve(secrets.SlackAppCredential(app.ID, AppTokenCredential)); value != "app-one" {
		t.Fatalf("app credential changed on conflict: %q", value)
	}
	if value, _ := credentials.Resolve(secrets.SlackInstallationCredential(installation.ID, BotTokenCredential)); value != "bot-one" {
		t.Fatalf("bot credential changed on conflict: %q", value)
	}
}
