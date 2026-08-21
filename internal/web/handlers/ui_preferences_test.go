package handlers

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
	h := New(Deps{})

	// Test DELETE method (not allowed)
	req := httptest.NewRequest(http.MethodDelete, "/api/ui-preferences", nil)
	w := httptest.NewRecorder()

	h.HandleUIPreferences(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleUIPreferences_GET_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	w := httptest.NewRecorder()

	h.handleGetUIPreferences(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var prefs UIPreferences
	if err := json.NewDecoder(w.Body).Decode(&prefs); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if prefs.GroupingMode != "" {
		t.Errorf("GroupingMode = %q, want empty", prefs.GroupingMode)
	}
	if len(prefs.ExpandedGroups) > 0 {
		t.Errorf("ExpandedGroups = %v, want nil or empty", prefs.ExpandedGroups)
	}
}

func TestHandleUIPreferences_PUT_ValidData(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	body := `{"grouping_mode":"server","expanded_groups":{"auggie":false,"claude":true}}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	prefsPath := filepath.Join(tmpDir, appdir.UIPreferencesFileName)
	if _, err := os.Stat(prefsPath); os.IsNotExist(err) {
		t.Fatalf("Preferences file was not created at %s", prefsPath)
	}

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
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	body := `{"grouping_mode":"invalid_mode"}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Failed to unmarshal error envelope: %v (body=%q)", err, w.Body.String())
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "bad_request")
	}
	const wantMsg = "Invalid grouping_mode: must be 'none', 'server', 'folder', or 'workspace'"
	if env.Error.Message != wantMsg {
		t.Errorf("error.message = %q, want %q", env.Error.Message, wantMsg)
	}
}

func TestHandleUIPreferences_PUT_InvalidJSON(t *testing.T) {
	h := New(Deps{})

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUIPreferences_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	saveBody := `{"grouping_mode":"folder","expanded_groups":{"project1":true,"project2":false},"dashboard_hidden_charts":["tokens"]}`
	saveReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveW := httptest.NewRecorder()

	h.handleSaveUIPreferences(saveW, saveReq)

	if saveW.Code != http.StatusOK {
		t.Fatalf("Save failed: Status = %d, Body: %s", saveW.Code, saveW.Body.String())
	}

	loadReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	loadW := httptest.NewRecorder()

	h.handleGetUIPreferences(loadW, loadReq)

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
	if len(prefs.DashboardHiddenCharts) != 1 || prefs.DashboardHiddenCharts[0] != "tokens" {
		t.Errorf("DashboardHiddenCharts = %v, want [tokens]", prefs.DashboardHiddenCharts)
	}
}

func TestHandleUIPreferences_PUT_AllValidModes(t *testing.T) {
	validModes := []string{"none", "server", "folder", ""}

	for _, mode := range validModes {
		t.Run("mode_"+mode, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv(appdir.MittoDirEnv, tmpDir)
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)

			h := New(Deps{})

			body := `{"grouping_mode":"` + mode + `"}`
			req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.handleSaveUIPreferences(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Status = %d, want %d for mode %q", w.Code, http.StatusOK, mode)
			}
		})
	}
}

