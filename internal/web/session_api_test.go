package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web/handlers"
)

// handleListSessions is a test-only shim delegating to the migrated
// handlers.HandleListSessions. It lets the existing web-package list-sessions
// tests keep calling server.handleListSessions while the full test-suite
// migration to the handlers package is deferred to a later increment.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	handlers.New(handlers.Deps{Store: s.Store(), SessionManager: s.sessionManager}).HandleListSessions(w, r)
}

// handleCreateSession is a test-only shim delegating to the migrated
// handlers.HandleCreateSession, mirroring the handleListSessions shim above so
// the existing web-package create-session tests keep their call sites.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	handlers.New(handlers.Deps{
		Store:            s.Store(),
		SessionManager:   s.sessionManager,
		DefaultACPServer: s.config.ACPServer,
	}).HandleCreateSession(w, r)
}

// handleUpdateSession is a test-only shim delegating to the migrated
// handlers.HandleUpdateSession, wiring the broadcast closures from the server's
// nil-safe methods so the existing web-package update/archive tests keep their
// call sites.
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	handlers.New(handlers.Deps{
		Logger:                   s.logger,
		Store:                    s.Store(),
		SessionManager:           s.sessionManager,
		CallbackIndex:            s.callbackIndex,
		BroadcastSessionDeleted:  s.BroadcastSessionDeleted,
		BroadcastACPStopped:      s.BroadcastACPStopped,
		BroadcastACPStarted:      s.BroadcastACPStarted,
		BroadcastACPStartFailed:  s.BroadcastACPStartFailed,
		BroadcastSessionRenamed:  s.BroadcastSessionRenamed,
		BroadcastSessionPinned:   s.BroadcastSessionPinned,
		BroadcastSessionArchived: s.BroadcastSessionArchived,
	}).HandleUpdateSession(w, r, sessionID)
}

func TestHandleListSessions_EmptyStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

	if len(response) != 0 {
		t.Errorf("Sessions count = %d, want 0", len(response))
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

func TestHandleSessions_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}

	// Test PUT method (not allowed)
	req := httptest.NewRequest(http.MethodPut, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleSessions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

func TestHandleUpdateSession_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

// wireWorkspacePromptsTestDeps wires server.apiHandlers with the dependencies the
// migrated workspace-prompts GET handler needs, using method values bound to the
// test server so the extracted handler exercises the same logic as before.
func wireWorkspacePromptsTestDeps(s *Server) {
	s.apiHandlers = handlers.New(handlers.Deps{
		Logger:                             s.logger,
		MittoConfig:                        s.config.MittoConfig,
		PromptsCache:                       s.config.PromptsCache,
		SessionManager:                     s.sessionManager,
		MigrateWorkspacePrompts:            s.migrateWorkspacePrompts,
		LoadPromptsFromDirs:                s.loadPromptsFromDirs,
		BuildPromptEnabledContext:          s.buildPromptEnabledContext,
		ApplyWorkspaceNamespace:            s.applyWorkspaceNamespace,
		BuildWorkspacePromptEnabledContext: s.buildWorkspacePromptEnabledContext,
		FilterPromptsByEnabled:             s.filterPromptsByEnabled,
	})
}

func TestHandleWorkspacePrompts_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}

	// PUT is not supported (GET, POST, DELETE are)
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/prompts", nil)
	w := httptest.NewRecorder()

	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleWorkspacePrompts_MissingDir(t *testing.T) {
	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	wireWorkspacePromptsTestDeps(server)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/prompts", nil)
	w := httptest.NewRecorder()

	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleWorkspacePrompts_Success(t *testing.T) {
	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	wireWorkspacePromptsTestDeps(server)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/prompts?dir=/tmp", nil)
	w := httptest.NewRecorder()

	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleWorkspacePrompts_ConditionalRequest(t *testing.T) {
	// Create a temp directory with a .mittorc file
	tmpDir := t.TempDir()
	rcPath := tmpDir + "/.mittorc"

	// Create a .mittorc file with prompts
	rcContent := `prompts:
  - name: "Test Prompt"
    prompt: "Test prompt text"
`
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to create .mittorc: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	wireWorkspacePromptsTestDeps(server)

	// First request - should return prompts with Last-Modified header
	req1 := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir, nil)
	w1 := httptest.NewRecorder()
	server.handleWorkspacePrompts(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("First request: Status = %d, want %d", w1.Code, http.StatusOK)
	}

	lastModified := w1.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Errorf("Expected Last-Modified header to be set")
	}

	// Second request with If-Modified-Since - should return 304
	req2 := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir, nil)
	req2.Header.Set("If-Modified-Since", lastModified)
	w2 := httptest.NewRecorder()
	server.handleWorkspacePrompts(w2, req2)

	if w2.Code != http.StatusNotModified {
		t.Errorf("Conditional request: Status = %d, want %d", w2.Code, http.StatusNotModified)
	}
}

func TestHandleWorkspacePrompts_FileDeleted(t *testing.T) {
	// Create a temp directory with a .mittorc file
	tmpDir := t.TempDir()
	rcPath := tmpDir + "/.mittorc"

	// Create a .mittorc file with prompts
	rcContent := `prompts:
  - name: "Test Prompt"
    prompt: "Test prompt text"
`
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to create .mittorc: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	wireWorkspacePromptsTestDeps(server)

	// First request - should return prompts
	req1 := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir, nil)
	w1 := httptest.NewRecorder()
	server.handleWorkspacePrompts(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("First request: Status = %d, want %d", w1.Code, http.StatusOK)
	}

	// Delete the .mittorc file
	if err := os.Remove(rcPath); err != nil {
		t.Fatalf("Failed to delete .mittorc: %v", err)
	}

	// Request after file deletion - should return OK with empty prompts
	req2 := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir, nil)
	w2 := httptest.NewRecorder()
	server.handleWorkspacePrompts(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Request after deletion: Status = %d, want %d", w2.Code, http.StatusOK)
	}

	// Should not have Last-Modified header since file doesn't exist
	lastModified := w2.Header().Get("Last-Modified")
	if lastModified != "" {
		t.Errorf("Expected no Last-Modified header after file deletion, got %q", lastModified)
	}
}

