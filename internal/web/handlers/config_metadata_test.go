package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleAdvancedFlags(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/advanced-flags", nil)
	w := httptest.NewRecorder()

	h.HandleAdvancedFlags(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify response contains JSON
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}

	// Parse response - now returns an object with flags and configured_defaults
	var response struct {
		Flags []struct {
			Name        string `json:"name"`
			Label       string `json:"label"`
			Description string `json:"description"`
			Default     bool   `json:"default"`
		} `json:"flags"`
		ConfiguredDefaults map[string]bool `json:"configured_defaults"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should have at least the can_do_introspection flag
	if len(response.Flags) < 1 {
		t.Fatalf("Expected at least 1 flag, got %d", len(response.Flags))
	}

	// Configured defaults should be non-nil (even if empty)
	if response.ConfiguredDefaults == nil {
		t.Error("configured_defaults should not be nil")
	}

	// Find can_do_introspection flag
	found := false
	for _, flag := range response.Flags {
		if flag.Name == "can_do_introspection" {
			found = true
			if flag.Label == "" {
				t.Error("can_do_introspection should have a label")
			}
			if flag.Description == "" {
				t.Error("can_do_introspection should have a description")
			}
			if flag.Default != false {
				t.Errorf("can_do_introspection default should be false, got %v", flag.Default)
			}
			break
		}
	}
	if !found {
		t.Error("can_do_introspection flag not found in response")
	}
}

func TestHandleAdvancedFlags_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/advanced-flags", nil)
	w := httptest.NewRecorder()

	h.HandleAdvancedFlags(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRunnerDefaults(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/runner-defaults", nil)
	w := httptest.NewRecorder()

	h.HandleRunnerDefaults(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleRunnerDefaults_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/runner-defaults", nil)
	w := httptest.NewRecorder()

	h.HandleRunnerDefaults(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleAgentTypes_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodPost, "/api/agent-types", nil)
	w := httptest.NewRecorder()

	h.HandleAgentTypes(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}
