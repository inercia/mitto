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
mcp:
  scopes: ["user", "project"]
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

// TestAgentMetadataDefaults_Parse verifies that a metadata.yaml with a `defaults` block
// is parsed correctly into AgentMetadata.Defaults.
func TestAgentMetadataDefaults_Parse(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "builtin", "fancy-agent")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	meta := `name: "Fancy Agent"
displayName: "Fancy Agent"
acpId: "fancy"
description: "An agent with defaults"
defaults:
  env:
    NODE_OPTIONS: "--max-old-space-size=8192"
    MY_VAR: "hello"
  constraints:
    model:
      matchMode: contains
      pattern: "Opus"
  tags:
    - coding
    - smart
  autoApprove: true
  contextFlushCommand: "/flush"
`
	if err := os.WriteFile(filepath.Join(agentDir, "metadata.yaml"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(agentsDir, nil)
	agent, err := m.GetAgent("fancy-agent", "builtin")
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}

	d := agent.Metadata.Defaults
	if d == nil {
		t.Fatal("expected Defaults to be non-nil")
	}

	// Env
	if got := d.Env["NODE_OPTIONS"]; got != "--max-old-space-size=8192" {
		t.Errorf("Env[NODE_OPTIONS] = %q, want %q", got, "--max-old-space-size=8192")
	}
	if got := d.Env["MY_VAR"]; got != "hello" {
		t.Errorf("Env[MY_VAR] = %q, want %q", got, "hello")
	}

	// Constraints
	mc, ok := d.Constraints["model"]
	if !ok {
		t.Fatal("expected Constraints[model] to be present")
	}
	if mc.MatchMode != "contains" {
		t.Errorf("Constraints[model].MatchMode = %q, want %q", mc.MatchMode, "contains")
	}
	if mc.Pattern != "Opus" {
		t.Errorf("Constraints[model].Pattern = %q, want %q", mc.Pattern, "Opus")
	}

	// Tags
	if len(d.Tags) != 2 || d.Tags[0] != "coding" || d.Tags[1] != "smart" {
		t.Errorf("Tags = %v, want [coding smart]", d.Tags)
	}

	// AutoApprove
	if !d.AutoApprove {
		t.Error("expected AutoApprove to be true")
	}

	// ContextFlushCommand
	if d.ContextFlushCommand != "/flush" {
		t.Errorf("ContextFlushCommand = %q, want %q", d.ContextFlushCommand, "/flush")
	}
}

// TestAgentMetadataDefaults_Absent verifies that agents without a `defaults` block
// still load successfully with Defaults == nil.
func TestAgentMetadataDefaults_Absent(t *testing.T) {
	agentsDir := setupTestAgent(t, "builtin", "test-agent")

	m := NewManager(agentsDir, nil)
	agent, err := m.GetAgent("test-agent", "builtin")
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}

	if agent.Metadata.Defaults != nil {
		t.Errorf("expected Defaults to be nil for agent without defaults block, got %+v", agent.Metadata.Defaults)
	}
}

// TestAgentMetadataDefaults_PartialSections verifies that a `defaults` block with only
// some sub-sections present (tags only) parses correctly without errors.
func TestAgentMetadataDefaults_PartialSections(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "builtin", "partial-agent")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	meta := `name: "Partial Agent"
displayName: "Partial Agent"
acpId: "partial"
defaults:
  tags:
    - coding
    - smart
`
	if err := os.WriteFile(filepath.Join(agentDir, "metadata.yaml"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(agentsDir, nil)
	agent, err := m.GetAgent("partial-agent", "builtin")
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}

	d := agent.Metadata.Defaults
	if d == nil {
		t.Fatal("expected Defaults to be non-nil")
	}
	if len(d.Tags) != 2 || d.Tags[0] != "coding" || d.Tags[1] != "smart" {
		t.Errorf("Tags = %v, want [coding smart]", d.Tags)
	}
	if len(d.Env) != 0 {
		t.Errorf("Env = %v, want empty", d.Env)
	}
	if len(d.Constraints) != 0 {
		t.Errorf("Constraints = %v, want empty", d.Constraints)
	}
	if d.AutoApprove {
		t.Error("AutoApprove should be false when not specified")
	}
}

// TestAgentMetadataDefaults_EmptyMaps verifies that a `defaults` block with explicitly
// empty env, constraints, and tags parses without error and yields empty (not nil) maps.
func TestAgentMetadataDefaults_EmptyMaps(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "builtin", "empty-defaults-agent")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	meta := `name: "Empty Defaults Agent"
displayName: "Empty Defaults Agent"
acpId: "empty-defaults"
defaults:
  env: {}
  constraints: {}
  tags: []
`
	if err := os.WriteFile(filepath.Join(agentDir, "metadata.yaml"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(agentsDir, nil)
	agent, err := m.GetAgent("empty-defaults-agent", "builtin")
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}

	d := agent.Metadata.Defaults
	if d == nil {
		t.Fatal("expected Defaults to be non-nil even with empty maps")
	}
	if len(d.Env) != 0 {
		t.Errorf("Env = %v, want empty", d.Env)
	}
	if len(d.Constraints) != 0 {
		t.Errorf("Constraints = %v, want empty", d.Constraints)
	}
	if len(d.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", d.Tags)
	}
	if d.AutoApprove {
		t.Error("AutoApprove should be false")
	}
}

// TestMCPServer_EnvUnmarshal verifies that the MCPServer.Env field is populated
// when an mcp-list.sh script emits an "env" object, so the value can be surfaced
// by GET /api/workspace-mcp-tools and copied for round-trip into the Add dialog.
func TestMCPServer_EnvUnmarshal(t *testing.T) {
	raw := `{"servers":[{"name":"with-env","command":"node","args":["server.js"],"env":{"API_KEY":"secret","DEBUG":"1"}},{"name":"url-only","url":"http://127.0.0.1:5757/mcp"}]}`

	var out MCPListOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to unmarshal MCPListOutput: %v", err)
	}
	if len(out.Servers) != 2 {
		t.Fatalf("servers = %d, want 2", len(out.Servers))
	}

	withEnv := out.Servers[0]
	if got := withEnv.Env["API_KEY"]; got != "secret" {
		t.Errorf("Env[API_KEY] = %q, want %q", got, "secret")
	}
	if got := withEnv.Env["DEBUG"]; got != "1" {
		t.Errorf("Env[DEBUG] = %q, want %q", got, "1")
	}

	urlOnly := out.Servers[1]
	if urlOnly.Env != nil {
		t.Errorf("expected nil Env for server without env, got %+v", urlOnly.Env)
	}
}

// TestMCPServer_EnvOmitEmpty verifies that an MCPServer with no env vars marshals
// without an "env" key (json:",omitempty"), keeping the listing output clean.
func TestMCPServer_EnvOmitEmpty(t *testing.T) {
	b, err := json.Marshal(MCPServer{Name: "no-env", Command: "node"})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if got := string(b); got != `{"name":"no-env","command":"node"}` {
		t.Errorf("marshaled = %s, want env omitted", got)
	}

	b, err = json.Marshal(MCPServer{Name: "with-env", Env: map[string]string{"K": "v"}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if got := string(b); got != `{"name":"with-env","env":{"K":"v"}}` {
		t.Errorf("marshaled = %s, want env included", got)
	}
}
