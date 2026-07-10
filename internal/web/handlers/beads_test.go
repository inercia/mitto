package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// beadsCreateParams is a minimal helper to capture title from stubBeadsClient.
type beadsCreateParams struct {
	title string
}

// listErrorClient is a beads.Client that always returns an error from List,
// used to test the canonical 500 envelope on bd-command failure.
type listErrorClient struct{ stubBeadsClient }

func (c *listErrorClient) List(_ context.Context, _ string) ([]byte, error) {
	return nil, errors.New("bd: command failed: exit status 1")
}

// showNotFoundClient is a beads.Client whose Show mimics bd's "issue not found"
// failure: a non-zero exit with the not-found phrase captured on stderr. Used to
// verify that a missing issue id maps to HTTP 404 rather than 500.
type showNotFoundClient struct{ stubBeadsClient }

func (c *showNotFoundClient) Show(_ context.Context, _, id string) ([]byte, error) {
	return nil, &beads.CmdError{
		Err:    errors.New("bd exited with non-zero status"),
		Stderr: `Error fetching ` + id + `: no issue found matching "` + id + `"`,
	}
}

// showInternalErrorClient is a beads.Client whose Show fails with a generic
// error (no not-found marker), verifying such failures still map to HTTP 500.
type showInternalErrorClient struct{ stubBeadsClient }

func (c *showInternalErrorClient) Show(_ context.Context, _, _ string) ([]byte, error) {
	return nil, &beads.CmdError{Err: errors.New("bd exited with non-zero status"), Stderr: "database is locked"}
}

// schemaSkewClient is a beads.Client whose List mimics bd's refusal to
// auto-migrate a remote-backed database that is behind the binary's schema.
// Used to verify that a schema-version skew maps to an actionable HTTP 409
// "needs migration" response instead of a bare 500.
type schemaSkewClient struct{ stubBeadsClient }

func (c *schemaSkewClient) List(_ context.Context, _ string) ([]byte, error) {
	return nil, &beads.CmdError{
		Err: errors.New("bd exited with non-zero status"),
		Stderr: "... refusing to auto-apply 4 pending schema migrations to a remote-backed database (v49 -> v53) ...\n" +
			"Error: failed to open routed store at /Users/test/.beads-planning: schema version mismatch: database is at v49, binary expects v53 ...",
	}
}

// stubBeadsClient implements beads.Client for unit tests.
// All methods except Create are no-ops that return nil / zero values.
type stubBeadsClient struct {
	createFn func(dir string, p beadsCreateParams) ([]byte, error)
	updateFn func(p beads.UpdateParams) error
}

func (c *stubBeadsClient) List(_ context.Context, _ string) ([]byte, error) {
	return []byte(`[]`), nil
}
func (c *stubBeadsClient) Status(_ context.Context, _ string) ([]byte, error) {
	return []byte(`{"summary":{}}`), nil
}
func (c *stubBeadsClient) Show(_ context.Context, _, _ string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *stubBeadsClient) Create(_ context.Context, dir string, p beads.CreateParams) ([]byte, error) {
	if c.createFn != nil {
		return c.createFn(dir, beadsCreateParams{title: p.Title})
	}
	return []byte(`{}`), nil
}
func (c *stubBeadsClient) Delete(_ context.Context, _, _ string) error { return nil }
func (c *stubBeadsClient) ListClosedIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (c *stubBeadsClient) DeleteIDs(_ context.Context, _ string, _ []string) error { return nil }
func (c *stubBeadsClient) SetStatus(_ context.Context, _, _, _ string) error       { return nil }
func (c *stubBeadsClient) Update(_ context.Context, _ string, p beads.UpdateParams) error {
	if c.updateFn != nil {
		return c.updateFn(p)
	}
	return nil
}
func (c *stubBeadsClient) Comment(_ context.Context, _, _, _ string) error { return nil }
func (c *stubBeadsClient) Dep(_ context.Context, _ string, _ beads.DepParams) error {
	return nil
}
func (c *stubBeadsClient) Label(_ context.Context, _ string, _ beads.LabelParams) error {
	return nil
}
func (c *stubBeadsClient) ListAllLabels(_ context.Context, _ string) ([]byte, error) {
	return []byte(`[]`), nil
}
func (c *stubBeadsClient) ConfigShow(_ context.Context, _ string) (map[string]string, error) {
	return nil, nil
}
func (c *stubBeadsClient) ConfigSet(_ context.Context, _, _, _ string) error   { return nil }
func (c *stubBeadsClient) ConfigUnset(_ context.Context, _, _ string) error    { return nil }
func (c *stubBeadsClient) EnsureInitialized(_ context.Context, _ string) error { return nil }
func (c *stubBeadsClient) Sync(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

// setupMittoDir points MITTO_DIR at a fresh temp dir and resets the appdir
// cache so folders.json reads/writes are isolated per test.
func setupMittoDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	return tmpDir
}

// newBeadsTestSM returns a session manager with one known workspace at
// /test/workspace.
func newBeadsTestSM() *conversation.SessionManager {
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/test/workspace", ACPServer: "test-server"},
	})
	return sm
}

