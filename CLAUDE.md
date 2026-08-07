# Mitto — Claude Code Project Memory

Mitto is a multi-agent interface for AI coding agents (Claude Code, Auggie, Cursor) with CLI, Web UI, and native macOS app.

## Build & Test Quick Reference

```bash
make build-mock-acp       # Build mock ACP server (REQUIRED before integration tests)
make test-integration     # Integration tests (needs mock-acp binary)
```

**Details**: See `.augment/rules/00-overview.md` for architecture, package structure, and full build commands.

## Core Data Flow

```
Frontend (Preact) ←WebSocket→ BackgroundSession ←JSON-RPC/stdio→ ACP Agent
```

Key files (in progress decomposition `mitto-dhg.2`):
- `internal/conversation/background_session.go` — Core observer bridge (6,483 LOC → 124 methods being extracted)
- `internal/conversation/bgsession_*.go` — Delegators to extracted components
- `internal/conversation/*_coordinator.go` — Workflow orchestrators (follow-up, auxiliary)
- `internal/conversation/*_manager.go` — State managers (config, queue, title)
- `internal/conversation/*_analyzer.go` — Data analyzers (session, collaborator)
- `internal/web/session_ws.go` — WebSocket `connected` message sends capabilities
- `internal/web/observer.go` — `SessionObserver` interface

## Key Patterns

**Observer Notification:**
```go
bs.notifyObservers(func(o SessionObserver) { o.OnError("msg") })
```

**ACP ContentBlock:** Uses nil-pointer checks, not Type():
```go
if block.Image != nil { /*...*/ } else if block.Text != nil { /*...*/ }
```

**Agent Capabilities:** Advertised during init, check before use:
```go
if len(imageIDs) > 0 && !bs.agentSupportsImages { /* warn */ }
```

**Frontend Capability Flow:** Backend sends in `connected` → `useWebSocket.js` stores → `app.js` passes as prop

## Testing

Integration tests require mock ACP server:
```bash
go build -o tests/mocks/acp-server/mock-acp-server ./tests/mocks/acp-server/
go test -v -tags integration ./tests/integration/inprocess/
```

- Tests use `SetupTestServer(t)` with mock ACP via stdin/stdout JSON-RPC
- Scenarios regex-matched in `tests/fixtures/responses/*.json`
- Test client: `CreateSession()`, `Connect()` (WebSocket), `SendPrompt()`, `UploadImage()`, `LoadEvents()`
- Known issue: `TestWSConn_ForceReconnect_AppliesBackoff` fails if uncommitted changes exist

## Critical Gotchas