func TestHandleWorkspacePrompts_DefaultMittoPromptsDir(t *testing.T) {
	// Create a temp directory to act as the workspace
	tmpDir := t.TempDir()

	// Create the default .mitto/prompts directory with a prompt file
	mittoPromptsDir := tmpDir + "/.mitto/prompts"
	if err := os.MkdirAll(mittoPromptsDir, 0755); err != nil {
		t.Fatalf("Failed to create .mitto/prompts dir: %v", err)
	}

	// Create a prompt file in the default workspace prompts directory
	promptContent := `name: "Default Workspace Prompt"
description: "A prompt from the default .mitto/prompts directory"
prompt: |
  This is the prompt content from the default workspace prompts directory.
`
	if err := os.WriteFile(mittoPromptsDir+"/test-prompt.prompt.yaml", []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to create prompt file: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	wireWorkspacePromptsTestDeps(server)

	// Request workspace prompts - should include the prompt from .mitto/prompts
	req := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir, nil)
	w := httptest.NewRecorder()
	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Parse response to verify the prompt is included
	var response struct {
		Prompts    []config.WebPrompt `json:"prompts"`
		WorkingDir string             `json:"working_dir"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should have one prompt
	if len(response.Prompts) != 1 {
		t.Errorf("Expected 1 prompt, got %d", len(response.Prompts))
	}

	if len(response.Prompts) > 0 && response.Prompts[0].Name != "Default Workspace Prompt" {
		t.Errorf("Expected prompt name 'Default Workspace Prompt', got %q", response.Prompts[0].Name)
	}
}

func TestHandleWorkspacePrompts_MittoDirOverriddenByPromptsDirs(t *testing.T) {
	// Create a temp directory to act as the workspace
	tmpDir := t.TempDir()

	// Create the default .mitto/prompts directory with a prompt file
	mittoPromptsDir := tmpDir + "/.mitto/prompts"
	if err := os.MkdirAll(mittoPromptsDir, 0755); err != nil {
		t.Fatalf("Failed to create .mitto/prompts dir: %v", err)
	}

	// Create a prompt file in the default workspace prompts directory
	defaultPromptContent := `name: "Shared Prompt"
description: "From default .mitto/prompts"
prompt: |
  Default version
`
	if err := os.WriteFile(mittoPromptsDir+"/shared.prompt.yaml", []byte(defaultPromptContent), 0644); err != nil {
		t.Fatalf("Failed to create default prompt file: %v", err)
	}

	// Create a custom prompts directory defined via prompts_dirs in .mittorc
	customPromptsDir := tmpDir + "/custom-prompts"
	if err := os.MkdirAll(customPromptsDir, 0755); err != nil {
		t.Fatalf("Failed to create custom prompts dir: %v", err)
	}

	// Create a prompt with the same name in the custom directory (should override)
	customPromptContent := `name: "Shared Prompt"
description: "From custom prompts_dirs"
prompt: |
  Custom version from prompts_dirs
`
	if err := os.WriteFile(customPromptsDir+"/shared.prompt.yaml", []byte(customPromptContent), 0644); err != nil {
		t.Fatalf("Failed to create custom prompt file: %v", err)
	}

	// Create .mittorc file with prompts_dirs pointing to custom directory
	rcContent := `prompts_dirs:
  - "custom-prompts"
`
	if err := os.WriteFile(tmpDir+"/.mittorc", []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to create .mittorc: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	wireWorkspacePromptsTestDeps(server)

	// Request workspace prompts
	req := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir, nil)
	w := httptest.NewRecorder()
	server.handleWorkspacePrompts(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Parse response
	var response struct {
		Prompts    []config.WebPrompt `json:"prompts"`
		WorkingDir string             `json:"working_dir"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should have one prompt (the custom one should override the default)
	if len(response.Prompts) != 1 {
		t.Errorf("Expected 1 prompt, got %d", len(response.Prompts))
	}

	// The custom version should win (prompts_dirs overrides default .mitto/prompts)
	if len(response.Prompts) > 0 && response.Prompts[0].Prompt != "Custom version from prompts_dirs\n" {
		t.Errorf("Expected custom prompt content, got %q", response.Prompts[0].Prompt)
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
	}
	server.apiHandlers = handlers.New(handlers.Deps{Store: store, SessionManager: server.sessionManager})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	server.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
	}
	server.apiHandlers = handlers.New(handlers.Deps{Store: store})

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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}
	server.apiHandlers = handlers.New(handlers.Deps{
		Store:                   store,
		SessionManager:          server.sessionManager,
		BroadcastSessionDeleted: server.BroadcastSessionDeleted,
	})

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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

func TestHandleCreateSession_InvalidWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Use conversation.NewSessionManagerWithOptions with empty workspaces list to ensure
	// no default workspace is configured. This simulates the case where
	// a user hasn't configured any workspaces yet.
	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{
		Workspaces:  []config.WorkspaceSettings{},
		AutoApprove: false,
		Logger:      nil,
	})

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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}
	server.apiHandlers = handlers.New(handlers.Deps{
		Store:                    store,
		SessionManager:           server.sessionManager,
		BroadcastSessionRenamed:  server.BroadcastSessionRenamed,
		BroadcastSessionPinned:   server.BroadcastSessionPinned,
		BroadcastSessionArchived: server.BroadcastSessionArchived,
		BroadcastACPStopped:      server.BroadcastACPStopped,
		BroadcastACPStarted:      server.BroadcastACPStarted,
		BroadcastACPStartFailed:  server.BroadcastACPStartFailed,
		BroadcastSessionDeleted:  server.BroadcastSessionDeleted,
	})

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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

