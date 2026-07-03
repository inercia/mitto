package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleCallbackTrigger_MethodNotAllowed verifies GET returns 405.
func TestHandleCallbackTrigger_MethodNotAllowed(t *testing.T) {
	h := New(Deps{APIPrefix: ""})

	req := httptest.NewRequest(http.MethodGet, "/api/callback/test-token", nil)
	rec := httptest.NewRecorder()

	h.HandleCallbackTrigger(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", rec.Code)
	}

	// Verify JSON response
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if resp["error"] != "method_not_allowed" {
		t.Errorf("Expected error code 'method_not_allowed', got %q", resp["error"])
	}
}

// TestHandleCallbackTrigger_InvalidToken verifies malformed token returns 400.
func TestHandleCallbackTrigger_InvalidToken(t *testing.T) {
	h := New(Deps{APIPrefix: ""})

	// Invalid token (too short or wrong format)
	req := httptest.NewRequest(http.MethodPost, "/api/callback/bad", nil)
	rec := httptest.NewRecorder()

	h.HandleCallbackTrigger(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rec.Code)
	}

	// Verify JSON response
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if resp["error"] != "invalid_token" {
		t.Errorf("Expected error code 'invalid_token', got %q", resp["error"])
	}
}
