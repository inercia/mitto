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
- **Verify prior edits actually persisted**: Don't trust that a previous turn's file edits are still on disk (session gaps, restarts, or reverted stashes can silently drop them). Before continuing/relying on earlier work, re-check with `git status`/`git diff` or re-view the file rather than assuming.

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

Prompts can declare `preferredModels:` to route to specific ACP models. `selectPreferredModel()` in `constraints.go` picks the best match using configurable match modes (`"contains"`, `"exact"`, `"startsWith"`, `"regex"`, `"lookAlike"`). **Key insight**: If the active model already satisfies the preference, it's kept; otherwise the preference is applied. This avoids unnecessary model switches in multi-model sessions.

**Per-prompt transient overrides**: When a prompt declares `preferredModels`, `setActiveModelOnly()` temporarily switches models for that prompt's execution **without** recording a `session_change` event. This is **intentional**:
- Baseline model (conversation-level setting) remains unchanged
- No "Model changed to X" message in timeline (silent override)
- After prompt completes, `restoreBaselineIfOverride()` flips model back to baseline
- Result: Heavy-lift work runs on cheaper models (e.g., Sonnet) while conversation stays on your chosen baseline (e.g., Opus)

**Contrast**: Manual model selection (via UI dropdown) → `applyConfigOption()` → `cmRecordSessionChange()` → records persistent `session_change` event and updates baseline.

## CEL Tool Evaluation (Fail-Open Behavior)

- **Prompts**: `tools.hasPattern()` returns `true` when the tool list is unknown (cold cache during init), so prompts are not hidden during warm-up
- **Processors**: Always see the real tool list (fail-open is disabled internally)
- Once tools are fetched, evaluation uses the actual list. Useful for tool-gated prompt/processor gating via `enabledWhen`

## Loop Conversations

**onCompletion trigger** (distinct from schedule-based loop):
- Re-fires automatically 30s after agent finishes each turn (configurable `delay_seconds`)
- Green "Running" pill = `loop_enabled: true`, NOT generic "agent is active" status
- Limited by `max_iterations` and `max_duration_seconds`
- Free-text loop prompts NOT sent to frontend → selector can't display them (UI gap)
- `app.js` line ~1928: `headerLoopState()` returns `{ state, label, badgeClass }` pill object
- Issue `mitto-36nm` tracks UI clarity improvement (prompt visibility + pill disambiguation)

## Tokensave Rule (Mandatory)

**NEVER use Explore agents for code research when tokensave is available.** Use `tokensave_context`, `tokensave_search`, `tokensave_callees`, `tokensave_callers`, `tokensave_impact`, `tokensave_node`, `tokensave_files`, or `tokensave_affected` first. See CLAUDE.md in project root for full details.
