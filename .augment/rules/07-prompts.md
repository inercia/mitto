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

```go
type WebPrompt struct {
    Name            string          `json:"name"`
    Prompt          string          `json:"prompt"`
    Description     string          `json:"description,omitempty"`
    Group           string          `json:"group,omitempty"`
    BackgroundColor string          `json:"backgroundColor,omitempty"`
    Icon            string          `json:"icon,omitempty"`      // name in frontend PROMPT_ICONS registry (Icons.js)
    Source          PromptSource    `json:"source,omitempty"`    // "builtin", "file", "settings", "workspace"
    Enabled         *bool           `json:"enabled,omitempty"`   // nil = enabled, false = disabled
    EnabledWhen     string          `json:"-"`                   // CEL expression (server-side filtering only)
    Periodic        *PromptPeriodic `json:"periodic,omitempty"`  // non-nil = prompt creates a periodic conversation
}

// PromptPeriodic is the periodic: YAML mapping. Presence = opt-in.
type PromptPeriodic struct {
    Value         int    `yaml:"value" json:"value"`                              // number of time units ≥ 1
    Unit          string `yaml:"unit"  json:"unit"`                               // "minutes" | "hours" | "days"
    At            string `yaml:"at,omitempty" json:"at,omitempty"`                // HH:MM UTC; only valid for "days"
    MaxIterations int    `yaml:"maxIterations,omitempty" json:"maxIterations,omitempty"` // 0/absent = unlimited
}
```

**Semantics**: `Value`/`Unit`/`At` are the **default period** applied when a conversation is made periodic (both new-periodic and make-periodic paths). `MaxIterations` caps scheduled runs; the backend auto-stops (disables, not archives) when the **effective cap** is hit — the smallest positive of {prompt `maxIterations`, config `conversations.max_periodic_iterations` (default 100), hardcoded backstop 1000}. See `02-session.md` for the engine-side counting.

## Merging Functions

`MergePrompts(global, settings, workspace)` — filters disabled. `MergePromptsKeepDisabled(...)` — keeps `enabled:false` entries (for WorkspacesDialog `include_global=true`). Higher-priority source overrides lower by name.

## PromptsCache (`internal/config/prompts_cache.go`)

Caches global file prompts from `MITTO_DIR/prompts/` with auto-refresh on directory changes:

```go
cache.GetWebPrompts()                      // All global prompts
cache.GetWebPromptsSpecificToACP("auggie") // Prompts with acps: "auggie"
cache.ForceReload()                        // Clear cache and reload
```

## API Endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /api/workspace-prompts?dir=...&session_id=...` | Fully merged prompt list (single source of truth for menu) |
| `GET /api/workspace-prompts?dir=...&include_global=true` | All prompts including disabled (for WorkspacesDialog toggles) |
| `PUT /api/workspace-prompts/toggle-enabled` | Toggle prompt enabled/disabled state |

### Toggle-Enabled Logic

When disabling prompt X:
1. If `.mitto/prompts/X.prompt.yaml` exists → set `enabled: false` in the file
2. If not → add `{name: X, enabled: false}` to `.mittorc` prompts section

When re-enabling: reverse (remove `enabled: false` from the file or `.mittorc` entry).

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

## MCP Prompt Tools (`internal/mcpserver/prompts.go`)

Three MCP tools for managing prompts programmatically:

| Tool | Purpose |
|------|---------|
| `mitto_prompt_list` | List all merged prompts (metadata only, no text) |
| `mitto_prompt_get` | Get full prompt details by name (case-insensitive) |
| `mitto_prompt_update` | Create/update workspace-local prompt overrides |

`loadMergedPrompts()` replicates the same 5-layer merge as the REST API. Updates always write to `.mitto/prompts/<slug>.prompt.yaml` (workspace-local override). Enable/disable-only updates use the optimized toggle path (`UpdatePromptFileEnabled` / `SaveWorkspaceRCPromptEnabled`). Name slugification via `config.SlugifyPromptName()`.

## Frontend Architecture

**Anti-pattern**: Never do client-side prompt merging — backend does everything. `predefinedPrompts = workspacePrompts` only. Refresh on: dropdown open, file watcher event, visibility change, 30s interval. Supports `If-Modified-Since` / `Last-Modified` for efficient polling.

**Session-switch re-fetch**: CEL expressions referencing `session.*` (e.g., `session.isChild`, `parent.exists`) produce different filtered lists per session. The frontend must re-fetch prompts on every `activeSessionId` change, even within the same workspace — not just on workspace directory change. In `app.js`, a dedicated `useEffect([activeSessionId])` calls `fetchWorkspacePrompts(workingDir, true)` when `workingDir === workspacePromptsDir`.

## Builtin Prompt Content Conventions (`config/prompts/builtin/`)

- **Template variables**: Use `@mitto:*` placeholders (substituted at send time). Full list in `docs/config/prompts.md#variable-substitution` and `internal/processors/variables.go`.
- **No hardcoded ACP server names**: Use `@mitto:available_acp_servers`; never hardcode server names.
- **Generic server selection**: instruct agents to "prefer faster/cheaper for simple tasks, more capable for complex tasks"
- **Spawn deduplication**: Use `@mitto:mcp_children` (auto-substituted list of MCP-created children with titles) to check for existing child conversations before spawning. Avoids extra `mitto_conversation_list` calls. Include spawn caps per run.
- **Periodic mode pattern**: Use `@mitto:periodic` and `@mitto:periodic_forced` to branch behavior. Scheduled runs → `mitto_ui_notify` only (no blocking UI). Force-triggered or interactive → may use `mitto_ui_options`/`mitto_ui_form`.
- **Cross-session delegation must confirm first**: Agent proposes its best plan based on conversation context; user confirms or overrides via `mitto_ui_options(allow_free_text: true, timeout: 120s)`; abort on timeout. Do NOT force "3–5 options" — a single clear proposal is preferred. Do NOT call `mitto_conversation_get_summary` — the agent already has context.

## enabledWhen Filtering

Server-side via `filterPromptsByEnabled()` / `buildPromptEnabledContext()`. Use `enabledWhen` (CEL) exclusively. Full CEL context: see `05-msghooks.md`. Useful functions: `fileExists(".git/config")`, `commandExists("gh")`, `tools.hasPattern("github_*")`.

### Merge Pitfall: `enabledWhen` Lost in Settings Override

`EnabledWhen` has `json:"-"` tag → not serialized. Settings override of a builtin **loses `enabledWhen`**. Fix: merge logic must carry forward `enabledWhen` from lower-priority source.

### Config Save Anti-pattern: Prompt Round-trip

`GET /api/config` returns ALL merged prompts (files + settings). Never round-trip these back via `POST /api/config`:
- **Frontend**: Set `prompts: []` explicitly in save requests. `SettingsDialog` (line 1883) does this correctly. `WorkspacesDialog` spreading `...config` was a bug — it included file-sourced prompts in the save payload.
- **Backend**: `buildNewSettings` must filter `req.Prompts` to only keep `Source == PromptSourceSettings` or empty source. Drop `PromptSourceFile` and `PromptSourceBuiltin` before persisting.
