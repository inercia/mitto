package web

import (
	"encoding/json"
	"github.com/inercia/mitto/internal/conversation"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web/handlers"
	"github.com/inercia/mitto/internal/web/middleware"
)

// handleGetConfig is a test-only shim delegating to the migrated
// handlers.HandleGetConfig. It lets the existing web-package config tests keep
// calling server.handleGetConfig directly, wiring the Deps from the server's
// fields and nil-safe methods.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	handlers.New(handlers.Deps{
		Logger:                s.logger,
		ConfigReadOnly:        s.config.ConfigReadOnly,
		MittoConfig:           s.config.MittoConfig,
		RCFilePath:            s.config.RCFilePath,
		HasRCFileServers:      s.config.HasRCFileServers,
		PromptsCache:          s.config.PromptsCache,
		HasExistingSimpleAuth: s.hasExistingSimpleAuth,
		Store:                 s.Store(),
		SessionManager:        s.sessionManager,
		APIPrefix:             s.apiPrefix,
		FilterPromptsForSession: func(prompts []config.WebPrompt, sessionID string) []config.WebPrompt {
			if visCtx := s.buildPromptEnabledContext(sessionID); visCtx != nil {
				return s.filterPromptsByEnabled(prompts, visCtx)
			}
			return prompts
		},
	}).HandleGetConfig(w, r)
}

// handleSaveConfig is a test-only shim delegating to the migrated
// handlers.HandleSaveConfig. It lets the existing web-package config tests keep
// calling server.handleSaveConfig directly, wiring the Deps from the server's
// fields and nil-safe methods.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	handlers.New(handlers.Deps{
		Logger:                    s.logger,
		ConfigReadOnly:            s.config.ConfigReadOnly,
		ValidateAndPrepareConfig:  s.validateAndPrepareSaveConfig,
		BuildNewSettings:          s.buildNewSettings,
		ApplyConfigChanges:        s.applyConfigChanges,
		AuthEnabled:               func() bool { return s.authManager != nil && s.authManager.IsEnabled() },
		IsExternalListenerRunning: s.IsExternalListenerRunning,
		GetExternalPort:           s.GetExternalPort,
	}).HandleSaveConfig(w, r)
}

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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	server.apiHandlers = handlers.New(handlers.Deps{SessionManager: server.sessionManager})

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
	// The POST branch of the dispatcher delegates to apiHandlers.HandleSaveConfig,
	// so apiHandlers must be wired. Empty Deps suffice: with ConfigReadOnly=false
	// the empty body fails JSON parsing and yields 400 before any closure is used.
	server.apiHandlers = handlers.New(handlers.Deps{})

	// POST without body should return 400
	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	w := httptest.NewRecorder()

	server.handleConfig(w, req)

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

	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
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

