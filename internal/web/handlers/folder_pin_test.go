package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

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

	before := time.Now()
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

	// PUT true also stamps the folder's last-opened timestamp (MRU for the
	// "Add folder" dialog).
	stamped := config.FolderLastOpenedAt("/tmp/proj-a")
	if stamped.IsZero() {
		t.Errorf("FolderLastOpenedAt is zero after PUT pinned=true, want non-zero")
	}
	if stamped.Before(before.Add(-time.Second)) || stamped.After(time.Now().Add(time.Second)) {
		t.Errorf("FolderLastOpenedAt = %v not within test window [%v, now]", stamped, before)
	}

	// GET reflects the persisted true.
	req = httptest.NewRequest(http.MethodGet, pinURL("/tmp/proj-a"), nil)
	w = httptest.NewRecorder()
	h.HandleFolderPin(w, req)
	if w.Code != http.StatusOK || !decodePinBody(t, w) {
		t.Fatalf("GET after PUT true: code=%d body=%s", w.Code, w.Body.String())
	}

	// PUT false → syncCalls == 2, and MUST NOT bump the MRU timestamp.
	// Sleep briefly so a re-stamp would be observably different.
	time.Sleep(10 * time.Millisecond)
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
	after := config.FolderLastOpenedAt("/tmp/proj-a")
	if !after.Equal(stamped) {
		t.Errorf("FolderLastOpenedAt after PUT pinned=false = %v, want unchanged %v", after, stamped)
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

// TestHandleGetWorkspaces_ReflectsPinAfterPut guards the read-side re-projection
// added for mitto-662: after PUT /api/folders/pin persists Pinned=true, the very
// next GET /api/workspaces must return that flag on every workspace in the folder
// (WorkspaceRegistry's map is populated once and never re-projected).
func TestHandleGetWorkspaces_ReflectsPinAfterPut(t *testing.T) {
	h, _ := newFolderPinHandlers(t)

	putReq := httptest.NewRequest(http.MethodPut, pinURL("/tmp/proj-a"),
		bytes.NewReader([]byte(`{"pinned":true}`)))
	putW := httptest.NewRecorder()
	h.HandleFolderPin(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body = %s", putW.Code, putW.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	getW := httptest.NewRecorder()
	h.HandleWorkspaces(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GET status = %d body = %s", getW.Code, getW.Body.String())
	}
	var resp struct {
		Workspaces []config.WorkspaceSettings `json:"workspaces"`
	}
	if err := json.NewDecoder(getW.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for i := range resp.Workspaces {
		if resp.Workspaces[i].WorkingDir == "/tmp/proj-a" {
			found = true
			if !resp.Workspaces[i].Pinned {
				t.Fatalf("workspace /tmp/proj-a Pinned = false, want true (re-projection missing?)")
			}
			break
		}
	}
	if !found {
		t.Fatalf("workspace /tmp/proj-a not found in response: %s", getW.Body.String())
	}
}
