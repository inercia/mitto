---
description: CLI UX patterns and ACP protocol client, connection lifecycle, permission handling
globs:
  - "internal/cmd/**/*"
  - "cmd/mitto/**/*"
  - "internal/acp/**/*"
keywords:
  - CLI command
  - Cobra
  - ACP connection
  - ACP protocol
  - acp-go-sdk
  - permission handling
  - AutoApprovePermission
---

# CLI and ACP Protocol

## CLI Patterns

### Cobra Command Structure

```go
var cliCmd = &cobra.Command{
    Use:   "cli",
    Short: "One-line description",
    RunE:  runCLI,
}
```

### User Feedback

```go
fmt.Printf("🚀 Starting ACP server: %s\n", server.Name)
fmt.Printf("✅ Connected (protocol v%v)\n", version)
```

### Multi-Workspace CLI Usage

```bash
mitto web --dir /path/to/project1 --dir /path/to/project2
mitto web --dir auggie:/path/to/project1 --dir claude-code:/path/to/project2
```

### `--host` Flag (Security-Sensitive)

`mitto web --host 0.0.0.0` binds to all interfaces (needed for Docker). Default is `127.0.0.1`.

**Security**: The local listener runs without authentication. When `--host` is not a loopback address, a runtime warning is printed. Never expose to untrusted networks.

## ACP Protocol

### SDK: `github.com/coder/acp-go-sdk`

- The `Client` struct implements `acp.Client` interface
- Use `acp.ClientSideConnection` for protocol handling
- JSON-RPC 2.0 over stdin/stdout of the agent subprocess

### ContentBlock — Discriminated Union

```go
// Check type via nil pointer checks (NOT a Type() method):
if block.Image != nil { /* block.Image.Data, block.Image.MimeType */ }
else if block.Text != nil { /* block.Text.Text */ }

// Create blocks via helpers:
acp.TextBlock("hello")
acp.ImageBlock(base64Data, "image/png")
```

**Anti-pattern**: `block.Type()`, `acp.ContentBlockTypeImage` do NOT exist.

### Connection Lifecycle

```go
conn, err := acp.NewConnection(ctx, command, autoApprove, output, logger)
defer conn.Close()

conn.Initialize(ctx)       // Returns AgentCapabilities
conn.NewSession(ctx, cwd)  // Returns SessionID + Modes
conn.Prompt(ctx, message)  // Streaming response via callbacks
```

### Agent Capabilities
Always check capabilities before sending unsupported content blocks:
```go
caps := resp.AgentCapabilities
bs.agentSupportsImages = caps.PromptCapabilities.Image
```

### Permission Handling

```go
// AutoApprovePermission selects the best "allow" option automatically
// Priority: AllowOnce > AllowAlways > first option
resp := AutoApprovePermission(options)

// SelectPermissionOption selects a specific option by index
resp := SelectPermissionOption(options, selectedIndex)

// CancelledPermissionResponse returns a cancelled response
resp := CancelledPermissionResponse()
```

## Agent Definitions

Agents are defined in `config/agents/builtin/<agent>/` (shipped) or `MITTO_DIR/agents/custom/<agent>/` (user-created).

### Key Types (`internal/agents/types.go`)

| Type | Purpose |
|------|---------|
| `AgentMetadata` | Parsed from `metadata.yaml` |
| `MCPMetadata` | MCP scope capabilities (`Scopes []string`) |
| `MCPInstallInput` | JSON input to `mcp-install.sh` (includes `Scope` field) |
| `AgentDefinition` | Resolved agent with metadata + filesystem location |
| `AgentDefaults` | Optional `defaults` block seeded into a new ACP server at discovery |
| `ConstraintSpec` | A single auto-select rule (`matchMode` + `pattern`); mirrors `config.ACPServerConstraint` |

### metadata.yaml structure

```yaml
name: claude-code
displayName: Claude Code
acpId: claude
mcp:
  scopes: ["user", "project", "local"]  # supported scopes
install:
  method: npx
  package: "@anthropic-ai/claude-code"
defaults:              # optional; seeded into the ACP server when this agent is discovered
  env:                 # default environment variables for the ACP server
    NODE_OPTIONS: "--max-old-space-size=8192"
  constraints:         # auto-select config options (e.g. model) on session start
    model:
      matchMode: contains   # contains | exact | startsWith | regex | lookAlike
      pattern: "Opus"
  tags: ["coding", "smart"] # categorization tags applied to the server
  autoApprove: false        # auto-approve tool-call permission requests
```

### Agent Defaults (seeded at discovery)

The optional `defaults` block pre-fills a newly discovered agent's ACP server settings.
The mapping is direct:

| `metadata.yaml` `defaults` | ACP server setting |
|----------------------------|--------------------|
| `defaults.env`             | `ACPServer.Env` |
| `defaults.constraints`     | `ACPServer.Constraints` (see [08-config.md](08-config.md#acp-server-constraints)) |
| `defaults.tags`            | `ACPServer.Tags` |
| `defaults.autoApprove`     | `ACPServer.AutoApprove` |

Seeding is **request-wins**: values the user supplies in the Agent Discovery dialog take
precedence; a default only fills a field the user left empty. `autoApprove` is taken from
the default. Types live in `internal/agents/types.go` (`AgentDefaults`, `ConstraintSpec`);
the mapping happens in `seedACPServerDefaults` (`internal/web/handlers/agent_discovery.go`).

**MCP scope values**: `user` (global config), `project` (per-repo), `local` (local-only, not committed).

### Agent Commands

- `CommandMCPList` (via `mcp-list.sh`) — List MCP servers
- `CommandMCPInstall` (via `mcp-install.sh`) — Install MCP server
- `CommandMCPRemove` (via `mcp-remove.sh`) — Remove MCP server (scope must match metadata)

### Adding Agents

Create `config/agents/builtin/<name>/metadata.yaml` + scripts (`install.sh`, `mcp-list.sh`, `mcp-install.sh`, `mcp-remove.sh`). Set `mcp.scopes` in metadata.
