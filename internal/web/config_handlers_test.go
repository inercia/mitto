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
	"github.com/inercia/mitto/internal/config"
)

func TestHandleConfig_MethodNotAllowed(t *testing.T) {
	server := &Server{
		config: Config{},
	}

	// Test DELETE method (not allowed)
	req := httptest.NewRequest(http.MethodDelete, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleConfig(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleGetConfig(t *testing.T) {
	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				ACPServers: []config.ACPServer{
					{Name: "test-server", Command: "test-cmd"},
				},
			},
		},
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify response contains JSON
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestHandleGetConfig_NilMittoConfig(t *testing.T) {
	server := &Server{
		config:         Config{},
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleSaveConfig_InvalidJSON(t *testing.T) {
	server := &Server{
		config: Config{},
	}

	// Test with invalid JSON body
	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleSaveConfig(w, req)

	// Should return 400 Bad Request for invalid JSON
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSaveConfig_ReadOnly(t *testing.T) {
	server := &Server{
		config: Config{
			ConfigReadOnly: true,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleSaveConfig(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestHandleConfig_GET(t *testing.T) {
	server := &Server{
		config:         Config{},
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleConfig_POST(t *testing.T) {
	server := &Server{
		config: Config{},
	}

	// POST without body should return 400
	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImprovePrompt_MethodNotAllowed(t *testing.T) {
	server := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/improve-prompt", nil)
	w := httptest.NewRecorder()

	server.handleImprovePrompt(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleImprovePrompt_EmptyPrompt(t *testing.T) {
	server := &Server{}

	body := strings.NewReader(`{"prompt": ""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/improve-prompt", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleImprovePrompt(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleImprovePrompt_InvalidJSON(t *testing.T) {
	server := &Server{}

	req := httptest.NewRequest(http.MethodPost, "/api/improve-prompt", nil)
	w := httptest.NewRecorder()

	server.handleImprovePrompt(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSaveConfig_ValidRequest(t *testing.T) {
	// Use temp dir to avoid writing to real settings file
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := NewSessionManager("test-cmd", "test-server", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "test-server"},
	})

	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				ACPServers: []config.ACPServer{
					{Name: "test-server", Command: "test-cmd"},
				},
			},
		},
		sessionManager: sm,
	}

	body := strings.NewReader(`{
		"workspaces": [{"working_dir": "/workspace1", "acp_server": "test-server"}],
		"acp_servers": [{"name": "test-server", "command": "test-cmd"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveConfig(w, req)

	// With proper temp dir setup, this should now succeed
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandleSaveConfig_EmptyWorkspaces(t *testing.T) {
	// Use temp dir to avoid writing to real settings file
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := NewSessionManager("test-cmd", "test-server", false, nil)

	server := &Server{
		config:         Config{},
		sessionManager: sm,
	}

	body := strings.NewReader(`{
		"workspaces": [],
		"acp_servers": [{"name": "test-server", "command": "test-cmd"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSaveConfig_EmptyACPServers(t *testing.T) {
	// Use temp dir to avoid writing to real settings file
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := NewSessionManager("test-cmd", "test-server", false, nil)

	server := &Server{
		config:         Config{},
		sessionManager: sm,
	}

	body := strings.NewReader(`{
		"workspaces": [{"working_dir": "/workspace1", "acp_server": "test-server"}],
		"acp_servers": []
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHasValidCredentials(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.WebAuth
		expected bool
	}{
		{
			name:     "nil config",
			config:   nil,
			expected: false,
		},
		{
			name:     "nil simple auth",
			config:   &config.WebAuth{Simple: nil},
			expected: false,
		},
		{
			name:     "empty username",
			config:   &config.WebAuth{Simple: &config.SimpleAuth{Username: "", Password: "pass"}},
			expected: false,
		},
		{
			name:     "empty password",
			config:   &config.WebAuth{Simple: &config.SimpleAuth{Username: "user", Password: ""}},
			expected: false,
		},
		{
			name:     "valid credentials",
			config:   &config.WebAuth{Simple: &config.SimpleAuth{Username: "user", Password: "pass"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasValidCredentials(tt.config); got != tt.expected {
				t.Errorf("hasValidCredentials() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestApplyAuthChanges_DisabledToEnabled_InvalidCredentials(t *testing.T) {
	server := &Server{
		config: Config{},
	}

	// Try to enable auth with invalid credentials - should not panic
	server.applyAuthChanges(false, true, nil)

	// Auth manager should not be created
	if server.authManager != nil {
		t.Error("authManager should be nil when credentials are invalid")
	}
}

func TestHandleSupportedRunners(t *testing.T) {
	server := &Server{
		config: Config{},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/supported-runners", nil)
	w := httptest.NewRecorder()

	server.handleSupportedRunners(w, req)

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
	server := &Server{
		config: Config{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/supported-runners", nil)
	w := httptest.NewRecorder()

	server.handleSupportedRunners(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestApplyAuthChanges_DisabledToEnabled_ValidCredentials(t *testing.T) {
	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				Web: config.WebConfig{
					ExternalPort: -1, // Disabled to avoid actually starting listener
				},
			},
		},
		externalPort: -1, // Also set the server's external port to disabled
	}

	authConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "testuser",
			Password: "testpass",
		},
	}

	server.applyAuthChanges(false, true, authConfig)

	// Auth manager should be created
	if server.authManager == nil {
		t.Error("authManager should be created when credentials are valid")
	}
}

func TestApplyAuthChanges_EnabledToDisabled(t *testing.T) {
	authConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "testuser",
			Password: "testpass",
		},
	}

	server := &Server{
		config:      Config{},
		authManager: NewAuthManager(authConfig),
	}

	server.applyAuthChanges(true, false, nil)

	// Auth manager should still exist but be disabled
	// (we don't destroy it, just update config to nil)
	if server.authManager == nil {
		t.Error("authManager should still exist after disabling")
	}
}

func TestApplyAuthChanges_EnabledToEnabled_UpdateCredentials(t *testing.T) {
	oldConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "olduser",
			Password: "oldpass",
		},
	}

	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				Web: config.WebConfig{
					ExternalPort: -1, // Disabled
				},
			},
		},
		authManager:  NewAuthManager(oldConfig),
		externalPort: -1, // Also set the server's external port to disabled
	}

	newConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "newuser",
			Password: "newpass",
		},
	}

	server.applyAuthChanges(true, true, newConfig)

	// Auth manager should still exist
	if server.authManager == nil {
		t.Error("authManager should still exist after updating credentials")
	}
}

func TestApplyAuthChanges_EnabledToEnabled_InvalidCredentials(t *testing.T) {
	oldConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "user",
			Password: "pass",
		},
	}

	server := &Server{
		config:      Config{},
		authManager: NewAuthManager(oldConfig),
	}

	// Update with invalid credentials
	server.applyAuthChanges(true, true, nil)

	// Auth manager config should be updated to nil (disabled)
	// The auth manager itself still exists
	if server.authManager == nil {
		t.Error("authManager should still exist")
	}
}

func TestApplyAuthChanges_DisabledToDisabled(t *testing.T) {
	server := &Server{
		config: Config{},
	}

	// Nothing should happen
	server.applyAuthChanges(false, false, nil)

	// Auth manager should remain nil
	if server.authManager != nil {
		t.Error("authManager should remain nil")
	}
}

func TestHandleSaveConfig_UIWithNativeNotifications(t *testing.T) {
	// Use temp dir to avoid writing to real settings file
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := NewSessionManager("test-cmd", "test-server", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "test-server"},
	})

	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				ACPServers: []config.ACPServer{
					{Name: "test-server", Command: "test-cmd"},
				},
			},
		},
		sessionManager: sm,
	}

	// Simulate what the frontend sends
	body := strings.NewReader(`{
		"workspaces": [{"working_dir": "/workspace1", "acp_server": "test-server"}],
		"acp_servers": [{"name": "test-server", "command": "test-cmd"}],
		"ui": {
			"confirmations": {
				"delete_session": false
			},
			"mac": {
				"notifications": {
					"sounds": {
						"agent_completed": true
					},
					"native_enabled": true
				},
				"show_in_all_spaces": true
			}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Read the saved settings file and verify native_enabled is saved
	settingsPath := filepath.Join(tmpDir, appdir.SettingsFileName)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	t.Logf("Saved settings: %s", string(data))

	var settings config.Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("Failed to parse settings: %v", err)
	}

	if settings.UI.Mac == nil {
		t.Fatal("Settings.UI.Mac is nil")
	}

	if settings.UI.Mac.Notifications == nil {
		t.Fatal("Settings.UI.Mac.Notifications is nil")
	}

	if !settings.UI.Mac.Notifications.NativeEnabled {
		t.Errorf("NativeEnabled = false, want true")
	}

	if settings.UI.Mac.Notifications.Sounds == nil || !settings.UI.Mac.Notifications.Sounds.AgentCompleted {
		t.Errorf("AgentCompleted = false, want true")
	}
}
