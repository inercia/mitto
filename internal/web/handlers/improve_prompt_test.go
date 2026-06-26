package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleImprovePrompt_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/aux/improve-prompt", nil)
	w := httptest.NewRecorder()

	h.HandleImprovePrompt(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleImprovePrompt_EmptyPrompt(t *testing.T) {
	h := New(Deps{})

	body := strings.NewReader(`{"prompt": ""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/aux/improve-prompt", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleImprovePrompt(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "bad_request")
	}
	if resp.Error.Message != "Prompt is required" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "Prompt is required")
	}
}

func TestHandleImprovePrompt_InvalidJSON(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/aux/improve-prompt", nil)
	w := httptest.NewRecorder()

	h.HandleImprovePrompt(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	// parseJSONBody uses writeErrorJSON → canonical envelope
	var resp2 struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp2.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", resp2.Error.Code, "bad_request")
	}
}
