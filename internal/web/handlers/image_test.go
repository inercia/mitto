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

// setupImageTestHandlers creates a test Handlers backed by a session store with
// a single test session, for exercising the image REST handlers.
func setupImageTestHandlers(t *testing.T, sessionID string) (*session.Store, *Handlers) {
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

func TestHandleSessionImages_MethodNotAllowed(t *testing.T) {
	_, h := setupImageTestHandlers(t, "test-session-method")

	// Test PATCH method (not allowed)
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/test-session-method/images", nil)
	w := httptest.NewRecorder()

	h.HandleSessionImages(w, req, "test-session-method", "")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
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
	if env.Error.Code != "method_not_allowed" {
		t.Errorf("code = %q, want %q", env.Error.Code, "method_not_allowed")
	}
	if env.Error.Message != "Method not allowed" {
		t.Errorf("message = %q, want %q", env.Error.Message, "Method not allowed")
	}
}

func TestHandleListImages_EmptyList(t *testing.T) {
	store, h := setupImageTestHandlers(t, "test-session-images")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/test-session-images/images", nil)
	w := httptest.NewRecorder()

	h.handleListImages(w, req, store, "test-session-images")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleServeImage_SessionNotFound(t *testing.T) {
	store, h := setupImageTestHandlers(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent/images/img1", nil)
	w := httptest.NewRecorder()

	h.handleServeImage(w, req, store, "nonexistent", "img1")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
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
	if env.Error.Code != "not_found" {
		t.Errorf("code = %q, want %q", env.Error.Code, "not_found")
	}
	if env.Error.Message != "Image not found" {
		t.Errorf("message = %q, want %q", env.Error.Message, "Image not found")
	}
}

func TestHandleDeleteImage_SessionNotFound(t *testing.T) {
	store, h := setupImageTestHandlers(t, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/nonexistent/images/img1", nil)
	w := httptest.NewRecorder()

	h.handleDeleteImage(w, req, store, "nonexistent", "img1")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
	var env2 struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env2); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env2.Error.Code != "not_found" {
		t.Errorf("code = %q, want %q", env2.Error.Code, "not_found")
	}
	if env2.Error.Message != "Image not found" {
		t.Errorf("message = %q, want %q", env2.Error.Message, "Image not found")
	}
}

func TestHandleUploadImage_InvalidForm(t *testing.T) {
	store, h := setupImageTestHandlers(t, "test-session-upload")

	// Request without multipart form
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-upload/images", nil)
	w := httptest.NewRecorder()

	h.handleUploadImage(w, req, store, "test-session-upload")

	// Should return 400 Bad Request for invalid form
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env3 struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env3); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env3.Error.Code != "bad_request" {
		t.Errorf("code = %q, want %q", env3.Error.Code, "bad_request")
	}
	if env3.Error.Message != "Failed to parse form" {
		t.Errorf("message = %q, want %q", env3.Error.Message, "Failed to parse form")
	}
}

// TestHandleUploadImage_ZeroByte reproduces mitto-e1bv: dropping a zero-byte
// image (preview thumbnail from another tab, Slack, Google Images, .webloc)
// produces a header-only multipart part; without the header.Size==0 guard the
// handler misclassifies it as unsupported_format instead of image_empty.
func TestHandleUploadImage_ZeroByte(t *testing.T) {
	store, h := setupImageTestHandlers(t, "test-session-zero")

	// Build a valid multipart body whose only "image" part is 0 bytes.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("image", "empty.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_ = part // no bytes written -> header-only part
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-zero/images", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()

	h.handleUploadImage(w, req, store, "test-session-zero")

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
	if env.Error.Code != "image_empty" {
		t.Errorf("code = %q, want %q", env.Error.Code, "image_empty")
	}
	if env.Error.Message != "Uploaded image is empty (0 bytes)" {
		t.Errorf("message = %q, want %q", env.Error.Message, "Uploaded image is empty (0 bytes)")
	}
}

func TestHandleImageSaveError_TooLarge(t *testing.T) {
	h := New(Deps{})

	w := httptest.NewRecorder()
	h.handleImageSaveError(w, session.ErrImageTooLarge)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleImageSaveError_UnsupportedFormat(t *testing.T) {
	h := New(Deps{})

	w := httptest.NewRecorder()
	h.handleImageSaveError(w, session.ErrUnsupportedFormat)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImageSaveError_SessionLimit(t *testing.T) {
	h := New(Deps{})

	w := httptest.NewRecorder()
	h.handleImageSaveError(w, session.ErrSessionImageLimit)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImageSaveError_StorageLimit(t *testing.T) {
	h := New(Deps{})

	w := httptest.NewRecorder()
	h.handleImageSaveError(w, session.ErrSessionStorageLimit)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
