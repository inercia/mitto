package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestAgent(t *testing.T, source, name string) string {
	t.Helper()
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, source, name)
	cmdsDir := filepath.Join(agentDir, "cmds")
	if err := os.MkdirAll(cmdsDir, 0755); err != nil {
		t.Fatalf("failed to create cmds dir: %v", err)
	}

	// Write metadata.yaml
	meta := `name: "Test Agent"
displayName: "Test Agent"
acpId: "test-agent"
description: "A test agent"
`
	if err := os.WriteFile(filepath.Join(agentDir, "metadata.yaml"), []byte(meta), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}

	// Write status.sh
	statusScript := `#!/bin/bash
cat <<EOF
{
  "installed": true,
  "version": "1.0.0",
  "command": "test-cmd",
  "path": "/usr/bin/test-cmd",
  "mcp_config_found": false,
  "mcp_config_path": "/tmp/test-config.json"
}
EOF
`
	if err := os.WriteFile(filepath.Join(cmdsDir, "status.sh"), []byte(statusScript), 0755); err != nil {
		t.Fatalf("failed to write status.sh: %v", err)
	}

	// Write install.sh
	installScript := `#!/bin/bash
echo '{"success": true, "message": "Installing test agent..."}'
`
	if err := os.WriteFile(filepath.Join(cmdsDir, "install.sh"), []byte(installScript), 0755); err != nil {
		t.Fatalf("failed to write install.sh: %v", err)
	}

	return agentsDir
}

func TestManagerListAgents(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")

	// Add a second agent in a different source
	customDir := filepath.Join(agentsDir, "custom", "my-agent")
	cmdsDir := filepath.Join(customDir, "cmds")
	os.MkdirAll(cmdsDir, 0755)
	meta := `name: "My Agent"
displayName: "My Custom Agent"
acpId: "my-agent"
description: "A custom agent"
`
	os.WriteFile(filepath.Join(customDir, "metadata.yaml"), []byte(meta), 0644)

	m := NewManager(agentsDir, nil)
	agents, err := m.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}

	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}

	// Should be sorted: builtin/test-agent, custom/my-agent
	if agents[0].Source != "builtin" || agents[0].DirName != "test-agent" {
		t.Errorf("first agent = %s/%s, want builtin/test-agent", agents[0].Source, agents[0].DirName)
	}
	if agents[1].Source != "custom" || agents[1].DirName != "my-agent" {
		t.Errorf("second agent = %s/%s, want custom/my-agent", agents[1].Source, agents[1].DirName)
	}
}

func TestManagerGetAgent(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	agent, err := m.GetAgent("test-agent")
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}

	if agent.Metadata.Name != "Test Agent" {
		t.Errorf("name = %q, want %q", agent.Metadata.Name, "Test Agent")
	}
	if agent.Metadata.ACPId != "test-agent" {
		t.Errorf("acpId = %q, want %q", agent.Metadata.ACPId, "test-agent")
	}
	if agent.Source != "builtin" {
		t.Errorf("source = %q, want %q", agent.Source, "builtin")
	}
}

func TestManagerGetAgentByACPId(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	agent, err := m.GetAgentByACPId("test-agent")
	if err != nil {
		t.Fatalf("GetAgentByACPId failed: %v", err)
	}
	if agent.DirName != "test-agent" {
		t.Errorf("dirName = %q, want %q", agent.DirName, "test-agent")
	}
}

func TestManagerGetAgentNotFound(t *testing.T) {
	agentsDir := t.TempDir()
	m := NewManager(agentsDir, nil)

	_, err := m.GetAgent("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}

func TestAgentDefinitionHasCommand(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	agent, err := m.GetAgent("test-agent")
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}

	if !agent.HasCommand(CommandStatus) {
		t.Error("expected agent to have status command")
	}
	if !agent.HasCommand(CommandInstall) {
		t.Error("expected agent to have install command")
	}
	if agent.HasCommand(CommandMCPList) {
		t.Error("expected agent to NOT have mcp-list command")
	}
	if agent.HasCommand(CommandMCPInstall) {
		t.Error("expected agent to NOT have mcp-install command")
	}
}

func TestManagerRunCommand(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	ctx := context.Background()
	result, err := m.RunCommand(ctx, "test-agent", CommandInstall, nil)
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !result.Success() {
		t.Errorf("expected success, got exit_code=%d error=%v", result.ExitCode, result.Error)
	}
	var output InstallOutput
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		t.Fatalf("failed to parse install output: %v (stdout: %s)", err, result.Stdout)
	}
	if !output.Success {
		t.Errorf("expected success=true")
	}
}

