package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// newSettingsStore creates a temp store with a single session whose metadata is
// provided by the caller, returning the store and the Handlers under test.
func newSettingsStore(t *testing.T, meta *session.Metadata) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if meta != nil {
		if err := store.Create(*meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}
	h := New(Deps{Store: store})
	return store, h
}

func TestHandleGetSessionSettings_EmptySettings(t *testing.T) {
	_, h := newSettingsStore(t, &session.Metadata{
		SessionID:  "20260217-120000-settings1",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260217-120000-settings1/settings", nil)
	w := httptest.NewRecorder()

	h.HandleGetSessionSettings(w, req, "20260217-120000-settings1")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Settings == nil {
		t.Error("Settings should be empty object, not nil")
	}
	if len(resp.Settings) != 0 {
		t.Errorf("Settings should be empty, got %v", resp.Settings)
	}
}

func TestHandleGetSessionSettings_WithSettings(t *testing.T) {
	_, h := newSettingsStore(t, &session.Metadata{
		SessionID:  "20260217-120000-settings2",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		AdvancedSettings: map[string]bool{
			"allow_external_images":  true,
			"disable_code_execution": false,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260217-120000-settings2/settings", nil)
	w := httptest.NewRecorder()

	h.HandleGetSessionSettings(w, req, "20260217-120000-settings2")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(resp.Settings) != 2 {
		t.Errorf("Settings should have 2 entries, got %d", len(resp.Settings))
	}
	if !resp.Settings["allow_external_images"] {
		t.Error("allow_external_images should be true")
	}
	if resp.Settings["disable_code_execution"] {
		t.Error("disable_code_execution should be false")
	}
}

func TestHandleGetSessionSettings_NotFound(t *testing.T) {
	_, h := newSettingsStore(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent/settings", nil)
	w := httptest.NewRecorder()

	h.HandleGetSessionSettings(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSessionSettings_MethodNotAllowed(t *testing.T) {
	h := New(Deps{})

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/someid/settings", nil)
	w := httptest.NewRecorder()

	h.HandleSessionSettings(w, req, "someid")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// decodeSettings issues a PATCH and returns the decoded response, asserting 200.
func patchSettings(t *testing.T, h *Handlers, sessionID string, settings map[string]bool) SettingsResponse {
	t.Helper()
	body, _ := json.Marshal(SettingsUpdateRequest{Settings: settings})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sessionID+"/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleUpdateSessionSettings(w, req, sessionID)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	return resp
}

func TestHandleUpdateSessionSettings_PartialUpdate(t *testing.T) {
	store, h := newSettingsStore(t, &session.Metadata{
		SessionID:        "20260217-120000-settings3",
		ACPServer:        "test-server",
		WorkingDir:       "/tmp",
		AdvancedSettings: map[string]bool{"existing_flag": true},
	})

	resp := patchSettings(t, h, "20260217-120000-settings3", map[string]bool{"new_flag": true})

	if len(resp.Settings) != 2 {
		t.Errorf("Settings should have 2 entries, got %d: %v", len(resp.Settings), resp.Settings)
	}
	if !resp.Settings["existing_flag"] {
		t.Error("existing_flag should still be true")
	}
	if !resp.Settings["new_flag"] {
		t.Error("new_flag should be true")
	}

	updatedMeta, err := store.GetMetadata("20260217-120000-settings3")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if len(updatedMeta.AdvancedSettings) != 2 {
		t.Errorf("Persisted settings should have 2 entries, got %d", len(updatedMeta.AdvancedSettings))
	}
}

func TestHandleUpdateSessionSettings_OverwriteExisting(t *testing.T) {
	_, h := newSettingsStore(t, &session.Metadata{
		SessionID:        "20260217-120000-settings4",
		ACPServer:        "test-server",
		WorkingDir:       "/tmp",
		AdvancedSettings: map[string]bool{"flag_to_change": true},
	})

	resp := patchSettings(t, h, "20260217-120000-settings4", map[string]bool{"flag_to_change": false})

	if resp.Settings["flag_to_change"] {
		t.Error("flag_to_change should be false after update")
	}
}

func TestHandleUpdateSessionSettings_InitializeFromNil(t *testing.T) {
	_, h := newSettingsStore(t, &session.Metadata{
		SessionID:  "20260217-120000-settings5",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	})

	resp := patchSettings(t, h, "20260217-120000-settings5", map[string]bool{"first_flag": true})

	if !resp.Settings["first_flag"] {
		t.Error("first_flag should be true")
	}
}

func TestHandleUpdateSessionSettings_NotFound(t *testing.T) {
	_, h := newSettingsStore(t, nil)

	body, _ := json.Marshal(SettingsUpdateRequest{Settings: map[string]bool{"some_flag": true}})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/nonexistent/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleUpdateSessionSettings(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
