package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleSupportedRunners(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/supported-runners", nil)
	w := httptest.NewRecorder()

	h.HandleSupportedRunners(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify response contains JSON array
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	// Decode and verify structure
	var runners []RunnerInfo
	if err := json.NewDecoder(w.Body).Decode(&runners); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should have at least exec runner
	if len(runners) == 0 {
		t.Error("Expected at least one runner")
	}

	// Verify exec runner is always present and supported
	foundExec := false
	for _, r := range runners {
		if r.Type == "exec" {
			foundExec = true
			if !r.Supported {
				t.Error("exec runner should always be supported")
			}
			if r.Label == "" {
				t.Error("exec runner should have a label")
			}
		}
	}
	if !foundExec {
		t.Error("exec runner should always be present")
	}
}

func TestHandleSupportedRunners_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/supported-runners", nil)
	w := httptest.NewRecorder()

	h.HandleSupportedRunners(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}
