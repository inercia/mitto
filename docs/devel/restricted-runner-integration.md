# Restricted Runner Integration Plan

## Overview

This document outlines the plan for integrating [go-restricted-runner](https://github.com/inercia/go-restricted-runner) into Mitto to sandbox agent execution. This will allow fine-grained control over what resources agents can access.

**Library Version**: Latest (with RunWithPipes support)
**Repository**: https://github.com/inercia/go-restricted-runner

## Executive Summary

**Key Points:**

1. **Default Behavior**: By default, Mitto uses the `exec` runner (no restrictions). Users must explicitly opt-in to sandboxing.

2. **MCP Server Compatibility**: ⚠️ Restricted execution can break MCP server access. Agents may lose access to MCP servers if filesystem/network restrictions are too strict.

3. **ACP Communication**: All runners must preserve stdin/stdout pipes for ACP protocol (JSON-RPC over stdio). No network ports are used.

4. **Docker Requirements**:
   - Agent must be installed in the container image
   - MCP servers must be installed in the image (if used)
   - No port exposure needed (ACP uses stdio, not network)
   - Workspace is auto-mounted at the same path

5. **Configuration Hierarchy**: Global defaults → Per-agent config → Workspace overrides

6. **Variable Substitution**: Supports `$WORKSPACE`, `$HOME`, `$MITTO_DIR`, `$USER`, `$TMPDIR` (resolved at runtime)

**Recommendation**: For most users, stick with the default `exec` runner. Use restricted runners only for:

- Testing untrusted agents
- Highly controlled environments
- Agents that don't require MCP servers

## Goals

1. **Sandboxed Execution**: Run ACP agents through a restricted runner instead of directly
2. **Flexible Configuration**: Per-agent and per-workspace restriction configurations
3. **Variable Substitution**: Support for runtime variable resolution (e.g., `$WORKSPACE`, `$HOME`)
4. **Configuration Hierarchy**: Global → Per-Agent → Per-Workspace overrides
5. **Backend-Only Changes**: No UI changes required at this stage

## Important Considerations

### Default Behavior

**By default, Mitto uses the `exec` runner (no restrictions).** This ensures:

- Backward compatibility with existing configurations
- No breaking changes for current users
- Agents work out-of-the-box without additional setup

Users must explicitly opt-in to restricted execution by configuring `restricted_runner` settings.

### MCP Server Access

**⚠️ WARNING: Restricted execution can break MCP server access.**

When using restricted runners (sandbox-exec, firejail, docker), agents may lose access to MCP servers because:

1. **Network Restrictions**: If `allow_networking: false`, agents cannot connect to remote MCP servers
2. **Filesystem Restrictions**: Agents may not be able to access MCP server executables or configuration files
3. **Process Restrictions**: Some runners may prevent spawning child processes (MCP servers)

**Recommendations:**

- If your agent uses MCP servers, use `exec` runner or carefully configure allowed paths
- For sandbox-exec/firejail: Ensure MCP server paths are in `allow_read_folders`
- For docker: MCP servers must be installed in the container image

### ACP Client Communication

**The restricted runner must allow the ACP client (Mitto) to communicate with the agent.**

All runners must ensure:

- **stdin/stdout pipes remain accessible** - ACP protocol uses JSON-RPC over stdio
- **No interference with stdio** - Runners should not capture or redirect stdio
- **Process lifecycle management** - Mitto must be able to start/stop the agent process

The go-restricted-runner library handles this correctly for exec, sandbox-exec, and firejail runners.

### Docker Runner Special Requirements

**Docker runner has additional requirements:**

1. **Agent must be installed in the image**:

   ```yaml
   restricted_runner:
     type: "docker"
     restrictions:
       docker:
         image: "my-agent-image:latest" # Must contain the agent executable
   ```

2. **No port exposure needed**: ACP uses stdio, not network ports
   - The agent runs inside the container
   - Mitto communicates via docker exec stdin/stdout
   - No `-p` port mapping required

3. **Workspace mounting**:
   - The workspace directory is automatically mounted into the container
   - Mounted at the same path to preserve absolute paths
   - Example: `/Users/user/project` → `/Users/user/project` in container

4. **MCP servers in Docker**:
   - MCP servers must be installed in the image
   - Or mounted from the host (requires additional volume mounts)
   - Network-based MCP servers require `allow_networking: true`

**Example Docker setup:**

```dockerfile
# Dockerfile for restricted agent
FROM alpine:latest

# Install agent and dependencies
RUN apk add --no-cache nodejs npm
RUN npm install -g @zed-industries/claude-code-acp

# Install any MCP servers the agent needs
RUN npm install -g @modelcontextprotocol/server-filesystem

ENTRYPOINT ["claude-code-acp"]
```

```yaml
# Mitto configuration
acp:
  - name: claude-code
    command: claude-code-acp # This runs inside the container
    restricted_runner:
      type: "docker"
      restrictions:
        allow_networking: true # If agent needs network access
        docker:
          image: "my-claude-agent:latest"
          memory_limit: "2g"
```

## Architecture

### Package Structure

Create a new package `internal/runner` to handle restricted runner integration:

```
internal/runner/
├── runner.go           # Main runner interface and factory
├── config.go           # Configuration types and parsing
├── variables.go        # Variable substitution logic
├── exec.go             # Wrapper for go-restricted-runner
└── runner_test.go      # Unit tests
```

### Integration Points

The restricted runner will be integrated at the ACP process execution layer:

1. **`internal/acp/connection.go`** - Modify `NewConnection()` to use restricted runner
2. **`internal/auxiliary/manager.go`** - Modify auxiliary session startup
3. **`internal/web/background_session.go`** - Modify `startACPProcess()`

## Configuration Schema

### 1. Global Configuration (settings.json / .mittorc)

```yaml
# Global per-runner-type configuration
# NOTE: By default (if this section is omitted), Mitto uses "exec" runner with no restrictions
restricted_runners:
  sandbox-exec:
    # Applied when any agent uses sandbox-exec runner
    restrictions:
      allow_networking: true # Required for network-based MCP servers
      allow_read_folders:
        - "$WORKSPACE"
        - "$HOME/.config" # May be needed for MCP server configs
        - "$HOME/.local" # May be needed for MCP server executables
      allow_write_folders:
        - "$WORKSPACE"
      deny_folders:
        - "$HOME/.ssh" # Prevent access to SSH keys
        - "$HOME/.aws" # Prevent access to AWS credentials
    merge_strategy: "extend"

  docker:
    # Applied when any agent uses docker runner
    restrictions:
      allow_networking: false
      docker:
        image: "default:latest"
    merge_strategy: "replace"

# Per-ACP-server configuration
acp_servers:
  - name: auggie
    command: auggie --acp --allow-indexing
    # Agent-specific per-runner-type configuration (optional)
    restricted_runners:
      sandbox-exec:
        # Applied when this agent uses sandbox-exec runner
        restrictions:
          allow_networking: false # WARNING: Breaks network-based MCP servers
          allow_read_folders:
            - "$WORKSPACE"
            - "$HOME/.augment" # Auggie config directory
            - "$HOME/.local/bin" # May contain MCP server executables
          allow_write_folders:
            - "$WORKSPACE"
            - "$HOME/.augment"
        merge_strategy: "extend"

  - name: experimental
    command: experimental-agent --acp
    restricted_runners:
      docker:
        # Applied when this agent uses docker runner
        restrictions:
          allow_networking: false
          docker:
            image: "experimental:latest"
        merge_strategy: "replace"

  - name: claude-code
    command: npx -y @zed-industries/claude-code-acp@latest
    restricted_runners:
      firejail:
        # Applied when this agent uses firejail runner
        restrictions:
          allow_networking: true # Allow network for MCP servers
          allow_read_folders:
            - "$WORKSPACE"
            - "$HOME/.npm" # npm cache for npx
            - "$HOME/.config" # May contain MCP configs
          allow_write_folders:
            - "$WORKSPACE"
        merge_strategy: "extend"

  - name: docker-agent
    command: my-agent --acp
    restricted_runners:
      docker:
        # Applied when this agent uses docker runner
        restrictions:
          allow_networking: true
          docker:
            image: "my-agent:latest" # Must contain agent + MCP servers
            memory_limit: "2g"
            cpu_limit: "2.0"
        merge_strategy: "replace"
```

**Legacy format** (deprecated but still supported):

```yaml
restricted_runner:
  default_type: "sandbox-exec"
  default_restrictions:
    allow_networking: true

acp:
  - auggie:
      command: auggie --acp
      restricted_runner:
        type: "sandbox-exec"
        restrictions:
          allow_networking: false
```

### 2. Workspace Configuration (.mittorc)

Configuration is **per runner type**. When a workspace uses a runner of type X, it applies the config for type X.

```yaml
# .mittorc - per-runner-type workspace overrides
restricted_runners:
  exec:
    # Applied when any workspace in this folder uses exec runner
    restrictions:
      allow_read_folders:
        - "$WORKSPACE"
    merge_strategy: "extend"

  sandbox-exec:
    # Applied when any workspace in this folder uses sandbox-exec runner
    restrictions:
      allow_networking: false
      allow_read_folders:
        - "$WORKSPACE"
        - "$WORKSPACE/vendor"
    merge_strategy: "extend"

  docker:
    # Applied when any workspace in this folder uses docker runner
    restrictions:
      allow_networking: false
      docker:
        image: "alpine:latest"
    merge_strategy: "replace"
```

**Why Per-Runner-Type?**

Multiple workspaces can use the same folder with different agents/configurations:

- Workspace 1: `/path/to/project` + agent with `exec` → uses `restricted_runners.exec`
- Workspace 2: `/path/to/project` + agent with `docker` → uses `restricted_runners.docker`

This allows workspace-specific overrides based on the runner type being used, not the agent name.

## Configuration Resolution Order

When a workspace uses a runner of type X, configuration is resolved in this order (highest priority last):

1. **Global per-runner-type**: `settings.json` → `restricted_runners[X]`
2. **Legacy global defaults**: `settings.json` → `restricted_runner.default_restrictions` (deprecated)
3. **Agent per-runner-type**: `settings.json` → `acp_servers[].restricted_runners[X]`
4. **Legacy agent config**: `settings.json` → `acp_servers[].restricted_runner.restrictions` (deprecated)
5. **Workspace per-runner-type**: `.mittorc` → `restricted_runners[X]`

Each level can either:

- **Extend** the previous level (merge restrictions) - `merge_strategy: "extend"`
- **Replace** the previous level (ignore parent config) - `merge_strategy: "replace"`

### Example Resolution

**Global** (`settings.json`):

```yaml
restricted_runners:
  sandbox-exec:
    restrictions:
      allow_networking: true
      allow_read_folders: ["$WORKSPACE"]
```

**Agent** (`settings.json`):

```yaml
acp_servers:
  - name: experimental
    command: experimental --acp
    restricted_runners:
      sandbox-exec:
        restrictions:
          allow_networking: false # Override
          allow_read_folders: ["$HOME/.experimental"] # Add
        merge_strategy: "extend"
```

**Workspace** (`.mittorc`):

```yaml
restricted_runners:
  sandbox-exec:
    restrictions:
      allow_read_folders: ["$WORKSPACE/vendor"] # Add
    merge_strategy: "extend"
```

**Result** for `experimental` agent with `sandbox-exec`:

- Networking: `false` (from agent)
- Read folders: `["$WORKSPACE", "$HOME/.experimental", "$WORKSPACE/vendor"]` (merged)

## Per-Agent Configuration Resolution

### How It Works

When creating a runner for a session, Mitto resolves the configuration in this order:

1. **Global per-runner-type** from `Config.RestrictedRunners`
2. **Agent per-runner-type config** from `ACPServer.RestrictedRunners` (looked up by agent name)
3. **Workspace per-runner-type overrides** from `.mittorc` `RestrictedRunners` section

The resolution happens in `SessionManager.createRunner()`:

```go
func (sm *SessionManager) createRunner(workingDir, acpServer string) (*runner.Runner, error) {
    // 1. Get workspace-specific configs from .mittorc (by runner type)
    var workspaceRunnerConfigByType map[string]*config.WorkspaceRunnerConfig
    if rc, err := sm.workspaceRCCache.Get(workingDir); err == nil && rc != nil {
        workspaceRunnerConfigByType = rc.RestrictedRunners
    }

    // 2. Get global configs
    sm.mu.RLock()
    globalRunnersByType := sm.globalRestrictedRunners
    mittoConfig := sm.mittoConfig
    sm.mu.RUnlock()

    // 3. Get agent-specific config from MittoConfig (settings.json)
    var agentRunnersByType map[string]*config.WorkspaceRunnerConfig
    if mittoConfig != nil && acpServer != "" {
        if server, err := mittoConfig.GetServer(acpServer); err == nil {
            agentRunnersByType = server.RestrictedRunners
        }
    }

    // 4. Create runner with hierarchy:
    //    Global[runnerType] → Agent[runnerType] → Workspace[runnerType]
    return runner.NewRunner(
        globalRunnersByType,
        agentRunnersByType,
        workspaceRunnerConfigByType,
        workingDir,
        logger,
    )
}
```

### Example Resolution

**Global config** (`settings.json`):

```yaml
restricted_runner:
  default_type: "exec"
  default_restrictions:
    allow_networking: true
```

**Agent config** (in `settings.json`):

```yaml
acp:
  - experimental:
      command: experimental-agent --acp
      restricted_runner:
        type: "sandbox-exec"
        restrictions:
          allow_networking: false
          allow_read_folders: ["$WORKSPACE"]
```

**Workspace config** (`.mittorc` in `/path/to/project`):

```yaml
restricted_runner:
  restrictions:
    allow_read_folders: ["$WORKSPACE", "$HOME/.cache"]
  merge_strategy: "extend"
```

**Result** for `experimental` agent in `/path/to/project`:

- Type: `sandbox-exec` (from agent config)
- Networking: `false` (from agent config)
- Read folders: `["$WORKSPACE", "$HOME/.cache"]` (merged from agent + workspace)

## Configuration Types

### Go Struct Definitions

```go
// RunnerRestrictions defines the restrictions for a runner
type RunnerRestrictions struct {
    // AllowNetworking controls network access
    AllowNetworking *bool `json:"allow_networking,omitempty" yaml:"allow_networking,omitempty"`

    // AllowReadFolders lists folders that can be read (supports variables)
    AllowReadFolders []string `json:"allow_read_folders,omitempty" yaml:"allow_read_folders,omitempty"`

    // AllowWriteFolders lists folders that can be written (supports variables)
    AllowWriteFolders []string `json:"allow_write_folders,omitempty" yaml:"allow_write_folders,omitempty"`

    // DenyFolders lists folders that are explicitly denied (supports variables)
    DenyFolders []string `json:"deny_folders,omitempty" yaml:"deny_folders,omitempty"`

    // MergeWithDefaults controls whether to merge with default restrictions
    MergeWithDefaults *bool `json:"merge_with_defaults,omitempty" yaml:"merge_with_defaults,omitempty"`

    // Docker-specific options
    Docker *DockerRestrictions `json:"docker,omitempty" yaml:"docker,omitempty"`
}

// DockerRestrictions defines Docker-specific restrictions
type DockerRestrictions struct {
    Image       string `json:"image,omitempty" yaml:"image,omitempty"`
    MemoryLimit string `json:"memory_limit,omitempty" yaml:"memory_limit,omitempty"`
    CPULimit    string `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
}

