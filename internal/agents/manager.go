package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Manager provides discovery and execution of agent definitions.
type Manager struct {
	agentsDir string
	logger    *slog.Logger
}

// NewManager creates a new agent manager.
// agentsDir is the path to MITTO_DIR/agents/.
func NewManager(agentsDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		agentsDir: agentsDir,
		logger:    logger,
	}
}

// AgentsDir returns the agents directory path.
func (m *Manager) AgentsDir() string {
	return m.agentsDir
}

// ListAgents discovers all agent definitions across all source directories.
// Returns agents sorted by source then directory name.
func (m *Manager) ListAgents() ([]*AgentDefinition, error) {
	if _, err := os.Stat(m.agentsDir); os.IsNotExist(err) {
		return nil, nil
	}

	topEntries, err := os.ReadDir(m.agentsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read agents directory: %w", err)
	}

	var agents []*AgentDefinition

	for _, topEntry := range topEntries {
		if !topEntry.IsDir() || strings.HasPrefix(topEntry.Name(), ".") {
			continue
		}
		source := topEntry.Name()
		sourceDir := filepath.Join(m.agentsDir, source)

		entries, err := os.ReadDir(sourceDir)
		if err != nil {
			m.logger.Warn("failed to read agent source directory",
				"source", source, "error", err)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}

			agentDir := filepath.Join(sourceDir, entry.Name())
			agent, err := m.loadAgent(source, entry.Name(), agentDir)
			if err != nil {
				m.logger.Debug("skipping agent directory",
					"source", source,
					"dir", entry.Name(),
					"error", err)
				continue
			}
			agents = append(agents, agent)
		}
	}

	// Sort by source, then by directory name
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Source != agents[j].Source {
			return agents[i].Source < agents[j].Source
		}
		return agents[i].DirName < agents[j].DirName
	})

	return agents, nil
}

// GetAgent returns a specific agent definition by directory name.
// It searches across all source directories and returns the first match.
// If source is specified, only that source directory is searched.
func (m *Manager) GetAgent(dirName string, source ...string) (*AgentDefinition, error) {
	if len(source) > 0 && source[0] != "" {
		agentDir := filepath.Join(m.agentsDir, source[0], dirName)
		return m.loadAgent(source[0], dirName, agentDir)
	}

	// Search all sources
	agents, err := m.ListAgents()
	if err != nil {
		return nil, err
	}
	for _, a := range agents {
		if a.DirName == dirName {
			return a, nil
		}
	}
	return nil, fmt.Errorf("agent %q not found", dirName)
}

// GetAgentByACPId returns an agent definition by its ACP ID.
// It first tries to match by metadata acpId, then falls back to matching
// by directory name (e.g., ACP type "augment" matches agent dir "augment"
// even though its acpId is "auggie").
func (m *Manager) GetAgentByACPId(acpId string) (*AgentDefinition, error) {
	agents, err := m.ListAgents()
	if err != nil {
		return nil, err
	}
	// Primary: match by acpId metadata
	for _, a := range agents {
		if a.Metadata.ACPId == acpId {
			return a, nil
		}
	}
	// Fallback: match by directory name
	for _, a := range agents {
		if a.DirName == acpId {
			return a, nil
		}
	}
	return nil, fmt.Errorf("agent with acpId %q not found", acpId)
}

// ListAgentNames returns just the directory names of all agents (deduplicated).
func (m *Manager) ListAgentNames() ([]string, error) {
	agents, err := m.ListAgents()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var names []string
	for _, a := range agents {
		if !seen[a.DirName] {
			seen[a.DirName] = true
			names = append(names, a.DirName)
		}
	}
	sort.Strings(names)
	return names, nil
}

// RunCommand executes an agent command script with the given arguments.
// Uses DefaultTimeout if no timeout is set on the context.
func (m *Manager) RunCommand(ctx context.Context, agentName string, command AgentCommand, input interface{}) (*CommandResult, error) {
	agent, err := m.GetAgent(agentName)
	if err != nil {
		return nil, err
	}
	return m.RunCommandForAgent(ctx, agent, command, input)
}

// RunCommandForAgent executes a command for a specific agent definition.
// If input is non-nil, it is marshaled to JSON and piped to the script's stdin.
func (m *Manager) RunCommandForAgent(ctx context.Context, agent *AgentDefinition, command AgentCommand, input interface{}) (*CommandResult, error) {
	cmdPath := agent.CommandPath(command)
	if cmdPath == "" {
		return nil, fmt.Errorf("agent %q does not have command %q", agent.DirName, command)
	}

	// Apply default timeout if none set
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	m.logger.Debug("running agent command",
		"agent", agent.DirName,
		"source", agent.Source,
		"command", string(command),
		"path", cmdPath,
		"has_input", input != nil)

	start := time.Now()

	cmd := exec.CommandContext(ctx, "bash", cmdPath)
	cmd.Dir = agent.Path

	// Pipe JSON input to stdin if provided
	if input != nil {
		inputJSON, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal input: %w", err)
		}
		cmd.Stdin = bytes.NewReader(inputJSON)
	}

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	duration := time.Since(start)

	result := &CommandResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: duration,
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("command timed out after %v", DefaultTimeout)
			result.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Error = err
			result.ExitCode = -1
		}
	}

	m.logger.Debug("agent command completed",
		"agent", agent.DirName,
		"command", string(command),
		"exit_code", result.ExitCode,
		"duration", duration)

	return result, nil
}

