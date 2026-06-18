---
description: Prompt system architecture, workspace prompts, PromptsCache, merging priority, enable/disable mechanism, API endpoints
globs:
  - "internal/config/prompts*.go"
  - "internal/config/workspace_rc*.go"
  - "internal/web/session_api.go"
  - "web/static/app.js"
keywords:
  - prompts
  - WebPrompt
  - PromptsCache
  - MergePrompts
  - MergePromptsKeepDisabled
  - workspace-prompts
  - enabledWhen
  - predefinedPrompts
  - toggle-enabled
  - prompts menu
  - .mitto/prompts
---

# Prompt System

## Architecture Overview

Prompts are predefined text snippets shown in the ChatInput "Insert predefined prompt" menu. They come from multiple sources and are merged server-side into a single list per workspace.

```
┌──────────────────────────────────────────────────────────────────────┐
│              GET /api/workspace-prompts?dir=...&session_id=...       │
│                          (Single Source of Truth)                     │
│                                                                      │
│  Priority (lowest → highest):                                        │
│  1. Global file prompts    (MITTO_DIR/prompts/*.prompt.yaml)         │
│  2. Settings prompts       (settings.json .prompts)                  │
│  3. ACP server-specific    (prompts with acps: field + inline)       │
│  4. Workspace dir prompts  (.mitto/prompts/*.prompt.yaml)            │
│  5. Workspace inline       (.mittorc prompts section)                │
│                                                                      │
│  Filters: enabled:false removed, enabledWhen evaluated               │
└──────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
              Frontend: predefinedPrompts = workspacePrompts
              (No client-side merge — backend does everything)
```

## Prompt File Format (`.prompt.yaml`)

```yaml
name: "Review Code"
description: "Review code for quality"
group: "Code Quality"
backgroundColor: "#4a90d9"
enabled: true
enabledWhen: "acp.matchesServerType('augment') && tools.hasPattern('filesystem_*')"
prompt: |
  Please review the following code for quality, readability, and potential bugs.
```

**Removed fields**: `enabledWhenACP` and `enabledWhenMCP` have been fully removed from the codebase. If encountered in old code or docs, replace with equivalent `enabledWhen` CEL expressions.

## Key Types

`WebPrompt`: Name, Prompt, Description, Group, BackgroundColor, Icon, Source ("builtin"|"file"|"settings"|"workspace"), Enabled (*bool: nil=enabled, false=disabled), EnabledWhen (CEL, server-side only), Periodic (non-nil = periodic conversation).

`PromptPeriodic.MaxIterations`: Caps scheduled runs; effective cap = min(prompt maxIterations, config default 100, hardcoded 1000). Backend auto-disables (not archives) when hit.

## Merging & Caching

`MergePrompts()` filters disabled; `MergePromptsKeepDisabled()` keeps `enabled:false` for dialogs. PromptsCache auto-refreshes `MITTO_DIR/prompts/` on changes.

## API Endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /api/workspace-prompts?dir=...&session_id=...` | Fully merged prompt list (single source of truth for menu) |
| `GET /api/workspace-prompts?dir=...&include_global=true` | All prompts including disabled (for WorkspacesDialog toggles) |
| `PUT /api/workspace-prompts/toggle-enabled` | Toggle prompt enabled/disabled state |

### Toggle-Enabled Logic

Disable: set `enabled: false` in `.mitto/prompts/X.prompt.yaml` or `.mittorc` prompts section. Re-enable: remove the `enabled: false` entry.

## Menu-Driven Prompt Sends (Named-Prompt Mechanism)

All menu-driven prompt sends (prompts menu, Cmd+/ slash picker, conversation seeding, beadsIssues, beadsList) use `prompt_name` — **never POST the full prompt body**:

