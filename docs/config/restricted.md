# Restricted Execution

Mitto can run AI agents through restricted runners to limit their access to system resources. This provides an additional security layer when working with untrusted agents or in sensitive environments.

## Overview

By default, Mitto runs agents with **no restrictions** (direct execution). Agents have full access to:

- Your entire filesystem
- Network connections
- System resources
- MCP servers

You can optionally enable **restricted execution** to sandbox agents and control what they can access.

## ⚠️ Important Warnings

### Default Behavior

**Restricted execution is disabled by default.** You must explicitly configure it in your settings.

Without configuration, agents run with the same permissions as your user account.

### MCP Server Compatibility

**Restricted execution can break MCP server access.**

MCP (Model Context Protocol) servers provide agents with additional capabilities like filesystem access, database queries, and API integrations. When you restrict an agent:

- **Network restrictions** prevent access to remote MCP servers
- **Filesystem restrictions** may prevent spawning MCP server processes
- **Docker isolation** requires MCP servers to be installed in the container

**Recommendation**: If your agent uses MCP servers, stick with the default (no restrictions) or carefully configure allowed paths.

### ACP Protocol Requirements

All restricted runners must preserve stdin/stdout communication for the ACP protocol. Mitto communicates with agents using JSON-RPC over stdio, not network sockets.

The go-restricted-runner library provides two execution methods:

- **Run()**: Execute a command and return output after completion
- **RunWithPipes()**: Interactive process communication with stdin/stdout/stderr pipes

Mitto uses **RunWithPipes()** for ACP agent communication, which allows:

- Bidirectional JSON-RPC communication over stdio
- Long-running agent processes
- Real-time message streaming
- Proper signal handling and cleanup

## Runner Types

Mitto supports four runner types:

| Runner             | Platform | Isolation | Overhead | MCP Support |
| ------------------ | -------- | --------- | -------- | ----------- |
| **exec** (default) | All      | None      | None     | ✅ Full     |
| **sandbox-exec**   | macOS    | Medium    | Low      | ⚠️ Partial  |
| **firejail**       | Linux    | Medium    | Low      | ⚠️ Partial  |
| **docker**         | All\*    | High      | Medium   | ❌ Limited  |

\*Docker must be installed and running

### exec (Default)

Direct execution with no restrictions. This is the **recommended option** for most users.

**Pros:**

- No setup required
- Full MCP server support
- No performance overhead
- Works everywhere

**Cons:**

- No security isolation
- Agent has full system access

**Use when:**

- Working with trusted agents
- Using MCP servers
- Development and testing

### sandbox-exec (macOS)

Uses macOS's built-in `sandbox-exec` for process isolation.

**Pros:**

- Built into macOS (no installation)
- Low performance overhead
- Can use MCP servers with proper configuration

**Cons:**

- macOS only
- Requires careful path configuration for MCP servers
- Complex sandbox profile syntax

**Use when:**

- Running on macOS
- Need basic isolation
- Willing to configure MCP server paths

### firejail (Linux)

Uses Linux's `firejail` for process isolation.

**Pros:**

- Good isolation on Linux
- Low performance overhead
- Can use MCP servers with proper configuration

**Cons:**

- Linux only
- Requires firejail installation
- Requires careful path configuration for MCP servers

**Use when:**

- Running on Linux
- Need basic isolation
- Willing to configure MCP server paths

### docker

Runs agents inside Docker containers for maximum isolation.

**Pros:**

- Strongest isolation
- Cross-platform (where Docker is available)
- Consistent environment

**Cons:**

- Requires Docker installation
- Higher performance overhead
- MCP servers must be installed in container image
- More complex setup

**Use when:**

- Need maximum isolation
- Testing untrusted agents
- Agent doesn't use MCP servers (or you can pre-install them)

## Configuration

### Basic Setup

Mitto includes reasonable defaults for all supported runner types in `config/config.default.yaml`.
These defaults are designed to work with most MCP servers out of the box.

**Default configuration** (already included in `config/config.default.yaml`):

