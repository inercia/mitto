package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// newFolderPinHandlers wires a Handlers facade for the folder-pin handler with
// two known workspace dirs (/tmp/proj-a, /tmp/proj-b) and an isolated MITTO_DIR.
// The returned counter records how many times SyncConfigWorkspaces was invoked.
func newFolderPinHandlers(t *testing.T) (*Handlers, *int32) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: "/tmp/proj-a"},
		{UUID: "u2", ACPServer: "claude", WorkingDir: "/tmp/proj-b"},
	})
	syncCalls := new(int32)
	h := New(Deps{
		SessionManager:       sm,
		MittoConfig:          &config.Config{},
		SyncConfigWorkspaces: func() { atomic.AddInt32(syncCalls, 1) },
	})
	return h, syncCalls
}

func decodePinBody(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	var body struct {
		Pinned bool `json:"pinned"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	return body.Pinned
}

func pinURL(dir string) string {
	return "/api/folders/pin?working_dir=" + url.QueryEscape(dir)
}

func TestHandleFolderPin_GetDefaultsFalse(t *testing.T) {
	h, _ := newFolderPinHandlers(t)
	req := httptest.NewRequest(http.MethodGet, pinURL("/tmp/proj-a"), nil)
	w := httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if decodePinBody(t, w) {
		t.Errorf("Pinned = true, want false (default)")
	}
}

func TestHandleFolderPin_PutSetsTrueAndSyncs(t *testing.T) {
	h, syncCalls := newFolderPinHandlers(t)

	req := httptest.NewRequest(http.MethodPut, pinURL("/tmp/proj-a"),
		bytes.NewReader([]byte(`{"pinned":true}`)))
	w := httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT true status = %d body = %s", w.Code, w.Body.String())
	}
	if !decodePinBody(t, w) {
		t.Errorf("PUT true response Pinned = false, want true")
	}
	if n := atomic.LoadInt32(syncCalls); n != 1 {
		t.Errorf("syncCalls after PUT true = %d, want 1", n)
	}

	// GET reflects the persisted true.
	req = httptest.NewRequest(http.MethodGet, pinURL("/tmp/proj-a"), nil)
	w = httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusOK || !decodePinBody(t, w) {
		t.Fatalf("GET after PUT true: code=%d body=%s", w.Code, w.Body.String())
	}

	// PUT false → syncCalls == 2.
	req = httptest.NewRequest(http.MethodPut, pinURL("/tmp/proj-a"),
		bytes.NewReader([]byte(`{"pinned":false}`)))
	w = httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT false status = %d body = %s", w.Code, w.Body.String())
	}
	if decodePinBody(t, w) {
		t.Errorf("PUT false response Pinned = true, want false")
	}
	if n := atomic.LoadInt32(syncCalls); n != 2 {
		t.Errorf("syncCalls after PUT false = %d, want 2", n)
	}
}

func TestHandleFolderPin_MissingWorkingDir(t *testing.T) {
	h, _ := newFolderPinHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/api/folders/pin", nil)
	w := httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

func TestHandleFolderPin_RelativeWorkingDirRejected(t *testing.T) {
	h, _ := newFolderPinHandlers(t)
	req := httptest.NewRequest(http.MethodGet, pinURL("proj-a"), nil)
	w := httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

func TestHandleFolderPin_UnknownWorkingDir(t *testing.T) {
	h, _ := newFolderPinHandlers(t)
	req := httptest.NewRequest(http.MethodGet, pinURL("/tmp/unknown"), nil)
	w := httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

func TestHandleFolderPin_PutInvalidBody(t *testing.T) {
	h, syncCalls := newFolderPinHandlers(t)
	req := httptest.NewRequest(http.MethodPut, pinURL("/tmp/proj-a"),
		bytes.NewReader([]byte(`not json`)))
	w := httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	if n := atomic.LoadInt32(syncCalls); n != 0 {
		t.Errorf("syncCalls after invalid body = %d, want 0", n)
	}
}

func TestHandleFolderPin_MethodNotAllowed(t *testing.T) {
	h, _ := newFolderPinHandlers(t)
	req := httptest.NewRequest(http.MethodPost, pinURL("/tmp/proj-a"), nil)
	w := httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 body=%s", w.Code, w.Body.String())
	}
}