- **One shared frontend helper** (`web/static/hooks/useConversationSeeding.js`) builds every seed request:
  - `seedConversationWithPrompt(sessionId, prompt, {arguments})` → POST `{prompt_name, arguments}` to existing session queue
  - `startConversationWithPrompt({workingDir, acpServer, name, beadsIssue, prompt, arguments, periodic?})` — two paths:
    - **No `periodic`**: POST `{initial_prompt_name, arguments}` to `POST /api/sessions` (atomic create+seed, existing behavior)
    - **With `periodic: { value, unit, at?, maxIterations? }`**: POST `POST /api/sessions` without `initial_prompt_name`, then PUT `/api/sessions/{id}/periodic` with `{ prompt_name, frequency, enabled: true, max_iterations }`. `at` (UTC HH:MM) included only for `unit === "days"`.
  - `configurePeriodicSchedule(sessionId, prompt, periodic, {fetchImpl?})` — standalone PUT helper (also exported for testing). Resolves `max_iterations` from the dialog value, then the prompt default; positive sent as-is, `0` = unlimited.
  - `makePeriodicNow(sessionId, prompt, {fetchImpl?})` — convert a regular conversation to periodic: PUT periodic (prompt's declared defaults + `max_iterations`), then `POST /api/sessions/{id}/periodic/run-now` (`reset_timer: true`) to fire the first run. No dialog.
- **Periodic menu branching (context-aware)**: when `prompt.periodic` is non-null, the app dispatcher (`handleSendPromptToConversation` in `app.js`) calls `decidePeriodicAction(session)` and branches:
  - `"new-periodic"` (no session) → open `PeriodicScheduleDialog` (pre-filled from defaults, incl. **max runs**) → on confirm call `startConversationWithPrompt` with `periodic`.
  - `"make-periodic"` (regular running, non-periodic, non-child) → call `makePeriodicNow` (no dialog; uses prompt defaults + fires first run).
  - `"one-shot"` (already periodic — `periodic_enabled || periodic_configured` — **or** a child) → `seedConversationWithPrompt` once; periodic config untouched. Backend 400s on periodic-for-child too.
- **ChatInput**: `handlePredefinedPrompt` → `onSend("", [], [], { promptName })` — never sends the full prompt text
- **Backend resolution**: name resolved to full text at dispatch via `resolvePromptByName()` in the **target conversation's** workspace context (not at enqueue time); `arguments` substitution (`${VAR}`/`${VAR:-default}`) applied at the same point
- **Title generation**: skipped for named-prompt queue items (prompt name is used as the queue label)
- **Anti-pattern**: Do NOT call `POST /api/sessions/{id}/queue` with a `message` containing the resolved prompt text; send `prompt_name` instead

## MCP Prompt Tools

- `mitto_prompt_list` — List merged prompts (metadata)
- `mitto_prompt_get` — Get full prompt by name
- `mitto_prompt_update` — Create/update workspace-local overrides (`.mitto/prompts/<slug>.prompt.yaml`)

Updates replicate the 5-layer REST API merge. Name slugification via `config.SlugifyPromptName()`.

## Frontend Architecture

Never merge prompts client-side — backend does all merging. Re-fetch on: dropdown open, file watcher, visibility change, 30s interval. Session-scoped CEL filters (e.g., `session.isChild`) require re-fetch on `activeSessionId` change, not just workspace directory change.

## Builtin Prompt Content Conventions

- **Template variables**: Use `@mitto:*` placeholders. See `docs/config/prompts.md#variable-substitution`.
- **No hardcoded servers**: Use `@mitto:available_acp_servers`.
- **Spawn deduplication**: Use `@mitto:mcp_children` to avoid duplicate children.
- **Periodic mode**: Use `@mitto:periodic` / `@mitto:periodic_forced` to branch; scheduled runs use `mitto_ui_notify` only (no blocking UI).
- **Cross-session confirmation**: Propose best plan, confirm via `mitto_ui_options(allow_free_text: true)`, abort on timeout. Single proposal preferred over "3–5 options".

## enabledWhen Filtering

Server-side via `filterPromptsByEnabled()` / `buildPromptEnabledContext()`. Use `enabledWhen` (CEL) exclusively. Full CEL context: see `05-msghooks.md`. Useful functions: `fileExists(".git/config")`, `commandExists("gh")`, `tools.hasPattern("github_*")`.

### Merge Pitfall: `enabledWhen` Lost in Settings Override

`EnabledWhen` has `json:"-"` tag → not serialized. Settings override of a builtin **loses `enabledWhen`**. Fix: merge logic must carry forward `enabledWhen` from lower-priority source.

### Config Save Anti-pattern: Prompt Round-trip

Never round-trip merged prompts back via `POST /api/config` — set `prompts: []` explicitly in save. Backend must filter `req.Prompts` to only keep `Source == PromptSourceSettings`.
