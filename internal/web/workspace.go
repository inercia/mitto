package web

// WorkspaceConfig represents an ACP server + working directory pair.
// Each workspace can run its own ACP server instance.
type WorkspaceConfig struct {
	// ACPServer is the name of the ACP server (from .mittorc config)
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
func (w *WorkspaceConfig) WorkspaceID() string {
	return w.WorkingDir
}
