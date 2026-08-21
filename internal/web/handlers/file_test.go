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

// TestHandleUploadFile_TruncatedMultipart reproduces mitto-q8fx: dragging a
// file from the VSCode explorer onto the native macOS app produces a
// WKWebView blob read that never completes (VSCode "promises" the file
// instead of handing over bytes), so the resulting multipart POST body is
// truncated with no closing boundary.
//
// Empirically confirmed (see the mitto-q8fx Investigation comment) that
// Go's r.ParseMultipartForm returns exactly "unexpected EOF" for this wire
// shape. The current handler discards the underlying error and always
// answers the same opaque "Failed to parse form" with an EMPTY error code
// (defaultCodeForStatus("") on 400 → "bad_request", not a distinguishing
// code) for every ParseMultipartForm failure. This test currently FAILS
// because there is no way to distinguish this case from any other parse
// failure via the error code — pinning the fix's job of surfacing a
// dedicated "file_truncated" code (or equivalent) once the underlying error
// is unexpected EOF.
func TestHandleUploadFile_TruncatedMultipart(t *testing.T) {
	store, h := setupFileTestHandlers(t, "test-session-file-truncated")

	// Build a well-formed multipart header for a "file" part, then cut the
	// body short WITHOUT writing the closing boundary — this is the exact
	// wire shape produced by a WKWebView promised-file blob read failing
	// mid-stream (confirmed against net/http's own ParseMultipartForm).
	boundary := "mitto-q8fx-boundary"
	var buf bytes.Buffer
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString(`Content-Disposition: form-data; name="file"; filename="GLB.md"` + "\r\n")
	buf.WriteString("Content-Type: text/markdown\r\n\r\n")
	buf.WriteString("some partial by") // truncated mid-content, no closing boundary

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-file-truncated/files", &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	w := httptest.NewRecorder()

	h.handleUploadFile(w, req, store, "test-session-file-truncated")

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
	// The bug: a truncated-stream read (recoverable-looking, source-app
	// fault) is indistinguishable from any other malformed multipart body.
	// The fix must surface a dedicated code so the UI/logs can name the
	// actual cause instead of the generic "Failed to parse form".
	if env.Error.Code != "file_truncated" {
		t.Errorf("code = %q, want %q (mitto-q8fx: truncated multipart body from a WKWebView promised-file read must be distinguishable from other parse failures)",
			env.Error.Code, "file_truncated")
	}
}
