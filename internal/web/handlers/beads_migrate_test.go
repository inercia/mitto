package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
)

// migrateStubClient records which of MigrateRemote / Bootstrap was called and
// with which working dir, so the handler tests can assert routing by mode.
// It also tracks ReconcileDatabaseMode ordering (mitto-aap): the pre-migration
// reconcile invoked bd (bd dolt remote list / bd config set) which deadlocked
// on a schema-skewed DB, so the fix moved reconcile AFTER the migration.
type migrateStubClient struct {
	stubBeadsClient
	migrateCalls  atomic.Int32
	localCalls    atomic.Int32
	bootstrapCall atomic.Int32
	reconcileCall atomic.Int32
	// Snapshots of migrate/local/bootstrap call counts at the moment
	// ReconcileDatabaseMode was first invoked, so tests can assert that
	// reconcile happens AFTER the migration completes.
	reconcileMigrateSnapshot   atomic.Int32
	reconcileLocalSnapshot     atomic.Int32
	reconcileBootstrapSnapshot atomic.Int32
	lastDir                    atomic.Value // string
	migrateErr                 error
	bootstrapErr               error
	reconcileErr               error
	migrateOut                 []byte
	bootstrapOut               []byte
}

func (c *migrateStubClient) MigrateRemote(_ context.Context, dir string) ([]byte, error) {
	c.migrateCalls.Add(1)
	c.lastDir.Store(dir)
	if c.migrateErr != nil {
		return c.migrateOut, c.migrateErr
	}
	out := c.migrateOut
	if out == nil {
		out = []byte(`{"applied":4}`)
	}
	return out, nil
}

func (c *migrateStubClient) MigrateLocal(_ context.Context, dir string) ([]byte, error) {
	c.localCalls.Add(1)
	c.lastDir.Store(dir)
	if c.migrateErr != nil {
		return c.migrateOut, c.migrateErr
	}
	out := c.migrateOut
	if out == nil {
		out = []byte(`{"applied":4}`)
	}
	return out, nil
}

func (c *migrateStubClient) Bootstrap(_ context.Context, dir string) ([]byte, error) {
	c.bootstrapCall.Add(1)
	c.lastDir.Store(dir)
	if c.bootstrapErr != nil {
		return nil, c.bootstrapErr
	}
	out := c.bootstrapOut
	if out == nil {
		out = []byte(`{"bootstrapped":true}`)
	}
	return out, nil
}

func (c *migrateStubClient) ReconcileDatabaseMode(_ context.Context, _ string, _ config.BeadsDatabaseMode) error {
	if c.reconcileCall.Add(1) == 1 {
		c.reconcileMigrateSnapshot.Store(c.migrateCalls.Load())
		c.reconcileLocalSnapshot.Store(c.localCalls.Load())
		c.reconcileBootstrapSnapshot.Store(c.bootstrapCall.Load())
	}
	return c.reconcileErr
}

// newBeadsMigrateHandlers wires a Handlers with a tri-state MittoConfig
// governing the beads-migration kill-switch. The migration path is enabled
// by default (mitto-erry): pass allow=true for the default-on path (no
// beads config block), or false to install an explicit kill-switch
// (Web.Beads.AllowMigrateFromUI == &false).
func newBeadsMigrateHandlers(t *testing.T, c beads.Client, allow bool) *Handlers {
	t.Helper()
	setupMittoDir(t)
	if err := config.SetFolderBeadsDatabaseMode("/test/workspace", config.BeadsDatabaseModeShared); err != nil {
		t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
	}
	cfg := &config.Config{}
	if !allow {
		f := false
		cfg.Web.Beads = &config.WebBeadsConfig{AllowMigrateFromUI: &f}
	}
	return New(Deps{
		SessionManager: newBeadsTestSM(),
		BeadsClient:    c,
		MittoConfig:    cfg,
	})
}

