package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// newWSHandlers builds a Handlers facade for the workspace CRUD tests, wiring
// only the dependencies the workspace handlers use.
func newWSHandlers(sm *conversation.SessionManager, mc *config.Config) *Handlers {
	return New(Deps{
		SessionManager:       sm,
		MittoConfig:          mc,
		SyncConfigWorkspaces: func() {},
	})
}

func TestHandleGetWorkspaces(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	sm.AddWorkspace(config.WorkspaceSettings{
		WorkingDir: "/workspace1",
		ACPServer:  "server1",
	})

	h := newWSHandlers(sm, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var response struct {
		Workspaces []interface{} `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Should have at least 1 workspace
	if len(response.Workspaces) < 1 {
		t.Errorf("Workspaces count = %d, want >= 1", len(response.Workspaces))
	}
}

func TestHandleWorkspaces_MethodNotAllowed(t *testing.T) {
	h := newWSHandlers(conversation.NewSessionManager("", "", false, nil), nil)

	// Test PUT method (not allowed)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleAddWorkspace_InvalidJSON(t *testing.T) {
	h := newWSHandlers(conversation.NewSessionManager("", "", false, nil), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	// Should return 400 for invalid JSON body
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRemoveWorkspace_MissingDir(t *testing.T) {
	h := newWSHandlers(conversation.NewSessionManager("", "", false, nil), nil)

	// Request without dir query parameter
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	// Should return 400 for missing dir parameter
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRemoveWorkspace_NotFound(t *testing.T) {
	h := newWSHandlers(conversation.NewSessionManager("", "", false, nil), nil)

	// Request with non-existent workspace
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces?working_dir=/nonexistent", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	// Should return 404 for non-existent workspace
	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleWorkspaces_GET(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	h := newWSHandlers(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleWorkspaces_POST_InvalidJSON(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	h := newWSHandlers(sm, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddWorkspace_MissingWorkingDir(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	h := newWSHandlers(sm, nil)

	body := strings.NewReader(`{"acp_server": "test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddWorkspace_MissingACPServer(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	h := newWSHandlers(sm, nil)

	body := strings.NewReader(`{"working_dir": "/tmp"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRemoveWorkspace_WithDir(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "server1"},
	})
	h := newWSHandlers(sm, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces?working_dir=/nonexistent", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetWorkspaces_WithWorkspaces(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "server1"},
		{WorkingDir: "/workspace2", ACPServer: "server2"},
	})
	h := newWSHandlers(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify response contains JSON
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestHandleGetWorkspaces_FilterByWorkingDir(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "server1", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "server1"},
		{WorkingDir: "/workspace2", ACPServer: "server2"},
	})

	mc := &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "server1", Command: "cmd1"},
			{Name: "server2", Command: "cmd2"},
			{Name: "server3", Command: "cmd3"},
		},
	}
	h := newWSHandlers(sm, mc)

	getACPServerNames := func(url string) []string {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		h.HandleWorkspaces(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp struct {
			ACPServers []struct {
				Name string `json:"name"`
			} `json:"acp_servers"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to unmarshal response: %v", err)
		}
		names := make([]string, 0, len(resp.ACPServers))
		for _, s := range resp.ACPServers {
			names = append(names, s.Name)
		}
		return names
	}

	// With working_dir → only the server configured for that folder.
	if got := getACPServerNames("/api/workspaces?working_dir=/workspace1"); len(got) != 1 || got[0] != "server1" {
		t.Errorf("acp_servers for /workspace1 = %v, want [server1]", got)
	}
	if got := getACPServerNames("/api/workspaces?working_dir=/workspace2"); len(got) != 1 || got[0] != "server2" {
		t.Errorf("acp_servers for /workspace2 = %v, want [server2]", got)
	}

	// Folder with no configured workspace → empty list.
	if got := getACPServerNames("/api/workspaces?working_dir=/unknown"); len(got) != 0 {
		t.Errorf("acp_servers for /unknown = %v, want []", got)
	}

	// Without working_dir → all configured servers (backward compatible).
	if got := getACPServerNames("/api/workspaces"); len(got) != 3 {
		t.Errorf("acp_servers without working_dir = %v, want 3 servers", got)
	}
}

func TestHandleGetWorkspaces_Empty(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	h := newWSHandlers(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleWorkspaces_DELETE(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
	h := newWSHandlers(sm, nil)

	// DELETE without dir parameter should return 400
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
