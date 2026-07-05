# MCP Tool Discovery (ADR)

Spike outcome for **mitto-1ae.2**. Decides how to make `Tools.HasPattern(...)`
(used in `enabledWhen` for external-tool gates like `jira_*`/`github_*`/`slack_*`)
trustworthy. The sibling bug mitto-1ae.1 already removed the Mitto-native
`mitto_conversation_*` gates in favor of deterministic `Permissions.*` — this
doc is only about the remaining **external MCP tool** discovery.

## Context / current mechanism

Mitto has no way to ask an ACP agent "what tools do you have" directly, so it
asks the agent's own LLM to introspect itself:

- `WorkspaceAuxiliaryManager.FetchMCPTools` (`internal/auxiliary/workspace_manager.go:272`)
  sends `FetchMCPToolsPromptTemplate` (`internal/auxiliary/prompts/fetch_mcp_tools.txt`)
  — "List ALL MCP tools currently available to you... respond ONLY with JSON" —
  to a dedicated auxiliary ACP session, then `parseMCPToolsList`
  (`internal/auxiliary/utils.go:174`) tolerantly parses the free-form reply
  (handles markdown fences, a bare array, surrounding prose).
- Results are cached **in-memory, per workspace** in `mcpToolsCache`
  (`workspace_manager.go:52`); **only non-empty results are cached**
  (`workspace_manager.go:339`), so an empty/failed fetch never overwrites a
  good cache — but a **short-but-non-empty hallucinated list also gets cached**.
- `ToolsContext.Available` (`internal/config/cel_context.go:236`) is set `true`
  **only** when `GetCachedMCPTools` finds an entry (`internal/web/session_api.go:350,490`).
  While `Available=false`, `hasPattern`/`hasAllPatterns`/`hasAnyPattern`
  (`internal/config/templatefuncs.go:23`) **fail open** (return `true`) so
  prompts aren't hidden during warm-up — this is also why `mitto-1ae.1` had to
  move deterministic capabilities off `Tools.HasPattern`. Processors instead
  force `Tools.Available=true` unconditionally (`internal/processors/hook.go:272`),
  so they never get the warm-up grace period.
- Triggers: `SessionManager.ensureMCPToolsFetch` fires an async fetch on first
  message (`internal/conversation/session_manager.go:2756`); `session_ws.go`'s
  WS-connect path (`internal/web/session_ws.go:1244`) is the fallback trigger,
  and `checkRequiredToolPatterns` (`session_ws.go:1664`) re-broadcasts
  `prompts_changed` at 30s/60s/120s **just in case tools appear late** — a
  symptom of not trusting the result, not a real refresh mechanism.
- Separately, `CheckRequiredToolPatterns` (`workspace_manager.go:392`) sends a
  **second**, per-pattern LLM query to the same auxiliary session — also
  non-deterministic.

## Findings

### Q1 — Can we get a real tool list deterministically?

**ACP protocol itself: no.** Checked the vendored SDK
(`github.com/coder/acp-go-sdk@v0.12.0`, `types_gen.go`): `McpServers` appears
only in `InitializeRequest`/`NewSessionRequest`/`ResumeSessionRequest` — it is
**client→agent** (Mitto tells the agent which MCP servers to wire up for the
session; see `internal/acp/connection.go:271` `NewSession`, which currently
passes `McpServers: []acp.McpServer{}`). `AgentCapabilities.McpCapabilities`
only advertises transport support booleans (`Http`, `Sse`), not tool names.
`ToolCall` types describe tool **use** during a turn, not a queryable catalog.
There is no `tools/list`-equivalent RPC exposed to the client in this SDK.

**Real MCP `tools/list` against agent-configured servers: yes, and mostly
already wired for server _discovery_.** `agents.Manager.ListMCPServers`
(`internal/agents/manager.go:299`, `agents.CommandMCPList`) already runs each
agent's `mcp-list.sh` and returns `[]MCPServer{Name, Command, Args, URL, Env}`
**deterministically** (parses the agent's own settings file, e.g.
`~/.claude/settings.json`, `~/.augment/settings.json` merged with
project/local scopes for auggie — verified for augment, claude-code, amp,
cline, kilo, codex, gemini, mistral-vibe; `junie`'s script is a stub always
returning `{"servers": []}`). This gives server **identity**, not tool names.
To get tool names, Mitto would need to actually connect to each server and
call `tools/list` — and it already depends on a client-capable MCP SDK:
`github.com/modelcontextprotocol/go-sdk@v1.4.0` provides `mcp.NewClient`,
`mcp.CommandTransport` (stdio, `mcp/cmd.go`), `mcp.StreamableClientTransport`
/ `mcp.SSEClientTransport` (http/sse, `mcp/streamable.go`, `mcp/sse.go`), and
`ClientSession.ListTools` (`mcp/client.go:982`). Today this SDK is only used
server-side (`internal/mcpserver`); nothing currently uses its client side.