// postJSON builds a localhost POST request with a JSON body.
func postJSON(t *testing.T, url string, body any) *http.Request {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(buf))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestHandleBeadsMigrate_MethodNotAllowed(t *testing.T) {
	h := newBeadsMigrateHandlers(t, &migrateStubClient{}, true)
	req := httptest.NewRequest(http.MethodGet, "/api/beads/migrate", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestHandleBeadsMigrate_KillSwitch_Forbidden verifies that explicitly
// setting web.beads.allow_migrate_from_ui to false honours the admin
// kill-switch: bd is not invoked and the response cites the flag by name so
// the frontend can render the disabled-by-admin banner. Post-mitto-erry the
// default is on, so this test covers the explicit-off path.
func TestHandleBeadsMigrate_KillSwitch_Forbidden(t *testing.T) {
	stub := &migrateStubClient{}
	h := newBeadsMigrateHandlers(t, stub, false)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if stub.migrateCalls.Load() != 0 {
		t.Errorf("MigrateRemote called %d times, want 0 (kill-switch must gate bd)", stub.migrateCalls.Load())
	}
	if !strings.Contains(w.Body.String(), "allow_migrate_from_ui") {
		t.Errorf("response body missing config-flag hint: %s", w.Body.String())
	}
	// Parse the error envelope and assert the machine-readable code is
	// `migrate_from_ui_disabled` (NOT the generic `forbidden`). Without this
	// mapping the SchemaSkewDialog kill-switch branch — which gates on
	// `data.code === "migrate_from_ui_disabled"` — is dead code. See mitto-erry.
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v (body=%s)", err, w.Body.String())
	}
	if env.Error.Code != "migrate_from_ui_disabled" {
		t.Errorf("error.code = %q, want %q (frontend SchemaSkewDialog gates its kill-switch copy on this code)",
			env.Error.Code, "migrate_from_ui_disabled")
	}
}

// TestHandleBeadsMigrate_DefaultOn_Allowed verifies the mitto-erry default:
// with no MittoConfig set (or with a MittoConfig whose Web.Beads block is
// unset), the migration endpoint is reachable without any opt-in flag. The
// SchemaSkewDialog collects the consent; the flag is a kill-switch only.
func TestHandleBeadsMigrate_DefaultOn_Allowed(t *testing.T) {
	stub := &migrateStubClient{}
	// allow=true here installs no beads config block, exercising the
	// "unset → default on" path (nil MittoConfig.Web.Beads).
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if stub.migrateCalls.Load() != 1 {
		t.Errorf("MigrateRemote called %d times, want 1 (default-on must reach bd)", stub.migrateCalls.Load())
	}
}

// TestHandleBeadsMigrate_ExplicitTrue_Allowed verifies parity between the
// nil (default-on) and *true (explicit-on) config states — an admin who
// spells out the flag as true gets the same behaviour as leaving it unset.
func TestHandleBeadsMigrate_ExplicitTrue_Allowed(t *testing.T) {
	stub := &migrateStubClient{}
	setupMittoDir(t)
	if err := config.SetFolderBeadsDatabaseMode("/test/workspace", config.BeadsDatabaseModeShared); err != nil {
		t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
	}
	cfg := &config.Config{}
	tr := true
	cfg.Web.Beads = &config.WebBeadsConfig{AllowMigrateFromUI: &tr}
	h := New(Deps{
		SessionManager: newBeadsTestSM(),
		BeadsClient:    stub,
		MittoConfig:    cfg,
	})
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if stub.migrateCalls.Load() != 1 {
		t.Errorf("MigrateRemote called %d times, want 1 (explicit-true must reach bd)", stub.migrateCalls.Load())
	}
}

func TestHandleBeadsMigrate_MissingWorkingDir(t *testing.T) {
	h := newBeadsMigrateHandlers(t, &migrateStubClient{}, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{"mode": "migrate"})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsMigrate_RelativeWorkingDir(t *testing.T) {
	h := newBeadsMigrateHandlers(t, &migrateStubClient{}, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "rel/path",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsMigrate_UnknownWorkspace(t *testing.T) {
	h := newBeadsMigrateHandlers(t, &migrateStubClient{}, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/unknown/dir",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsMigrate_InvalidMode(t *testing.T) {
	h := newBeadsMigrateHandlers(t, &migrateStubClient{}, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "delete-everything",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsMigrate_MigrateSuccess(t *testing.T) {
	stub := &migrateStubClient{migrateOut: []byte(`{"applied":4,"from":49,"to":53}`)}
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if stub.migrateCalls.Load() != 1 {
		t.Errorf("MigrateRemote called %d times, want 1", stub.migrateCalls.Load())
	}
	if stub.bootstrapCall.Load() != 0 {
		t.Errorf("Bootstrap called %d times, want 0 for mode=migrate", stub.bootstrapCall.Load())
	}
	if got := stub.lastDir.Load(); got != "/test/workspace" {
		t.Errorf("lastDir = %v, want /test/workspace", got)
	}

	var resp struct {
		Ok     bool            `json:"ok"`
		Mode   string          `json:"mode"`
		Output json.RawMessage `json:"output"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Ok || resp.Mode != "migrate" {
		t.Errorf("response = %+v, want {ok:true, mode:migrate}", resp)
	}
	if !bytes.Contains(resp.Output, []byte(`"applied":4`)) {
		t.Errorf("Output does not carry bd stdout: %s", resp.Output)
	}
}

func TestHandleBeadsMigrate_AdoptSuccess(t *testing.T) {
	stub := &migrateStubClient{}
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "adopt",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if stub.bootstrapCall.Load() != 1 {
		t.Errorf("Bootstrap called %d times, want 1", stub.bootstrapCall.Load())
	}
	if stub.migrateCalls.Load() != 0 {
		t.Errorf("MigrateRemote called %d times, want 0 for mode=adopt", stub.migrateCalls.Load())
	}
}

func TestHandleBeadsMigrate_LocalModeNeverPublishesOrBootstraps(t *testing.T) {
	setupMittoDir(t)
	if err := config.SetFolderBeadsDatabaseMode("/test/workspace", config.BeadsDatabaseModeLocal); err != nil {
		t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
	}
	stub := &migrateStubClient{}
	h := New(Deps{SessionManager: newBeadsTestSM(), BeadsClient: stub, MittoConfig: &config.Config{}})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if stub.localCalls.Load() != 1 || stub.migrateCalls.Load() != 0 || stub.bootstrapCall.Load() != 0 {
		t.Errorf("calls local/remote/bootstrap = %d/%d/%d, want 1/0/0",
			stub.localCalls.Load(), stub.migrateCalls.Load(), stub.bootstrapCall.Load())
	}
}

func TestHandleBeadsMigrate_LocalModeRejectsAdoptWithoutDispatch(t *testing.T) {
	setupMittoDir(t)
	if err := config.SetFolderBeadsDatabaseMode("/test/workspace", config.BeadsDatabaseModeLocal); err != nil {
		t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
	}
	stub := &migrateStubClient{}
	h := New(Deps{SessionManager: newBeadsTestSM(), BeadsClient: stub, MittoConfig: &config.Config{}})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "adopt",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if stub.localCalls.Load()+stub.migrateCalls.Load()+stub.bootstrapCall.Load() != 0 {
		t.Errorf("migration dispatched in local adopt path")
	}
}

func TestHandleBeadsMigrate_MigrateFailure(t *testing.T) {
	stub := &migrateStubClient{migrateErr: &beads.CmdError{
		Err:      errors.New("bd exited with non-zero status"),
		Stderr:   "Error: dolt push refused",
		ExitCode: 1,
	}}
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "server_error" {
		t.Errorf("code = %q, want server_error", env.Error.Code)
	}
	if got, want := env.Error.Details["stderr"], "Error: dolt push refused"; got != want {
		t.Errorf("details.stderr = %v, want %q", got, want)
	}
	if got, want := env.Error.Details["mode"], "migrate"; got != want {
		t.Errorf("details.mode = %v, want %q", got, want)
	}
	// A migrate-stage failure (no Stage set on the CmdError) must NOT be
	// misclassified as a publish failure — see
	// TestHandleBeadsMigrate_PublishFailure for the contrasting case.
	if _, ok := env.Error.Details["stage"]; ok {
		t.Errorf("details.stage = %v, want absent for a migrate-stage failure", env.Error.Details["stage"])
	}
}

// TestHandleBeadsMigrate_PublishFailure covers mitto-cq2n.1: when the local
// "bd migrate schema" step succeeds but "bd dolt push" fails, the error
// envelope must identify the publish stage and that the local migration was
// applied, carry an actionable message (not just the bare exit-status
// wrapper), and preserve the raw stderr — while still returning a non-2xx
// status (never claiming overall success).
func TestHandleBeadsMigrate_PublishFailure(t *testing.T) {
	stub := &migrateStubClient{
		migrateOut: []byte(`{"applied":4,"from":49,"to":53}`),
		migrateErr: &beads.CmdError{
			Err: errors.New("bd exited with non-zero status"),
			Stderr: "Error: push to origin/main: Error 1105: failed to get remote db; " +
				"ERROR: Repository not found.",
			ExitCode: 1,
			Stage:    beads.StagePublish,
		},
	}
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusInternalServerError, w.Body.String())
	}

	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}

	if env.Error.Code != "beads_migrate_publish_failed" {
		t.Errorf("code = %q, want %q", env.Error.Code, "beads_migrate_publish_failed")
	}
	if !strings.Contains(env.Error.Message, "local beads schema migration succeeded") {
		t.Errorf("message = %q, want it to state the local migration succeeded", env.Error.Message)
	}
	if !strings.Contains(env.Error.Message, "publishing") {
		t.Errorf("message = %q, want it to mention publishing failed", env.Error.Message)
	}
	if got, want := env.Error.Details["stage"], "push"; got != want {
		t.Errorf("details.stage = %v, want %q", got, want)
	}
	if got, want := env.Error.Details["local_migration_applied"], true; got != want {
		t.Errorf("details.local_migration_applied = %v, want %v", got, want)
	}
	if got, want := env.Error.Details["stderr"], stub.migrateErr.(*beads.CmdError).Stderr; got != want {
		t.Errorf("details.stderr = %v, want %q", got, want)
	}
	output, ok := env.Error.Details["local_migration_output"].(map[string]any)
	if !ok {
		t.Fatalf("details.local_migration_output = %T, want JSON object", env.Error.Details["local_migration_output"])
	}
	if got, want := output["applied"], float64(4); got != want {
		t.Errorf("details.local_migration_output.applied = %v, want %v", got, want)
	}
}

// TestHandleBeadsMigrate_PublishFailure_AdoptModeNeverClassified verifies the
// publish-failure classification is scoped to mode=migrate: an "adopt" (bd
// bootstrap) failure must never be reported as a publish failure even if the
// underlying CmdError happened to carry beads.StagePublish (defense in
// depth — Bootstrap never sets it in practice).
func TestHandleBeadsMigrate_PublishFailure_AdoptModeNeverClassified(t *testing.T) {
	stub := &migrateStubClient{bootstrapErr: &beads.CmdError{
		Err:      errors.New("bd exited with non-zero status"),
		Stderr:   "Error: bootstrap failed",
		ExitCode: 1,
		Stage:    beads.StagePublish,
	}}
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "adopt",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "server_error" {
		t.Errorf("code = %q, want %q (adopt-mode failures are never publish failures)", env.Error.Code, "server_error")
	}
	if _, ok := env.Error.Details["stage"]; ok {
		t.Errorf("details.stage = %v, want absent for mode=adopt", env.Error.Details["stage"])
	}
}

// TestHandleBeadsMigrate_ReconcileRunsAfterMigrate_mittoAap pins the mitto-aap
// fix: ReconcileDatabaseMode must be invoked AFTER the schema migration, not
// before. The pre-migration ordering deadlocked on a schema-skewed DB because
// reconcile invokes bd (bd dolt remote list / bd config set) which itself
// refuses on a skewed schema — so the migration endpoint used to return HTTP
// 409 beads_schema_skew for the very condition it was meant to fix.
func TestHandleBeadsMigrate_ReconcileRunsAfterMigrate_mittoAap(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		databaseMode  config.BeadsDatabaseMode
		wantMigrate   int32
		wantLocal     int32
		wantBootstrap int32
	}{
		{name: "shared_migrate", mode: "migrate", databaseMode: config.BeadsDatabaseModeShared, wantMigrate: 1},
		{name: "shared_adopt", mode: "adopt", databaseMode: config.BeadsDatabaseModeShared, wantBootstrap: 1},
		{name: "local_migrate", mode: "migrate", databaseMode: config.BeadsDatabaseModeLocal, wantLocal: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupMittoDir(t)
			if err := config.SetFolderBeadsDatabaseMode("/test/workspace", tc.databaseMode); err != nil {
				t.Fatalf("SetFolderBeadsDatabaseMode() error = %v", err)
			}
			stub := &migrateStubClient{}
			h := New(Deps{SessionManager: newBeadsTestSM(), BeadsClient: stub, MittoConfig: &config.Config{}})
			w := httptest.NewRecorder()
			h.HandleBeadsMigrate(w, postJSON(t, "/api/beads/migrate", map[string]string{
				"working_dir": "/test/workspace",
				"mode":        tc.mode,
			}))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
			}
			if stub.reconcileCall.Load() != 1 {
				t.Fatalf("ReconcileDatabaseMode calls = %d, want 1", stub.reconcileCall.Load())
			}
			if got := stub.reconcileMigrateSnapshot.Load(); got != tc.wantMigrate {
				t.Errorf("MigrateRemote calls at reconcile time = %d, want %d (reconcile must run AFTER migrate)", got, tc.wantMigrate)
			}
			if got := stub.reconcileLocalSnapshot.Load(); got != tc.wantLocal {
				t.Errorf("MigrateLocal calls at reconcile time = %d, want %d (reconcile must run AFTER migrate)", got, tc.wantLocal)
			}
			if got := stub.reconcileBootstrapSnapshot.Load(); got != tc.wantBootstrap {
				t.Errorf("Bootstrap calls at reconcile time = %d, want %d (reconcile must run AFTER migrate)", got, tc.wantBootstrap)
			}
		})
	}
}

// TestHandleBeadsMigrate_ReconcileFailureDoesNotFailRequest_mittoAap pins the
// mitto-aap fix: a post-migration ReconcileDatabaseMode failure is best-effort
// and MUST NOT fail the request — the migration itself already succeeded,
// which is what the user asked for. Guards can also be re-reconciled via the
// folder-config UI (HandleBeadsDatabaseMode) if needed.
func TestHandleBeadsMigrate_ReconcileFailureDoesNotFailRequest_mittoAap(t *testing.T) {
	stub := &migrateStubClient{reconcileErr: errors.New("bd config set failed (simulated)")}
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (reconcile failure must be best-effort); body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if stub.migrateCalls.Load() != 1 {
		t.Errorf("MigrateRemote calls = %d, want 1", stub.migrateCalls.Load())
	}
	if stub.reconcileCall.Load() != 1 {
		t.Errorf("ReconcileDatabaseMode calls = %d, want 1 (best-effort still attempted)", stub.reconcileCall.Load())
	}
}

// TestHandleBeadsMigrate_ReconcileNotCalledOnMigrateFailure_mittoAap pins that
// a failed migration short-circuits before reconcile is attempted: reconcile
// on a still-skewed DB would only pile on a spurious warning. The
// migration-failure error envelope is the sole response.
func TestHandleBeadsMigrate_ReconcileNotCalledOnMigrateFailure_mittoAap(t *testing.T) {
	stub := &migrateStubClient{migrateErr: &beads.CmdError{
		Err:      errors.New("bd exited with non-zero status"),
		Stderr:   "Error: migration failed (simulated)",
		ExitCode: 1,
	}}
	h := newBeadsMigrateHandlers(t, stub, true)
	req := postJSON(t, "/api/beads/migrate", map[string]string{
		"working_dir": "/test/workspace",
		"mode":        "migrate",
	})
	w := httptest.NewRecorder()
	h.HandleBeadsMigrate(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if stub.reconcileCall.Load() != 0 {
		t.Errorf("ReconcileDatabaseMode calls = %d, want 0 (must not run after a failed migration)", stub.reconcileCall.Load())
	}
}
