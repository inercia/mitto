// Package agents provides agent definition management and script execution.
//
// Agent definitions are organized in subdirectories under MITTO_DIR/agents/:
//
//	agents/
//	├── builtin/      (shipped with Mitto, auto-deployed)
//	│   ├── augment/
//	│   │   ├── metadata.yaml
//	│   │   └── cmds/
//	│   │       ├── install.sh
//	│   │       ├── status.sh
//	│   │       ├── mcp-list.sh
//	│   │       └── mcp-install.sh
//	│   ├── claude-code/
//	│   └── ...
//	└── custom/       (user-created)
//	    └── my-agent/
//	        ├── metadata.yaml
//	        └── cmds/
//	            └── status.sh
package agents

import "time"

// AgentCommand represents a well-known agent command script.
type AgentCommand string

const (
	// CommandInstall is the install script for the agent.
	CommandInstall AgentCommand = "install.sh"

	// CommandStatus returns JSON with agent installation status.
	CommandStatus AgentCommand = "status.sh"

	// CommandMCPList lists MCP servers configured for the agent.
	CommandMCPList AgentCommand = "mcp-list.sh"

	// CommandMCPInstall installs an MCP server for the agent.
	CommandMCPInstall AgentCommand = "mcp-install.sh"
)

// KnownCommands is the list of all well-known agent commands.
var KnownCommands = []AgentCommand{
	CommandInstall,
	CommandStatus,
	CommandMCPList,
	CommandMCPInstall,
}

// DefaultTimeout is the default timeout for running agent commands.
const DefaultTimeout = 30 * time.Second

// InstallMethod describes how to install an agent.
type InstallMethod struct {
	Method  string   `yaml:"method" json:"method"` // "npx", "binary", "brew"
	Package string   `yaml:"package,omitempty" json:"package,omitempty"`
	Note    string   `yaml:"note,omitempty" json:"note,omitempty"`
	Args    []string `yaml:"args,omitempty" json:"args,omitempty"`
}

// AgentMetadata holds the parsed content of a metadata.yaml file.
type AgentMetadata struct {
	Name        string         `yaml:"name" json:"name"`
	DisplayName string         `yaml:"displayName" json:"display_name"`
	ACPId       string         `yaml:"acpId" json:"acp_id"`
	Description string         `yaml:"description" json:"description"`
	Website     string         `yaml:"website,omitempty" json:"website,omitempty"`
	Repository  string         `yaml:"repository,omitempty" json:"repository,omitempty"`
	License     string         `yaml:"license,omitempty" json:"license,omitempty"`
	Install     *InstallMethod `yaml:"install,omitempty" json:"install,omitempty"`
}

// AgentDefinition represents a fully resolved agent definition with its
// metadata and filesystem location.
type AgentDefinition struct {
	// Metadata parsed from metadata.yaml
	Metadata AgentMetadata `json:"metadata"`

	// DirName is the directory name (e.g., "augment", "claude-code")
	DirName string `json:"dir_name"`

	// Source is the parent directory name (e.g., "builtin", "custom")
	Source string `json:"source"`

	// Path is the absolute path to the agent's directory
	Path string `json:"path"`

	// AvailableCommands lists which command scripts exist for this agent
	AvailableCommands []AgentCommand `json:"available_commands"`
}

// HasCommand returns true if the agent has the given command script.
func (a *AgentDefinition) HasCommand(cmd AgentCommand) bool {
	for _, c := range a.AvailableCommands {
		if c == cmd {
			return true
		}
	}
	return false
}

// CommandPath returns the full filesystem path to a command script.
// Returns empty string if the command is not available.
func (a *AgentDefinition) CommandPath(cmd AgentCommand) string {
	if !a.HasCommand(cmd) {
		return ""
	}
	return a.Path + "/cmds/" + string(cmd)
}

// AgentStatus represents the JSON output from status.sh.
type AgentStatus struct {
	Installed      bool   `json:"installed"`
	Version        string `json:"version"`
	Command        string `json:"command"`
	Path           string `json:"path"`
	MCPConfigFound bool   `json:"mcp_config_found"`
	MCPConfigPath  string `json:"mcp_config_path"`
}

// CommandInput is a generic map for JSON input to agent commands.
// Each command defines its own expected fields.
type CommandInput map[string]interface{}

// MCPInstallInput is the expected JSON input for mcp-install.sh.
type MCPInstallInput struct {
	// Name is the MCP server name
	Name string `json:"name"`
	// Command is the command to run the MCP server (mutually exclusive with URL)
	Command string `json:"command,omitempty"`
	// Args are the arguments for the command
	Args []string `json:"args,omitempty"`
	// URL is the URL for SSE-based MCP servers (mutually exclusive with Command)
	URL string `json:"url,omitempty"`
	// Path is an optional workspace path for project-local MCP config
	Path string `json:"path,omitempty"`
}

// MCPListInput is the expected JSON input for mcp-list.sh.
type MCPListInput struct {
	// Path is an optional workspace path to check for project-local MCP config
	Path string `json:"path,omitempty"`
}

// MCPServer represents a single MCP server entry returned by mcp-list.sh.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// MCPListOutput is the expected JSON output from mcp-list.sh.
type MCPListOutput struct {
	Servers []MCPServer `json:"servers"`
}

// InstallOutput is the expected JSON output from install.sh.
type InstallOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Version string `json:"version,omitempty"`
}

// MCPInstallOutput is the expected JSON output from mcp-install.sh.
type MCPInstallOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Name    string `json:"name,omitempty"`
}

// CommandResult holds the result of running an agent command.
type CommandResult struct {
	// ExitCode is the process exit code
	ExitCode int `json:"exit_code"`

	// Stdout is the captured standard output
	Stdout string `json:"stdout"`

	// Stderr is the captured standard error
	Stderr string `json:"stderr"`

	// Duration is how long the command took
	Duration time.Duration `json:"duration"`

	// Error is set if the command failed to execute (not exit code)
	Error error `json:"-"`
}

// Success returns true if the command exited with code 0 and no execution error.
func (r *CommandResult) Success() bool {
	return r.Error == nil && r.ExitCode == 0
}
