package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// buildRetitleHandlers wires up a Handlers with a real Store and a
// SessionManager holding a minimal BackgroundSession, mirroring the pattern
// in queue_required_args_test.go.
func buildRetitleHandlers(t *testing.T) (*Handlers, string) {
	t.Helper()
	const (
		sessionID     = "20260910-180000-retitle"
		workspaceUUID = "ws-retitle"
	)
	workingDir := t.TempDir()

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test",
		WorkingDir: workingDir,
		Status:     "active",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: workspaceUUID, WorkingDir: workingDir, ACPServer: "test"},
	})
	bs := conversation.NewMinimalBackgroundSession(sessionID, workingDir, workspaceUUID)
	sm.AddSessionForTest(bs)

	h := New(Deps{
		Store:          store,
		SessionManager: sm,
	})
	return h, sessionID
}

// TestHandleSessionRetitle_MethodNotAllowed verifies non-POST requests are
// rejected with 405, mirroring HandleSessionFlush's method guard.
func TestHandleSessionRetitle_MethodNotAllowed(t *testing.T) {
	h, sid := buildRetitleHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sid+"/retitle", nil)
	w := httptest.NewRecorder()

	h.HandleSessionRetitle(w, req, sid)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusMethodNotAllowed, w.Body.String())
	}
}

// TestHandleSessionRetitle_SessionNotFound verifies a 404 when the session
// isn't registered with the SessionManager (not found or not running).
func TestHandleSessionRetitle_SessionNotFound(t *testing.T) {
	h, _ := buildRetitleHandlers(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/does-not-exist/retitle", nil)
	w := httptest.NewRecorder()

	h.HandleSessionRetitle(w, req, "does-not-exist")

	if w.Code != http.StatusNotFound {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

// TestHandleSessionRetitle_Success verifies the happy path: 200 with the
// {"status": "retitling"} body, and that calling ForceRegenerateTitle on the
// (minimal, store-less) BackgroundSession does not panic — mitto-yv2's
// endpoint contract.
func TestHandleSessionRetitle_Success(t *testing.T) {
	h, sid := buildRetitleHandlers(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/retitle", nil)
	w := httptest.NewRecorder()

	h.HandleSessionRetitle(w, req, sid)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "retitling" {
		t.Errorf("status = %v, want %q", resp["status"], "retitling")
	}
}

// TestHandleSessionRetitle_NoSessionManager verifies a 500 when the
// SessionManager dependency isn't wired at all.
func TestHandleSessionRetitle_NoSessionManager(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	h := New(Deps{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/any/retitle", nil)
	w := httptest.NewRecorder()

	h.HandleSessionRetitle(w, req, "any")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}