- **Image pipeline**: Upload → disk storage → base64 encode → ACP ContentBlock. Only `image_ids` sent in WebSocket; backend loads from disk.
- **Log authoritative source**: Check `events.jsonl` (session dir) when debugging; server logs rotate and have gaps.
- **daisyUI drawer GPU bug**: `.drawer-side` + fixed-position overlay compete for pointer events → blank artifacts. Fix: See `web/static/styles.css` for verified pattern. Do NOT use `translateZ(0)`.
- **Zombie WebSocket recovery**: When phone sleeps or app backgrounded, WS may enter "zombie" state (appearing open but dead). On visibility change or app activate, force-close and reconnect. This is expected behavior — not a bug. See `.augment/rules/23-web-frontend-mobile.md` for resilience patterns.
- **Verify prior edits actually persisted**: Don't trust that a previous turn's file edits are still on disk — session gaps, restarts, or a **concurrent loop conversation** (e.g. a PR-babysitting/cleanup loop sharing the same repo) stashing/resetting the working directory mid-task can silently drop them. Re-check with `git status`/`git diff`/re-view before relying on earlier work; if files vanish unexpectedly, check `git stash list` first — work is often auto-stashed, not lost.
- **Cold-start MCP wedge (mitto-54k) — corrected 2026-07-08: it's a Mitto-side SSE stall, not agent-side**: symptom `⏳ mitto (timed out)`/hung first prompt for minutes. The earlier "agent-side, unfixable in Mitto" diagnosis was **wrong** — it was overturned after the same wedge reproduced in **both** auggie and Claude Code, isolated to workspaces with **concurrent MCP sessions** (e.g. multiple workspace UUIDs + an active loop sharing one `working_dir`). Root cause: `internal/mcpserver/server.go:347` builds the Streamable HTTP handler with `nil` options → stateful mode, where a POST's response can ride the client's per-session **GET SSE stream** instead of the POST body; under concurrency that stream stalls (~97s gap observed, SSE GET held open, never completes) so `initialize` times out even though Mitto's handler already returned 200 in 0ms (`duration_ms=0` in logs is a red herring). Fixed by `mitto-6hr` (P1, APPLIED): `startSSE()` passes `&mcp.StreamableHTTPOptions{JSONResponse: true}` so POST responses resolve inline, independent of the SSE GET — **not** `Stateless: true` (breaks `UIPrompter`/`mitto_ui_options`). See `.augment/rules/42-mcpserver-development.md`. Secondary mitigations (reduce concurrency, don't fix the stall): `mitto-clc` (disable proactive keep-warm pin), `mitto-cgc` (stagger aux-session creation).

## New Agent Capability Checklist

1. Store capability on `BackgroundSession` during ACP init
2. Add public getter; check before use in `PromptWithMeta`
3. Add to WebSocket `connected` message
4. Store in `useWebSocket.js` and pass through `app.js`
5. Update mock ACP server and add integration test

## Go 1.22+ Routing Pattern

`http.ServeMux` method+pattern routing (`r.PathValue()`), no manual path parsing. Route table (`routes.go`) is declarative:
```go
apiRoute{http.MethodGet, "/api/sessions/{id}", s.handleSessionGet},
```

## Frontend authFetch Pattern

Use `authFetch(url, options?)` for all authenticated calls — adds `credentials: "include"` + unified 401 handling. URLs always from `web/static/utils/endpoints.js` (never hardcoded):
```javascript
const response = await authFetch(endpoints.sessions.get(sessionId));
```
Exception: public endpoints (e.g. `/api/supported-runners`) use raw `fetch` with `same-origin`.

## Reusable Config-Driven Components

**Toolbar** (`Toolbar.js`) — segmented-pill action bar from an `items` array (`button`/`dropdown`/`overflow`/`separator`/`spacer`/`custom`). Prefer over bespoke "..." kebab menus.
```javascript
html`<${Toolbar} variant="block" surface="bg-mitto-surface-3" items=${headerToolbarItems} />`
```
Used in `BeadsView.js` (list actions + issue-detail header).

**ShortcutsEditor** (`ShortcutsEditor.js`) — one panel reused for both **global** (Settings dialog) and **folder** (Workspaces dialog) shortcut config. The three consumers (conversations/beadsIssue/tasksList toolbars) merge global + folder shortcuts **at render time**: global entries first, folder entries whose `prompt` duplicates a global one are dropped; any remaining duplicate renders greyed-out via `redundantPromptNames`. Backend: `config.ShortcutButton{Icon, Prompt}`, mirrored `GET/PUT /api/global/shortcuts` (`internal/web/handlers/global_shortcuts.go`). Refresh via `mitto:global_shortcuts_updated`/`mitto:folder_shortcuts_updated` window events — no reload needed. Safe defaults seeded in `config/config.default.yaml` (`shortcuts:`, new installs only).

## Model Selection & Preferred Models

Prompts can declare `preferredModels:` to route to specific ACP models. `selectPreferredModel()` in `constraints.go` picks the best match using configurable match modes (`"contains"`, `"exact"`, `"startsWith"`, `"regex"`, `"lookAlike"`). If the active model already satisfies the preference, it's kept; otherwise applied — avoids unnecessary switches in multi-model sessions.

**Per-prompt transient overrides**: `setActiveModelOnly()` switches models for a prompt's execution **without** recording a `session_change` event (silent; conversation-level baseline is untouched). `restoreBaselineIfOverride()` flips back after the prompt completes. **Contrast**: manual UI selection → `applyConfigOption()` → `cmRecordSessionChange()` → persistent event, updates baseline.

**Config-level tag resolution**: `(*Config).EffectiveModelProfiles()` unions `settings.json`'s `Models` with hardcoded `config.DefaultModelProfiles()` (7 canonical profiles), user wins by name — so `modelTag:` always resolves even when `settings.json` predates/omits `models:`. `make check-model-tags` keeps `config.default.yaml` and the Go defaults in sync and rejects unknown tags in builtin prompts. See `.augment/rules/08-config.md`.

## CEL Tool Evaluation (Fail-Open Behavior)

- **Prompts**: `tools.hasPattern()` returns `true` when the tool list is unknown (cold cache during init), so prompts are not hidden during warm-up
- **Processors**: Always see the real tool list (fail-open is disabled internally)
- Once tools are fetched, evaluation uses the actual list. Useful for tool-gated prompt/processor gating via `enabledWhen`

## MCP Tool Discovery

Two-tier discovery for `enabledWhen`/CEL `tools.*` gating (see `docs/devel/mcp-tool-discovery.md`):
1. **Deterministic** (`internal/mcpdiscovery`): connects directly to configured MCP servers (stdio/http/sse) via `modelcontextprotocol/go-sdk` client and calls `tools/list`. Preferred — no LLM involved.
2. **LLM fallback** (`internal/auxiliary/workspace_manager.go` `fetchMCPToolsViaLLM`): used only when a server can't be reached deterministically. `parseMCPToolsList` (`utils.go`) is **strict**: whole trimmed/unfenced response must be one JSON object with `tools`/`error` keys — no substring or bare-array extraction (that leniency caused false negatives/hallucinated tools). Retries once with a reminder prompt on parse failure or an implausible zero-tools result (checked against the deterministically-known configured server count).
3. **Disk persistence** (mitto-sys.8): deterministic tool lists survive restarts via `appdir.MCPToolsCacheDir()` (`$MITTO_DIR/mcp-tools-cache`), one JSON snapshot per workspace, 15-min TTL (`persistedMCPTools` + `loadPersistedMCPTools`/`savePersistedMCPTools` in `workspace_manager.go`). The **LLM fallback is never written to disk** — in-memory only. `ClearMCPToolsCache` also deletes the snapshot, forcing re-probe.
4. **Restart heuristic re-verify** (mitto-dza, Fix 4): snapshots carry `SchemaVersion` + `AnyUnreachable`. A snapshot flagged suspect (unreachable server at persist time, or pre-mitto-dza schema) is served instantly on load **and** triggers a background async re-probe (`triggerAsyncMCPToolsRefetch`, per-workspace in-flight guard, 60s bounded context). On completion `MCPToolsRefreshedHook` fires; `internal/web/server.go` wires it to broadcast `prompts_changed` with `reason: "mcp_tools_reverified"` so `enabledWhen` tool-gates re-evaluate within seconds — closes the "LLM-only servers vanish across restart" gap without weakening the anti-hallucination guarantee (LLM tools still never written to disk).

**Anti-pattern**: lenient JSON extraction (searching for `{...}` substrings or bare arrays in free-form LLM text) silently accepts malformed/partial answers. Prefer strict whole-response parsing + explicit retry over "try to salvage whatever looks like JSON."

`checkRequiredToolPatterns` (`internal/web/session_ws.go`) no longer runs a blind 30/60/120s `prompts_changed` re-broadcast timer (removed, mitto-sys.12) — it emits one immediate broadcast; late/changed tools surface only via the event-driven watcher and bounded-backoff paths above.

Per-agent `mcp-list.sh` config paths/keys are **not** interchangeable across agents — verify against real docs before writing/trusting one (audit + known-broken scripts: `.augment/rules/42-mcpserver-development.md`).

**Auggie git-root divergence**: `auggie mcp list` resolves `<workspace>` to the **git toplevel**, not the Mitto workspace's `working_dir` — so a workspace whose `working_dir` is a git subdirectory sees servers registered in `<git-root>/.augment/settings.local.json` (not its own `.augment/settings.local.json`). Mitto's `mcp-list.sh` reads `working_dir` literally, so the MCP tab can show servers (e.g. `slack`) the running agent never actually loads. Fix: move servers to the git-root config, register at user scope (`auggie mcp add`, no `--local`), or point `working_dir` at the git root.

## Loop Conversations

**Multi-trigger schema (mitto-r6j, breaking change)**: `loop:` frontmatter's
`trigger:` is a **list** (`[schedule, onCompletion, onTasks]`, one or more),
and each trigger's own fields live in a nested block of the same name
(`schedule:`, `onCompletion:`, `onTasks:`) rather than flat siblings of
`trigger:`. Every listed trigger **arms independently** and stays armed for
the loop's lifetime — e.g. `[onTasks, onCompletion]` reacts to beads changes
AND re-arms after every turn, simultaneously. When two triggers want to fire
in the same window, exactly ONE run is delivered per conversation — the
loser is **coalesced (dropped, not queued)** via a per-session dispatch claim
(`LoopRunner.claimDispatch`); precedence within a tick is `onTasks >
onCompletion > schedule`. `maxIterations`/`maxDuration` are loop-wide,
decremented once per delivered run regardless of which trigger fired it. The
winning trigger is recorded on `PromptMeta.LoopTrigger` and surfaced as
`loop_updated.triggers` (full armed set) alongside the back-compat singular
`loop_updated.trigger`. Runtime `loop.json` (`session.LoopPrompt`) was **not**
schema-versioned or migrated — `EffectiveTriggers()`'s fallback chain already
reads every persisted shape, so live loops from before mitto-r6j keep working
unchanged after upgrade. A `.prompt.yaml` file on the old flat form is
auto-migrated on load (WARN + on-disk line-splice rewrite, comments
preserved, idempotent) by `internal/prompts/migrate`; inline `prompts:` blocks
(`.mittorc`, `settings.yaml`, per-ACP-server) are migrated **in memory only**
via `prompts.DecodeInlineLoop` — WARN, no on-disk rewrite (mitto-opoh). See
[docs/config/prompts.md § Loop Prompts](docs/config/prompts.md#loop-prompts)
and
[docs/devel/message-queue.md § Multi-Trigger Architecture](docs/devel/message-queue.md#loop-prompts-multi-trigger-architecture).

**onCompletion trigger** (event-driven, one of the three triggers above):
- Re-fires automatically 30s after agent finishes each turn (configurable `delay_seconds`)
- Green "Running" pill = `loop_enabled: true`, NOT generic "agent is active" status
- Limited by `max_iterations` and `max_duration_seconds` (shared with any other armed trigger)
- Free-text loop prompts NOT sent to frontend → selector can't display them (UI gap)
- `app.js` line ~1928: `headerLoopState()` returns `{ state, label, badgeClass }` pill object
- Issue `mitto-36nm` tracks UI clarity improvement (prompt visibility + pill disambiguation)

**Persistence symmetry (LoopStore, `internal/session/loop.go`)**: un-loop calls `Detach()` (saves settings to a slot, clears active config); re-loop/restore reads it back via `GetSaved()`. A **fresh** loop create must call `ClearSaved()` right after `Set()` so a stale saved slot doesn't leak into a later un-loop — done identically in REST (`session_loop_write.go` `handleSetLoop`) and MCP (`mcpserver/server.go` create-loop path) to keep both interfaces symmetric.

**Spawning loop children — `arguments` vs `loop_arguments`**: `mitto_conversation_new`'s `arguments:` fills `.Args` only on the initial prompt; `loop_arguments:` fills `.Args` on every re-fire. When the spawned child is itself a loop, MIRROR the same resolved value into both — otherwise re-fires render with empty `.Args`, so positive-match gates (`{{ if eq .Args.Commit "true" }}`) silently resolve false and default-on gates silently fall back to the default instead of the operator's choice (bug `mitto-rtdr`, fixed 25ed20d9). `TestLoopProcessingSpawns_MirrorArgumentsIntoLoopArguments` pins this on `beads-issues/loop-processing.prompt.yaml`: for each `SubmitStrategy` value (`Commit`/`Pull Request`/`None`, plus unset) every §A/§B/§C spawn block must carry both maps with the identical literal. Prefer default-on gates (`{{ if ne .Args.X "false" }}`) since parameter defaults are NOT auto-merged into `.Args` at render time.

**`coalesceDuringBusy` silent-swallow**: when an `onTasks` subtree is busy, fs-watcher fires do NOT dispatch — they arm a quiescence rebase timer that silently updates the baseline on quiescence (`onTasks: baseline rebased after idle+quiescence`). Intentional coalescing, but supervisor loops can silently miss user-driven state changes for minutes until an external event re-fires the watcher. This is orthogonal to the multi-trigger dispatch coalescing above (which drops a *whole run* from a losing trigger); `coalesceDuringBusy` instead governs whether the `onTasks` trigger's *own* busy-window changes get folded silently into the next baseline rebase or trigger one more accumulated-delta fire.

## Tokensave Rule (Mandatory)

**NEVER use Explore agents for code research when tokensave is available.** Use `tokensave_context`, `tokensave_search`, `tokensave_callees`, `tokensave_callers`, `tokensave_impact`, `tokensave_node`, `tokensave_files`, or `tokensave_affected` first. See CLAUDE.md in project root for full details.
