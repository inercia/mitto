package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestHandleGetSessionUserData_NotFound(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent/user-data", nil)
	w := httptest.NewRecorder()

	server.handleGetSessionUserData(w, req, "nonexistent-session")

	// Should return empty data, not 404 (session dir check happens on write)
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetSessionUserData_EmptyData(t *testing.T) {
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
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
		store:          store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260131-120000-abcd1234/user-data", nil)
	w := httptest.NewRecorder()

	server.handleGetSessionUserData(w, req, "20260131-120000-abcd1234")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var response session.UserData
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(response.Attributes) != 0 {
		t.Errorf("Expected empty attributes, got %d", len(response.Attributes))
	}
}

func TestHandlePutSessionUserData(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceDir := t.TempDir() // Separate workspace directory

	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a .mittorc file with user data schema in the workspace
	mittorc := `
conversations:
  user_data:
    - name: "JIRA ticket"
      type: url
    - name: "Description"
      type: string
`
	if err := os.WriteFile(workspaceDir+"/.mittorc", []byte(mittorc), 0644); err != nil {
		t.Fatalf("Failed to write .mittorc: %v", err)
	}

	// Create a session with the workspace directory
	meta := session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: workspaceDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := NewSessionManager("", "", false, nil)
	sm.SetStore(store)

	server := &Server{
		sessionManager: sm,
		store:          store,
	}

	// Set user data
	reqBody := UserDataUpdateRequest{
		Attributes: []session.UserDataAttribute{
			{Name: "JIRA ticket", Value: "https://jira.example.com/PROJ-123"},
			{Name: "Description", Value: "Test description"},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/sessions/20260131-120000-abcd1234/user-data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handlePutSessionUserData(w, req, "20260131-120000-abcd1234")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify data was saved
	data, err := store.GetUserData("20260131-120000-abcd1234")
	if err != nil {
		t.Fatalf("GetUserData failed: %v", err)
	}

	if len(data.Attributes) != 2 {
		t.Fatalf("Expected 2 attributes, got %d", len(data.Attributes))
	}
}

func TestHandlePutSessionUserData_NoSchema(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceDir := t.TempDir() // Workspace without .mittorc

	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session (no .mittorc in workspace)
	meta := session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: workspaceDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := NewSessionManager("", "", false, nil)
	sm.SetStore(store)

	server := &Server{
		sessionManager: sm,
		store:          store,
	}

	// Try to set user data without a schema
	reqBody := UserDataUpdateRequest{
		Attributes: []session.UserDataAttribute{
			{Name: "anything", Value: "some value"},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/sessions/20260131-120000-abcd1234/user-data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handlePutSessionUserData(w, req, "20260131-120000-abcd1234")

	// Should fail with validation error
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d. Body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandlePutSessionUserData_EmptyData(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceDir := t.TempDir() // Workspace without .mittorc

	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session (no .mittorc in workspace)
	meta := session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: workspaceDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := NewSessionManager("", "", false, nil)
	sm.SetStore(store)

	server := &Server{
		sessionManager: sm,
		store:          store,
	}

	// Set empty user data (should succeed even without schema)
	reqBody := UserDataUpdateRequest{
		Attributes: []session.UserDataAttribute{},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/sessions/20260131-120000-abcd1234/user-data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handlePutSessionUserData(w, req, "20260131-120000-abcd1234")

	// Should succeed
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandleWorkspaceUserDataSchema_NoWorkspace(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspace/user-data-schema?working_dir=/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleWorkspaceUserDataSchema(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleWorkspaceUserDataSchema_MissingParam(t *testing.T) {
	server := &Server{
		sessionManager: NewSessionManager("", "", false, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspace/user-data-schema", nil)
	w := httptest.NewRecorder()

	server.handleWorkspaceUserDataSchema(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