// WorkspaceRunnerConfig represents per-runner-type configuration for restricted runners.
// This type is used at all levels: global, per-agent, and per-workspace.
type WorkspaceRunnerConfig struct {
    // Type overrides the runner type
    Type string `json:"type,omitempty" yaml:"type,omitempty"`

    // Restrictions are the runner restrictions
    Restrictions *RunnerRestrictions `json:"restrictions,omitempty" yaml:"restrictions,omitempty"`

    // MergeStrategy controls how to merge with parent config
    // Options: "extend" (default) - merge with parent config, "replace" - ignore parent config
    MergeStrategy string `json:"merge_strategy,omitempty" yaml:"merge_strategy,omitempty"`
}
```

### Updated ACPServer Type

```go
// ACPServer represents a single ACP server configuration.
type ACPServer struct {
    Name     string
    Command  string
    Prompts  []WebPrompt

    // RestrictedRunners contains per-runner-type configuration for this agent
    RestrictedRunners map[string]*WorkspaceRunnerConfig `json:"restricted_runners,omitempty" yaml:"restricted_runners,omitempty"`
}
```

### Updated WorkspaceRC Type

```go
// WorkspaceRC represents workspace-specific configuration loaded from .mittorc.
type WorkspaceRC struct {
    Prompts       []WebPrompt
    PromptsDirs   []string
    Conversations *ConversationsConfig

    // RestrictedRunners contains per-runner-type workspace overrides
    RestrictedRunners map[string]*WorkspaceRunnerConfig `json:"restricted_runners,omitempty" yaml:"restricted_runners,omitempty"`

    // ... existing fields ...
}
```

## Variable Substitution

### Supported Variables

| Variable     | Description                 | Example Value                         |
| ------------ | --------------------------- | ------------------------------------- |
| `$WORKSPACE` | Current workspace directory | `/Users/user/project`                 |
| `$HOME`      | User's home directory       | `/Users/user`                         |
| `$MITTO_DIR` | Mitto data directory        | `~/Library/Application Support/Mitto` |
| `$USER`      | Current username            | `user`                                |
| `$TMPDIR`    | System temp directory       | `/tmp`                                |

### Variable Resolution Logic

```go
// VariableResolver handles variable substitution in paths
type VariableResolver struct {
    workspace string
    home      string
    mittoDir  string
    user      string
    tmpDir    string
}

