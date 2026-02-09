package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
)

func TestHandleUIPreferences_MethodNotAllowed(t *testing.T) {
	server := &Server{}

	// Test DELETE method (not allowed)
	req := httptest.NewRequest(http.MethodDelete, "/api/ui-preferences", nil)
	w := httptest.NewRecorder()

	server.handleUIPreferences(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleUIPreferences_GET_EmptyFile(t *testing.T) {
	// Set up temp directory for appdir
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	server := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	w := httptest.NewRecorder()

	server.handleGetUIPreferences(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify response is empty preferences
	var prefs UIPreferences
	if err := json.NewDecoder(w.Body).Decode(&prefs); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if prefs.GroupingMode != "" {
		t.Errorf("GroupingMode = %q, want empty", prefs.GroupingMode)
	}
	if prefs.ExpandedGroups != nil && len(prefs.ExpandedGroups) > 0 {
		t.Errorf("ExpandedGroups = %v, want nil or empty", prefs.ExpandedGroups)
	}
}

func TestHandleUIPreferences_PUT_ValidData(t *testing.T) {
	// Set up temp directory for appdir
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	server := &Server{}

	body := `{"grouping_mode":"server","expanded_groups":{"auggie":false,"claude":true}}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify file was created
	prefsPath := filepath.Join(tmpDir, appdir.UIPreferencesFileName)
	if _, err := os.Stat(prefsPath); os.IsNotExist(err) {
		t.Fatalf("Preferences file was not created at %s", prefsPath)
	}

	// Verify file contents
	data, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatalf("Failed to read preferences file: %v", err)
	}

	var savedPrefs UIPreferences
	if err := json.Unmarshal(data, &savedPrefs); err != nil {
		t.Fatalf("Failed to parse saved preferences: %v", err)
	}

	if savedPrefs.GroupingMode != "server" {
		t.Errorf("Saved GroupingMode = %q, want %q", savedPrefs.GroupingMode, "server")
	}
	if savedPrefs.ExpandedGroups["auggie"] != false {
		t.Errorf("Saved ExpandedGroups[auggie] = %v, want false", savedPrefs.ExpandedGroups["auggie"])
	}
	if savedPrefs.ExpandedGroups["claude"] != true {
		t.Errorf("Saved ExpandedGroups[claude] = %v, want true", savedPrefs.ExpandedGroups["claude"])
	}
}

func TestHandleUIPreferences_PUT_InvalidGroupingMode(t *testing.T) {
	// Set up temp directory for appdir
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	server := &Server{}

	body := `{"grouping_mode":"invalid_mode"}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUIPreferences_PUT_InvalidJSON(t *testing.T) {
	server := &Server{}

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUIPreferences_RoundTrip(t *testing.T) {
	// Set up temp directory for appdir
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	server := &Server{}

	// Save preferences
	saveBody := `{"grouping_mode":"folder","expanded_groups":{"project1":true,"project2":false}}`
	saveReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveW := httptest.NewRecorder()

	server.handleSaveUIPreferences(saveW, saveReq)

	if saveW.Code != http.StatusOK {
		t.Fatalf("Save failed: Status = %d, Body: %s", saveW.Code, saveW.Body.String())
	}

	// Load preferences
	loadReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	loadW := httptest.NewRecorder()

	server.handleGetUIPreferences(loadW, loadReq)

	if loadW.Code != http.StatusOK {
		t.Fatalf("Load failed: Status = %d", loadW.Code)
	}

	var prefs UIPreferences
	if err := json.NewDecoder(loadW.Body).Decode(&prefs); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if prefs.GroupingMode != "folder" {
		t.Errorf("GroupingMode = %q, want %q", prefs.GroupingMode, "folder")
	}
	if len(prefs.ExpandedGroups) != 2 {
		t.Errorf("ExpandedGroups length = %d, want 2", len(prefs.ExpandedGroups))
	}
	if prefs.ExpandedGroups["project1"] != true {
		t.Errorf("ExpandedGroups[project1] = %v, want true", prefs.ExpandedGroups["project1"])
	}
	if prefs.ExpandedGroups["project2"] != false {
		t.Errorf("ExpandedGroups[project2] = %v, want false", prefs.ExpandedGroups["project2"])
	}
}

func TestHandleUIPreferences_PUT_AllValidModes(t *testing.T) {
	validModes := []string{"none", "server", "folder", ""}

	for _, mode := range validModes {
		t.Run("mode_"+mode, func(t *testing.T) {
			// Set up temp directory for appdir
			tmpDir := t.TempDir()
			t.Setenv(appdir.MittoDirEnv, tmpDir)
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)

			server := &Server{}

			body := `{"grouping_mode":"` + mode + `"}`
			req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleSaveUIPreferences(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Status = %d, want %d for mode %q", w.Code, http.StatusOK, mode)
			}
		})
	}
}

func TestHandleUIPreferences_PUT_EmptyBody(t *testing.T) {
	// Set up temp directory for appdir
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	server := &Server{}

	// Empty JSON object should be valid
	body := `{}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleUIPreferences_DispatchesByMethod(t *testing.T) {
	// Set up temp directory for appdir
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	server := &Server{}

	// Test GET dispatches correctly
	getReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	getW := httptest.NewRecorder()
	server.handleUIPreferences(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Errorf("GET Status = %d, want %d", getW.Code, http.StatusOK)
	}

	// Test PUT dispatches correctly
	putBody := `{"grouping_mode":"server"}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	server.handleUIPreferences(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Errorf("PUT Status = %d, want %d", putW.Code, http.StatusOK)
	}

	// Test POST is not allowed
	postReq := httptest.NewRequest(http.MethodPost, "/api/ui-preferences", nil)
	postW := httptest.NewRecorder()
	server.handleUIPreferences(postW, postReq)
	if postW.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST Status = %d, want %d", postW.Code, http.StatusMethodNotAllowed)
	}
}
