package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// newUserDataHandlers creates a temp store (optionally seeded with meta) plus a
// SessionManager, returning the store and the Handlers under test.
func newUserDataHandlers(t *testing.T, meta *session.Metadata) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if meta != nil {
		if err := store.Create(*meta); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetStore(store)
	h := New(Deps{Store: store, SessionManager: sm})
	return store, h
}

func TestHandleGetSessionUserData_NotFound(t *testing.T) {
	_, h := newUserDataHandlers(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent/user-data", nil)
	w := httptest.NewRecorder()

	h.HandleGetSessionUserData(w, req, "nonexistent-session")

	// Should return empty data, not 404 (session dir check happens on write)
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetSessionUserData_EmptyData(t *testing.T) {
	_, h := newUserDataHandlers(t, &session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260131-120000-abcd1234/user-data", nil)
	w := httptest.NewRecorder()

	h.HandleGetSessionUserData(w, req, "20260131-120000-abcd1234")

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
	workspaceDir := t.TempDir() // Separate workspace directory

	// Create a .mittorc file with user data schema in the workspace
	mittorc := `
metadata:
  user_data:
    - name: "JIRA ticket"
      type: url
    - name: "Description"
      type: string
`
	if err := os.WriteFile(workspaceDir+"/.mittorc", []byte(mittorc), 0644); err != nil {
		t.Fatalf("Failed to write .mittorc: %v", err)
	}

	store, h := newUserDataHandlers(t, &session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: workspaceDir,
	})

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

	h.HandlePutSessionUserData(w, req, "20260131-120000-abcd1234")

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
	workspaceDir := t.TempDir() // Workspace without .mittorc

	_, h := newUserDataHandlers(t, &session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: workspaceDir,
	})

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

	h.HandlePutSessionUserData(w, req, "20260131-120000-abcd1234")

	// Should fail with validation error
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d. Body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandlePutSessionUserData_EmptyData(t *testing.T) {
	workspaceDir := t.TempDir() // Workspace without .mittorc

	_, h := newUserDataHandlers(t, &session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: workspaceDir,
	})

	// Set empty user data (should succeed even without schema)
	reqBody := UserDataUpdateRequest{
		Attributes: []session.UserDataAttribute{},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/sessions/20260131-120000-abcd1234/user-data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePutSessionUserData(w, req, "20260131-120000-abcd1234")

	// Should succeed
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandleUserData_InvalidBody(t *testing.T) {
	// Use a valid session so the handler reaches parseJSONBody, not short-circuit on not-found.
	_, h := newUserDataHandlers(t, &session.Metadata{
		SessionID:  "20260131-120000-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	})

	req := httptest.NewRequest(http.MethodPut, "/api/sessions/20260131-120000-abcd1234/user-data", bytes.NewReader([]byte("{invalid json}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePutSessionUserData(w, req, "20260131-120000-abcd1234")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "bad_request")
	}
	if !strings.Contains(resp.Error.Message, "Invalid request body") {
		t.Errorf("error.message = %q, should contain %q", resp.Error.Message, "Invalid request body")
	}
}

func TestHandleWorkspaceUserDataSchema_NoWorkspace(t *testing.T) {
	_, h := newUserDataHandlers(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace/user-data-schema?working_dir=/nonexistent", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaceUserDataSchema(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "not_found" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "not_found")
	}
	if resp.Error.Message != "Unknown workspace" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "Unknown workspace")
	}
}

func TestHandleWorkspaceUserDataSchema_MissingParam(t *testing.T) {
	_, h := newUserDataHandlers(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace/user-data-schema", nil)
	w := httptest.NewRecorder()

	h.HandleWorkspaceUserDataSchema(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, "bad_request")
	}
	if resp.Error.Message != "working_dir query parameter is required" {
		t.Errorf("error.message = %q, want %q", resp.Error.Message, "working_dir query parameter is required")
	}
}
