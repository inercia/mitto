package api

import (
	"io"
	"net/http"
	"testing"
)

func TestClient_ListImages_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/images").
		RespondJSON(http.StatusOK, `[{"id":"img-1","url":"/api/files/img-1","name":"a.png","mime_type":"image/png","size":42,"created_at":"2026-08-10T06:00:00Z"}]`)

	images, err := f.Client().ListImages("sess-1")
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(images) != 1 || images[0].ID != "img-1" || images[0].Size != 42 {
		t.Errorf("images = %+v, unexpected", images)
	}
	// created_at is emitted by the list endpoint (session.ImageInfo) and was
	// dropped on decode before the review-phase DTO fix.
	if images[0].CreatedAt == nil || images[0].CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %v, want the server's timestamp", images[0].CreatedAt)
	}
}

func TestClient_ListImages_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/missing/images").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().ListImages("missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_GetImage_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/images/img-1").RespondRaw(http.StatusOK, "image/png", []byte("PNGDATA"))

	rc, contentType, err := f.Client().GetImage("sess-1", "img-1")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	defer rc.Close()
	if contentType != "image/png" {
		t.Errorf("contentType = %q, want image/png", contentType)
	}
	data, _ := io.ReadAll(rc)
	if string(data) != "PNGDATA" {
		t.Errorf("data = %q, want PNGDATA", data)
	}
}

func TestClient_GetImage_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/images/missing").RespondRaw(http.StatusNotFound, "", nil)

	_, _, err := f.Client().GetImage("sess-1", "missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_DeleteImage_HappyPath(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		f := newFakeServer(t)
		f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/images/img-1").RespondRaw(status, "", nil)

		if err := f.Client().DeleteImage("sess-1", "img-1"); err != nil {
			t.Fatalf("DeleteImage: %v, want nil for status %d", err, status)
		}
		if got := f.LastRequest().Method; got != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", got)
		}
	}
}

func TestClient_DeleteImage_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/images/missing").RespondRaw(http.StatusNotFound, "", nil)

	err := f.Client().DeleteImage("sess-1", "missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

// TestClient_UploadImage_HappyPath covers the previously-0%-tested
// UploadImage (mitto-rwxq.9), mirroring UploadFile's multipart assertions.
func TestClient_UploadImage_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/images").
		RespondRaw(http.StatusCreated, "application/json", []byte(`{"id":"img-1","url":"/api/files/img-1","name":"a.png","mime_type":"image/png","size":7}`))

	info, err := f.Client().UploadImage("sess-1", "a.png", "image/png", []byte("PNGDATA"))
	if err != nil {
		t.Fatalf("UploadImage: %v", err)
	}
	req := f.LastRequest()
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if len(req.ContentType) < 19 || req.ContentType[:19] != "multipart/form-data" {
		t.Errorf("Content-Type = %q, want multipart/form-data prefix", req.ContentType)
	}
	if info.ID != "img-1" || info.MimeType != "image/png" {
		t.Errorf("ImageInfo = %+v, unexpected", info)
	}
}

func TestClient_UploadFile_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodPost, "/mitto/api/sessions/sess-1/files").
		RespondRaw(http.StatusCreated, "application/json", []byte(`{"id":"file-1","url":"/api/files/file-1","name":"notes.txt","mime_type":"text/plain","size":5,"category":"text"}`))

	info, err := f.Client().UploadFile("sess-1", "notes.txt", "text/plain", []byte("hello"))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	req := f.LastRequest()
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if len(req.ContentType) < 19 || req.ContentType[:19] != "multipart/form-data" {
		t.Errorf("Content-Type = %q, want multipart/form-data prefix", req.ContentType)
	}
	if info.ID != "file-1" || info.Category != "text" || info.Size != 5 {
		t.Errorf("SessionFileInfo = %+v, unexpected", info)
	}
}

func TestClient_ListFiles_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/files").
		RespondJSON(http.StatusOK, `[{"id":"file-1","name":"a.txt","mime_type":"text/plain","size":3,"category":"text","created_at":"2026-08-10T06:00:00Z"}]`)

	files, err := f.Client().ListFiles("sess-1")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].ID != "file-1" {
		t.Errorf("files = %+v, unexpected", files)
	}
	// created_at is emitted by the list endpoint (session.FileInfo) and was
	// dropped on decode before the review-phase DTO fix.
	if files[0].CreatedAt == nil || files[0].CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %v, want the server's timestamp", files[0].CreatedAt)
	}
}

func TestClient_ListFiles_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/missing/files").RespondRaw(http.StatusNotFound, "", nil)

	_, err := f.Client().ListFiles("missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_GetFile_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/files/file-1").RespondRaw(http.StatusOK, "text/plain", []byte("hello"))

	rc, contentType, err := f.Client().GetFile("sess-1", "file-1")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	defer rc.Close()
	if contentType != "text/plain" {
		t.Errorf("contentType = %q, want text/plain", contentType)
	}
	data, _ := io.ReadAll(rc)
	if string(data) != "hello" {
		t.Errorf("data = %q, want hello", data)
	}
}

func TestClient_GetFile_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodGet, "/mitto/api/sessions/sess-1/files/missing").RespondRaw(http.StatusNotFound, "", nil)

	_, _, err := f.Client().GetFile("sess-1", "missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}

func TestClient_DeleteFile_HappyPath(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		f := newFakeServer(t)
		f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/files/file-1").RespondRaw(status, "", nil)

		if err := f.Client().DeleteFile("sess-1", "file-1"); err != nil {
			t.Fatalf("DeleteFile: %v, want nil for status %d", err, status)
		}
		if got := f.LastRequest().Method; got != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", got)
		}
	}
}

func TestClient_DeleteFile_404_ReturnsTypedNotFoundError(t *testing.T) {
	f := newFakeServer(t)
	f.On(http.MethodDelete, "/mitto/api/sessions/sess-1/files/missing").RespondRaw(http.StatusNotFound, "", nil)

	err := f.Client().DeleteFile("sess-1", "missing")
	assertAPIError(t, err, ErrNotFound, http.StatusNotFound, "")
}