**Feasibility table:**

| Agent (ACP type)                                                             | Server discovery (`ListMCPServers`)    | stdio `tools/list`                                                                                                                                                     | http/sse `tools/list`                                                                                                                                                                |
| ---------------------------------------------------------------------------- | -------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| augment (auggie), claude-code, amp, cline, kilo, codex, gemini, mistral-vibe | Deterministic (local settings JSON)    | High feasibility via `mcp.CommandTransport`; effort=moderate (process lifecycle + bounded timeout); risk=slow/side-effecting cold starts for heavy `npx`-based servers | Medium feasibility via `StreamableClientTransport`/`SSEClientTransport`; risk=`agents.MCPServer` has no `Headers` field, so servers needing bearer/auth headers can't be reached yet |
| junie                                                                        | `mcp-list.sh` is an unimplemented stub | N/A until server discovery is implemented                                                                                                                              | N/A                                                                                                                                                                                  |
| cursor, opencode, github-copilot, goose, qwen-code                           | Not inspected in this spike            | Unknown — needs per-agent script audit                                                                                                                                 | Unknown                                                                                                                                                                              |

### Q2 — Hardening the LLM fallback (for agents/servers a direct connection can't reach)

- Keep the JSON-object schema (`{"tools":[...]}` / `{"error":"..."}"`) but
  reject anything that doesn't parse as that exact shape — no bare-array
  fallback, no "grab the biggest `{...}` substring" leniency for new fetches.
- Add a **plausibility check**: if `ListMCPServers` reports N configured
  servers but the LLM reports 0 tools, treat the result as suspect and retry
  once with a stricter reminder before accepting an empty answer.
- One automatic retry on a parse failure or an empty response; only give up
  (and keep the last-known-good cache) after that retry also fails.

### Q3 — Cache & fail-open policy (the flicker fix)

1. **Last-known-good.** Never overwrite a cached non-empty tool list with an
   empty or shorter one without corroboration: require **two consecutive**
   independent negative fetches (different trigger points or time-separated)
   before downgrading any previously-observed tool/pattern to absent.
2. **Fail-open only during genuine cold start.** `Tools.Available=false`
   (pattern-match fails open) is legitimate only until the **first** fetch for
   a workspace completes, ever. After that, `Available` stays `true` for the
   process lifetime and pattern truth is name-based only (fail-closed) — this
   is already the code's intent (`session_api.go:350`) but should become an
   explicit, tested invariant rather than a side effect of "only cache
   non-empty results".
