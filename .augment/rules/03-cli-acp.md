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

Multi-workspace: `mitto web --dir auggie:/path/to/project1 --dir claude-code:/path/to/project2`

`--host` is loopback-only. Remote and Docker access require effective authentication plus `--port-external`, which routes through `StartExternalListener` for external provenance and scanner defense.

## ACP Protocol

SDK: `github.com/coder/acp-go-sdk` over JSON-RPC 2.0 stdin/stdout.

**ContentBlock** (discriminated union): Check type via nil-pointer checks, NOT `Type()`:
```go
if block.Image != nil { use block.Image.Data }
else if block.Text != nil { use block.Text.Text }
```

**Connection lifecycle**:
```go
conn, err := acp.NewConnection(ctx, cmd, autoApprove, output, logger)
conn.Initialize(ctx)  // AgentCapabilities
conn.NewSession(ctx, cwd)  // SessionID + Modes
conn.Prompt(ctx, msg)  // Streaming via callbacks
```

### Agent Capabilities
Always check capabilities before sending unsupported content blocks:
```go
caps := resp.AgentCapabilities
bs.agentSupportsImages = caps.PromptCapabilities.Image
```

### Error Code Extraction & Logging

Wrap `*acp.RequestError` extraction in a helper for structured logging (mitto-8d7):

```go
// rpcErrorCode extracts the JSON-RPC error code from err when it (or any error it
// wraps) is an *acp.RequestError. Used to surface a structured, queryable rpc_code
// field in addition to the full error string.
func rpcErrorCode(err error) (int, bool) {
    var re *acp.RequestError
    if errors.As(err, &re) && re != nil {
        return re.Code, true
    }
    return 0, false
}

// Log both code and message:
rpcCode, _ := rpcErrorCode(err)
logger.Warn("NewSession failed",
    "rpc_code", rpcCode,
    "error", err.Error(),
)
```

This decouples error-code queries (e.g., alerting on `-32603` internal server errors) from full error strings.

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

Located in `config/agents/builtin/<agent>/` (shipped) or `MITTO_DIR/agents/custom/<agent>/` (custom).

**metadata.yaml**: Defines agent, MCP scopes, install method, default env/constraints/tags.
**MCP scopes**: `user` (global), `project` (per-repo), `local` (uncommitted).
**Agent defaults** (seeded at discovery): Pre-fill ACP server settings. Request-wins: user values take precedence.
**Commands**: `mcp-list.sh`, `mcp-install.sh`, `mcp-remove.sh` (scope must match metadata).
**stderrPatterns** (mitto-k6h): Optional per-agent regex patterns applied by the ACP stderr monitor. `crash` (union with hardcoded baseline → `onCrashDetected` bypasses SDK 60s timeout), `ignore` (suppress from debug log; buffer capture unaffected), `degraded` (plumbed only, behaviour deferred). Compiled once at process start; invalid regex is skipped with a warn (never fatal). CI guard: `make check-stderr-patterns`. Compile happens in `internal/web` (only layer with both `agents.Manager` and ACP-server mapping); `*conversation.CompiledStderrPatterns` is injected into `SharedACPProcessConfig`/`BackgroundSessionConfig` via a per-server-name resolver. See [docs/devel/acp.md § Stderr Pattern Detection](../../docs/devel/acp.md).