```yaml
# Global per-runner-type configuration with reasonable defaults
restricted_runners:
  # sandbox-exec: macOS sandbox with MCP server compatibility
  sandbox-exec:
    restrictions:
      allow_networking: true
      allow_read_folders:
        - "$WORKSPACE"
        - "$HOME/.config"
        - "$HOME/.local/bin"
        - "$HOME/.npm"
        - "/usr/local/bin"
        - "/opt/homebrew/bin"
      allow_write_folders:
        - "$WORKSPACE"
        - "$HOME/.cache"
      deny_folders:
        - "$HOME/.ssh"
        - "$HOME/.aws"
    merge_strategy: "extend"

  # firejail: Linux namespace isolation
  firejail:
    restrictions:
      allow_networking: true
      allow_read_folders:
        - "$WORKSPACE"
        - "$HOME/.config"
        - "$HOME/.local/bin"
        - "$HOME/.npm"
        - "/usr/local/bin"
      allow_write_folders:
        - "$WORKSPACE"
        - "$HOME/.cache"
      deny_folders:
        - "$HOME/.ssh"
        - "$HOME/.aws"
    merge_strategy: "extend"

  # docker: Container execution
  docker:
    restrictions:
      allow_networking: true
      docker:
        image: "ubuntu:22.04"
        memory_limit: "2g"
        cpu_limit: "2.0"
      allow_read_folders:
        - "$WORKSPACE"
      allow_write_folders:
        - "$WORKSPACE"
    merge_strategy: "replace"
```

**To use these defaults:**

1. Select a runner type in the workspace settings (Web UI)
2. Or set `restricted_runner` in `workspaces.json`
3. The global defaults will be applied automatically

**To customize:**
Add your own `restricted_runners` section to `~/.mittorc` or `settings.json` to override the defaults.

### Per-Agent Configuration

Configure different restrictions for each agent using the same per-runner-type format:

```yaml
acp_servers:
  - name: trusted-agent
    command: trusted-agent --acp
    # No restricted_runners = uses global defaults

  - name: experimental-agent
    command: experimental-agent --acp
    # Per-runner-type configuration for this agent
    restricted_runners:
      sandbox-exec:
        # Applied when this agent uses sandbox-exec runner
        restrictions:
          allow_networking: false
          allow_read_folders:
            - "$WORKSPACE"
            - "$HOME/.experimental"
          allow_write_folders:
            - "$WORKSPACE"
        merge_strategy: "extend"

      docker:
        # Applied when this agent uses docker runner
        restrictions:
          allow_networking: false
          docker:
            image: "experimental:latest"
        merge_strategy: "replace"
```

**Legacy format** (still supported but deprecated):

```yaml
acp:
  - name: experimental-agent
    command: experimental-agent --acp
    restricted_runner:
      type: "sandbox-exec"
      restrictions:
        allow_networking: false
```

### Workspace-Specific Overrides

Override restrictions for specific workspaces by creating `.mittorc` in the workspace directory.

**Important**: Configuration is **per runner type** (exec, sandbox-exec, firejail, docker). When a workspace uses a runner of type X, it applies the config for type X.

```yaml
# .mittorc in /path/to/project/
# Configuration is per runner type
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
        - "/tmp"
    merge_strategy: "extend"

  docker:
    # Applied when any workspace in this folder uses docker runner
    restrictions:
      allow_networking: false
      docker:
        image: "alpine:latest"
    merge_strategy: "replace"
```

**How it works**:

- Workspace 1: `/path/to/project` + agent configured with `exec` → uses `restricted_runners.exec`
- Workspace 2: `/path/to/project` + agent configured with `sandbox-exec` → uses `restricted_runners.sandbox-exec`
- Workspace 3: `/path/to/project` + agent configured with `docker` → uses `restricted_runners.docker`

## Configuration Hierarchy

Mitto supports a three-level configuration hierarchy for restricted runners:

1. **Global per-runner-type** (`settings.json` or `~/.mittorc` - `restricted_runners` section)
2. **Per-agent per-runner-type** (`settings.json` acp_servers - `restricted_runners` section)
3. **Workspace per-runner-type** (`.mittorc` in workspace directory - `restricted_runners` section)

**All levels use the same format**: configuration is organized by runner type (exec, sandbox-exec, firejail, docker).

### Resolution Order

When a workspace uses a runner of type X, configuration is resolved in this order (highest priority last):

1. Global `restricted_runners[X]`
2. Legacy global `restricted_runner.default_restrictions` (deprecated)
3. Agent `restricted_runners[X]`
4. Legacy agent `restricted_runner.restrictions` (deprecated)
5. Workspace `restricted_runners[X]`

Each level can either:

- **Extend** the previous level (merge restrictions) - `merge_strategy: "extend"`
- **Replace** the previous level (ignore parent config) - `merge_strategy: "replace"`

### Example: Full Hierarchy

