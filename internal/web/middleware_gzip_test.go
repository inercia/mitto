package web

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShouldGzipContentType(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		// Explicitly compressible
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"text/css", true},
		{"text/javascript", true},
		{"application/javascript", true},
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/xml", true},
		{"image/svg+xml", true},

		// Text types (default compressible)
		{"text/plain", true},
		{"text/csv", true},

		// Explicitly not compressible (binary/already compressed)
		{"image/png", false},
		{"image/jpeg", false},
		{"image/gif", false},
		{"image/webp", false},
		{"application/octet-stream", false},

		// Unknown types (default to not compress)
		{"video/mp4", false},
		{"audio/mpeg", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			result := shouldGzipContentType(tt.contentType)
			if result != tt.expected {
				t.Errorf("shouldGzipContentType(%q) = %v, want %v", tt.contentType, result, tt.expected)
			}
		})
	}
}

func TestGzipMiddleware_ExternalConnection(t *testing.T) {
	// Create a handler that returns a large JSON response
	largeContent := strings.Repeat(`{"key": "value", "data": "test"}`, 100) // >1KB
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(largeContent))
	})

	// Wrap with gzip middleware
	wrapped := gzipMiddleware(handler)

	// Create request with external connection context and Accept-Encoding
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	// Mark as external connection
	ctx := context.WithValue(req.Context(), ContextKeyExternalConnection, true)
	req = req.WithContext(ctx)

	// Record response
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Verify gzip was applied
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Expected Content-Encoding: gzip, got %q", rec.Header().Get("Content-Encoding"))
	}

	// Verify Vary header was set
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Expected Vary header to contain Accept-Encoding, got %q", rec.Header().Get("Vary"))
	}

	// Verify content is actually gzipped by decompressing
	gzReader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("Failed to decompress: %v", err)
	}

	if string(decompressed) != largeContent {
		t.Errorf("Decompressed content doesn't match original")
	}
}

func TestGzipMiddleware_LocalConnection(t *testing.T) {
	// Create a handler that returns a large JSON response
	largeContent := strings.Repeat(`{"key": "value", "data": "test"}`, 100)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(largeContent))
	})

	// Wrap with gzip middleware
	wrapped := gzipMiddleware(handler)

	// Create request WITHOUT external connection context (local connection)
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	// No ContextKeyExternalConnection set - defaults to local

	// Record response
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Verify gzip was NOT applied for local connection
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("Expected no gzip for local connection, but Content-Encoding: gzip was set")
	}

	// Verify content is uncompressed
	if rec.Body.String() != largeContent {
		t.Errorf("Content was modified unexpectedly")
	}
}

func TestGzipMiddleware_WebSocketUpgrade(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})

	wrapped := gzipMiddleware(handler)

	// Create WebSocket upgrade request
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	ctx := context.WithValue(req.Context(), ContextKeyExternalConnection, true)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Verify gzip was NOT applied for WebSocket
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("Gzip should not be applied to WebSocket upgrade requests")
	}
}

func TestGzipMiddleware_SmallContent(t *testing.T) {
	// Create a handler that returns a small response (below 1KB threshold)
	smallContent := `{"status": "ok"}` // ~16 bytes, well below 1KB
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(smallContent))
	})

	// Wrap with gzip middleware
	wrapped := gzipMiddleware(handler)

	// Create request with external connection context
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	ctx := context.WithValue(req.Context(), ContextKeyExternalConnection, true)
	req = req.WithContext(ctx)

	// Record response
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Verify gzip was NOT applied (content too small)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Errorf("Gzip should not be applied to small responses (got Content-Encoding: gzip)")
	}

	// Verify content is uncompressed and matches original
	if rec.Body.String() != smallContent {
		t.Errorf("Content was modified. Got %q, want %q", rec.Body.String(), smallContent)
	}
}