// newBeadsTestServer returns a *Handlers with a session manager that has one
// known workspace at /test/workspace.
func newBeadsTestServer() *Handlers {
	return New(Deps{SessionManager: newBeadsTestSM()})
}

// newBeadsTestServerWithClient returns a *Handlers wired with the given beads
// client and the standard one-workspace session manager.
func newBeadsTestServerWithClient(c beads.Client) *Handlers {
	return New(Deps{SessionManager: newBeadsTestSM(), BeadsClient: c})
}

// Lowercase aliases so the migrated test bodies can keep calling the handlers
// by their original (pre-extraction) names.
func (h *Handlers) handleBeadsList(w http.ResponseWriter, r *http.Request)  { h.HandleBeadsList(w, r) }
func (h *Handlers) handleBeadsStats(w http.ResponseWriter, r *http.Request) { h.HandleBeadsStats(w, r) }
func (h *Handlers) handleBeadsShow(w http.ResponseWriter, r *http.Request)  { h.HandleBeadsShow(w, r) }
func (h *Handlers) handleBeadsCreate(w http.ResponseWriter, r *http.Request) {
	h.HandleBeadsCreate(w, r)
}
func (h *Handlers) handleBeadsCleanup(w http.ResponseWriter, r *http.Request) {
	h.HandleBeadsCleanup(w, r)
}
func (h *Handlers) handleBeadsDelete(w http.ResponseWriter, r *http.Request) {
	h.HandleBeadsDelete(w, r)
}
func (h *Handlers) handleBeadsStatus(w http.ResponseWriter, r *http.Request) {
	h.HandleBeadsStatus(w, r)
}
func (h *Handlers) handleBeadsUpdate(w http.ResponseWriter, r *http.Request) {
	h.HandleBeadsUpdate(w, r)
}
func (h *Handlers) handleBeadsDep(w http.ResponseWriter, r *http.Request) { h.HandleBeadsDep(w, r) }
func (h *Handlers) handleBeadsConfig(w http.ResponseWriter, r *http.Request) {
	h.HandleBeadsConfig(w, r)
}
func (h *Handlers) handleBeadsUpstream(w http.ResponseWriter, r *http.Request) {
	h.HandleBeadsUpstream(w, r)
}
func (h *Handlers) handleBeadsSync(w http.ResponseWriter, r *http.Request) { h.HandleBeadsSync(w, r) }

// localhostRequest creates a GET request arriving from localhost.
func localhostRequest(url string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	return req
}

// --- handleBeadsList ---------------------------------------------------------

func TestHandleBeadsList_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsList_MissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "bad_request")
	}
	if resp.Error.Message != "working_dir is required" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "working_dir is required")
	}
}

func TestHandleBeadsList_RelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues?working_dir=relative/path")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsList_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues?working_dir=/unknown/dir")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsList_BdMissingReturnsJSONError(t *testing.T) {
	// bd may or may not be present in the test environment.
	// On success: 200 (bd returns JSON). On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsList_BdCommandError_ReturnsServerError(t *testing.T) {
	// Deterministic failure via stub: List returns an error → canonical 500 envelope.
	s := newBeadsTestServerWithClient(&listErrorClient{})
	req := localhostRequest("/api/issues?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "server_error" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "server_error")
	}
	if env.Error.Message == "" {
		t.Error("error.message should be non-empty")
	}
}

func TestHandleBeadsList_SchemaSkew(t *testing.T) {
	// Deterministic schema-version skew via stub: List returns a schema-skew
	// error → actionable 409 envelope with the DB path, not a bare 500.
	s := newBeadsTestServerWithClient(&schemaSkewClient{})
	req := localhostRequest("/api/issues?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "beads_schema_skew" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "beads_schema_skew")
	}
	if got, want := env.Error.Details["db_path"], "/Users/test/.beads-planning"; got != want {
		t.Errorf("error.details.db_path = %v, want %q", got, want)
	}
}

// listTimeoutClient is a beads.Client whose List blocks until ctx is done.
type listTimeoutClient struct{ stubBeadsClient }

func (c *listTimeoutClient) List(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestHandleBeadsList_Timeout_ReturnsRetryable503(t *testing.T) {
	old := auxBackedRequestTimeout
	auxBackedRequestTimeout = 20 * time.Millisecond
	defer func() { auxBackedRequestTimeout = old }()

	s := newBeadsTestServerWithClient(&listTimeoutClient{})
	req := localhostRequest("/api/issues?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Error("Retry-After header not set")
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "unavailable" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "unavailable")
	}
}

// TestHandleBeadsList_PersistentError_LogsError verifies AC1: a persistent
// (non-not-found, non-timeout) bd failure both returns the canonical 500
// envelope AND emits a backend error log carrying the underlying cause, so
// the failure is never silent in mitto.log (regression test for mitto-rxd).
func TestHandleBeadsList_PersistentError_LogsError(t *testing.T) {
	old := beadsReadRetries
	beadsReadRetries = 0 // fail immediately, no retries needed for this test
	defer func() { beadsReadRetries = old }()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	sm := newBeadsTestSM()
	s := New(Deps{SessionManager: sm, BeadsClient: &listErrorClient{}, Logger: logger})

	req := localhostRequest("/api/issues?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "level=ERROR") {
		t.Errorf("log output = %q, want it to contain %q", logged, "level=ERROR")
	}
	if !strings.Contains(logged, "beads command failed") {
		t.Errorf("log output = %q, want it to contain %q", logged, "beads command failed")
	}
	if !strings.Contains(logged, "bd: command failed: exit status 1") {
		t.Errorf("log output = %q, want it to contain the underlying error", logged)
	}
}