func TestHandleUIPreferences_PUT_EmptyBody(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	body := `{}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleUIPreferences_PUT_InvalidFontSize(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	body := `{"font_size":"huge"}`
	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Failed to unmarshal error envelope: %v (body=%q)", err, w.Body.String())
	}
	const wantMsg = "Invalid font_size: must be 'small' or 'large'"
	if env.Error.Message != wantMsg {
		t.Errorf("error.message = %q, want %q", env.Error.Message, wantMsg)
	}
}

func TestHandleUIPreferences_FontSize_RoundTrip(t *testing.T) {
	for _, size := range []string{"small", "large", ""} {
		t.Run("size_"+size, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv(appdir.MittoDirEnv, tmpDir)
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)

			h := New(Deps{})

			saveBody := `{"font_size":"` + size + `"}`
			saveReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(saveBody))
			saveReq.Header.Set("Content-Type", "application/json")
			saveW := httptest.NewRecorder()
			h.handleSaveUIPreferences(saveW, saveReq)
			if saveW.Code != http.StatusOK {
				t.Fatalf("Save failed for %q: Status = %d, Body: %s", size, saveW.Code, saveW.Body.String())
			}

			loadReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
			loadW := httptest.NewRecorder()
			h.handleGetUIPreferences(loadW, loadReq)
			if loadW.Code != http.StatusOK {
				t.Fatalf("Load failed for %q: Status = %d", size, loadW.Code)
			}

			var prefs UIPreferences
			if err := json.NewDecoder(loadW.Body).Decode(&prefs); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}
			if prefs.FontSize != size {
				t.Errorf("FontSize = %q, want %q", prefs.FontSize, size)
			}
		})
	}
}

func TestHandleUIPreferences_PUT_InvalidTheme(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"bad_theme", `{"theme":"twilight"}`, "Invalid theme: must be 'light' or 'dark'"},
		{"bad_theme_light", `{"theme_light":"has space"}`, "Invalid theme_light: must be a short alphanumeric name"},
		{"bad_theme_light_symbols", `{"theme_light":"one$two"}`, "Invalid theme_light: must be a short alphanumeric name"},
		{"bad_theme_dark", `{"theme_dark":"weird/name"}`, "Invalid theme_dark: must be a short alphanumeric name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv(appdir.MittoDirEnv, tmpDir)
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)

			h := New(Deps{})

			req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.handleSaveUIPreferences(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Status = %d, want %d (body=%q)", w.Code, http.StatusBadRequest, w.Body.String())
			}
			var env struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("Failed to unmarshal error envelope: %v (body=%q)", err, w.Body.String())
			}
			if env.Error.Message != tc.wantMsg {
				t.Errorf("error.message = %q, want %q", env.Error.Message, tc.wantMsg)
			}
		})
	}
}

func TestHandleUIPreferences_ThemeCluster_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	// Explicit false for the two bool pointers so we exercise the *bool
	// serialization (nil vs false vs true) end-to-end.
	saveBody := `{
		"theme":"dark",
		"theme_light":"cupcake",
		"theme_dark":"synthwave",
		"follow_system_theme":false,
		"follow_system_reduced_motion":true,
		"reduce_animations":false,
		"font_size":"large"
	}`
	saveReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveW := httptest.NewRecorder()
	h.handleSaveUIPreferences(saveW, saveReq)
	if saveW.Code != http.StatusOK {
		t.Fatalf("Save failed: Status = %d, Body: %s", saveW.Code, saveW.Body.String())
	}

	loadReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	loadW := httptest.NewRecorder()
	h.handleGetUIPreferences(loadW, loadReq)
	if loadW.Code != http.StatusOK {
		t.Fatalf("Load failed: Status = %d", loadW.Code)
	}

	var prefs UIPreferences
	if err := json.NewDecoder(loadW.Body).Decode(&prefs); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if prefs.Theme != "dark" {
		t.Errorf("Theme = %q, want %q", prefs.Theme, "dark")
	}
	if prefs.ThemeLight != "cupcake" {
		t.Errorf("ThemeLight = %q, want %q", prefs.ThemeLight, "cupcake")
	}
	if prefs.ThemeDark != "synthwave" {
		t.Errorf("ThemeDark = %q, want %q", prefs.ThemeDark, "synthwave")
	}
	if prefs.FollowSystemTheme == nil || *prefs.FollowSystemTheme != false {
		t.Errorf("FollowSystemTheme = %v, want *false", prefs.FollowSystemTheme)
	}
	if prefs.FollowSystemReducedMotion == nil || *prefs.FollowSystemReducedMotion != true {
		t.Errorf("FollowSystemReducedMotion = %v, want *true", prefs.FollowSystemReducedMotion)
	}
	if prefs.ReduceAnimations == nil || *prefs.ReduceAnimations != false {
		t.Errorf("ReduceAnimations = %v, want *false", prefs.ReduceAnimations)
	}
	if prefs.FontSize != "large" {
		t.Errorf("FontSize = %q, want %q", prefs.FontSize, "large")
	}
}

func TestHandleUIPreferences_PUT_ValidThemeNames(t *testing.T) {
	// Accepted character-set: letters, digits, dashes; up to 32 chars.
	validNames := []string{"mitto", "light", "dark", "cupcake", "cyberpunk", "silk", "abyss", "a1-b2"}
	for _, name := range validNames {
		t.Run("name_"+name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv(appdir.MittoDirEnv, tmpDir)
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)

			h := New(Deps{})

			body := `{"theme_light":"` + name + `","theme_dark":"` + name + `"}`
			req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.handleSaveUIPreferences(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("Status = %d, want %d for name %q; body=%s", w.Code, http.StatusOK, name, w.Body.String())
			}
		})
	}
}

func TestHandleUIPreferences_DispatchesByMethod(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	getReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	getW := httptest.NewRecorder()
	h.HandleUIPreferences(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Errorf("GET Status = %d, want %d", getW.Code, http.StatusOK)
	}

	putBody := `{"grouping_mode":"server"}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	h.HandleUIPreferences(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Errorf("PUT Status = %d, want %d", putW.Code, http.StatusOK)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/ui-preferences", nil)
	postW := httptest.NewRecorder()
	h.HandleUIPreferences(postW, postReq)
	if postW.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST Status = %d, want %d", postW.Code, http.StatusMethodNotAllowed)
	}
}

