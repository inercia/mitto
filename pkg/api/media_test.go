package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ListImages_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/images", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"img-1","url":"/api/files/img-1","name":"a.png","mime_type":"image/png","size":42,"created_at":"2026-08-10T06:00:00Z"}]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	images, err := c.ListImages("sess-1")
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
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/missing/images", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.ListImages("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_GetImage_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/images/img-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("PNGDATA"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	rc, contentType, err := c.GetImage("sess-1", "img-1")
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
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/images/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, _, err := c.GetImage("sess-1", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_DeleteImage_HappyPath(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		mux := http.NewServeMux()
		var gotMethod string
		mux.HandleFunc("/mitto/api/sessions/sess-1/images/img-1", func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			w.WriteHeader(status)
		})
		ts := httptest.NewServer(mux)

		c := New(ts.URL)
		if err := c.DeleteImage("sess-1", "img-1"); err != nil {
			t.Fatalf("DeleteImage: %v, want nil for status %d", err, status)
		}
		if gotMethod != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", gotMethod)
		}
		ts.Close()
	}
}

func TestClient_DeleteImage_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/images/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	err := c.DeleteImage("sess-1", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_UploadFile_HappyPath(t *testing.T) {
	var gotMethod, gotContentType string
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/files", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"file-1","url":"/api/files/file-1","name":"notes.txt","mime_type":"text/plain","size":5,"category":"text"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	info, err := c.UploadFile("sess-1", "notes.txt", "text/plain", []byte("hello"))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType == "" || gotContentType[:19] != "multipart/form-data" {
		t.Errorf("Content-Type = %q, want multipart/form-data prefix", gotContentType)
	}
	if info.ID != "file-1" || info.Category != "text" || info.Size != 5 {
		t.Errorf("SessionFileInfo = %+v, unexpected", info)
	}
}

func TestClient_ListFiles_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"file-1","name":"a.txt","mime_type":"text/plain","size":3,"category":"text","created_at":"2026-08-10T06:00:00Z"}]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	files, err := c.ListFiles("sess-1")
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
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/missing/files", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.ListFiles("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_GetFile_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/files/file-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	rc, contentType, err := c.GetFile("sess-1", "file-1")
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
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/files/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, _, err := c.GetFile("sess-1", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}

func TestClient_DeleteFile_HappyPath(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		mux := http.NewServeMux()
		var gotMethod string
		mux.HandleFunc("/mitto/api/sessions/sess-1/files/file-1", func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			w.WriteHeader(status)
		})
		ts := httptest.NewServer(mux)

		c := New(ts.URL)
		if err := c.DeleteFile("sess-1", "file-1"); err != nil {
			t.Fatalf("DeleteFile: %v, want nil for status %d", err, status)
		}
		if gotMethod != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", gotMethod)
		}
		ts.Close()
	}
}

func TestClient_DeleteFile_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/files/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	err := c.DeleteFile("sess-1", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
}