// flakyListClient's List fails failCount times, then succeeds. Used to verify
// AC2: transient bd failures are retried instead of surfacing as bare 500s.
type flakyListClient struct {
	stubBeadsClient
	failCount int32
	calls     int32
}

func (c *flakyListClient) List(_ context.Context, _ string) ([]byte, error) {
	n := atomic.AddInt32(&c.calls, 1)
	if n <= c.failCount {
		return nil, errors.New("bd: transient dolt error")
	}
	return []byte(`[]`), nil
}

// TestHandleBeadsList_TransientFailure_RetriesThenSucceeds verifies AC2: a
// fail-then-succeed sequence from a read-only bd query is retried internally
// and returns HTTP 200 to the client, rather than a bare 500.
func TestHandleBeadsList_TransientFailure_RetriesThenSucceeds(t *testing.T) {
	oldRetries := beadsReadRetries
	oldBackoff := beadsRetryBackoff
	beadsReadRetries = 2
	beadsRetryBackoff = time.Millisecond
	defer func() {
		beadsReadRetries = oldRetries
		beadsRetryBackoff = oldBackoff
	}()

	client := &flakyListClient{failCount: 2}
	s := newBeadsTestServerWithClient(client)
	req := localhostRequest("/api/issues?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := atomic.LoadInt32(&client.calls); got != 3 {
		t.Errorf("List call count = %d, want 3 (2 failures + 1 success)", got)
	}
}

// TestHandleBeadsList_PersistentTransientFailure_ExhaustsRetries verifies that
// when every attempt fails, the handler still returns the canonical 500
// envelope after exhausting beadsReadRetries (not an infinite/unbounded retry).
func TestHandleBeadsList_PersistentTransientFailure_ExhaustsRetries(t *testing.T) {
	oldRetries := beadsReadRetries
	oldBackoff := beadsRetryBackoff
	beadsReadRetries = 2
	beadsRetryBackoff = time.Millisecond
	defer func() {
		beadsReadRetries = oldRetries
		beadsRetryBackoff = oldBackoff
	}()

	client := &flakyListClient{failCount: 100}
	s := newBeadsTestServerWithClient(client)
	req := localhostRequest("/api/issues?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsList(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if got := atomic.LoadInt32(&client.calls); got != 3 {
		t.Errorf("List call count = %d, want 3 (1 initial + 2 retries)", got)
	}
}

// --- handleBeadsStats --------------------------------------------------------

func TestHandleBeadsStats_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/stats", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsStats(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsStats_MissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/stats")
	w := httptest.NewRecorder()
	s.handleBeadsStats(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "bad_request")
	}
	if resp.Error.Message != "working_dir is required" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "working_dir is required")
	}
}

func TestHandleBeadsStats_RelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/stats?working_dir=relative/path")
	w := httptest.NewRecorder()
	s.handleBeadsStats(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsStats_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/stats?working_dir=/unknown/dir")
	w := httptest.NewRecorder()
	s.handleBeadsStats(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestHandleBeadsStats_StubReturnsSummary injects a stub client so the success
// path is deterministic: a known workspace returns 200 with the summary JSON.
func TestHandleBeadsStats_StubReturnsSummary(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/test/workspace", ACPServer: "test-server"},
	})
	s := New(Deps{SessionManager: sm, BeadsClient: &stubBeadsClient{}})

	req := localhostRequest("/api/issues/stats?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, `"summary"`) {
		t.Errorf("body = %q, want it to contain %q", body, `"summary"`)
	}
}

// --- handleBeadsShow ---------------------------------------------------------

func TestHandleBeadsShow_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsShow(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsShow_MissingID(t *testing.T) {
	// No PathValue("id") set on the request → the handler should treat the id
	// as missing and return 400 "id is required".
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsShow(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "bad_request")
	}
	if resp.Error.Message != "id is required" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "id is required")
	}
}

func TestHandleBeadsShow_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/abc-1?working_dir=/unknown/dir")
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsShow(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestHandleBeadsShow_NotFound verifies that a missing issue id (bd fails with
// "no issue found matching") maps to HTTP 404 with the not_found envelope,
// rather than the generic 500 (regression test for mitto-2pb).
func TestHandleBeadsShow_NotFound(t *testing.T) {
	s := newBeadsTestServerWithClient(&showNotFoundClient{})
	req := localhostRequest("/api/issues/mitto-cam?working_dir=/test/workspace")
	req.SetPathValue("id", "mitto-cam")
	w := httptest.NewRecorder()
	s.handleBeadsShow(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "not_found" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "not_found")
	}
}

// TestHandleBeadsShow_InternalError verifies that a genuine bd failure (not a
// missing issue) still maps to HTTP 500, so 404 is reserved for not-found.
func TestHandleBeadsShow_InternalError(t *testing.T) {
	s := newBeadsTestServerWithClient(&showInternalErrorClient{})
	req := localhostRequest("/api/issues/mitto-cam?working_dir=/test/workspace")
	req.SetPathValue("id", "mitto-cam")
	w := httptest.NewRecorder()
	s.handleBeadsShow(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "server_error" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "server_error")
	}
}

