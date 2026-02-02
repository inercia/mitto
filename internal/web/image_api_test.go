package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestHandleSessionImages_MethodNotAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session first
	meta := session.Metadata{
		SessionID:  "test-session-method",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Test PATCH method (not allowed)
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/test-session-method/images", nil)
	w := httptest.NewRecorder()

	server.handleSessionImages(w, req, "test-session-method", "")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleListImages_EmptyList(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session first
	meta := session.Metadata{
		SessionID:  "test-session-images",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/test-session-images/images", nil)
	w := httptest.NewRecorder()

	server.handleListImages(w, req, store, "test-session-images")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleServeImage_SessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent/images/img1", nil)
	w := httptest.NewRecorder()

	server.handleServeImage(w, req, store, "nonexistent", "img1")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDeleteImage_SessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/nonexistent/images/img1", nil)
	w := httptest.NewRecorder()

	server.handleDeleteImage(w, req, store, "nonexistent", "img1")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUploadImage_InvalidForm(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session first
	meta := session.Metadata{
		SessionID:  "test-session-upload",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request without multipart form
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-upload/images", nil)
	w := httptest.NewRecorder()

	server.handleUploadImage(w, req, store, "test-session-upload")

	// Should return 400 Bad Request for invalid form
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImageSaveError_TooLarge(t *testing.T) {
	server := &Server{}

	w := httptest.NewRecorder()
	server.handleImageSaveError(w, session.ErrImageTooLarge)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleImageSaveError_UnsupportedFormat(t *testing.T) {
	server := &Server{}

	w := httptest.NewRecorder()
	server.handleImageSaveError(w, session.ErrUnsupportedFormat)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImageSaveError_SessionLimit(t *testing.T) {
	server := &Server{}

	w := httptest.NewRecorder()
	server.handleImageSaveError(w, session.ErrSessionImageLimit)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImageSaveError_StorageLimit(t *testing.T) {
	server := &Server{}

	w := httptest.NewRecorder()
	server.handleImageSaveError(w, session.ErrSessionStorageLimit)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUploadImageFromPath_NonLocalhost(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-frompath",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		store: store,
	}

	// Simulate a request from a non-localhost IP
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath/images/from-path", nil)
	req.RemoteAddr = "192.168.1.100:12345" // Non-localhost IP
	w := httptest.NewRecorder()

	server.handleUploadImageFromPath(w, req, store, "test-session-frompath")

	// Should be forbidden for non-localhost
	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestHandleUploadImageFromPath_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-frompath2",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		store: store,
	}

	// Request from localhost with invalid JSON
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath2/images/from-path", nil)
	req.RemoteAddr = "127.0.0.1:12345" // Localhost
	w := httptest.NewRecorder()

	server.handleUploadImageFromPath(w, req, store, "test-session-frompath2")

	// Should be bad request for invalid JSON
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUploadImageFromPath_EmptyPaths(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-frompath3",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		store: store,
	}

	// Request from localhost with empty paths
	body := strings.NewReader(`{"paths": []}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath3/images/from-path", body)
	req.RemoteAddr = "127.0.0.1:12345" // Localhost
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUploadImageFromPath(w, req, store, "test-session-frompath3")

	// Should be bad request for empty paths
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
