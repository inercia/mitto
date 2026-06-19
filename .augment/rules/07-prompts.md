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

**Removed fields**: `enabledWhenACP` and `enabledWhenMCP` have been fully removed from the codebase. If encountered in old code or docs, replace with equivalent `enabledWhen` CEL expressions. The old `requires:` string field and its frontend counterparts (string-capability gating) are also gone — replaced by the typed `parameters:` system below.

## Typed Parameters & Type-Based Menu Gating

### parameters: field

Prompts may declare typed inputs via a `parameters:` list. Each entry:

```yaml
parameters:
  - name: ISSUE_ID      # variable used as ${ISSUE_ID} in the prompt body
    type: beadsId       # one of the six predefined types
    description: "..."  # optional
    required: true      # optional bool (declarative; body ${VAR:-default} still controls fallback)
```

### Predefined types (canonical registry: `internal/config/prompt_param_types.go`)

Frontend mirror: `KNOWN_PARAM_TYPES` in `web/static/utils/prompts.js`. Both must stay in sync.

| Type | Description |
| ---- | ----------- |
| `beadsId` | Beads issue ID (e.g. `"mitto-42"`). Auto-filled by `beadsIssues` menu. |
| `beadsTitle` | Beads issue title. Auto-filled by `beadsIssues` menu. |
| `sessionId` | Mitto conversation/session UUID. |
| `workspaceId` | Mitto workspace UUID. |
| `workspaceFolder` | Absolute path to a workspace root directory. |
| `text` | Generic free-form text (catch-all). |

### Type-based menu gating

Prompt shown in menu **M** only when M supplies **every** declared type. Frontend: `menuSatisfies(prompt, menu)`. Menu types: `beadsIssues` → `{beadsId, beadsTitle}`; others supply none. See `MENU_PARAM_TYPES` in `web/static/utils/prompts.js` and MCP tools `mitto_prompt_get/list` (include `parameters`).

## Key Types

`WebPrompt`: Name, Prompt, Description, Group, BackgroundColor, Icon, Source ("builtin"|"file"|"settings"|"workspace"), Enabled (*bool: nil=enabled, false=disabled), EnabledWhen (CEL, server-side only), Periodic (non-nil = periodic conversation).

`PromptPeriodic` (YAML `periodic:`): `value`/`unit`/`at` (schedule period), `maxIterations`, plus the on-completion fields `trigger` (`schedule` default | `onCompletion`), `delay` (int seconds for onCompletion; clamped to the global floor), and `maxDuration` (duration string e.g. `4h`; wall-clock cap from the first run). `MaxIterations` caps scheduled runs; effective cap = min(prompt maxIterations, config default 100, hardcoded 1000). Backend auto-disables (not archives) when either the iteration cap or `maxDuration` is hit.

## Merging & Caching

`MergePrompts()` filters disabled; `MergePromptsKeepDisabled()` keeps `enabled:false` for dialogs. PromptsCache auto-refreshes `MITTO_DIR/prompts/` on changes.

## API & Toggle

`GET /api/workspace-prompts?dir=...&session_id=...` (fully merged), `include_global=true` (disabled too), `PUT /api/workspace-prompts/toggle-enabled` (toggle state). Disable: set `enabled: false` in `.mitto/prompts/*.prompt.yaml` or `.mittorc`. Re-enable: remove the `enabled: false` entry.

## Menu-Driven Prompt Sends (Named-Prompt Mechanism)

All menus (prompts, beadsIssues, beadsList) send `prompt_name` only — never the full body. Frontend helpers in `useConversationSeeding.js`: `seedConversationWithPrompt()` (existing session), `startConversationWithPrompt()` (new ± periodic), `makePeriodicNow()` (convert to periodic). Backend resolves name at dispatch via `resolvePromptByName()` in target workspace context; `${VAR}` substitution applied there. **Anti-pattern**: never POST resolved text to `/api/sessions/{id}/queue` — send `prompt_name` instead.

## MCP Prompt Tools

- `mitto_prompt_list` — List merged prompts (metadata)
- `mitto_prompt_get` — Get full prompt by name
- `mitto_prompt_update` — Create/update workspace-local overrides (`.mitto/prompts/<slug>.prompt.yaml`)

Updates replicate the 5-layer REST API merge. Name slugification via `config.SlugifyPromptName()`.

## Frontend & Builtin Conventions

**Frontend**: Never merge client-side — backend does all merging. Refetch on: file changes, visibility change, 30s interval (session-scoped CEL filters like `session.isChild` trigger refetch on activeSessionId change).

**Builtin content**: Use `@mitto:*` placeholders (`@mitto:periodic`, `@mitto:mcp_children`, `@mitto:available_acp_servers`). Cross-session UI: propose best plan, confirm via `mitto_ui_options(allow_free_text: true)`. See `docs/config/prompts.md` for full template reference.

## enabledWhen Filtering & Preferred Models

Server-side via `filterPromptsByEnabled()` / `buildPromptEnabledContext()`. Use `enabledWhen` (CEL) exclusively. Full CEL context: see `05-msghooks.md`. Useful functions: `fileExists(".git/config")`, `commandExists("gh")`, `tools.hasPattern("github_*")`.

### preferredModels Field

Prompts may declare preferred ACP model(s) for auto-selection during session init:

```yaml
preferredModels:
  - name: "Claude"
    matchMode: "contains"  # "contains", "exact", "startsWith", "regex", "lookAlike"
```

Backend calls `selectPreferredModel()` to pick the best matching active model from the session's ACP server. If the active model **already satisfies** the preference, it is kept; otherwise the preference is applied. This enables smart routing of multi-model sessions without forcing model switches when not needed.

### Pitfalls

- `EnabledWhen` has `json:"-"` → settings override of a builtin loses `enabledWhen`. Merge logic must carry forward from lower-priority source.
- Never round-trip merged prompts via `POST /api/config` — set `prompts: []` explicitly. Backend must filter `req.Prompts` to `Source == PromptSourceSettings` only.