// NewVariableResolver creates a resolver with runtime values
func NewVariableResolver(workspace string) (*VariableResolver, error) {
    home, _ := os.UserHomeDir()
    mittoDir, _ := appdir.Dir()
    user := os.Getenv("USER")
    tmpDir := os.TempDir()

    return &VariableResolver{
        workspace: workspace,
        home:      home,
        mittoDir:  mittoDir,
        user:      user,
        tmpDir:    tmpDir,
    }, nil
}

// Resolve replaces variables in a path
func (vr *VariableResolver) Resolve(path string) string {
    path = strings.ReplaceAll(path, "$WORKSPACE", vr.workspace)
    path = strings.ReplaceAll(path, "${WORKSPACE}", vr.workspace)
    path = strings.ReplaceAll(path, "$HOME", vr.home)
    path = strings.ReplaceAll(path, "${HOME}", vr.home)
    path = strings.ReplaceAll(path, "$MITTO_DIR", vr.mittoDir)
    path = strings.ReplaceAll(path, "${MITTO_DIR}", vr.mittoDir)
    path = strings.ReplaceAll(path, "$USER", vr.user)
    path = strings.ReplaceAll(path, "${USER}", vr.user)
    path = strings.ReplaceAll(path, "$TMPDIR", vr.tmpDir)
    path = strings.ReplaceAll(path, "${TMPDIR}", vr.tmpDir)

    // Expand ~ to home directory
    if strings.HasPrefix(path, "~/") {
        path = filepath.Join(vr.home, path[2:])
    }

    return path
}

