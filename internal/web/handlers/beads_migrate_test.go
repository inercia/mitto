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
type migrateStubClient struct {
	stubBeadsClient
	migrateCalls  atomic.Int32
	bootstrapCall atomic.Int32
	lastDir       atomic.Value // string
	migrateErr    error
	bootstrapErr  error
	migrateOut    []byte
	bootstrapOut  []byte
}

func (c *migrateStubClient) MigrateRemote(_ context.Context, dir string) ([]byte, error) {
	c.migrateCalls.Add(1)
	c.lastDir.Store(dir)
	if c.migrateErr != nil {
		return nil, c.migrateErr
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

// newBeadsMigrateHandlers wires a Handlers with a tri-state MittoConfig
// governing the beads-migration kill-switch. The migration path is enabled
// by default (mitto-erry): pass allow=true for the default-on path (no
// beads config block), or false to install an explicit kill-switch
// (Web.Beads.AllowMigrateFromUI == &false).
func newBeadsMigrateHandlers(t *testing.T, c beads.Client, allow bool) *Handlers {
	t.Helper()
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
}
