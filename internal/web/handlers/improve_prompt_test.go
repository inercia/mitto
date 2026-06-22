package handlers

import (
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
}

func TestHandleImprovePrompt_InvalidJSON(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/aux/improve-prompt", nil)
	w := httptest.NewRecorder()

	h.HandleImprovePrompt(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