// ResolvePaths resolves variables in a list of paths
func (vr *VariableResolver) ResolvePaths(paths []string) []string {
    resolved := make([]string, len(paths))
    for i, path := range paths {
        resolved[i] = vr.Resolve(path)
    }
    return resolved
}
```

## Configuration Hierarchy and Merging

### Merge Strategy

The configuration is resolved in the following order (highest priority last):

1. **Global defaults** (`restricted_runner.default_restrictions`)
2. **Per-agent config** (`acp[].restricted_runner`)
3. **Workspace overrides** (`.mittorc` → `restricted_runner`)

### Merge Logic

```go
// MergeRestrictions merges restrictions with the specified strategy
func MergeRestrictions(base, override *RunnerRestrictions, strategy string) *RunnerRestrictions {
    if override == nil {
        return base
    }

    if strategy == "replace" {
        return override
    }

    // Default: "extend" strategy
    merged := &RunnerRestrictions{}

    if base != nil {
        *merged = *base // Copy base
    }

    // Override specific fields
    if override.AllowNetworking != nil {
        merged.AllowNetworking = override.AllowNetworking
    }

    // Merge folder lists (append unique entries)
    merged.AllowReadFolders = mergeFolderLists(
        base.AllowReadFolders,
        override.AllowReadFolders,
    )
    merged.AllowWriteFolders = mergeFolderLists(
        base.AllowWriteFolders,
        override.AllowWriteFolders,
    )
    merged.DenyFolders = mergeFolderLists(
        base.DenyFolders,
        override.DenyFolders,
    )

    // Docker config: override completely if specified
    if override.Docker != nil {
        merged.Docker = override.Docker
    }

    return merged
}

