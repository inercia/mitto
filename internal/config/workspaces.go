package config

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

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
	// WorkingDir is the absolute path to the working directory
	WorkingDir string `json:"working_dir"`
	// Name is the optional friendly display name for the workspace
	// If not set, the UI should fall back to displaying the directory basename
	Name string `json:"name,omitempty"`
	// Color is the optional custom color for the workspace badge (e.g., "#ff5500")
	// If not set, a color is automatically generated from the workspace path
	Color string `json:"color,omitempty"`
	// Code is the optional three-letter code for the workspace badge
	// If not set, a code is automatically generated from the workspace path
	Code string `json:"code,omitempty"`
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

	// Ensure all workspaces have UUIDs
	needsSave := false
	for i := range file.Workspaces {
		if file.Workspaces[i].EnsureUUID() {
			needsSave = true
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
