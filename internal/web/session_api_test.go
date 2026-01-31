package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

func TestHandleListSessions_EmptyStore(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var response struct {
		Sessions []interface{} `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(response.Sessions) != 0 {
		t.Errorf("Sessions count = %d, want 0", len(response.Sessions))
	}
}

func TestHandleListSessions_WithSessions(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-1",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Response is an array directly, not wrapped in an object
	var response []interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(response) != 1 {
		t.Errorf("Sessions count = %d, want 1", len(response))
	}
}

func TestHandleGetWorkspaces(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)
	sm.AddWorkspace(config.WorkspaceSettings{
		WorkingDir: "/workspace1",
		ACPServer:  "server1",
	})

	server := &Server{
		sessionManager: sm,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleGetWorkspaces(w, req)

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

func TestHandleRunningSessions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManager("", "", false, nil)

	server := &Server{
		sessionManager: sm,
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/running", nil)
	w := httptest.NewRecorder()

	server.handleRunningSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Response is a RunningSessionsResponse object
	var response RunningSessionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.TotalRunning != 0 {
		t.Errorf("TotalRunning = %d, want 0", response.TotalRunning)
	}

	if len(response.Sessions) != 0 {
		t.Errorf("Sessions count = %d, want 0", len(response.Sessions))
	}
}

func TestHandleSessions_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	// Test PUT method (not allowed)
	req := httptest.NewRequest(http.MethodPut, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleSessions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleWorkspaces_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	// Test PUT method (not allowed)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleWorkspaces(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleDeleteSession_NotFound(t *testing.T) {
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

	w := httptest.NewRecorder()

	server.handleDeleteSession(w, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleSessionDetail_MethodNotAllowed(t *testing.T) {
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

	// Test POST method (not allowed for session detail)
	// Use a valid session ID format: YYYYMMDD-HHMMSS-xxxxxxxx
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/20260131-120000-abcd1234", nil)
	w := httptest.NewRecorder()

	server.handleSessionDetail(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleGetSession_NotFound(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260131-120000-abcd1234", nil)
	w := httptest.NewRecorder()

	server.handleGetSession(w, req, "20260131-120000-abcd1234", false)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetSession_Found(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-get",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/test-session-get", nil)
	w := httptest.NewRecorder()

	server.handleGetSession(w, req, "test-session-get", false)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleUpdateSession_NotFound(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleUpdateSession(w, req, "nonexistent")

	// Should return 400 for invalid JSON body
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddWorkspace_InvalidJSON(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		config:         Config{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleAddWorkspace(w, req)

	// Should return 400 for invalid JSON body
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRemoveWorkspace_MissingDir(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		config:         Config{},
	}

	// Request without dir query parameter
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleRemoveWorkspace(w, req)

	// Should return 400 for missing dir parameter
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRemoveWorkspace_NotFound(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		config:         Config{},
	}

	// Request with non-existent workspace
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces?dir=/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleRemoveWorkspace(w, req)

	// Should return 404 for non-existent workspace
	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleWorkspacePrompts_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/prompts", nil)
	w := httptest.NewRecorder()

	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleWorkspacePrompts_MissingDir(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/prompts", nil)
	w := httptest.NewRecorder()

	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleWorkspacePrompts_Success(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/prompts?dir=/tmp", nil)
	w := httptest.NewRecorder()

	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleCreateSession_NoWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
		config:         Config{},
	}

	// Empty body with no workspace configured should return error
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleCreateSession(w, req)

	// Should return 400 because no workspace is configured
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSessions_GET(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetSession_Events(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-events",
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

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/test-session-events/events", nil)
	w := httptest.NewRecorder()

	server.handleGetSession(w, req, "test-session-events", true)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleDeleteSession_Success(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-delete",
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

	w := httptest.NewRecorder()

	server.handleDeleteSession(w, "test-session-delete")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleWorkspaces_GET(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)

	server := &Server{
		sessionManager: sm,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleWorkspaces(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleWorkspaces_POST_InvalidJSON(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)

	server := &Server{
		sessionManager: sm,
		config:         Config{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSessionDetail_GET(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
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

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260131-120000-abcd1234", nil)
	w := httptest.NewRecorder()

	server.handleSessionDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleSessionDetail_DELETE(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with valid ID format: YYYYMMDD-HHMMSS-XXXXXXXX (8 hex chars)
	meta := session.Metadata{
		SessionID:  "20260131-120000-de123456",
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

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/20260131-120000-de123456", nil)
	w := httptest.NewRecorder()

	server.handleSessionDetail(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleUpdateSession_Success(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "20260131-120000-up123456",
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

	body := strings.NewReader(`{"name": "Updated Name"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260131-120000-up123456", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSession(w, req, "20260131-120000-up123456")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListSessions_Pagination(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create multiple sessions
	for i := 0; i < 5; i++ {
		meta := session.Metadata{
			SessionID:  fmt.Sprintf("20260131-12000%d-abcd1234", i),
			ACPServer:  "test-server",
			WorkingDir: "/tmp",
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request with limit
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?limit=2", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleRunningSessions_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/running", nil)
	w := httptest.NewRecorder()

	server.handleRunningSessions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleListSessions_WorkspaceFilter(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create sessions in different workspaces
	meta1 := session.Metadata{
		SessionID:  "20260131-120001-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/workspace1",
	}
	if err := store.Create(meta1); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	meta2 := session.Metadata{
		SessionID:  "20260131-120002-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/workspace2",
	}
	if err := store.Create(meta2); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request with workspace filter
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?working_dir=/workspace1", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListSessions_Offset(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create multiple sessions
	for i := 0; i < 5; i++ {
		meta := session.Metadata{
			SessionID:  fmt.Sprintf("20260131-12001%d-abcd1234", i),
			ACPServer:  "test-server",
			WorkingDir: "/tmp",
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request with offset
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?offset=2", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListSessions_WithSearch(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with a name
	meta := session.Metadata{
		SessionID:  "20260131-120020-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session Name",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request with search query
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?search=Test", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleAddWorkspace_MissingWorkingDir(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)

	server := &Server{
		sessionManager: sm,
		config:         Config{},
	}

	body := strings.NewReader(`{"acp_server": "test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleAddWorkspace(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAddWorkspace_MissingACPServer(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)

	server := &Server{
		sessionManager: sm,
		config:         Config{},
	}

	body := strings.NewReader(`{"working_dir": "/tmp"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleAddWorkspace(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRemoveWorkspace_WithDir(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "server1"},
	})

	server := &Server{
		sessionManager: sm,
		config:         Config{},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces?dir=/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleRemoveWorkspace(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleCreateSession_InvalidWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := NewSessionManager("test-cmd", "test-server", false, nil)
	// No workspaces configured

	server := &Server{
		sessionManager: sm,
		store:          store,
		config:         Config{},
	}

	body := strings.NewReader(`{"working_dir": "/nonexistent"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateSession(w, req)

	// Should return 400 Bad Request for invalid workspace
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGetWorkspaces_WithWorkspaces(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "server1"},
		{WorkingDir: "/workspace2", ACPServer: "server2"},
	})

	server := &Server{
		sessionManager: sm,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleGetWorkspaces(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify response contains JSON
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestHandleGetWorkspaces_Empty(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)

	server := &Server{
		sessionManager: sm,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleGetWorkspaces(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListSessions_WithACPServer(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create sessions with different ACP servers
	meta1 := session.Metadata{
		SessionID:  "20260131-120040-abcd1234",
		ACPServer:  "server1",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta1); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	meta2 := session.Metadata{
		SessionID:  "20260131-120041-abcd1234",
		ACPServer:  "server2",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta2); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request with ACP server filter
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?acp_server=server1", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleSessionDetail_PATCH(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "20260131-120050-abcd1234",
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

	body := strings.NewReader(`{"name": "Updated Name"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260131-120050-abcd1234", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSessionDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListSessions_WithName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with a name
	meta := session.Metadata{
		SessionID:  "20260131-120060-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "My Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request with name filter
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?name=My+Test", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleUpdateSession_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "20260131-120070-abcd1234",
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

	// Invalid JSON body
	body := strings.NewReader(`{invalid json}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260131-120070-abcd1234", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSession(w, req, "20260131-120070-abcd1234")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleRunningSessions_WithSessions(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "20260131-120030-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := NewSessionManager("", "", false, nil)
	// Add a mock running session
	sm.mu.Lock()
	sm.sessions["20260131-120030-abcd1234"] = &BackgroundSession{
		persistedID: "20260131-120030-abcd1234",
		workingDir:  "/tmp",
	}
	sm.mu.Unlock()

	server := &Server{
		sessionManager: sm,
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/running", nil)
	w := httptest.NewRecorder()

	server.handleRunningSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleWorkspaces_DELETE(t *testing.T) {
	sm := NewSessionManager("test-cmd", "test-server", false, nil)

	server := &Server{
		sessionManager: sm,
		config:         Config{},
	}

	// DELETE without dir parameter should return 400
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces", nil)
	w := httptest.NewRecorder()

	server.handleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleListSessions_SortOrder(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create sessions with different timestamps
	for i := 0; i < 3; i++ {
		meta := session.Metadata{
			SessionID:  fmt.Sprintf("20260131-12003%d-abcd1234", i),
			ACPServer:  "test-server",
			WorkingDir: "/tmp",
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	// Request with sort order
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?sort=asc", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListSessions_InvalidLimit(t *testing.T) {
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

	// Request with invalid limit (should use default)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?limit=invalid", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleListSessions_InvalidOffset(t *testing.T) {
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

	// Request with invalid offset (should use default)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?offset=invalid", nil)
	w := httptest.NewRecorder()

	server.handleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}