// TestHandleUIPreferences_PUT_DashboardHiddenCharts_Valid exercises the happy
// paths of the new DashboardHiddenCharts field: single ID, multiple IDs, and
// an empty list. Each save must round-trip cleanly through GET.
//
// The empty-list sub-case verifies the opt-out invariant: when nothing is
// hidden, filterKnownChartIDs returns nil and the JSON field is omitted, so
// the loaded response has DashboardHiddenCharts == nil (len 0) — which is
// what any client should observe on a fresh install.
func TestHandleUIPreferences_PUT_DashboardHiddenCharts_Valid(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"single_id", `{"dashboard_hidden_charts":["tokens"]}`, []string{"tokens"}},
		{"multiple_ids", `{"dashboard_hidden_charts":["tokens","tool_calls","prompts_vs_turns"]}`, []string{"tokens", "tool_calls", "prompts_vs_turns"}},
		{"empty_list", `{"dashboard_hidden_charts":[]}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv(appdir.MittoDirEnv, tmpDir)
			appdir.ResetCache()
			t.Cleanup(appdir.ResetCache)

			h := New(Deps{})

			saveReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(tc.body))
			saveReq.Header.Set("Content-Type", "application/json")
			saveW := httptest.NewRecorder()
			h.handleSaveUIPreferences(saveW, saveReq)
			if saveW.Code != http.StatusOK {
				t.Fatalf("Save failed: Status = %d, Body: %s", saveW.Code, saveW.Body.String())
			}

			loadReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
			loadW := httptest.NewRecorder()
			h.handleGetUIPreferences(loadW, loadReq)
			if loadW.Code != http.StatusOK {
				t.Fatalf("Load failed: Status = %d", loadW.Code)
			}

			var prefs UIPreferences
			if err := json.NewDecoder(loadW.Body).Decode(&prefs); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			if len(prefs.DashboardHiddenCharts) != len(tc.want) {
				t.Fatalf("DashboardHiddenCharts = %v, want %v", prefs.DashboardHiddenCharts, tc.want)
			}
			for i, id := range tc.want {
				if prefs.DashboardHiddenCharts[i] != id {
					t.Errorf("DashboardHiddenCharts[%d] = %q, want %q", i, prefs.DashboardHiddenCharts[i], id)
				}
			}
		})
	}
}

// TestHandleUIPreferences_PUT_DashboardHiddenCharts_RejectsHidingAll verifies
// that a payload attempting to hide every known chart is rejected with 400
// and the canonical error code "at_least_one_chart_must_be_visible" — matches
// the "cannot uncheck all" UX constraint from mitto-3i2.
func TestHandleUIPreferences_PUT_DashboardHiddenCharts_RejectsHidingAll(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	// Build the payload from KnownDashboardChartIDs so this test tracks the
	// canonical list rather than hard-coding a snapshot of it.
	all, err := json.Marshal(KnownDashboardChartIDs)
	if err != nil {
		t.Fatalf("Failed to marshal KnownDashboardChartIDs: %v", err)
	}
	body := `{"dashboard_hidden_charts":` + string(all) + `}`

	req := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleSaveUIPreferences(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d (body=%q)", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Failed to unmarshal error envelope: %v (body=%q)", err, w.Body.String())
	}
	if env.Error.Code != "at_least_one_chart_must_be_visible" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "at_least_one_chart_must_be_visible")
	}
	const wantMsg = "At least one dashboard chart must remain visible"
	if env.Error.Message != wantMsg {
		t.Errorf("error.message = %q, want %q", env.Error.Message, wantMsg)
	}
}

// TestHandleUIPreferences_PUT_DashboardHiddenCharts_StripsUnknown verifies
// that unknown chart IDs are silently dropped by the server-side validator
// (defensive against stale clients that still send retired IDs), while
// known IDs in the same payload are preserved.
func TestHandleUIPreferences_PUT_DashboardHiddenCharts_StripsUnknown(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	h := New(Deps{})

	saveBody := `{"dashboard_hidden_charts":["tokens","bogus","also_bogus"]}`
	saveReq := httptest.NewRequest(http.MethodPut, "/api/ui-preferences", strings.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveW := httptest.NewRecorder()
	h.handleSaveUIPreferences(saveW, saveReq)
	if saveW.Code != http.StatusOK {
		t.Fatalf("Save failed: Status = %d, Body: %s", saveW.Code, saveW.Body.String())
	}

	loadReq := httptest.NewRequest(http.MethodGet, "/api/ui-preferences", nil)
	loadW := httptest.NewRecorder()
	h.handleGetUIPreferences(loadW, loadReq)
	if loadW.Code != http.StatusOK {
		t.Fatalf("Load failed: Status = %d", loadW.Code)
	}

	var prefs UIPreferences
	if err := json.NewDecoder(loadW.Body).Decode(&prefs); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if len(prefs.DashboardHiddenCharts) != 1 || prefs.DashboardHiddenCharts[0] != "tokens" {
		t.Errorf("DashboardHiddenCharts = %v, want [tokens]", prefs.DashboardHiddenCharts)
	}
}