func mergeFolderLists(base, override []string) []string {
    if len(override) == 0 {
        return base
    }

    seen := make(map[string]bool)
    result := make([]string, 0, len(base)+len(override))

    for _, path := range base {
        if !seen[path] {
            result = append(result, path)
            seen[path] = true
        }
    }

    for _, path := range override {
        if !seen[path] {
            result = append(result, path)
            seen[path] = true
        }
    }

    return result
}
```

## Implementation Details

### 1. Runner Factory

```go
// Package runner provides restricted execution for ACP agents
package runner

// NewRunner creates a new restricted runner.
//
// Configuration is resolved in this order (highest priority last):
//  1. Global per-runner-type config (globalRunnersByType)
//  2. Agent per-runner-type config (agentRunnersByType)
//  3. Workspace overrides for the resolved runner type (workspaceConfigByType)
//
// All parameters are maps of runner type -> config (map[string]*config.WorkspaceRunnerConfig).
func NewRunner(
    globalRunnersByType map[string]*config.WorkspaceRunnerConfig,
    agentRunnersByType map[string]*config.WorkspaceRunnerConfig,
    workspaceConfigByType map[string]*config.WorkspaceRunnerConfig,
    workspace string,
    logger *slog.Logger,
) (*Runner, error) {
    // See internal/runner/runner.go for current implementation
}
```

### 2. Integration with ACP Connection

Modify `internal/acp/connection.go`:

```go
// NewConnection starts an ACP server process and establishes a connection.
// If runnerConfig is provided, the process is started through a restricted runner.
func NewConnection(
    ctx context.Context,
    command string,
    autoApprove bool,
    output func(string),
    logger *slog.Logger,
    runnerConfig *runner.Runner, // NEW: optional restricted runner
) (*Connection, error) {
    args := strings.Fields(command)
    if len(args) == 0 {
        return nil, fmt.Errorf("empty command")
    }

    var cmd *exec.Cmd

    if runnerConfig != nil {
        // Execute through restricted runner
        // The runner will handle process creation and restrictions
        // We need to adapt the runner interface to work with exec.Cmd
        // This may require extending go-restricted-runner or creating a wrapper

        // For now, we'll use a simplified approach:
        // Create a wrapper script that the runner executes
        return nil, fmt.Errorf("restricted runner integration not yet implemented")
    } else {
        // Original direct execution
        cmd = exec.CommandContext(ctx, args[0], args[1:]...)
        cmd.Stderr = os.Stderr
    }

    // ... rest of the function remains the same
}
```

### 3. Configuration Loading

Add to `internal/config/config.go`:

```go
// Config represents the complete Mitto configuration.
type Config struct {
    ACPServers    []ACPServer
    Prompts       []WebPrompt
    PromptsDirs   []string
    Web           WebConfig
    UI            UIConfig
    Session       *SessionConfig
    Conversations *ConversationsConfig

    // RestrictedRunner is the global restricted runner configuration
    RestrictedRunner *RestrictedRunnerConfig `json:"restricted_runner,omitempty" yaml:"restricted_runner,omitempty"`
}
```

## Migration Path

### Phase 1: Configuration Schema (Backend Only)

1. Add configuration types to `internal/config/config.go`
2. Add parsing support in `internal/config/settings.go`
3. Add workspace override support in `internal/config/workspace_rc.go`
4. Create `internal/runner` package with basic structure
5. Add unit tests for configuration parsing and merging

**Deliverable**: Configuration can be loaded and parsed, but not yet used

### Phase 2: Runner Implementation

1. Implement `internal/runner/runner.go` with go-restricted-runner integration
2. Implement `internal/runner/variables.go` for variable substitution
3. Implement `internal/runner/config.go` for configuration resolution
4. Add integration tests

**Deliverable**: Runner can be created and configured, but not yet integrated with ACP

### Phase 3: ACP Integration

1. Modify `internal/acp/connection.go` to accept optional runner
2. Modify `internal/auxiliary/manager.go` for auxiliary sessions
3. Modify `internal/web/background_session.go` for web sessions
4. Update session creation to pass runner configuration
5. Add end-to-end tests

**Deliverable**: Agents run through restricted runner when configured

### Phase 4: Documentation and Examples

1. ✅ User documentation: `docs/config/restricted.md` (created)
2. Add example configurations to `config/config.default.yaml`
3. Update `docs/config/overview.md` to reference restricted execution
4. Add migration guide for existing users

**Deliverable**: Users can configure and use restricted runners

**Documentation Checklist**:

- [x] User guide (`docs/config/restricted.md`)
- [ ] Default config examples with comments
- [ ] Overview page updates
- [ ] Migration guide
- [ ] Troubleshooting section (included in user guide)

## Technical Challenges and Solutions

### Challenge 1: Process Management and ACP Communication

**Problem**: ~~go-restricted-runner's `Run()` method returns output as a string, but Mitto needs to interact with the ACP process via stdin/stdout pipes for JSON-RPC communication.~~

**Status**: ✅ **SOLVED** - go-restricted-runner now includes `RunWithPipes()` method

**ACP Protocol Requirements**:

- ACP uses JSON-RPC over stdio (not network sockets)
- Mitto must maintain bidirectional communication with the agent
- stdin: Mitto sends JSON-RPC requests to agent
- stdout: Agent sends JSON-RPC responses to Mitto
- stderr: Agent logs (not part of ACP protocol)

**Solution Implemented**:

The go-restricted-runner library now provides the `RunWithPipes()` method:

```go
stdin, stdout, stderr, wait, err := runner.RunWithPipes(
    ctx,
    command,
    args,
    env,
    params,
)
```

**Returns**:

- `stdin`: io.WriteCloser for sending input to the process
- `stdout`: io.ReadCloser for reading process output
- `stderr`: io.ReadCloser for reading process errors
- `wait()`: Function to wait for process completion and cleanup
- `err`: Any error during process startup

**Usage in Mitto**:

```go
// Start the agent with restricted runner
stdin, stdout, stderr, wait, err := runner.RunWithPipes(
    ctx,
    agentCommand,
    agentArgs,
    env,
    nil, // params
)
if err != nil {
    return fmt.Errorf("failed to start agent: %w", err)
}

