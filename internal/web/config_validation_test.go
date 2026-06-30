package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
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
			Name                string                                 `json:"name"`
			Command             string                                 `json:"command"`
			Type                string                                 `json:"type,omitempty"`
			Env                 map[string]string                      `json:"env,omitempty"`
			Prompts             []config.WebPrompt                     `json:"prompts,omitempty"`
			Source              config.ConfigItemSource                `json:"source,omitempty"`
			AutoApprove         bool                                   `json:"auto_approve,omitempty"`
			Tags                []string                               `json:"tags,omitempty"`
			Constraints         map[string]*config.ACPServerConstraint `json:"constraints,omitempty"`
			ContextFlushCommand string                                 `json:"context_flush_command,omitempty"`
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
		// Workspace with no ACPServer reference — valid when no servers configured
		Workspaces: []config.WorkspaceSettings{{WorkingDir: "/tmp"}},
		ACPServers: []struct {
			Name                string                                 `json:"name"`
			Command             string                                 `json:"command"`
			Type                string                                 `json:"type,omitempty"`
			Env                 map[string]string                      `json:"env,omitempty"`
			Prompts             []config.WebPrompt                     `json:"prompts,omitempty"`
			Source              config.ConfigItemSource                `json:"source,omitempty"`
			AutoApprove         bool                                   `json:"auto_approve,omitempty"`
			Tags                []string                               `json:"tags,omitempty"`
			Constraints         map[string]*config.ACPServerConstraint `json:"constraints,omitempty"`
			ContextFlushCommand string                                 `json:"context_flush_command,omitempty"`
		}{},
	}

	err := server.validateConfigRequest(req)
	if err != nil {
		t.Errorf("unexpected error for zero ACP servers: %v", err)
	}
}

func TestValidateConfigRequest_EmptyServerName(t *testing.T) {
	server := &Server{}

	req := &ConfigSaveRequest{
		Workspaces: []config.WorkspaceSettings{{WorkingDir: "/tmp", ACPServer: "test"}},
		ACPServers: []struct {
			Name                string                                 `json:"name"`
			Command             string                                 `json:"command"`
			Type                string                                 `json:"type,omitempty"`
			Env                 map[string]string                      `json:"env,omitempty"`
			Prompts             []config.WebPrompt                     `json:"prompts,omitempty"`
			Source              config.ConfigItemSource                `json:"source,omitempty"`
			AutoApprove         bool                                   `json:"auto_approve,omitempty"`
			Tags                []string                               `json:"tags,omitempty"`
			Constraints         map[string]*config.ACPServerConstraint `json:"constraints,omitempty"`
			ContextFlushCommand string                                 `json:"context_flush_command,omitempty"`
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
			Name                string                                 `json:"name"`
			Command             string                                 `json:"command"`
			Type                string                                 `json:"type,omitempty"`
			Env                 map[string]string                      `json:"env,omitempty"`
			Prompts             []config.WebPrompt                     `json:"prompts,omitempty"`
			Source              config.ConfigItemSource                `json:"source,omitempty"`
			AutoApprove         bool                                   `json:"auto_approve,omitempty"`
			Tags                []string                               `json:"tags,omitempty"`
			Constraints         map[string]*config.ACPServerConstraint `json:"constraints,omitempty"`
			ContextFlushCommand string                                 `json:"context_flush_command,omitempty"`
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
			Name                string                                 `json:"name"`
			Command             string                                 `json:"command"`
			Type                string                                 `json:"type,omitempty"`
			Env                 map[string]string                      `json:"env,omitempty"`
			Prompts             []config.WebPrompt                     `json:"prompts,omitempty"`
			Source              config.ConfigItemSource                `json:"source,omitempty"`
			AutoApprove         bool                                   `json:"auto_approve,omitempty"`
			Tags                []string                               `json:"tags,omitempty"`
			Constraints         map[string]*config.ACPServerConstraint `json:"constraints,omitempty"`
			ContextFlushCommand string                                 `json:"context_flush_command,omitempty"`
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
			Name                string                                 `json:"name"`
			Command             string                                 `json:"command"`
			Type                string                                 `json:"type,omitempty"`
			Env                 map[string]string                      `json:"env,omitempty"`
			Prompts             []config.WebPrompt                     `json:"prompts,omitempty"`
			Source              config.ConfigItemSource                `json:"source,omitempty"`
			AutoApprove         bool                                   `json:"auto_approve,omitempty"`
			Tags                []string                               `json:"tags,omitempty"`
			Constraints         map[string]*config.ACPServerConstraint `json:"constraints,omitempty"`
			ContextFlushCommand string                                 `json:"context_flush_command,omitempty"`
		}{{Name: "test", Command: "cmd"}},
	}

	err := server.validateConfigRequest(req)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

// authConfigBody is a minimal valid config save request (one workspace + matching
// ACP server) carrying a simple-auth block with the given username/password. It is
// built via JSON to avoid spelling out the anonymous structs in ConfigSaveRequest.
func authConfigBody(username, password string) *ConfigSaveRequest {
	body := `{
		"workspaces": [{"working_dir": "/tmp", "acp_server": "test"}],
		"acp_servers": [{"name": "test", "command": "cmd"}],
		"web": {"auth": {"simple": {"username": "` + username + `", "password": "` + password + `"}}}
	}`
	var req ConfigSaveRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		panic(err)
	}
	return &req
}