// =============================================================================
// Periodic glance-fields tests for handleListSessions / SessionListResponse
// =============================================================================

func TestHandleListSessions_PeriodicGlanceFields_Schedule(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sid := "20260131-120090-abcd1234"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// Schedule periodic with explicit cap and duration.
	if err := store.Periodic(sid).Set(&session.PeriodicPrompt{
		Prompt:             "hello",
		Frequency:          session.Frequency{Value: 30, Unit: session.FrequencyMinutes},
		Enabled:            true,
		Trigger:            session.TriggerSchedule,
		MaxIterations:      10,
		IterationCount:     3,
		DelaySeconds:       0,
		MaxDurationSeconds: 3600,
	}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	server := &Server{sessionManager: conversation.NewSessionManager("", "", false, nil), store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	server.handleListSessions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var sessions []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one session")
	}
	s := sessions[0]

	if s["periodic_trigger"] != "schedule" {
		t.Errorf("periodic_trigger = %v, want %q", s["periodic_trigger"], "schedule")
	}
	if s["periodic_iteration_count"] != float64(3) {
		t.Errorf("periodic_iteration_count = %v, want 3", s["periodic_iteration_count"])
	}
	if s["periodic_max_iterations"] != float64(10) {
		t.Errorf("periodic_max_iterations = %v, want 10", s["periodic_max_iterations"])
	}
	if s["periodic_max_duration_seconds"] != float64(3600) {
		t.Errorf("periodic_max_duration_seconds = %v, want 3600", s["periodic_max_duration_seconds"])
	}
	// delay_seconds=0 is omitempty so it must be absent.
	if _, ok := s["periodic_delay_seconds"]; ok {
		t.Errorf("periodic_delay_seconds should be absent for schedule trigger with 0 delay")
	}
}

func TestHandleListSessions_PeriodicGlanceFields_OnCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sid := "20260131-120091-abcd1234"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// onCompletion with delay and max duration.
	if err := store.Periodic(sid).Set(&session.PeriodicPrompt{
		Prompt:             "run on idle",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		DelaySeconds:       60,
		MaxDurationSeconds: 7200,
	}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	server := &Server{sessionManager: conversation.NewSessionManager("", "", false, nil), store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	server.handleListSessions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var sessions []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one session")
	}
	s := sessions[0]

	if s["periodic_trigger"] != "onCompletion" {
		t.Errorf("periodic_trigger = %v, want %q", s["periodic_trigger"], "onCompletion")
	}
	if s["periodic_delay_seconds"] != float64(60) {
		t.Errorf("periodic_delay_seconds = %v, want 60", s["periodic_delay_seconds"])
	}
	if s["periodic_max_duration_seconds"] != float64(7200) {
		t.Errorf("periodic_max_duration_seconds = %v, want 7200", s["periodic_max_duration_seconds"])
	}
}

func TestHandleListSessions_PeriodicGlanceFields_EmptyTriggerReportsSchedule(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sid := "20260131-120092-abcd1234"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// Trigger="" is the zero-value default; EffectiveTrigger() must resolve it to "schedule".
	if err := store.Periodic(sid).Set(&session.PeriodicPrompt{
		Prompt:    "hello",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
		Trigger:   "",
	}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	server := &Server{sessionManager: conversation.NewSessionManager("", "", false, nil), store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	server.handleListSessions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var sessions []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one session")
	}
	if sessions[0]["periodic_trigger"] != "schedule" {
		t.Errorf("periodic_trigger = %v, want %q for empty Trigger field", sessions[0]["periodic_trigger"], "schedule")
	}
}

// =============================================================================
// Archive Lifecycle Tests
// =============================================================================

// TestHandleUpdateSession_ArchiveStopsACP tests that archiving a session
// stops the ACP connection.
func TestHandleUpdateSession_ArchiveStopsACP(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-archive",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create session manager with a mock running session
	sm := conversation.NewSessionManager("echo test", "test-server", true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	mockSession := conversation.NewTestBackgroundSessionWithCtx("test-session-archive", ctx, cancel)
	sm.AddSessionForTest(mockSession)

	server := &Server{
		sessionManager: sm,
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Archive the session
	archived := true
	body, _ := json.Marshal(SessionUpdateRequest{Archived: &archived})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/test-session-archive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSession(w, req, "test-session-archive")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Session should be removed from session manager (ACP stopped)
	if sm.GetSession("test-session-archive") != nil {
		t.Error("Session should be removed from session manager after archiving")
	}

	// Metadata should be updated
	updatedMeta, err := store.GetMetadata("test-session-archive")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if !updatedMeta.Archived {
		t.Error("Session should be marked as archived")
	}
	if updatedMeta.ArchivedAt.IsZero() {
		t.Error("ArchivedAt should be set")
	}
}

// TestHandleUpdateSession_ArchiveWaitsForPrompt tests that archiving waits
// for an in-progress prompt to complete.
func TestHandleUpdateSession_ArchiveWaitsForPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-archive-wait",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create session manager with a mock running session that is prompting
	sm := conversation.NewSessionManager("echo test", "test-server", true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	mockSession := conversation.NewTestBackgroundSessionPromptingWithCtx("test-session-archive-wait", true, ctx, cancel)
	sm.AddSessionForTest(mockSession)

	server := &Server{
		sessionManager: sm,
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Simulate prompt completion after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		mockSession.SimulatePromptComplete()
	}()

	// Archive the session
	archived := true
	body, _ := json.Marshal(SessionUpdateRequest{Archived: &archived})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/test-session-archive-wait", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	start := time.Now()
	server.handleUpdateSession(w, req, "test-session-archive-wait")
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Should have waited for prompt to complete (~100ms)
	if elapsed < 50*time.Millisecond {
		t.Errorf("Archive took %v, expected to wait for prompt completion (~100ms)", elapsed)
	}

	// Session should be removed from session manager
	if sm.GetSession("test-session-archive-wait") != nil {
		t.Error("Session should be removed from session manager after archiving")
	}
}

// TestHandleUpdateSession_UnarchiveDoesNotStartACP tests that unarchiving
// attempts to resume the ACP session (but doesn't fail if it can't).
func TestHandleUpdateSession_UnarchiveDoesNotStartACP(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create an archived session
	meta := session.Metadata{
		SessionID:  "test-session-unarchive",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Test Session",
		Archived:   true,
		ArchivedAt: time.Now(),
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create session manager (no running sessions)
	sm := conversation.NewSessionManager("echo test", "test-server", true, nil)
	sm.SetStore(store)

	server := &Server{
		sessionManager: sm,
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Unarchive the session
	archived := false
	body, _ := json.Marshal(SessionUpdateRequest{Archived: &archived})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/test-session-unarchive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSession(w, req, "test-session-unarchive")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Metadata should be updated
	updatedMeta, err := store.GetMetadata("test-session-unarchive")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if updatedMeta.Archived {
		t.Error("Session should not be marked as archived")
	}
	if !updatedMeta.ArchivedAt.IsZero() {
		t.Error("ArchivedAt should be cleared")
	}

	// Note: We don't check if ACP was started because ResumeSession will fail
	// without a valid ACP command. The important thing is that the request succeeds.
}

// =============================================================================
// Child Session Guard Tests
// =============================================================================

// TestHandleUpdateSession_ArchiveChildDeletesInstead tests that archiving a child session
// deletes it instead of archiving — children should never end up in the archived list.
func TestHandleUpdateSession_ArchiveChildDeletesInstead(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a parent session
	if err := store.Create(session.Metadata{
		SessionID:  "test-parent-session",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Parent Session",
	}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	// Create a child session
	if err := store.Create(session.Metadata{
		SessionID:       "test-child-session",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		Name:            "Child Session",
		ParentSessionID: "test-parent-session",
	}); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}
	server.apiHandlers = handlers.New(handlers.Deps{
		Store:                   store,
		SessionManager:          server.sessionManager,
		BroadcastSessionDeleted: server.BroadcastSessionDeleted,
	})

	// Try to archive the child — should be converted to delete (HTTP 204)
	archived := true
	body, _ := json.Marshal(SessionUpdateRequest{Archived: &archived})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/test-child-session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSession(w, req, "test-child-session")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d (child archive should be converted to delete)", w.Code, http.StatusNoContent)
	}

	// Verify child is deleted (not just archived)
	_, err = store.GetMetadata("test-child-session")
	if err != session.ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound after child archive-to-delete, got: %v", err)
	}
}

// TestHandleUpdateSession_ArchiveTopLevelAllowed tests that a top-level session
// CAN be archived normally.
func TestHandleUpdateSession_ArchiveTopLevelAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(session.Metadata{
		SessionID:  "test-toplevel-archive",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
		Name:       "Top-Level Session",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	archived := true
	body, _ := json.Marshal(SessionUpdateRequest{Archived: &archived})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/test-toplevel-archive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateSession(w, req, "test-toplevel-archive")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d (top-level archive should succeed)", w.Code, http.StatusOK)
	}

	updatedMeta, _ := store.GetMetadata("test-toplevel-archive")
	if !updatedMeta.Archived {
		t.Error("Top-level session should be archived")
	}
}

// makePrompt is a helper for constructing config.WebPrompt in tests.
func makePrompt(name string, opts ...func(*config.WebPrompt)) config.WebPrompt {
	p := config.WebPrompt{Name: name, Prompt: "Do something useful."}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

func withEnabledWhen(v string) func(*config.WebPrompt) {
	return func(p *config.WebPrompt) { p.EnabledWhen = v }
}

func TestFilterPromptsByEnabled(t *testing.T) {
	server := &Server{} // minimal server – no logger needed for these tests

	tests := []struct {
		name      string
		prompts   []config.WebPrompt
		ctx       *config.PromptEnabledContext
		wantNames []string
	}{
		// 1. Nil context returns all prompts unchanged
		{
			name: "nil context returns all prompts",
			prompts: []config.WebPrompt{
				makePrompt("a", withEnabledWhen(`ACP.MatchesServerType("augment")`)),
				makePrompt("b", withEnabledWhen("Session.IsChild")),
			},
			ctx:       nil,
			wantNames: []string{"a", "b"},
		},
		// 2. No conditions — always included
		{
			name:      "no conditions always included",
			prompts:   []config.WebPrompt{makePrompt("plain")},
			ctx:       &config.PromptEnabledContext{},
			wantNames: []string{"plain"},
		},
		// 3a. ACP.MatchesServerType type match — included
		{
			name:    "acp_matchesServerType type match included",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`ACP.MatchesServerType("augment")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
			},
			wantNames: []string{"p"},
		},
		// 3b. ACP.MatchesServerType display name does not match — excluded
		{
			name:    "acp_matchesServerType display name does not match excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`ACP.MatchesServerType("Auggie (Opus 4.6)")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
			},
			wantNames: nil,
		},
		// 4. ACP.MatchesServerType type match with different display name
		{
			name:    "acp_matchesServerType type match different display name",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`ACP.MatchesServerType("augment")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Sonnet 4.6)", Type: "augment"},
			},
			wantNames: []string{"p"},
		},
		// 5. ACP.MatchesServerType list of server types
		{
			name:    "acp_matchesServerType list match",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`ACP.MatchesServerType(["augment", "claude-code"])`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Claude Code (Opus 4.6)", Type: "claude-code"},
			},
			wantNames: []string{"p"},
		},
		// 6. ACP.MatchesServerType case insensitive (matches type)
		{
			name:    "acp_matchesServerType case insensitive",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`ACP.MatchesServerType("AUGMENT")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
			},
			wantNames: []string{"p"},
		},
		// 7. ACP.MatchesServerType fail-open when no ACP active
		{
			name:    "acp_matchesServerType fail-open no acp active",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`ACP.MatchesServerType("augment")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "", Type: ""},
			},
			wantNames: []string{"p"},
		},
		// 8. Tools.HasPattern satisfied
		{
			name:    "tools_hasPattern satisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`Tools.HasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_conversation_new", "other_tool"}},
			},
			wantNames: []string{"p"},
		},
		// 9. Tools.HasPattern unsatisfied
		{
			name:    "tools_hasPattern unsatisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`Tools.HasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"other_tool"}},
			},
			wantNames: nil,
		},
		// 10. Tools.HasAllPatterns all satisfied
		{
			name:    "tools_hasAllPatterns all satisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`Tools.HasAllPatterns(["mitto_*", "jira_*"])`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_foo", "jira_bar"}},
			},
			wantNames: []string{"p"},
		},
		// 11. Tools.HasAllPatterns partially satisfied — excluded
		{
			name:    "tools_hasAllPatterns partially satisfied excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`Tools.HasAllPatterns(["mitto_*", "jira_*"])`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_foo"}},
			},
			wantNames: nil,
		},
		// 12. Tools.HasPattern fetched-empty tools — excluded (fail-closed)
		{
			name:    "tools_hasPattern fetched-empty tools excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`Tools.HasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: nil},
			},
			wantNames: nil,
		},
		// 12b. Tools.HasPattern unknown tools — included (fail-open during warm-up)
		{
			name:    "tools_hasPattern unknown tools fail-open included",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`Tools.HasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: false, Names: nil},
			},
			wantNames: []string{"p"},
		},
		// 13. enabledWhen CEL true expression
		{
			name:    "enabledWhen CEL true expression included",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen("Session.IsChild"))},
			ctx: &config.PromptEnabledContext{
				Session: config.SessionContext{IsChild: true},
			},
			wantNames: []string{"p"},
		},
		// 14. enabledWhen CEL false expression
		{
			name:    "enabledWhen CEL false expression excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen("Session.IsChild"))},
			ctx: &config.PromptEnabledContext{
				Session: config.SessionContext{IsChild: false},
			},
			wantNames: nil,
		},
		// 15. enabledWhen CEL complex expression
		{
			name: "enabledWhen CEL complex expression included",
			prompts: []config.WebPrompt{
				makePrompt("p", withEnabledWhen(`"reasoning" in ACP.Tags`)),
			},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Tags: []string{"reasoning", "fast"}},
			},
			wantNames: []string{"p"},
		},
		// 16. enabledWhen CEL invalid expression (fail-open)
		{
			name:      "enabledWhen CEL invalid expression fail-open",
			prompts:   []config.WebPrompt{makePrompt("p", withEnabledWhen("this is not valid CEL !!"))},
			ctx:       &config.PromptEnabledContext{},
			wantNames: []string{"p"},
		},
		// 17. Combined: ACP.MatchesServerType + Tools.HasPattern + CEL all pass
		{
			name: "combined acp_matchesServerType and tools_hasPattern and CEL all pass",
			prompts: []config.WebPrompt{
				makePrompt("p",
					withEnabledWhen(`ACP.MatchesServerType("augment") && Tools.HasPattern("mitto_*") && !Session.IsChild`),
				),
			},
			ctx: &config.PromptEnabledContext{
				ACP:     config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
				Tools:   config.ToolsContext{Available: true, Names: []string{"mitto_conversation_new"}},
				Session: config.SessionContext{IsChild: false},
			},
			wantNames: []string{"p"},
		},
		// 18. Combined: ACP.MatchesServerType passes, Tools.HasPattern fails
		{
			name: "combined acp_matchesServerType passes tools_hasPattern fails excluded",
			prompts: []config.WebPrompt{
				makePrompt("p",
					withEnabledWhen(`ACP.MatchesServerType("augment") && Tools.HasPattern("jira_*")`),
				),
			},
			ctx: &config.PromptEnabledContext{
				ACP:   config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_foo"}},
			},
			wantNames: nil,
		},
		// 19. Combined: ACP.MatchesServerType fails — whole expression excluded
		{
			name: "combined acp_matchesServerType fails excluded",
			prompts: []config.WebPrompt{
				makePrompt("p",
					withEnabledWhen(`ACP.MatchesServerType("claude") && Tools.HasPattern("mitto_*") && true`),
				),
			},
			ctx: &config.PromptEnabledContext{
				ACP:   config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_foo"}},
			},
			wantNames: nil,
		},
		// 20. Mixed prompts — some pass, some fail, order preserved
		{
			name: "mixed prompts correct order",
			prompts: []config.WebPrompt{
				makePrompt("included-1"),
				makePrompt("excluded-acp", withEnabledWhen(`ACP.MatchesServerType("claude")`)),
				makePrompt("included-2", withEnabledWhen("!Session.IsChild")),
				makePrompt("excluded-mcp", withEnabledWhen(`Tools.HasPattern("jira_*")`)),
				makePrompt("included-3", withEnabledWhen(`ACP.MatchesServerType("augment")`)),
			},
			ctx: &config.PromptEnabledContext{
				ACP:     config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
				Tools:   config.ToolsContext{Available: true, Names: []string{"mitto_foo"}},
				Session: config.SessionContext{IsChild: false},
			},
			wantNames: []string{"included-1", "included-2", "included-3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := server.filterPromptsByEnabled(tc.prompts, tc.ctx)

			// Extract names for easy comparison
			var gotNames []string
			for _, p := range got {
				gotNames = append(gotNames, p.Name)
			}

			if len(gotNames) != len(tc.wantNames) {
				t.Errorf("got %v, want %v", gotNames, tc.wantNames)
				return
			}
			for i := range gotNames {
				if gotNames[i] != tc.wantNames[i] {
					t.Errorf("got[%d] = %q, want %q (full: got %v want %v)", i, gotNames[i], tc.wantNames[i], gotNames, tc.wantNames)
				}
			}
		})
	}
}

// TestHandleUpdateSession_BeadsIssue verifies that PATCH /api/sessions/{id} with
// beads_issue persists the value and GET returns it.
func TestHandleUpdateSession_BeadsIssue(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := session.Metadata{
		SessionID:  "20260131-120099-beads001",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// PATCH: set beads_issue
	body := strings.NewReader(`{"beads_issue": "mitto-42"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260131-120099-beads001", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.handleUpdateSession(w, req, "20260131-120099-beads001")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// GET: verify beads_issue is returned in metadata
	updated, err := store.GetMetadata("20260131-120099-beads001")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if updated.BeadsIssue != "mitto-42" {
		t.Errorf("BeadsIssue = %q, want %q", updated.BeadsIssue, "mitto-42")
	}

	// PATCH: clear beads_issue with empty string
	body2 := strings.NewReader(`{"beads_issue": ""}`)
	req2 := httptest.NewRequest(http.MethodPatch, "/api/sessions/20260131-120099-beads001", body2)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	server.handleUpdateSession(w2, req2, "20260131-120099-beads001")
	if w2.Code != http.StatusOK {
		t.Fatalf("PATCH clear status = %d, want %d; body: %s", w2.Code, http.StatusOK, w2.Body.String())
	}

	cleared, err := store.GetMetadata("20260131-120099-beads001")
	if err != nil {
		t.Fatalf("GetMetadata after clear failed: %v", err)
	}
	if cleared.BeadsIssue != "" {
		t.Errorf("BeadsIssue after clear = %q, want empty", cleared.BeadsIssue)
	}
}

func TestResolveOwningWorkspace(t *testing.T) {
	// Use synthetic absolute paths so the test stays pure and fast: non-existent
	// paths skip symlink resolution and the git probe fails immediately.
	ws := func(uuid, dir string) config.WorkspaceSettings {
		return config.WorkspaceSettings{UUID: uuid, WorkingDir: dir}
	}

	root := filepath.Join(string(os.PathSeparator), "ws", "repo")
	sub := filepath.Join(root, "sub")
	deep := filepath.Join(root, "a", "b")
	sibling := filepath.Join(string(os.PathSeparator), "ws", "repobc")

	workspaces := []config.WorkspaceSettings{
		ws("uuid-root", root),
		ws("uuid-other", filepath.Join(string(os.PathSeparator), "other")),
	}

	tests := []struct {
		name       string
		reqDir     string
		workspaces []config.WorkspaceSettings
		wantUUID   string // "" means expect nil
	}{
		{"exact match", root, workspaces, "uuid-root"},
		{"strict subdir match", sub, workspaces, "uuid-root"},
		{"sibling dir not owned", sibling, workspaces, ""},
		{"unrelated dir", filepath.Join(string(os.PathSeparator), "elsewhere", "x"), workspaces, ""},
		{"empty reqDir", "", workspaces, ""},
		{"empty workspaces", root, nil, ""},
		{
			name:   "deepest workspace wins",
			reqDir: deep,
			workspaces: []config.WorkspaceSettings{
				ws("uuid-root", root),
				ws("uuid-mid", filepath.Join(root, "a")),
			},
			wantUUID: "uuid-mid",
		},
		{
			name:   "skip workspace with empty uuid",
			reqDir: sub,
			workspaces: []config.WorkspaceSettings{
				ws("", root),
			},
			wantUUID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOwningWorkspace(tt.reqDir, tt.workspaces)
			if tt.wantUUID == "" {
				if got != nil {
					t.Fatalf("expected nil, got workspace UUID %q (dir %q)", got.UUID, got.WorkingDir)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected workspace UUID %q, got nil", tt.wantUUID)
			}
			if got.UUID != tt.wantUUID {
				t.Fatalf("expected workspace UUID %q, got %q", tt.wantUUID, got.UUID)
			}
		})
	}
}

// TestFilterPromptsByEnabled_ItemGating validates that the unified enabledWhen
// filter evaluates item.*-gated prompts against the per-row item context (set on
// the PromptEnabledContext.Item) while still evaluating non-item prompts against
// the rest of the context. This replaces the old per-row-only item filter: with
// mitto-gns the beads menus run the full filterPromptsByEnabled so every gate
// (item.*, Session.IsChild, permissions, CommandExists, …) is applied at once.
func TestFilterPromptsByEnabled_ItemGating(t *testing.T) {
	s := &Server{}

	makePrompt := func(name, enabledWhen string) config.WebPrompt {
		p := config.WebPrompt{Name: name, EnabledWhen: enabledWhen}
		tr := true
		p.Enabled = &tr
		return p
	}

	itemPrompt := makePrompt("start-work", `Item.Status != "closed"`)
	nonItemPrompt := makePrompt("triage", `Session.IsChild == false`)
	noExprPrompt := makePrompt("review", "")

	closedCtx := &config.PromptEnabledContext{
		Item: config.ItemContext{Status: "closed", Kind: "beadsIssue"},
	}
	openCtx := &config.PromptEnabledContext{
		Item: config.ItemContext{Status: "open", Kind: "beadsIssue"},
	}

	tests := []struct {
		name      string
		prompts   []config.WebPrompt
		ctx       *config.PromptEnabledContext
		wantNames []string
	}{
		{
			name:      "item-gated prompt dropped for closed issue",
			prompts:   []config.WebPrompt{itemPrompt, noExprPrompt},
			ctx:       closedCtx,
			wantNames: []string{"review"},
		},
		{
			name:      "item-gated prompt kept for open issue",
			prompts:   []config.WebPrompt{itemPrompt, noExprPrompt},
			ctx:       openCtx,
			wantNames: []string{"start-work", "review"},
		},
		{
			name:      "non-item enabledWhen evaluated and kept when true",
			prompts:   []config.WebPrompt{nonItemPrompt},
			ctx:       closedCtx,
			wantNames: []string{"triage"},
		},
		{
			name:      "all prompt types together for closed issue",
			prompts:   []config.WebPrompt{itemPrompt, nonItemPrompt, noExprPrompt},
			ctx:       closedCtx,
			wantNames: []string{"triage", "review"},
		},
		{
			name:      "all prompt types together for open issue",
			prompts:   []config.WebPrompt{itemPrompt, nonItemPrompt, noExprPrompt},
			ctx:       openCtx,
			wantNames: []string{"start-work", "triage", "review"},
		},
		{
			name:      "nil ctx returns all prompts",
			prompts:   []config.WebPrompt{itemPrompt, nonItemPrompt, noExprPrompt},
			ctx:       nil,
			wantNames: []string{"start-work", "triage", "review"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.filterPromptsByEnabled(tt.prompts, tt.ctx)
			if len(got) != len(tt.wantNames) {
				var gotNames []string
				for _, p := range got {
					gotNames = append(gotNames, p.Name)
				}
				t.Fatalf("filterPromptsByEnabled returned %v, want %v", gotNames, tt.wantNames)
			}
			for i, p := range got {
				if p.Name != tt.wantNames[i] {
					t.Errorf("prompt[%d] = %q, want %q", i, p.Name, tt.wantNames[i])
				}
			}
		})
	}
}

// TestHandleWorkspacePrompts_EnabledContextWorkspaceFallback verifies the
// session-less fallback (mitto-gns, approach B): when the caller passes
// enabled_context=workspace and there is no session_id, the handler builds a
// workspace-level context and runs the full enabledWhen filter. Without that
// param (other callers), prompts are returned unfiltered so existing behavior is
// preserved.
func TestHandleWorkspacePrompts_EnabledContextWorkspaceFallback(t *testing.T) {
	tmpDir := t.TempDir()
	rcContent := `prompts:
  - name: "Always Visible"
    prompt: "x"
  - name: "Always Hidden"
    prompt: "y"
    enabledWhen: "false"
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".mittorc"), []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to create .mittorc: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}
	wireWorkspacePromptsTestDeps(server)

	decode := func(t *testing.T, body []byte) ([]string, bool) {
		t.Helper()
		var resp struct {
			Prompts          []config.WebPrompt `json:"prompts"`
			EnabledEvaluated bool               `json:"enabled_evaluated"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, string(body))
		}
		var names []string
		for _, p := range resp.Prompts {
			names = append(names, p.Name)
		}
		return names, resp.EnabledEvaluated
	}

	hasName := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	// Without enabled_context: no filtering, both prompts returned.
	req1 := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir, nil)
	w1 := httptest.NewRecorder()
	server.handleWorkspacePrompts(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("unfiltered request: status = %d, want %d", w1.Code, http.StatusOK)
	}
	names1, eval1 := decode(t, w1.Body.Bytes())
	if eval1 {
		t.Errorf("unfiltered request: enabled_evaluated = true, want false")
	}
	if !hasName(names1, "Always Visible") || !hasName(names1, "Always Hidden") {
		t.Errorf("unfiltered request: got %v, want both prompts", names1)
	}

	// With enabled_context=workspace: full filter applied, "false"-gated prompt hidden.
	req2 := httptest.NewRequest(http.MethodGet, "/api/workspace-prompts?dir="+tmpDir+"&enabled_context=workspace", nil)
	w2 := httptest.NewRecorder()
	server.handleWorkspacePrompts(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("filtered request: status = %d, want %d", w2.Code, http.StatusOK)
	}
	names2, eval2 := decode(t, w2.Body.Bytes())
	if !eval2 {
		t.Errorf("filtered request: enabled_evaluated = false, want true")
	}
	if !hasName(names2, "Always Visible") {
		t.Errorf("filtered request: missing Always Visible, got %v", names2)
	}
	if hasName(names2, "Always Hidden") {
		t.Errorf("filtered request: Always Hidden should be filtered out, got %v", names2)
	}
}

