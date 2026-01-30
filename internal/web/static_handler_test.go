package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// createTestFS creates a mock filesystem for testing static file serving.
func createTestFS() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html>
<html>
<head>
    <script nonce="{{CSP_NONCE}}">window.mittoApiPrefix = "{{API_PREFIX}}";</script>
</head>
<body>Hello</body>
</html>`),
		},
		"app.js": &fstest.MapFile{
			Data: []byte(`console.log("app");`),
		},
		"styles.css": &fstest.MapFile{
			Data: []byte(`body { color: white; }`),
		},
		"favicon.ico": &fstest.MapFile{
			Data: []byte{0x00, 0x00, 0x01, 0x00}, // Minimal ICO header
		},
	}
}

// TestStaticFileHandler_CacheHeaders verifies that all static files have no-cache headers.
func TestStaticFileHandler_CacheHeaders(t *testing.T) {
	testFS := createTestFS()
	s := &Server{}
	handler := s.staticFileHandler(testFS)

	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantHeaders map[string]string
	}{
		{
			name:       "index.html has no-cache headers",
			path:       "/",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"Cache-Control": "no-cache, no-store, must-revalidate",
				"Pragma":        "no-cache",
				"Expires":       "0",
			},
		},
		{
			name:       "JavaScript files have no-cache headers",
			path:       "/app.js",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"Cache-Control": "no-cache, no-store, must-revalidate",
				"Pragma":        "no-cache",
				"Expires":       "0",
			},
		},
		{
			name:       "CSS files have no-cache headers",
			path:       "/styles.css",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"Cache-Control": "no-cache, no-store, must-revalidate",
				"Pragma":        "no-cache",
				"Expires":       "0",
			},
		},
		{
			name:       "favicon has no-cache headers",
			path:       "/favicon.ico",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"Cache-Control": "no-cache, no-store, must-revalidate",
				"Pragma":        "no-cache",
				"Expires":       "0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			for header, want := range tt.wantHeaders {
				got := rec.Header().Get(header)
				if got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
		})
	}
}

// TestStaticFileHandler_NotFound verifies 404 handling for missing files.
func TestStaticFileHandler_NotFound(t *testing.T) {
	testFS := createTestFS()
	s := &Server{}
	handler := s.staticFileHandler(testFS)

	tests := []struct {
		name string
		path string
	}{
		{"missing file", "/nonexistent.js"},
		{"missing directory", "/subdir/file.html"},
		{"path traversal attempt", "/../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
			}

			// Should return minimal error message (security)
			body := rec.Body.String()
			if !strings.Contains(body, "Not Found") {
				t.Errorf("body = %q, want to contain 'Not Found'", body)
			}
		})
	}
}

// TestStaticFileHandler_RootRedirectsToIndex verifies that / serves index.html.
func TestStaticFileHandler_RootRedirectsToIndex(t *testing.T) {
	testFS := createTestFS()
	s := &Server{}
	handler := s.staticFileHandler(testFS)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("/ should serve index.html content")
	}
}