// GetStatus is a convenience method that runs status.sh and parses the JSON output.
func (m *Manager) GetStatus(ctx context.Context, agentName string) (*AgentStatus, error) {
	result, err := m.RunCommand(ctx, agentName, CommandStatus, nil)
	if err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, fmt.Errorf("status command failed: %w", result.Error)
	}

	var status AgentStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return nil, fmt.Errorf("failed to parse status JSON: %w (output: %s)", err, result.Stdout)
	}
	return &status, nil
}

// GetAllStatuses runs status.sh for all agents that have it and returns the results.
func (m *Manager) GetAllStatuses(ctx context.Context) (map[string]*AgentStatus, error) {
	agents, err := m.ListAgents()
	if err != nil {
		return nil, err
	}

	statuses := make(map[string]*AgentStatus)
	for _, agent := range agents {
		if !agent.HasCommand(CommandStatus) {
			continue
		}
		status, err := m.GetStatus(ctx, agent.DirName)
		if err != nil {
			m.logger.Warn("failed to get status for agent",
				"agent", agent.DirName, "error", err)
			continue
		}
		statuses[agent.DirName] = status
	}
	return statuses, nil
}

// InstallAgent runs install.sh and parses the JSON output.
func (m *Manager) InstallAgent(ctx context.Context, agentName string) (*InstallOutput, error) {
	result, err := m.RunCommand(ctx, agentName, CommandInstall, nil)
	if err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, fmt.Errorf("install command failed: %w", result.Error)
	}
	var output InstallOutput
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		return nil, fmt.Errorf("failed to parse install output: %w (output: %s)", err, result.Stdout)
	}
	return &output, nil
}

// ListMCPServers runs mcp-list.sh and parses the JSON output.
func (m *Manager) ListMCPServers(ctx context.Context, agentName string, input *MCPListInput) (*MCPListOutput, error) {
	result, err := m.RunCommand(ctx, agentName, CommandMCPList, input)
	if err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, fmt.Errorf("mcp-list command failed: %w", result.Error)
	}
	var output MCPListOutput
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		return nil, fmt.Errorf("failed to parse mcp-list output: %w (output: %s)", err, result.Stdout)
	}
	return &output, nil
}

// InstallMCPServer runs mcp-install.sh and parses the JSON output.
func (m *Manager) InstallMCPServer(ctx context.Context, agentName string, input *MCPInstallInput) (*MCPInstallOutput, error) {
	if input == nil || input.Name == "" {
		return nil, fmt.Errorf("MCP server name is required")
	}
	if input.Command == "" && input.URL == "" {
		return nil, fmt.Errorf("either command or URL is required")
	}
	result, err := m.RunCommand(ctx, agentName, CommandMCPInstall, input)
	if err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, fmt.Errorf("mcp-install command failed: %w", result.Error)
	}
	var output MCPInstallOutput
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		return nil, fmt.Errorf("failed to parse mcp-install output: %w (output: %s)", err, result.Stdout)
	}
	return &output, nil
}

// loadAgent reads metadata.yaml and discovers available commands for a single agent.
func (m *Manager) loadAgent(source, dirName, agentDir string) (*AgentDefinition, error) {
	metaPath := filepath.Join(agentDir, "metadata.yaml")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata.yaml: %w", err)
	}

	var meta AgentMetadata
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata.yaml: %w", err)
	}

	// Discover available commands
	cmdsDir := filepath.Join(agentDir, "cmds")
	var available []AgentCommand
	for _, cmd := range KnownCommands {
		cmdPath := filepath.Join(cmdsDir, string(cmd))
		if info, err := os.Stat(cmdPath); err == nil && !info.IsDir() {
			available = append(available, cmd)
		}
	}

	return &AgentDefinition{
		Metadata:          meta,
		DirName:           dirName,
		Source:            source,
		Path:              agentDir,
		AvailableCommands: available,
	}, nil
}

// IsKnownCommand returns true if the given command string is a known agent command.
func IsKnownCommand(cmd string) bool {
	for _, known := range KnownCommands {
		if string(known) == cmd {
			return true
		}
	}
	return false
}