// TestHandleWorkspacePrompts_DirGatesUseDirParamNotSession is a regression test
// for the beads-menu bug where dir-based enabledWhen gates (DirExists/FileExists)
// were evaluated against the active session's working dir instead of the dir
// query param. The frontend always appends &session_id=<activeConversation>, so
// when that conversation lived in a folder without ".beads" the gate
// DirExists(".beads") evaluated false and every beads prompt was filtered out —
// leaving the per-issue context menu empty. The fix makes the requested dir
// authoritative for the workspace namespace (applyWorkspaceNamespace), so the
// gate evaluates against the dir param even with a session_id present.
func TestHandleWorkspacePrompts_DirGatesUseDirParamNotSession(t *testing.T) {
	// beadsDir is the workspace the prompts belong to (has .beads); otherDir is
	// the active session's working dir (no .beads).
	beadsDir := t.TempDir()
	otherDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(beadsDir, ".beads"), 0755); err != nil {
		t.Fatalf("create .beads: %v", err)
	}
	rcContent := `prompts:
  - name: "Beads Gated"
    prompt: "x"
    enabledWhen: 'DirExists(".beads")'
  - name: "Ungated"
    prompt: "y"
`
	if err := os.WriteFile(filepath.Join(beadsDir, ".mittorc"), []byte(rcContent), 0644); err != nil {
		t.Fatalf("write .mittorc: %v", err)
	}

	storeDir := t.TempDir()
	store, err := session.NewStore(storeDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Active conversation lives in otherDir, which has no .beads.
	if err := store.Create(session.Metadata{
		SessionID:  "active-session",
		ACPServer:  "test-server",
		WorkingDir: otherDir,
		Name:       "Active elsewhere",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
	}
	wireWorkspacePromptsTestDeps(server)

	decode := func(t *testing.T, body []byte) []string {
		t.Helper()
		var resp struct {
			Prompts []config.WebPrompt `json:"prompts"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, string(body))
		}
		var names []string
		for _, p := range resp.Prompts {
			names = append(names, p.Name)
		}
		return names
	}
	hasName := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	// dir=beadsDir (has .beads) but session_id points at otherDir (no .beads).
	// The dir-gated prompt must still be returned because dir is authoritative.
	url := "/api/workspace-prompts?dir=" + beadsDir + "&enabled_context=workspace&session_id=active-session"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	server.handleWorkspacePrompts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	names := decode(t, w.Body.Bytes())
	if !hasName(names, "Beads Gated") {
		t.Errorf("dir-gated prompt was filtered out: DirExists(\".beads\") evaluated against the session's folder instead of the dir param; got %v", names)
	}
	if !hasName(names, "Ungated") {
		t.Errorf("ungated prompt missing, got %v", names)
	}
}


// TestSessionSubresourceRoutingPrecedence proves that specific patterns like
// /api/sessions/{id}/settings win over the /api/sessions/ subtree fallback,
// and that unmigrated sub-paths (events, periodic, …) still fall through to the
// subtree handler.
func TestSessionSubresourceRoutingPrecedence(t *testing.T) {
	mux := http.NewServeMux()
	hit := ""
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) { hit = "detail" })
	for _, sub := range []string{"user-data", "callback", "settings", "prune", "changes"} {
		s := sub
		mux.HandleFunc("/api/sessions/{id}/"+s, func(w http.ResponseWriter, r *http.Request) { hit = s + ":" + r.PathValue("id") })
	}
	// Sub-resources with optional trailing sub-ID (same handler registered for both).
	mux.HandleFunc("/api/sessions/{id}/images", func(w http.ResponseWriter, r *http.Request) {
		hit = "images:" + r.PathValue("id") + ":" + r.PathValue("imageId")
	})
	mux.HandleFunc("/api/sessions/{id}/images/{imageId}", func(w http.ResponseWriter, r *http.Request) {
		hit = "images:" + r.PathValue("id") + ":" + r.PathValue("imageId")
	})
	mux.HandleFunc("/api/sessions/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		hit = "files:" + r.PathValue("id") + ":" + r.PathValue("fileId")
	})
	mux.HandleFunc("/api/sessions/{id}/files/{fileId}", func(w http.ResponseWriter, r *http.Request) {
		hit = "files:" + r.PathValue("id") + ":" + r.PathValue("fileId")
	})
	mux.HandleFunc("/api/sessions/{id}/queue", func(w http.ResponseWriter, r *http.Request) {
		hit = "queue:" + r.PathValue("id") + ":" + r.PathValue("msgId")
	})
	mux.HandleFunc("/api/sessions/{id}/queue/{msgId}", func(w http.ResponseWriter, r *http.Request) {
		hit = "queue:" + r.PathValue("id") + ":" + r.PathValue("msgId")
	})
	mux.HandleFunc("/api/sessions/{id}/periodic", func(w http.ResponseWriter, r *http.Request) {
		hit = "periodic:" + r.PathValue("id") + ":" + r.PathValue("subPath")
	})
	mux.HandleFunc("/api/sessions/{id}/periodic/{subPath}", func(w http.ResponseWriter, r *http.Request) {
		hit = "periodic:" + r.PathValue("id") + ":" + r.PathValue("subPath")
	})

	cases := map[string]string{
		// Leaf sub-resources from increment 3 — still routed correctly.
		"/api/sessions/abc123/settings":  "settings:abc123",
		"/api/sessions/abc123/prune":     "prune:abc123",
		"/api/sessions/abc123/changes":   "changes:abc123",
		"/api/sessions/abc123/user-data": "user-data:abc123",
		"/api/sessions/abc123/callback":  "callback:abc123",
		// Sub-resources with optional trailing sub-ID (increment 4).
		"/api/sessions/abc123/images":           "images:abc123:",
		"/api/sessions/abc123/images/img7":       "images:abc123:img7",
		"/api/sessions/abc123/files":             "files:abc123:",
		"/api/sessions/abc123/files/f9":          "files:abc123:f9",
		"/api/sessions/abc123/queue":             "queue:abc123:",
		"/api/sessions/abc123/queue/m42":         "queue:abc123:m42",
		"/api/sessions/abc123/periodic":          "periodic:abc123:",
		"/api/sessions/abc123/periodic/run-now":  "periodic:abc123:run-now",
		// Unmigrated paths still fall through to detail.
		"/api/sessions/abc123":        "detail",
		"/api/sessions/abc123/events": "detail",
	}
	for path, want := range cases {
		hit = ""
		req := httptest.NewRequest(http.MethodGet, path, nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)
		if hit != want {
			t.Errorf("path %s routed to %q, want %q", path, hit, want)
		}
	}
}
