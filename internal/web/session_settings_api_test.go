package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestHandleGetSessionSettings_EmptySettings(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session without any advanced settings
	meta := session.Metadata{
		SessionID:  "20260217-120000-settings1",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260217-120000-settings1/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetSessionSettings(w, req, "20260217-120000-settings1")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should return empty object, not null
	if resp.Settings == nil {
		t.Error("Settings should be empty object, not nil")
	}
	if len(resp.Settings) != 0 {
		t.Errorf("Settings should be empty, got %v", resp.Settings)
	}
}

func TestHandleGetSessionSettings_WithSettings(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with advanced settings
	meta := session.Metadata{
		SessionID:  "20260217-120000-settings2",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		AdvancedSettings: map[string]bool{
			"allow_external_images":  true,
			"disable_code_execution": false,
		},
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260217-120000-settings2/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetSessionSettings(w, req, "20260217-120000-settings2")

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
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent/settings", nil)
	w := httptest.NewRecorder()

	server.handleGetSessionSettings(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpdateSessionSettings_PartialUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with some existing settings
	meta := session.Metadata{
		SessionID:  "20260217-120000-settings3",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		AdvancedSettings: map[string]bool{
			"existing_flag": true,
		},
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Update with a new setting
	reqBody := SettingsUpdateRequest{
		Settings: map[string]bool{
			"new_flag": true,
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260217-120000-settings3/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSessionSettings(w, req, "20260217-120000-settings3")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should have both existing and new settings
	if len(resp.Settings) != 2 {
		t.Errorf("Settings should have 2 entries, got %d: %v", len(resp.Settings), resp.Settings)
	}
	if !resp.Settings["existing_flag"] {
		t.Error("existing_flag should still be true")
	}
	if !resp.Settings["new_flag"] {
		t.Error("new_flag should be true")
	}

	// Verify persistence
	updatedMeta, err := store.GetMetadata("20260217-120000-settings3")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if len(updatedMeta.AdvancedSettings) != 2 {
		t.Errorf("Persisted settings should have 2 entries, got %d", len(updatedMeta.AdvancedSettings))
	}
}

func TestHandleUpdateSessionSettings_OverwriteExisting(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with an existing setting
	meta := session.Metadata{
		SessionID:  "20260217-120000-settings4",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		AdvancedSettings: map[string]bool{
			"flag_to_change": true,
		},
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Update the existing setting to false
	reqBody := SettingsUpdateRequest{
		Settings: map[string]bool{
			"flag_to_change": false,
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260217-120000-settings4/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSessionSettings(w, req, "20260217-120000-settings4")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Settings["flag_to_change"] {
		t.Error("flag_to_change should be false after update")
	}
}

func TestHandleUpdateSessionSettings_InitializeFromNil(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session without any settings
	meta := session.Metadata{
		SessionID:  "20260217-120000-settings5",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Add a new setting to a session that had nil settings
	reqBody := SettingsUpdateRequest{
		Settings: map[string]bool{
			"first_flag": true,
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260217-120000-settings5/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSessionSettings(w, req, "20260217-120000-settings5")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.Settings["first_flag"] {
		t.Error("first_flag should be true")
	}
}

func TestHandleUpdateSessionSettings_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	reqBody := SettingsUpdateRequest{
		Settings: map[string]bool{
			"some_flag": true,
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/nonexistent/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSessionSettings(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSessionSettings_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/someid/settings", nil)
	w := httptest.NewRecorder()

	server.handleSessionSettings(w, req, "someid")

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}