**Global** (`settings.json`):

```yaml
restricted_runners:
  sandbox-exec:
    restrictions:
      allow_networking: true
      allow_read_folders:
        - "$WORKSPACE"
```

**Agent** (`settings.json`):

```yaml
acp_servers:
  - name: experimental
    command: experimental --acp
    restricted_runners:
      sandbox-exec:
        restrictions:
          allow_networking: false # Override global
          allow_read_folders:
            - "$HOME/.experimental" # Add to global
        merge_strategy: "extend"
```

**Workspace** (`.mittorc` in `/path/to/project`):

```yaml
restricted_runners:
  sandbox-exec:
    restrictions:
      allow_read_folders:
        - "$WORKSPACE/vendor" # Add to agent + global
    merge_strategy: "extend"
```

**Result** for `experimental` agent with `sandbox-exec` in `/path/to/project`:

- Networking: `false` (from agent)
- Read folders: `["$WORKSPACE", "$HOME/.experimental", "$WORKSPACE/vendor"]` (merged from all levels)

## Configuration Options

### Restriction Settings

#### allow_networking

Controls network access.

```yaml
allow_networking: true   # Allow network (default)
allow_networking: false  # Block all network access
```

**Note**: Setting to `false` will break network-based MCP servers.

#### allow_read_folders

List of folders the agent can read from. Supports variables.

```yaml
allow_read_folders:
  - "$WORKSPACE" # Current workspace directory
  - "$HOME/.config" # User config directory
  - "$HOME/.local/bin" # Local executables
  - "/usr/local/bin" # System executables
```

#### allow_write_folders

List of folders the agent can write to. Supports variables.

```yaml
allow_write_folders:
  - "$WORKSPACE" # Current workspace directory
  - "$HOME/.cache" # Cache directory
```

#### deny_folders

List of folders to explicitly deny access to (overrides allow lists).

```yaml
deny_folders:
  - "$HOME/.ssh" # SSH keys
  - "$HOME/.aws" # AWS credentials
  - "$HOME/.env" # Environment files
```

### Docker-Specific Options

#### image

Docker image to use (required for docker runner).

```yaml
docker:
  image: "my-agent:latest"
```

The image must contain:

- The agent executable
- Any MCP servers the agent uses
- All required dependencies

#### memory_limit

Maximum memory the container can use.

```yaml
docker:
  memory_limit: "2g" # 2 gigabytes
```

#### cpu_limit

Maximum CPU cores the container can use.

```yaml
docker:
  cpu_limit: "2.0" # 2 CPU cores
```

### Merge Strategies

Control how workspace restrictions merge with agent/global config.

```yaml
merge_strategy: "extend"   # Merge with parent config (default)
merge_strategy: "replace"  # Ignore parent config
```

**extend**: Combines restrictions from all levels (global → agent → workspace)
**replace**: Uses only workspace restrictions, ignores global and agent config

## Supported Variables

Variables are resolved at runtime when the agent starts.

| Variable     | Description                 | Example                               |
| ------------ | --------------------------- | ------------------------------------- |
| `$WORKSPACE` | Current workspace directory | `/Users/user/project`                 |
| `$HOME`      | User's home directory       | `/Users/user`                         |
| `$MITTO_DIR` | Mitto data directory        | `~/Library/Application Support/Mitto` |
| `$USER`      | Current username            | `user`                                |
| `$TMPDIR`    | System temp directory       | `/tmp`                                |

Both `$VAR` and `${VAR}` syntax are supported.

## MCP Server Configuration

If your agent uses MCP servers, you need to allow access to:

### For sandbox-exec and firejail:

```yaml
restricted_runner:
  type: "sandbox-exec"
  restrictions:
    allow_networking: true # Required for network-based MCP servers
    allow_read_folders:
      - "$WORKSPACE"
      - "$HOME/.config" # MCP configs
      - "$HOME/.local/bin" # Local MCP executables
      - "$HOME/.npm" # npm global packages (npx-based MCP)
      - "$HOME/.cargo/bin" # Rust-based MCP servers
      - "/usr/local/bin" # System-wide executables
      - "/opt/homebrew/bin" # Homebrew (macOS Apple Silicon)
    allow_write_folders:
      - "$WORKSPACE"
      - "$HOME/.cache" # MCP servers may cache data
```

### For Docker:

Create a custom image with the agent and all MCP servers:

