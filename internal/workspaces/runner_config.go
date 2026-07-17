package workspaces

// ============================================================================
// Restricted Runner Types
//
// Restricted runners provide sandboxed execution for ACP agents.
// By default, agents run with no restrictions (exec runner).
// Users can opt-in to sandboxing by configuring restricted_runners settings.
//
// Configuration is per-runner-type using WorkspaceRunnerConfig.
// See docs/config/restricted.md for user documentation.
// ============================================================================

// RunnerRestrictions defines the restrictions for a runner.
type RunnerRestrictions struct {
	// AllowNetworking controls network access.
	// WARNING: Setting to false will break network-based MCP servers.
	AllowNetworking *bool `json:"allow_networking,omitempty" yaml:"allow_networking,omitempty"`

	// AllowReadFolders lists folders that can be read (supports variables like $MITTO_WORKING_DIR, $HOME).
	AllowReadFolders []string `json:"allow_read_folders,omitempty" yaml:"allow_read_folders,omitempty"`

	// AllowWriteFolders lists folders that can be written (supports variables).
	AllowWriteFolders []string `json:"allow_write_folders,omitempty" yaml:"allow_write_folders,omitempty"`

	// MergeWithDefaults controls whether to merge with default restrictions.
	MergeWithDefaults *bool `json:"merge_with_defaults,omitempty" yaml:"merge_with_defaults,omitempty"`

	// Docker contains Docker-specific options.
	Docker *DockerRestrictions `json:"docker,omitempty" yaml:"docker,omitempty"`
}

// DockerRestrictions defines Docker-specific restrictions.
type DockerRestrictions struct {
	// Image is the Docker image to use (required for docker runner).
	// The image must contain the agent executable and any MCP servers.
	Image string `json:"image,omitempty" yaml:"image,omitempty"`

	// MemoryLimit is the maximum memory the container can use (e.g., "2g").
	MemoryLimit string `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`

	// CPULimit is the maximum CPU cores the container can use (e.g., "2.0").
	CPULimit string `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
}

// WorkspaceRunnerConfig represents per-runner-type configuration for restricted runners.
// This type is used at all levels: global, per-agent, and per-workspace.
type WorkspaceRunnerConfig struct {
	// Type overrides the runner type for this workspace.
	Type string `json:"type,omitempty" yaml:"type,omitempty"`

	// Restrictions are workspace-specific restrictions.
	Restrictions *RunnerRestrictions `json:"restrictions,omitempty" yaml:"restrictions,omitempty"`

	// MergeStrategy controls how to merge with agent/global config.
	// Options: "extend" (default) - merge with parent config, "replace" - ignore parent config
	MergeStrategy string `json:"merge_strategy,omitempty" yaml:"merge_strategy,omitempty"`
}
