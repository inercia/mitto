package config

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// Valid runner types supported by go-restricted-runner
const (
	RunnerTypeExec        = "exec"
	RunnerTypeSandboxExec = "sandbox-exec"
	RunnerTypeFirejail    = "firejail"
	RunnerTypeDocker      = "docker"
)

// ValidRunnerTypes is the list of all valid runner types
var ValidRunnerTypes = []string{
	RunnerTypeExec,
	RunnerTypeSandboxExec,
	RunnerTypeFirejail,
	RunnerTypeDocker,
}

// WorkspacesFile represents the persisted workspaces in JSON format.
// This is stored in the Mitto data directory as workspaces.json.
type WorkspacesFile struct {
	// Workspaces is the list of configured workspaces
	Workspaces []WorkspaceSettings `json:"workspaces"`
}

// WorkspaceSettings is the JSON representation of a workspace.
// It represents an ACP server + working directory pair.
// Each workspace can run its own ACP server instance.
type WorkspaceSettings struct {
	// UUID is a unique identifier for this workspace.
	// Automatically generated if not set when loading or creating a workspace.
	UUID string `json:"uuid,omitempty"`
	// ACPServer is the name of the ACP server (from settings.json)
	ACPServer string `json:"acp_server"`
	// ACPCommand is the shell command to start the ACP server
	ACPCommand string `json:"acp_command"`
	// ACPCwd is the working directory for the ACP server process.
	// If empty, the ACP process inherits the current working directory.
	ACPCwd string `json:"acp_cwd,omitempty"`
	// WorkingDir is the absolute path to the working directory
	WorkingDir string `json:"working_dir"`
	// RestrictedRunner is the runner type to use for this workspace.
	// Options: "exec" (default), "sandbox-exec", "firejail", "docker"
	// This determines which runner type is used when creating sessions in this workspace.
	RestrictedRunner string `json:"restricted_runner,omitempty"`
	// Name is the optional friendly display name for the workspace
	// If not set, the UI should fall back to displaying the directory basename
	Name string `json:"name,omitempty"`
	// Color is the optional custom color for the workspace badge (e.g., "#ff5500")
	// If not set, a color is automatically generated from the workspace path
	Color string `json:"color,omitempty"`
	// Code is the optional three-letter code for the workspace badge
	// If not set, a code is automatically generated from the workspace path
	Code string `json:"code,omitempty"`
	// AutoApprove enables automatic approval of permission requests for sessions in this workspace.
	// When true, all permission requests (file writes, command execution, etc.) are auto-approved.
	// When false or nil, the global auto_approve setting or per-conversation settings apply.
	AutoApprove *bool `json:"auto_approve,omitempty"`
}

// WorkspaceID returns a unique identifier for this workspace.
// Returns the UUID if set, otherwise falls back to the working directory.
func (w *WorkspaceSettings) WorkspaceID() string {
	if w.UUID != "" {
		return w.UUID
	}
	return w.WorkingDir
}

// EnsureUUID ensures the workspace has a UUID.
// If the UUID is empty, a new one is generated.
// Returns true if a new UUID was generated.
func (w *WorkspaceSettings) EnsureUUID() bool {
	if w.UUID == "" {
		w.UUID = uuid.New().String()
		return true
	}
	return false
}

// GetRestrictedRunner returns the runner type for this workspace.
// Returns "exec" if not set or empty (default).
func (w *WorkspaceSettings) GetRestrictedRunner() string {
	if w.RestrictedRunner == "" {
		return RunnerTypeExec
	}
	return w.RestrictedRunner
}

// IsAutoApprove returns whether permission requests should be auto-approved for this workspace.
// Returns nil if not configured (fall back to global/server settings), true if explicitly enabled,
// or false if explicitly disabled.
func (w *WorkspaceSettings) IsAutoApprove() *bool {
	return w.AutoApprove
}

// ValidateRestrictedRunner validates the restricted_runner field.
// Returns an error if the runner type is invalid.
func (w *WorkspaceSettings) ValidateRestrictedRunner() error {
	if w.RestrictedRunner == "" {
		return nil // Empty is valid (defaults to exec)
	}

	for _, validType := range ValidRunnerTypes {
		if w.RestrictedRunner == validType {
			return nil
		}
	}

	return fmt.Errorf("invalid restricted_runner %q: must be one of %v", w.RestrictedRunner, ValidRunnerTypes)
}

// LoadWorkspaces loads workspaces from the Mitto data directory.
// Returns nil (not an error) if workspaces.json doesn't exist.
// This allows callers to distinguish between "no file" and "file with errors".
// Workspaces without UUIDs will have UUIDs generated automatically.
func LoadWorkspaces() ([]WorkspaceSettings, error) {
	workspacesPath, err := appdir.WorkspacesPath()
	if err != nil {
		return nil, err
	}

	// Check if workspaces.json exists
	if _, err := os.Stat(workspacesPath); os.IsNotExist(err) {
		// No workspaces file - return nil (not an error)
		return nil, nil
	}

	// Load workspaces from JSON file
	var file WorkspacesFile
	if err := fileutil.ReadJSON(workspacesPath, &file); err != nil {
		return nil, fmt.Errorf("failed to read workspaces file %s: %w", workspacesPath, err)
	}

	// Ensure all workspaces have UUIDs and validate runner types
	needsSave := false
	for i := range file.Workspaces {
		if file.Workspaces[i].EnsureUUID() {
			needsSave = true
		}

		// Validate restricted_runner field
		if err := file.Workspaces[i].ValidateRestrictedRunner(); err != nil {
			return nil, fmt.Errorf("workspace %q: %w", file.Workspaces[i].WorkspaceID(), err)
		}
	}

	// If any UUIDs were generated, save the file back
	if needsSave {
		if err := SaveWorkspaces(file.Workspaces); err != nil {
			// Log but don't fail - the workspaces are still valid
			// The UUIDs will be re-generated next time if save failed
			_ = err // ignore save error, UUIDs will be re-generated
		}
	}

	return file.Workspaces, nil
}

// SaveWorkspaces saves workspaces to the Mitto data directory.
func SaveWorkspaces(workspaces []WorkspaceSettings) error {
	workspacesPath, err := appdir.WorkspacesPath()
	if err != nil {
		return err
	}

	file := WorkspacesFile{
		Workspaces: workspaces,
	}

	// Use atomic write for safety
	return fileutil.WriteJSONAtomic(workspacesPath, file, 0644)
}
