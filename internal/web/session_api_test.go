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
)

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

func TestHandleGetWorkspaces(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
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

	sm := conversation.NewSessionManager("", "", false, nil)

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

func TestHandleWorkspaces_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

func TestHandleGetSession_NotFound(t *testing.T) {
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

func TestHandleAddWorkspace_InvalidJSON(t *testing.T) {
	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	w := httptest.NewRecorder()

	server.handleDeleteSession(w, "test-session-delete")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

// TestHandleDeleteSession_ClearsParentReferences verifies that deleting a parent session
// via the API clears the ParentSessionID field in all child sessions.
func TestHandleDeleteSession_ClearsParentReferences(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a parent session
	parentMeta := session.Metadata{
		SessionID:  "parent-api-test",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Parent Session",
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	// Create child sessions
	child1Meta := session.Metadata{
		SessionID:       "child-api-1",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child 1",
		ParentSessionID: "parent-api-test",
	}
	if err := store.Create(child1Meta); err != nil {
		t.Fatalf("Create child1 failed: %v", err)
	}

	child2Meta := session.Metadata{
		SessionID:       "child-api-2",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child 2",
		ParentSessionID: "parent-api-test",
	}
	if err := store.Create(child2Meta); err != nil {
		t.Fatalf("Create child2 failed: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Delete the parent session via API
	w := httptest.NewRecorder()
	server.handleDeleteSession(w, "parent-api-test")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify parent is deleted
	if store.Exists("parent-api-test") {
		t.Error("Parent session still exists after deletion")
	}

	// Verify child sessions are cascade-deleted along with the parent
	if store.Exists("child-api-1") {
		t.Error("Child 1 still exists after parent deletion — expected cascade delete")
	}
	if store.Exists("child-api-2") {
		t.Error("Child 2 still exists after parent deletion — expected cascade delete")
	}
}

func TestHandleWorkspaces_GET(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)

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
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)

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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

func TestHandleRunningSessions_MethodNotAllowed(t *testing.T) {
	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
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

func TestHandleAddWorkspace_MissingWorkingDir(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)

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
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)

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
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
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

func TestHandleGetWorkspaces_WithWorkspaces(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)
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

func TestHandleGetWorkspaces_FilterByWorkingDir(t *testing.T) {
	sm := conversation.NewSessionManager("test-cmd", "server1", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1", ACPServer: "server1"},
		{WorkingDir: "/workspace2", ACPServer: "server2"},
	})

	server := &Server{
		sessionManager: sm,
		config: Config{
			MittoConfig: &config.Config{
				ACPServers: []config.ACPServer{
					{Name: "server1", Command: "cmd1"},
					{Name: "server2", Command: "cmd2"},
					{Name: "server3", Command: "cmd3"},
				},
			},
		},
	}

	getACPServerNames := func(url string) []string {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		server.handleGetWorkspaces(w, req)
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

	sm := conversation.NewSessionManager("", "", false, nil)
	// Add a mock running session
	sm.AddSessionForTest(conversation.NewMinimalBackgroundSession("20260131-120030-abcd1234", "/tmp", ""))

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
	sm := conversation.NewSessionManager("test-cmd", "test-server", false, nil)

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

// =============================================================================
// Periodic Guard Tests
// =============================================================================

// TestHandleSessionPeriodic_ChildRejected tests that setting periodic on a child session is rejected.
func TestHandleSessionPeriodic_ChildRejected(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(session.Metadata{
		SessionID:  "test-parent-periodic",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	if err := store.Create(session.Metadata{
		SessionID:       "test-child-periodic",
		ACPServer:       "test-server",
		WorkingDir:      tmpDir,
		ParentSessionID: "test-parent-periodic",
	}); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	server := &Server{store: store}

	// PUT periodic on child — should be rejected
	body, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "check updates",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-child-periodic/periodic", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSessionPeriodic(w, req, "test-child-periodic", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("PUT periodic on child: Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// GET should still work (not rejected as 400)
	req2 := httptest.NewRequest(http.MethodGet, "/api/sessions/test-child-periodic/periodic", nil)
	w2 := httptest.NewRecorder()

	server.handleSessionPeriodic(w2, req2, "test-child-periodic", "")

	if w2.Code == http.StatusBadRequest {
		t.Error("GET periodic on child should NOT be rejected with 400")
	}
}

// TestHandleSessionPeriodic_TopLevelAllowed tests that setting periodic on a top-level session works.
func TestHandleSessionPeriodic_TopLevelAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(session.Metadata{
		SessionID:  "test-toplevel-periodic",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		store:         store,
		eventsManager: NewGlobalEventsManager(),
	}

	body, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "check updates",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-toplevel-periodic/periodic", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSessionPeriodic(w, req, "test-toplevel-periodic", "")

	if w.Code != http.StatusOK {
		t.Errorf("PUT periodic on top-level: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// putPeriodicForTest is a helper that PUTs a periodic config via the REST handler and
// returns the decoded response. It fails the test on a non-200 status.
func putPeriodicForTest(t *testing.T, server *Server, sid string, body PeriodicPromptRequest) session.PeriodicPrompt {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sid+"/periodic", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.handleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var got session.PeriodicPrompt
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	return got
}

// TestHandleSessionPeriodic_OnCompletionRoundTrip verifies that the on-completion trigger,
// completion delay, and max-duration fields round-trip through the PUT handler. A frequency
// is not required for the onCompletion trigger.
func TestHandleSessionPeriodic_OnCompletionRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const sid = "test-oncompletion-roundtrip"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{store: store, eventsManager: NewGlobalEventsManager()}

	got := putPeriodicForTest(t, server, sid, PeriodicPromptRequest{
		Prompt:             "keep going",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		DelaySeconds:       30,
		MaxDurationSeconds: 3600,
	})

	if got.Trigger != session.TriggerOnCompletion {
		t.Errorf("Trigger = %q, want %q", got.Trigger, session.TriggerOnCompletion)
	}
	if got.DelaySeconds != 30 {
		t.Errorf("DelaySeconds = %d, want 30", got.DelaySeconds)
	}
	if got.MaxDurationSeconds != 3600 {
		t.Errorf("MaxDurationSeconds = %d, want 3600", got.MaxDurationSeconds)
	}
}

// TestHandleSessionPeriodic_OnCompletionDelayClampedOnPut verifies that a delay below the
// global floor is clamped up to the floor on write (PUT). With no periodic runner configured,
// the floor is the package default.
func TestHandleSessionPeriodic_OnCompletionDelayClampedOnPut(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const sid = "test-oncompletion-clamp-put"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{store: store, eventsManager: NewGlobalEventsManager()}

	got := putPeriodicForTest(t, server, sid, PeriodicPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 1, // below the default floor (5)
	})

	if got.DelaySeconds != server.periodicDelayFloor() {
		t.Errorf("DelaySeconds = %d, want clamped to floor %d", got.DelaySeconds, server.periodicDelayFloor())
	}
}

// TestHandleSessionPeriodic_PatchPartialPreservesOnCompletionFields verifies that a partial
// PATCH updating only max_duration_seconds does not clobber the trigger or delay.
func TestHandleSessionPeriodic_PatchPartialPreservesOnCompletionFields(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const sid = "test-oncompletion-patch"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{store: store, eventsManager: NewGlobalEventsManager()}

	// Seed an onCompletion config with a delay and no duration cap.
	putPeriodicForTest(t, server, sid, PeriodicPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 30,
	})

	// PATCH only max_duration_seconds.
	maxDur := 7200
	patchBody, _ := json.Marshal(PeriodicPromptPatchRequest{MaxDurationSeconds: &maxDur})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/periodic", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.handleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := store.Periodic(sid).Get()
	if err != nil {
		t.Fatalf("Get periodic after PATCH: %v", err)
	}
	if stored.Trigger != session.TriggerOnCompletion {
		t.Errorf("Trigger after PATCH = %q, want %q (must not be clobbered)", stored.Trigger, session.TriggerOnCompletion)
	}
	if stored.DelaySeconds != 30 {
		t.Errorf("DelaySeconds after PATCH = %d, want 30 (must not be clobbered)", stored.DelaySeconds)
	}
	if stored.MaxDurationSeconds != 7200 {
		t.Errorf("MaxDurationSeconds after PATCH = %d, want 7200", stored.MaxDurationSeconds)
	}
}

// TestHandleSessionPeriodic_PatchResetCounters verifies that PATCHing with
// reset_counters=true (used when restoring a loop that hit its cap) re-enables the
// loop and resets IterationCount=0 and FirstRunAt=nil (elapsed time = 0).
func TestHandleSessionPeriodic_PatchResetCounters(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const sid = "test-reset-counters-patch"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{store: store, eventsManager: NewGlobalEventsManager()}

	// Seed an onCompletion config with a duration cap.
	putPeriodicForTest(t, server, sid, PeriodicPromptRequest{
		Prompt:             "keep going",
		Enabled:            true,
		Trigger:            session.TriggerOnCompletion,
		DelaySeconds:       30,
		MaxDurationSeconds: 60,
	})

	// Simulate two completed runs, then auto-stop on the duration cap.
	ps := store.Periodic(sid)
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent: %v", err)
	}
	if err := ps.RecordSent(); err != nil {
		t.Fatalf("RecordSent: %v", err)
	}
	if err := ps.MarkStopped(session.StoppedReasonMaxDuration); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}

	// PATCH restore with reset_counters=true.
	enabled := true
	reset := true
	patchBody, _ := json.Marshal(PeriodicPromptPatchRequest{Enabled: &enabled, ResetCounters: &reset})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/periodic", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.handleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := ps.Get()
	if err != nil {
		t.Fatalf("Get periodic after PATCH: %v", err)
	}
	if !stored.Enabled {
		t.Error("Enabled after restore = false, want true")
	}
	if stored.IterationCount != 0 {
		t.Errorf("IterationCount after reset = %d, want 0", stored.IterationCount)
	}
	if stored.FirstRunAt != nil {
		t.Errorf("FirstRunAt after reset = %v, want nil", stored.FirstRunAt)
	}
	if stored.StoppedReason != "" {
		t.Errorf("StoppedReason after restore = %q, want empty", stored.StoppedReason)
	}
}

// TestHandleSessionPeriodic_PatchDelayClamped verifies that a PATCH lowering the delay below
// the floor on an onCompletion config is clamped up to the floor.
func TestHandleSessionPeriodic_PatchDelayClamped(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const sid = "test-oncompletion-patch-clamp"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test-server", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{store: store, eventsManager: NewGlobalEventsManager()}

	putPeriodicForTest(t, server, sid, PeriodicPromptRequest{
		Prompt:       "keep going",
		Enabled:      true,
		Trigger:      session.TriggerOnCompletion,
		DelaySeconds: 30,
	})

	belowFloor := 1
	patchBody, _ := json.Marshal(PeriodicPromptPatchRequest{DelaySeconds: &belowFloor})
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sid+"/periodic", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.handleSessionPeriodic(w, req, sid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH periodic: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, err := store.Periodic(sid).Get()
	if err != nil {
		t.Fatalf("Get periodic after PATCH: %v", err)
	}
	if stored.DelaySeconds != server.periodicDelayFloor() {
		t.Errorf("DelaySeconds after PATCH = %d, want clamped to floor %d", stored.DelaySeconds, server.periodicDelayFloor())
	}
}

// TestHandleSessionPeriodic_MakePeriodicDraft verifies the "Make periodic" frontend flow:
// PUT /api/sessions/{id}/periodic with a draft body (enabled:false, prompt:"(pending)")
// on an existing top-level session succeeds and stores the draft config.
func TestHandleSessionPeriodic_MakePeriodicDraft(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(session.Metadata{
		SessionID:  "test-make-periodic-draft",
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		store:         store,
		eventsManager: NewGlobalEventsManager(),
	}

	// Draft body — mirrors what handleMakePeriodic in app.js sends.
	body, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "(pending)",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/test-make-periodic-draft/periodic", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSessionPeriodic(w, req, "test-make-periodic-draft", "")

	if w.Code != http.StatusOK {
		t.Errorf("PUT periodic draft: Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the stored periodic config reflects the draft state.
	ps := store.Periodic("test-make-periodic-draft")
	stored, err := ps.Get()
	if err != nil {
		t.Fatalf("Get periodic after PUT: %v", err)
	}
	if stored.Enabled {
		t.Errorf("Draft periodic should have Enabled=false, got true")
	}
	if stored.Prompt != "(pending)" {
		t.Errorf("Draft periodic prompt = %q, want %q", stored.Prompt, "(pending)")
	}
}

// TestHandleSessionPeriodic_DeleteRemovesConfig verifies the "Make non-periodic" frontend flow:
// PUT a draft config, confirm it exists, then DELETE it via handleSessionPeriodic,
// assert HTTP 204, and confirm the config is gone from the store.
func TestHandleSessionPeriodic_DeleteRemovesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const sid = "test-delete-periodic"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		store:         store,
		eventsManager: NewGlobalEventsManager(),
	}

	// Step 1: PUT a draft periodic config so there is something to delete.
	putBody, _ := json.Marshal(PeriodicPromptRequest{
		Prompt:    "(pending)",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/sessions/"+sid+"/periodic", bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	server.handleSessionPeriodic(putW, putReq, sid, "")
	if putW.Code != http.StatusOK {
		t.Fatalf("PUT periodic: Status = %d, want 200. Body: %s", putW.Code, putW.Body.String())
	}

	// Confirm the config exists before deleting.
	if _, err := store.Periodic(sid).Get(); err != nil {
		t.Fatalf("Get periodic before DELETE: %v", err)
	}

	// Step 2: DELETE — mirrors what handleMakeNonPeriodic in app.js sends.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sid+"/periodic", nil)
	delW := httptest.NewRecorder()
	server.handleSessionPeriodic(delW, delReq, sid, "")

	// handleDeletePeriodic calls writeNoContent → HTTP 204.
	if delW.Code != http.StatusNoContent {
		t.Errorf("DELETE periodic: Status = %d, want %d. Body: %s", delW.Code, http.StatusNoContent, delW.Body.String())
	}

	// Step 3: Confirm the config is gone.
	_, getErr := store.Periodic(sid).Get()
	if getErr == nil {
		t.Errorf("Expected error (config gone) after DELETE, got nil")
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
				makePrompt("a", withEnabledWhen(`acp.matchesServerType("augment")`)),
				makePrompt("b", withEnabledWhen("session.isChild")),
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
		// 3a. acp.matchesServerType type match — included
		{
			name:    "acp_matchesServerType type match included",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`acp.matchesServerType("augment")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
			},
			wantNames: []string{"p"},
		},
		// 3b. acp.matchesServerType display name does not match — excluded
		{
			name:    "acp_matchesServerType display name does not match excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`acp.matchesServerType("Auggie (Opus 4.6)")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
			},
			wantNames: nil,
		},
		// 4. acp.matchesServerType type match with different display name
		{
			name:    "acp_matchesServerType type match different display name",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`acp.matchesServerType("augment")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Sonnet 4.6)", Type: "augment"},
			},
			wantNames: []string{"p"},
		},
		// 5. acp.matchesServerType list of server types
		{
			name:    "acp_matchesServerType list match",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`acp.matchesServerType(["augment", "claude-code"])`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Claude Code (Opus 4.6)", Type: "claude-code"},
			},
			wantNames: []string{"p"},
		},
		// 6. acp.matchesServerType case insensitive (matches type)
		{
			name:    "acp_matchesServerType case insensitive",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`acp.matchesServerType("AUGMENT")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
			},
			wantNames: []string{"p"},
		},
		// 7. acp.matchesServerType fail-open when no ACP active
		{
			name:    "acp_matchesServerType fail-open no acp active",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`acp.matchesServerType("augment")`))},
			ctx: &config.PromptEnabledContext{
				ACP: config.ACPContext{Name: "", Type: ""},
			},
			wantNames: []string{"p"},
		},
		// 8. tools.hasPattern satisfied
		{
			name:    "tools_hasPattern satisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_conversation_new", "other_tool"}},
			},
			wantNames: []string{"p"},
		},
		// 9. tools.hasPattern unsatisfied
		{
			name:    "tools_hasPattern unsatisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"other_tool"}},
			},
			wantNames: nil,
		},
		// 10. tools.hasAllPatterns all satisfied
		{
			name:    "tools_hasAllPatterns all satisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasAllPatterns(["mitto_*", "jira_*"])`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_foo", "jira_bar"}},
			},
			wantNames: []string{"p"},
		},
		// 11. tools.hasAllPatterns partially satisfied — excluded
		{
			name:    "tools_hasAllPatterns partially satisfied excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasAllPatterns(["mitto_*", "jira_*"])`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_foo"}},
			},
			wantNames: nil,
		},
		// 12. tools.hasPattern fetched-empty tools — excluded (fail-closed)
		{
			name:    "tools_hasPattern fetched-empty tools excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: true, Names: nil},
			},
			wantNames: nil,
		},
		// 12b. tools.hasPattern unknown tools — included (fail-open during warm-up)
		{
			name:    "tools_hasPattern unknown tools fail-open included",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Available: false, Names: nil},
			},
			wantNames: []string{"p"},
		},
		// 13. enabledWhen CEL true expression
		{
			name:    "enabledWhen CEL true expression included",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen("session.isChild"))},
			ctx: &config.PromptEnabledContext{
				Session: config.SessionContext{IsChild: true},
			},
			wantNames: []string{"p"},
		},
		// 14. enabledWhen CEL false expression
		{
			name:    "enabledWhen CEL false expression excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen("session.isChild"))},
			ctx: &config.PromptEnabledContext{
				Session: config.SessionContext{IsChild: false},
			},
			wantNames: nil,
		},
		// 15. enabledWhen CEL complex expression
		{
			name: "enabledWhen CEL complex expression included",
			prompts: []config.WebPrompt{
				makePrompt("p", withEnabledWhen(`"reasoning" in acp.tags`)),
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
		// 17. Combined: acp.matchesServerType + tools.hasPattern + CEL all pass
		{
			name: "combined acp_matchesServerType and tools_hasPattern and CEL all pass",
			prompts: []config.WebPrompt{
				makePrompt("p",
					withEnabledWhen(`acp.matchesServerType("augment") && tools.hasPattern("mitto_*") && !session.isChild`),
				),
			},
			ctx: &config.PromptEnabledContext{
				ACP:     config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
				Tools:   config.ToolsContext{Available: true, Names: []string{"mitto_conversation_new"}},
				Session: config.SessionContext{IsChild: false},
			},
			wantNames: []string{"p"},
		},
		// 18. Combined: acp.matchesServerType passes, tools.hasPattern fails
		{
			name: "combined acp_matchesServerType passes tools_hasPattern fails excluded",
			prompts: []config.WebPrompt{
				makePrompt("p",
					withEnabledWhen(`acp.matchesServerType("augment") && tools.hasPattern("jira_*")`),
				),
			},
			ctx: &config.PromptEnabledContext{
				ACP:   config.ACPContext{Name: "Auggie (Opus 4.6)", Type: "augment"},
				Tools: config.ToolsContext{Available: true, Names: []string{"mitto_foo"}},
			},
			wantNames: nil,
		},
		// 19. Combined: acp.matchesServerType fails — whole expression excluded
		{
			name: "combined acp_matchesServerType fails excluded",
			prompts: []config.WebPrompt{
				makePrompt("p",
					withEnabledWhen(`acp.matchesServerType("claude") && tools.hasPattern("mitto_*") && true`),
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
				makePrompt("excluded-acp", withEnabledWhen(`acp.matchesServerType("claude")`)),
				makePrompt("included-2", withEnabledWhen("!session.isChild")),
				makePrompt("excluded-mcp", withEnabledWhen(`tools.hasPattern("jira_*")`)),
				makePrompt("included-3", withEnabledWhen(`acp.matchesServerType("augment")`)),
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

// TestToggleEnabled_SingleDocFile verifies that toggling a processor whose YAML
// file contains a single document updates the file in-place (existing behavior).
func TestToggleEnabled_SingleDocFile(t *testing.T) {
	wsDir := t.TempDir()

	// Create the workspace processors directory and a single-doc processor file.
	procDir := filepath.Join(wsDir, ".mitto", "processors")
	if err := os.MkdirAll(procDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	procFile := filepath.Join(procDir, "my-proc.yaml")
	original := "name: my-proc\nwhen:\n  on: userPrompt\n  match: all\ncommand: /bin/echo\n"
	if err := os.WriteFile(procFile, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}

	body, _ := json.Marshal(map[string]interface{}{
		"dir":     wsDir,
		"name":    "my-proc",
		"enabled": false,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/workspace-processors/toggle-enabled", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleWorkspaceProcessorsToggleEnabled(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// The processor file must have been updated in-place.
	data, err := os.ReadFile(procFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("expected 'enabled: false' in file after toggle; got:\n%s", string(data))
	}

	// No .mittorc should have been created (in-place path, not .mittorc path).
	rcPath := filepath.Join(wsDir, ".mittorc")
	if _, err := os.Stat(rcPath); err == nil {
		data, _ := os.ReadFile(rcPath)
		t.Errorf(".mittorc should NOT be created for single-doc toggle; content:\n%s", string(data))
	}
}

// TestToggleEnabled_MultiDocFile verifies that toggling a processor whose YAML
// file contains multiple `---`-separated documents writes to .mittorc and leaves
// the YAML file byte-identical to the original.
func TestToggleEnabled_MultiDocFile(t *testing.T) {
	wsDir := t.TempDir()

	// Create the workspace processors directory and a multi-doc processor file.
	procDir := filepath.Join(wsDir, ".mitto", "processors")
	if err := os.MkdirAll(procDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	procFile := filepath.Join(procDir, "multi-proc.yaml")
	original := "name: multi-proc\nwhen:\n  on: userPrompt\n  match: all\ncommand: /bin/echo\n---\nname: multi-proc-b\nwhen:\n  on: agentResponded\n  match: all\ncommand: /bin/echo\n"
	if err := os.WriteFile(procFile, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}

	body, _ := json.Marshal(map[string]interface{}{
		"dir":     wsDir,
		"name":    "multi-proc",
		"enabled": false,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/workspace-processors/toggle-enabled", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleWorkspaceProcessorsToggleEnabled(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// The multi-doc file must be byte-identical to the original (not rewritten).
	data, err := os.ReadFile(procFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Errorf("multi-doc YAML file was modified:\ngot:\n%s\nwant:\n%s", string(data), original)
	}

	// .mittorc must have been created with the processors override.
	rcPath := filepath.Join(wsDir, ".mittorc")
	rcData, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf(".mittorc not created: %v", err)
	}
	if !strings.Contains(string(rcData), "multi-proc") {
		t.Errorf(".mittorc does not contain 'multi-proc':\n%s", string(rcData))
	}
	if !strings.Contains(string(rcData), "enabled: false") {
		t.Errorf(".mittorc does not contain 'enabled: false':\n%s", string(rcData))
	}
}

// TestToggleEnabled_GlobalProcessor verifies that toggling a global processor
// (not found in workspace dirs) writes to .mittorc.
func TestToggleEnabled_GlobalProcessor(t *testing.T) {
	wsDir := t.TempDir()
	// Do NOT create any processor file in the workspace dir —
	// simulates a global/builtin processor.

	server := &Server{
		sessionManager: conversation.NewSessionManager("", "", false, nil),
	}

	body, _ := json.Marshal(map[string]interface{}{
		"dir":     wsDir,
		"name":    "global-proc",
		"enabled": false,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/workspace-processors/toggle-enabled", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleWorkspaceProcessorsToggleEnabled(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// .mittorc must record the override.
	rcPath := filepath.Join(wsDir, ".mittorc")
	rcData, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf(".mittorc not created: %v", err)
	}
	if !strings.Contains(string(rcData), "global-proc") {
		t.Errorf(".mittorc does not contain 'global-proc':\n%s", string(rcData))
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
// (item.*, session.isChild, permissions, commandExists, …) is applied at once.
func TestFilterPromptsByEnabled_ItemGating(t *testing.T) {
	s := &Server{}

	makePrompt := func(name, enabledWhen string) config.WebPrompt {
		p := config.WebPrompt{Name: name, EnabledWhen: enabledWhen}
		tr := true
		p.Enabled = &tr
		return p
	}

	itemPrompt := makePrompt("start-work", `item.status != "closed"`)
	nonItemPrompt := makePrompt("triage", `session.isChild == false`)
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
// for the beads-menu bug where dir-based enabledWhen gates (dirExists/fileExists)
// were evaluated against the active session's working dir instead of the dir
// query param. The frontend always appends &session_id=<activeConversation>, so
// when that conversation lived in a folder without ".beads" the gate
// dirExists(".beads") evaluated false and every beads prompt was filtered out —
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
    enabledWhen: 'dirExists(".beads")'
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
		t.Errorf("dir-gated prompt was filtered out: dirExists(\".beads\") evaluated against the session's folder instead of the dir param; got %v", names)
	}
	if !hasName(names, "Ungated") {
		t.Errorf("ungated prompt missing, got %v", names)
	}
}

// TestHandleSetPeriodic_PendingPlaceholderDoesNotBecomeTitle verifies that when the
// frontend submits { "prompt": "(pending)", "prompt_name": "CGW: latest questions", ... }
// (the draft shape documented at the top of this file), the title generator receives the
// resolved prompt body rather than the literal "(pending)" placeholder string.
func TestHandleSetPeriodic_PendingPlaceholderDoesNotBecomeTitle(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	const sid = "test-pending-placeholder-title"
	if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: tmpDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// conversation.BackgroundSession with a promptResolver that returns a recognisable body.
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:  sid,
		WorkingDir: tmpDir,
		Store:      store,
		PromptResolver: func(name, dir string) (string, error) {
			return "The actual resolved body for " + name, nil
		},
	})

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.AddSessionForTest(bs)

	server := &Server{
		store:          store,
		sessionManager: sm,
		eventsManager:  NewGlobalEventsManager(),
	}

	putPeriodicForTest(t, server, sid, PeriodicPromptRequest{
		Prompt:     "(pending)",
		PromptName: "CGW: latest questions",
		Frequency:  session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:    true,
	})

	meta, err := store.GetMetadata(sid)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if strings.Contains(strings.ToLower(meta.Name), "pending") {
		t.Errorf("title must not contain 'pending' when prompt_name is set; got %q", meta.Name)
	}
	if !strings.Contains(strings.ToLower(meta.Name), "actual") && !strings.Contains(strings.ToLower(meta.Name), "resolved") {
		t.Errorf("title should be derived from the resolved prompt body; got %q", meta.Name)
	}
}
