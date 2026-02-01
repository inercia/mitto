package web

import (
	"net/http"
	"net/http/httptest"
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
