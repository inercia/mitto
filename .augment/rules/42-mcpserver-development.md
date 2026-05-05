---
description: MCP server development patterns and tool implementation
globs:
  - "internal/mcpserver/**/*"
keywords:
  - MCP server
  - MCP tool
  - AddTool
  - mcp-go
  - Streamable HTTP
  - STDIO mode
  - session registration
  - session_id parameter
---

# MCP Server Development

The Mitto MCP server (`internal/mcpserver/`) provides a **single global server** with two types of tools:

1. **Global tools** - Always available (list conversations, get config, runtime info)
2. **Session-scoped tools** - Require `session_id` parameter (UI prompts, send prompt, get current session)

## Architecture

Single global MCP server at `http://127.0.0.1:5757/mcp`. Two tool classes:
- **Global tools** (no session): `mitto_conversation_list`, `mitto_get_config`, `mitto_get_runtime_info`
- **Session-scoped tools** (require `self_id`): UI prompts, conversation control, history, prompt management (`mitto_prompt_list/get/update`), periodic control (`mitto_conversation_set_periodic`, `mitto_conversation_run_periodic_now`)

## Transport Modes

| Mode               | Use Case                          | Configuration                                   |
| ------------------ | --------------------------------- | ----------------------------------------------- |
| **HTTP** (default) | Remote access, Auggie integration | `TransportModeHTTP`, runs on port 5757          |
| **STDIO**          | Subprocess usage, Claude Desktop  | `TransportModeSTDIO`, reads/writes stdin/stdout |

HTTP: `mcpserver.NewServer(Config{Mode: TransportModeHTTP, Address: "127.0.0.1:5757"}, sessionsDir, settingsPath).Start(ctx)`

STDIO: `server.RunSTDIO(ctx)` — blocks until context cancelled. Used by `mitto mcp --proxy-to <url>` for stdio MCP proxy.

## Adding New Tools

Handler signature (3-arg form — SDK unmarshals input automatically):

```go
func (s *Server) handleFoo(ctx context.Context, req *mcp.CallToolRequest, input FooInput) (*mcp.CallToolResult, FooOutput, error) {
    // 1. Validate self_id
    sessionID, err := s.resolveSessionID(ctx, req, input.SelfID)
    if err != nil {
        return nil, FooOutput{Error: err.Error()}, nil
    }
    // 2. Do work ...
    return nil, FooOutput{Success: true}, nil
}
```

Register:
```go
mcp.AddTool(mcpSrv, &mcp.Tool{Name: "mitto_foo", Description: "..." + selfIDNote}, s.handleFoo)
```

**Rules:**
- Output must be a **struct** (not slice/primitive) — MCP SDK requirement
- Initialize slice fields as `[]T{}` not nil — Go encodes nil as JSON `null`, ACP rejects that
- `selfIDNote` constant is already defined in `server.go` — append it to Description

**Reading session store:**
```go
s.mu.RLock()
store := s.store
s.mu.RUnlock()
events, err := store.ReadEvents(sessionID)
// Decode typed data:
data := session.DecodeEventData(event)  // returns typed union; check each field
```

See `40-debugging.md` for using MCP tools for debugging.

## Session Registration

`BackgroundSession` registers/unregisters with the global MCP server:

```go
bs.globalMcpServer.RegisterSession(bs.persistedID, bs, bs.logger)   // on start/unarchive
bs.globalMcpServer.UnregisterSession(bs.persistedID)                 // on archive/delete
```

Session-scoped tool pattern: accept `session_id` param → `s.getSession(id)` → `s.checkSessionFlag(id, flag)` → use `reg.uiPrompter`.

New flags: define `const FlagXxx` in `internal/session/flags.go`, add to `AvailableFlags`, check with `checkSessionFlag()`.

**Key rules:**
- No per-session MCP servers — all tools on the global server
- All session-scoped tools require `session_id` parameter
- `SessionManager` interface (in `server.go`) has ~20 methods including workspace/prompt helpers (`GetWorkspacePrompts`, `GetWorkspacePromptsDirs`, `GetWorkspace`, etc.). When extending: add stub methods to **all 7 mock types** in `server_test.go`. Stubs returning nil/zero are acceptable for non-tested methods.

## Optional Late-Bound Dependencies

Some dependencies (e.g. `PeriodicRunner`) are initialized after the MCP server and wired in via setter methods rather than through `Dependencies`:

```go
// In internal/web/server.go — after s.periodicRunner.Start():
if s.mcpServer != nil {
    s.mcpServer.SetPeriodicRunner(s.periodicRunner)
}
```

The `PeriodicRunner` interface (defined in `mcpserver/server.go`) is satisfied by `*web.PeriodicRunner`. Use setter methods (not `Dependencies`) when a dependency must exist before `NewServer()` completes but the dependency itself starts later.

## Processor Auxiliary Session MCP Access

Processor auxiliary sessions (purpose prefix `"processor:"`) get a stdio MCP proxy so the agent can call Mitto tools. Configured in `internal/web/acp_process_manager.go` via `ACPProcessManager.MCPServerURL`. Non-processor auxiliary sessions (title-gen, follow-up, etc.) do NOT get MCP access.

See `docs/devel/mcp.md` for detailed documentation.

## Agents Package (`internal/agents`)

Manages agent definitions loaded from `agents/builtin/<dir>/` directories. Each agent has `metadata.yaml` with an `acpId` field that maps from ACP server type to agent directory name.

**Key gotcha:** ACP type ≠ agent directory name. Example: ACP type `"auggie"` → directory `"augment"` (matched via `acpId` in `metadata.yaml`). Always use `GetAgentByACPId` to resolve this mapping.

```go
mgr := agents.NewManager(agentsDir, logger)

// Look up by ACP server type (NOT directory name)
agent, err := mgr.GetAgentByACPId(acpType)  // acpType e.g. "auggie", "claude-code"

// Check if agent supports a command
if agent.HasCommand(agents.CommandMCPList) { ... }

// Run mcp-list command (script reads optional JSON from stdin)
output, err := mgr.ListMCPServers(ctx, agent.DirName, &agents.MCPListInput{Path: workingDir})
// output.Servers: []MCPServer{Name, Command, Args, URL}
```

API endpoint: `GET /api/workspace-mcp-tools?acp_server=NAME&dir=PATH` (handler in `config_handlers.go`). Returns `{servers, agent_name, error?, message?}`.
