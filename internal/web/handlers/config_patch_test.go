package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// newConfigPatchHandlers isolates MITTO_DIR and wires a Handlers with an
// always-allow instance-bearer validator, a live MittoConfig, and a
// broadcast counter for task_label_colors_updated.
func newConfigPatchHandlers(t *testing.T, readOnly bool) (*Handlers, *config.Config, *int) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	cfg := &config.Config{}
	broadcasts := 0
	h := New(Deps{
		MittoConfig:                     cfg,
		ConfigReadOnly:                  readOnly,
		ValidateInstanceBearer:          func(r *http.Request) bool { return true },
		BroadcastTaskLabelColorsUpdated: func() { broadcasts++ },
	})
	return h, cfg, &broadcasts
}

func patchRequest(h *Handlers, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/config/patch", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.HandleConfigPatch(w, req)
	return w
}

func TestHandleConfigPatch_NilValidator_ReturnsServiceUnavailable(t *testing.T) {
	h := New(Deps{MittoConfig: &config.Config{}})
	w := patchRequest(h, `{"ops":[{"path":"web.port","value":9090}]}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleConfigPatch_RejectedByValidator_ReturnsUnauthorized(t *testing.T) {
	h := New(Deps{MittoConfig: &config.Config{}, ValidateInstanceBearer: func(r *http.Request) bool { return false }})
	w := patchRequest(h, `{"ops":[{"path":"web.port","value":9090}]}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleConfigPatch_ReadOnly_ReturnsForbidden(t *testing.T) {
	h, _, _ := newConfigPatchHandlers(t, true)
	w := patchRequest(h, `{"ops":[{"path":"web.port","value":9090}]}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleConfigPatch_RejectsInvalidBody(t *testing.T) {
	tests := map[string]string{
		"malformed json": `{`,
		"empty ops":      `{"ops":[]}`,
		"missing ops":    `{}`,
		"invalid path":   `{"ops":[{"path":"[","value":1}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			h, _, _ := newConfigPatchHandlers(t, false)
			w := patchRequest(h, body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleConfigPatch_UnknownField_ReturnsBadRequest(t *testing.T) {
	h, _, _ := newConfigPatchHandlers(t, false)
	w := patchRequest(h, `{"ops":[{"path":"totally.unknown.field","value":1}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleConfigPatch_RejectedSecretField_ReturnsForbidden(t *testing.T) {
	h, _, _ := newConfigPatchHandlers(t, false)
	w := patchRequest(h, `{"ops":[{"path":"web.auth.shared_token","value":"x"}]}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

// TestHandleConfigPatch_RestartRequiredField pins the "no automatic
// restarts" decision (mitto-4rz.3 plan): web.port is persisted but reported
// restart_required, never applied live.
func TestHandleConfigPatch_RestartRequiredField(t *testing.T) {
	h, cfg, _ := newConfigPatchHandlers(t, false)
	w := patchRequest(h, `{"ops":[{"path":"web.port","value":9090}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp configPatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DryRun {
		t.Error("DryRun = true, want false")
	}
	if len(resp.Applied) != 1 || resp.Applied[0].Path != "web.port" || resp.Applied[0].Status != configPatchStatusRestartRequired {
		t.Fatalf("Applied = %+v, want [{web.port restart_required}]", resp.Applied)
	}
	if resp.Revision == "" {
		t.Error("Revision is empty, want a non-empty revision after a real write")
	}
	// web.port has no in-memory mirror; the in-memory Config must be untouched.
	if cfg.Web.Port != 0 {
		t.Errorf("cfg.Web.Port = %d, want unchanged (0) for a restart-required field", cfg.Web.Port)
	}
}

// TestHandleConfigPatch_LiveField_AppliesInMemoryAndBroadcasts pins the
// LivenessLive contract: task_label_colors is persisted, refreshed into
// Deps.MittoConfig, and broadcasts exactly once.
func TestHandleConfigPatch_LiveField_AppliesInMemoryAndBroadcasts(t *testing.T) {
	h, cfg, broadcasts := newConfigPatchHandlers(t, false)
	w := patchRequest(h, `{"ops":[{"path":"task_label_colors","value":[{"label":"needs-human","color":"#ef4444"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp configPatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Applied) != 1 || resp.Applied[0].Status != configPatchStatusApplied {
		t.Fatalf("Applied = %+v, want status=applied", resp.Applied)
	}
	if len(cfg.TaskLabelColors) != 1 || cfg.TaskLabelColors[0].Label != "needs-human" {
		t.Fatalf("cfg.TaskLabelColors = %+v, want the live-refreshed entry", cfg.TaskLabelColors)
	}
	if *broadcasts != 1 {
		t.Fatalf("broadcasts = %d, want exactly 1", *broadcasts)
	}
}

// TestHandleConfigPatch_LiveField_NoMittoConfig_ReportsApplyFailed covers the
// nil-MittoConfig edge case: the write still persists, but the in-memory
// refresh cannot happen, so the per-key status must say so rather than
// silently claiming success.
func TestHandleConfigPatch_LiveField_NoMittoConfig_ReportsApplyFailed(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	h := New(Deps{ValidateInstanceBearer: func(r *http.Request) bool { return true }})

	w := patchRequest(h, `{"ops":[{"path":"shortcuts","value":{"conversations":[{"icon":"","prompt":"Commit"}]}}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp configPatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Applied) != 1 || resp.Applied[0].Status != configPatchStatusApplyFailed {
		t.Fatalf("Applied = %+v, want status=apply_failed (nil MittoConfig)", resp.Applied)
	}
}

// TestHandleConfigPatch_RevisionMismatch_ReturnsConflict pins the
// optimistic-concurrency contract: a stale Revision fails the whole batch
// with 409, and settings.json must be left untouched.
func TestHandleConfigPatch_RevisionMismatch_ReturnsConflict(t *testing.T) {
	h, _, _ := newConfigPatchHandlers(t, false)
	settingsPath, err := appdir.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"web":{"port":8080}}`), 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	w := patchRequest(h, `{"ops":[{"path":"web.port","value":9090}],"revision":"stale-revision"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if string(raw) != `{"web":{"port":8080}}` {
		t.Errorf("settings.json changed despite revision mismatch: %s", raw)
	}
}

// TestHandleConfigPatch_DryRun_NoSideEffects pins the dry-run contract:
// per-key status reports "would_apply" (never "applied"/"restart_required")
// and nothing is persisted, broadcast, or applied in memory.
func TestHandleConfigPatch_DryRun_NoSideEffects(t *testing.T) {
	h, cfg, broadcasts := newConfigPatchHandlers(t, false)
	w := patchRequest(h, `{"ops":[{"path":"task_label_colors","value":[{"label":"x","color":"#ffffff"}]}],"dry_run":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp configPatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.DryRun {
		t.Error("DryRun = false, want true")
	}
	if len(resp.Applied) != 1 || resp.Applied[0].Status != configPatchStatusWouldApply {
		t.Fatalf("Applied = %+v, want status=would_apply", resp.Applied)
	}
	if resp.Revision != "" {
		t.Errorf("Revision = %q, want empty (dry-run never reports a post-write revision)", resp.Revision)
	}
	if len(cfg.TaskLabelColors) != 0 || *broadcasts != 0 {
		t.Errorf("dry-run had side effects: cfg.TaskLabelColors=%+v broadcasts=%d", cfg.TaskLabelColors, *broadcasts)
	}
	settingsPath, _ := appdir.SettingsPath()
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("dry-run created settings.json, want no file created")
	}
}
