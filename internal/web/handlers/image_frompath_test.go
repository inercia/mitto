package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/web/middleware"
)

func TestHandleUploadImageFromPath_NonLocalhost(t *testing.T) {
	store, h := setupImageTestHandlers(t, "test-session-frompath")

	// Simulate a request from a non-localhost IP
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath/images/from-path", nil)
	req.RemoteAddr = "192.168.1.100:12345" // Non-localhost IP
	w := httptest.NewRecorder()

	h.handleUploadImageFromPath(w, req, store, "test-session-frompath")

	// Should be forbidden for non-localhost
	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestHandleUploadImageFromPath_ExternalConnection(t *testing.T) {
	store, h := setupImageTestHandlers(t, "test-session-frompath-ext")

	// Test case 1: External connection with localhost IP (defense-in-depth)
	// This simulates an attacker connecting to the external port from localhost
	t.Run("localhost_via_external_port", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath-ext/images/from-path", nil)
		req.RemoteAddr = "127.0.0.1:12345" // Localhost IP, but marked as external connection

		// Mark the request as coming from the external listener
		ctx := context.WithValue(req.Context(), middleware.ContextKeyExternalConnection, true)
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		h.handleUploadImageFromPath(w, req, store, "test-session-frompath-ext")

		if w.Code != http.StatusForbidden {
			t.Errorf("Status = %d, want %d (external connections should be rejected)", w.Code, http.StatusForbidden)
		}
	})

	// Test case 2: External connection via Tailscale (100.x.x.x IP range)
	// Tailscale connections to the external port should be rejected
	t.Run("tailscale_via_external_port", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath-ext/images/from-path", nil)
		req.RemoteAddr = "100.64.0.1:12345" // Tailscale CGNAT IP range

		// Mark the request as coming from the external listener
		ctx := context.WithValue(req.Context(), middleware.ContextKeyExternalConnection, true)
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		h.handleUploadImageFromPath(w, req, store, "test-session-frompath-ext")

		if w.Code != http.StatusForbidden {
			t.Errorf("Status = %d, want %d (Tailscale connections via external port should be rejected)", w.Code, http.StatusForbidden)
		}
	})

	// Test case 3: External connection with spoofed X-Forwarded-For header
	// Even if attacker spoofs localhost in X-Forwarded-For, external marker takes precedence
	t.Run("spoofed_xff_via_external_port", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath-ext/images/from-path", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		req.Header.Set("X-Forwarded-For", "127.0.0.1") // Attacker tries to spoof localhost

		// Mark the request as coming from the external listener
		ctx := context.WithValue(req.Context(), middleware.ContextKeyExternalConnection, true)
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		h.handleUploadImageFromPath(w, req, store, "test-session-frompath-ext")

		if w.Code != http.StatusForbidden {
			t.Errorf("Status = %d, want %d (spoofed X-Forwarded-For should not bypass external check)", w.Code, http.StatusForbidden)
		}
	})
}

func TestHandleUploadImageFromPath_InvalidJSON(t *testing.T) {
	store, h := setupImageTestHandlers(t, "test-session-frompath2")

	// Request from localhost with invalid JSON
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath2/images/from-path", nil)
	req.RemoteAddr = "127.0.0.1:12345" // Localhost
	w := httptest.NewRecorder()

	h.handleUploadImageFromPath(w, req, store, "test-session-frompath2")

	// Should be bad request for invalid JSON
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUploadImageFromPath_EmptyPaths(t *testing.T) {
	store, h := setupImageTestHandlers(t, "test-session-frompath3")

	// Request from localhost with empty paths
	body := strings.NewReader(`{"paths": []}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session-frompath3/images/from-path", body)
	req.RemoteAddr = "127.0.0.1:12345" // Localhost
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleUploadImageFromPath(w, req, store, "test-session-frompath3")

	// Should be bad request for empty paths
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
