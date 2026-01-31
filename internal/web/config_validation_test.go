package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func TestConfigValidationError_Error(t *testing.T) {
	err := &configValidationError{
		StatusCode: http.StatusBadRequest,
		Message:    "Test error message",
	}

	if err.Error() != "Test error message" {
		t.Errorf("Error() = %q, want %q", err.Error(), "Test error message")
	}
}

func TestConfigValidationError_WithDetails(t *testing.T) {
	err := &configValidationError{
		StatusCode: http.StatusConflict,
		Message:    "Conflict error",
		Details: map[string]interface{}{
			"workspace": "/path/to/workspace",
			"count":     5,
		},
	}

	if err.Error() != "Conflict error" {
		t.Errorf("Error() = %q, want %q", err.Error(), "Conflict error")
	}

	if err.Details["workspace"] != "/path/to/workspace" {
		t.Errorf("Details[workspace] = %v, want %q", err.Details["workspace"], "/path/to/workspace")
	}

	if err.Details["count"] != 5 {
		t.Errorf("Details[count] = %v, want %d", err.Details["count"], 5)
	}
}

func TestConfigValidationError_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"BadRequest", http.StatusBadRequest},
		{"Conflict", http.StatusConflict},
		{"InternalServerError", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &configValidationError{
				StatusCode: tt.statusCode,
				Message:    "Test",
			}
			if err.StatusCode != tt.statusCode {
				t.Errorf("StatusCode = %d, want %d", err.StatusCode, tt.statusCode)
			}
		})
	}
}

func TestValidateConfigRequest_NoWorkspaces(t *testing.T) {
	server := &Server{}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{},
		ACPServers: []struct {
			Name    string             `json:"name"`
			Command string             `json:"command"`
			Prompts []config.WebPrompt `json:"prompts,omitempty"`
		}{{Name: "test", Command: "cmd"}},
	}

	err := server.validateConfigRequest(req)
	if err == nil {
		t.Fatal("Expected error for empty workspaces")
	}
	if err.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusBadRequest)
	}
}

func TestValidateConfigRequest_NoACPServers(t *testing.T) {
	server := &Server{}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{{WorkingDir: "/tmp", ACPServer: "test"}},
		ACPServers: []struct {
			Name    string             `json:"name"`
			Command string             `json:"command"`
			Prompts []config.WebPrompt `json:"prompts,omitempty"`
		}{},
	}

	err := server.validateConfigRequest(req)
	if err == nil {
		t.Fatal("Expected error for empty ACP servers")
	}
	if err.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusBadRequest)
	}
}

func TestValidateConfigRequest_EmptyServerName(t *testing.T) {
	server := &Server{}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{{WorkingDir: "/tmp", ACPServer: "test"}},
		ACPServers: []struct {
			Name    string             `json:"name"`
			Command string             `json:"command"`
			Prompts []config.WebPrompt `json:"prompts,omitempty"`
		}{{Name: "", Command: "cmd"}},
	}

	err := server.validateConfigRequest(req)
	if err == nil {
		t.Fatal("Expected error for empty server name")
	}
}

func TestValidateConfigRequest_EmptyServerCommand(t *testing.T) {
	server := &Server{}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{{WorkingDir: "/tmp", ACPServer: "test"}},
		ACPServers: []struct {
			Name    string             `json:"name"`
			Command string             `json:"command"`
			Prompts []config.WebPrompt `json:"prompts,omitempty"`
		}{{Name: "test", Command: ""}},
	}

	err := server.validateConfigRequest(req)
	if err == nil {
		t.Fatal("Expected error for empty server command")
	}
}

func TestValidateConfigRequest_DuplicateServerName(t *testing.T) {
	server := &Server{}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{{WorkingDir: "/tmp", ACPServer: "test"}},
		ACPServers: []struct {
			Name    string             `json:"name"`
			Command string             `json:"command"`
			Prompts []config.WebPrompt `json:"prompts,omitempty"`
		}{
			{Name: "test", Command: "cmd1"},
			{Name: "test", Command: "cmd2"},
		},
	}

	err := server.validateConfigRequest(req)
	if err == nil {
		t.Fatal("Expected error for duplicate server name")
	}
}

func TestValidateConfigRequest_Valid(t *testing.T) {
	server := &Server{}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{{WorkingDir: "/tmp", ACPServer: "test"}},
		ACPServers: []struct {
			Name    string             `json:"name"`
			Command string             `json:"command"`
			Prompts []config.WebPrompt `json:"prompts,omitempty"`
		}{{Name: "test", Command: "cmd"}},
	}

	err := server.validateConfigRequest(req)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestWriteConfigError(t *testing.T) {
	server := &Server{}

	err := &configValidationError{
		StatusCode: http.StatusBadRequest,
		Message:    "test error",
	}

	w := httptest.NewRecorder()
	server.writeConfigError(w, err)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}

func TestWriteConfigError_WithDetails(t *testing.T) {
	server := &Server{}

	err := &configValidationError{
		StatusCode: http.StatusConflict,
		Message:    "conflict error",
		Details: map[string]interface{}{
			"field": "workspace",
			"value": "/tmp",
		},
	}

	w := httptest.NewRecorder()
	server.writeConfigError(w, err)

	if w.Code != http.StatusConflict {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestCheckWorkspaceConflicts_NoRemovedWorkspaces(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1"},
	})

	server := &Server{
		sessionManager: sm,
	}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/workspace1"},
		},
	}

	err := server.checkWorkspaceConflicts(req)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestCheckWorkspaceConflicts_NilStore(t *testing.T) {
	sm := NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{WorkingDir: "/workspace1"},
		{WorkingDir: "/workspace2"},
	})

	server := &Server{
		sessionManager: sm,
		store:          nil,
	}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{
			{WorkingDir: "/workspace1"},
		},
	}

	err := server.checkWorkspaceConflicts(req)
	if err == nil {
		t.Fatal("Expected error for nil store")
	}
	if err.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusInternalServerError)
	}
}
