package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// newConfigSnapshotHandlers isolates MITTO_DIR and wires a Handlers whose
// ValidateInstanceBearer closure always returns allow, mirroring the
// production validateInstanceBearer contract without depending on
// internal/web (which would import this package, not vice versa).
func newConfigSnapshotHandlers(t *testing.T, allow bool) *Handlers {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	return New(Deps{
		MittoConfig:            &config.Config{},
		ValidateInstanceBearer: func(r *http.Request) bool { return allow },
	})
}

func snapshotRequest(h *Handlers) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	w := httptest.NewRecorder()
	h.HandleConfigSnapshot(w, req)
	return w
}

func TestHandleConfigSnapshot_NilValidator_ReturnsServiceUnavailable(t *testing.T) {
	h := New(Deps{MittoConfig: &config.Config{}})
	w := snapshotRequest(h)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleConfigSnapshot_RejectedByValidator_ReturnsUnauthorized(t *testing.T) {
	h := newConfigSnapshotHandlers(t, false)
	w := snapshotRequest(h)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleConfigSnapshot_NoSettingsFile_ReturnsExistsFalse(t *testing.T) {
	h := newConfigSnapshotHandlers(t, true)
	w := snapshotRequest(h)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp configSnapshotResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Exists {
		t.Errorf("Exists = true, want false when settings.json does not exist")
	}
	if resp.Revision != "" || resp.Config != nil {
		t.Errorf("Revision/Config = %q/%v, want empty/nil when settings.json does not exist", resp.Revision, resp.Config)
	}
}

// TestHandleConfigSnapshot_RedactsSecretsAndReportsRevision pins the
// mitto-4rz.3 security requirement: a snapshot must never leak
// web.auth.simple.password/web.auth.shared_token/the mcp subtree, while
// still surfacing a non-secret value and a non-empty Revision token.
func TestHandleConfigSnapshot_RedactsSecretsAndReportsRevision(t *testing.T) {
	h := newConfigSnapshotHandlers(t, true)

	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	raw := `{
		"web": {"port": 9999, "auth": {"simple": {"username": "admin", "password": "hunter2"}, "shared_token": "tok"}},
		"mcp": {"host": "127.0.0.1", "port": 5757}
	}`
	if err := os.WriteFile(settingsPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	w := snapshotRequest(h)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp configSnapshotResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Exists {
		t.Fatal("Exists = false, want true")
	}
	if resp.Revision == "" {
		t.Error("Revision is empty, want a non-empty optimistic-concurrency token")
	}
	cfg, ok := resp.Config.(map[string]interface{})
	if !ok {
		t.Fatalf("Config is not an object: %#v", resp.Config)
	}
	webObj := cfg["web"].(map[string]interface{})
	authObj := webObj["auth"].(map[string]interface{})
	simpleObj := authObj["simple"].(map[string]interface{})
	if simpleObj["password"] != "[REDACTED]" {
		t.Errorf("web.auth.simple.password = %v, want redacted", simpleObj["password"])
	}
	if simpleObj["username"] != "admin" {
		t.Errorf("web.auth.simple.username = %v, want preserved (not a secret)", simpleObj["username"])
	}
	if authObj["shared_token"] != "[REDACTED]" {
		t.Errorf("web.auth.shared_token = %v, want redacted", authObj["shared_token"])
	}
	if cfg["mcp"] != "[REDACTED]" {
		t.Errorf("mcp = %#v, want the whole subtree collapsed to the redaction placeholder", cfg["mcp"])
	}
	if port, ok := webObj["port"].(float64); !ok || int(port) != 9999 {
		t.Errorf("web.port = %v, want 9999 (non-secret field preserved)", webObj["port"])
	}
}