// Set up ACP client with the pipes
acpClient := acp.NewClient(stdin, stdout)

// Monitor stderr in background
go func() {
    scanner := bufio.NewScanner(stderr)
    for scanner.Scan() {
        logger.Debug("agent stderr: %s", scanner.Text())
    }
}()

// Use ACP client for communication
// ...

// Cleanup when done
stdin.Close()
wait()
```

**Key Features**:

- Works with all runner types (exec, sandbox-exec, firejail, docker)
- Proper resource cleanup via wait()
- Context cancellation support
- All restrictions still enforced

### Challenge 2: Platform Compatibility

**Problem**: Different runner types are available on different platforms (sandbox-exec on macOS, firejail on Linux).

**Solution**:

- Implement automatic fallback: if configured runner is not available, fall back to `exec`
- Log warnings when falling back
- Add `CheckImplicitRequirements()` call during configuration validation

```go
func (r *Runner) ValidateAvailability() error {
    return r.runner.CheckImplicitRequirements()
}
```

### Challenge 3: Variable Resolution Timing

**Problem**: Variables like `$WORKSPACE` need to be resolved at runtime, not at config load time.

**Solution**:

- Store unresolved paths in configuration
- Resolve variables when creating the runner (per-session)
- Cache resolved configurations per workspace

### Challenge 4: Backward Compatibility

**Problem**: Existing configurations should continue to work without restricted runner config.

**Solution**:

- Make all restricted runner configuration optional
- Default to `exec` runner (no restrictions) if not configured
- Ensure nil checks throughout the codebase

## MCP Server Compatibility

### Overview

Model Context Protocol (MCP) servers provide additional capabilities to agents (filesystem access, database queries, API integrations, etc.). Restricted execution can break MCP server access if not configured properly.

### How MCP Servers Work

1. **Agent spawns MCP server processes** as child processes
2. **Communication** happens via stdio (JSON-RPC)
3. **MCP servers need**:
   - Executable access (filesystem read)
   - Configuration file access (filesystem read)
   - Network access (for remote APIs)
   - Ability to spawn as child processes

### Compatibility Matrix

| Runner Type      | MCP Support | Requirements                                          |
| ---------------- | ----------- | ----------------------------------------------------- |
| **exec**         | ✅ Full     | None - works out of the box                           |
| **sandbox-exec** | ⚠️ Partial  | Must allow read access to MCP executables and configs |
| **firejail**     | ⚠️ Partial  | Must allow read access to MCP executables and configs |
| **docker**       | ❌ Limited  | MCP servers must be installed in the container image  |

### Configuration Guidelines

#### For sandbox-exec and firejail:

```yaml
restricted_runner:
  type: "sandbox-exec" # or "firejail"
  restrictions:
    allow_networking: true # Required for network-based MCP servers
    allow_read_folders:
      - "$WORKSPACE"
      - "$HOME/.config" # MCP configs (e.g., ~/.config/mcp/)
      - "$HOME/.local/bin" # Local MCP executables
      - "$HOME/.npm" # npm global packages (npx-based MCP)
      - "$HOME/.cargo/bin" # Rust-based MCP servers
      - "/usr/local/bin" # System-wide MCP executables
      - "/opt/homebrew/bin" # Homebrew on Apple Silicon
    allow_write_folders:
      - "$WORKSPACE"
      - "$HOME/.cache" # MCP servers may cache data
