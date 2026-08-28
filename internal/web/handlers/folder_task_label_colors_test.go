package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// newFolderTaskLabelColorHandlers wires a Handlers facade for the folder
// task-label-colors handler with one known workspace dir (/tmp/proj-a) and an
// isolated MITTO_DIR. The returned counter records broadcast calls and the
// last working_dir argument seen by the broadcast callback.
func newFolderTaskLabelColorHandlers(t *testing.T) (*Handlers, *int, *string) {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "u1", ACPServer: "auggie", WorkingDir: "/tmp/proj-a"},
	})
	broadcasts := 0
	var lastDir string
	h := New(Deps{
		SessionManager: sm,
		MittoConfig:    &config.Config{},
		BroadcastFolderTaskLabelColorsUpdated: func(workingDir string) {
			broadcasts++
			lastDir = workingDir
		},
	})
	return h, &broadcasts, &lastDir
}

func folderTaskLabelColorURL(dir string) string {
	return "/api/folders/task-label-colors?working_dir=" + url.QueryEscape(dir)
}

func folderTaskLabelColorRequest(h *Handlers, method, dir, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, folderTaskLabelColorURL(dir), bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.HandleFolderTaskLabelColors(w, req)
	return w
}

func TestHandleFolderTaskLabelColors_PutGetOrderNormalizeAndBroadcast(t *testing.T) {
	h, broadcasts, lastDir := newFolderTaskLabelColorHandlers(t)
	w := folderTaskLabelColorRequest(h, http.MethodPut, "/tmp/proj-a",
		`{"entries":[{"label":" needs-human ","color":"#EF4444"},{"label":"blocked","color":"#f59e0b"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", w.Code, w.Body.String())
	}
	if *broadcasts != 1 || *lastDir != "/tmp/proj-a" {
		t.Fatalf("broadcasts = %d lastDir = %q, want 1 /tmp/proj-a", *broadcasts, *lastDir)
	}

	w = folderTaskLabelColorRequest(h, http.MethodGet, "/tmp/proj-a", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", w.Code, w.Body.String())
	}
	var got folderTaskLabelColorsBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if len(got.Entries) != 2 || got.Entries[0].Label != "needs-human" || got.Entries[0].Color != "#ef4444" || got.Entries[1].Label != "blocked" {
		t.Fatalf("GET entries = %+v, want normalized + preserved order", got.Entries)
	}
}

func TestHandleFolderTaskLabelColors_Clear(t *testing.T) {
	h, broadcasts, _ := newFolderTaskLabelColorHandlers(t)
	w := folderTaskLabelColorRequest(h, http.MethodPut, "/tmp/proj-a", `{"entries":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if *broadcasts != 1 {
		t.Fatalf("broadcasts = %d, want 1", *broadcasts)
	}
	if got := config.FolderTaskLabelColors("/tmp/proj-a"); len(got) != 0 {
		t.Errorf("FolderTaskLabelColors after clear = %v, want empty", got)
	}
	if w.Body.String() != "{\"entries\":[]}\n" {
		t.Errorf("body = %q, want empty array", w.Body.String())
	}
}

func TestHandleFolderTaskLabelColors_RejectsInvalidBodiesWithoutBroadcast(t *testing.T) {
	tests := map[string]string{
		"malformed":       `{`,
		"missing entries": `{}`,
		"null entries":    `{"entries":null}`,
		"empty label":     `{"entries":[{"label":"  ","color":"#ef4444"}]}`,
		"bad color":       `{"entries":[{"label":"needs-human","color":"red"}]}`,
		"unknown field":   `{"entries":[],"extra":true}`,
		"trailing body":   `{"entries":[]} {}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			h, broadcasts, _ := newFolderTaskLabelColorHandlers(t)
			w := folderTaskLabelColorRequest(h, http.MethodPut, "/tmp/proj-a", body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
			if *broadcasts != 0 {
				t.Errorf("invalid request broadcast: broadcasts=%d", *broadcasts)
			}
		})
	}
}

func TestHandleFolderTaskLabelColors_MissingWorkingDir(t *testing.T) {
	h, _, _ := newFolderTaskLabelColorHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/api/folders/task-label-colors", nil)
	w := httptest.NewRecorder()
	h.HandleFolderTaskLabelColors(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

func TestHandleFolderTaskLabelColors_RelativeWorkingDirRejected(t *testing.T) {
	h, _, _ := newFolderTaskLabelColorHandlers(t)
	w := folderTaskLabelColorRequest(h, http.MethodGet, "proj-a", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

func TestHandleFolderTaskLabelColors_UnknownWorkingDir(t *testing.T) {
	h, _, _ := newFolderTaskLabelColorHandlers(t)
	w := folderTaskLabelColorRequest(h, http.MethodGet, "/tmp/unknown", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

func TestHandleFolderTaskLabelColors_MethodNotAllowed(t *testing.T) {
	h, _, _ := newFolderTaskLabelColorHandlers(t)
	req := httptest.NewRequest(http.MethodPost, folderTaskLabelColorURL("/tmp/proj-a"), nil)
	w := httptest.NewRecorder()
	h.HandleFolderTaskLabelColors(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 body=%s", w.Code, w.Body.String())
	}
}
