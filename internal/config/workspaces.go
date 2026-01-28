package config

import (
	"fmt"
	"os"

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
	// ACPServer is the name of the ACP server (from settings.json)
	ACPServer string `json:"acp_server"`
	// ACPCommand is the shell command to start the ACP server
	ACPCommand string `json:"acp_command"`
	// WorkingDir is the absolute path to the working directory
	WorkingDir string `json:"working_dir"`
	// Color is the optional custom color for the workspace badge (e.g., "#ff5500")
	Color string `json:"color,omitempty"`
}

// WorkspaceID returns a unique identifier for this workspace.
// Currently uses the working directory since one-dir <-> one-ACP is enforced.
func (w *WorkspaceSettings) WorkspaceID() string {
	return w.WorkingDir
}

// LoadWorkspaces loads workspaces from the Mitto data directory.
// Returns nil (not an error) if workspaces.json doesn't exist.
// This allows callers to distinguish between "no file" and "file with errors".
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

// WorkspacesPath returns the path to the workspaces.json file.
// This is a convenience function that delegates to appdir.WorkspacesPath().
func WorkspacesPath() (string, error) {
	return appdir.WorkspacesPath()
}
