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

## Adding New Tools

Handler signature (3-arg form — SDK unmarshals input automatically):

```go
func (s *Server) handleFoo(ctx context.Context, req *mcp.CallToolRequest, input FooInput) (*mcp.CallToolResult, FooOutput, error) {
    // 1. Resolve self_id (always use resolveSelfIDWithMCP in handlers — 3-phase lookup)
    realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
    if realSessionID == "" {
        return nil, FooOutput{Error: fmt.Sprintf("session not found: self_id '%s' could not be resolved", input.SelfID)}, nil
    }
    // 2. Do work ...
    return nil, FooOutput{Success: true}, nil
}
```

**Session ID resolution:** Use `resolveSelfIDWithMCP(selfID, req)` in all handlers (3-phase: direct lookup → ACP correlation → MCP session cache). Use `resolveSelfID(selfID)` only when no `*mcp.CallToolRequest` is available (rare).

Register with `mcp.AddTool(mcpSrv, &mcp.Tool{Name: "mitto_foo", Description: "..." + selfIDNote}, s.handleFoo)`.

**Rules:**
- Output must be a **struct** (not slice/primitive) — MCP SDK requirement
- Initialize slice fields as `[]T{}` not nil — Go encodes nil as JSON `null`, ACP rejects that
- `selfIDNote` constant defined in `server.go` — append it to Description
- Store access: `s.mu.RLock(); store := s.store; s.mu.RUnlock()`. Decode: `session.DecodeEventData(event)`.

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

## Cross-Workspace Operations

Any tool that operates on a target conversation in a different workspace than the caller MUST:
1. Check `FlagCanInteractOtherWorkspaces` flag
2. Call `confirmCrossWorkspaceOperation` (blocking UI confirmation — always required, no bypass)

```go
if callerMeta.WorkingDir != targetWS.WorkingDir {
    if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
        return nil, Out{Error: "cross-workspace ops require 'can_interact_other_workspaces' flag"}, nil
    }
    if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "description", targetWS); err != nil {
        return nil, Out{Error: err.Error()}, nil
    }
}
```

**SessionManager workspace methods:**
- `sm.GetWorkspaces()` — all configured workspaces
- `sm.GetWorkspacesForFolder(folder)` — workspaces for a specific directory
- `sm.GetWorkspaceByUUID(uuid)` — lookup by workspace UUID

**Workspace lookup:** build two maps from `sm.GetWorkspaces()`: exact key `workingDir+"|"+acpServer` and dir-only fallback. Try exact first, fall back to dir-only.

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

## Input Validation in Tools

Reject invalid inputs with errors that guide AI retry behavior — **never silently truncate or fix**:
```go
if len([]rune(question)) > maxQuestionLen {
    return nil, Out{}, fmt.Errorf("question too long (%d chars, max %d); print context as a message first, then call with a concise question", len([]rune(question)), maxQuestionLen)
}
```
Also document limits in tool descriptions upfront so AI agents know constraints before calling.

## Agents Package (`internal/agents`)

Agents defined in `agents/builtin/<dir>/` with `metadata.yaml`. **Key gotcha:** ACP type ≠ directory name (e.g. `"auggie"` → `"augment"`). Always use `GetAgentByACPId(acpType)`.

```go
mgr := agents.NewManager(agentsDir, logger)
agent, err := mgr.GetAgentByACPId(acpType)  // e.g. "auggie", "claude-code"
if agent.HasCommand(agents.CommandMCPList) {
    output, err := mgr.ListMCPServers(ctx, agent.DirName, &agents.MCPListInput{Path: workingDir})
    // output.Servers: []MCPServer{Name, Command, Args, URL}
}
```

API endpoint: `GET /api/workspace-mcp-tools?acp_server=NAME&dir=PATH` (handler in `config_handlers.go`).
