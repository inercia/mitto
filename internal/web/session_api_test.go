package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
		sessionManager: NewSessionManager("", "", false, nil),
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
		sessionManager: NewSessionManager("", "", false, nil),
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
	promptContent := `---
name: "Default Workspace Prompt"
description: "A prompt from the default .mitto/prompts directory"
---
This is the prompt content from the default workspace prompts directory.
`
	if err := os.WriteFile(mittoPromptsDir+"/test-prompt.md", []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to create prompt file: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
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
	defaultPromptContent := `---
name: "Shared Prompt"
description: "From default .mitto/prompts"
---
Default version
`
	if err := os.WriteFile(mittoPromptsDir+"/shared.md", []byte(defaultPromptContent), 0644); err != nil {
		t.Fatalf("Failed to create default prompt file: %v", err)
	}

	// Create a custom prompts directory defined via prompts_dirs in .mittorc
	customPromptsDir := tmpDir + "/custom-prompts"
	if err := os.MkdirAll(customPromptsDir, 0755); err != nil {
		t.Fatalf("Failed to create custom prompts dir: %v", err)
	}

	// Create a prompt with the same name in the custom directory (should override)
	customPromptContent := `---
name: "Shared Prompt"
description: "From custom prompts_dirs"
---
Custom version from prompts_dirs
`
	if err := os.WriteFile(customPromptsDir+"/shared.md", []byte(customPromptContent), 0644); err != nil {
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
		sessionManager: NewSessionManager("", "", false, nil),
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
	if len(response.Prompts) > 0 && response.Prompts[0].Prompt != "Custom version from prompts_dirs" {
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
		sessionManager: NewSessionManager("", "", false, nil),
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

	// Use NewSessionManagerWithOptions with empty workspaces list to ensure
	// no default workspace is configured. This simulates the case where
	// a user hasn't configured any workspaces yet.
	sm := NewSessionManagerWithOptions(SessionManagerOptions{
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
	sm := NewSessionManager("echo test", "test-server", true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	mockSession := &BackgroundSession{
		persistedID: "test-session-archive",
		isPrompting: false,
		ctx:         ctx,
		cancel:      cancel,
	}
	mockSession.promptCond = sync.NewCond(&mockSession.promptMu)
	sm.mu.Lock()
	sm.sessions["test-session-archive"] = mockSession
	sm.mu.Unlock()

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
	sm := NewSessionManager("echo test", "test-server", true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	mockSession := &BackgroundSession{
		persistedID: "test-session-archive-wait",
		isPrompting: true,
		ctx:         ctx,
		cancel:      cancel,
	}
	mockSession.promptCond = sync.NewCond(&mockSession.promptMu)
	sm.mu.Lock()
	sm.sessions["test-session-archive-wait"] = mockSession
	sm.mu.Unlock()

	server := &Server{
		sessionManager: sm,
		store:          store,
		eventsManager:  NewGlobalEventsManager(),
	}

	// Simulate prompt completion after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		mockSession.promptMu.Lock()
		mockSession.isPrompting = false
		mockSession.promptCond.Broadcast()
		mockSession.promptMu.Unlock()
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
	sm := NewSessionManager("echo test", "test-server", true, nil)
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
		sessionManager: NewSessionManager("", "", false, nil),
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
		sessionManager: NewSessionManager("", "", false, nil),
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
				Tools: config.ToolsContext{Names: []string{"mitto_conversation_new", "other_tool"}},
			},
			wantNames: []string{"p"},
		},
		// 9. tools.hasPattern unsatisfied
		{
			name:    "tools_hasPattern unsatisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Names: []string{"other_tool"}},
			},
			wantNames: nil,
		},
		// 10. tools.hasAllPatterns all satisfied
		{
			name:    "tools_hasAllPatterns all satisfied",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasAllPatterns(["mitto_*", "jira_*"])`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Names: []string{"mitto_foo", "jira_bar"}},
			},
			wantNames: []string{"p"},
		},
		// 11. tools.hasAllPatterns partially satisfied — excluded
		{
			name:    "tools_hasAllPatterns partially satisfied excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasAllPatterns(["mitto_*", "jira_*"])`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Names: []string{"mitto_foo"}},
			},
			wantNames: nil,
		},
		// 12. tools.hasPattern empty tools — excluded
		{
			name:    "tools_hasPattern empty tools excluded",
			prompts: []config.WebPrompt{makePrompt("p", withEnabledWhen(`tools.hasPattern("mitto_*")`))},
			ctx: &config.PromptEnabledContext{
				Tools: config.ToolsContext{Names: nil},
			},
			wantNames: nil,
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
				Tools:   config.ToolsContext{Names: []string{"mitto_conversation_new"}},
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
				Tools: config.ToolsContext{Names: []string{"mitto_foo"}},
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
				Tools: config.ToolsContext{Names: []string{"mitto_foo"}},
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
				Tools:   config.ToolsContext{Names: []string{"mitto_foo"}},
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