// showEmptyStderrClient mimics the mitto-edk failure mode: bd exits non-zero
// with an empty stdout AND empty stderr for a missing issue (no diagnostic
// text at all). The handler must treat this as a 404 rather than an opaque 500.
type showEmptyStderrClient struct{ stubBeadsClient }

func (c *showEmptyStderrClient) Show(_ context.Context, _, _ string) ([]byte, error) {
	return nil, &beads.CmdError{
		Err:      errors.New("bd exited with non-zero status"),
		Stderr:   "",
		ExitCode: 1,
	}
}

// TestHandleBeadsShow_EmptyStderrTreatedAsNotFound is the mitto-edk regression
// test: bd exiting non-zero with empty output must produce a 404 with a WARN
// log entry, not an opaque 500 (regression test for mitto-edk).
func TestHandleBeadsShow_EmptyStderrTreatedAsNotFound(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	sm := newBeadsTestSM()
	s := New(Deps{SessionManager: sm, BeadsClient: &showEmptyStderrClient{}, Logger: logger})

	req := localhostRequest("/api/issues/mitto-bbi?working_dir=/test/workspace")
	req.SetPathValue("id", "mitto-bbi")
	w := httptest.NewRecorder()
	s.handleBeadsShow(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "not_found" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "not_found")
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Errorf("log output = %q, want it to contain %q", logged, "level=WARN")
	}
	if !strings.Contains(logged, "treating as not found") {
		t.Errorf("log output = %q, want it to contain %q", logged, "treating as not found")
	}
	if !strings.Contains(logged, "exit_code=1") {
		t.Errorf("log output = %q, want it to contain %q", logged, "exit_code=1")
	}
}

// showPluralJSONNotFoundClient mimics bd emitting a plural JSON error object
// on stdout (captured into Stderr by diagnosticOutput) for a missing issue.
type showPluralJSONNotFoundClient struct{ stubBeadsClient }

func (c *showPluralJSONNotFoundClient) Show(_ context.Context, _, _ string) ([]byte, error) {
	return nil, &beads.CmdError{
		Err:      errors.New("bd exited with non-zero status"),
		Stderr:   `{"error":"no issues found matching the provided IDs","schema_version":1}`,
		ExitCode: 1,
	}
}

// TestHandleBeadsShow_PluralJSONErrorTreatedAsNotFound is the mitto-edk
// regression test for the JSON-error variant: bd's plural JSON error object
// must be recognized by IsNotFound and produce a 404 rather than a 500.
func TestHandleBeadsShow_PluralJSONErrorTreatedAsNotFound(t *testing.T) {
	s := newBeadsTestServerWithClient(&showPluralJSONNotFoundClient{})
	req := localhostRequest("/api/issues/mitto-bbi?working_dir=/test/workspace")
	req.SetPathValue("id", "mitto-bbi")
	w := httptest.NewRecorder()
	s.handleBeadsShow(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "not_found" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "not_found")
	}
}

// --- handleBeadsCreate -------------------------------------------------------