3. **Persistence.** Persist the **real-MCP-derived** list to disk per
   workspace (it reflects static config, so it's stable) with a TTL-based
   refresh (e.g. 15 min) plus an explicit "refresh" action and the existing
   `ClearMCPToolsCache` invalidation hook. Do **not** persist the LLM-fallback
   result across restarts — it is inherently less trustworthy; keep it
   in-memory only, as today.
4. **Retire blind timed retries.** The 30s/60s/120s re-broadcast loop in
   `checkRequiredToolPatterns` (`session_ws.go:1684`) exists only because the
   LLM path is slow and unreliable. A bounded (5-10s timeout) direct MCP
   connection resolves synchronously, so this polling can be removed once
   real discovery lands for a given agent/transport.

### Q4 — Dynamic / late-starting servers (the tool list changes over time)

MCP servers can come online _after_ the ACP agent starts (e.g. a slow `npx`
cold start that is only ready ~30s in), and a server may add/remove tools
mid-session. The Q3 policy above is written for _flicker_ (stop tools spuriously
disappearing/reappearing) and, taken alone, would **regress** this legitimate
case: once the first fetch completes, `Available` latches `true` and matching
becomes fail-closed (Q3.2); last-known-good guards only against _downgrades_,
not _appearances_ (Q3.1); and retiring the 30s/60s/120s re-broadcast (Q3.4)
plus a 15-min TTL (Q3.3) removes the only fast late-detection path — a tool that
appears at t=40s could stay hidden for up to 15 min.

Refinements so late/dynamic tools surface without reintroducing flicker:

1. **Per-server availability state, not one global `Available` bit.**
   Distinguish "server _configured_ (from `ListMCPServers`) but not yet
   _reachable_" from "server reachable, tool genuinely absent." Keep fail-open +
   short backoff for the former (that server's namespace only); treat only the
   latter as a real negative for the two-consecutive-negatives rule. A single
   global latch cannot express this.
2. **Event-driven refresh via MCP `notifications/tools/list_changed`.** MCP
   servers advertise a `listChanged` capability and push a notification when
   their tool set changes; a held `ClientSession` can react immediately. Verify
   the vendored `modelcontextprotocol/go-sdk` client exposes a handler
   (`mcp/client.go`) — if so, this is the proper replacement for blind polling.
3. **Bounded backoff for configured-but-unreachable servers.** When a direct
   `tools/list` times out (the 5-10s bound from Q1) against a still-starting
   server, schedule short exponential-backoff retries (up to a cap) rather than
   waiting for the flat TTL — and do **not** cache that timeout as a negative.
4. **Do not remove the fast re-broadcast (Q3.4) until an event-driven or
   backoff-based equivalent exists**, otherwise late-starting servers regress.

Note: server _identity_ is unaffected by startup timing — `ListMCPServers` reads
static config, so the set of servers to probe is known immediately; only tool
_names_ depend on a live connection.

## Recommendation / Decision

**Prefer real MCP `tools/list` via the servers already discovered by
`ListMCPServers`, for every transport the vendored `modelcontextprotocol/go-sdk`
client supports (stdio, http, sse). Fall back to the hardened LLM enumeration
only when a server can't be reached this way** (unsupported agent, missing
`mcp-list.sh` support, auth-required HTTP endpoint, or a live connection
error). Cache = last-known-good with the two-consecutive-negatives downgrade
rule; fail-open is a cold-start-only exception, never a steady-state behavior.
This removes LLM non-determinism entirely for the common case (locally
configured stdio/plain-URL MCP servers for auggie/claude-code) and confines
the flaky path to the genuinely hard cases.

**Caveat (Q4):** the cold-start-only fail-open above must be scoped
_per server_, not as one global latch, and paired with event-driven refresh
(`notifications/tools/list_changed`) or bounded backoff so late-starting servers
still surface. The fast re-broadcast must stay until that equivalent exists.

## Proposed follow-up implementation issues (not implemented here)

- Direct MCP client tool discovery over stdio (`mcp.CommandTransport` +
  `ListTools`), wired to `agents.Manager.ListMCPServers`.
- Direct MCP client tool discovery over http/sse (`StreamableClientTransport`
  / `SSEClientTransport`).
- Add a `Headers` field to `agents.MCPServer` (+ each `mcp-list.sh`) to support
  authenticated HTTP/SSE MCP servers.
- Implement a real `mcp-list.sh` for `junie` (currently a stub).
- Harden `FetchMCPTools`'s LLM fallback: strict schema validation,
  retry-on-incomplete, plausibility check against `ListMCPServers` count.
- Implement the last-known-good / two-consecutive-negatives cache policy in
  `WorkspaceAuxiliaryManager.mcpToolsCache`.
- Persist the real-MCP-derived tools cache to disk per workspace with TTL
  refresh + manual refresh action.
- Remove the blind 30s/60s/120s re-broadcast retries in `session_ws.go` **only
  after** an event-driven or backoff-based late-detection replacement exists
  (see Q4) — removing it earlier regresses late-starting servers.
- Scope fail-open state **per configured server** (not one global `Available`
  bit) so "configured-but-unreachable" keeps that namespace fail-open with
  bounded backoff, while "reachable, tool absent" is a genuine negative (Q4).
- Implement event-driven refresh via MCP `notifications/tools/list_changed`:
  verify the vendored `modelcontextprotocol/go-sdk` client exposes a handler and
  react to it on held `ClientSession`s to surface late/changed tools (Q4).
- Audit `mcp-list.sh` support for `cursor`, `opencode`, `github-copilot`,
  `goose`, `qwen-code` (not inspected in this spike).