```

#### For Docker:

```yaml
restricted_runner:
  type: "docker"
  restrictions:
    allow_networking: true
    docker:
      image: "my-agent-with-mcp:latest"
```

**Dockerfile:**

```dockerfile
FROM node:18-alpine

# Install agent
RUN npm install -g my-agent

# Install ALL MCP servers the agent might use
RUN npm install -g @modelcontextprotocol/server-filesystem
RUN npm install -g @modelcontextprotocol/server-github
RUN npm install -g @modelcontextprotocol/server-postgres
# ... etc

# Copy MCP configuration if needed
COPY mcp-config.json /root/.config/mcp/

ENTRYPOINT ["my-agent"]
```

### Testing MCP Server Access

After configuring restrictions, test that MCP servers work:

1. **Start a session** with the restricted agent
2. **Ask the agent to use an MCP server**:
   - "List files in the current directory" (filesystem MCP)
   - "Search GitHub for X" (GitHub MCP)
3. **Check for errors** in the agent's response
4. **Review logs** for MCP server spawn failures

### Troubleshooting

**Symptom**: Agent reports "MCP server not found" or "Failed to start MCP server"

**Solutions**:

1. Add MCP executable path to `allow_read_folders`
2. Add MCP config directory to `allow_read_folders`
3. Enable networking if MCP server needs it
4. For Docker: Install MCP server in the image

**Symptom**: MCP server starts but cannot access resources

**Solutions**:

1. Add resource paths to `allow_read_folders` or `allow_write_folders`
2. Enable networking if MCP server needs external APIs
3. Check MCP server logs for permission errors

### Recommendation

**For production use with MCP servers, use `exec` runner (no restrictions).**

Restricted runners are best suited for:

- Testing untrusted agents
- Agents that don't use MCP servers
- Highly controlled environments where MCP servers are pre-installed

## Security Considerations

### 1. Path Validation

All resolved paths must be validated to prevent path traversal attacks:

```go
func validatePath(path string) error {
    // Ensure path is absolute
    if !filepath.IsAbs(path) {
        return fmt.Errorf("path must be absolute: %s", path)
    }

    // Clean the path to remove .. and .
    cleaned := filepath.Clean(path)

    // Ensure no symlink escapes (optional, may be too restrictive)
    // resolved, err := filepath.EvalSymlinks(cleaned)
    // if err != nil {
    //     return fmt.Errorf("failed to resolve symlinks: %w", err)
    // }

    return nil
}
```

### 2. Command Injection Prevention

The runner configuration should not allow arbitrary command execution:

- Validate runner types against a whitelist
- Validate Docker image names
- Sanitize all user-provided configuration values

### 3. Privilege Escalation

Ensure that restricted runners cannot be used to escalate privileges:

- Document that Mitto should not run as root
- Warn if running with elevated privileges
- Consider adding explicit checks

## Testing Strategy

### Unit Tests

1. **Configuration Parsing**
   - Test YAML/JSON parsing
   - Test validation
   - Test default values

2. **Configuration Merging**
   - Test global + agent merging
   - Test global + agent + workspace merging
   - Test replace vs extend strategies

3. **Variable Substitution**
   - Test all supported variables
   - Test edge cases (missing variables, nested variables)
   - Test path normalization

### Integration Tests

1. **Runner Creation**
   - Test creating runners with different configurations
   - Test fallback behavior
   - Test error handling

2. **ACP Process Execution**
   - Test starting ACP process through runner
   - Test stdin/stdout communication
   - Test process cleanup

### End-to-End Tests

1. **Full Session Flow**
   - Create session with restricted runner
   - Send prompts
   - Verify restrictions are enforced
   - Clean up session

2. **Multi-Workspace**
   - Test different restrictions per workspace
   - Test workspace switching
   - Test configuration reloading

## Example Configurations

### Example 1: Default (No Restrictions)

```yaml
# No restricted runner config - uses "exec" runner (direct execution)
# This is the default and recommended for most users
# Agents have full access to filesystem, network, and can use MCP servers
acp:
  - name: auggie
    command: auggie --acp --allow-indexing
  - name: claude-code
    command: npx -y @zed-industries/claude-code-acp@latest