func TestHandleBeadsCreate_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/issues?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsCreate_InvalidBody(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=/test/workspace",
		strings.NewReader(`not-json`))
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCreate_BothEmpty(t *testing.T) {
	// Both title and description empty (or whitespace-only) → 400.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=/test/workspace",
		strings.NewReader(`{"title":"   "}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "title or description is required" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "title or description is required")
	}
}

func TestHandleBeadsCreate_EmptyTitleWithDescription_FallbackTitle(t *testing.T) {
	// Empty title + non-empty description: conversation.GenerateQuickTitle fallback is used
	// (no auxiliaryManager wired), and the request reaches bd.Create → 200.
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/test/workspace", ACPServer: "test-server"},
	})

	// Capture the title that was passed to bd.Create.
	var capturedTitle string
	mock := &stubBeadsClient{
		createFn: func(_ string, p beadsCreateParams) ([]byte, error) {
			capturedTitle = p.title
			return []byte(`{}`), nil
		},
	}

	s := New(Deps{SessionManager: sm, BeadsClient: mock})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=/test/workspace",
		strings.NewReader(`{"title":"","description":"Fix the authentication bug in the login flow"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if capturedTitle == "" {
		t.Error("expected a non-empty title to be passed to bd.Create")
	}
	// The quick-title fallback should derive something meaningful from the description.
	if capturedTitle == "New Issue" {
		// Only acceptable if conversation.GenerateQuickTitle returned ""; log but don't fail hard.
		t.Logf("note: capturedTitle=%q (last-resort fallback used)", capturedTitle)
	}
}

func TestHandleBeadsCreate_EmptyTitleNoDescriptionWhitespace_Rejected(t *testing.T) {
	// Explicitly: only description whitespace → both empty → 400.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=/test/workspace",
		strings.NewReader(`{"title":"","description":"   "}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCreate_MissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues",
		strings.NewReader(`{"title":"Test"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "working_dir is required" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "working_dir is required")
	}
}

func TestHandleBeadsCreate_RelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=relative/path",
		strings.NewReader(`{"title":"Test"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCreate_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=/unknown/dir",
		strings.NewReader(`{"title":"Test"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCreate_NilSessionManager(t *testing.T) {
	s := New(Deps{})
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=/test/workspace",
		strings.NewReader(`{"title":"Test"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCreate_BdErrorReturnsJSONError(t *testing.T) {
	// Valid request reaching bd execution — bd may or may not be present.
	// On success: 200 (bd returns JSON). On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues?working_dir=/test/workspace",
		strings.NewReader(`{"title":"Test issue"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsCreate(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

// --- handleBeadsCleanup ------------------------------------------------------

func TestHandleBeadsCleanup_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/issues/cleanup?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsCleanup(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsCleanup_MissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/cleanup", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsCleanup(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCleanup_RelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/cleanup?working_dir=relative/path", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsCleanup(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCleanup_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/cleanup?working_dir=/unknown/dir", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsCleanup(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsCleanup_BdErrorReturnsJSONError(t *testing.T) {
	// Valid request reaching bd execution — bd may or may not be present.
	// On success with empty closed list: 200. On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/cleanup?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsCleanup(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

// --- handleBeadsDelete -------------------------------------------------------

func TestHandleBeadsDelete_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsDelete(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsDelete_MissingID(t *testing.T) {
	// No path value set → id = "" → 400.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodDelete, "/api/issues?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "id is required" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "id is required")
	}
}

func TestHandleBeadsDelete_MissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/abc-1", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDelete_RelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/abc-1?working_dir=relative/path", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDelete_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/abc-1?working_dir=/unknown/dir", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- handleBeadsStatus -------------------------------------------------------

func TestHandleBeadsStatus_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/issues/abc-1/status?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsStatus(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsStatus_InvalidBody(t *testing.T) {
	// working_dir + id are validated before body decode — supply valid values so
	// the request reaches the body-decode step.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/status?working_dir=/test/workspace",
		strings.NewReader(`not-json`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsStatus_MissingID(t *testing.T) {
	// No path value set → id = "" → isValidBeadsIssueRef fails → 400.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues//status?working_dir=/test/workspace",
		strings.NewReader(`{"action":"close"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsStatus_InvalidAction(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/status?working_dir=/test/workspace",
		strings.NewReader(`{"action":"frobnicate"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	const wantMsg = "action must be 'close', 'reopen', 'defer' or 'undefer'"
	if env.Error.Message != wantMsg {
		t.Errorf("error.message = %q, want %q", env.Error.Message, wantMsg)
	}
}

func TestHandleBeadsStatus_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/status?working_dir=/unknown/dir",
		strings.NewReader(`{"action":"close"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsStatus_DeferActionAccepted(t *testing.T) {
	// "defer" is a valid action — the request reaches bd execution.
	// On success: 200. On bd error: 500 (canonical envelope). Never 4xx.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/status?working_dir=/test/workspace",
		strings.NewReader(`{"action":"defer"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsStatus(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsStatus_UndeferActionAccepted(t *testing.T) {
	// "undefer" is a valid action — same expectation as defer above.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/status?working_dir=/test/workspace",
		strings.NewReader(`{"action":"undefer"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsStatus(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

// --- handleBeadsUpdate -------------------------------------------------------

func TestHandleBeadsUpdate_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsUpdate_InvalidBody(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`not-json`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpdate_MissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1",
		strings.NewReader(`{"description":"x"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpdate_MissingID(t *testing.T) {
	// No path value set → id = "" → 400.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues?working_dir=/test/workspace",
		strings.NewReader(`{"description":"x"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpdate_MissingDescription(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpdate_RelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=relative/path",
		strings.NewReader(`{"description":"x"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpdate_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/unknown/dir",
		strings.NewReader(`{"description":"x"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpdate_EmptyDescriptionAllowed(t *testing.T) {
	// An empty (but present) description is valid — never a 4xx for the empty value.
	// On bd success: 200. On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"description":""}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsUpdate_EmptyTitleRejected(t *testing.T) {
	// A present but blank title is rejected — bd requires a non-empty title.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"title":"  "}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpdate_TitleOnlyAllowed(t *testing.T) {
	// A non-empty title with no description is valid — never a 4xx for this.
	// On bd success: 200. On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"title":"New title"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsUpdate_PriorityOnlyAllowed(t *testing.T) {
	// A priority-only update is valid — never a 4xx for the value itself.
	// On bd success: 200. On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"priority":0}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsUpdate_PriorityOutOfRangeRejected(t *testing.T) {
	// A priority outside the 0-4 range is rejected before reaching bd execution.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"priority":7}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "priority must be between 0 and 4" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "priority must be between 0 and 4")
	}
}

func TestHandleBeadsUpdate_AssigneeOnlyAllowed(t *testing.T) {
	// An assignee-only update is valid — never a 4xx for this.
	// On bd success: 200. On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"assignee":"alice"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsUpdate_EmptyAssigneeAllowed(t *testing.T) {
	// An empty (but present) assignee is valid — it clears the field.
	// On bd success: 200. On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"assignee":""}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsUpdate_TypeAccepted(t *testing.T) {
	// A type-only update must be accepted (HTTP 200) and the captured
	// UpdateParams.Type must equal the submitted value.
	setupMittoDir(t)
	var captured beads.UpdateParams
	s := newBeadsTestServerWithClient(&stubBeadsClient{
		updateFn: func(p beads.UpdateParams) error {
			captured = p
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/abc-1?working_dir=/test/workspace",
		strings.NewReader(`{"type":"bug"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if captured.Type == nil {
		t.Fatal("UpdateParams.Type is nil; want non-nil")
	}
	if *captured.Type != "bug" {
		t.Errorf("UpdateParams.Type = %q, want %q", *captured.Type, "bug")
	}
}

// --- handleBeadsDep ----------------------------------------------------------

func TestHandleBeadsDep_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/issues/abc-1/dependencies?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsDep_InvalidBody(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies?working_dir=/test/workspace",
		strings.NewReader(`not-json`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDep_MissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies",
		strings.NewReader(`{"depends_on":"abc-2","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDep_RelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies?working_dir=relative/path",
		strings.NewReader(`{"depends_on":"abc-2","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDep_MissingID(t *testing.T) {
	// No path value set → id = "" → fails isValidBeadsIssueRef → 400.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues//dependencies?working_dir=/test/workspace",
		strings.NewReader(`{"depends_on":"abc-2","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDep_MissingDependsOn(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies?working_dir=/test/workspace",
		strings.NewReader(`{"depends_on":"","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "depends_on is required" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "depends_on is required")
	}
}

func TestHandleBeadsDep_FlagLikeID(t *testing.T) {
	// A leading-dash id in the path must be rejected to prevent flag injection.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/--force/dependencies?working_dir=/test/workspace",
		strings.NewReader(`{"depends_on":"abc-2","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "--force")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDep_InvalidAction(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies?working_dir=/test/workspace",
		strings.NewReader(`{"depends_on":"abc-2","action":"frobnicate"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "action must be 'add' or 'remove'" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "action must be 'add' or 'remove'")
	}
}

func TestHandleBeadsDep_InvalidType(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies?working_dir=/test/workspace",
		strings.NewReader(`{"depends_on":"abc-2","type":"bogus","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDep_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies?working_dir=/unknown/dir",
		strings.NewReader(`{"depends_on":"abc-2","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsDep_ExternalRefAccepted(t *testing.T) {
	// An external reference (external:<project>:<capability>) passes validation —
	// never a 4xx for the colon-bearing ref itself.
	// On bd success: 200. On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/abc-1/dependencies?working_dir=/test/workspace",
		strings.NewReader(`{"depends_on":"external:beads:mol-run","action":"add"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.SetPathValue("id", "abc-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsDep(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

// --- handleBeadsConfig -------------------------------------------------------

func TestHandleBeadsConfig_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/config", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsConfig_GetMissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/config")
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "working_dir is required" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "working_dir is required")
	}
}

func TestHandleBeadsConfig_GetRelativeWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/config?working_dir=relative/path")
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsConfig_GetUnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/config?working_dir=/unknown/dir")
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsConfig_GetKnownWorkspace(t *testing.T) {
	// bd may or may not be present.
	// On bd success: 200 (JSON config). On bd error: 500 (canonical envelope).
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/config?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", w.Code)
	}
}

func TestHandleBeadsConfig_SetInvalidBody(t *testing.T) {
	// working_dir is now validated before body decode, so supply a valid working_dir
	// in the query so the request reaches the body-decode step.
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPut, "/api/issues/config?working_dir=/test/workspace",
		strings.NewReader(`not-json`))
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsConfig_SetMissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPut, "/api/issues/config",
		strings.NewReader(`{"key":"jira.url","value":"https://x"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsConfig_SetInvalidKey(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPut, "/api/issues/config?working_dir=/test/workspace",
		strings.NewReader(`{"key":"--force","value":"x"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	if env.Error.Message != "invalid config key" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "invalid config key")
	}
}

func TestHandleBeadsConfig_SetUnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPut, "/api/issues/config?working_dir=/unknown/dir",
		strings.NewReader(`{"key":"jira.url","value":"x"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsConfig_UnsetMissingKey(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/config?working_dir=/test/workspace", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsConfig_UnsetUnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/config?working_dir=/unknown/dir&key=jira.url", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- handleBeadsUpstream -----------------------------------------------------

func TestHandleBeadsUpstream_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/upstream", nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.handleBeadsUpstream(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsUpstream_GetMissingWorkingDir(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/upstream")
	w := httptest.NewRecorder()
	s.handleBeadsUpstream(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpstream_GetUnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/upstream?working_dir=/unknown/dir")
	w := httptest.NewRecorder()
	s.handleBeadsUpstream(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpstream_GetKnownDefaultsToNone(t *testing.T) {
	setupMittoDir(t)
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/upstream?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsUpstream(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"upstream":"none"`) {
		t.Errorf("body = %q, want upstream none", w.Body.String())
	}
}

func TestHandleBeadsUpstream_SetInvalidUpstream(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"trello"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpstream(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	const wantMsg = "upstream must be one of: none, jira, github, gitlab, linear, prompts"
	if env.Error.Message != wantMsg {
		t.Errorf("error.message = %q, want %q", env.Error.Message, wantMsg)
	}
}

func TestHandleBeadsUpstream_SetUnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/unknown/dir",
		strings.NewReader(`{"upstream":"jira"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsUpstream(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsUpstream_SetThenGetRoundTrip(t *testing.T) {
	setupMittoDir(t)
	s := newBeadsTestServer()

	put := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"jira"}`))
	put.RemoteAddr = "127.0.0.1:1"
	put.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	s.handleBeadsUpstream(pw, put)
	if pw.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d (%s)", pw.Code, http.StatusOK, pw.Body.String())
	}

	get := localhostRequest("/api/issues/upstream?working_dir=/test/workspace")
	gw := httptest.NewRecorder()
	s.handleBeadsUpstream(gw, get)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", gw.Code, http.StatusOK)
	}
	if !strings.Contains(gw.Body.String(), `"upstream":"jira"`) {
		t.Errorf("GET body = %q, want upstream jira", gw.Body.String())
	}
}

func TestHandleBeadsUpstream_SetPromptsUpstream_AllEmpty(t *testing.T) {
	// All three prompt names empty is allowed — operation simply unconfigured.
	setupMittoDir(t)
	s := newBeadsTestServer()

	put := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"prompts","pull_prompt":"","push_prompt":"","sync_prompt":""}`))
	put.RemoteAddr = "127.0.0.1:1"
	put.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	s.handleBeadsUpstream(pw, put)
	if pw.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d (%s)", pw.Code, http.StatusOK, pw.Body.String())
	}
	if !strings.Contains(pw.Body.String(), `"upstream":"prompts"`) {
		t.Errorf("PUT body = %q, want upstream prompts", pw.Body.String())
	}
}

func TestHandleBeadsUpstream_SetPromptsUpstream_NonExistentPrompt(t *testing.T) {
	// A non-existent prompt name must be rejected with 400.
	setupMittoDir(t)
	s := newBeadsTestServer()

	put := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"prompts","pull_prompt":"does-not-exist"}`))
	put.RemoteAddr = "127.0.0.1:1"
	put.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	s.handleBeadsUpstream(pw, put)
	if pw.Code != http.StatusBadRequest {
		t.Errorf("PUT status = %d, want %d (%s)", pw.Code, http.StatusBadRequest, pw.Body.String())
	}
}

func TestHandleBeadsUpstream_SetPromptsUpstream_ParameterizedPromptAccepted(t *testing.T) {
	// A prompt with parameters must now be ACCEPTED — its arguments are
	// supplied via *_prompt_args and forwarded at dispatch time.
	setupMittoDir(t)
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/test/workspace", ACPServer: "test-server"},
	})

	required := true
	paramPrompt := config.WebPrompt{
		Name:   "parameterized-prompt",
		Prompt: "do something with ${id}",
		Parameters: []config.PromptParameter{
			{Name: "id", Type: "text", Required: &required},
		},
	}
	s := New(Deps{
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return []config.WebPrompt{paramPrompt}
		},
	})

	put := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"prompts","pull_prompt":"parameterized-prompt","pull_prompt_args":{"id":"mitto-1"}}`))
	put.RemoteAddr = "127.0.0.1:1"
	put.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	s.handleBeadsUpstream(pw, put)
	if pw.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d (%s)", pw.Code, http.StatusOK, pw.Body.String())
	}

	// GET must return the stored pull_prompt and its args.
	get := localhostRequest("/api/issues/upstream?working_dir=/test/workspace")
	gw := httptest.NewRecorder()
	s.handleBeadsUpstream(gw, get)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", gw.Code, http.StatusOK)
	}
	body := gw.Body.String()
	if !strings.Contains(body, `"pull_prompt":"parameterized-prompt"`) {
		t.Errorf("GET body = %q, want pull_prompt parameterized-prompt", body)
	}
	if !strings.Contains(body, `"pull_prompt_args":{"id":"mitto-1"}`) {
		t.Errorf("GET body = %q, want pull_prompt_args {\"id\":\"mitto-1\"}", body)
	}
}

func TestHandleBeadsUpstream_SetPromptsUpstream_ValidPromptRoundTrip(t *testing.T) {
	// A valid (no-param) prompt name must be accepted and round-tripped via GET.
	setupMittoDir(t)
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/test/workspace", ACPServer: "test-server"},
	})

	noParamPrompt := config.WebPrompt{
		Name:   "my-pull-prompt",
		Prompt: "run the pull operation",
	}
	s := New(Deps{
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return []config.WebPrompt{noParamPrompt}
		},
	})

	put := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"prompts","pull_prompt":"my-pull-prompt"}`))
	put.RemoteAddr = "127.0.0.1:1"
	put.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	s.handleBeadsUpstream(pw, put)
	if pw.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d (%s)", pw.Code, http.StatusOK, pw.Body.String())
	}

	// GET must return upstream=prompts and the stored pull_prompt.
	get := localhostRequest("/api/issues/upstream?working_dir=/test/workspace")
	gw := httptest.NewRecorder()
	s.handleBeadsUpstream(gw, get)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", gw.Code, http.StatusOK)
	}
	body := gw.Body.String()
	if !strings.Contains(body, `"upstream":"prompts"`) {
		t.Errorf("GET body = %q, want upstream prompts", body)
	}
	if !strings.Contains(body, `"pull_prompt":"my-pull-prompt"`) {
		t.Errorf("GET body = %q, want pull_prompt my-pull-prompt", body)
	}
}

func TestHandleBeadsUpstream_SwitchAwayFromPrompts_ClearsPromptNames(t *testing.T) {
	// Switching from "prompts" to a regular tracker must clear the stored prompt names.
	setupMittoDir(t)
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/test/workspace", ACPServer: "test-server"},
	})

	noParamPrompt := config.WebPrompt{
		Name:   "pull-prompt",
		Prompt: "run pull",
	}
	s := New(Deps{
		SessionManager: sm,
		GetWorkspacePromptsAll: func(string) []config.WebPrompt {
			return []config.WebPrompt{noParamPrompt}
		},
	})

	// First, set prompts upstream.
	put1 := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"prompts","pull_prompt":"pull-prompt"}`))
	put1.RemoteAddr = "127.0.0.1:1"
	put1.Header.Set("Content-Type", "application/json")
	pw1 := httptest.NewRecorder()
	s.handleBeadsUpstream(pw1, put1)
	if pw1.Code != http.StatusOK {
		t.Fatalf("first PUT status = %d, want %d (%s)", pw1.Code, http.StatusOK, pw1.Body.String())
	}

	// Switch to jira — prompt names must disappear.
	put2 := httptest.NewRequest(http.MethodPut, "/api/issues/upstream?working_dir=/test/workspace",
		strings.NewReader(`{"upstream":"jira"}`))
	put2.RemoteAddr = "127.0.0.1:1"
	put2.Header.Set("Content-Type", "application/json")
	pw2 := httptest.NewRecorder()
	s.handleBeadsUpstream(pw2, put2)
	if pw2.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, want %d (%s)", pw2.Code, http.StatusOK, pw2.Body.String())
	}

	get := localhostRequest("/api/issues/upstream?working_dir=/test/workspace")
	gw := httptest.NewRecorder()
	s.handleBeadsUpstream(gw, get)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", gw.Code, http.StatusOK)
	}
	body := gw.Body.String()
	if !strings.Contains(body, `"upstream":"jira"`) {
		t.Errorf("GET body = %q, want upstream jira", body)
	}
	if strings.Contains(body, "pull_prompt") {
		t.Errorf("GET body = %q, pull_prompt should not be present after switching to jira", body)
	}
}

// --- handleBeadsSync ---------------------------------------------------------

func TestHandleBeadsSync_MethodNotAllowed(t *testing.T) {
	s := newBeadsTestServer()
	req := localhostRequest("/api/issues/sync?working_dir=/test/workspace")
	w := httptest.NewRecorder()
	s.handleBeadsSync(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBeadsSync_UnknownWorkspace(t *testing.T) {
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/sync?working_dir=/unknown/dir",
		strings.NewReader(`{"action":"pull"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsSync(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleBeadsSync_NoUpstreamConfigured(t *testing.T) {
	// No upstream configured → handler returns canonical 500 envelope with message "no upstream...".
	setupMittoDir(t)
	s := newBeadsTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/sync?working_dir=/test/workspace",
		strings.NewReader(`{"action":"pull"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsSync(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "server_error" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "server_error")
	}
	if !strings.Contains(env.Error.Message, "no upstream") {
		t.Errorf("error.message = %q, want it to contain %q", env.Error.Message, "no upstream")
	}
}

func TestHandleBeadsSync_InvalidAction(t *testing.T) {
	setupMittoDir(t)
	s := newBeadsTestServer()
	if err := config.SetFolderBeadsUpstream("/test/workspace", "jira"); err != nil {
		t.Fatalf("SetFolderBeadsUpstream() returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/issues/sync?working_dir=/test/workspace",
		strings.NewReader(`{"action":"frobnicate"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleBeadsSync(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	const wantMsg = "action must be one of: pull, push, sync, status"
	if env.Error.Message != wantMsg {
		t.Errorf("error.message = %q, want %q", env.Error.Message, wantMsg)
	}
}

// --- isKnownWorkspaceDir -----------------------------------------------------

func TestIsKnownWorkspaceDir(t *testing.T) {
	s := newBeadsTestServer()

	if !s.isKnownWorkspaceDir("/test/workspace") {
		t.Error("isKnownWorkspaceDir should return true for known workspace")
	}
	if s.isKnownWorkspaceDir("/unknown") {
		t.Error("isKnownWorkspaceDir should return false for unknown workspace")
	}
}

func TestIsKnownWorkspaceDir_NilSessionManager(t *testing.T) {
	s := New(Deps{})
	if s.isKnownWorkspaceDir("/any/path") {
		t.Error("isKnownWorkspaceDir should return false when sessionManager is nil")
	}
}