// serverWithSimpleAuth returns a Server whose persisted config already contains a
// simple-auth block with the given username/password.
func serverWithSimpleAuth(username, password string) *Server {
	return &Server{
		config: Config{
			MittoConfig: &config.Config{
				Web: config.WebConfig{
					Auth: &config.WebAuth{
						Simple: &config.SimpleAuth{Username: username, Password: password},
					},
				},
			},
		},
	}
}

// Regression test: changing an unrelated setting (e.g. a workspace's ACP server)
// must not be rejected just because the round-tripped simple-auth block has an empty
// password (the backend always redacts the password before sending config to the
// client, and the stored password may live only in the keychain or be absent).
func TestValidateConfigRequest_RoundTripEmptyPasswordExistingBlock(t *testing.T) {
	// Existing config has a partial simple-auth block (username set, no stored password).
	server := serverWithSimpleAuth("admin", "")

	// Frontend round-trips the auth block with an empty (redacted) password.
	req := authConfigBody("admin", "")

	if err := server.validateConfigRequest(req); err != nil {
		t.Fatalf("unexpected error round-tripping existing simple-auth block: %v", err)
	}
}

// When a non-empty password already exists, an empty round-tripped password is still
// accepted (the existing password is preserved by buildNewSettings).
func TestValidateConfigRequest_RoundTripEmptyPasswordExistingPassword(t *testing.T) {
	server := serverWithSimpleAuth("admin", "S0meStoredPass!")
	req := authConfigBody("admin", "")

	if err := server.validateConfigRequest(req); err != nil {
		t.Fatalf("unexpected error round-tripping with stored password: %v", err)
	}
}

// A brand-new simple-auth setup (no pre-existing block) with an empty password must
// still be rejected with "Password is required".
func TestValidateConfigRequest_NewAuthEmptyPasswordRejected(t *testing.T) {
	server := &Server{} // no existing MittoConfig / auth block
	req := authConfigBody("admin", "")

	err := server.validateConfigRequest(req)
	if err == nil {
		t.Fatal("expected error for new simple auth with empty password")
	}
	if err.Message != "Password is required" {
		t.Errorf("Message = %q, want %q", err.Message, "Password is required")
	}
	if err.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusBadRequest)
	}
}

// A brand-new simple-auth setup with a valid password is accepted.
func TestValidateConfigRequest_NewAuthValidPassword(t *testing.T) {
	server := &Server{}
	req := authConfigBody("admin", "Str0ngPassphrase!")

	if err := server.validateConfigRequest(req); err != nil {
		t.Fatalf("unexpected error for new auth with valid password: %v", err)
	}
}