```dockerfile
FROM node:18-alpine

# Install agent
RUN npm install -g my-agent

# Install MCP servers
RUN npm install -g @modelcontextprotocol/server-filesystem
RUN npm install -g @modelcontextprotocol/server-github

ENTRYPOINT ["my-agent"]
```

Then configure:

```yaml
restricted_runners:
  docker:
    restrictions:
      allow_networking: true
      docker:
        image: "my-agent-with-mcp:latest"
```

## How to Check Which Runner is Active

When a session starts, Mitto logs the runner type and restriction status:

```
INFO: session created session_id=abc123 workspace=/path/to/project acp_server=experimental runner_type=sandbox-exec runner_restricted=true
```

The session metadata also includes runner information:

```json
{
  "session_id": "abc123",
  "runner_type": "sandbox-exec",
  "runner_restricted": true
}
```

You can also check the configuration hierarchy:

- If no `restricted_runners` at any level → **exec** (direct execution)
- If only global config → uses global per-runner-type config
- If agent config exists → merges with or replaces global config
- If workspace config exists → merges with or replaces agent config

## Examples

### Example 1: Default (Recommended)

No configuration needed. Agents run with full access.

```yaml
acp:
  - name: auggie
    command: auggie --acp --allow-indexing
```

**Pros**: Works out of the box, full MCP support
**Cons**: No isolation

### Example 2: Basic Sandboxing (macOS)

```yaml
# Global per-runner-type configuration
restricted_runners:
  sandbox-exec:
    restrictions:
      allow_networking: true
      allow_read_folders:
        - "$WORKSPACE"
        - "$HOME/.config"
        - "$HOME/.local/bin"
      allow_write_folders:
        - "$WORKSPACE"

acp_servers:
  - name: auggie
    command: auggie --acp --allow-indexing
    # Will use sandbox-exec runner (configured elsewhere or via agent config)
```

**Pros**: Basic isolation, MCP servers work
**Cons**: macOS only, requires path configuration

### Example 3: Strict Isolation (Docker)

```yaml
# Global per-runner-type configuration
restricted_runners:
  docker:
    restrictions:
      allow_networking: true
      docker:
        image: "my-agent-with-mcp:latest"
        memory_limit: "2g"
        cpu_limit: "2.0"

acp_servers:
  - name: sandboxed-agent
    command: my-agent --acp
    # Will use docker runner (configured elsewhere or via agent config)
```

**Pros**: Maximum isolation, cross-platform
**Cons**: Requires Docker, MCP servers must be in image, higher overhead

### Example 4: Mixed Configuration

```yaml
acp_servers:
  # Trusted agent - no restrictions
  - name: auggie
    command: auggie --acp --allow-indexing
    # No restricted_runners = uses exec (no restrictions)

  # Experimental agent - sandboxed with firejail
  - name: experimental
    command: experimental-agent --acp
    restricted_runners:
      firejail:
        restrictions:
          allow_networking: false
          allow_read_folders:
            - "$WORKSPACE"
          allow_write_folders:
            - "$WORKSPACE/output"
        merge_strategy: "replace"
```

**Pros**: Flexibility, different security levels per agent
**Cons**: More complex configuration

**JSON format:**

```json
{
  "acp_servers": [
    {
      "name": "auggie",
      "command": "auggie --acp --allow-indexing"
    },
    {
      "name": "experimental",
      "command": "experimental-agent --acp",
      "restricted_runners": {
        "firejail": {
          "restrictions": {
            "allow_networking": false,
            "allow_read_folders": ["$WORKSPACE"],
            "allow_write_folders": ["$WORKSPACE/output"]
          },
          "merge_strategy": "replace"
        }
      }
    }
  ]
}
```

### Example 5: Per-Runner-Type Workspace Configuration

**Use case**: Override settings for any workspace that uses Docker in this folder.

**Scenario**:

- Workspace 1: `/path/to/project` + agent configured with `exec`
- Workspace 2: `/path/to/project` + agent configured with `docker`

**Solution**: Use per-runner-type configuration in `.mittorc`:

```yaml
# .mittorc in /path/to/project/
restricted_runners:
  exec:
    # Applied to any workspace using exec runner
    restrictions:
      allow_read_folders:
        - "$WORKSPACE"
    merge_strategy: "extend"

  docker:
    # Applied to any workspace using docker runner
    restrictions:
      allow_networking: false
      docker:
        image: "project-specific:latest"
    merge_strategy: "replace"
```

Now:

