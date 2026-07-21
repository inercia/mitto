package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// setupFileTestHandlers creates a test Handlers backed by a session store with
// a single test session, for exercising the file REST handlers.
func setupFileTestHandlers(t *testing.T, sessionID string) (*session.Store, *Handlers) {
	t.Helper()

	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if sessionID != "" {
		meta := session.Metadata{
			SessionID:  sessionID,
			ACPServer:  "test-server",
			WorkingDir: "/tmp",
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	h := New(Deps{Store: store})
	return store, h
}

// TestHandleUploadFile_ZeroByte pins the mitto-e1bv fix for the file endpoint:
// dropping a zero-byte file (preview shortcut, .webloc, or a similar header-only
// multipart payload) must be rejected with a clear file_empty error instead of
// silently saving a 0-byte attachment.
func TestHandleUploadFile_ZeroByte(t *testing.T) {
	store, h := setupFileTestHandlers(t, "test-session-file-zero")

	// Build a valid multipart body whose only "file" part is 0 bytes.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "empty.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_ = part // no bytes written -> header-only part
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-file-zero/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	h.handleUploadFile(w, req, store, "test-session-file-zero")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "file_empty" {
		t.Errorf("code = %q, want %q", env.Error.Code, "file_empty")
	}
	if env.Error.Message != "Uploaded file is empty (0 bytes)" {
		t.Errorf("message = %q, want %q", env.Error.Message, "Uploaded file is empty (0 bytes)")
	}
}