func TestHandleSaveConfig_ServerRenames_MigratesConversation(t *testing.T) {
	// Use temp dir to avoid writing to real settings file.
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Store with an existing (non-running) conversation referencing the old name.
	store, err := session.NewStore(filepath.Join(tmpDir, "sessions"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()
	if err := store.Create(session.Metadata{
		SessionID:  "conv-1",
		ACPServer:  "old-server",
		WorkingDir: "/workspace1",
		Name:       "Conversation 1",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := conversation.NewSessionManager("test-cmd", "old-server", false, nil)
	sm.SetStore(store)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "old-server"},
	})

	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				ACPServers: []config.ACPServer{
					{Name: "old-server", Command: "test-cmd"},
				},
			},
		},
		sessionManager: sm,
	}

	// Save renames "old-server" -> "new-server" (in place).
	body := strings.NewReader(`{
		"workspaces": [{"working_dir": "/workspace1", "acp_server": "new-server"}],
		"acp_servers": [{"name": "new-server", "command": "test-cmd"}],
		"server_renames": {"old-server": "new-server"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// The conversation's stored ACP server must have been migrated to the new name.
	meta, err := store.GetMetadata("conv-1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.ACPServer != "new-server" {
		t.Errorf("conversation ACPServer = %q, want %q (server_renames not applied)", meta.ACPServer, "new-server")
	}
}

func TestHandleSaveConfig_EmptyWorkspaces(t *testing.T) {
	// Use temp dir to avoid writing to real settings file
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)

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

	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)

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
		authManager: middleware.NewAuthManager(authConfig),
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
		authManager:  middleware.NewAuthManager(oldConfig),
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
		authManager: middleware.NewAuthManager(oldConfig),
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

// TestApplyAuthChanges_Case3_IncompleteCredentials_ReturnsWarning verifies that a
// Case-3 (auth enabled → still enabled) save with incomplete credentials tears down
// the listener AND returns a non-nil ExternalAccessWarning with the attempted port.
func TestApplyAuthChanges_Case3_IncompleteCredentials_ReturnsWarning(t *testing.T) {
	oldConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "user",
			Password: "pass",
		},
	}

	const expectedPort = 58343
	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				Web: config.WebConfig{
					ExternalPort: expectedPort,
				},
			},
		},
		authManager:  middleware.NewAuthManager(oldConfig),
		externalPort: expectedPort,
	}

	// Update with incomplete credentials (nil config = no password) — Case 3.
	warning := server.applyAuthChanges(true, true, nil)

	if warning == nil {
		t.Fatal("Expected non-nil ExternalAccessWarning for Case-3 incomplete-credentials teardown")
	}
	if warning.Port != expectedPort {
		t.Errorf("warning.Port = %d, want %d", warning.Port, expectedPort)
	}
	if warning.Reason == "" {
		t.Error("warning.Reason should not be empty")
	}
	if warning.Message == "" {
		t.Error("warning.Message should not be empty")
	}
}

// TestApplyAuthChanges_Case2_IntentionalDisable_ReturnsNil verifies that
// intentionally disabling auth (Case 2) returns nil — no user-facing warning.
func TestApplyAuthChanges_Case2_IntentionalDisable_ReturnsNil(t *testing.T) {
	oldConfig := &config.WebAuth{
		Simple: &config.SimpleAuth{
			Username: "user",
			Password: "pass",
		},
	}

	server := &Server{
		config:      Config{},
		authManager: middleware.NewAuthManager(oldConfig),
	}

	// Disable auth intentionally (Case 2) — must not warn.
	warning := server.applyAuthChanges(true, false, nil)

	if warning != nil {
		t.Errorf("Expected nil warning when auth is intentionally disabled, got: %+v", warning)
	}
}

// TestApplyAuthChanges_DisabledToDisabled_ReturnsNil verifies Case 4 (never
// enabled) also returns nil.
func TestApplyAuthChanges_DisabledToDisabled_ReturnsNil(t *testing.T) {
	server := &Server{
		config:       Config{},
		externalPort: -1,
	}

	warning := server.applyAuthChanges(false, false, nil)

	if warning != nil {
		t.Errorf("Expected nil warning for disabled-to-disabled, got: %+v", warning)
	}
}

func TestHandleSaveConfig_UIWithNativeNotifications(t *testing.T) {
	// Use temp dir to avoid writing to real settings file
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
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

func TestHandleGetConfig_ETag(t *testing.T) {
	server := &Server{
		config: Config{
			MittoConfig: &config.Config{
				ACPServers: []config.ACPServer{
					{Name: "test-server", Command: "test-cmd"},
				},
			},
		},
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}

	// First request — should get 200 with ETag
	req1 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w1 := httptest.NewRecorder()
	server.handleGetConfig(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("First request: status = %d, want %d", w1.Code, http.StatusOK)
	}

	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag header should be set")
	}

	// Second request with matching If-None-Match — should get 304
	req2 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	server.handleGetConfig(w2, req2)

	if w2.Code != http.StatusNotModified {
		t.Errorf("Second request: status = %d, want %d", w2.Code, http.StatusNotModified)
	}

	if w2.Body.Len() != 0 {
		t.Errorf("304 response should have empty body, got %d bytes", w2.Body.Len())
	}

	// Third request with stale ETag — should get 200
	req3 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req3.Header.Set("If-None-Match", `"stale-etag"`)
	w3 := httptest.NewRecorder()
	server.handleGetConfig(w3, req3)

	if w3.Code != http.StatusOK {
		t.Errorf("Third request: status = %d, want %d", w3.Code, http.StatusOK)
	}

	if w3.Body.Len() == 0 {
		t.Error("Full response should have non-empty body")
	}
}