- Workspace 1 (exec runner) gets workspace-specific read folders
- Workspace 2 (docker runner) uses project-specific Docker image
- Configuration is based on runner type, not agent name

### Example 6: Workspace Override

Global config (`~/.mittorc`):

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

Workspace config (`/path/to/sensitive-project/.mittorc`):

```yaml
# Stricter restrictions for this project
restricted_runners:
  sandbox-exec:
    # Applied when any workspace in this folder uses sandbox-exec
    restrictions:
      allow_networking: false # Override: disable network
      deny_folders:
        - "$WORKSPACE/.env" # Explicitly deny .env file
    merge_strategy: "extend"
```

**Pros**: Project-specific security policies
**Cons**: Must maintain multiple configs

## Troubleshooting

### Agent fails to start

**Symptom**: Agent process fails immediately after starting

**Possible causes**:

1. Runner executable not found (sandbox-exec, firejail, docker)
2. Docker image not available
3. Insufficient permissions

**Solutions**:

- Check that the runner is installed: `which sandbox-exec` or `which firejail` or `docker --version`
- For Docker: Pull the image first: `docker pull my-agent:latest`
- Check Mitto logs for detailed error messages

### MCP server not found

**Symptom**: Agent reports "MCP server not found" or "Failed to start MCP server"

**Possible causes**:

1. MCP executable path not in `allow_read_folders`
2. MCP config directory not accessible
3. Network disabled but MCP server needs it

**Solutions**:

- Add MCP executable path to `allow_read_folders`:
  ```yaml
  allow_read_folders:
    - "$HOME/.local/bin"
    - "$HOME/.npm"
    - "/usr/local/bin"
  ```
- Add MCP config directory:
  ```yaml
  allow_read_folders:
    - "$HOME/.config"
  ```
- Enable networking if needed:
  ```yaml
  allow_networking: true
  ```

### MCP server starts but cannot access resources

**Symptom**: MCP server runs but reports permission errors

**Possible causes**:

1. Resource paths not in allowed folders
2. Network disabled but MCP needs external APIs

**Solutions**:

- Add resource paths to `allow_read_folders` or `allow_write_folders`
- Enable networking for API-based MCP servers
- Check MCP server logs for specific permission errors

### Docker container fails to start

**Symptom**: Docker runner fails with container errors

**Possible causes**:

1. Image doesn't contain the agent
2. Agent command is incorrect
3. Docker daemon not running

**Solutions**:

- Verify image contains agent: `docker run my-agent:latest --version`
- Check Docker is running: `docker ps`
- Review Docker logs: `docker logs <container-id>`

### Performance issues

**Symptom**: Agent is slow or unresponsive

**Possible causes**:

1. Docker overhead
2. Resource limits too restrictive

**Solutions**:

- Increase Docker resource limits:
  ```yaml
  docker:
    memory_limit: "4g"
    cpu_limit: "4.0"
  ```
- Consider using sandbox-exec or firejail instead of Docker
- Use `exec` runner if performance is critical

## Best Practices

### 1. Start with No Restrictions

Begin with the default `exec` runner. Only add restrictions if you have specific security requirements.

### 2. Test with MCP Servers

If your agent uses MCP servers, test thoroughly after enabling restrictions:

- Try all MCP server features
- Check agent logs for errors
- Verify MCP servers can access required resources

### 3. Use Workspace Overrides for Sensitive Projects

Keep global config permissive, add restrictions only for sensitive workspaces:

```yaml
# Global: permissive
acp:
  - name: auggie
    command: auggie --acp

# Sensitive project: restricted
# /path/to/sensitive/.mittorc
restricted_runner:
  type: "docker"
  restrictions:
    allow_networking: false
```

### 4. Document Your Restrictions

Add comments explaining why each restriction exists:

```yaml
restricted_runner:
  restrictions:
    allow_networking: false # This project handles PII, no external access
    deny_folders:
      - "$WORKSPACE/.env" # Contains production credentials
```

### 5. Monitor and Adjust

- Check agent logs regularly
- Adjust restrictions based on actual needs
- Remove unnecessary restrictions

### 6. Security vs Usability

Balance security with usability:

- **High security**: Use Docker, disable networking, minimal folder access
- **Medium security**: Use sandbox-exec/firejail, allow networking, specific folders
- **Low security**: Use exec (default), rely on agent's built-in safety

## Limitations

### Platform-Specific Runners

- `sandbox-exec` only works on macOS
- `firejail` only works on Linux
- `docker` requires Docker installation