func TestManagerRunCommandNotFound(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	ctx := context.Background()
	_, err := m.RunCommand(ctx, "test-agent", CommandMCPList, nil)
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestManagerGetStatus(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	ctx := context.Background()
	status, err := m.GetStatus(ctx, "test-agent")
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}

	if !status.Installed {
		t.Error("expected installed=true")
	}
	if status.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", status.Version, "1.0.0")
	}
	if status.Command != "test-cmd" {
		t.Errorf("command = %q, want %q", status.Command, "test-cmd")
	}
	if status.Path != "/usr/bin/test-cmd" {
		t.Errorf("path = %q, want %q", status.Path, "/usr/bin/test-cmd")
	}
}

func TestManagerRunCommandTimeout(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "builtin", "slow-agent")
	cmdsDir := filepath.Join(agentDir, "cmds")
	os.MkdirAll(cmdsDir, 0755)

	os.WriteFile(filepath.Join(agentDir, "metadata.yaml"), []byte(`name: "Slow"
displayName: "Slow Agent"
acpId: "slow"
description: "Slow agent"
`), 0644)

	// Script that sleeps — use exec to replace bash with sleep so signal is delivered directly
	os.WriteFile(filepath.Join(cmdsDir, "status.sh"), []byte(`#!/bin/bash
exec sleep 30
`), 0755)

	m := NewManager(agentsDir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := m.RunCommand(ctx, "slow-agent", CommandStatus, nil)
	if err != nil {
		t.Fatalf("RunCommand returned error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected timeout error in result")
	}
}

func TestListAgentNames(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	names, err := m.ListAgentNames()
	if err != nil {
		t.Fatalf("ListAgentNames failed: %v", err)
	}
	if len(names) != 1 || names[0] != "test-agent" {
		t.Errorf("names = %v, want [test-agent]", names)
	}
}

func TestIsKnownCommand(t *testing.T) {
	if !IsKnownCommand("status.sh") {
		t.Error("status.sh should be a known command")
	}
	if IsKnownCommand("unknown.sh") {
		t.Error("unknown.sh should not be a known command")
	}
}

func TestManagerEmptyDir(t *testing.T) {
	agentsDir := filepath.Join(t.TempDir(), "nonexistent")
	m := NewManager(agentsDir, nil)

	agents, err := m.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents on nonexistent dir should not error: %v", err)
	}
	if agents != nil {
		t.Errorf("expected nil agents, got %d", len(agents))
	}
}

func TestAgentCommandPath(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")
	m := NewManager(agentsDir, nil)

	agent, _ := m.GetAgent("test-agent")

	path := agent.CommandPath(CommandStatus)
	if path == "" {
		t.Fatal("expected non-empty path for status command")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("command path does not exist: %v", err)
	}

	path = agent.CommandPath(CommandMCPList)
	if path != "" {
		t.Error("expected empty path for unavailable command")
	}
}

func TestManagerRunCommandWithInput(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "builtin", "input-agent")
	cmdsDir := filepath.Join(agentDir, "cmds")
	os.MkdirAll(cmdsDir, 0755)

	os.WriteFile(filepath.Join(agentDir, "metadata.yaml"), []byte(`name: "Input Agent"
displayName: "Input Agent"
acpId: "input-agent"
description: "Agent that reads input"
`), 0644)

	// Script that reads JSON from stdin and echoes it back
	os.WriteFile(filepath.Join(cmdsDir, "mcp-install.sh"), []byte(`#!/bin/bash
INPUT=$(cat)
NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null)
echo "{\"success\": true, \"message\": \"Installed $NAME\", \"name\": \"$NAME\"}"
`), 0755)

	m := NewManager(agentsDir, nil)
	ctx := context.Background()

	input := &MCPInstallInput{
		Name:    "my-mcp-server",
		Command: "/usr/bin/my-server",
	}

	result, err := m.RunCommand(ctx, "input-agent", CommandMCPInstall, input)
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !result.Success() {
		t.Errorf("expected success, got exit_code=%d error=%v stderr=%s", result.ExitCode, result.Error, result.Stderr)
	}

	var output MCPInstallOutput
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		t.Fatalf("failed to parse output: %v (stdout: %s)", err, result.Stdout)
	}
	if !output.Success {
		t.Error("expected success=true")
	}
	if output.Name != "my-mcp-server" {
		t.Errorf("name = %q, want %q", output.Name, "my-mcp-server")
	}
}