```

### Example 2: Basic Sandboxing (macOS)

```yaml
# Basic sandboxing with MCP server support
restricted_runner:
  default_type: "sandbox-exec"
  default_restrictions:
    allow_networking: true # Required for network-based MCP servers
    allow_read_folders:
      - "$WORKSPACE"
      - "$HOME/.config" # MCP server configs
      - "$HOME/.local/bin" # MCP server executables
      - "$HOME/.npm" # For npx-based MCP servers
    allow_write_folders:
      - "$WORKSPACE"

acp:
  - name: auggie
    command: auggie --acp --allow-indexing
```

### Example 3: Strict Isolation (Docker)

```yaml
# Docker-based isolation
# NOTE: Agent and all MCP servers must be installed in the image
restricted_runner:
  default_type: "docker"
  default_restrictions:
    allow_networking: true # May be needed for network-based MCP servers
    allow_read_folders:
      - "$WORKSPACE"
    allow_write_folders:
      - "$WORKSPACE"
    docker:
      image: "my-agent-with-mcp:latest" # Custom image with agent + MCP servers
      memory_limit: "2g"
      cpu_limit: "2.0"

acp:
  - name: sandboxed-agent
    command: my-agent --acp # This command runs inside the container
```

**Dockerfile for Example 3:**

```dockerfile
FROM node:18-alpine

# Install the agent
RUN npm install -g my-agent

# Install MCP servers that the agent needs
RUN npm install -g @modelcontextprotocol/server-filesystem
RUN npm install -g @modelcontextprotocol/server-github

# The agent will be started by Mitto via docker exec
ENTRYPOINT ["my-agent"]
```

### Example 4: Per-Agent Configuration

```yaml
# Different restrictions for different agents
acp:
  - name: trusted-agent
    command: trusted-agent --acp
    # No restricted_runner config = uses "exec" (no restrictions)
    # Full access to filesystem, network, and MCP servers

  - name: experimental-agent
    command: experimental-agent --acp
    restricted_runner:
      type: "firejail"
      restrictions:
        allow_networking: false # WARNING: Breaks network-based MCP servers
        allow_read_folders:
          - "$WORKSPACE"
          - "$HOME/.local/bin" # If agent needs local MCP servers
        allow_write_folders:
          - "$WORKSPACE/output"
```

### Example 5: Workspace Override

Global config:

```yaml
acp:
  - name: auggie
    command: auggie --acp
    restricted_runner:
      type: "sandbox-exec"
      restrictions:
        allow_networking: true
        allow_read_folders:
          - "$WORKSPACE"
```

Workspace `.mittorc`:

```yaml
# Stricter restrictions for this sensitive project
restricted_runner:
  restrictions:
    allow_networking: false # Override: disable network
    allow_read_folders:
      - "$WORKSPACE"
      - "$WORKSPACE/vendor" # Add vendor folder
    deny_folders:
      - "$WORKSPACE/.env" # Explicitly deny .env file
  merge_strategy: "extend"
```

## Open Questions

1. **Process Lifecycle**: How should we handle runner process cleanup on session termination?
   - Current approach: Kill the ACP process, which should clean up the runner
   - Alternative: Add explicit cleanup method to runner

2. **Performance Impact**: What is the overhead of running through restricted runners?
   - Need benchmarks comparing direct execution vs each runner type
   - May need to make runner type configurable per-session

3. **Error Reporting**: How should we report runner-specific errors to users?
   - Log to session log?
   - Show in UI?
   - Both?

4. **Configuration Validation**: Should we validate runner configuration at startup or lazily?
   - Startup validation: Fail fast, but may prevent Mitto from starting
   - Lazy validation: More flexible, but errors appear later
   - **Recommended**: Validate at startup with warnings, not errors

## Next Steps

1. Review this plan with stakeholders
2. Prioritize phases based on requirements
3. Create detailed tasks for Phase 1
4. Begin implementation

## References

### User Documentation

- [Restricted Execution User Guide](../config/restricted.md) - **Main user-facing documentation**
- [Mitto Configuration Overview](../config/overview.md)
- [ACP Server Configuration](../config/web/acp.md)
- [Workspace Configuration](../config/web/workspace.md)

### Developer Documentation

- [go-restricted-runner Documentation](https://github.com/inercia/go-restricted-runner/blob/main/docs/README.md)
- [Mitto Architecture Documentation](./architecture.md)
- [ACP Protocol Specification](https://github.com/coder/acp-go-sdk)

### Related

- [Session Management](./session-management.md)
- [Workspaces](./workspaces.md)

```

```