If a configured runner is unavailable, Mitto will log a warning and fall back to `exec`.

### Working Directory (cwd) Option

The ACP server `cwd` option (for setting the process working directory) is **not supported** with restricted runners. If `cwd` is specified for an agent using a restricted runner, a warning will be logged and the setting will be ignored.

To use `cwd`, you must use the default `exec` runner (no restrictions):

```yaml
acp:
  - my-agent:
      command: my-agent --acp
      cwd: /home/user/my-project  # Only works with exec runner
```

See [ACP Server Configuration](web/acp.md) for more details on the `cwd` option.

### MCP Server Compatibility

Restricted runners have limited MCP server support:

- `exec`: ✅ Full support
- `sandbox-exec`/`firejail`: ⚠️ Requires configuration
- `docker`: ❌ MCP servers must be in image

### Performance Overhead

- `exec`: No overhead
- `sandbox-exec`/`firejail`: Minimal overhead (~1-5%)
- `docker`: Moderate overhead (~10-20%)

### Configuration Complexity

Restricted execution adds configuration complexity:

- Must understand filesystem paths
- Must know which MCP servers the agent uses
- Must maintain multiple config files for workspace overrides

## Security Considerations

### What Restricted Execution Protects Against

✅ **Prevents**:

- Unauthorized filesystem access outside allowed folders
- Network access when disabled
- Access to sensitive files (SSH keys, credentials)

❌ **Does NOT prevent**:

- Malicious code execution within allowed folders
- Resource exhaustion (except Docker with limits)
- Privilege escalation (if Mitto runs as root)

### Running Mitto as Root

**Never run Mitto as root.** Restricted runners cannot prevent privilege escalation if Mitto itself has root privileges.

### Trusted vs Untrusted Agents

- **Trusted agents** (Auggie, Claude Code): Use `exec` runner
- **Experimental agents**: Use `sandbox-exec` or `firejail`
- **Untrusted agents**: Use `docker` with strict restrictions

## Implementation Details

### go-restricted-runner Library

Mitto uses the [go-restricted-runner](https://github.com/inercia/go-restricted-runner) library for sandboxed execution.

**Current version**: Latest (supports RunWithPipes)

**Key features**:

- Unified interface for multiple runner types (exec, sandbox-exec, firejail, docker)
- Interactive process communication via RunWithPipes()
- Context-based cancellation and timeout support
- Proper resource cleanup and signal handling

### RunWithPipes() Method

The library's `RunWithPipes()` method enables interactive communication with restricted processes:

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

- `stdin`: WriteCloser for sending input to the process
- `stdout`: ReadCloser for reading process output
- `stderr`: ReadCloser for reading process errors
- `wait()`: Function to wait for process completion and cleanup
- `err`: Any error during process startup

**Usage in Mitto**:

1. Mitto starts the agent using RunWithPipes()
2. Sends ACP JSON-RPC messages to stdin
3. Reads ACP responses from stdout
4. Monitors stderr for agent errors
5. Calls wait() when the session ends

**Important notes**:

- Always close stdin when done to signal EOF
- Always call wait() to clean up resources
- Context cancellation kills the process immediately
- All restrictions (paths, network, etc.) apply to RunWithPipes()

### Restriction Enforcement

Each runner type enforces restrictions differently:

**exec**: No enforcement (direct execution)

**sandbox-exec**: macOS sandbox profile

- Generates a sandbox profile from restrictions
- Applied via `sandbox-exec -p <profile> <command>`
- Enforced by macOS kernel

**firejail**: Linux namespace isolation

- Converts restrictions to firejail flags
- Applied via `firejail --noprofile <flags> <command>`
- Enforced by Linux namespaces and seccomp

**docker**: Container isolation

- Mounts allowed folders as volumes
- Uses `--network none` for network restrictions
- Applied via `docker run` or `docker exec`
- Enforced by Docker daemon

### Variable Substitution

The following variables are expanded in path configurations:

- `$WORKSPACE`: Current workspace directory
- `$HOME`: User's home directory
- `$MITTO_DIR`: Mitto's data directory (from MITTO_DIR env or platform default)

Expansion happens before passing paths to the runner, ensuring consistent behavior across all runner types.

## Further Reading

- [go-restricted-runner Documentation](https://github.com/inercia/go-restricted-runner)
- [Mitto Configuration Overview](./overview.md)
- [ACP Server Configuration](./web/acp.md)
- [Workspace Configuration](./web/workspace.md)
