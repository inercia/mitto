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
- **Session-scoped tools** (require `self_id`): UI prompts, conversation control, history, prompt management (`mitto_prompt_list/get/update`), loop control (`mitto_conversation_set_loop`, `mitto_conversation_run_loop_now`)

## Cold-Start MCP Wedge (mitto-54k) — Definitive Diagnosis: Agent-Side, Not Fixable in Mitto

Mitto's inbound `/mcp` (`initialize`+`tools/list`) was **falsified** as the cause by direct probing (~1.5ms single, 13ms/0 errors at 12-way concurrency; regression-guarded by `internal/mcpserver/server_fastpath_test.go`). **Root cause (mitto-54k.7 v3, 2026-07-08)**: auggie (v0.32.0) **hard-gates the first prompt's first token on ALL its configured MCP servers finishing `initialize`** (stderr: `🔌 Waiting for N MCP server(s) to initialize...`), and forks **~6 duplicate copies of every MCP server in one simultaneous burst**; the differentiator is spawn **timing** (I/O thundering-herd), **not** CPU/server-count/warmth — a wedged auggie sits at ~2% CPU in state S (I/O-blocked). No flag exists to make this lazy/bounded/non-blocking; it's agent-side and unfixable in Mitto.

`mitto-54k.3` (warm-once barrier, `internal/acpproc/shared_acp_process.go`) and `mitto-54k.4` (defer `session/new`/`LoadSession` to first-prompt time) shipped — `session/new` reports ready in ~250ms — but only **relocate** auggie's wait to first-prompt-time (fast create, then a hung first prompt for 2-5 min). Don't re-chase as a 54k.3/54k.4 regression. Child tickets: `mitto-clc` (P1) — inactivate the *proactive* always-on keep-warm pin/re-warm (it piles RPC load onto an already-wedged agent instead of letting it recover); `mitto-cgc` (P2) — stagger aux-session creation (mcp-check + mcp-tools currently bunched within ~10ms per workspace).

**Mitigations (none are Mitto code fixes)**: trim unused MCP servers from the workspace's agent config; file an upstream auggie feature request for lazy MCP init (the only real fix).

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

Some dependencies (e.g. `LoopRunner`) are wired in via setter methods (`s.mcpServer.SetLoopRunner(s.loopRunner)` in `internal/web/server.go`, after `s.loopRunner.Start()`) rather than through `Dependencies`, since they must exist before `NewServer()` completes but start later. The `LoopRunner` interface (in `mcpserver/server.go`) is satisfied by `*web.LoopRunner`.

## Processor Auxiliary Session MCP Access

Processor auxiliary sessions (purpose prefix `"processor:"`) get a stdio MCP proxy so the agent can call Mitto tools. Configured in `internal/web/acp_process_manager.go` via `ACPProcessManager.MCPServerURL`. Non-processor auxiliary sessions (title-gen, follow-up, etc.) do NOT get MCP access. See `docs/devel/mcp.md` for detailed documentation.

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

**`mcp-list.sh` audit (mitto-sys.11)** — scripts are often copy-pasted from the claude-code template but never repointed at the target agent's real config path/key/shape, so `ListMCPServers` silently returns empty. Verify against actual docs/source when adding or fixing one:

| Agent | Status | Notes |
|---|---|---|
| cursor, goose | OK | `~/.cursor/mcp.json`/`mcpServers`; `~/.config/goose/config.yaml`/`extensions` |
| opencode | BROKEN (mitto-sys.13) | wrong path/key (`mcp` not `mcpServers`), command-as-array, `environment` not `env` |
| github-copilot | BROKEN (mitto-sys.14) | wrong path: real is `~/.copilot/mcp-config.json` |
| qwen-code | BROKEN (mitto-sys.15) | wrong path: real is `~/.qwen` |
| junie | stub (mitto-sys.10) | always returns `{"servers": []}` |

**Auggie git-root divergence** (not a script bug): `auggie mcp list` resolves `<workspace>` to the **git toplevel**, not the target `workingDir` — so when `workingDir` is a git subdirectory, `mcp-list.sh` (which reads `<workingDir>/.augment/settings.local.json` literally) can report servers (e.g. `slack`) that auggie itself never loads (it reads `<git-root>/.augment/settings.local.json` instead). Verify workspace vs. git-root config before trusting the MCP tab for auggie workspaces nested in a larger repo.
