package handlers

import (
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
}

func TestHandleDeleteImage_SessionNotFound(t *testing.T) {
	store, h := setupImageTestHandlers(t, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/nonexistent/images/img1", nil)
	w := httptest.NewRecorder()

	h.handleDeleteImage(w, req, store, "nonexistent", "img1")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
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