// workspacesOnlyBody is the payload the Workspaces dialog sends: workspaces and ACP
// servers but NO web section. req.Web is therefore nil and external-access auth must
// not be validated or touched.
func workspacesOnlyBody() *ConfigSaveRequest {
	body := `{
		"workspaces": [{"working_dir": "/tmp", "acp_server": "test"}],
		"acp_servers": [{"name": "test", "command": "cmd"}]
	}`
	var req ConfigSaveRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		panic(err)
	}
	return &req
}

// Regression test for the "Password is required" bug: a Workspaces-dialog save omits the
// web section entirely, so it must validate cleanly even when the existing config has a
// partial (username-only) simple-auth block with no stored password.
func TestValidateConfigRequest_OmittedWebPartialAuth(t *testing.T) {
	server := serverWithSimpleAuth("admin", "")
	req := workspacesOnlyBody()
	if req.Web != nil {
		t.Fatalf("expected req.Web to be nil when the web section is omitted")
	}
	if err := server.validateConfigRequest(req); err != nil {
		t.Fatalf("unexpected error validating a workspaces-only save: %v", err)
	}
}

// A workspaces-only save (no web section) is also accepted when there is no existing
// auth configured at all.
func TestValidateConfigRequest_OmittedWebNoExistingAuth(t *testing.T) {
	server := &Server{}
	req := workspacesOnlyBody()
	if err := server.validateConfigRequest(req); err != nil {
		t.Fatalf("unexpected error validating a workspaces-only save: %v", err)
	}
}

// mcpConfigBody is a minimal valid config save request carrying an MCP section
// with the given port. A nil port means the "mcp" section omits the port field
// entirely (so MCPConfig.Port stays nil, i.e. "use the default").
func mcpConfigBody(port *int) *ConfigSaveRequest {
	portField := ""
	if port != nil {
		portField = fmt.Sprintf(`, "port": %d`, *port)
	}
	body := `{
		"workspaces": [{"working_dir": "/tmp", "acp_server": "test"}],
		"acp_servers": [{"name": "test", "command": "cmd"}],
		"mcp": {"host": "127.0.0.1"` + portField + `}
	}`
	var req ConfigSaveRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		panic(err)
	}
	return &req
}

// Port 0 (auto-assigned / random) must be rejected: the MCP address must be
// known in advance so ACP servers can be configured to connect to it.
func TestValidateConfigRequest_MCPPortZeroRejected(t *testing.T) {
	server := &Server{}
	zero := 0
	err := server.validateConfigRequest(mcpConfigBody(&zero))
	if err == nil {
		t.Fatal("expected error for MCP port 0")
	}
	if err.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusBadRequest)
	}
}

// Out-of-range ports are rejected.
func TestValidateConfigRequest_MCPPortOutOfRangeRejected(t *testing.T) {
	server := &Server{}
	tooBig := 70000
	if err := server.validateConfigRequest(mcpConfigBody(&tooBig)); err == nil {
		t.Fatal("expected error for out-of-range MCP port")
	}
}

// A fixed, valid MCP port is accepted.
func TestValidateConfigRequest_MCPPortValid(t *testing.T) {
	server := &Server{}
	port := 5757
	if err := server.validateConfigRequest(mcpConfigBody(&port)); err != nil {
		t.Fatalf("unexpected error for valid MCP port: %v", err)
	}
}

// A nil MCP port (section present, port omitted) is accepted: it means "use the
// default port".
func TestValidateConfigRequest_MCPPortNilAccepted(t *testing.T) {
	server := &Server{}
	if err := server.validateConfigRequest(mcpConfigBody(nil)); err != nil {
		t.Fatalf("unexpected error for nil MCP port: %v", err)
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
	sm := conversation.NewSessionManager("", "", false, nil)
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
	sm := conversation.NewSessionManager("", "", false, nil)
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
